// 视频节点的「智能续写」面板。store.continuationNodeId === nodeId 时浮现在
// 节点正下方(定位方案同 VideoTrimBar,那边注释有逐行解释)。
//
// 交互对标 liblib 的智能续写(实机逆向 2026-08-17),架构照本仓「片段重拍」:
// - 时间轴 filmstrip 单选区(4~30s,默认选结尾 30s)拖把手/整体平移;
// - 一行续写内容输入框(留空=自然续写,兜底文案见 videoContinuation.ts);
// - 确认:浏览器内切出前置片段(复用 trim 的 mediabunny 管道)→ 上传 →
//   铺「切片 preview 节点 + gen-video 节点」对并连线,模型写死续写专用
//   doubao-seedance-2-5-260628-extend;切片自动异步提交 Seedance 合规校验;
// - 确认只铺节点,不扣积分——生成由用户在 gen-video 节点上手动点
//   (批量入口绝不自动扣积分的铁律)。
import { useCallback, useEffect, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Check, Loader2, X } from 'lucide-react';
import { listRuntimeModels, uploadAsset, currentProjectId, verifySeedance } from '@/shared/api';
import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { modelSettingDefaults } from '../../utils/generationInputs';
import { extractFilmstrip, trimVideo } from '../trim/videoTrim';
import {
  continuationPrompt,
  dragContinuationSelection,
  initialContinuationSelection,
  CONTINUATION_MIN_SOURCE_SEC,
  CONTINUATION_MODEL_ID,
  type ContinuationSelection,
} from './videoContinuation';

const FRAME_COUNT = 12;
// 同 reshoot 的取舍:续写面向长视频,比 trim(384)宽一档把手才不挤。
const STRIP_WIDTH = 480;

// 铺节点用的默认尺寸,与 SegmentReshootBar 同一组常量(store.ts DEFAULT_SIZES)。
const CLIP_W = 320;
const CLIP_H = 260;
const GEN_W = 400;

type DragKind = 'left' | 'right' | 'move';

