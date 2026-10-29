import { useReconcileCanvasTask } from './useReconcileCanvasTask';
import { useEffect, useState } from 'react';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { useQuery } from '@tanstack/react-query';
import { ChevronDown, PlayCircle } from 'lucide-react';
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
  submitCanvasUpscale,
  submitCanvasWhiteModel,
  type CanvasPostprocessConfig,
  type CanvasUpscaleModelOption,
  type SubmitCanvasTieredInput,
} from '@/shared/api';

// 「模型 + 分辨率档位」类视频后处理节点：视频转高清、视频转白模。
//
// 两者只差「后台配的是哪些模型、能不能选模型、按钮叫什么」，所以共用这一份实现，差异收在
// TieredVariant 里。
// 为什么不像去字幕那样复制一份：去字幕与转高清有实质差异（选模型 vs 选档位、限时直连分支），
// 所以刻意分两份；转白模与转高清没有任何实质差异，复制只会让后续修正（例如档位列表
// max-h）漏改一边。
//
// 与去字幕的实质差异只有三点：
//   1. 核心选项是「分辨率档位」——档位由后端从模型 uiSchema 读出下发，换模型档位跟着换。
//      转高清后台配多个模型（upscaleModels），节点底部有模型下拉；转白模仍是单模型，不显示下拉；
//   2. 没有「限时上游直连」分支，只吃对象存储里已落地的视频；
//   3. 提交要带 resolution（档位）。
interface TieredVariant {
  /** 可选模型（各带档位），顺序即下拉框顺序，默认选第一个；空 = 未配置。 */
  modelsOf: (cfg: CanvasPostprocessConfig) => CanvasUpscaleModelOption[];
  /** 是否显示模型下拉。只配一个模型也显示——让用户看得到用的是哪个模型。 */
  pickModel: boolean;
  submit: (input: SubmitCanvasTieredInput) => Promise<{ taskId: number; status: string }>;
  /** 提交按钮文案，同时是轮询失败时的默认错误前缀（「转高清失败」）。 */
  action: string;
  unconfigured: string;
}

const UPSCALE: TieredVariant = {
  modelsOf: (c) => c.upscaleModels ?? [],
  pickModel: true,
  submit: (input) => submitCanvasUpscale(input),
  action: '转高清',
  unconfigured: '未配置转高清模型',
};

const WHITE_MODEL: TieredVariant = {
  // 单模型包成一项列表，走同一套选择逻辑；不显示下拉，label 用不到。
  modelsOf: (c) =>
    c.whiteModelModel
      ? [{ modelId: c.whiteModelModel, label: c.whiteModelModel, resolutions: c.whiteModelResolutions ?? [] }]
      : [],
  pickModel: false,
  submit: (input) => submitCanvasWhiteModel(input),
  action: '转白模',
  unconfigured: '未配置白模模型',
};

export function VideoUpscaleNode({ id }: { id: string }) {
  return <TieredVideoNode id={id} variant={UPSCALE} />;
}

export function VideoWhiteModelNode({ id }: { id: string }) {
  return <TieredVideoNode id={id} variant={WHITE_MODEL} />;
}

