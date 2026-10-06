import { useEffect, useState } from "react";

// 【画布集成适配·阶段二唯一注入点】宿主（自由画布）向导演台注入的能力集合。
// 设计约束：
// 1. 本文件不 import 导演台任何其他模块（directorStore 会反向 import 它，
//    引入依赖会成环），状态类型一律用 unknown，由调用方收窄。
// 2. 适配器缺席（独立运行 / 单元测试）时，所有挂钩点必须回落上游原行为
//    （localStorage / dataURL / objectURL），desk 独立可用是硬约束。
// 3. 实现方是画布侧 DirectorDeskNode——上传/落库/限额的具体策略（30MB、
//    500KB 告警、assets 行语义）全部收在实现侧，desk 只透传错误 message。
export interface DirectorHostAdapter {
  /** 读取当前节点已持久化的导演台状态（画布快照 settings.directorProject）。
   *  返回 null 表示尚无持久化状态（新节点）。同步调用——画布快照已在内存。 */
  readState(): unknown;
  /** 持久化导演台状态。实现侧自带 debounce 落库（scheduleNodePatch 1.5s），
   *  desk 在每次 store 变更时直接调用即可。 */
  writeState(state: unknown): void;
  /** 机位截图落对象存储。入参 dataURL，成功返回签名 URL（进镜头列表/工程 JSON，
   *  落库时后端自动收敛 media://）。失败时 reject，由调用方走既有错误展示。 */
  storeCapture(dataUrl: string, fileName: string): Promise<string>;
  /** 本地导入的模型/全景文件落对象存储，成功返回签名 URL。
   *  大小限额（30MB）在实现侧校验，超限 reject 带中文提示。 */
  storeAssetFile(file: Blob, fileName: string): Promise<string>;
  /** 资产 URL 的加载期解析：按 object_key（= 签名 URL 去 query）过 Cache API，
   *  大模型只有第一次真下载；缓存不可用/跨域失败时原样返回 url。 */
  resolveAssetUrl(url: string): Promise<string>;
  /** 用户级云端模型库（跨项目「我的模型」）。与 storeAssetFile 是两条线：
   *  storeAssetFile 走项目级素材（全景图等，随项目清理），模型库是用户级
   *  目录（删项目不影响，上限默认 30 个由服务端 settings 控制）。 */
  listUserModels(): Promise<DirectorUserModel[]>;
  uploadUserModel(file: Blob, fileName: string): Promise<DirectorUserModel>;
  /** true=已删除（含引用确认后强删）；false=用户在确认弹窗里取消。
   *  引用检查与确认交互都在实现侧（画布），desk 只管刷新列表。 */
  deleteUserModel(id: number): Promise<boolean>;
  /** 平台预置模型目录（阶段三·管理员在后台配置的分类与模型）。分类是后台
   *  数据（可增删改名），desk 的分类 tab 按返回值动态渲染，不再依赖上游
   *  写死的四个分类；「我的模型」不在其中，由 desk 自己拼在尾部。 */
  listPresetLibrary(): Promise<DirectorPresetCategory[]>;
}

export interface DirectorUserModel {
  id: number;
  name: string;
  fileName: string;
  url: string;
}

export interface DirectorPresetModel {
  id: string;
  name: string;
  fileName: string;
  url: string;
  thumbUrl?: string;
}

export interface DirectorPresetCategory {
  id: string;
  label: string;
  items: DirectorPresetModel[];
}

let hostAdapter: DirectorHostAdapter | null = null;

export function setDirectorHostAdapter(adapter: DirectorHostAdapter | null) {
  hostAdapter = adapter;
}

export function getDirectorHostAdapter(): DirectorHostAdapter | null {
  return hostAdapter;
}

/** 在渲染前解析资产 URL（Cache API 优先）。适配器缺席时同步返回原 URL；
 *  解析期间返回 null，调用方渲染占位/空（模型本来就是异步出场）。 */
export function useResolvedAssetUrl(url: string | null): string | null {
  const adapter = hostAdapter;
  const [resolved, setResolved] = useState<string | null>(() => (adapter && url ? null : url));

  useEffect(() => {
    if (!url) {
      setResolved(null);
      return undefined;
    }
    const currentAdapter = hostAdapter;
    if (!currentAdapter) {
      setResolved(url);
      return undefined;
    }

    let cancelled = false;
    setResolved(null);
    currentAdapter
      .resolveAssetUrl(url)
      .then((next) => {
        if (!cancelled) setResolved(next);
      })
      .catch(() => {
        if (!cancelled) setResolved(url);
      });

    return () => {
      cancelled = true;
    };
  }, [url]);

  return resolved;
}
