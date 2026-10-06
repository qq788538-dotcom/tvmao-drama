import axios, { AxiosInstance, AxiosError } from 'axios';
import { isApiInReadOnlyMode, isApiWriteAllowed } from './apiGuards';
import { queryClient } from './queryClient';
// 循环 import（identity.ts 反过来要 api.ts 的 setToken/switchContext）是刻意的：
// 两侧的顶层都只有 const 字面量与函数声明，没有任何"import 完立刻用对方绑定"的
// 代码，ESM 的活绑定能正确解析。把三步收尾逻辑复制一份到这里才是真正的坑
//（将来加第四步只会加在其中一处）。
import { recoverToPersonalIdentity } from './identity';

const TOKEN_KEY = 'tvmao.auth.token';
// 改名前的 key。首次读到就搬到新 key——不搬的话，改名上线的那一刻全站在线用户
// 一起掉登录态，而他们什么都没做。等线上不再有旧 key（几个月后）可以删掉。
const LEGACY_TOKEN_KEY = 'tapnow.auth.token';

// localStorage 在某些环境会缺失或抛错（Safari 隐私模式访问即抛 SecurityError、
// SSR、测试里的残缺 jsdom shim……）。读写都包 try/catch + 方法存在性检查，失败时
// 当作「无 token / 无法持久化」，绝不让一个存储异常掀翻整个渲染。
export function getToken(): string | null {
  try {
    if (typeof localStorage === 'undefined' || typeof localStorage.getItem !== 'function') return null;
    const current = localStorage.getItem(TOKEN_KEY);
    if (current) return current;
    const legacy = localStorage.getItem(LEGACY_TOKEN_KEY);
    if (legacy) {
      localStorage.setItem(TOKEN_KEY, legacy);
      localStorage.removeItem(LEGACY_TOKEN_KEY);
    }
    return legacy;
  } catch {
    return null;
  }
}

export function setToken(token: string | null): void {
  try {
    if (typeof localStorage === 'undefined' || typeof localStorage.setItem !== 'function') return;
    if (!token) {
      localStorage.removeItem(TOKEN_KEY);
      // 旧 key 也要清：只清新的话，登出后 getToken 会把旧 key 的 token 搬回来，
      // 用户点了退出却还是登录态。
      localStorage.removeItem(LEGACY_TOKEN_KEY);
    } else {
      localStorage.setItem(TOKEN_KEY, token);
    }
  } catch {
    /* 存储不可用——静默忽略 */
  }
}

export function clearAuthSession(): void {
  setToken(null);
  queryClient.clear();
}

export const api: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  headers: { 'Content-Type': 'application/json' },
});

api.interceptors.request.use((config) => {
  const token = getToken();
  if (token && config.headers) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  if (!isApiWriteAllowed(config.method, config.url)) {
    // 直接硬失败，让调用方节点的 try/catch 抛出错误提示，而不是默默
    // 吞掉这次点击。viewer 是只读模式——那些本会改动状态的按钮会得到
    // 类似 409 的行为。
    return Promise.reject(
      Object.assign(new Error('画布处于只读模式'), { code: 'READ_ONLY' }),
    );
  }
  return config;
});

// 认证端点自己返回的 401 语义是「这次提交的凭据不对」，不是「会话过期」。
// 调用它们时本来就没有会话可失效——把它们卷进强制登出跳转，只会让整页
// 导航抢在错误提示前面，用户看到「闪一下什么都没有」，不知道是密码错了。
function isAuthEndpoint(url: string | undefined): boolean {
  return !!url && (url.startsWith('/auth/login') || url.startsWith('/auth/register'));
}

// 团队被解散 / 自己被移出之后，团队 token 的**每一个**请求都会拿到这句 401
//（后端 middleware.go requireTeam 的固定文案）。它跟「会话过期」不是一回事：
// 真人的登录态完全有效，失效的只是团队这一层身份。当成会话过期清 session
// 跳登录页，等于因为团队解散就把人踢下线（2026-08-18 终审 C1）。
const DEAD_TEAM_IDENTITY_MSG = '团队成员资格已失效';

function isDeadTeamIdentity(error: AxiosError): boolean {
  const data = error.response?.data;
  if (typeof data === 'string') return data.includes(DEAD_TEAM_IDENTITY_MSG);
  const msg = (data as { error?: unknown } | undefined)?.error;
  return typeof msg === 'string' && msg.includes(DEAD_TEAM_IDENTITY_MSG);
}

// 防循环守卫：自愈动作本身就是发一个 switch-context: 如果**它**回 401，
// 再去自愈就是无限递归。这种情况说明真人那一层的凭据也不行了，走原来的
// 清 session 路径。
function isSwitchContextEndpoint(url: string | undefined): boolean {
  return !!url && url.startsWith('/auth/switch-context');
}

// 自愈单飞（2026-08-18 复审 N1）。身份失效是**瞬时全体失效**：那一刻页面上飞行
// 中的每个请求都会各自拿到一个 401，叠加 react-query 的 retry 就是 N×2 个。
// 每个都发一次 switch-context 的话——那个端点跟 login 共享同一个按 IP 的 authRL
// 桶（10/min、30/5min）——几个并发请求就能把桶打穿：自愈自己被 429 拒 → 回落
// 登出，而且接下来 5 分钟里用户重新登录也被同一个桶挡着，这个角落比不修更糟。
// 另一半是竞态：先成功的自愈已经 setToken 了，后到的失败分支会把它清掉。
//
// 成功后**不清标志**：location.assign 已经发出，页面正在整页重启，此时清掉只会
// 让卸载前到达的 401 再打一发。只有失败才清，让后续 401 还能重试。
let identityRecovery: Promise<boolean> | null = null;

function recoverDeadTeamIdentityOnce(): void {
  if (identityRecovery) return; // 已有自愈在跑：这次 401 直接吞掉，等整页重启
  identityRecovery = recoverToPersonalIdentity();
  void identityRecovery.then((ok) => {
    if (!ok) identityRecovery = null;
  });
}

/**
 * 仅供测试复位模块级单飞标志。成功路径**故意不清**它（见上），在浏览器里那是
 * 对的——页面马上就没了；但在测试进程里模块是长活的，不复位的话第一个成功用例
 * 会把后面所有用例的自愈全堵死。
 */
export function __resetIdentityRecoveryForTest(): void {
  identityRecovery = null;
}

api.interceptors.response.use(
  (resp) => resp,
  (error: AxiosError) => {
    if (error.response?.status === 401 && !isAuthEndpoint(error.config?.url)) {
      // 身份自愈这一支必须排在只读门**之前**。只读态不只有匿名的分享 viewer，
      // 还有团队画布的观看模式（setReadOnly → setApiWritesBlocked）——观看当中
      // 被移出团队 / 团队被解散时，如果自愈也被只读门短路，画布就冻在那里：
      // 重拉快照的 401 被 catch 吞掉、点「接管编辑」也只弹一句通用报错，用户
      // 完全不知道自己已经不在这个团队里了。
      //
      // 匿名 viewer 不会误触发：这句固定文案只由后端 requireTeam 中间件产出，
      // 而它只可能命中带团队 JWT 的请求；匿名请求拿到的 401 走不到这里。
      if (isDeadTeamIdentity(error) && !isSwitchContextEndpoint(error.config?.url)) {
        // 身份自愈：换回个人 token 并整页重启，不清 session、不跳登录页。
        // 单飞——并发 401 只会触发一次换发，见上面 identityRecovery 的注释。
        recoverDeadTeamIdentityOnce();
      } else if (!isApiInReadOnlyMode()) {
        // 在分享 viewer 里用户可能本来就是匿名的——任何按用户维度的端点
        // 返回 401 都不代表「会话过期」，只是「这个端点在这里访问不到」。
        // 此时跳过强制登出跳转；调用方的 promise 会 reject，由 UI 自己
        // 处理（通常是空列表 / 静默忽略）。
        clearAuthSession();
        if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
          window.location.href = '/login';
        }
      }
    }
    return Promise.reject(error);
  },
);

// ── 项目管理(drama) 调试日志 ──────────────────────────────────────
// 仅对 /drama/ 路径打印：请求(方法+完整URL+params+body) 与 响应(status+data)，
// body/响应都以对象输出(devtools 可展开成 JSON 查询)。失败也打印。
// 方便线上调试与排错；作用域限 drama，不影响其它模块的控制台。
function isDramaUrl(url?: string): boolean {
  return !!url && url.includes('/drama/');
}
function fullUrl(baseURL?: string, url?: string): string {
  return `${baseURL ?? ''}${url ?? ''}`;
}
api.interceptors.request.use((config) => {
  if (isDramaUrl(config.url)) {
    let body: unknown = config.data;
    if (config.data instanceof FormData) body = '[FormData]';
    else if (typeof config.data === 'string') {
      try {
        body = JSON.parse(config.data);
      } catch {
        /* 非 JSON 串，原样 */
      }
    }
    console.log(
      `%c[drama →] ${(config.method ?? 'get').toUpperCase()} ${fullUrl(config.baseURL, config.url)}`,
      'color:#a855f7;font-weight:bold',
      { params: config.params, body },
    );
  }
  return config;
});
api.interceptors.response.use(
  (resp) => {
    if (isDramaUrl(resp.config.url)) {
      console.log(
        `%c[drama ←] ${(resp.config.method ?? 'get').toUpperCase()} ${fullUrl(resp.config.baseURL, resp.config.url)} ${resp.status}`,
        'color:#10b981;font-weight:bold',
        resp.data,
      );
    }
    return resp;
  },
  (error: AxiosError) => {
    if (isDramaUrl(error.config?.url)) {
      console.log(
        `%c[drama ✗] ${(error.config?.method ?? 'get').toUpperCase()} ${fullUrl(error.config?.baseURL, error.config?.url)} ${error.response?.status ?? ''}`,
        'color:#ef4444;font-weight:bold',
        error.response?.data ?? error.message,
      );
    }
    return Promise.reject(error);
  },
);

export interface AuthUser {
  id: number;
  email: string;
  role: string;
  balanceCredits?: number;
  storageQuotaBytes?: number;
  storageUsedBytes?: number;
}

