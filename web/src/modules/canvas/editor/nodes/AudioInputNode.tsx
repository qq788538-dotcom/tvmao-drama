import { useRef } from 'react';
import { Mic2, Upload } from 'lucide-react';
import { useCanvasStore } from '../store';
import { NodeShell } from '../NodeShell';
import { uploadFileIntoNode } from '../utils/uploadIntoNode';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { WaveformPlayer } from './WaveformPlayer';
import { AudioActionsBar } from './AudioActionsBar';
import { useStoryboardMode } from '../../storyboard/StoryboardContext';

export function AudioInputNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodesById.get(id));
  const fileInputRef = useRef<HTMLInputElement>(null);
  const storyboardMode = useStoryboardMode();
  const { onContextMenu, overlay: menuOverlay, badge: seedanceBadge } = useMediaNodeMenu({
    url: node?.content,
    kind: 'audio',
    nodeId: id,
  });

  if (!node) return null;

  const handleFile = async (file: File | undefined) => {
    if (!file) return;
    await uploadFileIntoNode(id, file);
  };

  const fileName = node.content
    ? decodeURIComponent(node.content.split('/').pop()?.split('?')[0] ?? '音频文件')
    : '';

  return (
    <NodeShell node={node} inputs={0} minWidth={260} minHeight={160} titleExtra={seedanceBadge} bodyOverflow="visible">
      {/* 故事板详情面板里节点是静态预览，没有选中/工具条语义（同 InputImageNode）。 */}
      <AudioActionsBar nodeId={id} visible={!!node.content && !storyboardMode} url={node.content} />
      <input ref={fileInputRef} type="file" accept="audio/*" className="hidden" onChange={(e) => handleFile(e.target.files?.[0])} />

      <div className="flex items-center gap-1.5 px-3 py-2 border-b border-input/50 text-xs font-semibold shrink-0">
        <Mic2 size={13} className="text-orange-400" />
        <span>音频输入</span>
      </div>

      <div className="flex-1 flex flex-col p-2 gap-1 min-h-0" onContextMenu={onContextMenu}>
        {node.content ? (
          <>
            {/* key=url：更换音频时整体重挂载，播放态/波形/blob 全部随旧 url 一起走。 */}
            <WaveformPlayer
              key={node.content}
              url={node.content}
              nodeId={id}
              className="flex-1 rounded-lg bg-black/40 p-2"
              topRight={
                <label
                  className="w-6 h-6 rounded-md bg-black/55 hover:bg-black/80 text-white cursor-pointer flex items-center justify-center transition-colors"
                  onMouseDown={(e) => e.stopPropagation()}
                  title="更换音频"
                >
                  <Upload size={12} />
                  <input type="file" className="hidden" accept="audio/*" onChange={(e) => handleFile(e.target.files?.[0])} />
                </label>
              }
            />
            <div className="text-[10px] text-muted-foreground truncate w-full text-center shrink-0" title={fileName}>
              {fileName}
            </div>
          </>
        ) : node.status === 'uploading' ? (
          <div className="flex-1 flex flex-col items-center justify-center gap-2 text-sidebar-foreground">
            <Upload size={24} className="canvas-pulse" />
            <span className="text-xs">上传中…</span>
          </div>
        ) : (
          <div
            className="w-full flex-1 flex flex-col items-center justify-center border-2 border-dashed border-input hover:border-input rounded-lg transition-colors cursor-pointer"
            onClick={(e) => { e.stopPropagation(); fileInputRef.current?.click(); }}
            onMouseDown={(e) => e.stopPropagation()}
          >
            <Mic2 size={28} className="text-muted-foreground mb-2" />
            <span className="text-xs text-sidebar-foreground">点击选择音频</span>
            <span className="text-[10px] text-muted-foreground mt-1">MP3 / WAV / AAC / OGG</span>
          </div>
        )}
      </div>
      {menuOverlay}
    </NodeShell>
  );
}
