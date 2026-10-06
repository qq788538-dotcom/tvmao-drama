// spec S5：生成结果下载到本机失败（sidecar 自动重试 3 次仍失败）时，节点里留的是上游
// 外链。这里负责认出这种节点、挂「未保存到本地」角标和「重试」按钮。
//
// 重试走 sidecar 的 POST /tasks/{id}/repersist（P1c Task 5）。成功时 sidecar 已经把快照里
// 的节点改成本地地址；页面这边把同样的改动落到 store，再补一次局部持久化——与生成结果
// 回填同一套做法（服务端权威写回，前端补齐自己的状态）。
import { useState, type ReactNode } from 'react';
import { App as AntApp } from 'antd';
import { CloudOff, Loader2 } from 'lucide-react';
import { api, currentProjectId } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { CanvasNode, NodeType } from '@/modules/canvas/editor/types';
import { isRemoteMediaUrl } from './localMedia';

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
  if (node.settings?.unsaved === true) return true;
  // 页面开着时生成结果靠轮询任务回填，任务状态里没有 unsaved（它只写进快照），要等重新
  // 打开项目才读得到。所以对生成类节点按「成功了但内容是外链」认定：桌面端生成结果正常
  // 都是本机地址，只有下载失败才会留下外链。
  return GENERATED_TYPES.has(node.type) && node.status === 'succeeded' && isRemoteMediaUrl(node.content);
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

export function retryErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string } | undefined;
  return e?.response?.data?.error || e?.message || '保存到本地失败';
}

/** 节点名字行上的「未保存到本地」角标；节点已在本机时返回 null。 */
export function useUnsavedLocalBadge(nodeId: string): ReactNode | null {
  const unsaved = useCanvasStore((s) => isUnsavedNode(s.nodes.find((n) => n.id === nodeId)));
  const readOnly = useCanvasStore((s) => s.readOnly);
  const { message } = AntApp.useApp();
  const [retrying, setRetrying] = useState(false);
  if (!unsaved) return null;

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
      title="生成结果没能下载到本机，现在用的是上游的临时地址，过期后会打不开"
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
