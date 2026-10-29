// 生成结果下载到本机失败（sidecar 自动重试 3 次仍失败）时，节点里留的是上游
// 外链。这里负责认出这种节点、挂「未保存到本地」角标和「重试」按钮。
//
// 重试走 sidecar 的 POST /tasks/{id}/repersist。成功时 sidecar 已经把快照里
// 的节点改成本地地址；页面这边把同样的改动落到 store，再补一次局部持久化——与生成结果
// 回填同一套做法（服务端权威写回，前端补齐自己的状态）。
//
// 另一种情况：文件已落本地（media://），但素材记录没写成功（assetId=0）。sidecar 的
// isUnsavedOutput 只认 http(s)，不会给它打 unsaved；repersist 也只搬外链槽位，重试
// 没有意义。这里单独挂「素材未登记」角标说明，不给重试按钮。
import { useEffect, useState, type ReactNode } from 'react';
import { App as AntApp } from 'antd';
import { CloudOff, Loader2 } from 'lucide-react';
import { api, currentProjectId, type CanvasTaskStatus } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { CanvasNode, NodeType } from '@/modules/canvas/editor/types';
import { isLocalMediaUrl, isRemoteMediaUrl } from './localMedia';

// 产物由 sidecar 生成管线写回的节点类型（canvasgen 的 sink 只写这几类）。
const GENERATED_TYPES: ReadonlySet<NodeType> = new Set<NodeType>([
  'gen-image',
  'gen-video',
  'gen-audio',
  'subtitle-erase',
  'video-upscale',
  'video-whitemodel',
]);

export function isUnsavedNode(node: CanvasNode | undefined): boolean {
  if (!node) return false;
  // 快照里的 unsaved 标记只在内容仍是外链时才算数：页面开着重新生成拿到本地地址后，
  // store 里可能残留旧标记（服务端 sink 已经删了），这时不能再挂角标，否则重试会把
  // 新结果换回旧任务的图。
  if (node.settings?.unsaved === true && !isLocalMediaUrl(node.content)) return true;
  // 页面开着时生成结果靠轮询任务回填，任务状态里没有 unsaved（它只写进快照），要等重新
  // 打开项目才读得到。所以对生成类节点按「成功了但内容是外链」认定：桌面端生成结果正常
  // 都是本机地址，只有下载失败才会留下外链。
  return GENERATED_TYPES.has(node.type) && node.status === 'succeeded' && isRemoteMediaUrl(node.content);
}

/** 文件在本机、但素材记录没写成功（sidecar 落盘时 assets 事务失败，assetId 记 0）。 */
export function isUnregisteredNode(node: CanvasNode | undefined): boolean {
  if (!node || !GENERATED_TYPES.has(node.type) || node.status !== 'succeeded') return false;
  return isLocalMediaUrl(node.content) && node.settings?.assetId === 0;
}

/** 与 sidecar 的 ApplyRepersist 同一规则：history 里字符串条目和 {url} 条目都换。 */
export function replaceHistoryUrl(history: unknown, oldUrl: string, newUrl: string): unknown {
  if (!Array.isArray(history)) return history;
  return history.map((entry) => {
    if (entry === oldUrl) return newUrl;
    if (entry && typeof entry === 'object' && (entry as { url?: unknown }).url === oldUrl) {
      return { ...(entry as Record<string, unknown>), url: newUrl };
    }
    return entry;
  });
}

export interface RepersistResult {
  outputUrls: string[];
  assetIds: number[];
}

export async function repersistTask(taskId: number): Promise<RepersistResult> {
  const { data } = await api.post<Partial<RepersistResult>>(`/tasks/${taskId}/repersist`);
  return { outputUrls: data?.outputUrls ?? [], assetIds: data?.assetIds ?? [] };
}

// 快照里有 genTaskId 就用它；没有（页面开着时出的结果）就按节点找回最近一次任务。
async function resolveTaskId(node: CanvasNode): Promise<number | null> {
  const recorded = Number(node.settings?.genTaskId);
  if (Number.isInteger(recorded) && recorded > 0) return recorded;
  const projectId = currentProjectId();
  if (!projectId) return null;
  const { data } = await api.get<{ taskId?: number }>('/canvas-tasks/by-node', {
    params: { projectId, nodeId: node.id },
  });
  return data?.taskId && data.taskId > 0 ? data.taskId : null;
}

export async function retrySaveToLocal(nodeId: string): Promise<void> {
  const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
  if (!node?.content) return;
  const oldUrl = node.content;
  const taskId = await resolveTaskId(node);
  if (!taskId) throw new Error('找不到这次生成的任务记录，无法重试');
  const { outputUrls, assetIds } = await repersistTask(taskId);
  const newUrl = outputUrls[0];
  if (!newUrl) throw new Error('保存到本地失败：sidecar 没有返回本地地址');

  // 等待期间节点可能被删、被重新生成，按最新状态落。
  const latest = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
  if (!latest) return;
  const store = useCanvasStore.getState();
  const history = latest.settings?.history;
  if (latest.content !== oldUrl) {
    // 已经重新生成过：当前内容和标记属于新任务，只把历史里那一版换成本地地址。
    if (Array.isArray(history)) {
      store.updateNodeSettings(nodeId, { history: replaceHistoryUrl(history, oldUrl, newUrl) });
      persistNodeById(nodeId);
    }
    return;
  }
  const settingsPatch: Record<string, unknown> = { unsaved: undefined, genTaskId: undefined };
  if (assetIds[0]) settingsPatch.assetId = assetIds[0];
  if (Array.isArray(history)) settingsPatch.history = replaceHistoryUrl(history, oldUrl, newUrl);
  store.updateNodeSettings(nodeId, settingsPatch);
  store.updateNode(nodeId, { content: newUrl });
  persistNodeById(nodeId);
}

