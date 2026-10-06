// 图片标注的交互层，对标 liblib 实测 1:1（见 docs/2026-08-15-image-annotate-design.md）：
//   - 模式工具条顶掉常规工具条：✕退出 | 画笔 | 矩形 | 文字 | 颜色 | 线宽 |
//     撤销/重做 | 保存；
//   - 节点图片上叠透明 canvas 绘制层（底图就是节点里已渲染的 <img>，绘制层
//     只画笔画——不 drawImage 就不存在预览期的跨域污染）；
//   - 文字 = 点击处挂隐藏 textarea 收键盘输入实时绘制（liblib 同款 Excalidraw
//     套路）；IME 用 input 事件读全量 value 重绘，composition 期间画拼音过程串、
//     compositionend 自然变汉字，无需特判；
//   - ✕ / ESC / 点节点外任意处 = 直接丢弃全部未保存标注，无确认（对标拍板）。
// 状态自持：进入模式后节点可能失去单选态（工具条隐藏），标注不能被打断——
// 生命周期由 ImageActionsBar 只管一个布尔（同 splitMode 先例）。
import { useCallback, useEffect, useRef, useState } from 'react';
import { Pencil, Redo2, Spline, Square, Type, Undo2, X } from 'lucide-react';
import { App as AntApp } from 'antd';
import { useCanvasStore } from '../../store';
import {
  ANNOTATE_COLORS,
  DEFAULT_ANNOTATE_COLOR,
  DEFAULT_STROKE_WIDTH,
  MAX_STROKE_WIDTH,
  MIN_STROKE_WIDTH,
  TEXT_FONT_RATIO,
  commitTextDraft,
  fitRect,
  naturalStrokeWidth,
  replayStrokes,
  toNatural,
  type AnnotateStroke,
} from './annotateMath';
import { executeAnnotateSave, loadAnnotateImage } from './executeAnnotateSave';

type Tool = 'pen' | 'rect' | 'text';

interface TextDraft {
  x: number;
  y: number;
  text: string;
  color: string;
  fontSize: number;
}

