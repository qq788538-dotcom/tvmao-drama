import { Suspense, lazy, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Camera } from 'lucide-react';
import { Modal, message } from 'antd';
import { AxiosError } from 'axios';
import { DEFAULT_SIZES, useCanvasStore } from '../../store';
import { NodeShell } from '../../NodeShell';
import {
  flushNodePatch,
  persistNodeById,
  scheduleNodePatch,
} from '../../persistence/canvasMutations';
import { layoutDropPoints } from '../../utils/fileDrop';
import {
  deleteDirectorModel,
  listDirectorLibrary,
  listDirectorModels,
  uploadAsset,
  uploadDirectorModel,
} from '@/shared/api';
import {
  getDirectorHostAdapter,
  setDirectorHostAdapter,
  type DirectorHostAdapter,
} from './desk/editor/io/hostAdapter';

// 3D 导演台本体（desk/ 是参考仓 storyai-3d-director-desk 的整体 copy，除 CSS
// 作用域与 hostAdapter 挂钩外零改动）连同 three.js/R3F 一起走懒加载 chunk——
// 只有用户真点开节点才下载约 600KB+ 的 3D 依赖，未用到导演台的画布用户零成本。
const DirectorDeskApp = lazy(() => import('./desk/App'));

// desk 的宿主通信桥（hostBridge.ts）设计给 iframe 嵌入用，全部消息走
// window.parent.postMessage。融合模式下页面不在 iframe 里，window.parent ===
// window——消息发给自己窗口，画布侧在同一 window 上监听/回发即可，origin 校验
//（=== window.location.origin）天然通过。desk 源码因此一行不用改。
const MSG_READY = 'storyai:director-desk-ready';
const MSG_CLOSE = 'storyai:director-desk-close';
const MSG_SESSION = 'storyai:director-desk-session';
const MSG_CAPTURES = 'storyai:director-desk-captures-sent';

// 阶段二拍板的两个额度（设计文档 §7）：导入单文件 30MB（弱网下 50MB 首载可达
// 1 分钟，价值场景太少，超出直接拒）；场景 JSON 超 500KB 只告警不拦——正常是
// 几十 KB，到这个量级说明出现 dataURL 泄漏或未知放大器，要可见但不能打断保存。
const MAX_IMPORT_BYTES = 30 * 1024 * 1024;
const STATE_WARN_BYTES = 500 * 1024;
const ASSET_CACHE_NAME = 'director-desk-assets-v1';

// 与 NodeShell 的 currentProjectId 同源逻辑：uploadAsset 需要 projectId 归属
//（删项目时 ProjectCleaner 按 project_id 收割 assets 行，归属错了清理就漏）。
function currentProjectId(): number | undefined {
  if (typeof window === 'undefined') return undefined;
  const m = /\/canvas\/(\d+)/.exec(window.location.pathname);
  return m ? parseInt(m[1], 10) : undefined;
}

function dataUrlToBlob(dataUrl: string): Blob {
  const [head, body] = dataUrl.split(',');
  const mime = /data:([^;]+)/.exec(head)?.[1] ?? 'image/png';
  const bin = atob(body);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  return new Blob([bytes], { type: mime });
}

interface CapturePayloadItem {
  dataUrl?: unknown;
  fileName?: unknown;
}

// 画布侧的适配器实现：desk 只声明能力（hostAdapter.ts 契约），上传/落库/限额
// 策略全部收在这里。assetIdByUrl 记住本次会话上传产物的 assets 行 id，发送到
// 画布铺 input-image 时补进 settings.assetId（「保存到素材库」桥接可用）；刷新
// 后映射丢失也无碍——save-from-url 路径以 URL 为身份，不依赖 assetId。
// AxiosError 的 message 是「Request failed with status code 409」这类英文模板，
// 服务端的业务文案（如「存储空间已满，请清理后重试」）在 response.data.error 里。
// desk 各处错误展示（setCaptureError / importError）拿到的是 Error.message，
// 不在这里翻译的话用户只能看到无意义的状态码文案。
function apiErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof AxiosError) {
    const serverText = (err.response?.data as { error?: unknown } | undefined)?.error;
    if (typeof serverText === 'string' && serverText) return serverText;
  }
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

