import { Download, Eye, Maximize2 } from 'lucide-react';
import { App as AntApp } from 'antd';
import { extFromUrl, saveMediaToDisk, saveWithFeedback } from '@/shared/saveToDisk';
import { useCanvasStore } from '../store';
import { NodeShell } from '../NodeShell';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { ImageActionsBar } from './ImageActionsBar';
import { VideoActionsBar } from './VideoActionsBar';
import { VideoPlayer } from './VideoPlayer';
import { useGenerationTimer } from '../hooks/useGenerationTimer';
import { useThumbUrl } from '../thumbs';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { WaveformPlayer } from './WaveformPlayer';
import { AudioActionsBar } from './AudioActionsBar';

export function PreviewNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  // fallback URL 故意只限定在「asset」类源节点（用户上传的输入）。这里
  // 绝不读取 gen-image / gen-video 源节点，哪怕它们的 .content 里还留着
  // URL——生成结果直接落在 gen 节点自身上，不走连线。在这里读 gen 源会把
  // 过期 URL 漏到新连上来的预览节点上。
  const fallbackUrl = useCanvasStore((s) => {
    const incoming = s.incomingByNode.get(id) ?? [];
    for (const c of incoming) {
      const src = s.nodesById.get(c.from);
      if (!src) continue;
      if (
        src.type !== 'input-image' &&
        src.type !== 'video-input' &&
        src.type !== 'input-audio'
      ) {
        continue;
      }
      const u = src.content;
      if (typeof u === 'string' && /^https?:\/\//.test(u)) return u;
    }
    return '';
  });

  const isGenerating = node?.status === 'generating';
  const { elapsed, finalDuration } = useGenerationTimer(isGenerating);

  // 提前算好 url（不靠 node 短路），这样下面的缩略图 hook 才能无条件执行
  // ——hooks 必须在任何提前 return 之前。
  const kind = node?.settings?.kind as 'image' | 'video' | 'audio' | undefined;
  const ownUrl = node?.content;
  // 只在该预览自己没有内容、且不在生成中时才用 fallback URL。上面的
  // selector 已经把它限定在 asset 源节点上了。
  const url = ownUrl || (isGenerating ? '' : fallbackUrl);
  const isVideo =
    kind === 'video' ||
    (!kind && /\.(mp4|webm|mov)(\?|$)/i.test(url));
  // 音频只能靠 kind 判（签名 URL 通常没有扩展名）：agent 的 audio_info 产物落
  // preview 节点时写 settings.kind='audio'。没有这个分支时音频会掉进 <img>
  // 渲染直接破图。线上此前不存在 kind='audio' 的节点，纯增量。
  const isAudio = kind === 'audio' || (!kind && /\.(mp3|wav|m4a|ogg)(\?|$)/i.test(url));
  // 图片预览走缩略图；视频不适用。Lightbox 与下载按钮仍然用原图 url。
  const thumbUrl = useThumbUrl(isVideo ? undefined : url);
  const { message } = AntApp.useApp();
  // 故事板详情面板里这张媒体是主角，占满预览区、用户就是来看清的：直接上原图，
  // 也不需要再点开 Lightbox（开出来还是同一张）。画布上必须相反——节点才两三百
  // 像素宽、一张画布可能挂上百个，缩略图省流量，双击预览是唯一看清的途径。
  const storyboardMode = useStoryboardMode();
  // 预览节点此前完全没有右键菜单——生成结果最常落在这里，却什么都点不了。
  const mediaMenu = useMediaNodeMenu({ url, kind: isVideo ? 'video' : 'image', nodeId: id });

  if (!node) return null;

  return (
    <NodeShell
      node={node}
      outputs={1}
      minWidth={260}
      minHeight={220}
      bodyOverflow="visible"
    >
      {/* 图片态挂图片工具条，视频态挂视频工具条，音频态挂音频工具条；空态没有可操作对象。 */}
      <ImageActionsBar nodeId={id} visible={!!url && !isVideo && !isAudio && !storyboardMode} />
      <VideoActionsBar nodeId={id} visible={!!url && isVideo && !storyboardMode} url={url} />
      <AudioActionsBar nodeId={id} visible={!!url && isAudio && !storyboardMode} url={url} />
      {mediaMenu.overlay}
      <div className="flex items-center justify-between px-3 py-2 border-b border-input/50 text-xs font-semibold shrink-0">
        <div className="flex items-center gap-1.5">
          <Eye size={13} className="text-amber-500" />
          <span>预览</span>
          {mediaMenu.badge}
        </div>
        {(isGenerating || finalDuration !== null) && (
          <span
            className={`px-1.5 py-0.5 rounded text-[10px] font-mono border ${
              isGenerating
                ? 'bg-blue-500/20 text-blue-400 border-blue-500/30'
                : 'bg-emerald-500/15 text-emerald-400 border-emerald-500/30'
            }`}
          >
            {isGenerating ? `⏱ ${elapsed.toFixed(1)}s` : `✓ ${finalDuration!.toFixed(1)}s`}
          </span>
        )}
      </div>
      <div className="flex-1 p-2 min-h-0" onContextMenu={mediaMenu.onContextMenu}>
        {isGenerating && !url ? (
          <div className="w-full h-full border border-dashed border-blue-500/30 rounded-md flex flex-col items-center justify-center text-blue-300/70 text-xs gap-2 bg-blue-500/5">
            <div className="w-6 h-6 rounded-full border-2 border-blue-400/40 border-t-blue-400 animate-spin" />
            <span>生成中…</span>
          </div>
        ) : url ? (
          <div
            className={`relative w-full h-full rounded-md overflow-hidden bg-background border border-border ${
              storyboardMode ? '' : 'cursor-zoom-in'
            }`}
            onDoubleClick={storyboardMode || isAudio ? undefined : (e) => {
              // 音频没有灯箱形态（开出来是张破图），双击不做事，播放靠内联 controls。
              e.stopPropagation();
              setLightbox({ url, kind: isVideo ? 'video' : 'image', title: '预览' });
            }}
          >
            {/* 容器不再整体拦 mousedown（那会连图片预览都拖不动节点）；只有视频
                的控制条需要保护，VideoPlayer 的控制条自己拦冒泡。 */}
            {isVideo ? (
              <VideoPlayer src={url} crossOrigin={mediaCrossOrigin(url)} className="w-full h-full object-contain" />
            ) : isAudio ? (
              /* 波形播放器自己在交互区拦 mousedown（seek 拖动不能被节点拖拽吃掉），
                 无需 stopIfInVideoControls。底部行时间靠左、播放键居中——右下角留给
                 下面那个通用下载按钮，不重叠。 */
              <div className="w-full h-full p-2">
                <WaveformPlayer key={url} url={url} nodeId={id} className="w-full h-full" />
              </div>
            ) : (
              <img src={storyboardMode ? url : thumbUrl} crossOrigin={mediaCrossOrigin(storyboardMode ? url : thumbUrl)} loading="lazy" decoding="async" alt="预览" className="w-full h-full object-contain" draggable={false} />
            )}
            {/* hover 时浮出的放大图标：它只是在**暗示**「双击可放大」。故事板详情
                面板里双击已经不弹 Lightbox 了（那儿本来就是原图，开出来还是同一张），
                这个暗示留着就是骗人——用户照着提示双击，什么都不会发生。音频同理：
                没有灯箱形态，双击被禁用，图标还会盖在波形正中间。 */}
            {!storyboardMode && !isAudio && (
              <div className="absolute inset-0 bg-black/0 group-hover:bg-black/30 transition-colors flex items-center justify-center opacity-0 group-hover:opacity-100 pointer-events-none">
                <Maximize2 size={22} className="text-white/90" />
              </div>
            )}
            {/* 音频态不渲染角落下载：音频工具条（AudioActionsBar）已有下载，
                避免两个下载图标叠着出现。 */}
            {!isAudio && (
              <button
                type="button"
                className="absolute bottom-2 right-2 p-1.5 bg-black/70 hover:bg-black text-white rounded backdrop-blur border-none cursor-pointer"
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.stopPropagation();
                  const kindName = isVideo ? 'video' : 'image';
                  void saveWithFeedback(
                    () => saveMediaToDisk(url, `${kindName}-${Date.now()}.${extFromUrl(url, isVideo ? 'mp4' : 'png')}`),
                    message,
                  );
                }}
                title="下载"
              >
                <Download size={14} />
              </button>
            )}
          </div>
        ) : (
          <div className="w-full h-full border border-dashed border-input rounded-md flex items-center justify-center text-muted-foreground text-xs text-center px-4">
            等待生成结果，或将素材节点连接到此节点以预览
          </div>
        )}
      </div>
    </NodeShell>
  );
}
