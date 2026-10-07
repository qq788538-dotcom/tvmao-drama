// 视频节点的「片段重拍」面板。store.reshootNodeId === nodeId 时浮现在节点
// 正下方(定位方案同 VideoTrimBar,那边注释有逐行解释)。
//
// 交互对标 liblib 的片段重拍,架构走本项目自己的路(设计讨论 2026-08-17,用户拍板):
// - 时间轴 filmstrip 上点空档加段(≤5 段,每段 4~30s,段间不重叠),拖把手调区间;
// - 每段一行独立输入框(liblib 是单框 + {{SegmentRange}} 富文本 token——那是
//   它单节点单任务架构的产物,我们 N 节点方案不需要);
// - 不选段 = 整段重拍,仅源视频 ≤30s 时可用(seedance edit 单次输入上限);
// - 点「提交」:逐段浏览器内切片(复用 trim 的 mediabunny 管道)→ 上传 →
//   铺「切片 preview 节点 + gen-video 节点」对并连线,提示词写进各 gen-video;
//   切片随即提交 Seedance 合规校验(不等待;同步调用期间节点角标显示校验中,
//   结束后刷新角标显示结果);
// - 提交只铺节点——生成由用户在每个 gen-video 节点上手动点
//   (批量入口绝不自动发起生成的铁律)。
import { redactSecrets } from '@/shared/redact';
import { useCallback, useEffect, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, Loader2, X } from 'lucide-react';
import {
  listRuntimeModels,
  uploadAsset,
  currentProjectId,
} from '@/shared/api';
import { verifySeedanceAndRefresh } from '../seedanceVerify';
import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { modelSettingDefaults } from '../../utils/generationInputs';
import { extractFilmstrip, trimVideo } from '../trim/videoTrim';
import {
  addRangeAt,
  dragRange,
  formatTimeLabel,
  rangePrompt,
  removeRange,
  sortRanges,
  updateRangeIntent,
  RESHOOT_MAX_RANGES,
  RESHOOT_MIN_SOURCE_SEC,
  RESHOOT_MODEL_ID,
  RESHOOT_REROLL_PROMPT,
  RESHOOT_WHOLE_MAX_SEC,
  type ReshootRange,
} from './segmentReshoot';

const FRAME_COUNT = 12;
// 比 trim(384)宽:重拍面向长视频(几分钟),同宽下选段把手会挤成一坨。
const STRIP_WIDTH = 480;

// 铺节点用的默认尺寸。preview/gen-video 的 DEFAULT_SIZES 值(store.ts),
// addNode 把坐标当中心点,这里要用半宽/半高算落位。
const CLIP_W = 320;
const CLIP_H = 260;
const GEN_W = 400;
const ROW_GAP = 60;

type DragKind = 'left' | 'right' | 'move';