function createHostAdapter(nodeId: string, assetIdByUrl: Map<string, number>): DirectorHostAdapter {
  async function uploadBlob(blob: Blob, fileName: string): Promise<string> {
    const file =
      blob instanceof File
        ? blob
        : new File([blob], fileName, { type: blob.type || 'application/octet-stream' });
    try {
      const asset = await uploadAsset(file, currentProjectId());
      assetIdByUrl.set(asset.url, asset.id);
      return asset.url;
    } catch (err) {
      throw new Error(apiErrorMessage(err, '上传失败，请重试'));
    }
  }

  return {
    readState() {
      const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
      return (node?.settings as Record<string, unknown> | undefined)?.directorProject ?? null;
    },
    writeState(state) {
      try {
        const size = JSON.stringify(state).length;
        if (size > STATE_WARN_BYTES) {
          // key 去重：desk 每次变更都会写，1.5s debounce 节奏下不去重会刷屏。
          message.warning({
            content: `3D 场景数据已达 ${(size / 1024).toFixed(0)}KB，超出预期量级，请反馈给管理员`,
            key: 'director-desk-state-size',
          });
          console.warn(
            `[director-desk] settings.directorProject 序列化 ${size}B 超过告警阈值 ${STATE_WARN_BYTES}B`,
          );
        }
      } catch {
        // 尺寸测量失败不阻塞落库。
      }
      // 内存里的画布节点必须同步更新：readState 读的就是它（desk 每次打开
      // openScopedScene 都从这里重读，并立刻 writeState 回写）。只发 patch 不改
      // store 的话，同一会话里关掉再打开，读到的是快照加载时的旧场景，紧接着又把
      // 旧场景回写服务端——刚才的编辑整体丢失。
      useCanvasStore.getState().updateNodeSettings(nodeId, { directorProject: state });
      // debounce 1.5s / 前后端深合并 / 签名 URL 落库自动收敛 media:// 全部由
      // 画布现有 mutation 链路提供。DirectorState 的集合全是数组（深合并对数组
      // 整体覆盖），无动态键嵌套对象，深合并语义安全（阶段二方案已核实）。
      scheduleNodePatch(nodeId, { settings: { directorProject: state } });
    },
    async storeCapture(dataUrl, fileName) {
      return uploadBlob(dataUrlToBlob(dataUrl), fileName);
    },
    async storeAssetFile(file, fileName) {
      if (file.size > MAX_IMPORT_BYTES) {
        throw new Error('文件不能超过 30MB');
      }
      return uploadBlob(file, fileName);
    },
    async listUserModels() {
      return listDirectorModels();
    },
    async listPresetLibrary() {
      // 后端数值 id → desk 契约的字符串 id（desk 侧只当 tab key / react key 用）。
      const categories = await listDirectorLibrary();
      return categories.map((category) => ({
        id: `preset-${category.id}`,
        label: category.name,
        items: category.models.map((model) => ({
          id: `preset-model-${model.id}`,
          name: model.name,
          fileName: model.fileName,
          url: model.url,
          thumbUrl: model.thumbUrl || undefined,
        })),
      }));
    },
    async uploadUserModel(file, fileName) {
      // 30MB 前端预检省一次白传；服务端同样校验（上限/大小都以服务端为准）。
      if (file.size > MAX_IMPORT_BYTES) {
        throw new Error('文件不能超过 30MB');
      }
      const f =
        file instanceof File
          ? file
          : new File([file], fileName, { type: file.type || 'application/octet-stream' });
      try {
        return await uploadDirectorModel(f);
      } catch (err) {
        // 409 有两种业务分支，靠 body.error 区分：limit_reached=条数上限（值以
        // 服务端返回为准，settings 可调别写死）；其余（如存储配额已满）服务端
        // 直接给中文文案，原样透出。
        if (err instanceof AxiosError && err.response?.status === 409) {
          const data = err.response.data as { error?: unknown; limit?: unknown };
          if (data?.error === 'limit_reached') {
            const limit = Number(data.limit ?? 30);
            throw new Error(`模型库已满（上限 ${limit} 个），请删除后再上传`);
          }
        }
        throw new Error(apiErrorMessage(err, '上传失败，请重试'));
      }
    },
    async deleteUserModel(modelId) {
      const first = await deleteDirectorModel(modelId);
      if (first.deleted) return true;
      // 有项目引用 → 用户确认后带 force 重试；取消则不动。7 天孤儿宽限
      // 天然是反悔窗口。
      const confirmed = await new Promise<boolean>((resolve) => {
        Modal.confirm({
          title: '删除模型',
          content: `有 ${first.referencedProjects} 个项目正在使用该模型，删除后这些场景将无法加载。确定删除？`,
          okText: '删除',
          okButtonProps: { danger: true },
          cancelText: '取消',
          onOk: () => resolve(true),
          onCancel: () => resolve(false),
        });
      });
      if (!confirmed) return false;
      const second = await deleteDirectorModel(modelId, true);
      return second.deleted;
    },
    resolveAssetUrl: resolveCachedAssetUrl,
  };
}

