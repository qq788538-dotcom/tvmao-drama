import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __resetIdentityRecoveryForTest,
  api,
  CanvasOwnerLostError,
  claimCanvasOwner,
  deleteCanvasConnection,
  deleteCanvasNodes,
  fetchCanvasPostprocessConfig,
  getToken,
  heartbeatCanvasOwner,
  login,
  patchCanvasNode,
  patchCanvasNodePositions,
  register,
  setToken,
  submitCanvasDesub,
  upsertCanvasConnection,
  upsertCanvasNode,
} from './api';
import { setApiWritesBlocked } from './apiGuards';
import { queryClient } from './queryClient';

describe('shared/api register', () => {
  beforeEach(() => {
    window.localStorage.clear();
    queryClient.clear();
    vi.restoreAllMocks();
  });

  it('注册成功后覆盖旧用户 me 缓存', async () => {
    queryClient.setQueryData(['me'], {
      id: 1,
      email: 'a@example.com',
      role: 'user',
      balanceCredits: 12,
    });

    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        accessToken: 'token-b',
        user: {
          id: 2,
          email: 'b@example.com',
          role: 'user',
          balanceCredits: 34,
          storageUsedBytes: 256,
          storageQuotaBytes: 2048,
        },
      },
    });

    const result = await register('b@example.com', 'secret123', 'K7M2X9');

    expect(result.user.email).toBe('b@example.com');
    expect(getToken()).toBe('token-b');
    expect(queryClient.getQueryData(['me'])).toMatchObject({
      id: 2,
      email: 'b@example.com',
      balanceCredits: 34,
    });
    expect(api.post).toHaveBeenCalledWith('/auth/register', {
      email: 'b@example.com',
      password: 'secret123',
      inviteCode: 'K7M2X9',
    });
  });
});

describe('shared/api 401 拦截器', () => {
  const realAdapter = api.defaults.adapter;
  const realLocation = window.location;
  let fakeLocation: { pathname: string; href: string };

  beforeEach(() => {
    // 上面的 describe 只在 beforeEach 里 restore，spy 会泄漏到这里；
    // 这几条用例必须打到真实 adapter 才能触发响应 interceptor。
    vi.restoreAllMocks();
    window.localStorage.clear();
    queryClient.clear();
    // 弹框登录发生在首页 / 画布等任意路径上，不是 /login——正是拦截器
    // 判「会话过期 → 强制登出跳转」的那一支。
    fakeLocation = { pathname: '/', href: 'http://localhost/' };
    Object.defineProperty(window, 'location', { value: fakeLocation, configurable: true, writable: true });
  });

  afterEach(() => {
    api.defaults.adapter = realAdapter;
    Object.defineProperty(window, 'location', { value: realLocation, configurable: true, writable: true });
  });

  function reject401(): void {
    api.defaults.adapter = async (config) => {
      throw Object.assign(new Error('Request failed with status code 401'), {
        isAxiosError: true,
        config,
        response: { status: 401, data: { error: '邮箱或密码错误' }, config, headers: {}, statusText: 'Unauthorized' },
      });
    };
  }

  it('登录密码错误的 401 不清会话、不跳转——错误原样抛给表单去提示', async () => {
    reject401();

    const err = await login('a@example.com', 'wrong-pass').catch((e: unknown) => e);

    expect((err as { response?: { data?: { error?: string } } })?.response?.data?.error).toBe('邮箱或密码错误');
    // 没有整页跳转 ⇒ AuthForm 的 message.error 才有机会被看见
    expect(fakeLocation.href).toBe('http://localhost/');
  });

  it('注册失败的 401 同样不跳转', async () => {
    reject401();

    await register('a@example.com', 'pass1234', 'K7M2X9').catch(() => undefined);

    expect(fakeLocation.href).toBe('http://localhost/');
  });

  it('其它端点的 401 仍然清会话并跳登录（会话过期语义不变）', async () => {
    setToken('stale-token');
    queryClient.setQueryData(['me'], { id: 1, email: 'a@example.com', role: 'user' });
    reject401();

    await api.get('/projects').catch(() => undefined);

    expect(getToken()).toBeNull();
    expect(queryClient.getQueryData(['me'])).toBeUndefined();
    expect(fakeLocation.href).toBe('/login');
  });
});

