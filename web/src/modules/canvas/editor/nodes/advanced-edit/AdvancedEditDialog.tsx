// 高级编辑弹框：视频播放器叠加矢量标注画布。
// 交互模型：
//   - 默认无工具选中——标注层不拦截指针，视频完全可交互（播放/暂停/拖进度条）；
//   - 选中工具进入标注态，画的内容先进「待确认」缓冲，工具栏出现「取消/添加至输入框」；
//   - 「添加至输入框」才把这批标注落成该时间点的记录（缩略图+说明输入），「取消」整批丢弃；
//   - 确认/取消后自动退出工具选中态，回到纯播放器。
// 不 import store——画布副作用（上传/建节点/连线）全部留给调用方通过 onSave 注入，
// 弹框自身只管交互与产出 { entries, captureFrame }，保持可脱离画布独立测试。
import { useEffect, useRef, useState } from 'react';
import {
  ArrowUpRight, Check, Eraser, MapPin, Pencil, Redo2, Square, Type, Undo2, X,
} from 'lucide-react';
import { Tooltip } from 'antd';
import {
  ANNO_COLORS, DEFAULT_ANNO_COLOR,
  composeFrame, eraseAt, formatTime, newFrame, pushOp, redo as redoFrame, renderOps, undo as undoFrame,
  type AnnoOp, type AnnoTool, type FrameAnnotation,
} from './engine';
import type { SaveResult } from './save';

interface Props {
  videoUrl: string;
  // 返回 SaveResult（而不是原来的「全对即 resolve、半单也 throw」）——部分失败时
  // 弹框要知道成功了几条，才能只重试失败的那条，不然用户点「保存」重试整单会把
  // 已成功的条目重复上传/建节点/追加提示词一遍。
  onSave: (entries: FrameAnnotation[], captureFrame: (e: FrameAnnotation) => Promise<Blob>) => Promise<SaveResult>;
  onClose: () => void;
}

// 擦除命中的**屏幕像素**手感：鼠标离笔画 14 屏幕px 内即命中。之前用原始像素
// 定阈值（12px），720p 视频缩显到 200px 宽时只剩 3 个屏幕像素——要像素级精准
// 才擦得掉，体验极差（用户实测点名）。换算比与标注样式同一机制（落笔烘焙 px）。
const ERASE_DISPLAY_PX = 14;
// 记录条缩略图尺寸——只做本地展示用，不参与保存内容。
// 320 而不是 160：这一张 dataURL 身兼两职——记录条 64px 小图 + hover 预览大图
// （用户点名要能悬停看大图审查），160 放大后糊到没法核对标注细节。
const THUMB_W = 320;

const TOOLS: Array<{ id: AnnoTool; label: string; icon: typeof Square }> = [
  { id: 'rect', label: '矩形', icon: Square },
  { id: 'pen', label: '画笔', icon: Pencil },
  { id: 'arrow', label: '箭头', icon: ArrowUpRight },
  { id: 'text', label: '文字', icon: Type },
  { id: 'eraser', label: '橡皮', icon: Eraser },
  { id: 'pin', label: '定位点', icon: MapPin },
];