export function SegmentReshootBar({ nodeId, url }: { nodeId: string; url: string }) {
  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();
  const show = useCanvasStore((s) => s.reshootNodeId === nodeId);
  const [duration, setDuration] = useState(0);
  const [frames, setFrames] = useState<string[]>([]);
  const [ranges, setRanges] = useState<ReshootRange[]>([]);
  const [wholeIntent, setWholeIntent] = useState('');
  const [phase, setPhase] = useState<'loading' | 'edit' | 'processing'>('loading');
  const [progressText, setProgressText] = useState('');
  // 拖拽锚点:按下时的 clientX 与当时的全量选段。装 ref,move 不触发重渲染链。
  const dragRef = useRef<{ id: string; kind: DragKind; x0: number; ranges0: ReshootRange[] } | null>(null);

  // 与 GenVideoNode 同 queryKey 共享缓存;要拿重拍模型(写死 RESHOOT_MODEL_ID)
  // 的 uiSchema 铺默认参数。模型中心没配它时工具条已拦,这里兜底再查一次。
  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 300_000,
  });

  useEffect(() => {
    if (!show) return;
    let alive = true;
    setPhase('loading');
    extractFilmstrip(url, FRAME_COUNT)
      .then(({ duration: d, frames: f }) => {
        if (!alive) return;
        if (d < RESHOOT_MIN_SOURCE_SEC) {
          message.warning(`当前仅支持时长不少于 ${RESHOOT_MIN_SOURCE_SEC} 秒的视频进行片段重拍`);
          useCanvasStore.getState().setReshootNode(null);
          return;
        }
        setDuration(d);
        setFrames(f);
        setRanges([]);
        setPhase('edit');
      })
      .catch((e) => {
        if (!alive) return;
        message.error(e?.message ? redactSecrets(String(e.message)) : '视频加载失败,无法重拍');
        useCanvasStore.getState().setReshootNode(null);
      });
    return () => {
      alive = false;
    };
  }, [show, url, message]);

  const cancel = useCallback(() => {
    if (phase === 'processing') return; // 切片/上传/铺节点进行中不允许取消
    useCanvasStore.getState().setReshootNode(null);
  }, [phase]);

  // Esc 退出(模态语义,capture 挡画布快捷键;不劫持输入框里的编辑键)。
  useEffect(() => {
    if (!show) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        cancel();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [show, cancel]);

  // 拖拽收在 window 上,理由同 VideoTrimBar(把手小,滑出不断拖)。
  useEffect(() => {
    const onMove = (e: PointerEvent) => {
      const drag = dragRef.current;
      if (!drag || duration <= 0) return;
      const dt = ((e.clientX - drag.x0) / STRIP_WIDTH) * duration;
      setRanges(dragRange(drag.ranges0, drag.id, drag.kind, dt, duration));
    };
    const onUp = () => {
      dragRef.current = null;
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
    };
  }, [duration]);

  // 整段模式(不选段)只在源视频不超上限时可提交;选了段则各段自身已被钳制。
  const wholeTooLong = ranges.length === 0 && duration > RESHOOT_WHOLE_MAX_SEC;
  const modelReady = (modelsQuery.data ?? []).some((m) => m.modelId === RESHOOT_MODEL_ID);
  const submitTip = wholeTooLong
    ? `源视频超过 ${RESHOOT_WHOLE_MAX_SEC} 秒,请在时间轴上点选要重拍的片段`
    : !modelReady
      ? `模型中心未配置 ${RESHOOT_MODEL_ID}`
      : null;

  const submit = useCallback(async () => {
    if (phase !== 'edit' || wholeTooLong) return;
    const store = useCanvasStore.getState();
    const src = store.nodes.find((n) => n.id === nodeId);
    if (!src) return;
    const model = (modelsQuery.data ?? []).find((m) => m.modelId === RESHOOT_MODEL_ID);
    if (!model) {
      message.warning(`模型中心未配置 ${RESHOOT_MODEL_ID},无法使用片段重拍`);
      return;
    }
    setPhase('processing');
    try {
      const srcW = src.width ?? 320;
      const srcH = src.height ?? 260;
      const sorted = sortRanges(ranges);
      // 整段模式:不切片不上传,直接铺一个 gen-video 连回源节点。
      // 源视频同样补一发合规校验——seedance 前缀模型生成前置要求已校验素材。
      if (sorted.length === 0) {
        const genId = store.addNode('gen-video', {
          x: src.x + srcW + 80 + GEN_W / 2,
          y: src.y + srcH / 2,
        });
        if (genId) {
          store.addConnection(nodeId, genId, 'video');
          store.updateNodeSettings(genId, {
            modelId: RESHOOT_MODEL_ID,
            ...modelSettingDefaults(model.uiSchema),
            videoPrompt: wholeIntent.trim() || RESHOOT_REROLL_PROMPT,
          });
          persistNodeById(genId);
        }
        verifySeedanceAndRefresh(queryClient, url, 'video').catch(() => {
          // 失败结果由节点「不合规」角标展示(结束后会刷新状态),
          // 用户可在节点右键手动重新提交。
        });
        message.success('已铺重拍节点,确认参数后点生成');
        store.setReshootNode(null);
        return;
      }
      // 逐段串行切片:5 段并行转码会同时吃 5 份解码内存,长视频上容易崩。
      for (let i = 0; i < sorted.length; i++) {
        const r = sorted[i];
        setProgressText(`切片 ${i + 1}/${sorted.length}`);
        const blob = await trimVideo(url, r.start, r.end, (p) =>
          setProgressText(`切片 ${i + 1}/${sorted.length} · ${Math.round(p * 100)}%`),
        );
        const file = new File(
          [blob],
          `reshoot-${r.start.toFixed(1)}s-${r.end.toFixed(1)}s.mp4`,
          { type: 'video/mp4' },
        );
        setProgressText(`上传 ${i + 1}/${sorted.length}`);
        const asset = await uploadAsset(file, currentProjectId());
        const rowCenterY = src.y + srcH / 2 + i * (CLIP_H + ROW_GAP);
        const clipId = store.addNode('preview', {
          x: src.x + srcW + 80 + CLIP_W / 2,
          y: rowCenterY,
        });
        if (!clipId) continue;
        store.updateNode(clipId, { content: asset.url, status: 'succeeded' });
        // preview 是通用媒体容器,settings.kind 自报产物类型(签名 URL 可能没
        // 扩展名,下游分类只有 kind 兜得住)——与 trim 落产物同一套约定。
        store.updateNodeSettings(clipId, { kind: 'video' });
        store.addConnection(nodeId, clipId, 'video');
        persistNodeById(clipId);
        const genId = store.addNode('gen-video', {
          x: src.x + srcW + 80 + CLIP_W + 80 + GEN_W / 2,
          y: rowCenterY,
        });
        if (genId) {
          store.addConnection(clipId, genId, 'video');
          store.updateNodeSettings(genId, {
            modelId: RESHOOT_MODEL_ID,
            ...modelSettingDefaults(model.uiSchema),
            videoPrompt: rangePrompt(r),
          });
          persistNodeById(genId);
        }
        verifySeedanceAndRefresh(queryClient, asset.url, 'video').catch(() => {});
      }
      message.success(`已铺 ${sorted.length} 组重拍节点,校验通过后逐个点生成`);
      store.setReshootNode(null);
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '片段重拍提交失败');
      setPhase('edit'); // 选段与意图保留,可直接重试
    } finally {
      setProgressText('');
    }
  }, [phase, wholeTooLong, nodeId, modelsQuery.data, ranges, wholeIntent, url, message, queryClient]);

  if (!show) return null;

  const startDrag = (id: string, kind: DragKind) => (e: React.PointerEvent) => {
    if (phase !== 'edit') return;
    e.preventDefault();
    e.stopPropagation();
    dragRef.current = { id, kind, x0: e.clientX, ranges0: ranges };
  };

  const onStripClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (phase !== 'edit' || duration <= 0) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const t = ((e.clientX - rect.left) / rect.width) * duration;
    const next = addRangeAt(ranges, t, duration);
    if (next === ranges) {
      if (ranges.length >= RESHOOT_MAX_RANGES) message.warning(`最多选择 ${RESHOOT_MAX_RANGES} 个片段`);
      return;
    }
    setRanges(next);
  };

  const sorted = sortRanges(ranges);

  return (
    <div
      className="absolute z-40"
      style={{
        top: '100%',
        left: '50%',
        transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 12px)',
        transformOrigin: '0 0',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <div className="flex flex-col gap-2 p-2 rounded-xl bg-card border border-border shadow-xl">
        {/* 第一行:取消 + 缩略图条(多选区) + 段数 + 提交 */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            title="取消 (Esc)"
            onMouseDown={(e) => {
              e.preventDefault();
              e.stopPropagation();
            }}
            onClick={(e) => {
              e.stopPropagation();
              cancel();
            }}
            className="w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60 transition-colors shrink-0"
          >
            <X size={15} />
          </button>

          <div
            className="relative h-12 rounded-lg overflow-hidden bg-muted shrink-0 select-none cursor-copy"
            style={{ width: STRIP_WIDTH }}
            title="点击空档新增片段"
            onClick={onStripClick}
          >
            <div className="absolute inset-0 flex">
              {(frames.length ? frames : Array.from({ length: FRAME_COUNT }, () => '')).map((f, i) =>
                f ? (
                  <img key={i} src={f} alt="" draggable={false} className="h-full object-cover" style={{ width: STRIP_WIDTH / FRAME_COUNT }} />
                ) : (
                  <div key={i} className="h-full bg-accent/40" style={{ width: STRIP_WIDTH / FRAME_COUNT }} />
                ),
              )}
            </div>
            {phase === 'loading' && (
              <div className="absolute inset-0 flex items-center justify-center text-muted-foreground text-xs gap-1.5 bg-card/60">
                <Loader2 size={13} className="animate-spin" /> 加载中…
              </div>
            )}
            {phase !== 'loading' &&
              sorted.map((r) => {
                const leftPct = (r.start / duration) * 100;
                const widthPct = ((r.end - r.start) / duration) * 100;
                return (
                  <div
                    key={r.id}
                    className="absolute inset-y-0 border-2 border-solid border-primary rounded-md cursor-grab bg-primary/15"
                    style={{ left: `${leftPct}%`, width: `${widthPct}%` }}
                    onClick={(e) => e.stopPropagation()}
                    onPointerDown={startDrag(r.id, 'move')}
                  >
                    <div
                      className="absolute -left-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                      onPointerDown={startDrag(r.id, 'left')}
                    >
                      <div className="w-1 h-5 rounded-full bg-primary" />
                    </div>
                    <div
                      className="absolute -right-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                      onPointerDown={startDrag(r.id, 'right')}
                    >
                      <div className="w-1 h-5 rounded-full bg-primary" />
                    </div>
                    <div className="absolute left-1/2 -translate-x-1/2 top-1/2 -translate-y-1/2 px-1.5 py-0.5 rounded bg-black/70 text-white text-[10px] font-medium whitespace-nowrap pointer-events-none">
                      {(r.end - r.start).toFixed(1)}s
                    </div>
                  </div>
                );
              })}
            {phase === 'processing' && (
              <div className="absolute inset-0 flex items-center justify-center text-white text-xs gap-1.5 bg-black/70">
                <Loader2 size={13} className="animate-spin" />
                {progressText || '处理中…'}
              </div>
            )}
          </div>

          <span className="text-[11px] text-muted-foreground whitespace-nowrap shrink-0">
            {sorted.length}/{RESHOOT_MAX_RANGES} 个片段
          </span>

          <span title={submitTip ?? '提交:切片并铺出重拍节点'} className="shrink-0 inline-flex">
            <button
              type="button"
              disabled={phase !== 'edit' || wholeTooLong || !modelReady}
              onMouseDown={(e) => {
                e.preventDefault();
                e.stopPropagation();
              }}
              onClick={(e) => {
                e.stopPropagation();
                void submit();
              }}
              className="w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer bg-white text-black hover:bg-white/85 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              <Check size={15} />
            </button>
          </span>
        </div>

        {/* 第二行起:每段一行输入框;没选段时是整段重拍的单行。 */}
        {phase !== 'loading' &&
          (sorted.length === 0 ? (
            <div className="flex items-center gap-2">
              <span className="shrink-0 px-2 py-1 rounded-md bg-accent/60 text-[11px] text-foreground/80 whitespace-nowrap">
                整段 0:00–{formatTimeLabel(duration)}
              </span>
              <input
                type="text"
                value={wholeIntent}
                disabled={phase !== 'edit' || wholeTooLong}
                onChange={(e) => setWholeIntent(e.target.value)}
                placeholder={
                  wholeTooLong
                    ? `源视频超过 ${RESHOOT_WHOLE_MAX_SEC} 秒,请点击上方时间轴框选片段`
                    : '留空＝原样重跑一次;也可以写要改什么,例如:把黄色台灯换成白色台灯'
                }
                className="flex-1 min-w-0 h-8 px-2.5 rounded-lg bg-muted border border-border text-[12px] text-foreground placeholder:text-muted-foreground outline-none focus:border-primary/60 disabled:opacity-50"
                onKeyDown={(e) => e.stopPropagation()}
              />
            </div>
          ) : (
            sorted.map((r) => (
              <div key={r.id} className="flex items-center gap-2">
                <span className="shrink-0 px-2 py-1 rounded-md bg-accent/60 text-[11px] text-foreground/80 whitespace-nowrap tabular-nums">
                  {formatTimeLabel(r.start)}–{formatTimeLabel(r.end)}
                </span>
                <input
                  type="text"
                  value={r.intent}
                  disabled={phase !== 'edit'}
                  onChange={(e) => setRanges((prev) => updateRangeIntent(prev, r.id, e.target.value))}
                  placeholder="留空＝原样重拍这一段;也可以写要改什么"
                  className="flex-1 min-w-0 h-8 px-2.5 rounded-lg bg-muted border border-border text-[12px] text-foreground placeholder:text-muted-foreground outline-none focus:border-primary/60 disabled:opacity-50"
                  onKeyDown={(e) => e.stopPropagation()}
                />
                <button
                  type="button"
                  title="删除该片段"
                  disabled={phase !== 'edit'}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                  }}
                  onClick={(e) => {
                    e.stopPropagation();
                    setRanges((prev) => removeRange(prev, r.id));
                  }}
                  className="w-7 h-7 rounded-md flex items-center justify-center border-none cursor-pointer bg-transparent text-foreground/50 hover:text-foreground hover:bg-accent/60 disabled:opacity-40 transition-colors shrink-0"
                >
                  <X size={13} />
                </button>
              </div>
            ))
          ))}
      </div>
    </div>
  );
}