export interface LoginResult {
  accessToken: string;
  user: AuthUser;
}

export function syncAuthUser(user: AuthUser): void {
  queryClient.setQueryData(['me'], user);
  void queryClient.invalidateQueries({ queryKey: ['me'] });
}

export function applyAuthSession(accessToken: string, user: AuthUser): void {
  setToken(accessToken);
  syncAuthUser(user);
}

export async function login(email: string, password: string): Promise<LoginResult> {
  const { data } = await api.post<LoginResult>('/auth/login', { email, password });
  applyAuthSession(data.accessToken, data.user);
  return data;
}

export async function register(
  email: string,
  password: string,
  inviteCode: string,
): Promise<LoginResult> {
  const { data } = await api.post<LoginResult>('/auth/register', { email, password, inviteCode });
  applyAuthSession(data.accessToken, data.user);
  return data;
}

export async function me(): Promise<AuthUser> {
  const { data } = await api.get<AuthUser>('/me');
  return data;
}

export async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
  await api.put('/me/password', { oldPassword, newPassword });
}

export interface Project {
  id: number;
  userId: number;
  name: string;
  description: string;
  createdAt: string;
  updatedAt: string;
  storageBytes?: number;
  /** 项目封面：最早一次成功生成图的 768w 缩略签名 URL；没生成过图则缺省。 */
  coverUrl?: string;
}

export async function getProject(id: number): Promise<Project> {
  const { data } = await api.get<Project>(`/projects/${id}`);
  return data;
}

export async function listProjects(): Promise<Project[]> {
  const { data } = await api.get<{ data: Project[] }>('/projects');
  return data.data ?? [];
}

/**
 * 分页版列表，供 /canvas 的无限滚动。不带 limit 的 listProjects 保持全量语义
 * （首页「个人最近项目」区还在用它，本次不动）。
 */
export async function listProjectsPage(
  limit: number,
  offset: number,
): Promise<{ items: Project[]; total: number }> {
  const { data } = await api.get<{ data: Project[]; total: number }>('/projects', {
    params: { limit, offset },
  });
  return { items: data.data ?? [], total: data.total ?? 0 };
}

export interface ProjectImage {
  id: number;
  url: string;
  mimeType: string;
  createdAt: string;
}

/** 项目内的图片资产，供「修改封面 → 从项目中选择」。url 是签名缩略图。 */
export async function listProjectImages(projectId: number): Promise<ProjectImage[]> {
  const { data } = await api.get<{ data: ProjectImage[] }>(`/projects/${projectId}/images`);
  return data.data ?? [];
}

/**
 * 设封面。传 assetId 而不是 URL——前端手里的是短时签名 URL，存进库过几小时就是
 * 死链；服务端从 asset 行取 canonical 形态落库。assetId=0 清除（眼下 UI 不给
 * 清除入口，后端能力留着）。
 */
export async function setProjectCover(projectId: number, assetId: number): Promise<void> {
  await api.put(`/projects/${projectId}/cover`, { assetId });
}

/** 创建副本。真复制不是引用，会实占等量存储配额。返回新项目 id。 */
export async function duplicateProject(projectId: number): Promise<number> {
  const { data } = await api.post<{ projectId: number }>(`/projects/${projectId}/duplicate`);
  return data.projectId;
}

export async function createProject(name: string, description = ''): Promise<Project> {
  const { data } = await api.post<Project>('/projects', { name, description });
  return data;
}

export async function updateProject(id: number, body: { name: string; description?: string }): Promise<Project> {
  const { data } = await api.put<Project>(`/projects/${id}`, { name: body.name, description: body.description ?? '' });
  return data;
}

export async function deleteProject(id: number): Promise<void> {
  await api.delete(`/projects/${id}`);
}

export interface Snapshot {
  payload: unknown;
  syncedAt: string | null;
  version: number;
}

export interface CanvasOwnerClaim {
  ownerToken: string;
  expiresAt: string;
}

export interface CanvasMutationResponse {
  version: number;
}

export interface CanvasNodeGeometryPatch {
  id: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
}

export interface CanvasMutationNode {
  id: string;
  [key: string]: unknown;
}

export interface CanvasMutationConnection {
  id: string;
  from: string;
  to: string;
  inputType?: string;
  [key: string]: unknown;
}

type CanvasOwnerLostCode = 'CANVAS_OWNER_LOST' | 'CANVAS_OWNER_EXPIRED';

export class CanvasOwnerLostError extends Error {
  code: CanvasOwnerLostCode;

  constructor(message: string, code: CanvasOwnerLostCode) {
    super(message);
    this.name = 'CanvasOwnerLostError';
    this.code = code;
  }
}

export function isCanvasOwnerLostError(error: unknown): error is CanvasOwnerLostError {
  return error instanceof CanvasOwnerLostError;
}

function throwCanvasOwnerLostError(error: unknown): never {
  if (axios.isAxiosError(error) && error.response?.status === 409) {
    const body = error.response.data as {
      error?: string;
      code?: string;
    } | undefined;
    if (body?.code === 'CANVAS_OWNER_LOST' || body?.code === 'CANVAS_OWNER_EXPIRED') {
      throw new CanvasOwnerLostError(
        body.error ?? '画布已在其他页面继续编辑，请刷新',
        body.code,
      );
    }
  }
  throw error;
}

export async function getSnapshot(projectID: number): Promise<Snapshot> {
  const { data } = await api.get<Snapshot>(`/projects/${projectID}/snapshot`);
  return data;
}

// 整快照保存（PUT /projects/{id}/snapshot + 乐观锁冲突处理）已随「局部 mutation」
// 模型下线：画布的每次改动都走 nodes/connections 的局部端点，没有任何调用方再整份
// 覆盖快照。后端那半（路由 + SaveWithOptimisticLock + SnapshotVersionConflictError）
// 也一并删了，所以这条路径现在**两侧都不存在**，别再照旧代码把它加回来。

export async function claimCanvasOwner(projectID: number): Promise<CanvasOwnerClaim> {
  const { data } = await api.post<CanvasOwnerClaim>(`/projects/${projectID}/canvas/owner/claim`, {});
  return data;
}