export function AnnotateMode({ nodeId, onClose }: { nodeId: string; onClose: () => void }) {
  const { message } = AntApp.useApp();
  const [tool, setTool] = useState<Tool>('pen');
  const [color, setColor] = useState(DEFAULT_ANNOTATE_COLOR);
  const [width, setWidth] = useState(DEFAULT_STROKE_WIDTH);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [strokes, setStrokes] = useState<AnnotateStroke[]>([]);
  const [redo, setRedo] = useState<AnnotateStroke[]>([]);
  const [draft, setDraft] = useState<TextDraft | null>(null);
  const [busy, setBusy] = useState(false);
  const [image, setImage] = useState<HTMLImageElement | null>(null);

  const barRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // 绘制中的笔画走 ref 不走 state：pointermove 每帧都来，走 setState 会整树
  // 重渲染；ref + 直接 redraw 只重画 canvas。
  const inflight = useRef<AnnotateStroke | null>(null);

  // 进入模式立刻加载原图：拿 natural 尺寸建立坐标空间，也把跨域失败提前到
  // 落笔之前暴露（画完十笔才发现导不出来最伤）。
  useEffect(() => {
    let alive = true;
    const st = useCanvasStore.getState();
    const node = st.nodes.find((n) => n.id === nodeId);
    // 取数同宫格切分：自己的 content 优先，「上游引用」态退回连线图。
    const url = node?.content || st.getConnectedInputs(nodeId).images[0] || '';
    if (!url) {
      message.warning('当前节点没有可标注的图片');
      onClose();
      return;
    }
    loadAnnotateImage(url)
      .then((img) => {
        if (alive) setImage(img);
      })
      .catch((err) => {
        if (!alive) return;
        message.error(err instanceof Error ? err.message : '原图加载失败');
        onClose();
      });
    return () => {
      alive = false;
    };
  }, [nodeId, message, onClose]);

  // ---- 重绘 ----------------------------------------------------------------
  // 全量重绘：backing store 尺寸每次按当前显示尺寸×DPR 重设（顺带清屏），
  // transform 把原图像素空间投到显示区域，之后所有笔画用原图坐标直接画。
  const redraw = useCallback(() => {
    const canvas = canvasRef.current;
    const img = image;
    if (!canvas || !img) return;
    const rect = canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return;
    const dpr = window.devicePixelRatio || 1;
    canvas.width = Math.round(rect.width * dpr);
    canvas.height = Math.round(rect.height * dpr);
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    const fit = fitRect(img.naturalWidth, img.naturalHeight, rect.width, rect.height);
    const s = dpr / fit.scale;
    ctx.setTransform(s, 0, 0, s, fit.x * dpr, fit.y * dpr);
    const all: AnnotateStroke[] = [...strokes];
    if (inflight.current) all.push(inflight.current);
    replayStrokes(ctx, draft ? commitTextDraft(all, draft) : all);
    // 文字输入点：在草稿文字末尾画一根竖线光标。没有它用户不知道落点在哪、
    // 也无从判断文字工具是否已激活（空草稿 replayStrokes 什么都不画）。
    if (draft) {
      ctx.font = `${draft.fontSize}px sans-serif`;
      const lines = draft.text.split('\n');
      const lastW = ctx.measureText(lines[lines.length - 1]).width;
      const cx = draft.x + lastW;
      const cy = draft.y + (lines.length - 1) * draft.fontSize * 1.25;
      ctx.strokeStyle = draft.color;
      ctx.lineWidth = Math.max(1, draft.fontSize / 14);
      ctx.beginPath();
      ctx.moveTo(cx, cy);
      ctx.lineTo(cx, cy + draft.fontSize);
      ctx.stroke();
    }
  }, [image, strokes, draft]);

  useEffect(() => {
    redraw();
  }, [redraw]);

  // 节点被拉伸/画布缩放时显示尺寸变化，backing store 要跟着重设。
  // jsdom 没有 ResizeObserver（同 GenImageNode 先例），测试环境跳过无害。
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => redraw());
    ro.observe(canvas);
    return () => ro.disconnect();
  }, [redraw]);

  // ---- 丢弃路径：ESC / 点节点外 --------------------------------------------
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      // 正在输入文字时 ESC 只取消草稿，不整个退出模式（已画的笔画不该陪葬）。
      if (draftRef.current) {
        draftRef.current = null;
        setDraft(null);
        return;
      }
      onClose();
    };
    // pointerdown 而不是 mousedown：工具条按钮把 mousedown stopPropagation 了，
    // 但 pointerdown 仍会冒到 window，所以必须 contains 判定（同 openMenu 先例）。
    const onPointer = (e: PointerEvent) => {
      const t = e.target instanceof Node ? e.target : null;
      if (t && (barRef.current?.contains(t) || overlayRef.current?.contains(t))) return;
      onClose();
    };
    window.addEventListener('keydown', onKey);
    window.addEventListener('pointerdown', onPointer);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('pointerdown', onPointer);
    };
  }, [onClose]);

  // ---- 文字草稿 ------------------------------------------------------------
  // draft 的 ref 镜像：commitDraft 要在事件回调里拿「此刻」的草稿，不能依赖
  // 渲染闭包（否则要把 draft 塞进一堆 useCallback 依赖里引发连环重建）。
  const draftRef = useRef<TextDraft | null>(null);
  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);

  const commitDraft = useCallback(() => {
    const d = draftRef.current;
    if (!d) return;
    draftRef.current = null;
    setDraft(null);
    if (!d.text.trim()) return;
    setStrokes((prev) => commitTextDraft(prev, d));
    setRedo([]);
  }, []);

  // draft 出现后聚焦隐藏 textarea 收键盘输入。
  useEffect(() => {
    if (draft) textareaRef.current?.focus();
  }, [draft?.x, draft?.y]); // eslint-disable-line react-hooks/exhaustive-deps

  // ---- 指针绘制 ------------------------------------------------------------
  const pointerInfo = (e: React.PointerEvent) => {
    const canvas = canvasRef.current!;
    const img = image!;
    const rect = canvas.getBoundingClientRect();
    const fit = fitRect(img.naturalWidth, img.naturalHeight, rect.width, rect.height);
    const p = toNatural(
      e.clientX - rect.left,
      e.clientY - rect.top,
      fit,
      img.naturalWidth,
      img.naturalHeight,
    );
    const zoom = useCanvasStore.getState().view.zoom || 1;
    return { p, widthNat: naturalStrokeWidth(width, fit.scale, zoom) };
  };

  const onPointerDown = (e: React.PointerEvent) => {
    // 绘制层接管节点上的指针事件：落笔不能被画布当成拖节点/框选起点。
    // preventDefault 挡掉 mousedown 的默认焦点转移——不挡的话点击会把焦点
    // 从文字工具的隐藏 textarea 上抢走（表现为打字无反应）。
    e.stopPropagation();
    e.preventDefault();
    if (!image || e.button !== 0) return;
    const { p, widthNat } = pointerInfo(e);
    if (tool === 'text') {
      commitDraft();
      setDraft({ x: p.x, y: p.y, text: '', color, fontSize: widthNat * TEXT_FONT_RATIO });
      return;
    }
    // 画笔/矩形落笔也要先定格未提交的文字草稿——语义同「点了别处」。
    commitDraft();
    (e.target as Element).setPointerCapture(e.pointerId);
    inflight.current =
      tool === 'pen'
        ? { kind: 'pen', color, width: widthNat, points: [p] }
        : { kind: 'rect', color, width: widthNat, x: p.x, y: p.y, w: 0, h: 0 };
    redraw();
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const cur = inflight.current;
    if (!cur || !image) return;
    const { p } = pointerInfo(e);
    if (cur.kind === 'pen') cur.points.push(p);
    else if (cur.kind === 'rect') {
      cur.w = p.x - cur.x;
      cur.h = p.y - cur.y;
    }
    redraw();
  };

  const onPointerUp = () => {
    const cur = inflight.current;
    if (!cur) return;
    inflight.current = null;
    setStrokes((prev) => [...prev, cur]);
    setRedo([]);
  };

  // ---- 撤销 / 重做 / 保存 --------------------------------------------------
  const undo = () => {
    // 正在输入的文字草稿本身算一步：先撤它（= 丢弃），再轮到已定格的笔画。
    if (draftRef.current) {
      draftRef.current = null;
      setDraft(null);
      return;
    }
    if (strokes.length === 0) return;
    setRedo([...redo, strokes[strokes.length - 1]]);
    setStrokes(strokes.slice(0, -1));
  };

  const redoOne = () => {
    if (redo.length === 0) return;
    setStrokes([...strokes, redo[redo.length - 1]]);
    setRedo(redo.slice(0, -1));
  };

  const handleSave = async () => {
    if (!image || busy) return;
    // 保存前先定格未提交的文字草稿（对标：liblib 保存时也会带上正在输入的字）。
    let all = strokes;
    if (draft) {
      all = commitTextDraft(strokes, draft);
      setDraft(null);
      setStrokes(all);
    }
    setBusy(true);
    try {
      const id = await executeAnnotateSave({ sourceId: nodeId, image, strokes: all });
      if (id) message.success('标注已保存为新图片节点');
      else message.warning('标注上传失败，已在新节点上标记');
      onClose();
    } catch (err) {
      // 合成失败（跨域污染等）留在模式里——笔画不能丢。
      message.error(err instanceof Error ? err.message : '标注保存失败');
    } finally {
      setBusy(false);
    }
  };

  // ---- UI ------------------------------------------------------------------
  const stopMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };

  const toolBtn = (t: Tool, title: string, icon: React.ReactNode) => (
    <button
      type="button"
      title={title}
      aria-label={title}
      onMouseDown={stopMouseDown}
      onClick={(e) => {
        e.stopPropagation();
        commitDraft();
        setTool(t);
        setPaletteOpen(false);
      }}
      className={`w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer transition-colors ${
        tool === t
          ? 'bg-accent text-foreground'
          : 'bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
      }`}
    >
      {icon}
    </button>
  );

  const histBtn = (title: string, enabled: boolean, onAction: () => void, icon: React.ReactNode) => (
    <button
      type="button"
      title={title}
      aria-label={title}
      disabled={!enabled}
      onMouseDown={stopMouseDown}
      onClick={(e) => {
        e.stopPropagation();
        onAction();
      }}
      className={`w-8 h-8 rounded-lg flex items-center justify-center border-none transition-colors ${
        enabled
          ? 'cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
          : 'cursor-not-allowed bg-transparent text-foreground/30'
      }`}
    >
      {icon}
    </button>
  );

  // 文字草稿的 textarea 显示坐标（overlay 盒子坐标系）。
  const draftPos = (() => {
    if (!draft || !image || !canvasRef.current) return null;
    const rect = canvasRef.current.getBoundingClientRect();
    const fit = fitRect(image.naturalWidth, image.naturalHeight, rect.width, rect.height);
    return { left: fit.x + draft.x / fit.scale, top: fit.y + draft.y / fit.scale };
  })();

  return (
    <>
      {/* 模式工具条：定位与常规工具条同一套 inv-zoom 锚点（解释见 ImageActionsBar 顶注）。 */}
      <div
        ref={barRef}
        className="absolute z-40"
        style={{
          bottom: 'calc(100% + 28px)',
          left: '50%',
          transform: 'scale(var(--inv-zoom, 1)) translate(-50%, -10px)',
          transformOrigin: '0 100%',
        }}
        onMouseDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        <div className="w-max flex items-center gap-0.5 px-1.5 py-1 rounded-xl bg-card border border-border shadow-xl">
          <button
            type="button"
            title="退出标注"
            aria-label="退出标注"
            onMouseDown={stopMouseDown}
            onClick={(e) => {
              e.stopPropagation();
              onClose();
            }}
            className="h-8 px-2 rounded-lg flex items-center gap-1.5 border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60 transition-colors text-[13px] whitespace-nowrap shrink-0"
          >
            <X size={14} />
            标注
          </button>
          <div className="w-px h-4 bg-border mx-1 shrink-0" />
          {toolBtn('pen', '画笔', <Pencil size={14} />)}
          {toolBtn('rect', '矩形', <Square size={14} />)}
          {toolBtn('text', '文字', <Type size={14} />)}
          <div className="w-px h-4 bg-border mx-1 shrink-0" />
          <div className="relative">
            <button
              type="button"
              title="颜色"
              aria-label="颜色"
              onMouseDown={stopMouseDown}
              onClick={(e) => {
                e.stopPropagation();
                setPaletteOpen((v) => !v);
              }}
              className="w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer bg-transparent hover:bg-accent/60 transition-colors"
            >
              <span className="w-4 h-4 rounded-full" style={{ backgroundColor: color }} />
            </button>
            {paletteOpen && (
              <div className="absolute left-1/2 -translate-x-1/2 top-[calc(100%+10px)] flex flex-col gap-1 p-1.5 rounded-2xl border border-solid border-white/25 bg-muted shadow-[0_12px_32px_rgba(0,0,0,0.55)] z-50">
                {ANNOTATE_COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    aria-label={`颜色 ${c}`}
                    onMouseDown={stopMouseDown}
                    onClick={(e) => {
                      e.stopPropagation();
                      setColor(c);
                      setPaletteOpen(false);
                    }}
                    className={`w-6 h-6 rounded-full border-2 border-solid cursor-pointer ${
                      c === color ? 'border-white' : 'border-transparent'
                    }`}
                    style={{ backgroundColor: c }}
                  />
                ))}
              </div>
            )}
          </div>
          <div className="flex items-center gap-1.5 mx-1 shrink-0">
            <Spline size={14} className="text-foreground/60" />
            <input
              type="range"
              aria-label="线宽"
              min={MIN_STROKE_WIDTH}
              max={MAX_STROKE_WIDTH}
              value={width}
              onMouseDown={(e) => e.stopPropagation()}
              onPointerDown={(e) => e.stopPropagation()}
              onChange={(e) => setWidth(Number(e.target.value))}
              className="w-24 accent-white cursor-pointer"
            />
          </div>
          <div className="w-px h-4 bg-border mx-1 shrink-0" />
          {histBtn('撤销', strokes.length > 0 || !!draft, undo, <Undo2 size={14} />)}
          {histBtn('重做', redo.length > 0, redoOne, <Redo2 size={14} />)}
          <button
            type="button"
            aria-label="保存标注"
            disabled={busy}
            onMouseDown={stopMouseDown}
            onClick={(e) => {
              e.stopPropagation();
              handleSave();
            }}
            className={`h-8 px-4 ml-1 rounded-lg border-none text-[13px] font-medium transition-colors whitespace-nowrap shrink-0 ${
              busy
                ? 'cursor-not-allowed bg-white/60 text-neutral-950'
                : 'cursor-pointer bg-white text-neutral-950 hover:bg-neutral-200'
            }`}
          >
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
      {/* 绘制层：透明 canvas 叠在节点内容区上，只画笔画（底图就是下面的 <img>）。 */}
      <div
        ref={overlayRef}
        className="absolute inset-0 z-30"
        onMouseDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      >
        <canvas
          ref={canvasRef}
          className="w-full h-full touch-none select-none cursor-crosshair"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
        />
        {draft && draftPos && (
          // liblib 同款：1×1 透明 textarea 只负责收键盘输入（含 IME），
          // 内容实时绘到 canvas 上；blur = 点了别处，定格草稿。
          <textarea
            ref={textareaRef}
            value={draft.text}
            onChange={(e) => setDraft((d) => (d ? { ...d, text: e.target.value } : d))}
            // blur 不提交而是夺回焦点：画布/节点的各种 focus 行为随时可能抢走
            // 焦点（首版就是被抢走导致「打字无反应」）。提交只走显式路径——
            // 点别处落笔、切工具、保存；丢弃走 ESC/退出。所有这些路径都会先清
            // draftRef，所以不会和这里的夺回打架。
            onBlur={() => {
              setTimeout(() => {
                if (draftRef.current) textareaRef.current?.focus();
              }, 0);
            }}
            onMouseDown={(e) => e.stopPropagation()}
            className="absolute h-px w-px resize-none opacity-0"
            style={{ left: draftPos.left, top: draftPos.top }}
          />
        )}
      </div>
    </>
  );
}