// 去 query 后的稳定缓存键 → blob URL。同一资产整个会话只造一个 blob URL：
// R3F 的 useLoader 按 URL 字符串缓存解析结果（suspend-react，永不过期），每次
// 打开导演台都 createObjectURL 一个新地址的话，既命中不了 useLoader 缓存（每次
// 重新解析整只模型），旧 blob 与旧解析结果又永远留在内存里——开关几次面板就是
// 几份 30MB 级模型泄漏。失败不入表，下次重试。
const resolvedAssetUrls = new Map<string, Promise<string>>();

async function fetchCachedAssetBlobUrl(url: string, cacheKey: string): Promise<string> {
  const cache = await caches.open(ASSET_CACHE_NAME);
  let resp = await cache.match(cacheKey);
  if (!resp) {
    const fetched = await fetch(url);
    if (!fetched.ok) throw new Error(`asset fetch ${fetched.status}`);
    await cache.put(cacheKey, fetched.clone());
    resp = fetched;
  }
  return URL.createObjectURL(await resp.blob());
}

// 签名 URL 的 query 每次变、路径含 object_key 恒定——去 query 即稳定缓存
// 键，前端无需真提取 object_key。任何一步失败（无 Cache API、跨域 fetch
// 被拒、配额满）都原样回落直连 URL：缓存只是加速，不是功能。export 仅为测试。
export async function resolveCachedAssetUrl(url: string): Promise<string> {
  if (typeof caches === 'undefined') return url;
  if (url.startsWith('data:') || url.startsWith('blob:')) return url;
  const cacheKey = url.split('?')[0];
  let pending = resolvedAssetUrls.get(cacheKey);
  if (!pending) {
    pending = fetchCachedAssetBlobUrl(url, cacheKey);
    resolvedAssetUrls.set(cacheKey, pending);
    pending.catch(() => resolvedAssetUrls.delete(cacheKey));
  }
  try {
    return await pending;
  } catch {
    return url;
  }
}

