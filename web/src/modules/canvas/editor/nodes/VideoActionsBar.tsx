// 视频节点选中态的浮动操作工具条（对标参考产品），是 ImageActionsBar 的
// 视频版。已接功能：「高清」「白模」「智能去字幕」——原是右键菜单里的
// 「视频转高清」「擦除字幕」（2026-08-14 迁到这里，菜单里已删），点击在
// 源节点右侧新建对应后处理节点并连线；「下载」「放大查看」（2026-08-17）
// 照抄 ImageActionsBar 同款——隐藏 <a download> + 全局 Lightbox，纯本地零积分；
// 「裁剪」（2026-08-17）空间裁剪，进 crop 模式由 VideoCropOverlay 接管（设计
// docs/2026-08-17-video-crop-design.md）；「音频分离」（2026-08-17）点击直接
// 执行音视频分离——纯前端拆轨出 音轨wav+无声视频 两个节点，零积分（对标实测
// docs/2026-08-17-liblib-audio-separation-analysis.md；v1 只有这一项所以不用
// 下拉，「人声分离」要服务端跑分离模型，暂不做，接入时再改回下拉形态）。
// 其余按钮仍是占位、点击不触发功能。
// 2026-08-17 用户要求：未接功能的占位按钮先藏起来不给用户看（避免点了没反应），
// 由 SHOW_PLACEHOLDER_ACTIONS 统一控制，接入功能时翻回 true 即可，代码不删。
//
// 定位方案与 ImageActionsBar 完全一致（那边注释有逐行解释）：借 NodeShell
// bodyOverflow=visible 溢出到节点上方 + scale(var(--inv-zoom)) 抵消画布缩放
// → 屏幕尺寸恒定、跟随节点；calc(100% + 28px) 世界单位让开名字 chip，
// translate 里 -10px 叠屏幕像素恒定的间隙。
import {
  AlarmClockPlus,
  AudioLines,
  Box,
  ChevronDown,
  Download,
  Maximize2,
  Palette,
  RemoveFormatting,
  Scan,
  Scissors,
  SquarePen,
  SquarePlay,
} from 'lucide-react';
import { useRef } from 'react';
import { App as AntApp } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { listRuntimeModels } from '@/shared/api';
import { executeAudioSeparate } from './audiosep/executeAudioSeparate';
import { useCanvasStore } from '../store';
import { spawnPostprocessNode } from './useMediaNodeMenu';
import { VideoTrimBar } from './trim/VideoTrimBar';
import { isTrimSupported } from './trim/videoTrim';
import { VideoCropOverlay } from './crop/VideoCropOverlay';
import { SegmentReshootBar } from './reshoot/SegmentReshootBar';
import { RESHOOT_MODEL_ID } from './reshoot/segmentReshoot';
import { VideoContinuationBar } from './continuation/VideoContinuationBar';
import { CONTINUATION_MODEL_ID } from './continuation/videoContinuation';

const iconSize = 14;

// 占位按钮（逐帧拉片/画面编辑）的显示开关。片段重拍/智能续写/音频分离已于
// 2026-08-17 接入真功能,不再受此开关控制。
// 声明成宽类型 boolean，避免字面量 false 触发 lint 的常量条件告警。
const SHOW_PLACEHOLDER_ACTIONS: boolean = false;

function BarButton({
  label,
  title,
  chevron,
  onAction,
  children,
}: {
  label?: string;
  title?: string;
  chevron?: boolean;
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
      {chevron && <ChevronDown size={12} className="opacity-60" />}
    </button>
  );
}

function Divider() {
  return <div className="w-px h-4 bg-border mx-1 shrink-0" />;
}

// 「高清」的图标是方框里的 HD 字样，lucide 没有对应图标，手绘一个同尺寸的
// 文字徽标（描边方框 + 9px 粗体），视觉重量对齐其余 14px 线性图标。
function HdBadge() {
  return (
    <span className="w-[15px] h-[13px] rounded-[3px] border border-solid border-current flex items-center justify-center text-[8px] font-bold leading-none shrink-0">
      HD
    </span>
  );
}