export async function heartbeatCanvasOwner(projectID: number, ownerToken: string): Promise<void> {
  try {
    await api.post(`/projects/${projectID}/canvas/owner/heartbeat`, { ownerToken });
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

/**
 * 画布编辑锁的**只读**探测结果（GET /projects/{id}/canvas-owner/probe）。
 * 字段名以 apps/api/internal/http/canvas_mutation_handlers.go 的
 * canvasOwnerProbeResponse json tag 为准。
 *
 * - `held=false` 时其余字段恒为零值。
 * - `isSelf` 比的是**真人**（团队身份下 userID 是全团队共用的影子账号，
 *   只比它的话同团队所有人都会被判成自己人）。
 * - `staleSeconds` = 距持锁者最后一次心跳的秒数，`-1` = 未知（PTTL 读不出）。
 *   心跳周期 10s，锁 TTL 24h 且关页面不释放——光看 `held` 会把全团队顶在
 *   只读里一整天，所以必须用它判断「对方其实早就走了」。
 */
export interface CanvasOwnerProbe {
  held: boolean;
  editorActorId: number;
  isSelf: boolean;
  staleSeconds: number;
}

/**
 * 探测画布当前有没有人在编辑。**只读**：不写不续期，跟 claim（无条件抢占）
 * 是两回事——团队成员打开一个别人正在编辑的项目时必须先看后决定，拿 claim
 * 探测等于每次打开都把在编辑的同事踢下线。
 */
export async function probeCanvasOwner(projectID: number): Promise<CanvasOwnerProbe> {
  const { data } = await api.get<CanvasOwnerProbe>(`/projects/${projectID}/canvas-owner/probe`);
  return data;
}

export async function upsertCanvasNode(
  projectID: number,
  ownerToken: string,
  node: CanvasMutationNode,
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.post<CanvasMutationResponse>(`/projects/${projectID}/canvas/nodes`, {
      ownerToken,
      node,
    });
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export async function patchCanvasNode(
  projectID: number,
  ownerToken: string,
  nodeID: string,
  patch: Record<string, unknown>,
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.patch<CanvasMutationResponse>(
      `/projects/${projectID}/canvas/nodes/${encodeURIComponent(nodeID)}`,
      {
        ownerToken,
        patch,
      },
    );
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export async function deleteCanvasNodes(
  projectID: number,
  ownerToken: string,
  nodeIds: string[],
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.delete<CanvasMutationResponse>(`/projects/${projectID}/canvas/nodes`, {
      data: { ownerToken, nodeIds },
    });
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export async function patchCanvasNodePositions(
  projectID: number,
  ownerToken: string,
  nodes: CanvasNodeGeometryPatch[],
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.patch<CanvasMutationResponse>(`/projects/${projectID}/canvas/nodes/positions`, {
      ownerToken,
      nodes,
    });
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export async function upsertCanvasConnection(
  projectID: number,
  ownerToken: string,
  connection: CanvasMutationConnection,
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.post<CanvasMutationResponse>(`/projects/${projectID}/canvas/connections`, {
      ownerToken,
      connection,
    });
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export async function deleteCanvasConnection(
  projectID: number,
  ownerToken: string,
  connectionID: string,
): Promise<CanvasMutationResponse> {
  try {
    const { data } = await api.delete<CanvasMutationResponse>(
      `/projects/${projectID}/canvas/connections/${encodeURIComponent(connectionID)}`,
      { data: { ownerToken } },
    );
    return data;
  } catch (error) {
    throwCanvasOwnerLostError(error);
  }
}

export interface UploadedAsset {
  id: number;
  url: string;
  size: number;
  mimeType: string;
}

// 从 URL 路径里提取当前项目 id。不在 /canvas/:id 路由内时（比如在
// 项目列表页）返回 undefined。
export function currentProjectId(): number | undefined {
  if (typeof window === 'undefined') return undefined;
  const m = window.location.pathname.split('/canvas/')[1];
  const n = Number(m?.split('/')[0]);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

export async function uploadAsset(file: File, projectId?: number): Promise<UploadedAsset> {
  const form = new FormData();
  form.append('file', file);
  // 把项目 id 作为查询串带上，这样这条素材记录之后能被
  // DELETE /projects/{id} 一并清理（存储空间也会回收）。
  const url = projectId ? `/assets?projectId=${projectId}` : '/assets';
  const { data } = await api.post<UploadedAsset>(url, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  });
  return data;
}

// ── 3D 导演台·用户级云端模型库（跨项目「我的模型」）────────────────────────
// 与项目级 uploadAsset 是两条线：模型要跨项目复用，不能挂 project_id——否则
// 删项目时 ProjectCleaner 会把文件收走，别的项目引用就断了。上限（默认 30，
// settings 可调）与 30MB 校验都在服务端，前端只翻译错误文案。

export interface DirectorUserModelRecord {
  id: number;
  name: string;
  fileName: string;
  url: string;
  sizeBytes: number;
  createdAt?: string;
}

export async function listDirectorModels(): Promise<DirectorUserModelRecord[]> {
  const { data } = await api.get<{ items: DirectorUserModelRecord[] }>('/director/models');
  return data.items ?? [];
}

export async function uploadDirectorModel(file: File): Promise<DirectorUserModelRecord> {
  const form = new FormData();
  form.append('file', file);
  const { data } = await api.post<DirectorUserModelRecord>('/director/models', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  });
  return data;
}

export interface DirectorLibraryModel {
  id: number;
  name: string;
  fileName: string;
  url: string;
  thumbUrl: string;
}

export interface DirectorLibraryCategory {
  id: number;
  name: string;
  models: DirectorLibraryModel[];
}

// 平台预置模型目录（管理员后台配置）。只回启用中的分类与模型，thumbUrl 可为空串。
export async function listDirectorLibrary(): Promise<DirectorLibraryCategory[]> {
  const { data } = await api.get<{ categories: DirectorLibraryCategory[] }>('/director/library');
  return data.categories ?? [];
}

export type DeleteDirectorModelResult =
  | { deleted: true }
  | { deleted: false; referencedProjects: number };

// 409 是「有项目引用，需要确认」的正常业务分支而不是异常，所以 validateStatus
// 放行它，让调用方走确认弹窗后再带 force 重试。
export async function deleteDirectorModel(id: number, force = false): Promise<DeleteDirectorModelResult> {
  const resp = await api.delete(`/director/models/${id}${force ? '?force=1' : ''}`, {
    validateStatus: (s) => s === 204 || s === 409,
  });
  if (resp.status === 204) return { deleted: true };
  const referenced = Number((resp.data as { referencedProjects?: unknown })?.referencedProjects ?? 0);
  return { deleted: false, referencedProjects: referenced };
}

export interface UISchemaField {
  label?: string;
  options: Array<{ value: string | number; label: string }>;
  default?: string | number;
}

export interface ModelUISchema {
  ratio?: UISchemaField;
  resolution?: UISchemaField;
  duration?: UISchemaField;
  n?: UISchemaField;
  [key: string]: UISchemaField | undefined;
}

export interface ModelInputLimit {
  min?: number;
  max?: number;
}

// gen_control.inputLimits：按媒体类型限制生成节点的上游连线数量。
// 缺失 = 不限；max 为 0 = 禁止连接该类型。与后端 models.InputLimit 语义一致。
export interface ModelInputLimits {
  video?: ModelInputLimit;
  image?: ModelInputLimit;
  audio?: ModelInputLimit;
}

export interface ModelGenControl {
  inputLimits?: ModelInputLimits;
}

export interface ModelConfig {
  id: number;
  type: 'text' | 'image' | 'video' | 'audio';
  name: string;
  modelId: string;
  basePath: string;
  // 按上游响应实际用量结算的模型的单价说明（如「100积分/分钟」）。这类模型
  // estimate 给的是预冻结上限而非实价——非空时把「预计 X 积分」改成「预扣 X 积分 · 文案，按实际结算」。
  priceNote?: string;
  enabled: boolean;
  hidden: boolean;
  sortOrder: number;
  requestTemplate: Record<string, unknown>;
  uiSchema?: ModelUISchema | null;
  // /models 直出 gen_control 全量 JSON；前端只关心 inputLimits，其余键不建型。
  genControl?: ModelGenControl | null;
}

export async function listRuntimeModels(): Promise<ModelConfig[]> {
  const { data } = await api.get<{ data: ModelConfig[] }>('/models');
  return data.data ?? [];
}

// estimateCost 预计扣费：后端按模型计费配置(billingConfig)对这组 inputs 算预冻结积分，
// 给「生成」按钮显示「预计消耗 X 积分」。纯定价计算、不冻结；传与提交一致的 inputs 即与实扣对齐。
// 旧的 defaultCostCents 标量字段已随后端 0027 迁移删除，统一走此接口。
export async function estimateCost(modelId: string, inputs: Record<string, unknown>): Promise<number> {
  const { data } = await api.post<{ credits: number }>('/models/estimate', { modelId, inputs });
  return data.credits ?? 0;
}

// estimateCostBatch 批量估算并求和：每条可不同模型/inputs（如分镜各自选了不同视频模型）。
// 一次请求拿总价 + 每条明细，用于「提交至下一步」这种把多条加总到一个按钮的场景。
export interface EstimateItem {
  modelId: string;
  inputs: Record<string, unknown>;
}
export async function estimateCostBatch(items: EstimateItem[]): Promise<{ total: number; items: number[] }> {
  const { data } = await api.post<{ total: number; items: number[] }>('/models/estimate-batch', { items });
  return { total: data.total ?? 0, items: data.items ?? [] };
}

// ---------- 自由画布 AI 生成（方案 B：恒异步 + 每模型并发 + 后端权威回写） --------
// 画布节点走 /canvas-tasks:入队即返回画布任务 id,前端轮询状态用于「显示」;
// 结果由后端权威写回快照节点(关页面/发布也不丢),前端只负责即时展示。

export interface SubmitCanvasTaskInput {
  projectId: number;
  nodeId: string;
  modelId: string;
  inputs: Record<string, unknown>;
  // 提示词预设的 key（gen-image 专用，选填）。前端只传 key——预设的 prompt 正文
  // 对前端保密，key → 正文的替换与拼接全部在服务端执行层完成。
  presetKey?: string;
  // 预设参数选择（维度 key → 选项 key，随 presetKey 一起提交）。只需带用户显式
  // 选过的维度——缺失/非法的维度服务端自动落该维度的 default，前端不用补全。
  presetParams?: Record<string, string>;
}

export interface CanvasTaskStatus {
  status: 'processing' | 'succeeded' | 'failed';
  outputUrls?: string[];
  assetIds?: number[]; // 与 outputUrls 同序;节点靠它做「保存素材库」「合规校验」
  text?: string; // 文本生成任务的产物(文本节点 AI 生成);与 outputUrls 互斥
  error?: string;
}

export async function submitCanvasTask(
  input: SubmitCanvasTaskInput,
): Promise<{ taskId: number; status: string }> {
  const { data } = await api.post<{ taskId: number; status: string }>('/canvas-tasks', input);
  return data;
}

// ---------- 自由画布视频后处理（擦除字幕 / 转高清 / 转白模）：读配置 + 提交任务 ----------
// 配置来自 settings key canvas_postprocess_config（后台可编辑），端点需登录（画布本身也要登录才能用）。
// 提交走既有的 /canvas-tasks 家族，但各有独立子路由 /canvas-tasks/desub、/upscale、/whitemodel：
// 后处理的计费口径（源地址解析 + 服务端权威 duration）跟普通生成任务不同，不能塞进 SubmitCanvasTaskInput。

export interface CanvasDesubOption {
  modelId: string;
  label: string;
  subtitle: string;
  isTimeLimited: boolean;
}

export interface CanvasUpscaleResolution {
  value: string;
  label: string;
}

// 一个可选的转高清模型。label 后端已做「空则回落 modelId」，前端直接显示。
export interface CanvasUpscaleModelOption {
  modelId: string;
  label: string;
  resolutions: CanvasUpscaleResolution[];
}

export interface CanvasPostprocessConfig {
  subtitleRemoval: CanvasDesubOption[];
  // 转高清是「多模型 × 各自档位」：顺序即后台配置顺序，节点默认选第一个。档位由后端从各模型
  // uiSchema.resolution.options 读出下发，这样转高清模型可以在模型中心保持隐藏（不污染生视频
  // 的模型列表）而节点仍能显示档位；没档位的模型后端不下发。
  // （响应里还有单模型时代的 upscaleModel/upscaleResolutions，只给发版前打开的旧页面用，新代码别读。）
  upscaleModels: CanvasUpscaleModelOption[];
  // 转白模：单模型 + 档位，形态同单模型时代的转高清。
  whiteModelModel: string;
  whiteModelResolutions: CanvasUpscaleResolution[];
}

export async function fetchCanvasPostprocessConfig(): Promise<CanvasPostprocessConfig> {
  const { data } = await api.get<CanvasPostprocessConfig>('/canvas/postprocess-config');
  return data;
}

// ---------- AI 绘图节点「提示词预设」（gen-image 专属） ----------
// 配置来自 settings key gen_image_prompt_presets（后台「绘图预设设置」可编辑）。
// 用户端下发的 value **没有 prompt 字段**——预设正文保密，服务端白名单剥离后才下发；
// 前端提交任务时只带 presetKey，拼接在服务端执行层完成。

export interface GenImagePresetValue {
  name: string;
  category?: string;
  icon?: string;
  description?: string;
  placeholder?: string;
  // 对上游连线图片数的约束。缺省 = 不限制；「必须恰好一张」= min 1 / max 1，
  // 「纯文生图不许连图」= max 0。
  minImages?: number;
  maxImages?: number;
  sort?: number;
  // 可调参数维度（如「人像质感调节」的人景融合/光影融合/皮肤…），每维度固定档位
  // 单选。带 params 的预设，chip 上的 ⚙ 打开参数面板而不是换预设菜单。
  // options 里**没有 prompt**——档位对应的正文片段与预设正文同规则保密，
  // key → 片段的拼接全在服务端；default 标记该维度未选时服务端落哪个档。
  params?: Array<{
    key: string;
    name: string;
    options: Array<{ key: string; name: string; default?: boolean }>;
  }>;
}

export interface GenImagePresetItem {
  key: string;
  value: GenImagePresetValue;
}

export interface GenImagePresetsConfig {
  // 绑定的模型 slug 列表：节点当前选中的模型命中才显示预设菜单入口。
  models: string[];
  presets: GenImagePresetItem[];
}

export async function fetchGenImagePresets(): Promise<GenImagePresetsConfig> {
  const { data } = await api.get<GenImagePresetsConfig>('/canvas/gen-image-presets');
  // 后台未配置时后端返回空配置；这里再兜一层，让调用方拿到的永远是数组。
  return { models: data?.models ?? [], presets: data?.presets ?? [] };
}

// ---------- 画布图片「多角度编辑器」 ----------
// 配置来自 settings key canvas_multi_angle（后台「绘图多角度设置」可编辑）。
// 下发内容已脱敏：只有模型列表与预设的角度数值（点预设滑杆跳位用）——
// 附加提示词与拼接模板保密，key → 文本的拼接在服务端执行层完成。

/** 多角度走 SubmitCanvasTaskInput.presetKey 通道的保留 key（服务端同名常量）。 */
export const MULTI_ANGLE_PRESET_KEY = '__multi_angle__';

export type MultiAngleZoom = 'closeup' | 'medium' | 'panorama';

export interface MultiAnglePresetPublic {
  key: string;
  name: string;
  rotation: number; // 0~359，向右为正
  tilt: number; // -90~90，正=俯拍
  zoom: MultiAngleZoom;
}

export interface MultiAngleConfigPublic {
  models: string[]; // 启用的绘图模型，顺序即下拉顺序（第一个默认选中）
  presets: MultiAnglePresetPublic[];
}

export async function fetchMultiAngleConfig(): Promise<MultiAngleConfigPublic> {
  const { data } = await api.get<MultiAngleConfigPublic>('/canvas/multi-angle');
  return { models: data?.models ?? [], presets: data?.presets ?? [] };
}

// ---------- 画布图片「打光效果」面板 ----------
// 配置来自 settings key canvas_light_control（后台「绘图打光设置」可编辑）。
// 下发内容已脱敏：模型列表与预设的参数/缩略图/参考图（点预设表单跳位/带图用）——
// 附加提示词与拼接模板保密，参数 → 中文打光文本的拼接在服务端执行层完成。

/** 打光走 SubmitCanvasTaskInput.presetKey 通道的保留 key（服务端同名常量）。 */
export const LIGHT_CONTROL_PRESET_KEY = '__light_control__';

export interface LightControlPresetPublic {
  key: string;
  name: string;
  thumbnail?: string; // 预设网格缩略图，空=退化为文字 chip
  direction?: string; // 26 方向枚举之一，空=该预设不动方向
  brightness?: number; // 0-100；缺失=不动亮度（服务端未设置时不下发该键）
  lightColor?: string; // #rrggbb，空=不动颜色
  referenceImage?: string; // 打光参考图 URL，空=无
}

export interface LightControlConfigPublic {
  models: string[]; // 启用的绘图模型，第一个默认选中
  presets: LightControlPresetPublic[];
}

export async function fetchLightControlConfig(): Promise<LightControlConfigPublic> {
  const { data } = await api.get<LightControlConfigPublic>('/canvas/light-control');
  return { models: data?.models ?? [], presets: data?.presets ?? [] };
}
export interface SubmitCanvasDesubInput {
  projectId: number;
  nodeId: string;
  sourceNodeId: string;
  modelId: string;
}

export async function submitCanvasDesub(
  input: SubmitCanvasDesubInput,
): Promise<{ taskId: number; status: string }> {
  const { data } = await api.post<{ taskId: number; status: string }>('/canvas-tasks/desub', input);
  return data;
}

// 转高清 / 转白模共用的提交入参（后端同一个 submitTiered 实现，只差允许的模型）。
export interface SubmitCanvasTieredInput {
  projectId: number;
  nodeId: string;
  sourceNodeId: string;
  modelId: string;
  resolution: string;
}

export type SubmitCanvasUpscaleInput = SubmitCanvasTieredInput;

// 独立于 desub 的端点：转高清没有「限时上游直连」分支，但多一个 resolution 档位。
// 计费秒数同样服务端权威（请求体不带 duration，后端从 assets.duration_ms 取）。
export async function submitCanvasUpscale(
  input: SubmitCanvasUpscaleInput,
): Promise<{ taskId: number; status: string }> {
  const { data } = await api.post<{ taskId: number; status: string }>(
    '/canvas-tasks/upscale',
    input,
  );
  return data;
}

// 转白模：与转高清同契约，后端只认后台配的 whiteModelModel。
export async function submitCanvasWhiteModel(
  input: SubmitCanvasTieredInput,
): Promise<{ taskId: number; status: string }> {
  const { data } = await api.post<{ taskId: number; status: string }>(
    '/canvas-tasks/whitemodel',
    input,
  );
  return data;
}

// ---------- 后处理预估积分 ----------
// 为什么不用通用的 /models/estimate：那条路要前端自己把 duration 拼进 inputs，而视频时长
// 只有 <video>.duration 拿得到——那是浏览器解析出的容器声明值，可被工具单独篡改。曾因此
// 让一个两分多钟的视频显示「预计 1 积分」（实际按真实时长结算，钱没错但数字骗人）。
// 现在整个计费 req 都由后端用 assets.duration_ms（ffprobe 数真实帧测定）构造，前端只显示。
// resolution 供转高清/转白模用（档位影响单价）；去字幕不传。

export interface CanvasPostprocessEstimateInput {
  projectId: number;
  sourceNodeId: string;
  modelId: string;
  resolution?: string;
}

export interface CanvasPostprocessEstimate {
  credits: number;
  durationMs: number;
}

export async function fetchCanvasPostprocessEstimate(
  input: CanvasPostprocessEstimateInput,
): Promise<CanvasPostprocessEstimate> {
  const { data } = await api.post<CanvasPostprocessEstimate>(
    '/canvas-tasks/postprocess-estimate',
    input,
  );
  return data;
}

export async function getCanvasTask(taskID: number): Promise<CanvasTaskStatus> {
  const { data } = await api.get<CanvasTaskStatus>(`/canvas-tasks/${taskID}`);
  return data;
}

// 轮询 GET /canvas-tasks/{id} 直到 succeeded/failed。给画布 gen 节点用:拿到结果只为即时展示,
// 持久化由后端回写快照负责(前端刷新/重开也能看到)。
//
// 容错:单次网络错误/5xx 不放弃——曾经任意一次抖动就 reject,调用方把节点判「失败」
// 并整节点 persist,把后端刚写回快照的成功结果覆盖掉。只有连续失败 5 次才放弃;
// 4xx 是确定性错误(任务不存在/无权),重试无意义,立即抛。
export async function pollCanvasTaskUntilDone(
  taskID: number,
  opts: { intervalMs?: number; signal?: AbortSignal } = {},
): Promise<CanvasTaskStatus> {
  const interval = opts.intervalMs ?? 3000;
  const maxConsecutiveFailures = 5;
  let failures = 0;
  const sleep = () =>
    new Promise<void>((resolve, reject) => {
      const t = setTimeout(resolve, interval);
      opts.signal?.addEventListener('abort', () => {
        clearTimeout(t);
        reject(new Error('aborted'));
      });
    });
  while (true) {
    if (opts.signal?.aborted) throw new Error('aborted');
    try {
      const st = await getCanvasTask(taskID);
      failures = 0;
      if (st.status === 'succeeded' || st.status === 'failed') return st;
    } catch (err: any) {
      const status = err?.response?.status;
      if (status && status >= 400 && status < 500) throw err;
      failures += 1;
      if (failures >= maxConsecutiveFailures) throw err;
    }
    await sleep();
  }
}

// Chat completion 消息结构（OpenAI 兼容子集）。`video_url`
// 和 `audio_url` 是豆包 / ARK 针对原生视频 / 音频理解的扩展
// （尽力而为——所选模型不支持时会优雅降级）。
export type ChatContentPart =
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } }
  | { type: 'video_url'; video_url: { url: string; fps?: string | number } }
  | { type: 'audio_url'; audio_url: { url: string } };

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string | ChatContentPart[];
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
  max_tokens?: number;
  stream?: boolean;
  response_format?: { type: 'json_object' | 'text' };
}

export interface ChatCompletionResponse {
  id?: string;
  choices: Array<{
    index: number;
    message: ChatMessage;
    finish_reason?: string;
  }>;
}

// chatCompletion 代理到后端的 /chat/completions，后端再带上调用方的
// sk-key 转发给 newapi。不带流式（以后要 SSE 就单独用 EventSource）。
export async function chatCompletion(req: ChatCompletionRequest): Promise<ChatCompletionResponse> {
  const { data } = await api.post<ChatCompletionResponse>('/chat/completions', req);
  return data;
}

export interface LedgerEntry {
  id: number;
  entryType: string; // recharge | freeze | settle | refund
  amountCredits: number;
  beforeCredits: number;
  afterCredits: number;
  note: string;
  createdAt: string;
}

export interface LedgerParams {
  page?: number;
  pageSize?: number;
  type?: string; // entryType 过滤；空 = 不限
  from?: string; // RFC3339，时间区间起
  to?: string; // RFC3339，时间区间止
}

export async function getMyLedger(params: LedgerParams = {}): Promise<{ data: LedgerEntry[]; total: number }> {
  const { data } = await api.get<{ data: LedgerEntry[]; total: number }>('/me/ledger', { params });
  return { data: data.data ?? [], total: data.total ?? 0 };
}

export interface GenerationTask {
  id: number;
  userId: number;
  projectId?: number;
  modelId: string;
  status: 'queued' | 'processing' | 'succeeded' | 'failed';
  /** 产物类型大类（0103 列，服务端过滤/回显用；老任务四层回填仍缺信号时为空） */
  outputKind?: 'image' | 'video' | 'audio';
  upstreamTaskId?: string;
  input: {
    modelId?: string;
    inputs?: Record<string, unknown>;
    payload?: unknown;
  };
  output?: {
    outputUrls?: string[];
    assetIds?: number[];
    raw?: unknown;
    upstreamTaskId?: string;
    // 主产物真实媒体类型的大类(后端按下载 mime 推导)。隐藏模型(如 sd2.0
    // 去字幕)不进 /models 列表、产物对象 key 又没有扩展名，纯前端猜不出
    // 是图还是视频，所以后端补这个字段做权威判定；旧任务没有此字段。
    outputKind?: 'image' | 'video' | 'audio';
  };
  costCents?: number;
  auditFlag?: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

// ---------- Chat（项目维度） ---------------------------------------

export interface ChatSession {
  id: number;
  projectId: number;
  title: string;
  defaultModel?: string;
  createdAt: string;
  updatedAt: string;
  messageCount?: number;
}

export interface ChatFileAttachment {
  type: 'image' | 'video' | 'audio' | 'doc' | 'code' | 'text';
  url?: string;     // data URL 或 http URL
  name?: string;
  mime?: string;
  text?: string;    // 对于 code/text 类附件，我们把内容内联进来
}

export interface ChatMessageRecord {
  id: number;
  sessionId: number;
  role: 'user' | 'assistant' | 'system';
  content: string;
  files?: ChatFileAttachment[];
  modelId?: string;
  isError?: boolean;
  createdAt: string;
}

export async function listChatSessions(projectId: number): Promise<ChatSession[]> {
  const { data } = await api.get<{ data: ChatSession[] }>(`/projects/${projectId}/chat/sessions`);
  return data.data ?? [];
}

export async function createChatSession(projectId: number, body: { title?: string; defaultModel?: string } = {}): Promise<ChatSession> {
  const { data } = await api.post<ChatSession>(`/projects/${projectId}/chat/sessions`, body);
  return data;
}

export async function updateChatSession(projectId: number, sessionId: number, body: { title?: string; defaultModel?: string }): Promise<ChatSession> {
  const { data } = await api.put<ChatSession>(`/projects/${projectId}/chat/sessions/${sessionId}`, body);
  return data;
}

export async function deleteChatSession(projectId: number, sessionId: number): Promise<void> {
  await api.delete(`/projects/${projectId}/chat/sessions/${sessionId}`);
}

export async function listChatMessages(projectId: number, sessionId: number): Promise<ChatMessageRecord[]> {
  const { data } = await api.get<{ data: ChatMessageRecord[] }>(`/projects/${projectId}/chat/sessions/${sessionId}/messages`);
  return data.data ?? [];
}

export async function appendChatMessage(
  projectId: number,
  sessionId: number,
  body: { role: 'user' | 'assistant' | 'system'; content: string; files?: ChatFileAttachment[]; modelId?: string; isError?: boolean },
): Promise<ChatMessageRecord> {
  const { data } = await api.post<ChatMessageRecord>(`/projects/${projectId}/chat/sessions/${sessionId}/messages`, body);
  return data;
}

export interface ListTasksParams {
  projectId?: number;
  status?: string;
  modelId?: string;
  /** 产物类型过滤（服务端按 output_kind 列过滤，跨项目生成历史选择用） */
  kind?: 'image' | 'video' | 'audio';
  search?: string;
  limit?: number;
  offset?: number;
}

export interface ListTasksResult {
  data: GenerationTask[];
  total: number;
}

export async function listTasks(params: ListTasksParams = {}): Promise<ListTasksResult> {
  const { data } = await api.get<ListTasksResult>('/tasks', { params });
  return { data: data.data ?? [], total: data.total ?? 0 };
}

export async function deleteTask(taskID: number): Promise<void> {
  await api.delete(`/tasks/${taskID}`);
}

/** 「从生成历史选择」确认动作：把任务第 outputIndex 个产物以 generation_ref
 *  引用行落进目标画布项目（引用行是跨项目 GC 防线，同 library_ref/canvas_ref）。 */
export async function applyGenerationOutputToCanvas(
  taskId: number,
  projectId: number,
  outputIndex: number,
): Promise<{ assetId: number; url: string; kind: 'image' | 'video' | 'audio'; mimeType: string }> {
  const { data } = await api.post(`/tasks/${taskId}/apply-to-canvas`, { projectId, outputIndex });
  return data;
}

// ---------- 素材库 ----------
import type { LibraryFolder, LibraryItem } from '@/modules/canvas/panels/library/types';

export async function listLibraryFolders(): Promise<LibraryFolder[]> {
  const { data } = await api.get<{ data: LibraryFolder[] }>('/library/folders');
  return data.data ?? [];
}

export async function createLibraryFolder(name: string, parentId?: number): Promise<LibraryFolder> {
  const { data } = await api.post<LibraryFolder>('/library/folders', { name, parentId });
  return data;
}

export async function renameLibraryFolder(id: number, name: string): Promise<LibraryFolder> {
  const { data } = await api.patch<LibraryFolder>(`/library/folders/${id}`, { name });
  return data;
}

export async function moveLibraryFolder(id: number, parentId: number | null): Promise<LibraryFolder> {
  const body = parentId === null ? { clearParent: true } : { parentId };
  const { data } = await api.patch<LibraryFolder>(`/library/folders/${id}`, body);
  return data;
}

export async function deleteLibraryFolder(id: number, force = false): Promise<void> {
  await api.delete(`/library/folders/${id}${force ? '?force=1' : ''}`);
}

export interface ListLibraryItemsParams {
  folderId?: number;
  search?: string;
  limit?: number;
  offset?: number;
}

export async function listLibraryItems(
  params: ListLibraryItemsParams = {},
): Promise<{ data: LibraryItem[]; total: number }> {
  const qs = new URLSearchParams();
  if (params.folderId) qs.set('folder_id', String(params.folderId));
  if (params.search) qs.set('search', params.search);
  if (params.limit) qs.set('limit', String(params.limit));
  if (params.offset) qs.set('offset', String(params.offset));
  const { data } = await api.get<{ data: LibraryItem[]; total: number }>(`/library/items?${qs}`);
  return { data: data.data ?? [], total: data.total ?? 0 };
}

export async function uploadLibraryItem(
  folderId: number,
  file: File,
  onProgress?: (pct: number) => void,
): Promise<LibraryItem> {
  const form = new FormData();
  form.append('file', file);
  const { data } = await api.post<LibraryItem>(`/library/folders/${folderId}/items/upload`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
    onUploadProgress: (e) => {
      if (e.total && onProgress) onProgress(Math.round((e.loaded / e.total) * 100));
    },
  });
  return data;
}

export async function updateLibraryItem(
  id: number,
  patch: { name?: string; folderId?: number },
): Promise<LibraryItem> {
  const { data } = await api.patch<LibraryItem>(`/library/items/${id}`, patch);
  return data;
}

export async function deleteLibraryItem(id: number): Promise<void> {
  await api.delete(`/library/items/${id}`);
}

export async function saveAssetToLibrary(
  projectId: number,
  assetId: number,
  folderId: number,
  name?: string,
): Promise<LibraryItem> {
  const { data } = await api.post<LibraryItem>(
    `/projects/${projectId}/assets/${assetId}/save-to-library`,
    { folderId, name },
  );
  return data;
}

export interface ApplyLibraryItemResult {
  assetId: number;
  url: string;
  kind: 'image' | 'video' | 'audio';
  mimeType: string;
  width?: number;
  height?: number;
  durationMs?: number;
}

export async function applyLibraryItemToCanvas(
  itemId: number,
  projectId: number,
): Promise<ApplyLibraryItemResult> {
  const { data } = await api.post<ApplyLibraryItemResult>(
    `/library/items/${itemId}/apply-to-canvas`,
    { projectId },
  );
  return data;
}

export interface ImportAssetRefsResult {
  imported: number;
  missing?: string[];
}

/** 「复制节点」跨项目粘贴后的资产引用登记：给目标项目按 object_key 插引用行,
 *  防源项目删除后媒体被孤儿 GC 回收。幂等，可重试。 */
export async function importAssetRefs(
  projectId: number,
  objectKeys: string[],
): Promise<ImportAssetRefsResult> {
  const { data } = await api.post<ImportAssetRefsResult>(
    `/projects/${projectId}/assets/import-refs`,
    { objectKeys },
  );
  return data;
}

// ─── 分享工作流 ─────────────────────────────────────────────────────

export type WorkflowVisibility = 'public' | 'unlisted';
// share 行沿用 published/unpublished/removed_by_admin 软删除循环；tvshow 行走
// pending → approved/rejected 审核状态机（下架回 unpublished）——同一个 status
// 字段两种 kind 共用，见后端 apps/api/internal/workflows/types.go 的 Status。
export type WorkflowStatus =
  | 'published'
  | 'unpublished'
  | 'removed_by_admin'
  | 'pending'
  | 'approved'
  | 'rejected';

export interface WorkflowSummary {
  id: number;
  title: string;
  description: string;
  tags: string;
  thumbnailUrl: string;
  visibility: WorkflowVisibility;
  status: WorkflowStatus;
  shareToken: string;
  cloneCount: number;
  byteSize: number;
  publishedAt: string | null;
  unpublishedAt: string | null;
  createdAt: string;
  // ─── TV Show 投稿字段（kind='tvshow' 时才有意义，share 行全零值/缺省）───
  kind?: 'share' | 'tvshow';
  rejectReason?: string;
  category?: string;
  weight?: number;
  approvedAt?: string | null;
  sourceProject?: number;
}

export interface WorkflowPreviewNode {
  id: string;
  type: string;
  x: number;
  y: number;
  width: number;
  height: number;
  content?: string;
  settings?: Record<string, unknown>;
}

export interface WorkflowPreviewConnection {
  id?: string;
  from: string;
  to: string;
  inputType?: string;
}

export interface WorkflowPreviewSnapshot {
  nodes?: WorkflowPreviewNode[];
  connections?: WorkflowPreviewConnection[];
  view?: { x: number; y: number; zoom: number };
}

export interface WorkflowPreview {
  id: number;
  title: string;
  description: string;
  tags: string;
  thumbnailUrl: string;
  visibility: WorkflowVisibility;
  cloneCount: number;
  byteSize: number;
  publishedAt: string | null;
  nodeCount: number;
  models: string[];
  ownerName?: string;
  snapshot?: WorkflowPreviewSnapshot;
}

export interface PublishWorkflowInput {
  projectId: number;
  title: string;
  description?: string;
  tags?: string;
  visibility?: WorkflowVisibility;
  redactPrompts?: boolean;
  thumbnailUrl?: string;
}

export interface PublishWorkflowResult {
  id: number;
  shareToken: string;
  thumbnailUrl: string;
  byteSize: number;
}

export async function publishWorkflow(input: PublishWorkflowInput): Promise<PublishWorkflowResult> {
  const { data } = await api.post<PublishWorkflowResult>('/workflows/publish', input);
  return data;
}

export async function getWorkflowByToken(token: string): Promise<WorkflowPreview> {
  const { data } = await api.get<WorkflowPreview>(`/workflows/by-token/${token}`);
  return data;
}

export async function cloneWorkflow(token: string): Promise<{ projectId: number }> {
  const { data } = await api.post<{ projectId: number }>(`/workflows/by-token/${token}/clone`);
  return data;
}

// kind 不传时保持老行为（后端缺省/非法值也按 share 处理）——「我的分享」页
// 拆分双 tab 后，分享 tab 与 TV Show tab 各自带上 kind 查询自己的那一半。
export async function listMyWorkflows(kind?: 'share' | 'tvshow'): Promise<WorkflowSummary[]> {
  const url = kind ? `/workflows/mine?kind=${kind}` : '/workflows/mine';
  const { data } = await api.get<{ data: WorkflowSummary[] }>(url);
  return data.data ?? [];
}

export async function unpublishWorkflow(id: number): Promise<void> {
  await api.delete(`/workflows/${id}`);
}

export async function hardDeleteWorkflow(id: number): Promise<void> {
  await api.delete(`/workflows/${id}?hard=true`);
}

export async function republishWorkflow(
  id: number,
  regenerateToken = true,
): Promise<{ shareToken: string }> {
  const { data } = await api.post<{ shareToken: string }>(`/workflows/${id}/republish`, {
    regenerateToken,
  });
  return data;
}

// ========== Agent 线程 & 消息 ==========

export interface AgentThreadDTO {
  id: string;
  title: string;
  status: string;
  agent_name: string;
  created_at: string;
  updated_at: string;
}

export async function listAgentThreads(projectId: number): Promise<AgentThreadDTO[]> {
  const { data } = await api.get(`/projects/${projectId}/agent/threads`);
  return (data.data ?? []) as AgentThreadDTO[];
}

export async function createAgentThread(projectId: number, title?: string): Promise<{ id: string; title: string }> {
  const { data } = await api.post(`/projects/${projectId}/agent/threads`, { title });
  return data as { id: string; title: string };
}

export async function updateAgentThread(projectId: number, threadId: string, title: string): Promise<void> {
  await api.put(`/projects/${projectId}/agent/threads/${threadId}`, { title });
}

export async function deleteAgentThread(projectId: number, threadId: string): Promise<void> {
  await api.delete(`/projects/${projectId}/agent/threads/${threadId}`);
}

export interface AgentMessageDTO {
  id: string;
  thread_id: string;
  action_id: string;
  role: 'user' | 'assistant' | 'tool';
  content_blocks: unknown; // 服务端返回已解析的 JSON；需要时 cast 成 ContentBlock[]
  stop_reason: string;
  created_at: string;
}

export interface AgentMessagesPage {
  data: AgentMessageDTO[];
  /** 排序上是否还有更早的活跃消息（懒加载哨兵开关）。全量模式恒为 false。 */
  hasMore: boolean;
}

// 缺省（不传 opts）= 全量返回，与历史行为一致；带 limit 走开窗：最新 limit 条，
// 或 before（某条已加载消息的 id）之前的 limit 条。data 恒为升序。
export async function listAgentMessages(
  projectId: number,
  threadId: string,
  opts?: { limit?: number; before?: string },
): Promise<AgentMessagesPage> {
  const params: Record<string, string | number> = {};
  if (opts?.limit) params.limit = opts.limit;
  if (opts?.before) params.before = opts.before;
  const { data } = await api.get(`/projects/${projectId}/agent/threads/${threadId}/messages`, { params });
  return { data: (data.data ?? []) as AgentMessageDTO[], hasMore: Boolean(data.has_more) };
}

// ========== Agent skills（面向用户的目录） ==========

export interface AgentSkillDTO {
  id: string;
  name: string;
  description: string;
  /** 主分类。后端保证等于 categories[0]（写入时 normalizeSkillCategories 回写）。 */
  category: string;
  /** 多分类真源（后端 5fbb661a 起）。旧行/旧服务端可能缺失或为空——读方一律走
   *  skillCategoryGroups 的 effectiveCategories 拿 [category] 兜底，别直接读。 */
  categories?: string[];
  icon_url: string;
  skill_type: string;
  system_prompt_addon: string;
  force_tools: string[];
  param_defaults: Record<string, unknown>;
  is_official: boolean;
  is_published: boolean;
}

export async function listAgentSkills(category?: string, q?: string): Promise<AgentSkillDTO[]> {
  const params = new URLSearchParams();
  if (category) params.set('category', category);
  if (q) params.set('q', q);
  const url = params.toString() ? `/skills?${params}` : '/skills';
  const { data } = await api.get(url);
  return data.data ?? [];
}

// 首页故事灵感入口下方的「首推 skill」行——公开接口（匿名访客也看首页）。
// 服务端只出已发布+官方+后台勾了首推的 skill，icon_url 已是小缩略签名 URL，
// system_prompt_addon 恒为空（提示词不下发给匿名访客；发送只带 skill id）。
export async function listFeaturedSkills(): Promise<AgentSkillDTO[]> {
  const { data } = await api.get('/skills/featured');
  return data.data ?? [];
}

// skill 分类字典（后台维护词表+顺序+颜色）。服务端已按 sort_order 升序返回，
// 前端不再排序；color 是 AntD 预设色名（purple/magenta/orange/gold/blue 等），
// SkillPopover 用它映射到 tailwind 配色。
export interface SkillCategoryDTO {
  id: number;
  name: string;
  color: string;
  sort_order: number;
}

export async function listSkillCategories(): Promise<SkillCategoryDTO[]> {
  const { data } = await api.get('/skills/categories');
  return data.data ?? [];
}

export type SeedanceStatus = 'pending' | 'active' | 'failed' | 'failed_permanent';

export interface SeedanceAssetState {
  objectKey: string;
  assetId?: string;
  assetType?: 'Image' | 'Video' | 'Audio';
  status: SeedanceStatus;
  error?: string;
}

// 提交一个画布素材（图片/视频/音频）做 Seedance 2.0 合规校验。幂等——已通过的直接回原状态。
// 以 URL 而不是 assetId 为入口：assetId 要靠前端在生成那一刻存进节点，历史节点、
// agent 产出的节点都没有；URL 则是节点始终持有的东西。
// kind 必传：上游按类型套用不同规格要求，而它无法从 object_key 可靠推断（生成产物
// 的 key 不保证带扩展名）——节点自己知道它挂的是什么。
export async function verifySeedance(
  url: string,
  kind: 'image' | 'video' | 'audio',
): Promise<SeedanceAssetState> {
  const { data } = await api.post<SeedanceAssetState>('/seedance/verify', { url, kind });
  return data;
}

// 批量查校验状态，键是传入的 URL；没校验过的不会出现在结果里。
export async function fetchSeedanceStatus(
  urls: string[],
): Promise<Record<string, SeedanceAssetState>> {
  const { data } = await api.post<Record<string, SeedanceAssetState>>('/seedance/status', { urls });
  return data;
}

// ─── 画布 AI 角色库 ─────────────────────────────────────────────────
// 平台预置的公共角色目录（已过 Seedance 合规校验）。只读。

export interface AICharacter {
  assetId: string;
  age: number;
  gender: string;
  occupation: string;
  description: string;
  ageGroup: string;
  imageUrl: string; // 签名后的原图，详情展示 + 应用到画布用
  thumbUrl: string; // 768w 缩略图，网格卡片用
}

export interface AICharacterPage {
  items: AICharacter[];
  hasMore: boolean;
}

export async function fetchAICharacters(params: {
  search?: string;
  gender?: string;
  ageGroup?: string;
  offset?: number;
}): Promise<AICharacterPage> {
  const { data } = await api.get<AICharacterPage>('/canvas/ai-characters', { params });
  return data;
}

// 按媒体 URL 把画布上的东西存进素材库（同样不依赖 assetId）。
export async function saveMediaToLibrary(
  url: string,
  folderId: number,
  name?: string,
): Promise<LibraryItem> {
  const { data } = await api.post<LibraryItem>('/library/save-from-url', { url, folderId, name });
  return data;
}

// ========== CLI 浏览器登录 ==========
// 网页登录态 → 换一个 aud=cli 的 JWT，由 `tvmao auth login` 的本机回调收走。
//
// 必须走换发而不是直接把网页 token 交出去：CLI 凭据必须带 aud=cli，网页 token
// 刻意不带（双向隔离），直接给过去会被 CLI 中间件以 AUDIENCE_DENIED 拒掉。
//
// callbackUrl 一并送给后端再校验一次是不是本机地址——前端那道校验在用户浏览器里
// 跑、理论上可被绕过，后端那道绕不过。

export interface CliExchangeResult {
  accessToken: string;
  expiresAt: string;
}

export async function exchangeCliToken(callbackUrl: string): Promise<CliExchangeResult> {
  const { data } = await api.post<CliExchangeResult>('/cli/auth/exchange', { callbackUrl });
  return data;
}

// ========== tvmao-flow 浏览器登录 ==========
// 与上面的 CLI 换发并排：aud=flow，回调参数名 `flow_callback_url`，互不干扰。

export interface FlowExchangeResult {
  accessToken: string;
  expiresAt: string;
}

export async function exchangeFlowToken(callbackUrl: string): Promise<FlowExchangeResult> {
  const { data } = await api.post<FlowExchangeResult>('/flow/auth/exchange', { callbackUrl });
  return data;
}

// ========== 自由画布 · 脚本节点「剧本生成分镜」 ==========
// 风格是后台配置的展示名（映射到服务端 skill，前端不感知 skillName）；
// 任务恒异步：提交冻结积分 → 轮询 → succeeded 拿 candidate.json 原文自行导入。

export interface CanvasScriptModelCandidate {
  modelId: string;
  displayName: string;
  /** 该模型一次执行的预冻结积分（后端按模型中心计费实时算，与扣费同源） */
  credits: number;
}

export interface CanvasScriptStylesResponse {
  /** version 可为空；提交时 style 传 "name" 或 "name version" 全串 */
  styles: Array<{ name: string; version?: string }>;
  /** 可选的 chat 模型候选；顺序即展示顺序，第一项为默认选中 */
  modelCandidates: CanvasScriptModelCandidate[];
}

export async function getCanvasScriptStyles(): Promise<CanvasScriptStylesResponse> {
  const { data } = await api.get<CanvasScriptStylesResponse>('/canvas-script/styles');
  return data;
}

export async function submitCanvasScriptTask(body: {
  projectId: number;
  nodeId: string;
  style: string;
  durationSec: number;
  script: string;
  model: string;
}): Promise<{ taskId: number; status: string }> {
  const { data } = await api.post<{ taskId: number; status: string }>('/canvas-script-tasks', body);
  return data;
}

export interface CanvasScriptTaskStatus {
  status: 'pending' | 'running' | 'succeeded' | 'failed';
  nodeId: string;
  durationSec: number;
  /** succeeded 时的 candidate.json 原文，导入映射在前端 */
  result?: string;
  /** failed 时的失败原因 */
  error?: string;
}

export async function getCanvasScriptTask(taskId: number): Promise<CanvasScriptTaskStatus> {
  const { data } = await api.get<CanvasScriptTaskStatus>(`/canvas-script-tasks/${taskId}`);
  return data;
}

// ========== TV Show（投稿 + 首页展示） ==========
// 公开端点未登录也能调（后端 handler 刻意不挂在需鉴权路径下）；字典只出 enabled
// 的选项，永不返回 null（空字典是 []）——前端据此判断「活动标签」字段要不要整个
// 隐藏，不需要额外判 undefined。
// 2026-08-06 用户拍板：分类/活动标签/参赛赛道本是同一个东西，只留一个字段，
// API 字段名仍叫 category/categories（不动，是硬边界），用户端显示文案改叫
// 「活动标签」；赛道彻底移除。

export interface TvshowWork {
  id: number;
  title: string;
  coverUrl: string;
  authorName: string;
  category: string;
  shareToken: string;
}

// 排序完全由后端保证（weight DESC, approved_at DESC），前端不再排序。
export async function listTvshowWorks(): Promise<TvshowWork[]> {
  const { data } = await api.get<{ data: TvshowWork[] }>('/tvshow/works');
  return data.data ?? [];
}

export interface TvshowDicts {
  categories: string[];
}

export async function getTvshowDicts(): Promise<TvshowDicts> {
  const { data } = await api.get<TvshowDicts>('/tvshow/dicts');
  return data;
}

export interface SubmitTvshowInput {
  projectId: number;
  title: string;
  description: string;
  category: string;
  socialLink?: string;
  thumbnailUrl?: string;
}

export async function submitTvshowWork(input: SubmitTvshowInput): Promise<{ id: number }> {
  const { data } = await api.post<{ id: number }>('/workflows/submit-tvshow', input);
  return data;
}

// ========== 首页 banner 轮播 ==========
// 公开端点，未登录也能调（首页匿名可见，同 TvShow 端点走法）。只出启用项且已按
// sort_order,id 排好序，前端不再重排。imageUrl 是原图签名 URL（不是缩略图）。

export interface HomeBannerItem {
  id: number;
  imageUrl: string;
  linkUrl: string;
}

export async function listHomeBanners(): Promise<HomeBannerItem[]> {
  const { data } = await api.get<{ data: HomeBannerItem[] }>('/home/banners');
  return data.data ?? [];
}

// ─── 团队 ─────────────────────────────────────────────────────────
// 字段以 apps/api/internal/http/team_auth_handlers.go(切换器) 与
// team_handlers.go(团队管理) 的 json tag 为准，不是猜出来的。
//
// 三层身份模型：真人(human) 可以在个人身份或若干团队身份间切换；团队身份下
// ctx.userID 是"影子用户"(资产/账本挂在它名下)，真人是 actor。切换器
// (/auth/contexts、/auth/switch-context) 与团队管理(/teams/*) 是两组端点，
// 前者在网页 auth 组下不限流/限流规则也不同，但都要求已登录。

/**
 * GET /auth/contexts 里单个可切换身份。role 由服务端按 ownerUserId 算好。
 *
 * memberCount 之后四个字段是 2026-08-18 补的切换面板副行数据（负责人行
 * 「成员 N·团队积分 X」、成员行「成员 N·本月额度 已用/总额」）：
 * - memberCount 两种角色都下发，且始终存在（非 undefined）——用它判断
 *   响应是否来自已支持这批字段的后端；`undefined` = 旧后端，前端应砍掉
 *   整行副行，不要报错也不要瞎猜。
 * - teamBalanceCredits 只在 role='owner' 的行出现；quotaCredits/usedCredits
 *   只在 role='member' 的行有意义（owner 行 usedCredits 服务端固定给 0，
 *   前端应忽略，靠 role 而不是字段是否存在来分支）。
 * - quotaCredits 的 null 与 undefined 语义不同（2026-08-18 审查 finding
 *   P2-4 起，Important-1 修正；2026-08-21 随「取消不限额」更新 null 的
 *   含义）：`null` 是服务端显式下发的"**存量未配置**"（消费侧按 0 拒绝，
 *   显示「未配置」）；`undefined` 是键完全不存在（role='owner' 的行，或极
 *   旧后端未支持这批字段），代表"不适用/未知"（显示「—」）。不能用
 *   `== null` 把两者混着判——那会把"不知道"和"配额为空"说成同一件事。
 *   **两者在 wire 上都真实可达**：服务端对 member 行手动控制序列化（不靠
 *   `*int64`+`omitempty`），quotaCredits 键在 member 行永远出现（值是数字
 *   或字面 JSON null），只在 owner 行/未知后端时整个键缺席——所以
 *   `=== null` 与 `=== undefined` 在实际响应里都会真的发生，不是防御性死代码。
 */
export interface TeamContext {
  id: number;
  uuid: string;
  name: string;
  role: string; // 'owner' | 'member'
  memberCount?: number;
  teamBalanceCredits?: number;
  quotaCredits?: number | null;
  usedCredits?: number;
}

/** currentTeamId=0 表示当前是个人身份（服务端按 token 里的 actor 反查算出）。 */
export interface AuthContexts {
  currentTeamId: number;
  /**
   * 当前操作者（真人）的邮箱。**团队身份下唯一能拿到真人邮箱的地方**——
   * 那时 /me 返回的是影子用户，邮箱是合成的 `team-{uuid}@team.internal`
   * （2026-08-18 终审 I1）。标 optional 是旧后端兼容：字段不存在时前端退回
   * /me 的 email（个人身份下两者本来就相等）。
   */
  personalEmail?: string;
  teams: TeamContext[];
}

export async function getAuthContexts(): Promise<AuthContexts> {
  const { data } = await api.get<AuthContexts>('/auth/contexts');
  return data;
}

/**
 * switch-context 的响应。teamId=0 切回个人时 teamName 不下发；
 * 切到某个团队时 teamId/teamName 都是该团队的（不是"目标 id 回显"，
 * 是服务端从 DB 查出来的当前值）。
 */
export interface SwitchContextResult {
  token: string;
  teamId: number;
  teamName?: string;
}

/** POST /auth/switch-context。teamId=0 切回个人身份，>0 切到该团队。 */
export async function switchContext(teamId: number): Promise<SwitchContextResult> {
  const { data } = await api.post<SwitchContextResult>('/auth/switch-context', { teamId });
  return data;
}

export interface TeamDTO {
  id: number;
  uuid: string;
  name: string;
  status: string;
  /** 建团/管理场景下才有意义，让前端不必再拉成员名单就能判断"我是不是负责人" */
  ownerUserId: number;
}

export async function createTeam(name: string): Promise<TeamDTO> {
  const { data } = await api.post<TeamDTO>('/teams', { name });
  return data;
}

/**
 * 邀请落地页的公开团队信息。GET /teams/{uuid} 刻意只回 uuid/name/status——
 * 不带自增 id、不带 ownerUserId，防止拿到邀请链接的任何人反查团队总量或负责人身份。
 */
export async function getTeamPublic(uuid: string): Promise<{ uuid: string; name: string; status: string }> {
  const { data } = await api.get<{ uuid: string; name: string; status: string }>(`/teams/${uuid}`);
  return data;
}

/** POST /teams/{uuid}/apply 提交加入申请，进入 applied 待审核状态。 */
export async function applyJoinTeam(uuid: string): Promise<void> {
  await api.post(`/teams/${uuid}/apply`);
}

/**
 * 团队成员一行。quotaCredits 是指针语义的镜像：数字=本月额度上限（0=不可
 * 消费），null=**存量未配置**（0104 之前建团给 owner 落的 NULL 行）——
 * 2026-08-21 取消「不限额」后 null 在消费侧与 0 同样被拒，展示上仍要分开说
 * （「未配置」vs「明确设成 0」），但绝不能再显示成「不限」。
 * 非 owner 拿到的名单里，别人那行的 quotaCredits/quotaPeriod 会被服务端置空、
 * usedCredits 恒为 0（只暴露自己那行的真实值）。
 *
 * isSelf 是区分"这行是不是我自己"的唯一信号——上面那条清零规则在数值上跟
 * "自己本来就是 0/不限" 完全没法区分（0 和 null 各自都可能是真值，也可能是
 * 被清空的占位），前端不能靠猜，必须读这个字段（见 team_handlers.go Members
 * 的同名注释）。
 */
export interface TeamMember {
  userId: number;
  email?: string;
  role: string;
  status: string;
  quotaCredits: number | null;
  usedCredits: number;
  quotaPeriod?: string;
  isSelf: boolean;
}

/**
 * GET /teams/{id}/members。服务端还回了一个当月周期 period 字段（每个成员行的
 * quotaPeriod 已经是它自己的值，用于跟 period 比对判断"是不是本月"）——这里
 * 只取 members 数组，period 未来如需展示"当前统计周期"可以从任意成员的
 * quotaPeriod 复用或另开一个函数读顶层字段。
 */
export async function listTeamMembers(teamId: number): Promise<TeamMember[]> {
  const { data } = await api.get<{ members: TeamMember[]; period: string }>(`/teams/${teamId}/members`);
  return data.members ?? [];
}

export async function approveMember(teamId: number, userId: number): Promise<void> {
  await api.post(`/teams/${teamId}/members/${userId}/approve`);
}

export async function rejectMember(teamId: number, userId: number): Promise<void> {
  await api.post(`/teams/${teamId}/members/${userId}/reject`);
}

export async function removeMember(teamId: number, userId: number): Promise<void> {
  await api.post(`/teams/${teamId}/members/${userId}/remove`);
}

/**
 * PUT /teams/{id}/members/{uid}/quota。quotaCredits 只接受非负整数（0=禁止消费）
 * ——2026-08-21 拍板取消「不限额」后 null 不再是可设值，服务端把「字段缺失/
 * 显式 null/负数」一律 400。签名因此从 `number | null` 收成 `number`：类型层
 * 就不给调用方传 null 的机会，而不是靠调用点自觉。
 *
 * 目标可以是负责人自己——全员（含 owner）都必须配额度才能消费团队积分。
 */
export async function setMemberQuota(teamId: number, userId: number, quotaCredits: number): Promise<void> {
  await api.put(`/teams/${teamId}/members/${userId}/quota`, { quotaCredits });
}

export async function transferTeam(teamId: number, newOwnerUserId: number): Promise<void> {
  await api.post(`/teams/${teamId}/transfer`, { newOwnerUserId });
}

export async function disbandTeam(teamId: number): Promise<void> {
  await api.post(`/teams/${teamId}/disband`);
}

/**
 * 团队账本一行。与个人账本的 LedgerEntry 字段不完全相同（多了 userId/taskId/
 * actorUserId），所以另起一个类型而不是复用——两个端点背后是同一张 billing 表，
 * 但对外形状本来就不同，硬合并会让个人账本调用方也要处理用不上的字段。
 */
export interface TeamLedgerEntry {
  id: number;
  userId: number;
  taskId?: number;
  entryType: string;
  amountCredits: number;
  beforeCredits: number;
  afterCredits: number;
  note: string;
  /** 团队消费的实际操作者(真人成员)；个人消费场景该字段不会出现在团队账本里 */
  actorUserId?: number;
  createdAt: string;
}

export interface LedgerPage {
  data: TeamLedgerEntry[];
  total: number;
  limit: number;
  offset: number;
}

/**
 * GET /teams/{id}/ledger?memberId=&limit=&offset=。仅 owner 可调（服务端校验）。
 * memberId 留空 = 整个团队的流水，不叠加 actor 过滤。
 */
export async function getTeamLedger(
  teamId: number,
  p: { memberId?: number; limit?: number; offset?: number } = {},
): Promise<LedgerPage> {
  const { data } = await api.get<LedgerPage>(`/teams/${teamId}/ledger`, { params: p });
  return data;
}

// ---------------------------------------------------------------------------
// 会员与积分超市（2c）。四个只读端点，都挂在普通登录组下——
// 后台那套 /admin/membership/* 是运营配置用的，跟这里没关系。
// ---------------------------------------------------------------------------

/** 一个可购买的售卖项：档位 × 周期 = 一个固定价。金额单位是**分**。 */
export interface CatalogSKU {
  id: number;
  periodMonths: number; // 12 | 3 | 1
  priceCents: number;
  originalPriceCents: number; // 0 = 不显示划线价
}

/** 一个会员档位（弹框里的一张卡片）。 */
export interface CatalogPlan {
  id: number;
  kind: 'personal' | 'team';
  name: string;
  /** 权益。后端保证为空时出 `{}` 不出 null，可以直接取键。 */
  benefits: { creditsPerMonth?: number; storageBytes?: number; seats?: number };
  badge: string; // 空串 = 无角标
  /** 主推：高亮展示（后台勾选）。深蓝渐变底 + 青色角标，与 badge 各管各的。 */
  featured?: boolean;
  /** team 档位为 true：benefits 里的量是**每席位**的，实际发放 = 量 × 席位数。 */
  perSeat: boolean;
  skus: CatalogSKU[];
}

/** 积分超市里的一张通用积分卡。**总积分要前端自己算** credits + bonusCredits。 */
export interface StoreCard {
  id: number;
  name: string;
  credits: number;
  bonusCredits: number; // 0 合法 = 不赠
  priceCents: number;
  badge: string;
  /** 主推：高亮展示（后台勾选）。 */
  featured?: boolean;
}

/** 我持有的一份会员。planName 查不到时是空串（后端刻意降级，不 500）。 */
export interface MyMembership {
  id: number;
  planName: string;
  kind: string;
  seats: number;
  startsAt: string;
  expiresAt: string;
  status: string;
}

/** 积分按来源分栏的一项。source 是原始标签，中文映射由展示层做。 */
export interface CreditBucket {
  source: string;
  credits: number;
}

export async function getMembershipCatalog(): Promise<CatalogPlan[]> {
  const { data } = await api.get<{ data: CatalogPlan[] }>('/membership/catalog');
  return data.data ?? [];
}

/** 自定义充值配置。关着时只有 enabled=false，其余字段不下发。 */
export interface CustomRechargeConfig {
  enabled: boolean;
  creditsPerYuan?: number;
  minYuan?: number;
  maxYuan?: number;
}

export async function getCustomRecharge(): Promise<CustomRechargeConfig> {
  const { data } = await api.get<CustomRechargeConfig>('/membership/custom-recharge');
  return data;
}

export async function getCreditCards(): Promise<StoreCard[]> {
  const { data } = await api.get<{ data: StoreCard[] }>('/membership/cards');
  return data.data ?? [];
}

export async function getMyMemberships(): Promise<MyMembership[]> {
  const { data } = await api.get<{ data: MyMembership[] }>('/membership/mine');
  return data.data ?? [];
}

export async function getCreditBreakdown(): Promise<CreditBucket[]> {
  const { data } = await api.get<{ data: CreditBucket[] }>('/credits/breakdown');
  return data.data ?? [];
}

// ─── 支付（汇付天下 · 支付宝扫码）───────────────────────────────────────
// 设计依据：docs/superpowers/plans/2026-08-27-membership-phase3b-payment.md

/**
 * 下单入参。**没有金额字段**——金额由服务端从 price_cents 现查，
 * 前端传金额等于把定价权交出去。同理没有「发给谁」：受益人由服务端从
 * teamId + owner 校验推出来。
 */
export interface CreatePayOrderInput {
  itemType: 'membership' | 'credit_card' | 'custom_credits';
  /** custom_credits 不用；传 0。 */
  itemId: number;
  /**
   * 仅 custom_credits：整数元。这是全接口唯一由客户端决定的金额，服务端做范围校验
   * （后台配置的 min/max）与换算，不是定价。
   */
  amountYuan?: number;
  /** 仅团队会员有意义，其余忽略。 */
  seats?: number;
  /** 仅团队会员有意义，用来定位影子账号。 */
  teamId?: number;
}

export interface PayOrder {
  orderNo: string;
  /** 可能是图片(data URI / 图片链接)，也可能是支付链接，渲染方式见 PayQRModal.qrRenderMode。 */
  qrCode: string;
  amountCents: number;
  /**
   * 给用户看的剩余秒数（服务端 15 分钟窗口）。倒计时归零就遮住二维码不让再扫。
   * 0 = 服务端没给（存量订单），前端按自己的默认窗口走。
   */
  expireSeconds?: number;
  /**
   * 轮询该跟到什么时候（服务端 20 分钟硬过期）。**比 expireSeconds 长是故意的**：
   * 用户在 14:59 扫码、15:02 才付完，这笔钱照样到账，前端必须还在轮询才看得到。
   */
  pollSeconds?: number;
}

export type PayOrderStatus = 'no_paid' | 'paid' | 'failed' | 'closed' | 'refunded';

/**
 * 下单失败时后端除了中文 message 还给一个机器可读的 `code`
 * （`payment_not_configured` / `item_unavailable` / …）。前端要按类型换文案，
 * 靠中文做 if 太脆，所以这里把 code 一并抛出去。
 */
export class PayOrderError extends Error {
  code: string;
  constructor(message: string, code: string) {
    super(message);
    this.name = 'PayOrderError';
    this.code = code;
  }
}

export async function createPayOrder(input: CreatePayOrderInput): Promise<PayOrder> {
  try {
    const { data } = await api.post<PayOrder>('/pay/orders', input);
    return data;
  } catch (e) {
    const resp = (e as { response?: { data?: { error?: string; code?: string } } }).response;
    throw new PayOrderError(
      resp?.data?.error || '下单失败，请稍后重试',
      resp?.data?.code || 'internal',
    );
  }
}

export async function getPayOrder(orderNo: string): Promise<{ orderNo: string; status: PayOrderStatus }> {
  const { data } = await api.get<{ orderNo: string; status: PayOrderStatus }>(
    `/pay/orders/${encodeURIComponent(orderNo)}`,
  );
  return data;
}
