import axios, { AxiosInstance } from 'axios';
import { isApiWriteAllowed } from './apiGuards';
import { buildHttpUrl } from './server';
import { currentSidecarSession } from './sidecarSession';

// 桌面端：所有 REST 请求直连本机 sidecar（spec D3）。地址和 token 来自本次启动的
// 会话（./sidecarSession → server.ts 的 getServerInfo）。端口每次启动、每次 sidecar
// 重启都不同，所以 baseURL 和 token 在每个请求里现取，不在模块加载时固化。
//
// 与参考仓的差异：没有登录（spec X4），所以没有 localStorage 里的登录 token、没有
// 「401 → 清会话跳 /login」、没有团队身份自愈。401 只可能是 token 对不上（sidecar
// 换了一次而页面没重载），原样抛给调用方。
export const api: AxiosInstance = axios.create({
  headers: { 'Content-Type': 'application/json' },
});

api.interceptors.request.use(async (config) => {
  // 每个请求现取会话（getServerInfo 有缓存，sidecar 重启事件会清掉它）：P8 的壳会自动
  // 重启 sidecar，端口和 token 都会变。sidecar 正在重启时这里会等到它就绪或失败。
  const session = await currentSidecarSession();
  config.baseURL = session ? buildHttpUrl(session, '/api/v1') : '/api/v1';
  const token = session?.token ?? '';
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
  // 参考仓的单价说明。桌面端不计费（spec X3），界面不显示；字段保留只为兼容 sidecar 返回。
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

// 提交一个画布素材（图片/视频/音频）做 Seedance 合规校验。幂等——已通过的直接回原状态。
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

// ========== 自由画布 · 脚本节点「剧本生成分镜」 ==========
// 风格是后台配置的展示名（映射到服务端 skill，前端不感知 skillName）；
// 任务恒异步：提交 → 轮询 → succeeded 拿 candidate.json 原文自行导入。

export interface CanvasScriptModelCandidate {
  modelId: string;
  displayName: string;
  /** 参考仓的预冻结积分。桌面端 sidecar 恒为 0、界面不显示（spec X3），字段保留只为兼容。 */
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