export function DirectorDeskNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === id));
  // 面板开合是纯 UI 状态（同 ScriptNode）：刷新画布后默认收起。3D 场景阶段二起
  // 持久化在本节点 settings.directorProject（走画布快照链路），localStorage 仅在
  // desk 独立运行（无 adapter）时作为回落。
  const [open, setOpen] = useState(false);
  const assetIdByUrlRef = useRef<Map<string, number>>(new Map());
  const adapterRef = useRef<DirectorHostAdapter | null>(null);

  // 节点卸载时若全局 adapter 还是本节点的，摘掉——防止已删节点的 adapter 残留，
  // 让后续 desk 读写打到不存在的节点上。
  useEffect(
    () => () => {
      if (adapterRef.current && getDirectorHostAdapter() === adapterRef.current) {
        setDirectorHostAdapter(null);
      }
    },
    [],
  );

  useEffect(() => {
    if (!open) return;
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      const type = (e.data as { type?: string } | null)?.type;
      if (type === MSG_READY) {
        // ready 在 StrictMode 下会因 effect 双跑发两次；session 是幂等的
        //（openScopedScene 同一 scopeId 重复执行结果一致），重复响应无害。
        // instanceId=节点 ID：同画布多个导演台节点各自独立场景。
        window.postMessage(
          { type: MSG_SESSION, payload: { instanceId: id, theme: 'dark' } },
          window.location.origin,
        );
        return;
      }
      if (type === MSG_CAPTURES) {
        const payload = (e.data as { payload?: { captures?: CapturePayloadItem[] } }).payload;
        const items = Array.isArray(payload?.captures) ? payload!.captures! : [];
        const captures = items.filter(
          (c): c is { dataUrl: string; fileName?: string } =>
            typeof c.dataUrl === 'string' && /^https?:/i.test(c.dataUrl),
        );
        // dataURL 一律拒收：只有上传失败回落的截图才会以 data: 形态出现，放行会
        // 把 MB 级 base64 写进画布快照（dataURL 禁入库硬规则，设计文档 §4.3）。
        if (items.length > 0 && captures.length === 0) {
          message.warning('截图尚未上传成功，无法发送到画布');
          return;
        }
        const store = useCanvasStore.getState();
        const desk = store.nodes.find((n) => n.id === id);
        if (!desk || captures.length === 0) return;

        // 排布对齐 fileDrop 的批量语义：导演台右侧 80px 起、每行 4 个铺开——
        // 「发送全部」一次可能来 12 张，扑克堆叠式会叠成一摞（fileDrop.ts 注释
        // 点名过这个坑）。layoutDropPoints 吃/返回的都是中心点，与 addNode 一致。
        const size = DEFAULT_SIZES['input-image'];
        const origin = {
          x: desk.x + desk.width + 80 + size.width / 2,
          y: desk.y + size.height / 2,
        };
        const points = layoutDropPoints(
          captures.map(() => size),
          origin,
        );
        let spawned = 0;
        captures.forEach((capture, i) => {
          // readOnly 画布下 addNode 返回空串，必须判（store 约定）。
          const newId = store.addNode('input-image', points[i]);
          if (!newId) return;
          store.updateNode(newId, { content: capture.dataUrl, status: 'succeeded' });
          const assetId = assetIdByUrlRef.current.get(capture.dataUrl);
          store.updateNodeSettings(newId, {
            ...(typeof capture.fileName === 'string' && capture.fileName
              ? { name: capture.fileName.replace(/\.png$/i, '') }
              : {}),
            ...(assetId ? { assetId } : {}),
            mimeType: 'image/png',
          });
          // updateNode/updateNodeSettings 只改内存不广播 op，内容补齐必须显式
          // persist（canvasMutations 既定语义；addNode 广播的 upsert 里还没有
          // content/settings）。顺序照 batchVideo.ts 的样板。
          persistNodeById(newId);
          store.addConnection(id, newId);
          spawned += 1;
        });
        if (spawned > 0) {
          message.success(`已发送 ${spawned} 张截图到画布`);
        }
        return;
      }
      if (type === MSG_CLOSE) {
        // 关面板前排干 debounce 中的场景落库——用户常「摆完就关」，1.5s 窗口内
        // 关掉会丢最后一笔改动。
        void flushNodePatch(id);
        setOpen(false);
      }
    };
    // 监听在 portal 渲染的同一 commit 里挂上；desk 的 ready 经 postMessage
    // 异步派发，不存在「先发后听」的竞态。
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, [open, id]);

  if (!node) return null;

  const openPanel = () => {
    // adapter 必须先于 desk chunk 挂载注册：desk 模块首次 import 时 store 初始化
    // 就会读一次持久化状态，注册晚了首帧会走 localStorage 回落读到空场景。
    const adapter = createHostAdapter(id, assetIdByUrlRef.current);
    adapterRef.current = adapter;
    setDirectorHostAdapter(adapter);
    setOpen(true);
  };

  return (
    <NodeShell node={node} inputs={0} outputs={1} minWidth={260} minHeight={200}>
      <div className="flex-1 flex flex-col p-3 min-h-0">
        <div className="flex-1 flex flex-col items-center justify-center gap-2">
          <Camera size={28} className="text-muted-foreground" aria-hidden />
          <span className="text-[11px] text-muted-foreground text-center leading-relaxed">
            3D 摆位预演：角色 / 机位 / 全景背景
            <br />
            机位截图可用作生图参考
          </span>
        </div>
        {/* 显式 bg + border-none：原生 button 的 UA 浅灰底在暗色画布里是刺眼白块
            （同 ScriptNode 的处理）。 */}
        <button
          type="button"
          data-testid="director-desk-open-panel"
          onClick={openPanel}
          className="w-full flex items-center justify-center py-2 bg-muted hover:bg-accent text-foreground text-xs rounded-lg transition-colors border-none cursor-pointer"
        >
          打开 3D 导演台 →
        </button>
      </div>
      {open &&
        createPortal(
          // data-director-desk-open 是画布快捷键的停用标记（CanvasEditor 的
          // onKey 见到它直接返回）——desk 自己在 window 上监听 Ctrl+C/V/Z，
          // 不停用画布侧的话同一次按键会双触发（画布撤销节点 + desk 撤销场景）。
          //
          // 指针类事件必须在包裹层截断：createPortal 只改 DOM 挂载点，React 合成
          // 事件仍沿组件树冒泡回 NodeShell / CanvasEditor——不拦的话在导演台里
          // 右键会弹出画布节点的「删除」兜底菜单（线上实拍到），点击/滚轮同理会
          // 穿透成节点选中拖拽与画布缩放。只 stopPropagation 不 preventDefault，
          // desk 内部各组件自己的右键/滚轮行为不受影响。
          <div
            className="director-desk-root"
            data-theme="dark"
            data-director-desk-open
            style={{ position: 'fixed', inset: 0, zIndex: 50 }}
            onContextMenu={(e) => e.stopPropagation()}
            onMouseDown={(e) => e.stopPropagation()}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
            onWheel={(e) => e.stopPropagation()}
          >
            <Suspense
              fallback={
                <div
                  className="w-full h-full flex items-center justify-center text-sm"
                  style={{ background: '#090909', color: '#888' }}
                >
                  正在加载 3D 导演台…
                </div>
              }
            >
              <DirectorDeskApp />
            </Suspense>
          </div>,
          document.body,
        )}
    </NodeShell>
  );
}
