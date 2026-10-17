// 视频节点的空间裁剪(crop)交互层。store.croppingNodeId === nodeId 时由
// VideoActionsBar 渲染(工具条让位,同 trim 先例):节点上叠裁剪框,节点
// 正下方浮确认条:
// 初始居中 80% 框、角手柄锁比例、边手柄单轴、框身拖动、三分线、实时像素
// 标签、框外 9999px box-shadow 遮罩。
//
// 裁剪框锚定在「视频内容区」而不是节点 body:preview 节点默认尺寸与视频
// 宽高比可能不一致,object-fit: contain 会留黑边,裁剪框必须贴着画面本身,
// 否则拖到黑边上的框换算出的像素是错的。内容区通过 getBoundingClientRect
// 量出来——屏幕坐标相除,画布 zoom 在分子分母里同时出现,天然消掉。
//
// 确认后的数据流照抄 VideoTrimBar:cropVideo 出 Blob → uploadAsset → 源节点
// 右侧 spawnPostprocessNode('preview') 写产物 URL 并连线;唯一差异是产物节点
// 尺寸按裁剪比例缩(trim 不改画面抄源尺寸,裁剪画面变了不能照抄)。
import { useCallback, useEffect, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { Check, Loader2, X } from 'lucide-react';
import { uploadAsset, currentProjectId } from '@/shared/api';
import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { spawnPostprocessNode } from '../useMediaNodeMenu';
import {
  INITIAL_RECT,
  MIN_CROP_PX,
  cropVideo,
  moveRect,
  resizeRect,
  toPixelRect,
  type FracRect,
  type HandleKind,
} from './videoCrop';

// 8 个手柄的定位 class(白底圆点)。key 与 HandleKind 一致。
const HANDLES: Array<{ kind: Exclude<HandleKind, 'move'>; cls: string }> = [
  { kind: 'nw', cls: '-left-1.5 -top-1.5 cursor-nwse-resize' },
  { kind: 'n', cls: 'left-1/2 -top-1.5 -translate-x-1/2 cursor-ns-resize' },
  { kind: 'ne', cls: '-right-1.5 -top-1.5 cursor-nesw-resize' },
  { kind: 'e', cls: '-right-1.5 top-1/2 -translate-y-1/2 cursor-ew-resize' },
  { kind: 'se', cls: '-bottom-1.5 -right-1.5 cursor-nwse-resize' },
  { kind: 's', cls: '-bottom-1.5 left-1/2 -translate-x-1/2 cursor-ns-resize' },
  { kind: 'sw', cls: '-bottom-1.5 -left-1.5 cursor-nesw-resize' },
  { kind: 'w', cls: '-left-1.5 top-1/2 -translate-y-1/2 cursor-ew-resize' },
];

// 视频内容区(百分比,相对 overlay 容器)。frame=null 表示还没量出来。
interface ContentFrame {
  left: number;
  top: number;
  width: number;
  height: number;
}

export function VideoCropOverlay({ nodeId, url }: { nodeId: string; url: string }) {
  const { message } = AntApp.useApp();
  const containerRef = useRef<HTMLDivElement>(null);
  const [phase, setPhase] = useState<'measure' | 'edit' | 'processing'>('measure');
  const [progress, setProgress] = useState(0);
  const [videoDims, setVideoDims] = useState<{ w: number; h: number } | null>(null);
  const [frame, setFrame] = useState<ContentFrame | null>(null);
  const [rect, setRect] = useState<FracRect>(INITIAL_RECT);
  // 拖拽锚点:pointerdown 时的 rect 快照 + 屏幕坐标 + 内容区屏幕尺寸。
  // 增量一律从锚点绝对推算(不是逐帧累加),快速拖动不漂移——同 trim 的 dragRef。
  const dragRef = useRef<{
    kind: HandleKind;
    x0: number;
    y0: number;
    rect0: FracRect;
    frameW: number;
    frameH: number;
  } | null>(null);

  const exit = useCallback(() => {
    useCanvasStore.getState().setCroppingNode(null);
  }, []);

  // 量视频内容区:找宿主节点里的 <video>(同 trim 经 data-node-id 定位,免穿
  // ref),等 metadata 拿到真实像素,再用 contain 数学从元素盒里抠出画面区。
  useEffect(() => {
    const video = document.querySelector<HTMLVideoElement>(`[data-node-id="${nodeId}"] video`);
    const container = containerRef.current;
    if (!video || !container) {
      message.error('未找到视频,无法裁剪');
      exit();
      return;
    }
    let cancelled = false;
    const measure = () => {
      if (cancelled) return;
      const vw = video.videoWidth;
      const vh = video.videoHeight;
      if (!vw || !vh) {
        message.error('视频加载失败,无法裁剪');
        exit();
        return;
      }
      const box = video.getBoundingClientRect();
      const host = container.getBoundingClientRect();
      if (!box.width || !box.height || !host.width || !host.height) return;
      // object-fit: contain——画面按短边贴合居中,黑边在长轴两侧。
      const scale = Math.min(box.width / vw, box.height / vh);
      const cw = vw * scale;
      const ch = vh * scale;
      const cx = box.left + (box.width - cw) / 2;
      const cy = box.top + (box.height - ch) / 2;
      setVideoDims({ w: vw, h: vh });
      setFrame({
        left: ((cx - host.left) / host.width) * 100,
        top: ((cy - host.top) / host.height) * 100,
        width: (cw / host.width) * 100,
        height: (ch / host.height) * 100,
      });
      setPhase('edit');
    };
    if (video.videoWidth) measure();
    else {
      video.addEventListener('loadedmetadata', measure);
      // preload=metadata 的 <video> 可能还没发起加载,催一下。
      video.load?.();
    }
    return () => {
      cancelled = true;
      video.removeEventListener('loadedmetadata', measure);
    };
  }, [nodeId, message, exit]);

  // 拖拽:pointerdown 记锚点,window 上收 move/up——手柄很小,鼠标滑出也不断拖。
  useEffect(() => {
    const onMove = (e: PointerEvent) => {
      const drag = dragRef.current;
      const dims = videoDims;
      if (!drag || !dims) return;
      const dx = (e.clientX - drag.x0) / drag.frameW;
      const dy = (e.clientY - drag.y0) / drag.frameH;
      if (drag.kind === 'move') {
        setRect(moveRect(drag.rect0, dx, dy));
      } else {
        setRect(resizeRect(drag.rect0, drag.kind, dx, dy, MIN_CROP_PX / dims.w, MIN_CROP_PX / dims.h));
      }
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
  }, [videoDims]);

  const cancel = useCallback(() => {
    if (phase === 'processing') return; // 转码/上传中不允许取消——半个产物没意义
    exit();
  }, [phase, exit]);

  const confirm = useCallback(async () => {
    if (phase !== 'edit' || !videoDims) return;
    const px = toPixelRect(rect, videoDims.w, videoDims.h);
    setPhase('processing');
    setProgress(0);
    try {
      const blob = await cropVideo(url, px, setProgress);
      const file = new File([blob], `crop-${px.width}x${px.height}.mp4`, { type: 'video/mp4' });
      const asset = await uploadAsset(file, currentProjectId());
      const store = useCanvasStore.getState();
      const newId = spawnPostprocessNode('preview', nodeId);
      if (newId) {
        store.updateNode(newId, { content: asset.url, status: 'succeeded' });
        // 尺寸按裁剪比例从源节点缩:画面裁小了,产物节点跟着小才不突兀
        // (trim 抄源尺寸是因为画面没变)。下限防裁一小角出个米粒节点。
        const src = store.nodes.find((n) => n.id === nodeId);
        if (src) {
          const srcW = src.width ?? 320;
          store.updateNode(newId, {
            width: Math.max(160, Math.round(srcW * (px.width / videoDims.w))),
            height: Math.max(120, Math.round((src.height ?? 260) * (px.height / videoDims.h))),
            x: src.x + srcW + 80,
            y: src.y,
          });
        }
        // preview 是通用媒体容器,靠 settings.kind 自报产物类型(同 trim)。
        store.updateNodeSettings(newId, { kind: 'video' });
        persistNodeById(newId);
      }
      exit();
      message.success('裁剪完成');
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '裁剪失败');
      setPhase('edit'); // 框位保留,可直接重试
    }
  }, [phase, videoDims, rect, url, nodeId, message, exit]);

  // Esc 取消 / Enter 确认。capture + stopPropagation:crop 期间是模态语义,
  // 不能让画布自己的快捷键(删除节点等)抢走(同 trim)。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        cancel();
      } else if (e.key === 'Enter') {
        e.stopPropagation();
        void confirm();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [cancel, confirm]);

  const startDrag = (kind: HandleKind) => (e: React.PointerEvent) => {
    if (phase !== 'edit') return;
    const container = containerRef.current;
    if (!container || !frame) return;
    e.preventDefault();
    e.stopPropagation();
    const host = container.getBoundingClientRect();
    dragRef.current = {
      kind,
      x0: e.clientX,
      y0: e.clientY,
      rect0: rect,
      // 内容区的屏幕尺寸在拖拽起点定格——zoom 中途不会变(拖拽时没法滚轮)。
      frameW: (host.width * frame.width) / 100,
      frameH: (host.height * frame.height) / 100,
    };
  };

  const px = videoDims ? toPixelRect(rect, videoDims.w, videoDims.h) : null;
  const stop = (e: React.SyntheticEvent) => e.stopPropagation();

  return (
    <>
      {/* 裁剪层:叠满节点 body(同 GridSplitOverlay),内部再按内容区锚定。
          overflow-hidden 把裁剪框的 9999px 遮罩阴影裁在节点内。 */}
      <div
        ref={containerRef}
        className="absolute inset-0 z-30 select-none overflow-hidden rounded-xl"
        onMouseDown={stop}
        onDoubleClick={stop}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        {frame && phase !== 'measure' && (
          <div
            className="absolute"
            style={{
              left: `${frame.left}%`,
              top: `${frame.top}%`,
              width: `${frame.width}%`,
              height: `${frame.height}%`,
            }}
          >
            {/* 裁剪框:百分比定位;框外遮罩 = 自身 9999px box-shadow(
                一行 CSS 免掉四块暗片的拼接)。 */}
            <div
              className="absolute z-[2] cursor-move border border-solid border-white shadow-[0_0_0_1px_rgba(0,0,0,0.35),0_0_0_9999px_rgba(0,0,0,0.45)]"
              style={{
                left: `${rect.left * 100}%`,
                top: `${rect.top * 100}%`,
                width: `${rect.width * 100}%`,
                height: `${rect.height * 100}%`,
              }}
              onPointerDown={startDrag('move')}
            >
              {/* 三分线 */}
              <div className="pointer-events-none absolute left-1/3 top-0 h-full w-px bg-white/45" />
              <div className="pointer-events-none absolute left-2/3 top-0 h-full w-px bg-white/45" />
              <div className="pointer-events-none absolute left-0 top-1/3 h-px w-full bg-white/45" />
              <div className="pointer-events-none absolute left-0 top-2/3 h-px w-full bg-white/45" />
              {/* 实时像素标签 */}
              {px && (
                <span className="pointer-events-none absolute left-2 top-2 rounded bg-black/65 px-2 py-0.5 text-[11px] tabular-nums text-white whitespace-nowrap">
                  {px.width} x {px.height}
                </span>
              )}
              {HANDLES.map((h) => (
                <button
                  key={h.kind}
                  type="button"
                  aria-label={`resize-${h.kind}`}
                  onPointerDown={startDrag(h.kind)}
                  className={`absolute z-[3] size-3 rounded-full border border-solid border-black/45 bg-white shadow-[0_1px_4px_rgba(0,0,0,0.45)] p-0 ${h.cls}`}
                />
              ))}
            </div>
          </div>
        )}
      </div>

      {/* 确认条:节点正下方,inv-zoom 反缩放(同 VideoTrimBar 定位)。 */}
      <div
        className="absolute z-40"
        style={{
          top: '100%',
          left: '50%',
          transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 12px)',
          transformOrigin: '0 0',
        }}
        onMouseDown={stop}
        onDoubleClick={stop}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        <div className="flex items-center gap-2 px-2 py-1.5 rounded-xl bg-card border border-border shadow-xl">
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
          <span className="text-[13px] tabular-nums text-foreground/90 whitespace-nowrap min-w-20 text-center">
            {phase === 'processing' ? (
              <span className="flex items-center gap-1.5 justify-center">
                <Loader2 size={13} className="animate-spin" />
                处理中… {Math.round(progress * 100)}%
              </span>
            ) : px ? (
              `${px.width} x ${px.height}`
            ) : (
              '加载中…'
            )}
          </span>
          <button
            type="button"
            title="生成裁剪 (Enter)"
            disabled={phase !== 'edit'}
            onMouseDown={(e) => {
              e.preventDefault();
              e.stopPropagation();
            }}
            onClick={(e) => {
              e.stopPropagation();
              void confirm();
            }}
            className="w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer bg-white text-black hover:bg-white/85 disabled:opacity-50 disabled:cursor-not-allowed transition-colors shrink-0"
          >
            <Check size={15} />
          </button>
        </div>
      </div>
    </>
  );
}
