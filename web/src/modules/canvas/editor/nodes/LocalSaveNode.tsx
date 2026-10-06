import { Download } from 'lucide-react';
import { useCanvasStore } from '../store';
import { ThumbImg } from '../ThumbImg';
import { NodeShell } from '../NodeShell';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export function LocalSaveNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === id));
  const getConnectedInputs = useCanvasStore((s) => s.getConnectedInputs);

  if (!node) return null;

  const linked = getConnectedInputs(id);
  const allUrls = [...linked.images, ...linked.videos];
  const hasContent = allUrls.length > 0;

  const handleDownloadSingle = (url: string, idx: number) => {
    const ext = /\.(mp4|webm|mov)(\?|$)/i.test(url) ? 'mp4' : 'png';
    const a = document.createElement('a');
    a.href = url;
    a.download = `output-${idx + 1}.${ext}`;
    a.target = '_blank';
    a.rel = 'noreferrer';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  };

  const handleDownloadAll = async () => {
    // 如果有 jszip 就打包成 zip；否则逐个下载
    for (let i = 0; i < allUrls.length; i++) {
      handleDownloadSingle(allUrls[i], i);
    }
  };

  return (
    <NodeShell
      node={node}
      outputs={0}
      minWidth={260}
      minHeight={200}
    >
      {hasContent ? (
        <div className="flex flex-col gap-2 flex-1 min-h-0">
          <div className="flex-1 overflow-auto min-h-0 flex flex-col gap-1.5">
            {allUrls.map((url, i) => {
              const isVideo = /\.(mp4|webm|mov)(\?|$)/i.test(url);
              return (
                <div
                  key={i}
                  className="flex items-center gap-2 p-1.5 rounded bg-popover border border-input"
                >
                  {isVideo ? (
                    <video src={url} crossOrigin={mediaCrossOrigin(url)} className="w-12 h-12 object-cover rounded" muted />
                  ) : (
                    <ThumbImg
                      src={url}
                      alt={`file-${i + 1}`}
                      className="w-12 h-12 object-cover rounded"
                      draggable={false}
                    />
                  )}
                  <span className="text-[10px] text-sidebar-foreground flex-1 truncate">
                    {isVideo ? '视频' : '图片'} {i + 1}
                  </span>
                  <button
                    className="p-1 text-sidebar-foreground hover:text-white transition-colors"
                    onClick={(e) => {
                      e.stopPropagation();
                      handleDownloadSingle(url, i);
                    }}
                    onMouseDown={(e) => e.stopPropagation()}
                    title="下载"
                  >
                    <Download size={14} />
                  </button>
                </div>
              );
            })}
          </div>
          <button
            className="w-full flex items-center justify-center gap-1.5 py-1.5 bg-blue-600 hover:bg-blue-500 text-white text-xs rounded transition-colors border-none cursor-pointer shrink-0"
            onClick={(e) => {
              e.stopPropagation();
              handleDownloadAll();
            }}
            onMouseDown={(e) => e.stopPropagation()}
          >
            <Download size={12} />
            全部下载（{allUrls.length} 个文件）
          </button>
        </div>
      ) : (
        <div className="flex-1 border border-dashed border-input rounded-md flex items-center justify-center text-muted-foreground text-xs text-center px-4">
          连接上游节点，即可下载生成的图片或视频
        </div>
      )}
    </NodeShell>
  );
}
