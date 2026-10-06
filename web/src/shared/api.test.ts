import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { InternalAxiosRequestConfig } from 'axios';
import {
  api,
  CanvasOwnerLostError,
  deleteCanvasConnection,
  deleteCanvasNodes,
  fetchCanvasPostprocessConfig,
  getProject,
  patchCanvasNode,
  patchCanvasNodePositions,
  submitCanvasDesub,
  upsertCanvasConnection,
  upsertCanvasNode,
} from './api';
import { setApiWritesBlocked } from './apiGuards';
import { resetSidecarSessionForTest, setSidecarSession } from './sidecarSession';

// 桌面端：请求一律发往本次启动的 sidecar，带 Bearer token；没有登录态，401 不跳转。
describe('shared/api 接 sidecar', () => {
  const realAdapter = api.defaults.adapter;
  let seen: InternalAxiosRequestConfig[] = [];
  let status = 200;

  beforeEach(() => {
    vi.restoreAllMocks();
    seen = [];
    status = 200;
    api.defaults.adapter = async (config) => {
      seen.push(config);
      if (status >= 400) {
        return Promise.reject(
          Object.assign(new Error(`Request failed with status code ${status}`), {
            isAxiosError: true,
            config,
            response: { status, statusText: '', headers: {}, config, data: { error: 'x' } },
          }),
        );
      }
      return { data: { id: 3, name: 'p' }, status, statusText: 'OK', headers: {}, config };
    };
  });

  afterEach(() => {
    api.defaults.adapter = realAdapter;
    resetSidecarSessionForTest();
    setApiWritesBlocked(false);
  });

  it('baseURL 指向 127.0.0.1:<端口>/api/v1，并带 Authorization: Bearer <token>', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });

    const project = await getProject(3);

    expect(project).toEqual({ id: 3, name: 'p' });
    expect(seen).toHaveLength(1);
    expect(seen[0].baseURL).toBe('http://127.0.0.1:50123/api/v1');
    expect(seen[0].url).toBe('/projects/3');
    expect(seen[0].headers.Authorization).toBe('Bearer tok-1');
  });

  it('sidecar 换了端口和 token（重新建会话）后，下一个请求立刻用新的', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });
    await getProject(3);
    setSidecarSession({ port: 50999, token: 'tok-2' });
    await getProject(3);

    expect(seen[1].baseURL).toBe('http://127.0.0.1:50999/api/v1');
    expect(seen[1].headers.Authorization).toBe('Bearer tok-2');
  });

  it('401 原样抛给调用方，不跳登录页', async () => {
    setSidecarSession({ port: 50123, token: 'stale' });
    status = 401;
    const before = window.location.href;

    const err = await getProject(3).catch((e: unknown) => e);

    expect((err as { response?: { status?: number } }).response?.status).toBe(401);
    expect(window.location.href).toBe(before);
  });

  it('只读模式下写请求被本地挡下，不发到 sidecar', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });
    setApiWritesBlocked(true);

    const err = await upsertCanvasNode(14, 'local', { id: 'n1' }).catch((e: unknown) => e);

    expect((err as { code?: string }).code).toBe('READ_ONLY');
    expect(seen).toHaveLength(0);
  });
});

describe('shared/api canvas mutations', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('calls canvas mutation endpoints with exact payloads', async () => {
    const node = { id: 'n1', type: 'image', x: 10 };
    const patch = { data: { prompt: 'new' } };
    const nodes = [{ id: 'n1', x: 1, y: 2, width: 300, height: 200 }];
    const connection = { id: 'e1', from: 'n1', to: 'n2' };

    const postSpy = vi.spyOn(api, 'post')
      .mockResolvedValueOnce({ data: { version: 1 } })
      .mockResolvedValueOnce({ data: { version: 2 } });
    const patchSpy = vi.spyOn(api, 'patch')
      .mockResolvedValueOnce({ data: { version: 3 } })
      .mockResolvedValueOnce({ data: { version: 4 } });
    const deleteSpy = vi.spyOn(api, 'delete')
      .mockResolvedValueOnce({ data: { version: 5 } })
      .mockResolvedValueOnce({ data: { version: 6 } });

    await upsertCanvasNode(14, 'tok', node);
    await patchCanvasNode(14, 'tok', 'n/1 space', patch);
    await deleteCanvasNodes(14, 'tok', ['n1']);
    await patchCanvasNodePositions(14, 'tok', nodes);
    await upsertCanvasConnection(14, 'tok', connection);
    await deleteCanvasConnection(14, 'tok', 'e1');

    expect(postSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes', { ownerToken: 'tok', node });
    expect(patchSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes/n%2F1%20space', { ownerToken: 'tok', patch });
    expect(deleteSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes', {
      data: { ownerToken: 'tok', nodeIds: ['n1'] },
    });
    expect(patchSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/nodes/positions', { ownerToken: 'tok', nodes });
    expect(postSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/connections', { ownerToken: 'tok', connection });
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

    const error = await upsertCanvasConnection(14, 'tok', { id: 'e1', from: 'n1', to: 'n2' }).catch((err: unknown) => err);

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
