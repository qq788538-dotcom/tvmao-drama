import { useEffect } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { mediaCrossOrigin } from '../../utils/mediaCrossOrigin';

// 脚本模块自己的图片放大层。不复用画布的 Lightbox：它挂在 .canvas-root
// （position:fixed 自成 stacking context）内部，z-1500 再高也被困在画布层里，
// 压不过 portal 到 body 的脚本面板（z-50 且 DOM 更靠后）。这里同样 portal 到
// body 并给更高 z，天然盖住面板/弹窗/抽屉。展示用原图——缩略图只属于卡片。
export function ImagePreview({ url, title, onClose }: { url: string; title?: string; onClose: () => void }) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        // capture + 阻断：ESC 只关预览，不能穿透把底下的脚本面板一起关掉。
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [onClose]);

  return createPortal(
    <div
      data-testid="script-image-preview"
      className="fixed inset-0 z-[2000] flex items-center justify-center bg-black/90 backdrop-blur-sm"
      onClick={onClose}
      onMouseDown={(e) => e.stopPropagation()}
    >
      <img
        src={url}
        crossOrigin={mediaCrossOrigin(url)}
        alt={title ?? ''}
        className="max-h-[92vh] max-w-[92vw] rounded-lg shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      />
      <button
        type="button"
        aria-label="关闭预览"
        onClick={onClose}
        className="absolute right-4 top-4 rounded-full bg-white/10 p-2 text-white hover:bg-white/20 border-none cursor-pointer"
      >
        <X size={18} />
      </button>
    </div>,
    document.body,
  );
}
