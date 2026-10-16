import { useEffect, useRef, useState } from 'react';
import { Loader2, Pause, Play } from 'lucide-react';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { useCanvasStore } from '../store';
import { MIN_TRIM_DURATION, dragSelection } from './trim/videoTrim';

// 画布音频节点共用的波形播放器（音频输入 / 预览 / 生成音频三处同一份皮肤）。
//
// 流量纪律（用户点名的硬约束）：**点播放之前一个字节音频本体都不拉**。
// - 初始只挂 <audio preload="metadata">——浏览器 Range 拉文件头几 KB 拿总时长，
//   波形画的是 url 做种子的确定性假柱子（每次渲染形状一致，不闪）。
// - 首次点播放才 fetch 全量：同一份 ArrayBuffer 一份转 blob URL 喂 <audio> 播放、
//   一份喂 decodeAudioData 换真波形——总流量 = 一次完整下载，与原生播放器点一次
//   播放等价，波形是白捡的。
// - fetch/decode 任一步失败（CORS 没放行 fetch 之类）静默降级：占位波形照旧，
//   播放退回 <audio> 直接流式拉原始 URL，功能不受影响。
export const WAVEFORM_BAR_COUNT = 56;

export function formatClock(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '0:00';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${m}:${String(s).padStart(2, '0')}`;
}

// 占位假波形：url 做种子的 xorshift 伪随机。要求确定性——React 重渲染、节点
// 拖动都会反复执行，用 Math.random 会看到波形每帧乱跳。
export function placeholderPeaks(seed: string, n = WAVEFORM_BAR_COUNT): number[] {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  if (h === 0) h = 88675123;
  const out: number[] = [];
  for (let i = 0; i < n; i++) {
    h ^= h << 13;
    h ^= h >>> 17;
    h ^= h << 5;
    h |= 0;
    out.push(0.2 + ((h >>> 0) % 1000) / 1000 * 0.65);
  }
  return out;
}

// PCM → N 根柱子：分桶取绝对值峰值再整体归一。桶内按步长抽样（≥200 点/桶）
// 而不是逐样本扫——3 分钟 mp3 有 800 万采样点，全扫是白烧的主线程时间，
// 波形精度上 200 点足够。
export function peaksFromChannel(data: Float32Array, n = WAVEFORM_BAR_COUNT): number[] {
  if (data.length === 0) return placeholderPeaks('empty', n);
  const bucket = Math.max(1, Math.floor(data.length / n));
  const stride = Math.max(1, Math.floor(bucket / 200));
  const out: number[] = [];
  let max = 0;
  for (let i = 0; i < n; i++) {
    const start = i * bucket;
    const end = Math.min(data.length, start + bucket);
    let peak = 0;
    for (let j = start; j < end; j += stride) {
      const v = Math.abs(data[j]);
      if (v > peak) peak = v;
    }
    out.push(peak);
    if (peak > max) max = peak;
  }
  if (max === 0) return out.map(() => 0.05);
  // 地板 0.06：静音段留一排小点而不是空白，和参考样式一致。
  return out.map((p) => Math.max(0.06, p / max));
}

export function WaveformPlayer({
  url,
  nodeId,
  topRight,
  className = '',
}: {
  url: string;
  /** 宿主节点 id。传了才参与「截取/变速」编辑会话（选区层画在波形上、变速
   *  实时预览）；不传就是纯播放器。 */
  nodeId?: string;
  /** 波形区右上角的浮动操作位（更换音频 / 下载等），由调用方决定内容。 */
  topRight?: React.ReactNode;
  className?: string;
}) {
  const audioRef = useRef<HTMLAudioElement>(null);
  const waveRef = useRef<HTMLDivElement>(null);
  // 播放头拖动中。用户拍板 2026-08-18：**只有按住红色播放头线才能拖动改进度**；
  // 波形其他位置完全不拦事件——点击走节点选中（工具条弹出）、按住拖动就是拖节点，
  // 和节点身体的其他区域一致。
  const draggingRef = useRef(false);
  const blobUrlRef = useRef<string | null>(null);
  const loadPromiseRef = useRef<Promise<void> | null>(null);
  // 卸载标记：全量 fetch 可能在卸载后才返回，这时再 createObjectURL 就没人回收了。
  const unmountedRef = useRef(false);

  const [playing, setPlaying] = useState(false);
  const [loading, setLoading] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState<number | null>(null);
  const [peaks, setPeaks] = useState<number[] | null>(null);

  // 本节点的音频编辑会话（截取选区 / 变速倍率）。选区把手画在波形上、模式条
  // 在 AudioActionsBar，两棵树靠 store 共享。没传 nodeId 时恒为 null。
  const audioEdit = useCanvasStore((s) =>
    nodeId && s.audioEdit?.nodeId === nodeId ? s.audioEdit : null,
  );
  const setAudioEdit = useCanvasStore((s) => s.setAudioEdit);
  // 编辑把手拖动中的初始选区（pointer capture 期间的基准）。
  const editDragRef = useRef<{ kind: 'left' | 'right'; startX: number; sel: { start: number; end: number } } | null>(null);

  // 进入编辑会话时回填时长 + 给截取一个默认选区（前 1/3，对标 liblib）。
  // duration 只有这里知道（metadata 预载/解码后），模式条在拿到前禁用提交。
  useEffect(() => {
    if (!audioEdit || audioEdit.duration != null || duration == null) return;
    setAudioEdit({
      ...audioEdit,
      duration,
      start: 0,
      end:
        audioEdit.mode === 'trim'
          ? Math.max(Math.min(duration / 3, duration), Math.min(MIN_TRIM_DURATION, duration))
          : audioEdit.end,
    });
  }, [audioEdit, duration, setAudioEdit]);

  // 变速实时预览：调倍率立即作用到播放（浏览器 preservesPitch 默认开，听感
  // 即成品），时长显示同步换算。退出会话回 1。
  const previewRate = audioEdit?.mode === 'speed' ? audioEdit.rate : 1;
  useEffect(() => {
    const el = audioRef.current;
    if (el) el.playbackRate = previewRate;
  }, [previewRate]);

  // url 变化（更换音频 / 切 history 版本）由调用方 key={url} 重挂载解决——
  // 这里只需管好卸载时回收 blob。
  useEffect(() => {
    unmountedRef.current = false;
    return () => {
      unmountedRef.current = true;
      if (blobUrlRef.current) URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    };
  }, []);

  // 播放中的红线/已播柱子用 rAF 驱动——timeupdate 只有 ~4Hz，播放头会一顿一顿。
  useEffect(() => {
    if (!playing) return undefined;
    let raf = 0;
    const tick = () => {
      const el = audioRef.current;
      if (el) setCurrentTime(el.currentTime);
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [playing]);

  const ensureFullAudio = () => {
    if (!loadPromiseRef.current) {
      loadPromiseRef.current = (async () => {
        setLoading(true);
        try {
          const res = await fetch(url);
          if (!res.ok) throw new Error(`audio fetch ${res.status}`);
          const buf = await res.arrayBuffer();
          // 下载期间节点已删 / 换了音频（key 重挂载）：卸载清理已经跑过，此处
          // 再造 blob 会整段音频常驻内存，解码也是白烧。
          if (unmountedRef.current) return;
          // blob 必须用 slice 副本——decodeAudioData 会 detach 传入的 ArrayBuffer。
          const blobUrl = URL.createObjectURL(
            new Blob([buf.slice(0)], { type: res.headers.get('content-type') || 'audio/mpeg' }),
          );
          blobUrlRef.current = blobUrl;
          const el = audioRef.current;
          if (el) {
            const t = el.currentTime;
            el.src = blobUrl;
            if (t > 0) el.currentTime = t;
          }
          const Ctx: typeof AudioContext | undefined =
            window.AudioContext || (window as any).webkitAudioContext;
          if (Ctx) {
            const ctx = new Ctx();
            try {
              const decoded = await ctx.decodeAudioData(buf);
              setPeaks(peaksFromChannel(decoded.getChannelData(0)));
              setDuration(decoded.duration);
            } finally {
              void ctx.close();
            }
          }
        } catch {
          // 静默降级，见文件头注释。loadPromiseRef 不清空：失败通常是 CORS/格式
          // 这类确定性原因，重试只会反复全量下载。
        } finally {
          setLoading(false);
        }
      })();
    }
    return loadPromiseRef.current;
  };

  const togglePlay = async () => {
    const el = audioRef.current;
    if (!el) return;
    if (playing) {
      el.pause();
      return;
    }
    // 先等全量下载换成 blob 源再 play——直接 play 会让 <audio> 自己流式拉原始
    // URL，和 fetch 叠成两次完整下载。
    await ensureFullAudio();
    try {
      await el.play();
    } catch {
      /* 播放被浏览器拒绝（极少数策略场景）就保持暂停态，不抛给用户 */
    }
  };

  const effectiveDuration =
    duration ?? (Number.isFinite(audioRef.current?.duration ?? NaN) ? audioRef.current!.duration : null);
  const progress =
    effectiveDuration && effectiveDuration > 0 ? Math.min(1, currentTime / effectiveDuration) : 0;

  const seekFromClientX = (clientX: number) => {
    const wave = waveRef.current;
    const el = audioRef.current;
    if (!wave || !el || !effectiveDuration) return;
    const rect = wave.getBoundingClientRect();
    if (rect.width <= 0) return;
    // clientX 与 rect 同为屏幕像素，比值天然免疫画布 zoom。
    const frac = Math.min(1, Math.max(0, (clientX - rect.left) / rect.width));
    el.currentTime = frac * effectiveDuration;
    setCurrentTime(frac * effectiveDuration);
  };

  const bars = peaks ?? placeholderPeaks(url);

  return (
    <div className={`relative flex flex-col min-h-0 ${className}`}>
      {/* 播放本体。hidden 而不是不渲染：metadata 预载靠它拿总时长。 */}
      {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
      <audio
        ref={audioRef}
        src={url}
        preload="metadata"
        crossOrigin={mediaCrossOrigin(url)}
        className="hidden"
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onTimeUpdate={(e) => setCurrentTime(e.currentTarget.currentTime)}
        onLoadedMetadata={(e) => {
          const d = e.currentTarget.duration;
          // decode 出的时长更准，别用 metadata 的覆盖它。
          if (Number.isFinite(d)) setDuration((prev) => prev ?? d);
        }}
      />

      {topRight && <div className="absolute top-1 right-1 z-10">{topRight}</div>}

      {/* 波形本身不是交互区：点击/按住冒泡给 NodeShell（选中弹工具条 / 拖节点）。 */}
      <div
        ref={waveRef}
        data-testid="waveform"
        className="relative flex-1 min-h-0 flex items-center gap-[2px] px-1 select-none"
      >
        {bars.map((p, i) => (
          <div
            key={i}
            className="flex-1 rounded-full"
            style={{
              height: `${Math.round(p * 100)}%`,
              minHeight: 2,
              background:
                progress > 0 && i < progress * bars.length
                  ? 'rgba(255,255,255,0.95)'
                  : 'rgba(255,255,255,0.28)',
            }}
          />
        ))}
        {/* 播放头 = 唯一的进度交互入口，进度 0 也渲染（不然从头就没得抓）。
            外层是 12px 宽的隐形抓握热区（2px 线本身按不中），pointer capture
            让拖出波形区仍持续跟手。 */}
        <div
          data-testid="playhead"
          className="absolute top-0 bottom-0 w-3 -translate-x-1/2 flex justify-center cursor-ew-resize touch-none"
          style={{ left: `${progress * 100}%` }}
          onMouseDown={(e) => e.stopPropagation()}
          onPointerDown={(e) => {
            e.stopPropagation();
            draggingRef.current = true;
            e.currentTarget.setPointerCapture(e.pointerId);
          }}
          onPointerMove={(e) => {
            if (draggingRef.current) seekFromClientX(e.clientX);
          }}
          onPointerUp={() => {
            draggingRef.current = false;
          }}
          onPointerCancel={() => {
            draggingRef.current = false;
          }}
        >
          <div className="w-[2px] h-full rounded bg-red-400" />
        </div>

        {/* 截取选区层：盖满波形，选区外压暗、白框选区 + 左右把手 + 时长标签。
            拖拽数学复用视频剪辑的 dragSelection（钳制/最小时长同一套规则）。 */}
        {audioEdit?.mode === 'trim' && audioEdit.duration != null && (
          <div
            data-testid="trim-overlay"
            className="absolute inset-0 z-10 select-none"
            onMouseDown={(e) => e.stopPropagation()}
          >
            <div
              className="absolute top-0 bottom-0 left-0 bg-black/60"
              style={{ width: `${(audioEdit.start / audioEdit.duration) * 100}%` }}
            />
            <div
              className="absolute top-0 bottom-0 right-0 bg-black/60"
              style={{ width: `${100 - (audioEdit.end / audioEdit.duration) * 100}%` }}
            />
            <div
              className="absolute top-0 bottom-0 border-2 border-solid border-white rounded-md pointer-events-none"
              style={{
                left: `${(audioEdit.start / audioEdit.duration) * 100}%`,
                width: `${((audioEdit.end - audioEdit.start) / audioEdit.duration) * 100}%`,
              }}
            >
              <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 px-2 py-0.5 rounded-md bg-black/70 text-white text-[11px] font-mono whitespace-nowrap">
                {(audioEdit.end - audioEdit.start).toFixed(1)}s
              </span>
            </div>
            {(['left', 'right'] as const).map((kind) => (
              <div
                key={kind}
                data-testid={`trim-handle-${kind}`}
                className="absolute top-0 bottom-0 w-4 -translate-x-1/2 flex items-center justify-center cursor-ew-resize touch-none"
                style={{
                  left: `${((kind === 'left' ? audioEdit.start : audioEdit.end) / audioEdit.duration!) * 100}%`,
                }}
                onPointerDown={(e) => {
                  e.stopPropagation();
                  e.currentTarget.setPointerCapture(e.pointerId);
                  editDragRef.current = {
                    kind,
                    startX: e.clientX,
                    sel: { start: audioEdit.start, end: audioEdit.end },
                  };
                }}
                onPointerMove={(e) => {
                  const drag = editDragRef.current;
                  const wave = waveRef.current;
                  const cur = useCanvasStore.getState().audioEdit;
                  if (!drag || !wave || !cur || cur.duration == null) return;
                  const rect = wave.getBoundingClientRect();
                  if (rect.width <= 0) return;
                  // 与播放头同理：clientX/rect 都是屏幕像素，比值免疫画布 zoom。
                  const dt = ((e.clientX - drag.startX) / rect.width) * cur.duration;
                  const next = dragSelection(drag.kind, drag.sel, dt, cur.duration, false);
                  setAudioEdit({ ...cur, ...next });
                }}
                onPointerUp={() => {
                  editDragRef.current = null;
                }}
                onPointerCancel={() => {
                  editDragRef.current = null;
                }}
              >
                <div className="w-[4px] h-2/3 rounded-full bg-white shadow" />
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="relative shrink-0 h-8 mt-1 flex items-center justify-center">
        {/* 变速预览中时长按倍率换算显示（对标 liblib：调到 2x 时 02:16 即显 01:08）。 */}
        <span className="absolute left-1 text-[10px] font-mono text-white/70 pointer-events-none">
          {formatClock(currentTime / previewRate)} /{' '}
          {effectiveDuration != null ? formatClock(effectiveDuration / previewRate) : '--:--'}
        </span>
        <button
          type="button"
          aria-label={playing ? '暂停' : '播放'}
          className="w-7 h-7 rounded-full bg-white hover:bg-white/85 text-black border-none cursor-pointer flex items-center justify-center shadow"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            void togglePlay();
          }}
        >
          {loading ? (
            <Loader2 size={13} className="animate-spin" />
          ) : playing ? (
            <Pause size={13} fill="currentColor" />
          ) : (
            <Play size={13} fill="currentColor" className="ml-[1px]" />
          )}
        </button>
      </div>
    </div>
  );
}
