// 音频节点选中态的浮动操作工具条，是 VideoActionsBar 的音频版复制适配。
// 对标参考产品的音频工具条，砍掉「智能切分」「自定义切分」（用户拍板
// 2026-08-18），保留：截取 / 变速 / 下载。
//
// 截取/变速都是纯前端零积分（对标 liblib 实测，见 audioedit/audioEdit.ts
// 头注）：选区/倍率在模式条上定，提交后 转码/time-stretch → 上传 wav →
// 落 preview 音频产物节点并连回源。模式条顶掉工具条位置（同宫格切分先例）
// 而不是对标产品的节点下方——gen-audio 宿主的下方已被提示词面板占用。
// 截取的选区把手画在 WaveformPlayer 的波形上，会话状态在 store.audioEdit。
//
// 定位方案与 ImageActionsBar 完全一致（那边注释有逐行解释）：借 NodeShell
// bodyOverflow=visible 溢出到节点上方 + scale(var(--inv-zoom)) 抵消画布缩放
// → 屏幕尺寸恒定、跟随节点；calc(100% + 28px) 世界单位让开名字 chip，
// translate 里 -10px 叠屏幕像素恒定的间隙。
import { useState } from 'react';
import { ArrowUp, ChevronDown, ChevronUp, Download, Gauge, Loader2, Scissors, X } from 'lucide-react';
import { App as AntApp } from 'antd';
import { extFromUrl, saveMediaToDisk, saveWithFeedback } from '@/shared/saveToDisk';
import { useCanvasStore } from '../store';
import { isTrimSupported, MIN_TRIM_DURATION } from './trim/videoTrim';
import { MAX_SPEED_RATE, MIN_SPEED_RATE } from './audioedit/audioEdit';

const iconSize = 14;

function BarButton({
  label,
  title,
  onAction,
  children,
}: {
  label?: string;
  title?: string;
  onAction?: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title ?? label}
      // 同 ImageActionsBar：preventDefault 防抢焦点，stopPropagation 防冒泡到
      // 画布开始拖节点/框选。
      onMouseDown={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
      onClick={(e) => {
        e.stopPropagation();
        onAction?.();
      }}
      className={`h-8 rounded-lg flex items-center gap-1.5 border-none cursor-pointer text-[13px] whitespace-nowrap transition-colors ${
        label ? 'px-2' : 'w-8 justify-center'
      } bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60`}
    >
      {children}
      {label && <span>{label}</span>}
    </button>
  );
}

function Divider() {
  return <div className="w-px h-4 bg-border mx-1 shrink-0" />;
}