function TieredVideoNode({ id, variant }: { id: string; variant: TieredVariant }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  // 兜底对账:节点卡在 generating 且丢 taskId 时按 nodeId 找回任务号(useReconcileCanvasTask 顶部注释)。
  useReconcileCanvasTask(id);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);
  const inputs = useConnectedInputs(id);
  // 天然是单输入操作，取连进来的第一条视频线即可。
  const srcRef = inputs.refs.find((r) => r.kind === 'video');
  const sourceNodeId = srcRef?.nodeId;
  const sourceUrl = inputs.videos[0];

  const cfgQuery = useQuery({
    queryKey: ['canvas-postprocess-config'],
    queryFn: fetchCanvasPostprocessConfig,
    staleTime: 300_000,
  });
  const models = cfgQuery.data ? variant.modelsOf(cfgQuery.data) : [];

  // 模型与档位都只是本地选择态、不存进节点：面板只在出产物前可见，产物出来就收起，
  // 没有需要跨刷新保留的东西；刷新后回到默认（第一个模型的第一档）。
  const [pickedModelId, setPickedModelId] = useState<string>('');
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  useEffect(() => {
    // 默认第一个；后台删掉了当前选中的模型也回落第一个，避免拿白名单外的 modelId 去提交。
    if (models[0] && !models.some((m) => m.modelId === pickedModelId)) {
      setPickedModelId(models[0].modelId);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [models]);
  const selectedModel = models.find((m) => m.modelId === pickedModelId);
  const modelId = selectedModel?.modelId ?? '';
  const resolutions = selectedModel?.resolutions ?? [];

  const [resolution, setResolution] = useState<string>('');
  useEffect(() => {
    if (!resolution && resolutions[0]) setResolution(String(resolutions[0].value));
    // 换模型（或后台改了档位）后：新列表里有同值档位就保留——用户换模型多半还想要同一档；
    // 没有就回落到第一档，避免悬空选中。按 value 比而不是 label。
    else if (resolution && !resolutions.some((o) => String(o.value) === resolution) && resolutions[0]) {
      setResolution(String(resolutions[0].value));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resolutions]);

  const projectId = currentProjectId();

  const isGenerating = node?.status === 'generating';
  const configured = Boolean(modelId) && resolutions.length > 0;
  const canSubmit =
    Boolean(sourceUrl && sourceNodeId && configured && resolution) && !isGenerating;

  const mediaMenu = useMediaNodeMenu({ url: node?.content, kind: 'video', nodeId: id });
  const mediaActive = useNodeMediaActive(id);

  // 与 GenVideoNode / SubtitleEraseNode 同一套「方案 B」：结果由后端权威写回快照，
  // 轮询仅用于前端即时展示，刷新页面能恢复进行中的任务。
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
            updateNode(id, { status: 'failed', error: finalTask.error || `${variant.action}失败`, taskId: undefined });
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
  }, [node?.status, node?.taskId, id, updateNode, updateNodeSettings, variant.action]);

  if (!node) return null;

  const handleSubmit = async () => {
    if (!sourceNodeId || !canSubmit) return;
    updateNode(id, { status: 'generating', error: undefined, taskId: undefined });
    persistNodeById(id);
    try {
      const r = await variant.submit({
        projectId: projectId!,
        nodeId: id,
        sourceNodeId,
        modelId,
        resolution,
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
      {/* 有模型下拉时 bodyOverflow=visible：下拉弹层向上弹出，不放开会被节点边框裁掉。 */}
      <NodeShell
        node={node}
        inputs={1}
        outputs={1}
        minWidth={280}
        minHeight={260}
        titleExtra={mediaMenu.badge}
        bodyOverflow={variant.pickModel ? 'visible' : undefined}
      >
        <div className="w-full h-full p-2 flex flex-col gap-2">
          <div className="relative flex-1 rounded-2xl bg-popover/30 border border-border/60 flex items-center justify-center overflow-hidden min-h-0">
            {/* 已有产物：展示处理结果，可走 useMediaNodeMenu 保存/发送。 */}
            {node.content ? (
              <VideoPlayer
                active={mediaActive}
                {...mediaMenu.mediaProps}
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

          {/* 有产物之后就收起面板——这是一次性操作，要换档位就对源节点再建一个，
              这样旧产物不被覆盖（与擦除字幕一致）。 */}
          {!node.content && (
            <div className="shrink-0 flex flex-col gap-1.5 px-1 pb-1">
              {!sourceUrl && (
                <div className="text-[11px] text-muted-foreground text-center">请连接一个视频</div>
              )}
              {sourceUrl && !configured && (
                <div className="text-[11px] text-muted-foreground text-center">{variant.unconfigured}</div>
              )}
              {sourceUrl && configured && (
                // max-h 给到 180px:后台常配 3~4 个档位,原来的 max-h-24(96px) 一定要滚才能看全
                // (线上反馈「选项太多还要滚,不够智能」)。配更多时仍可滚,不失控。
                <div className="flex flex-col gap-1 max-h-[180px] overflow-auto custom-scrollbar">
                  {resolutions.map((o) => (
                    <button
                      key={String(o.value)}
                      type="button"
                      onMouseDown={(e) => e.stopPropagation()}
                      onClick={(e) => {
                        e.stopPropagation();
                        setResolution(String(o.value));
                      }}
                      className={`text-left px-2 py-1 rounded-md text-[11px] border-none cursor-pointer transition-colors ${
                        resolution === String(o.value)
                          ? 'bg-accent text-foreground'
                          : 'bg-popover hover:bg-accent text-sidebar-foreground'
                      }`}
                    >
                      <div>{o.label}</div>
                    </button>
                  ))}
                </div>
              )}

              <div className="flex items-center gap-1.5">
                {variant.pickModel && configured && (
                  <div className="relative min-w-0">
                    <button
                      type="button"
                      aria-label="选择转高清模型"
                      aria-haspopup="listbox"
                      aria-expanded={modelMenuOpen}
                      onMouseDown={(e) => e.stopPropagation()}
                      onClick={(e) => {
                        e.stopPropagation();
                        setModelMenuOpen((v) => !v);
                      }}
                      className="flex max-w-[120px] items-center gap-1 rounded-md bg-white/5 px-2 py-1 text-[11px] text-foreground border border-white/10 cursor-pointer hover:bg-white/10"
                      title={selectedModel?.label}
                    >
                      <span className="truncate">{selectedModel?.label}</span>
                      <ChevronDown size={12} className="shrink-0 text-muted-foreground" />
                    </button>
                    {modelMenuOpen && (
                      <Popover onClose={() => setModelMenuOpen(false)} width={200}>
                        <div role="listbox" aria-label="转高清模型">
                          {models.map((m) => (
                            <PopoverItem
                              key={m.modelId}
                              active={m.modelId === modelId}
                              onClick={() => {
                                setPickedModelId(m.modelId);
                                setModelMenuOpen(false);
                              }}
                            >
                              <span className="min-w-0 flex-1 whitespace-normal break-words">{m.label}</span>
                            </PopoverItem>
                          ))}
                        </div>
                      </Popover>
                    )}
                  </div>
                )}
                <div className="flex-1" />
                <button
                  type="button"
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    void handleSubmit();
                  }}
                  disabled={!canSubmit}
                  className="px-3 py-1.5 rounded-md bg-blue-600 hover:bg-blue-500 text-white disabled:opacity-40 disabled:cursor-not-allowed text-[12px] border-none cursor-pointer"
                  title={
                    !sourceUrl
                      ? '请连接一个视频'
                      : !configured
                        ? variant.unconfigured
                        : isGenerating
                          ? '处理中…'
                          : variant.action
                  }
                >
                  {isGenerating ? '处理中…' : variant.action}
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

// 以下两个组件照搬 GenVideoNode 的模型选择弹层（同名本地函数），保持两个节点点开下拉的
// 观感一致。弹层是 absolute 贴着触发按钮、随画布一起缩放——不是 fixed，所以不受
// 「节点子树内 fixed 弹层在 zoom≠1 错位」那条约束影响，无需 portal。
function Popover({
  children,
  onClose,
  width = 200,
}: {
  children: React.ReactNode;
  onClose: () => void;
  width?: number;
}) {
  return (
    <>
      <div
        className="fixed inset-0 z-[200]"
        onClick={onClose}
        onMouseDown={(e) => e.stopPropagation()}
      />
      <div
        className="absolute bottom-full mb-2 left-0 z-[201] rounded-[20px] max-h-[440px] overflow-auto custom-scrollbar"
        style={{
          width,
          maxWidth: 'calc(100vw - 32px)',
          backgroundColor: '#2a2a2a',
          boxShadow:
            '0px 4px 16px rgba(0, 0, 0, 0.45), inset 0px 0.5px 0px rgba(255, 255, 255, 0.08)',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </>
  );
}

function PopoverItem({
  active,
  children,
  onClick,
}: {
  active?: boolean;
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="option"
      aria-selected={Boolean(active)}
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => { e.stopPropagation(); onClick(); }}
      className={`w-full text-left px-2.5 py-1.5 text-[11px] flex gap-1.5 cursor-pointer transition-colors border-none items-center ${
        active ? 'bg-accent text-foreground' : 'bg-transparent hover:bg-accent/60 text-sidebar-foreground'
      }`}
    >
      {children}
    </button>
  );
}