// C1（2026-08-18 终审）：团队被解散 / 自己被移出之后，团队 token 的每一个请求都
// 拿到 401「团队成员资格已失效」。它跟会话过期不是一回事——真人还登录着，失效的
// 只是团队这层身份。旧行为清 session 跳 /login：用户点了「解散团队」，终态是被
// 强制重新登录。新行为是换回个人 token 整页重启。
describe('shared/api 团队身份失效自愈', () => {
  const realAdapter = api.defaults.adapter;
  const realLocation = window.location;
  let fakeLocation: { pathname: string; href: string; assign: ReturnType<typeof vi.fn> };
  let switchCalls: number;

  beforeEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
    queryClient.clear();
    switchCalls = 0;
    // 单飞标志是模块级的、成功后故意不清（浏览器里页面马上就没了）。
    // 测试进程里模块长活，不复位第一个成功用例会堵死后面所有用例。
    __resetIdentityRecoveryForTest();
    fakeLocation = { pathname: '/team/manage', href: 'http://localhost/team/manage', assign: vi.fn() };
    Object.defineProperty(window, 'location', { value: fakeLocation, configurable: true, writable: true });
  });

  afterEach(() => {
    api.defaults.adapter = realAdapter;
    // 只读标记是模块级的：不复位会把只读态漏给后面所有用例（写请求全被拦）。
    setApiWritesBlocked(false);
    Object.defineProperty(window, 'location', { value: realLocation, configurable: true, writable: true });
  });

  /** switchOk=true 时 switch-context 换发成功，false 时它自己也回 401（防循环那一支）。 */
  function deadTeamAdapter(switchOk: boolean): void {
    api.defaults.adapter = async (config) => {
      if (config.url?.startsWith('/auth/switch-context')) {
        switchCalls += 1;
        if (switchOk) {
          return { data: { token: 'personal-token', teamId: 0 }, status: 200, statusText: 'OK', headers: {}, config };
        }
        throw Object.assign(new Error('401'), {
          isAxiosError: true,
          config,
          response: { status: 401, data: { error: '团队成员资格已失效' }, config, headers: {}, statusText: 'Unauthorized' },
        });
      }
      throw Object.assign(new Error('401'), {
        isAxiosError: true,
        config,
        response: { status: 401, data: { error: '团队成员资格已失效' }, config, headers: {}, statusText: 'Unauthorized' },
      });
    };
  }

  const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

  it('「团队成员资格已失效」的 401 换回个人 token 整页重启，不清 session 不跳登录', async () => {
    setToken('dead-team-token');
    deadTeamAdapter(true);

    await api.get('/projects').catch(() => undefined);
    await flush();

    expect(getToken()).toBe('personal-token');
    expect(switchCalls).toBe(1);
    expect(fakeLocation.assign).toHaveBeenCalledWith('/');
    expect(fakeLocation.href).toBe('http://localhost/team/manage'); // 没跳 /login
  });

  // N1（2026-08-18 复审）：身份失效是瞬时全体失效——那一刻飞行中的每个请求都各自
  // 拿到一个 401。不去重的话它们各发一个 switch-context，而该端点跟 login 共享
  // 同一个按 IP 的 authRL 桶（10/min、30/5min）：几个并发就能打穿，自愈自己被 429
  // 拒 → 照样登出，且接下来 5 分钟里重新登录也被同一个桶挡着，比不修更糟。
  // 顺带堵掉「先成功的自愈已 setToken，后到的失败分支把它清掉」那条竞态。
  it('并发 401 只触发一次 switch-context（单飞，别把 authRL 的 IP 桶打穿）', async () => {
    setToken('dead-team-token');
    deadTeamAdapter(true);

    // 六个并发请求同时撞上身份失效——真实场景里 react-query 的 retry 还会再翻一倍。
    await Promise.all(
      ['/projects', '/me', '/auth/contexts', '/drama/projects', '/workflows', '/me/ledger'].map((u) =>
        api.get(u).catch(() => undefined),
      ),
    );
    await flush();

    expect(switchCalls).toBe(1);
    expect(getToken()).toBe('personal-token');
    // 整页重启只发一次——多发几次不致命，但说明单飞漏了。
    expect(fakeLocation.assign).toHaveBeenCalledTimes(1);
  });

  // 跨计划 Major（计划四终审 M1）：团队画布的**观看模式**是只读态
  // （setReadOnly → setApiWritesBlocked），而自愈原本整个包在只读门里面。观看
  // 当中被移出团队 / 团队解散 → 重拉快照的 401 被 catch 吞掉、点接管也只弹通用
  // 报错，画布就冻在那里，用户完全不知道自己已经不在这个团队里了。
  it('只读态（画布观看模式）下的死身份 401 照样自愈，不被只读门短路', async () => {
    setToken('dead-team-token');
    deadTeamAdapter(true);
    setApiWritesBlocked(true);

    await api.get('/projects/14/snapshot').catch(() => undefined);
    await flush();

    expect(switchCalls).toBe(1);
    expect(getToken()).toBe('personal-token');
    expect(fakeLocation.assign).toHaveBeenCalledWith('/');
  });

  // 对偶：只读态下**普通** 401 的既有行为一个字不能变——匿名分享 viewer 里
  // 按用户维度的端点本来就会 401，那不是「会话过期」，不该清 session 跳登录。
  it('只读态下普通 401 仍然不清会话、不跳登录（分享 viewer 零回归）', async () => {
    setToken('anon-viewer-token');
    queryClient.setQueryData(['me'], { id: 1, email: 'a@example.com', role: 'user' });
    api.defaults.adapter = async (config) => {
      throw Object.assign(new Error('401'), {
        isAxiosError: true,
        config,
        response: { status: 401, data: { error: 'unauthorized' }, config, headers: {}, statusText: 'Unauthorized' },
      });
    };
    setApiWritesBlocked(true);

    await api.get('/me/library').catch(() => undefined);
    await flush();

    expect(getToken()).toBe('anon-viewer-token');
    expect(queryClient.getQueryData(['me'])).toBeDefined();
    expect(fakeLocation.href).toBe('http://localhost/team/manage');
    expect(switchCalls).toBe(0);
  });

  it('switch-context 自己回这句 401 时不再自愈（防无限递归），走原来的清会话登出', async () => {
    setToken('dead-team-token');
    queryClient.setQueryData(['me'], { id: 1, email: 'a@example.com', role: 'user' });
    deadTeamAdapter(false);

    await api.post('/auth/switch-context', { teamId: 0 }).catch(() => undefined);
    await flush();

    expect(getToken()).toBeNull();
    expect(queryClient.getQueryData(['me'])).toBeUndefined();
    expect(fakeLocation.href).toBe('/login');
    expect(fakeLocation.assign).not.toHaveBeenCalled();
  });
});