export function VideoActionsBar({
  nodeId,
  visible,
  url,
}: {
  nodeId: string;
  visible: boolean;
  // 节点当前视频 URL。「剪辑」要用；三个宿主的 visible 都已经以「有视频」
  // 为前提，正常情况下非空。
  url?: string;
}) {
  const { message } = AntApp.useApp();
  // 只在「单选中本节点」时显示，理由同 ImageActionsBar：多选（框选拖动）时
  // 一次冒出 N 条工具条只会互相叠罗汉。
  const show = useCanvasStore(
    (s) => s.selectedIds.has(nodeId) && s.selectedIds.size === 1 && !s.readOnly,
  );
  // trim/crop/重拍模式下工具条让位给对应交互层（共用节点上下的同一片空间）。
  const trimming = useCanvasStore((s) => s.trimmingNodeId === nodeId);
  const cropping = useCanvasStore((s) => s.croppingNodeId === nodeId);
  const reshooting = useCanvasStore((s) => s.reshootNodeId === nodeId);
  const continuing = useCanvasStore((s) => s.continuationNodeId === nodeId);

  // 重拍/续写模型都写死(RESHOOT_MODEL_ID / CONTINUATION_MODEL_ID),唯一的
  // 前置条件是模型中心配了对应 id——与 GenVideoNode 同 queryKey 共享一份
  // 模型目录缓存,不随节点选中反复打请求。
  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 300_000,
  });

  // 「音频分离」处理中防重(busy 用 ref:处理是后台异步,不需要重渲染,
  // 只要挡住连点)。
  const audioSepBusy = useRef(false);

  if (!show || !visible) return null;
  if (trimming && url) return <VideoTrimBar nodeId={nodeId} url={url} />;
  if (cropping && url) return <VideoCropOverlay nodeId={nodeId} url={url} />;
  if (reshooting && url) return <SegmentReshootBar nodeId={nodeId} url={url} />;
  if (continuing && url) return <VideoContinuationBar nodeId={nodeId} url={url} />;

  // 下载：与 ImageActionsBar 同款隐藏 <a download> 触发。跨域源浏览器会忽略
  // download 属性退化成新标签打开，靠 target=_blank 兜底不至于把画布顶掉。
  // 取 URL 不用另写解析——三个宿主传进来的 url 已是各自解析好的当前视频
  // （GenVideoNode 含历史档位、PreviewNode 含上游 fallback）。
  const handleDownload = () => {
    if (!url) {
      message.warning('当前节点没有可下载的视频');
      return;
    }
    const a = document.createElement('a');
    a.href = url;
    a.download = 'video.mp4';
    a.target = '_blank';
    a.rel = 'noreferrer';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  };

  // 音视频分离：无模式 UI，点了直接后台跑（拆轨→双上传→落 音频+无声视频 两
  // 节点并连回源），进度走全局 message（key 覆盖更新）。工具条随选中态隐藏
  // 也不打断——整条链路闭包自持，不依赖组件存活。
  const handleAudioVideoSeparate = async () => {
    if (!url || audioSepBusy.current) return;
    // 音频解码同属 WebCodecs,复用「剪辑」的探测。
    if (!isTrimSupported()) {
      message.error('当前浏览器不支持音视频分离,请使用新版 Chrome/Edge');
      return;
    }
    audioSepBusy.current = true;
    const key = `audiosep-${nodeId}`;
    message.open({ key, type: 'loading', content: '音视频分离处理中… 0%', duration: 0 });
    try {
      await executeAudioSeparate(nodeId, url, (p) =>
        message.open({
          key,
          type: 'loading',
          content: `音视频分离处理中… ${Math.round(p * 100)}%`,
          duration: 0,
        }),
      );
      message.open({ key, type: 'success', content: '已分离出音轨与无声视频', duration: 2 });
    } catch (e: any) {
      message.open({
        key,
        type: 'error',
        content: e?.response?.data?.error || e?.message || '音视频分离失败',
        duration: 3,
      });
    } finally {
      audioSepBusy.current = false;
    }
  };

  // 放大查看：走全局 Lightbox（与双击节点视频同一个弹框，自带下载/Esc 关闭）。
  // title 与各宿主双击入口的文案保持一致。
  const handlePreview = () => {
    if (!url) {
      message.warning('当前节点没有可预览的视频');
      return;
    }
    const st = useCanvasStore.getState();
    const node = st.nodes.find((n) => n.id === nodeId);
    st.setLightbox({
      url,
      kind: 'video',
      title:
        node?.type === 'gen-video' ? 'AI 视频' : node?.type === 'video-input' ? '视频输入' : '视频',
    });
  };

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
        <BarButton
          label="剪辑"
          title="裁剪时间段,产出新视频节点"
          onAction={() => {
            if (!url) return;
            if (!isTrimSupported()) {
              message.error('当前浏览器不支持视频剪辑,请使用新版 Chrome/Edge');
              return;
            }
            useCanvasStore.getState().setTrimmingNode(nodeId);
          }}
        >
          <Scissors size={iconSize} />
        </BarButton>
        <BarButton
          label="片段重拍"
          title="选取片段重新生成(整段可留空原样重跑)"
          onAction={() => {
            if (!url) return;
            // 与「剪辑」同一条 WebCodecs 依赖(filmstrip 抽帧 + 切片转码)。
            if (!isTrimSupported()) {
              message.error('当前浏览器不支持片段重拍,请使用新版 Chrome/Edge');
              return;
            }
            if (!modelsQuery.data) {
              message.warning('模型目录加载中,请稍后再试');
              return;
            }
            // 报错只在模型中心没配写死的重拍模型时出现(用户拍板 2026-08-17)。
            if (!modelsQuery.data.some((m) => m.modelId === RESHOOT_MODEL_ID)) {
              message.warning(`模型中心未配置 ${RESHOOT_MODEL_ID},无法使用片段重拍`);
              return;
            }
            useCanvasStore.getState().setReshootNode(nodeId);
          }}
        >
          <SquarePlay size={iconSize} />
        </BarButton>
        <BarButton
          label="裁剪"
          title="框选画面区域,裁出新视频节点"
          onAction={() => {
            if (!url) return;
            // 与「剪辑」同一条 WebCodecs 依赖,复用同一个探测。
            if (!isTrimSupported()) {
              message.error('当前浏览器不支持视频裁剪,请使用新版 Chrome/Edge');
              return;
            }
            useCanvasStore.getState().setCroppingNode(nodeId);
          }}
        >
          <Scan size={iconSize} />
        </BarButton>
        <BarButton
          label="高清"
          title="新建转高清节点并连线"
          onAction={() => spawnPostprocessNode('video-upscale', nodeId)}
        >
          <HdBadge />
        </BarButton>
        {/* 与「高清」同形态：只新建节点并连线，档位与提交在节点里完成（用户拍板 2026-09-11）。 */}
        <BarButton
          label="白模"
          title="新建转白模节点并连线"
          onAction={() => spawnPostprocessNode('video-whitemodel', nodeId)}
        >
          <Box size={iconSize} />
        </BarButton>
        {SHOW_PLACEHOLDER_ACTIONS && (
          <BarButton label="逐帧拉片">
            <Palette size={iconSize} />
          </BarButton>
        )}
        <BarButton
          label="智能续写"
          title="选取前置片段,以其为参考续写生成"
          onAction={() => {
            if (!url) return;
            // 与「剪辑」同一条 WebCodecs 依赖(filmstrip 抽帧 + 切片转码)。
            if (!isTrimSupported()) {
              message.error('当前浏览器不支持智能续写,请使用新版 Chrome/Edge');
              return;
            }
            if (!modelsQuery.data) {
              message.warning('模型列表加载中,请稍后再试');
              return;
            }
            if (!modelsQuery.data.some((m) => m.modelId === CONTINUATION_MODEL_ID)) {
              message.warning('模型中心未启用智能续写模型');
              return;
            }
            useCanvasStore.getState().setContinuationNode(nodeId);
          }}
        >
          <AlarmClockPlus size={iconSize} />
        </BarButton>
        {/* 只有「擦除字幕」一项功能，不用下拉形态（用户拍板 2026-08-14）。 */}
        <BarButton
          label="智能去字幕"
          title="新建擦除字幕节点并连线"
          onAction={() => spawnPostprocessNode('subtitle-erase', nodeId)}
        >
          <RemoveFormatting size={iconSize} />
        </BarButton>
        {/* v1 只有「音视频分离」一项功能,不用下拉形态(用户拍板 2026-08-17,
            同「智能去字幕」先例);「人声分离」要服务端分离模型,暂不做,
            将来接入时再改回下拉。 */}
        <BarButton
          label="音频分离"
          title="拆出音轨与无声视频,各落一个节点"
          onAction={() => void handleAudioVideoSeparate()}
        >
          <AudioLines size={iconSize} />
        </BarButton>
        {SHOW_PLACEHOLDER_ACTIONS && (
          <BarButton label="画面编辑" chevron>
            <SquarePen size={iconSize} />
          </BarButton>
        )}
        <Divider />
        <BarButton title="下载" onAction={handleDownload}>
          <Download size={iconSize} />
        </BarButton>
        <BarButton title="放大查看" onAction={handlePreview}>
          <Maximize2 size={iconSize} />
        </BarButton>
      </div>
    </div>
  );
}