export function VideoContinuationBar({ nodeId, url }: { nodeId: string; url: string }) {
  const { message } = AntApp.useApp();
  const show = useCanvasStore((s) => s.continuationNodeId === nodeId);
  const [duration, setDuration] = useState(0);
  const [frames, setFrames] = useState<string[]>([]);
  const [sel, setSel] = useState<ContinuationSelection>({ start: 0, end: 0 });
  const [intent, setIntent] = useState('');
  const [phase, setPhase] = useState<'loading' | 'edit' | 'processing'>('loading');
  const [progressText, setProgressText] = useState('');
  // 拖拽锚点:按下时的 clientX 与当时的选区。装 ref,move 不触发重渲染链。
  const dragRef = useRef<{ kind: DragKind; x0: number; sel0: ContinuationSelection } | null>(null);

  // 与 GenVideoNode 同 queryKey 共享缓存;要拿续写模型的 uiSchema 铺默认参数。
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
        if (d < CONTINUATION_MIN_SOURCE_SEC) {
          message.warning(`当前仅支持时长不少于 ${CONTINUATION_MIN_SOURCE_SEC} 秒的视频进行智能续写`);
          useCanvasStore.getState().setContinuationNode(null);
          return;
        }
        setDuration(d);
        setFrames(f);
        setSel(initialContinuationSelection(d));
        setPhase('edit');
      })
      .catch((e) => {
        if (!alive) return;
        message.error(e?.message || '视频加载失败,无法续写');
        useCanvasStore.getState().setContinuationNode(null);
      });
    return () => {
      alive = false;
    };
  }, [show, url, message]);

  const cancel = useCallback(() => {
    if (phase === 'processing') return; // 切片/上传/铺节点进行中不允许取消
    useCanvasStore.getState().setContinuationNode(null);
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
      setSel(dragContinuationSelection(drag.kind, drag.sel0, dt, duration));
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

  const submit = useCallback(async () => {
    if (phase !== 'edit') return;
    const store = useCanvasStore.getState();
    const src = store.nodes.find((n) => n.id === nodeId);
    if (!src) return;
    const model = (modelsQuery.data ?? []).find((m) => m.modelId === CONTINUATION_MODEL_ID);
    if (!model) {
      // 入口按钮已拦过一次;走到这说明模型在打开面板后被后台下线,给同一句提示。
      message.warning('模型中心未启用智能续写模型');
      return;
    }
    setPhase('processing');
    try {
      const srcW = src.width ?? 320;
      const srcH = src.height ?? 260;
      setProgressText('切片中');
      const blob = await trimVideo(url, sel.start, sel.end, (p) =>
        setProgressText(`切片 ${Math.round(p * 100)}%`),
      );
      const file = new File(
        [blob],
        `continuation-${sel.start.toFixed(1)}s-${sel.end.toFixed(1)}s.mp4`,
        { type: 'video/mp4' },
      );
      setProgressText('上传中');
      const asset = await uploadAsset(file, currentProjectId());
      const rowCenterY = src.y + srcH / 2;
      const clipId = store.addNode('preview', {
        x: src.x + srcW + 80 + CLIP_W / 2,
        y: rowCenterY,
      });
      if (!clipId) throw new Error('创建切片节点失败');
      store.updateNode(clipId, { content: asset.url, status: 'succeeded' });
      // preview 是通用媒体容器,settings.kind 自报产物类型(签名 URL 可能没
      // 扩展名,下游分类只有 kind 兜得住)——与 trim/reshoot 落产物同一套约定。
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
          modelId: CONTINUATION_MODEL_ID,
          ...modelSettingDefaults(model.uiSchema),
          videoPrompt: continuationPrompt(intent),
        });
        persistNodeById(genId);
      }
      // 续写模型是 doubao-seedance* 前缀,generation 层强制要求输入素材已过
      // 合规校验(execution_plan.go)——切片一落地就异步补校验,结果走节点角标。
      verifySeedance(asset.url, 'video').catch(() => {});
      message.success('已铺续写节点,确认参数后点生成');
      store.setContinuationNode(null);
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '智能续写提交失败');
      setPhase('edit'); // 选区与输入保留,可直接重试
    } finally {
      setProgressText('');
    }
  }, [phase, nodeId, modelsQuery.data, sel.start, sel.end, intent, url, message]);

  if (!show) return null;

  const startDrag = (kind: DragKind) => (e: React.PointerEvent) => {
    if (phase !== 'edit') return;
    e.preventDefault();
    e.stopPropagation();
    dragRef.current = { kind, x0: e.clientX, sel0: sel };
  };

  const leftPct = duration > 0 ? (sel.start / duration) * 100 : 0;
  const rightPct = duration > 0 ? (sel.end / duration) * 100 : 100;

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
        {/* 第一行:取消 + 缩略图条(单选区) + 确认 */}
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
            className="relative h-12 rounded-lg overflow-hidden bg-muted shrink-0 select-none"
            style={{ width: STRIP_WIDTH }}
            title="拖拽选择要续写的前置片段"
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
            {phase !== 'loading' && (
              <>
                {/* 选区外的暗化,同 VideoTrimBar */}
                <div className="absolute inset-y-0 left-0 bg-black/60 pointer-events-none" style={{ width: `${leftPct}%` }} />
                <div className="absolute inset-y-0 right-0 bg-black/60 pointer-events-none" style={{ width: `${100 - rightPct}%` }} />
                <div
                  className="absolute inset-y-0 border-2 border-solid border-primary rounded-md cursor-grab bg-primary/10"
                  style={{ left: `${leftPct}%`, width: `${rightPct - leftPct}%` }}
                  onPointerDown={startDrag('move')}
                >
                  <div
                    className="absolute -left-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                    onPointerDown={startDrag('left')}
                  >
                    <div className="w-1 h-5 rounded-full bg-primary" />
                  </div>
                  <div
                    className="absolute -right-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                    onPointerDown={startDrag('right')}
                  >
                    <div className="w-1 h-5 rounded-full bg-primary" />
                  </div>
                  <div className="absolute left-1/2 -translate-x-1/2 top-1/2 -translate-y-1/2 px-2 py-0.5 rounded-md bg-black/70 text-white text-[11px] font-medium whitespace-nowrap pointer-events-none">
                    {(sel.end - sel.start).toFixed(2)} s
                  </div>
                </div>
              </>
            )}
            {phase === 'processing' && (
              <div className="absolute inset-0 flex items-center justify-center text-white text-xs gap-1.5 bg-black/70">
                <Loader2 size={13} className="animate-spin" />
                {progressText || '处理中…'}
              </div>
            )}
          </div>

          <span title="确认:切出前置片段并铺续写节点(不扣积分)" className="shrink-0 inline-flex">
            <button
              type="button"
              disabled={phase !== 'edit'}
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

        {/* 第二行:续写内容输入 */}
        {phase !== 'loading' && (
          <input
            type="text"
            value={intent}
            disabled={phase !== 'edit'}
            onChange={(e) => setIntent(e.target.value)}
            placeholder="请输入需要续写的内容,留空＝自然续写"
            className="w-full h-8 px-2.5 rounded-lg bg-muted border border-border text-[12px] text-foreground placeholder:text-muted-foreground outline-none focus:border-primary/60 disabled:opacity-50"
            onKeyDown={(e) => e.stopPropagation()}
          />
        )}
      </div>
    </div>
  );
}