describe('shared/api canvas owner and mutations', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('calls canvas owner and mutation endpoints with exact payloads', async () => {
    const node = { id: 'n1', type: 'image', x: 10 };
    const patch = { data: { prompt: 'new' } };
    const nodes = [{ id: 'n1', x: 1, y: 2, width: 300, height: 200 }];
    const connection = { id: 'e1', from: 'n1', to: 'n2' };

    const postSpy = vi.spyOn(api, 'post')
      .mockResolvedValueOnce({ data: { ownerToken: 'tok', expiresAt: '2026-07-24T12:00:00Z' } })
      .mockResolvedValueOnce({ data: undefined })
      .mockResolvedValueOnce({ data: { version: 1 } })
      .mockResolvedValueOnce({ data: { version: 2 } });
    const patchSpy = vi.spyOn(api, 'patch')
      .mockResolvedValueOnce({ data: { version: 3 } })
      .mockResolvedValueOnce({ data: { version: 4 } });
    const deleteSpy = vi.spyOn(api, 'delete')
      .mockResolvedValueOnce({ data: { version: 5 } })
      .mockResolvedValueOnce({ data: { version: 6 } });

    await claimCanvasOwner(14);
    await heartbeatCanvasOwner(14, 'tok');
    await upsertCanvasNode(14, 'tok', node);
    await patchCanvasNode(14, 'tok', 'n/1 space', patch);
    await deleteCanvasNodes(14, 'tok', ['n1']);
    await patchCanvasNodePositions(14, 'tok', nodes);
    await upsertCanvasConnection(14, 'tok', connection);
    await deleteCanvasConnection(14, 'tok', 'e1');

    expect(postSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/owner/claim', {});
    expect(postSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/owner/heartbeat', { ownerToken: 'tok' });
    expect(postSpy).toHaveBeenNthCalledWith(3, '/projects/14/canvas/nodes', { ownerToken: 'tok', node });
    expect(patchSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes/n%2F1%20space', { ownerToken: 'tok', patch });
    expect(deleteSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes', {
      data: { ownerToken: 'tok', nodeIds: ['n1'] },
    });
    expect(patchSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/nodes/positions', { ownerToken: 'tok', nodes });
    expect(postSpy).toHaveBeenNthCalledWith(4, '/projects/14/canvas/connections', { ownerToken: 'tok', connection });
    expect(deleteSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/connections/e1', {
      data: { ownerToken: 'tok' },
    });
  });

  it('maps CANVAS_OWNER_LOST conflicts to CanvasOwnerLostError', async () => {
    vi.spyOn(api, 'patch').mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'CANVAS_OWNER_LOST', error: 'owner lost' },
      },
    });

    const error = await patchCanvasNode(14, 'tok', 'n1', {}).catch((err: unknown) => err);

    expect(error).toBeInstanceOf(CanvasOwnerLostError);
    expect(error).toMatchObject({
      code: 'CANVAS_OWNER_LOST',
      message: 'owner lost',
    });
  });

  it('maps CANVAS_OWNER_EXPIRED conflicts to CanvasOwnerLostError', async () => {
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'CANVAS_OWNER_EXPIRED', error: 'owner expired' },
      },
    });

    const error = await heartbeatCanvasOwner(14, 'tok').catch((err: unknown) => err);

    expect(error).toBeInstanceOf(CanvasOwnerLostError);
    expect(error).toMatchObject({
      code: 'CANVAS_OWNER_EXPIRED',
      message: 'owner expired',
    });
  });

  it('rethrows 409 conflicts with other codes unchanged', async () => {
    const originalError = {
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'SNAPSHOT_VERSION_CONFLICT', error: 'conflict' },
      },
    };
    vi.spyOn(api, 'delete').mockRejectedValue(originalError);

    await expect(deleteCanvasNodes(14, 'tok', ['n1'])).rejects.toBe(originalError);
  });

  it('rethrows non-409 errors unchanged', async () => {
    const originalError = {
      isAxiosError: true,
      response: {
        status: 500,
        data: { error: 'server error' },
      },
    };
    vi.spyOn(api, 'delete').mockRejectedValue(originalError);

    await expect(deleteCanvasConnection(14, 'tok', 'e1')).rejects.toBe(originalError);
  });
});

