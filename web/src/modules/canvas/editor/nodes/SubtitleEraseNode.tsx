import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { useEffect, useState } from 'react';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { useQuery } from '@tanstack/react-query';
import { PlayCircle } from 'lucide-react';
import { useCanvasStore, useConnectedInputs } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { NodeShell } from '../NodeShell';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { VideoPlayer } from './VideoPlayer';
import { useNodeMediaActive } from './useNodeMediaActive';
import {
  currentProjectId,
  fetchCanvasPostprocessConfig,
  pollCanvasTaskUntilDone,
  submitCanvasDesub,
} from '@/shared/api';

// 限时去字幕项是否可选：仅当上游是 seedance 生成的视频、且它的 upstreamExpire
// （素材在上游侧的可访问截止时间，RFC3339）还没过期。跟后端 drama 侧
// isLimitedDesubAvailable 同一条规则——这里只用于前端显隐，真正的权限判定在
// 提交去字幕时由后端权威校验一遍，前端算错也不会导致越权计费。
export function isLimitedAvailable(upstreamExpire: string | undefined, now: Date): boolean {
  if (!upstreamExpire) return false;
  return new Date(upstreamExpire).getTime() > now.getTime();
}

export function SubtitleEraseNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const inputs = useConnectedInputs(id);
  // 「直接上游」= 连进这个节点的第一条视频连线。去字幕天然是单输入操作，不需要
  // 像 gen-video 那样处理多路 @ 引用。
  const srcRef = inputs.refs.find((r) => r.kind === 'video');
  const sourceNodeId = srcRef?.nodeId;
  const sourceUrl = inputs.videos[0];
  const srcNode = useCanvasStore((s) => (sourceNodeId ? s.nodesById.get(sourceNodeId) : undefined));
  const upstreamExpire = srcNode?.settings?.upstreamExpire as string | undefined;

  const cfgQuery = useQuery({
    queryKey: ['canvas-postprocess-config'],
    queryFn: fetchCanvasPostprocessConfig,
    staleTime: 300_000,
  });
  const limited = isLimitedAvailable(upstreamExpire, new Date());
  const options = (cfgQuery.data?.subtitleRemoval ?? []).filter((o) => !o.isTimeLimited || limited);

  const [modelId, setModelId] = useState<string>('');
  useEffect(() => {
    if (!modelId && options[0]) setModelId(options[0].modelId);
    // options 变化时（比如上游连线换了、限时项显隐了）若当前选中的模型不再在
    // 列表里，也回落到第一个可用项，避免悬空选中一个已经不可选的限时项。
    else if (modelId && !options.some((o) => o.modelId === modelId) && options[0]) {
      setModelId(options[0].modelId);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [options]);

  const projectId = currentProjectId();

  const isGenerating = node?.status === 'generating';
  const canSubmit = Boolean(sourceUrl && sourceNodeId && modelId) && !isGenerating;

  const mediaMenu = useMediaNodeMenu({ url: node?.content, kind: 'video', nodeId: id });
  const mediaActive = useNodeMediaActive(id);

  // 只要这个节点持有一个进行中的去字幕任务就恢复/驱动 polling——跟 GenVideoNode
  // 同一套「方案 B」：结果由后端权威写回快照，轮询仅用于前端即时展示，刷新页面
  // 能恢复进行中的任务。
  useEffect(() => {
    if (node?.status === 'generating' && node.taskId) {
      const ctrl = new AbortController();
      let cancelled = false;
      pollCanvasTaskUntilDone(node.taskId, { signal: ctrl.signal })
        .then((finalTask) => {
          if (cancelled) return;
          if (finalTask.status === 'succeeded') {
            const url = finalTask.outputUrls?.[0];
            if (url) {
              // 新结果属于这次任务：清掉上次下载失败留下的 unsaved / genTaskId 标记。
              updateNodeSettings(id, { kind: 'video', unsaved: undefined, genTaskId: undefined });
              updateNode(id, { content: url, status: 'succeeded', error: undefined, taskId: undefined });
            } else {
              updateNode(id, { status: 'failed', error: '上游未返回视频', taskId: undefined });
            }
          } else {
            updateNode(id, { status: 'failed', error: finalTask.error || '去字幕失败', taskId: undefined });
          }
          persistNodeById(id);
        })
        .catch((err: any) => {
          if (cancelled || err?.message === 'aborted') return;
          updateNode(id, { status: 'failed', error: err?.message || '轮询失败', taskId: undefined });
          persistNodeById(id);
        });
      return () => {
        cancelled = true;
        ctrl.abort();
      };
    }
    return undefined;
  }, [node?.status, node?.taskId, id, updateNode, updateNodeSettings]);

  if (!node) return null;

  const handleDesub = async () => {
    if (!sourceNodeId || !canSubmit) return;
    updateNode(id, { status: 'generating', error: undefined, taskId: undefined });
    persistNodeById(id);
    try {
      const r = await submitCanvasDesub({
        projectId: projectId!,
        nodeId: id,
        sourceNodeId,
        modelId,
      });
      updateNode(id, { taskId: r.taskId });
      persistNodeById(id);
    } catch (err: any) {
      const msg = err?.response?.data?.error || err?.message || '提交失败';
      updateNode(id, { status: 'failed', error: msg, taskId: undefined });
      persistNodeById(id);
    }
  };

  return (
    <>
      <NodeShell node={node} inputs={1} outputs={1} minWidth={280} minHeight={260} titleExtra={mediaMenu.badge}>
        <div className="w-full h-full p-2 flex flex-col gap-2">
          <div className="relative flex-1 rounded-2xl bg-popover/30 border border-border/60 flex items-center justify-center overflow-hidden min-h-0">
            {/* 已有产物：展示去字幕结果，可走 useMediaNodeMenu 保存/发送。 */}
            {node.content ? (
              <VideoPlayer
                active={mediaActive}
                src={node.content} crossOrigin={mediaCrossOrigin(node.content)}
                className="w-full h-full object-contain bg-black"
                playsInline
                preload="metadata"
                onContextMenu={(e) => {
                  e.preventDefault();
                  mediaMenu.onContextMenu(e);
                }}
              />
            ) : sourceUrl && mediaActive ? (
              // 还没有产物：展示上游视频缩略，纯预览。时长不从这里读（见上方预估注释）。
              // 无 controls，没有可保护的控制条——mousedown 放行冒泡，画面可拖动节点。
              // 离开视口（含一屏边距）就不挂，免得屏幕外的节点也去拉源视频（P15）。
              <video
                src={sourceUrl} crossOrigin={mediaCrossOrigin(sourceUrl)}
                className="w-full h-full object-contain bg-black"
                muted
                playsInline
                preload="metadata"
              />
            ) : (
              <PlayCircle size={40} className="text-muted-foreground/60" />
            )}

            {isGenerating && (
              <div
                className="absolute inset-0 bg-black/35 flex items-center justify-center pointer-events-none"
                style={{ backdropFilter: 'blur(1px)' }}
              >
                <div className="text-white/85 text-[11px] font-mono px-2 py-1 rounded bg-black/40">
                  处理中…
                </div>
              </div>
            )}
          </div>

          {/* 有产物之后就不再需要选模型面板——去字幕是一次性操作,不像 gen-video
              那样支持反复重跑同一节点。 */}
          {!node.content && (
            <div className="shrink-0 flex flex-col gap-1.5 px-1 pb-1">
              {!sourceUrl && (
                <div className="text-[11px] text-muted-foreground text-center">请连接一个视频</div>
              )}
              {sourceUrl && options.length === 0 && (
                <div className="text-[11px] text-muted-foreground text-center">未配置去字幕模型</div>
              )}
              {sourceUrl && options.length > 0 && (
                // max-h 给到 180px:后台常配 3~4 个模型,原来的 max-h-24(96px) 一定要滚才能看全
                // (线上反馈「选项太多还要滚,不够智能」)。配更多时仍可滚,不失控。
                <div className="flex flex-col gap-1 max-h-[180px] overflow-auto custom-scrollbar">
                  {options.map((o) => (
                    <button
                      key={o.modelId}
                      type="button"
                      onMouseDown={(e) => e.stopPropagation()}
                      onClick={(e) => {
                        e.stopPropagation();
                        setModelId(o.modelId);
                      }}
                      className={`text-left px-2 py-1 rounded-md text-[11px] border-none cursor-pointer transition-colors ${
                        modelId === o.modelId
                          ? 'bg-accent text-foreground'
                          : 'bg-popover hover:bg-accent text-sidebar-foreground'
                      }`}
                    >
                      <div>{o.label}</div>
                      {o.subtitle && <div className="text-[10px] opacity-70">{o.subtitle}</div>}
                    </button>
                  ))}
                </div>
              )}

              <div className="flex items-center gap-1.5">
                <div className="flex-1" />
                <button
                  type="button"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    void handleDesub();
                  }}
                  disabled={!canSubmit}
                  className="px-3 py-1.5 rounded-md bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-40 disabled:cursor-not-allowed text-[12px] border-none cursor-pointer"
                  title={!sourceUrl ? '请连接一个视频' : isGenerating ? '处理中…' : '去字幕'}
                >
                  {isGenerating ? '处理中…' : '去字幕'}
                </button>
              </div>
            </div>
          )}

          {node.error && (
            <div className="shrink-0 text-[11px] text-red-400 text-center px-1">{node.error}</div>
          )}
        </div>
      </NodeShell>
      {mediaMenu.overlay}
    </>
  );
}
