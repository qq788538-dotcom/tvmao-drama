import { useEffect } from 'react';
import { Download, X } from 'lucide-react';
import { useCanvasStore } from './store';
import { mediaCrossOrigin } from './utils/mediaCrossOrigin';

export function Lightbox() {
  const lightbox = useCanvasStore((s) => s.lightbox);
  const setLightbox = useCanvasStore((s) => s.setLightbox);

  useEffect(() => {
    if (!lightbox) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setLightbox(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [lightbox, setLightbox]);

  if (!lightbox) return null;

  return (
    <div
      className="fixed inset-0 bg-black/90 z-[1500] flex items-center justify-center backdrop-blur-sm"
      onClick={() => setLightbox(null)}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div
        className="relative max-w-[92vw] max-h-[92vh] flex items-center justify-center"
        onClick={(e) => e.stopPropagation()}
      >
        {lightbox.kind === 'video' ? (
          <video
            src={lightbox.url} crossOrigin={mediaCrossOrigin(lightbox.url)}
            controls
            autoPlay
            className="max-w-[92vw] max-h-[92vh] rounded-lg shadow-2xl"
          />
        ) : lightbox.kind === 'audio' ? (
          <div className="bg-card rounded-lg shadow-2xl p-6 w-[480px] max-w-[92vw] flex flex-col items-center gap-4">
            <div className="w-20 h-20 rounded-full bg-blue-600/30 flex items-center justify-center text-blue-200 text-4xl">
              ♪
            </div>
            <audio src={lightbox.url} controls autoPlay className="w-full" />
            {lightbox.title && (
              <div className="text-foreground text-sm truncate w-full text-center">{lightbox.title}</div>
            )}
          </div>
        ) : (
          <img
            src={lightbox.url}
            crossOrigin={mediaCrossOrigin(lightbox.url)}
            alt={lightbox.title ?? ''}
            className="max-w-[92vw] max-h-[92vh] object-contain rounded-lg shadow-2xl"
            draggable={false}
          />
        )}
        <div className="absolute top-4 right-4 flex gap-2">
          <a
            href={lightbox.url}
            download
            target="_blank"
            rel="noreferrer"
            className="p-2 bg-black/70 hover:bg-black text-white rounded-full backdrop-blur transition-colors"
            title="下载"
            onClick={(e) => e.stopPropagation()}
          >
            <Download size={18} />
          </a>
          <button
            className="p-2 bg-black/70 hover:bg-black text-white rounded-full backdrop-blur transition-colors border-none cursor-pointer"
            onClick={() => setLightbox(null)}
            title="关闭 (Esc)"
          >
            <X size={18} />
          </button>
        </div>
        {lightbox.title && (
          <div className="absolute bottom-4 left-1/2 -translate-x-1/2 text-white/80 text-xs bg-black/70 px-3 py-1 rounded-full backdrop-blur">
            {lightbox.title}
          </div>
        )}
      </div>
    </div>
  );
}
