// 视频节点的剪辑(trim)裁剪条。store.trimmingNodeId === nodeId 时浮现在节点
// 正下方(gen-video 的 prompt 面板在 trim 期间隐藏让位,
// 见 GenVideoNode 里的 trimming 判断)。
// 交互:缩略图条 + 拖拽选区(左右把手/整体平移)+ 时长气泡 + 选区循环预览
// + 整数秒吸附开关 + 全套键盘快捷键(←→ 平移 / ↑↓ 缩放 / I·O 设入出点 /
// Shift 精确 / ⌘·Ctrl 快速 / Space 播放暂停 / Enter 确认 / Esc 取消,说明
// 面板见键盘图标按钮)。确认后:浏览器内转码 → uploadAsset 上传 → 在源
// 节点右侧新建 preview 节点写入产物 URL 并连线(用户拍板:要连线上游)。
// 没有「循环播放开关」——选区预览常开循环。
//
// 定位方案同 VideoActionsBar(inv-zoom 反缩放,屏幕尺寸恒定),那边注释有
// 逐行解释。
import { redactSecrets } from '@/shared/redact';
import { useCallback, useEffect, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { Check, Keyboard, Loader2, Magnet, X } from 'lucide-react';
import { uploadAsset, currentProjectId } from '@/shared/api';
import { useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { spawnPostprocessNode } from '../useMediaNodeMenu';
import {
  clampSelection,
  dragSelection,
  extractFilmstrip,
  moveSelection,
  resizeSelection,
  trimVideo,
  MIN_TRIM_DURATION,
} from './videoTrim';

const FRAME_COUNT = 12;
const STRIP_WIDTH = 384; // 12 帧 × 32px,屏幕像素(bar 整体被 inv-zoom 反缩放)

type DragKind = 'left' | 'right' | 'move';

// 快捷键面板的一行:左标签右键帽。'+' 这类连接符不渲染键帽样式。
function HelpRow({ label, keys, prefix }: { label: string; keys: string[]; prefix?: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-foreground/70 whitespace-nowrap">{label}</span>
      <span className="flex items-center gap-1">
        {prefix && <span className="text-foreground/50">{prefix}</span>}
        {keys.map((k, i) =>
          k === '+' ? (
            <span key={i} className="text-foreground/50">
              +
            </span>
          ) : (
            <kbd
              key={i}
              className="px-1.5 py-0.5 rounded-md bg-accent/60 border border-border text-[11px] font-medium not-italic"
            >
              {k}
            </kbd>
          ),
        )}
      </span>
    </div>
  );
}

export function VideoTrimBar({ nodeId, url }: { nodeId: string; url: string }) {
  const { message } = AntApp.useApp();
  const show = useCanvasStore((s) => s.trimmingNodeId === nodeId);
  const [duration, setDuration] = useState(0);
  const [frames, setFrames] = useState<string[]>([]);
  const [sel, setSel] = useState({ start: 0, end: 0 });
  const [phase, setPhase] = useState<'loading' | 'edit' | 'processing'>('loading');
  const [progress, setProgress] = useState(0);
  const [snap, setSnap] = useState(false);
  const [showHelp, setShowHelp] = useState(false);
  const stripRef = useRef<HTMLDivElement>(null);
  // 拖拽期间的锚点:按下时的 clientX 与当时的选区。装在 ref 里,move 事件不触发重渲染链。
  const dragRef = useRef<{ kind: DragKind; x0: number; sel0: { start: number; end: number } } | null>(null);
  // snap 的 ref 镜像:onMove 挂在只依赖 duration 的 effect 里,读 state 会拿到旧闭包。
  const snapRef = useRef(false);
  // Space 暂停标记:选区变化会重跑循环预览 effect,不带这个标记会把用户刚按下的暂停顶掉。
  const pausedRef = useRef(false);

  // 打开时抽帧 + 初始化全选。关闭/换节点时状态自然被卸载重置(组件按 show 卸载)。
  useEffect(() => {
    if (!show) return;
    let alive = true;
    setPhase('loading');
    extractFilmstrip(url, FRAME_COUNT)
      .then(({ duration: d, frames: f }) => {
        if (!alive) return;
        setDuration(d);
        setFrames(f);
        setSel({ start: 0, end: d });
        setPhase('edit');
      })
      .catch((e) => {
        if (!alive) return;
        message.error(e?.message ? redactSecrets(String(e.message)) : '视频加载失败,无法剪辑');
        useCanvasStore.getState().setTrimmingNode(null);
      });
    return () => {
      alive = false;
    };
  }, [show, url, message]);

  // 选区循环预览:直接驱动宿主节点内的 <video>(经 NodeShell 的 data-node-id
  // 定位,免去三个宿主各自穿 ref)。选区变化就跳回起点,拖拽时即时反馈。
  useEffect(() => {
    if (!show || phase !== 'edit') return;
    const video = document.querySelector<HTMLVideoElement>(`[data-node-id="${nodeId}"] video`);
    if (!video) return;
    video.currentTime = sel.start;
    if (!pausedRef.current) void video.play().catch(() => {});
    const onTime = () => {
      if (video.currentTime >= sel.end - 0.03 || video.currentTime < sel.start - 0.25) {
        video.currentTime = sel.start;
      }
    };
    video.addEventListener('timeupdate', onTime);
    return () => video.removeEventListener('timeupdate', onTime);
  }, [show, phase, nodeId, sel.start, sel.end]);

  const cancel = useCallback(() => {
    if (phase === 'processing') return; // 转码/上传中不允许取消——半个产物没意义
    useCanvasStore.getState().setTrimmingNode(null);
  }, [phase]);

  const confirm = useCallback(async () => {
    if (phase !== 'edit') return;
    setPhase('processing');
    setProgress(0);
    try {
      const blob = await trimVideo(url, sel.start, sel.end, setProgress);
      const file = new File([blob], `trim-${sel.start.toFixed(2)}s-${sel.end.toFixed(2)}s.mp4`, {
        type: 'video/mp4',
      });
      const asset = await uploadAsset(file, currentProjectId());
      const store = useCanvasStore.getState();
      const newId = spawnPostprocessNode('preview', nodeId);
      if (newId) {
        store.updateNode(newId, { content: asset.url, status: 'succeeded' });
        // 尺寸抄源节点:剪辑不改画面,产物节点跟源节点一样大才不会视觉跳变
        // (spawnPostprocessNode 落的是 preview 默认 320×260)。同尺寸下直接
        // 取同 y 即垂直对齐,x 仍是源节点右侧留 80 间距。
        const src = store.nodes.find((n) => n.id === nodeId);
        if (src) {
          const srcW = src.width ?? 320;
          store.updateNode(newId, {
            width: srcW,
            height: src.height ?? 260,
            x: src.x + srcW + 80,
            y: src.y,
          });
        }
        // preview 是通用媒体容器,靠 settings.kind 自报产物类型——签名 URL 可能
        // 没有扩展名,下游分类(getConnectedInputs)只有 kind 兜得住。
        store.updateNodeSettings(newId, { kind: 'video' });
        persistNodeById(newId);
      }
      store.setTrimmingNode(null);
      message.success('剪辑完成');
    } catch (e: any) {
      message.error(e?.response?.data?.error || e?.message || '剪辑失败');
      setPhase('edit'); // 选区保留,可直接重试
    }
  }, [phase, url, sel.start, sel.end, nodeId, message]);

  // 快捷键。挂 window:焦点通常不在裁剪条里
  // (用户刚拖完画布)。capture + stopPropagation 挡住画布自己的快捷键——
  // trim 期间是模态语义。
  useEffect(() => {
    if (!show) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        cancel();
        return;
      }
      if (e.key === 'Enter') {
        e.stopPropagation();
        void confirm();
        return;
      }
      // 正在别处输入(聊天框等)时不劫持编辑键;Esc/Enter 是模态操作,不受此限。
      const t = e.target as HTMLElement | null;
      const typing = !!t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable);
      if (typing || phase !== 'edit' || duration <= 0) return;
      // 步长:默认 0.1s;按住 Shift=精确模式 0.01s;⌘/Ctrl=快速调整 1s。
      const step = e.shiftKey ? 0.01 : e.metaKey || e.ctrlKey ? 1 : 0.1;
      const video = document.querySelector<HTMLVideoElement>(`[data-node-id="${nodeId}"] video`);
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault();
        e.stopPropagation();
        const dt = e.key === 'ArrowLeft' ? -step : step;
        setSel((prev) => moveSelection(prev, dt, duration));
      } else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
        e.preventDefault();
        e.stopPropagation();
        const dt = e.key === 'ArrowUp' ? step : -step;
        setSel((prev) => resizeSelection(prev, dt, duration));
      } else if (e.key === 'i' || e.key === 'I') {
        // 入点设为当前播放位置。钳制交给 clampSelection(不短于最小时长)。
        e.stopPropagation();
        if (video) setSel((prev) => clampSelection(video.currentTime, prev.end, duration));
      } else if (e.key === 'o' || e.key === 'O') {
        e.stopPropagation();
        if (video) setSel((prev) => clampSelection(prev.start, video.currentTime, duration));
      } else if (e.key === ' ') {
        e.preventDefault(); // Space 默认滚动页面
        e.stopPropagation();
        if (!video) return;
        if (video.paused) {
          pausedRef.current = false;
          void video.play().catch(() => {});
        } else {
          pausedRef.current = true;
          video.pause();
        }
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [show, cancel, confirm, phase, duration, nodeId]);

  // 拖拽:pointerdown 记锚点,window 上收 move/up——把手很小,鼠标滑出也不断拖。
  useEffect(() => {
    const onMove = (e: PointerEvent) => {
      const drag = dragRef.current;
      if (!drag || duration <= 0) return;
      const dt = ((e.clientX - drag.x0) / STRIP_WIDTH) * duration;
      setSel(dragSelection(drag.kind, drag.sel0, dt, duration, snapRef.current));
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
      // 定位同 GenVideoNode 的 prompt 面板:锚在节点下缘,inv-zoom 反缩放后
      // 水平居中、再留 12px 屏幕间隙(下方没有名字 chip,不需要世界单位让位)。
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
      {showHelp && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 w-[480px] rounded-xl bg-card border border-border shadow-xl p-4">
          <div className="flex items-center justify-between mb-3">
            <span className="text-sm font-semibold">快捷键</span>
            <button
              type="button"
              onMouseDown={(e) => {
                e.preventDefault();
                e.stopPropagation();
              }}
              onClick={(e) => {
                e.stopPropagation();
                setShowHelp(false);
              }}
              className="w-6 h-6 rounded flex items-center justify-center border-none cursor-pointer bg-transparent text-foreground/60 hover:text-foreground hover:bg-accent/60 transition-colors"
            >
              <X size={13} />
            </button>
          </div>
          <div className="grid grid-cols-2 gap-x-8 gap-y-2.5 text-xs text-foreground/90">
            <HelpRow label="移动选区" keys={['←', '→']} />
            <HelpRow label="精确模式" prefix="按住" keys={['Shift']} />
            <HelpRow label="扩展/收缩选区" keys={['↑', '↓']} />
            <HelpRow label="快速调整" keys={['⌘/Ctrl', '+', '↑', '↓']} />
            <HelpRow label="设置入点出点" keys={['I', 'O']} />
            <HelpRow label="播放/暂停" keys={['Space']} />
            <HelpRow label="退出" keys={['Esc']} />
            <HelpRow label="确认剪辑" keys={['Enter']} />
          </div>
        </div>
      )}
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

        <button
          type="button"
          title="快捷键"
          onMouseDown={(e) => {
            e.preventDefault();
            e.stopPropagation();
          }}
          onClick={(e) => {
            e.stopPropagation();
            setShowHelp((v) => !v);
          }}
          className={`w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer transition-colors shrink-0 ${
            showHelp
              ? 'bg-accent text-foreground'
              : 'bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
          }`}
        >
          <Keyboard size={15} />
        </button>

        {/* 缩略图条 + 选区 */}
        <div
          ref={stripRef}
          className="relative h-12 rounded-lg overflow-hidden bg-muted shrink-0 select-none"
          style={{ width: STRIP_WIDTH }}
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
              {/* 选区外的暗化 */}
              <div className="absolute inset-y-0 left-0 bg-black/60 pointer-events-none" style={{ width: `${leftPct}%` }} />
              <div className="absolute inset-y-0 right-0 bg-black/60 pointer-events-none" style={{ width: `${100 - rightPct}%` }} />
              {/* 选区窗口:内部平移,两侧把手 */}
              <div
                className="absolute inset-y-0 border-2 border-solid border-white rounded-md cursor-grab"
                style={{ left: `${leftPct}%`, width: `${rightPct - leftPct}%` }}
                onPointerDown={startDrag('move')}
              >
                <div
                  className="absolute -left-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                  onPointerDown={startDrag('left')}
                >
                  <div className="w-1 h-5 rounded-full bg-white" />
                </div>
                <div
                  className="absolute -right-0.5 inset-y-0 w-2.5 cursor-ew-resize flex items-center justify-center"
                  onPointerDown={startDrag('right')}
                >
                  <div className="w-1 h-5 rounded-full bg-white" />
                </div>
                {/* 时长气泡 */}
                <div className="absolute left-1/2 -translate-x-1/2 top-1/2 -translate-y-1/2 px-2 py-0.5 rounded-md bg-black/70 text-white text-[11px] font-medium whitespace-nowrap pointer-events-none">
                  {(sel.end - sel.start).toFixed(2)} s
                </div>
              </div>
            </>
          )}
          {phase === 'processing' && (
            <div className="absolute inset-0 flex items-center justify-center text-white text-xs gap-1.5 bg-black/70">
              <Loader2 size={13} className="animate-spin" />
              处理中… {Math.round(progress * 100)}%
            </div>
          )}
        </div>

        <button
          type="button"
          title={snap ? '关闭整数秒吸附' : '开启整数秒吸附'}
          onMouseDown={(e) => {
            e.preventDefault();
            e.stopPropagation();
          }}
          onClick={(e) => {
            e.stopPropagation();
            setSnap((v) => {
              snapRef.current = !v;
              return !v;
            });
          }}
          className={`w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer transition-colors shrink-0 ${
            snap
              ? 'bg-accent text-foreground'
              : 'bg-transparent text-foreground/50 hover:text-foreground hover:bg-accent/60'
          }`}
        >
          <Magnet size={15} />
        </button>

        <button
          type="button"
          title="确认剪辑 (Enter)"
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
  );
}
