import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type VideoHTMLAttributes,
} from 'react';
import { Maximize, Minimize, MoreVertical, Pause, Play, Volume2, VolumeX } from 'lucide-react';

// 自绘视频控制条（仿 Chrome 原生样式）。
//
// 为什么不用 <video controls>：桌面端跑在 WKWebView（Safari 内核），原生控制条
// 按元素的**布局宽度**排版——画布节点布局宽只有 ~280px，WebKit 进紧凑模式把
// 进度条压成一根竖线、删掉总时长；画布 transform scale 再把这套小尺寸毛玻璃放大，
// 图标和模糊层整体发虚。原生控制条的样式与布局都不受页面控制，只能自己画。
//
// 拖拽口径：控制条根节点带 data-video-controls 并拦 mousedown 冒泡——NodeShell
// 捕获阶段据此只做选中、不启动拖动；画面区域放行冒泡，按住画面照常拖节点。

export type VideoPlayerProps = Omit<VideoHTMLAttributes<HTMLVideoElement>, 'controls'> & {
  /** 外层容器的尺寸类名；默认撑满父元素。灯箱等按视频自身尺寸排版的场景传 inline-flex 之类。 */
  wrapperClassName?: string;
};

const SPEEDS = [0.5, 1, 1.5, 2];
// 播放中鼠标静止多久后收起控制条（同 Chrome 的观感，约 2.5s）。
const IDLE_HIDE_MS = 2500;
// mousedown → click 之间位移超过这个值视为拖动（拖节点），不切换播放。
const CLICK_SLOP = 4;