// 分:秒都补零的时间格式（00:45）。模式条对标 liblib 的显示形态；播放器里的
// 0:45 形态是另一处（WaveformPlayer.formatClock），别混用。
function formatClockPadded(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '00:00';
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

// 截取/变速模式条的悬浮容器：**节点下方**（对标 liblib 的位置；gen-audio 宿主
// 下方的提示词面板在编辑会话期间由 GenAudioNode 自己让位隐藏）。反缩放锚点
// 同文件头注释那套。
function FloatBelowNode({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="absolute z-40"
      style={{
        top: 'calc(100% + 16px)',
        left: '50%',
        transform: 'scale(var(--inv-zoom, 1)) translate(-50%, 0)',
        transformOrigin: '0 0',
        // 绝对定位盒的 shrink-to-fit 受限于包含块宽度:left:50% 后可用宽度只剩
        // 节点宽度的一半,窄节点上内容被挤到把「截取/生成」压成竖排换行。
        // max-content 让条宽只由内容决定,不受节点宽度约束。
        width: 'max-content',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <div className="flex items-center gap-2.5 px-3.5 py-2.5 rounded-2xl bg-card border border-border shadow-xl">
        {children}
      </div>
    </div>
  );
}

// 工具条的悬浮容器（节点上方，反缩放锚点见文件头注释）。
function FloatAboveNode({ children }: { children: React.ReactNode }) {
  return (
    <div
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
        // 工具条上的右键不该弹出节点菜单（NodeShell 在 onContextMenu 里接管）。
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <div className="flex items-center gap-0.5 px-1.5 py-1 rounded-xl bg-card border border-border shadow-xl">
        {children}
      </div>
    </div>
  );
}

export function AudioActionsBar({
  nodeId,
  visible,
  url,
}: {
  nodeId: string;
  visible: boolean;
  // 节点当前音频 URL。三个宿主的 visible 都以「有音频」为前提，正常非空
  // （GenAudioNode 含历史档位、PreviewNode 含上游 fallback，同 VideoActionsBar）。
  url?: string;
}) {
  const { message } = AntApp.useApp();
  // 只在「单选中本节点」时显示，理由同 ImageActionsBar：多选（框选拖动）时
  // 一次冒出 N 条工具条只会互相叠罗汉。
  const show = useCanvasStore(
    (s) => s.selectedIds.has(nodeId) && s.selectedIds.size === 1 && !s.readOnly,
  );
  const audioEdit = useCanvasStore((s) =>
    s.audioEdit?.nodeId === nodeId ? s.audioEdit : null,
  );
  const setAudioEdit = useCanvasStore((s) => s.setAudioEdit);
  // 提交中防重 + 按钮 spinner。用 state 而不是 ref：模式条要按它重渲染。
  const [busy, setBusy] = useState(false);

  if (!show || !visible) return null;

  // 下载：统一走 saveMediaToDisk（Tauri 里弹系统保存对话框、Rust 直接复制本地媒体）。
  const handleDownload = () => {
    if (!url) {
      message.warning('当前节点没有可下载的音频');
      return;
    }
    void saveWithFeedback(() => saveMediaToDisk(url, `audio-${Date.now()}.${extFromUrl(url, 'mp3')}`), message);
  };

  const enterEdit = (mode: 'trim' | 'speed') => {
    if (!url) return;
    // 截取走 mediabunny(WebCodecs 解码)，与视频剪辑同一条依赖、同一个探测。
    if (mode === 'trim' && !isTrimSupported()) {
      message.error('当前浏览器不支持音频截取,请使用新版 Chrome/Edge');
      return;
    }
    // duration 由 WaveformPlayer 回填（只有它知道音频时长），回填前提交禁用。
    setAudioEdit({ nodeId, mode, start: 0, end: 0, rate: 1, duration: null });
  };

  // 提交管道共用的收尾：进度 toast（key 覆盖更新）→ 成功/失败提示。整条链路
  // 闭包自持，模式条随选中态隐藏也不打断处理。
  const runPipeline = async (
    kind: '截取' | '变速',
    produce: (onProgress: (p: number) => void) => Promise<void>,
  ) => {
    if (busy) return;
    setBusy(true);
    const key = `audioedit-${nodeId}`;
    message.open({ key, type: 'loading', content: `${kind}处理中… 0%`, duration: 0 });
    try {
      await produce((p) =>
        message.open({
          key,
          type: 'loading',
          content: `${kind}处理中… ${Math.round(p * 100)}%`,
          duration: 0,
        }),
      );
      message.open({ key, type: 'success', content: `${kind}音频生成成功`, duration: 2 });
      setAudioEdit(null);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : `${kind}失败`;
      message.open({ key, type: 'error', content: msg, duration: 3 });
    } finally {
      setBusy(false);
    }
  };

  const handleTrimGenerate = () => {
    const cur = useCanvasStore.getState().audioEdit;
    if (!url || !cur || cur.duration == null || cur.end - cur.start < MIN_TRIM_DURATION) return;
    void runPipeline('截取', async (onProgress) => {
      const [{ trimAudio }, { deliverAudioEditProduct }] = await Promise.all([
        import('./audioedit/audioEdit'),
        import('./audioedit/executeAudioEdit'),
      ]);
      // 转码占 0~0.85，上传+落节点收尾。
      const blob = await trimAudio(url, cur.start, cur.end, (p) => onProgress(p * 0.85));
      onProgress(0.9);
      await deliverAudioEditProduct(nodeId, blob, 'audio-trim');
    });
  };

  const handleSpeedGenerate = () => {
    const cur = useCanvasStore.getState().audioEdit;
    if (!url || !cur || cur.rate <= 0) return;
    void runPipeline('变速', async (onProgress) => {
      const [{ stretchAudio }, { deliverAudioEditProduct }] = await Promise.all([
        import('./audioedit/audioEdit'),
        import('./audioedit/executeAudioEdit'),
      ]);
      const blob = await stretchAudio(url, cur.rate, (p) => onProgress(p * 0.9));
      await deliverAudioEditProduct(nodeId, blob, `audio-speed-${cur.rate}x`);
    });
  };

  // ── 截取模式条（对标 liblib）：✕ 截取 ｜ [00:00 - 00:45] ｜ [生成] ──
  if (audioEdit?.mode === 'trim') {
    const ready = audioEdit.duration != null && audioEdit.end - audioEdit.start >= MIN_TRIM_DURATION;
    return (
      <FloatBelowNode>
        <button
          type="button"
          title="取消"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            setAudioEdit(null);
          }}
          className="w-9 h-9 rounded-lg border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60 flex items-center justify-center"
          aria-label="取消"
        >
          <X size={16} />
        </button>
        <span className="text-[14px] pr-1 whitespace-nowrap shrink-0">截取</span>
        <span className="px-4 py-2 rounded-xl bg-popover text-[13px] font-mono whitespace-nowrap tracking-wide">
          {formatClockPadded(audioEdit.start)} - {formatClockPadded(audioEdit.end)}
        </span>
        <button
          type="button"
          disabled={!ready || busy}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            handleTrimGenerate();
          }}
          className="h-10 px-5 rounded-xl border-none cursor-pointer text-[14px] font-medium bg-white text-black hover:bg-white/85 disabled:opacity-40 disabled:cursor-not-allowed flex items-center gap-1.5 whitespace-nowrap shrink-0"
        >
          {busy ? <Loader2 size={14} className="animate-spin" /> : '生成'}
        </button>
      </FloatBelowNode>
    );
  }

  // ── 变速模式条（对标 liblib）：✕ 变速 ｜ 0.1x —滑杆— 4.0x ｜ [1.00x ▲▼] ｜ [↑] ──
  if (audioEdit?.mode === 'speed') {
    const setRate = (raw: number) => {
      if (!Number.isFinite(raw)) return;
      const rate = Math.min(MAX_SPEED_RATE, Math.max(MIN_SPEED_RATE, raw));
      const cur = useCanvasStore.getState().audioEdit;
      if (cur) setAudioEdit({ ...cur, rate: Math.round(rate * 100) / 100 });
    };
    return (
      <FloatBelowNode>
        <button
          type="button"
          title="取消"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            setAudioEdit(null);
          }}
          className="w-9 h-9 rounded-lg border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60 flex items-center justify-center"
          aria-label="取消"
        >
          <X size={16} />
        </button>
        <span className="text-[14px] pr-1 whitespace-nowrap shrink-0">变速</span>
        <span className="text-[12px] text-muted-foreground">{MIN_SPEED_RATE}x</span>
        <input
          type="range"
          min={MIN_SPEED_RATE}
          max={MAX_SPEED_RATE}
          step={0.05}
          value={audioEdit.rate}
          onMouseDown={(e) => e.stopPropagation()}
          onPointerDown={(e) => e.stopPropagation()}
          onChange={(e) => setRate(Number(e.target.value))}
          className="w-44 accent-blue-500 cursor-pointer"
        />
        <span className="text-[12px] text-muted-foreground">{MAX_SPEED_RATE}.0x</span>
        <div className="flex items-center gap-1 pl-3 pr-1.5 py-1.5 rounded-xl bg-popover">
          {/* 文本框显示 1.00 定宽两位小数（number 输入框会把 1.00 缩成 1，
              和后面的 x 拉开一截空档）。解析失败的中间态 setRate 直接忽略。 */}
          <input
            type="text"
            inputMode="decimal"
            value={audioEdit.rate.toFixed(2)}
            onMouseDown={(e) => e.stopPropagation()}
            onChange={(e) => setRate(Number(e.target.value))}
            className="w-10 bg-transparent border-none outline-none text-[14px] font-mono text-foreground text-right"
          />
          <span className="text-[12px] text-muted-foreground">x</span>
          {/* 自定义 ▲▼ 步进（原生 spinner 已隐藏，样式对不上 liblib）。 */}
          <div className="flex flex-col">
            <button
              type="button"
              aria-label="加速"
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                setRate((useCanvasStore.getState().audioEdit?.rate ?? 1) + 0.1);
              }}
              className="border-none bg-transparent cursor-pointer p-0 h-3.5 flex items-center text-foreground/60 hover:text-foreground"
            >
              <ChevronUp size={12} />
            </button>
            <button
              type="button"
              aria-label="减速"
              onMouseDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                setRate((useCanvasStore.getState().audioEdit?.rate ?? 1) - 0.1);
              }}
              className="border-none bg-transparent cursor-pointer p-0 h-3.5 flex items-center text-foreground/60 hover:text-foreground"
            >
              <ChevronDown size={12} />
            </button>
          </div>
        </div>
        <button
          type="button"
          disabled={busy}
          aria-label="生成变速音频"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            handleSpeedGenerate();
          }}
          className="w-10 h-10 rounded-xl border-none cursor-pointer bg-white text-black hover:bg-white/85 disabled:opacity-40 disabled:cursor-not-allowed flex items-center justify-center"
        >
          {busy ? <Loader2 size={14} className="animate-spin" /> : <ArrowUp size={16} />}
        </button>
      </FloatBelowNode>
    );
  }

  return (
    <FloatAboveNode>
      <BarButton label="截取" title="截取音频片段" onAction={() => enterEdit('trim')}>
        <Scissors size={iconSize} />
      </BarButton>
      <BarButton label="变速" title="调整播放速度（保音调）" onAction={() => enterEdit('speed')}>
        <Gauge size={iconSize} />
      </BarButton>
      <Divider />
      <BarButton title="下载" onAction={handleDownload}>
        <Download size={iconSize} />
      </BarButton>
    </FloatAboveNode>
  );
}