// 落盘失败原因（sidecar 的 output.persistErrors，与 outputUrls 同序），按节点内容对上槽位。
// 同一节点同一内容只查一次；失败不缓存，下次再查。
const persistErrorCache = new Map<string, Promise<string | undefined>>();

type TaskStatusWithErrors = Partial<CanvasTaskStatus> & { taskId?: number };

async function fetchPersistError(node: CanvasNode): Promise<string | undefined> {
  const recorded = Number(node.settings?.genTaskId);
  let st: TaskStatusWithErrors | undefined;
  if (Number.isInteger(recorded) && recorded > 0) {
    st = (await api.get<TaskStatusWithErrors>(`/canvas-tasks/${recorded}`)).data;
  } else {
    // 没有 genTaskId（页面开着时出的结果）：by-node 直接带回最近一次任务的状态。
    const projectId = currentProjectId();
    if (!projectId) return undefined;
    st = (await api.get<TaskStatusWithErrors>('/canvas-tasks/by-node', { params: { projectId, nodeId: node.id } })).data;
  }
  const i = (st?.outputUrls ?? []).indexOf(node.content ?? '');
  return (i >= 0 ? st?.persistErrors?.[i] : undefined) || undefined;
}

export function loadPersistError(node: CanvasNode): Promise<string | undefined> {
  const key = `${node.id}|${node.content ?? ''}`;
  let pending = persistErrorCache.get(key);
  if (!pending) {
    pending = fetchPersistError(node);
    persistErrorCache.set(key, pending);
    pending.catch(() => persistErrorCache.delete(key));
  }
  return pending.catch(() => undefined);
}

/** 测试用：清掉落盘失败原因的查询缓存。 */
export function resetPersistErrorCache(): void {
  persistErrorCache.clear();
}

export function retryErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string } | undefined;
  return e?.response?.data?.error || e?.message || '保存到本地失败';
}

const UNSAVED_TITLE = '生成结果没能下载到本机，现在用的是上游的临时地址，过期后会打不开';
const UNREGISTERED_TITLE =
  '生成结果已保存到本机，但素材记录没有写成功（assetId=0）：存储用量和项目清理都不会算上这个文件。重新生成可恢复。';

/** 节点名字行上的「未保存到本地」/「素材未登记」角标；都不是时返回 null。 */
export function useUnsavedLocalBadge(nodeId: string): ReactNode | null {
  const unsaved = useCanvasStore((s) => isUnsavedNode(s.nodesById.get(nodeId)));
  const unregistered = useCanvasStore((s) => isUnregisteredNode(s.nodesById.get(nodeId)));
  const content = useCanvasStore((s) => s.nodesById.get(nodeId)?.content);
  const readOnly = useCanvasStore((s) => s.readOnly);
  const { message } = AntApp.useApp();
  const [retrying, setRetrying] = useState(false);
  // 落盘失败原因只放 tooltip：超限、链接失效这类原因下重试没有意义，用户得知道。
  const [reason, setReason] = useState<{ content: string | undefined; text: string } | null>(null);

  useEffect(() => {
    if (!unsaved) return;
    const node = useCanvasStore.getState().nodesById.get(nodeId);
    if (!node) return;
    let alive = true;
    void loadPersistError(node).then((text) => {
      if (alive && text) setReason({ content: node.content, text });
    });
    return () => {
      alive = false;
    };
  }, [unsaved, nodeId, content]);

  if (unregistered) {
    return (
      <span
        data-unsaved-local={nodeId}
        data-unsaved-kind="unregistered"
        className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-amber-600/90 text-white text-[11px] leading-none font-medium"
        title={UNREGISTERED_TITLE}
      >
        <CloudOff size={11} />
        素材未登记
      </span>
    );
  }
  if (!unsaved) return null;
  const reasonText = reason && reason.content === content ? reason.text : null;

  const retry = async () => {
    if (retrying) return;
    setRetrying(true);
    try {
      await retrySaveToLocal(nodeId);
      message.success('已保存到本地');
    } catch (err) {
      message.error(retryErrorMessage(err));
    } finally {
      setRetrying(false);
    }
  };

  return (
    <span
      data-unsaved-local={nodeId}
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-amber-600/90 text-white text-[11px] leading-none font-medium"
      title={reasonText ? `${UNSAVED_TITLE}\n原因：${reasonText}` : UNSAVED_TITLE}
    >
      <CloudOff size={11} />
      未保存到本地
      <button
        type="button"
        disabled={retrying || readOnly}
        className="ml-0.5 px-1 py-0.5 rounded bg-white/20 hover:bg-white/30 text-white text-[11px] leading-none border-none cursor-pointer disabled:cursor-default disabled:opacity-60"
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          void retry();
        }}
      >
        {retrying ? <Loader2 size={11} className="animate-spin" aria-label="重试" /> : '重试'}
      </button>
    </span>
  );
}