describe('shared/api canvas postprocess (去字幕)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('fetchCanvasPostprocessConfig 调 GET /canvas/postprocess-config 并透传返回', async () => {
    const getSpy = vi.spyOn(api, 'get').mockResolvedValueOnce({
      data: {
        subtitleRemoval: [{ modelId: 'm', label: 'L', subtitle: 's', isTimeLimited: false }],
      },
    });

    const cfg = await fetchCanvasPostprocessConfig();

    expect(getSpy).toHaveBeenCalledWith('/canvas/postprocess-config');
    expect(cfg.subtitleRemoval).toHaveLength(1);
    expect(cfg.subtitleRemoval[0]).toMatchObject({ modelId: 'm', label: 'L', subtitle: 's', isTimeLimited: false });
  });

  it('submitCanvasDesub 调 POST /canvas-tasks/desub 带完整 payload 并透传返回', async () => {
    const postSpy = vi.spyOn(api, 'post').mockResolvedValueOnce({ data: { taskId: 7, status: 'processing' } });

    const result = await submitCanvasDesub({ projectId: 1, nodeId: 'n2', sourceNodeId: 'n1', modelId: 'm' });

    expect(postSpy).toHaveBeenCalledWith('/canvas-tasks/desub', {
      projectId: 1,
      nodeId: 'n2',
      sourceNodeId: 'n1',
      modelId: 'm',
    });
    expect(result).toEqual({ taskId: 7, status: 'processing' });
  });
});

// 登录态 key 从 tapnow.* 改名到 tvmao.*。搬迁是为了让改名上线时在线用户不掉线，
// 但搬迁本身有个陷阱：登出只清新 key 的话，下次读又把旧 key 搬回来，用户退不掉。
describe('shared/api 登录态 key 改名搬迁', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('读到旧 key 时搬到新 key 并删掉旧的', () => {
    window.localStorage.setItem('tapnow.auth.token', 'legacy-token');

    expect(getToken()).toBe('legacy-token');
    expect(window.localStorage.getItem('tvmao.auth.token')).toBe('legacy-token');
    expect(window.localStorage.getItem('tapnow.auth.token')).toBeNull();
  });

  it('新 key 存在时不被旧 key 覆盖', () => {
    window.localStorage.setItem('tvmao.auth.token', 'current');
    window.localStorage.setItem('tapnow.auth.token', 'stale');

    expect(getToken()).toBe('current');
  });

  it('登出同时清掉旧 key，否则刷新后又变回登录态', () => {
    window.localStorage.setItem('tapnow.auth.token', 'legacy-token');

    setToken(null);

    expect(getToken()).toBeNull();
  });
});