function formatTime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '0:00';
  const s = Math.floor(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

type WebkitVideo = HTMLVideoElement & {
  webkitEnterFullscreen?: () => void;
  webkitSupportsPresentationMode?: (mode: string) => boolean;
  webkitSetPresentationMode?: (mode: string) => void;
};

export const VideoPlayer = forwardRef<HTMLVideoElement, VideoPlayerProps>(function VideoPlayer(
  { wrapperClassName, className, onClick, onMouseDown, ...videoProps },
  ref,
) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  useImperativeHandle(ref, () => videoRef.current as HTMLVideoElement);

  const [paused, setPaused] = useState(true);
  const [muted, setMuted] = useState(!!videoProps.muted);
  const [volume, setVolume] = useState(1);
  const [volumeOpen, setVolumeOpen] = useState(false);
  const [current, setCurrent] = useState(0);
  const [duration, setDuration] = useState(0);
  const [buffered, setBuffered] = useState(0);
  const [rate, setRate] = useState(1);
  const [active, setActive] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const idleTimer = useRef<number | undefined>(undefined);
  const downPos = useRef<{ x: number; y: number } | null>(null);
  const seeking = useRef(false);

  const v = () => videoRef.current as WebkitVideo | null;

  // 同步 <video> 自身状态（autoPlay、键盘、画中画里操作都会改它，不能只信本地 state）。
  useEffect(() => {
    const el = videoRef.current;
    if (!el) return;
    const sync = () => {
      setPaused(el.paused);
      setMuted(el.muted);
      setVolume(el.volume);
      if (!seeking.current) setCurrent(el.currentTime);
      setDuration(Number.isFinite(el.duration) ? el.duration : 0);
      setRate(el.playbackRate);
      const b = el.buffered;
      setBuffered(b.length ? b.end(b.length - 1) : 0);
    };
    const events = ['play', 'pause', 'timeupdate', 'durationchange', 'loadedmetadata', 'progress', 'volumechange', 'ratechange', 'ended', 'emptied'];
    events.forEach((ev) => el.addEventListener(ev, sync));
    sync();
    return () => events.forEach((ev) => el.removeEventListener(ev, sync));
  }, [videoProps.src]);

  useEffect(() => {
    const onFs = () => setFullscreen(document.fullscreenElement === wrapRef.current);
    document.addEventListener('fullscreenchange', onFs);
    return () => document.removeEventListener('fullscreenchange', onFs);
  }, []);

  useEffect(() => () => window.clearTimeout(idleTimer.current), []);

  const poke = useCallback(() => {
    setActive(true);
    window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => setActive(false), IDLE_HIDE_MS);
  }, []);

  const togglePlay = () => {
    const el = v();
    if (!el) return;
    if (el.paused || el.ended) void el.play().catch(() => {});
    else el.pause();
  };

  const toggleMute = () => {
    const el = v();
    if (!el) return;
    // 音量拖到 0 后再点喇叭：恢复成可听的音量，而不是「取消静音但还是 0」。
    if (el.muted || el.volume === 0) {
      el.muted = false;
      if (el.volume === 0) el.volume = 0.5;
    } else {
      el.muted = true;
    }
  };

  const setVolumeFromPointer = (e: ReactPointerEvent<HTMLDivElement>) => {
    const el = v();
    if (!el) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    el.volume = ratio;
    el.muted = ratio === 0;
  };

  const toggleFullscreen = () => {
    const wrap = wrapRef.current;
    const el = v();
    if (!wrap || !el) return;
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => {});
      return;
    }
    // 优先整块容器全屏（自绘控制条跟着进全屏）；WebView 没开元素全屏时
    // 退回 WebKit 的视频原生全屏。
    if (wrap.requestFullscreen) {
      wrap.requestFullscreen().catch(() => el.webkitEnterFullscreen?.());
    } else {
      el.webkitEnterFullscreen?.();
    }
  };

  const canPip =
    typeof document !== 'undefined' &&
    (('pictureInPictureEnabled' in document && document.pictureInPictureEnabled) ||
      !!(videoRef.current as WebkitVideo | null)?.webkitSupportsPresentationMode?.('picture-in-picture'));

  const enterPip = () => {
    const el = v();
    if (!el) return;
    if (el.requestPictureInPicture) void el.requestPictureInPicture().catch(() => {});
    else el.webkitSetPresentationMode?.('picture-in-picture');
  };

  const seekFromPointer = (e: ReactPointerEvent<HTMLDivElement>) => {
    const el = v();
    if (!el || !duration) return;
    // 用屏幕坐标比例换算：画布 transform scale 对比例无影响。
    const rect = e.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    const t = ratio * duration;
    setCurrent(t);
    el.currentTime = t;
  };

  const onVideoMouseDown = (e: ReactMouseEvent<HTMLVideoElement>) => {
    downPos.current = { x: e.clientX, y: e.clientY };
    onMouseDown?.(e);
  };

  const onVideoClick = (e: ReactMouseEvent<HTMLVideoElement>) => {
    onClick?.(e);
    const d = downPos.current;
    downPos.current = null;
    // 按住画面拖节点后松手也会触发 click，位移超阈值不算点击。
    if (d && Math.hypot(e.clientX - d.x, e.clientY - d.y) > CLICK_SLOP) return;
    togglePlay();
  };

  const visible = paused || active || menuOpen || volumeOpen;
  const shownVolume = muted ? 0 : volume;
  const pct = duration ? (current / duration) * 100 : 0;
  const bufPct = duration ? Math.min(100, (buffered / duration) * 100) : 0;
  const iconBtn =
    'w-8 h-8 shrink-0 flex items-center justify-center rounded-full bg-transparent border-none text-white cursor-pointer hover:bg-white/15';

  return (
    <div
      ref={wrapRef}
      className={`relative overflow-hidden ${fullscreen ? 'w-full h-full bg-black flex items-center justify-center' : wrapperClassName ?? 'w-full h-full'}`}
      onMouseMove={poke}
      onMouseLeave={() => setActive(false)}
    >
      <video
        ref={videoRef}
        {...videoProps}
        className={fullscreen ? 'w-full h-full object-contain bg-black' : className}
        onMouseDown={onVideoMouseDown}
        onClick={onVideoClick}
      />

      <div
        data-video-controls
        className={`absolute inset-x-0 bottom-0 px-3 pt-6 pb-2 flex flex-col gap-1.5 bg-gradient-to-t from-black/75 via-black/40 to-transparent transition-opacity duration-200 ${
          visible ? 'opacity-100' : 'opacity-0 pointer-events-none'
        }`}
        onMouseDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-1">
          <button type="button" tabIndex={-1} className={iconBtn} title={paused ? '播放' : '暂停'} aria-label={paused ? '播放' : '暂停'} onClick={togglePlay}>
            {paused ? <Play size={18} fill="currentColor" /> : <Pause size={18} fill="currentColor" />}
          </button>
          <span className="ml-1 text-white text-[13px] tabular-nums whitespace-nowrap select-none">
            {formatTime(current)}
            <span className="text-white/60"> / {formatTime(duration)}</span>
          </span>
          <div className="flex-1" />
          {/* 音量：悬停喇叭时向左展开一枚胶囊，内含音量滑杆（同 Chrome）。 */}
          <div
            className={`flex items-center rounded-full transition-colors ${volumeOpen ? 'bg-neutral-800/90 pl-3' : ''}`}
            onMouseEnter={() => setVolumeOpen(true)}
            onMouseLeave={() => setVolumeOpen(false)}
          >
            {volumeOpen && (
              <div
                role="slider"
                aria-label="音量"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={Math.round(shownVolume * 100)}
                className="relative w-20 h-4 flex items-center cursor-pointer"
                onPointerDown={(e) => {
                  e.currentTarget.setPointerCapture(e.pointerId);
                  setVolumeFromPointer(e);
                }}
                onPointerMove={(e) => {
                  if (e.currentTarget.hasPointerCapture(e.pointerId)) setVolumeFromPointer(e);
                }}
                onPointerUp={(e) => e.currentTarget.releasePointerCapture(e.pointerId)}
              >
                <div className="relative w-full h-1 rounded-full bg-white/30 overflow-hidden">
                  <div className="absolute inset-y-0 left-0 bg-white" style={{ width: `${shownVolume * 100}%` }} />
                </div>
                <div
                  className="absolute w-3 h-3 -ml-1.5 rounded-full bg-white pointer-events-none"
                  style={{ left: `${shownVolume * 100}%` }}
                />
              </div>
            )}
            <button type="button" tabIndex={-1} className={iconBtn} title={muted ? '取消静音' : '静音'} aria-label={muted ? '取消静音' : '静音'} onClick={toggleMute}>
              {shownVolume === 0 ? <VolumeX size={18} /> : <Volume2 size={18} />}
            </button>
          </div>
          <button type="button" tabIndex={-1} className={iconBtn} title={fullscreen ? '退出全屏' : '全屏'} aria-label={fullscreen ? '退出全屏' : '全屏'} onClick={toggleFullscreen}>
            {fullscreen ? <Minimize size={17} /> : <Maximize size={17} />}
          </button>
          <div className="relative">
            <button type="button" tabIndex={-1} className={iconBtn} title="更多" aria-label="更多" aria-expanded={menuOpen} onClick={() => setMenuOpen((o) => !o)}>
              <MoreVertical size={18} />
            </button>
            {menuOpen && (
              <div
                role="menu"
                className="absolute right-0 bottom-full mb-1 z-10 min-w-[120px] py-1 rounded-lg bg-neutral-900/95 border border-white/10 shadow-xl text-[12px] text-white"
                onMouseLeave={() => setMenuOpen(false)}
              >
                <div className="px-3 py-1 text-white/50">播放速度</div>
                {SPEEDS.map((s) => (
                  <button
                    key={s}
                    type="button"
                    role="menuitemradio"
                    aria-checked={rate === s}
                    tabIndex={-1}
                    className={`w-full text-left px-3 py-1 bg-transparent border-none cursor-pointer hover:bg-white/10 ${rate === s ? 'text-blue-400' : 'text-white'}`}
                    onClick={() => {
                      const el = v();
                      if (el) el.playbackRate = s;
                      setMenuOpen(false);
                    }}
                  >
                    {s === 1 ? '正常' : `${s}x`}
                  </button>
                ))}
                {canPip && (
                  <button
                    type="button"
                    role="menuitem"
                    tabIndex={-1}
                    className="w-full text-left px-3 py-1 mt-1 border-t border-white/10 bg-transparent border-x-0 border-b-0 text-white cursor-pointer hover:bg-white/10"
                    onClick={() => {
                      enterPip();
                      setMenuOpen(false);
                    }}
                  >
                    画中画
                  </button>
                )}
              </div>
            )}
          </div>
        </div>

        {/* 进度条：外层加高命中区，内层 4px 细线。 */}
        <div
          role="slider"
          aria-label="播放进度"
          aria-valuemin={0}
          aria-valuemax={Math.round(duration)}
          aria-valuenow={Math.round(current)}
          className="group/seek relative h-3 mx-1 flex items-center cursor-pointer"
          onPointerDown={(e) => {
            seeking.current = true;
            e.currentTarget.setPointerCapture(e.pointerId);
            seekFromPointer(e);
          }}
          onPointerMove={(e) => {
            if (seeking.current) seekFromPointer(e);
          }}
          onPointerUp={(e) => {
            seeking.current = false;
            e.currentTarget.releasePointerCapture(e.pointerId);
          }}
        >
          <div className="relative w-full h-1 rounded-full bg-white/30 overflow-hidden">
            <div className="absolute inset-y-0 left-0 bg-white/50" style={{ width: `${bufPct}%` }} />
            <div className="absolute inset-y-0 left-0 bg-white" style={{ width: `${pct}%` }} />
          </div>
          <div
            className="absolute w-3 h-3 -ml-1.5 rounded-full bg-white opacity-0 group-hover/seek:opacity-100 transition-opacity pointer-events-none"
            style={{ left: `${pct}%` }}
          />
        </div>
      </div>
    </div>
  );
});