export function AdvancedEditDialog({ videoUrl, onSave, onClose }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const overlayRef = useRef<HTMLCanvasElement>(null);

  const [entries, setEntries] = useState<FrameAnnotation[]>([]);
  // null = 未选工具 = 纯播放器态。这是「选了工具才能画」门控的唯一开关：
  // overlay 的 pointerEvents 跟着它走，不选工具时指针直接穿到 video 上。
  const [tool, setTool] = useState<AnnoTool | null>(null);
  const [color, setColor] = useState<string>(DEFAULT_ANNO_COLOR);
  // 待确认缓冲：本次标注会话画的所有 op，锚定在落第一笔时的播放时间。
  // 「添加至输入框」才并入 entries，「取消」整批丢弃——落笔即成记录的旧交互
  // 让误画直接变成脏记录，用户没有反悔的机会。
  const [pending, setPending] = useState<FrameAnnotation | null>(null);
  // 内嵌文字输入：文字工具点击处出现输入框（video 原始像素坐标），回车落定。
  const [textDraft, setTextDraft] = useState<{ x: number; y: number; value: string } | null>(null);
  const [corsError, setCorsError] = useState<string | null>(null);
  // 视频加载失败与 CORS 探测失败分开报：crossOrigin=anonymous 下缺 CORS 头浏览器
  // 会拒绝加载整个视频（连播放都不行），此时 loadeddata 不触发、探测永远不跑，
  // 不接 error 事件界面就是个无提示的死播放器。
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [draft, setDraft] = useState<AnnoOp | null>(null);
  // 拖动中标记 + 起点，rect/arrow/pen 共用；用 ref 存起点避免每帧重渲染。
  const drawingRef = useRef(false);
  // 橡皮按住拖动连续擦的进行中标记——逐次点击才能擦的手感太差（用户实测点名）。
  const erasingRef = useRef(false);
  // 加载失败自动重试一次：偶发失败多为浏览器缓存里存了「无 ACAO 头的响应」
  // （其他不带 crossOrigin 的播放器先播过同一 URL），重新 load 会触发重新验证、
  // 换来带头的响应——等价于用户手动关闭重开。播放点已统一 crossOrigin 根治，
  // 这里是历史缓存/网络抖动的兜底。
  const retriedRef = useRef(false);
  const startRef = useRef<{ x: number; y: number } | null>(null);
  const draftIdRef = useRef(0);
  // 播放头心跳：渲染期从 video.currentTime 派生的状态（当前时间点的已有记录等）
  // 在原生 seek/播放时不会触发 React 重渲染——用一次 tick 状态强制对齐。
  const [playheadTick, setPlayheadTick] = useState(0);
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const onTick = () => setPlayheadTick((n) => n + 1);
    video.addEventListener('seeked', onTick);
    video.addEventListener('timeupdate', onTick);
    return () => {
      video.removeEventListener('seeked', onTick);
      video.removeEventListener('timeupdate', onTick);
    };
  }, []);
  void playheadTick; // 只借状态变化触发重渲染，值本身不参与计算

  const disabled = corsError != null || loadError != null;
  const annotating = tool != null && !disabled;

  // CORS 探测：能读像素才能抓帧保存，探测失败就整体锁死工具与保存，
  // 避免用户画了一堆图最后保存时才发现拿不到帧数据。
  const probeCors = () => {
    const video = videoRef.current;
    if (!video) return;
    try {
      composeFrame(video, [], 2, 2).toDataURL();
    } catch (err) {
      if (err instanceof DOMException && err.name === 'SecurityError') {
        setCorsError('视频源未开启 CORS，无法抓帧，请检查视频源的 CORS 配置');
      }
    }
  };

  // 坐标映射（MaskEditor 手法复制）：指针相对显示盒的 offset，乘 natural/display
  // 比例换算回视频原始像素空间——overlay canvas 的绘制上下文本身就是原始空间，
  // renderOps 直接用，不需要显示层再缩放一次。
  //
  // 缩放比和原点必须用 video 自己的 getBoundingClientRect()（overlay 现已全高
  // 贴合 video、两者矩形相同，但坐标语义的真源始终是视频显示盒——历史上 overlay
  // 曾比 video 矮 40px，用它的 rect 算 scaleY 导致标注越靠底部纵向漂移越大，
  // 踩过两次，别再换回去）。composeFrame 合成的是完整原始帧。
  const toVideoCoords = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const video = videoRef.current;
    if (!video) return null;
    const rect = video.getBoundingClientRect();
    const scaleX = video.videoWidth / rect.width;
    const scaleY = video.videoHeight / rect.height;
    return { x: (e.clientX - rect.left) * scaleX, y: (e.clientY - rect.top) * scaleY };
  };

  // 视频原始像素坐标 → 相对包裹容器的显示坐标（内嵌文字输入框定位用）。
  const toDisplayCoords = (x: number, y: number) => {
    const video = videoRef.current;
    if (!video || !video.videoWidth) return { left: 0, top: 0 };
    const rect = video.getBoundingClientRect();
    return { left: (x / video.videoWidth) * rect.width, top: (y / video.videoHeight) * rect.height };
  };

  // 确保待确认缓冲存在——锚定在落第一笔时的播放时间，本次会话后续笔画都归它。
  const ensurePending = (): FrameAnnotation => {
    if (pending) return pending;
    const t = videoRef.current?.currentTime ?? 0;
    const p = newFrame(t);
    setPending(p);
    return p;
  };

  const pushPendingOp = (op: AnnoOp) => {
    setPending((p) => pushOp(p ?? newFrame(videoRef.current?.currentTime ?? 0), op));
  };

  const newOpId = () => `op-${Date.now()}-${draftIdRef.current++}`;

  // 落笔那一刻的「原始像素/屏幕像素」换算比——统一样式的锚点是屏幕像素
  // （用户拍板：视频分辨率千差万别，按帧宽算样式没法统一观感），烘焙进 op
  // 让渲染/合成/保存的比例与屏幕所见完全一致。
  const displayScale = (): number => {
    const video = videoRef.current;
    if (!video || !video.videoWidth) return 1;
    const rect = video.getBoundingClientRect();
    return rect.width > 0 ? video.videoWidth / rect.width : 1;
  };

  const handlePointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!annotating || !tool) return;
    const video = videoRef.current;
    if (!video) return;
    video.pause(); // 落笔自动暂停——播放中画不成立
    const pos = toVideoCoords(e);
    if (!pos) return;
    const px = displayScale();

    if (tool === 'text') {
      // 内嵌输入框（替代 window.prompt）：点击处出现输入，回车落定、Esc 取消。
      // preventDefault 是它能活下来的关键：pointerdown 的默认动作会把焦点移给
      // 点击目标/body，而输入框是本次事件里刚挂载并 autoFocus 的——不阻断的话
      // 浏览器随后的默认聚焦立刻把它 blur 掉，onBlur 空值即取消，输入框闪现即
      // 消失，表现为「文字工具输不了字」（实机踩中）。
      e.preventDefault();
      ensurePending();
      setTextDraft({ x: pos.x, y: pos.y, value: '' });
      return;
    }
    if (tool === 'pin') {
      pushPendingOp({ id: newOpId(), tool: 'pin', points: [pos], color, px });
      return;
    }
    if (tool === 'eraser') {
      // 只擦待确认缓冲：已确认进记录的标注不再可改（与「不做回读」语义一致），
      // 要改就删记录重标。按住拖动可连续擦（erasingRef），不必逐次点击。
      erasingRef.current = true;
      setPending((p) => (p ? eraseAt(p, pos.x, pos.y, ERASE_DISPLAY_PX * px) : p));
      return;
    }

    // rect/arrow/pen：进入拖动态，抬起时才落定成 op。
    drawingRef.current = true;
    startRef.current = pos;
    const id = newOpId();
    setDraft(tool === 'pen'
      ? { id, tool: 'pen', points: [pos], color, px }
      : { id, tool: tool as 'rect' | 'arrow', points: [pos, pos], color, px });
  };

  const handlePointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!annotating) return;
    if (erasingRef.current) {
      const pos = toVideoCoords(e);
      if (pos) setPending((p) => (p ? eraseAt(p, pos.x, pos.y, ERASE_DISPLAY_PX * displayScale()) : p));
      return;
    }
    if (!drawingRef.current) return;
    const pos = toVideoCoords(e);
    if (!pos) return;
    setDraft((d) => {
      if (!d) return d;
      if (d.tool === 'pen') return { ...d, points: [...d.points, pos] };
      return { ...d, points: [d.points[0], pos] };
    });
  };

  const handlePointerUp = () => {
    erasingRef.current = false;
    if (!annotating || !drawingRef.current) return;
    drawingRef.current = false;
    startRef.current = null;
    // 副作用不放 setDraft 的 updater 里（StrictMode 双调风险）——先读当前值再清。
    const d = draft;
    setDraft(null);
    if (d) pushPendingOp(d);
  };

  const commitText = () => {
    if (textDraft && textDraft.value.trim()) {
      pushPendingOp({
        id: newOpId(), tool: 'text', points: [{ x: textDraft.x, y: textDraft.y }],
        text: textDraft.value.trim(), color, px: displayScale(),
      });
    }
    setTextDraft(null);
  };

  // 「添加至输入框」：每次都新增一条**独立**记录——
  // 同一秒的多次确认各自成条（独立缩略图/说明/图片节点/提示词行），不再按
  // timeSec 合并进已有记录。缩略图用当前视频帧合成——正常流程里确认发生在
  // 标注的同一暂停帧上；若用户中途拖了进度条，缩略图会是新帧+旧标注，仅影响
  // 本地展示，保存时 captureFrame 会 seek 回记录时间重新合成，产物不受影响。
  const commitPending = () => {
    const video = videoRef.current;
    if (!pending || pending.ops.length === 0) { setPending(null); setTool(null); return; }
    let committed: FrameAnnotation = { ...pending, redoStack: [] };
    if (video && video.videoWidth) {
      const h = Math.round((THUMB_W * video.videoHeight) / video.videoWidth);
      try {
        // 传入源尺寸让 composeFrame 缩放绘制标注——ops 坐标在原始像素空间，
        // 不缩放会全部落在 320px 小画布之外，缩略图看起来「没有涂鸦」。
        committed = { ...committed, thumbDataUrl: composeFrame(video, committed.ops, THUMB_W, h, video.videoWidth, video.videoHeight).toDataURL('image/png') };
      } catch {
        // CORS 已在探测阶段拦截，这里理论上不会再失败；失败就无缩略图展示
      }
    }
    // sort 是稳定排序：同秒多条保持确认顺序。
    setEntries((prev) => [...prev, committed].sort((a, b) => a.timeSec - b.timeSec));
    setPending(null);
    setTextDraft(null);
    setTool(null); // 确认即退出标注态——回到纯播放器，用户 seek 下一个时间点
  };

  const cancelPending = () => {
    setPending(null);
    setDraft(null);
    setTextDraft(null);
    setTool(null);
  };

  // 每次状态变化重绘 overlay：已确认记录（当前时间点）打底 + 待确认缓冲 + 拖动预览。
  useEffect(() => {
    const canvas = overlayRef.current;
    const video = videoRef.current;
    if (!canvas || !video || !video.videoWidth) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    // 只画待确认缓冲 + 拖动预览——已确认进记录的标注不再常驻画面（用户点名：
    // 点「添加至输入框」后画面要清空，旧涂鸦留着会挡住继续标注；它们的样子
    // 保存在记录条缩略图里，hover 可看大图）。
    const ops = [...(pending ? pending.ops : [])];
    if (draft) ops.push(draft);
    renderOps(ctx, ops); // 几何逻辑不在这里重写，直接用 engine 的纯函数
  }, [pending, draft]);

  const handleLoadedData = () => {
    const video = videoRef.current;
    const canvas = overlayRef.current;
    if (video && canvas) {
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
    }
    setLoadError(null);
    probeCors();
  };

  const seek = (t: number) => {
    const video = videoRef.current;
    if (!video) return;
    video.pause();
    video.currentTime = t;
  };

  const captureFrame = (target: FrameAnnotation): Promise<Blob> => new Promise((resolve, reject) => {
    const t = target.timeSec;
    const video = videoRef.current;
    if (!video) { reject(new Error('视频未就绪')); return; }
    const doCapture = () => {
      // 合成/toBlob 在跨域画布被污染等情况下可能同步抛错（SecurityError 等）——
      // 不接住的话这个异常会在 seeked 回调里被吞掉，Promise 永远不 resolve/reject，
      // 弹框卡死在「保存中…」且无法恢复。no-op 快路径和 seeked 路径共用这一个
      // 函数，两边因此天然一起获得这层保护。
      try {
        // 直接用传入记录的 ops——同秒可有多条独立记录，按时间查找会让它们
        // 全部合成出第一条的涂鸦。
        const c = composeFrame(video, target.ops, video.videoWidth, video.videoHeight);
        c.toBlob((b) => {
          if (b) resolve(b); else reject(new Error('抓帧失败'));
        }, 'image/png');
      } catch (err) {
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    };
    // 目标时间已经是当前播放位置时，`currentTime = t` 是浏览器规范定义的
    // no-op——不触发 seeked，死等事件会让 Promise 永不 settle，弹框卡死在
    // 「保存中…」。落笔不播放直接标注第一帧（currentTime 停在 0）最容易踩中。
    if (Math.abs(video.currentTime - t) < 0.001) {
      doCapture();
      return;
    }
    let settled = false;
    // 兜底超时：万一某些浏览器/编码在别的边界下也不触发 seeked，不能让弹框
    // 永久挂起——超时给用户一个可见错误，而不是无声卡死。
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      video.removeEventListener('seeked', onSeeked);
      reject(new Error('视频定位超时'));
    }, 3000);
    const onSeeked = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      video.removeEventListener('seeked', onSeeked);
      doCapture();
    };
    video.addEventListener('seeked', onSeeked);
    video.currentTime = t;
  });

  const handleSave = async () => {
    setSaveError(null);
    setSaving(true);
    try {
      const res = await onSave(entries, captureFrame);
      if (res.failedAt !== undefined) {
        // 部分失败：把已成功的前 ok 条从记录条里剔除，只留失败起点开始的条目——
        // 不这样做的话，用户点「保存」重试会把已经上传/建好节点/写进提示词的
        // 那部分再走一遍全流程，产出重复的图片节点、连线和提示词行。
        setEntries((prev) => prev.slice(res.ok));
        // 用 failedAt 而不是 ok 报第几条失败——两者通常相等（逐条顺序处理），
        // 但整单前置校验（如说明文字非空）失败时 ok 恒为 0、failedAt 却可能
        // 指向列表中间的某一条，报 ok+1 会指错行号。
        setSaveError(`前 ${res.ok} 条已保存，第 ${(res.failedAt ?? res.ok) + 1} 条失败：${res.error ?? '未知错误'}`);
      } else {
        onClose(); // 全部成功才关框
      }
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const banner = corsError ?? loadError;
  const hasPending = pending != null && pending.ops.length > 0;
  const saveBlockReason = disabled ? banner
    : hasPending ? '有未确认的标注，请先「添加至输入框」或取消'
      : entries.length === 0 ? '还没有标注记录' : null;

  return (
    <div
      className="fixed inset-0 z-[1000] flex items-center justify-center bg-black/60 backdrop-blur-sm"
      // 遮罩本身也要挡住 mousedown——只在内层面板挡的话，点到遮罩空白处（面板
      // 之外、弹框之内的区域）鼠标事件仍会往下穿透，画布把它当成拖拽起点，
      // 弹框开着的时候把底下的节点拖走。
      onMouseDown={(e) => e.stopPropagation()}
    >
      <div
        className="w-[880px] max-w-[92vw] max-h-[92vh] bg-card border border-input rounded-xl shadow-2xl flex flex-col overflow-hidden"
        onMouseDown={(e) => e.stopPropagation()}
        // 键盘事件在面板层截停：弹框虽 portal 到 body，React 事件仍沿组件树冒泡回
        // 画布，document 级快捷键（Del 删节点、Ctrl+Z 撤销画布）会吃掉输入框的按键
        // ——文字标注/说明输入打字时误触画布操作。
        onKeyDown={(e) => e.stopPropagation()}
        onKeyUp={(e) => e.stopPropagation()}
      >
        {/* 头部 */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border shrink-0">
          <div className="text-foreground text-sm font-semibold">高级编辑</div>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="p-1 rounded bg-transparent border-none cursor-pointer text-muted-foreground hover:text-foreground hover:bg-popover transition-colors"
          >
            <X size={16} />
          </button>
        </div>

        {banner && (
          <div className="mx-5 mt-3 px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-xs shrink-0">
            {banner}
          </div>
        )}

        {/* 视频区 + 叠加标注画布 */}
        <div className="px-5 pt-3 flex items-center justify-center bg-black/20 shrink-0">
          <div className="relative inline-block max-w-full max-h-[46vh]">
            <video
              ref={videoRef}
              src={videoUrl}
              crossOrigin="anonymous"
              controls
              className="block max-w-full max-h-[46vh]"
              onLoadedData={handleLoadedData}
              // crossOrigin=anonymous 下对象存储缺 CORS 头时浏览器直接拒载整个
              // 视频，loadeddata 不触发、CORS 探测永远不跑——必须接 error 事件
              // 给出可见提示，否则界面就是一个无解释的死播放器。
              onError={() => {
                const video = videoRef.current;
                if (!retriedRef.current && video) {
                  retriedRef.current = true;
                  window.setTimeout(() => video.load(), 300);
                  return;
                }
                setLoadError('视频加载失败——可能是浏览器缓存或链接过期，请关闭弹框重开；持续出现请检查素材来源或网络');
              }}
            />
            <canvas
              ref={overlayRef}
              // 尺寸必须给**显式**宽高（w-full h-full），不能用 inset 双边钉住
              // 期望拉伸：canvas 是替换元素，绝对定位下宽高为 auto 时直接用自己
              // 的内在尺寸（width/height 属性，即视频原始分辨率），left+right 并
              // 不会像普通 div 那样把它撑开。
              // 必须**全高贴合视频、1:1 零压缩**：曾为让出底部原生 controls 裁掉
              // 40px 高，但缓冲区仍是完整帧——完整帧压进矮盒子显示，纵向出现
              // (h-40)/h 的系数错位，顶部无感、越往下漂移越大（实机踩中）。
              // controls 的可点性交给工具门控解决：未选工具时画布对指针透明，
              // controls 随便点；标注态想操作视频，取消工具选中即可。
              className="absolute left-0 top-0 w-full h-full"
              // 工具门控：未选工具时对指针完全透明——视频可直接点击播放/暂停/
              // hover 出 controls；选了工具才进入标注态拦截指针。
              style={{
                cursor: annotating ? 'crosshair' : 'default',
                pointerEvents: annotating ? 'auto' : 'none',
              }}
              onPointerDown={handlePointerDown}
              onPointerMove={handlePointerMove}
              onPointerUp={handlePointerUp}
              onPointerLeave={handlePointerUp}
            />
            {textDraft && (
              <input
                autoFocus
                value={textDraft.value}
                onChange={(e) => setTextDraft((d) => (d ? { ...d, value: e.target.value } : d))}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitText();
                  if (e.key === 'Escape') setTextDraft(null);
                }}
                onBlur={commitText}
                placeholder="输入标注文字，回车确定"
                className="absolute z-10 bg-black/70 border border-blue-500 rounded px-2 py-1 text-xs text-white outline-none placeholder:text-white/40 w-44"
                style={toDisplayCoords(textDraft.x, textDraft.y)}
              />
            )}
          </div>
        </div>

        {/* 调色板：选中工具时出现（橡皮不需要颜色，但常显更稳定，避免行高跳动） */}
        {tool != null && (
          <div className="flex items-center gap-2 px-5 pt-2.5 shrink-0">
            {ANNO_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={`颜色 ${c}`}
                onClick={() => setColor(c)}
                className={`w-5 h-5 rounded-full cursor-pointer transition-transform ${
                  color === c ? 'ring-2 ring-blue-500 ring-offset-2 ring-offset-card scale-110' : 'ring-1 ring-border'
                }`}
                style={{ backgroundColor: c, border: 'none' }}
              />
            ))}
          </div>
        )}

        {/* 工具栏 */}
        <div className="flex items-center gap-1 px-5 py-2.5 border-b border-border shrink-0">
          {TOOLS.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              type="button"
              aria-label={label}
              title={label}
              disabled={disabled}
              // 再点一次当前工具 = 取消选中，回到纯播放器态。
              // 点工具即暂停播放：选工具意味着要对「当前帧」标注了，画面继续
              // 往前跑的话落笔就对不上用户看中的那一帧。
              onClick={() => {
                videoRef.current?.pause();
                setTool((cur) => (cur === id ? null : id));
              }}
              className={`p-1.5 rounded-md border-none cursor-pointer transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                tool === id ? 'bg-blue-600 text-white' : 'bg-transparent text-muted-foreground hover:text-foreground hover:bg-popover'
              }`}
            >
              <Icon size={15} />
            </button>
          ))}
          <span className="w-px h-5 bg-border/60 mx-1" aria-hidden />
          <button
            type="button"
            aria-label="撤销"
            title="撤销"
            disabled={disabled || !pending || pending.ops.length === 0}
            // 撤销/重做同样是在操作当前帧的标注，播放中点它也先停下来。
            onClick={() => {
              videoRef.current?.pause();
              setPending((p) => (p ? undoFrame(p) : p));
            }}
            className="p-1.5 rounded-md border-none bg-transparent cursor-pointer text-muted-foreground hover:text-foreground hover:bg-popover transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <Undo2 size={15} />
          </button>
          <button
            type="button"
            aria-label="重做"
            title="重做"
            disabled={disabled || !pending || pending.redoStack.length === 0}
            onClick={() => {
              videoRef.current?.pause();
              setPending((p) => (p ? redoFrame(p) : p));
            }}
            className="p-1.5 rounded-md border-none bg-transparent cursor-pointer text-muted-foreground hover:text-foreground hover:bg-popover transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >
            <Redo2 size={15} />
          </button>

          {/* 待确认操作区：有未确认标注时出现（「取消/添加至输入框」） */}
          {hasPending && (
            <div className="flex items-center gap-1 ml-3">
              <span className="text-[11px] text-muted-foreground mr-1">
                {formatTime(pending.timeSec)} 的标注
              </span>
              <button
                type="button"
                onClick={cancelPending}
                className="flex items-center gap-1 px-2.5 py-1.5 rounded-md border-none bg-transparent cursor-pointer text-xs text-muted-foreground hover:text-red-400 hover:bg-popover transition-colors"
              >
                <X size={13} /> 取消
              </button>
              <button
                type="button"
                onClick={commitPending}
                className="flex items-center gap-1 px-2.5 py-1.5 rounded-md border-none bg-blue-600 hover:bg-blue-500 cursor-pointer text-xs text-white transition-colors"
              >
                <Check size={13} /> 添加至输入框
              </button>
            </div>
          )}
        </div>

        {/* 记录条 */}
        <div className="flex-1 min-h-0 overflow-y-auto px-5 py-3 flex flex-col gap-2">
          {entries.length === 0 && (
            <div className="text-xs text-muted-foreground py-6 text-center">
              播放/拖动到目标画面 → 选择工具标注 → 点「添加至输入框」生成记录
            </div>
          )}
          {entries.map((entry) => (
            <div key={entry.id} className="flex items-center gap-2.5 p-1.5 rounded-lg border border-border/60">
              {/* hover 弹大图供审查（用户点名）——同一张 320px dataURL，小图只是缩显 */}
              <Tooltip
                title={entry.thumbDataUrl
                  ? <img src={entry.thumbDataUrl} alt="" style={{ width: 320, display: 'block', borderRadius: 6 }} />
                  : null}
                mouseEnterDelay={0}
                placement="right"
              >
                <button
                  type="button"
                  onClick={() => seek(entry.timeSec)}
                  className="shrink-0 w-16 h-10 rounded-md overflow-hidden border-none cursor-pointer bg-black/40 p-0"
                >
                  {entry.thumbDataUrl
                    ? <img src={entry.thumbDataUrl} alt="" className="w-full h-full object-cover" />
                    : <div className="w-full h-full" />}
                </button>
              </Tooltip>
              <span className="text-[11px] text-muted-foreground shrink-0 w-10">{formatTime(entry.timeSec)}</span>
              <input
                value={entry.note}
                onChange={(e) => {
                  const note = e.target.value;
                  setEntries((prev) => prev.map((it) => (it.id === entry.id ? { ...it, note } : it)));
                }}
                placeholder="说明这处标注（可选，进提示词）"
                className="flex-1 bg-card border border-input rounded-md px-2.5 py-1.5 text-xs text-foreground outline-none focus:border-blue-500 placeholder:text-muted-foreground"
              />
              <button
                type="button"
                aria-label="删除记录"
                title="删除"
                onClick={() => setEntries((prev) => prev.filter((it) => it.id !== entry.id))}
                className="shrink-0 p-1.5 rounded-md border-none bg-transparent cursor-pointer text-muted-foreground hover:text-red-400 hover:bg-popover transition-colors"
              >
                <X size={13} />
              </button>
            </div>
          ))}
        </div>

        {/* 底部保存 */}
        <div className="flex items-center justify-end gap-3 px-5 py-3.5 border-t border-border shrink-0">
          {saveError && <span className="text-xs text-red-400 mr-auto">{saveError}</span>}
          {/* 禁用原因即显（与 GenVideoNode 生成按钮同规范）：disabled 按钮不派发
              鼠标事件，Tooltip 须包 span 接管 hover。 */}
          <Tooltip title={saving ? null : saveBlockReason} mouseEnterDelay={0} placement="top">
            <span className="inline-flex">
              <button
                type="button"
                onClick={handleSave}
                disabled={saving || saveBlockReason != null}
                className="px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-xs font-medium border-none cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                {saving ? '保存中…' : '保存'}
              </button>
            </span>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}
