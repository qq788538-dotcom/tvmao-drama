import { useMutation } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { X } from 'lucide-react';
import { applyLibraryItemToCanvas } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { NodeType } from '@/modules/canvas/editor/types';
import type { LibraryItem } from './types';
import { mediaCrossOrigin } from '../../editor/utils/mediaCrossOrigin';

interface Props {
  item: LibraryItem;
  projectId: number;
  anchorRect?: DOMRect;
  onClose: () => void;
  onMouseEnter?: () => void;
  onMouseLeave?: () => void;
}

function nodeTypeFor(kind: LibraryItem['kind']): NodeType {
  if (kind === 'image') return 'input-image';
  if (kind === 'video') return 'video-input';
  return 'input-audio';
}

export function ItemDetail({
  item,
  projectId,
  anchorRect,
  onClose,
  onMouseEnter,
  onMouseLeave,
}: Props) {
  const { message } = AntApp.useApp();
  const addNode = useCanvasStore((s) => s.addNode);
  const updateNode = useCanvasStore((s) => s.updateNode);
  const updateNodeSettings = useCanvasStore((s) => s.updateNodeSettings);

  const mutation = useMutation({
    mutationFn: () => applyLibraryItemToCanvas(item.id, projectId),
    onSuccess: (res) => {
      const view = useCanvasStore.getState().view;
      const cx = (window.innerWidth / 2 - view.x) / view.zoom;
      const cy = (window.innerHeight / 2 - view.y) / view.zoom;
      const id = addNode(nodeTypeFor(item.kind), { x: cx, y: cy });
      updateNode(id, { content: res.url, status: 'succeeded' });
      updateNodeSettings(id, {
        url: res.url,
        assetId: res.assetId,
        libraryItemId: item.id,
        mimeType: res.mimeType,
      });
      persistNodeById(id);
      message.success('已应用到画布');
      onClose();
    },
    onError: (e: any) => {
      message.error(e?.response?.data?.error || e.message || '应用失败');
    },
  });

  return (
    <div
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      className="fixed w-72 bg-card border border-input rounded-2xl shadow-2xl p-3 z-50"
      style={{
        left: anchorRect ? anchorRect.right + 12 : 0,
        // 让预览相对 hover 的那一行垂直居中，并钳制在视口内。
        // PREVIEW_HEIGHT 是个估值；媒体排版后 image/video/audio 卡片高度相近。
        top: anchorRect
          ? Math.max(
              8,
              Math.min(
                anchorRect.top + anchorRect.height / 2 - 190,
                window.innerHeight - 380 - 8,
              ),
            )
          : 16,
      }}
    >
      {/* 跨过缝隙的透明桥接区，让光标不会丢失 hover。 */}
      <div
        className="absolute"
        style={{ left: -16, top: 0, bottom: 0, width: 16 }}
      />
      <button
        onClick={onClose}
        className="absolute top-2 right-2 text-muted-foreground hover:text-foreground bg-transparent border-none cursor-pointer"
        aria-label="关闭预览"
      >
        <X size={14} />
      </button>
      {item.kind === 'image' && item.url && (
        <img
          src={item.url}
          crossOrigin={mediaCrossOrigin(item.url)}
          alt=""
          className="w-full max-h-72 object-contain rounded-xl mb-3 bg-black"
        />
      )}
      {item.kind === 'video' && item.url && (
        <video
          src={item.url} crossOrigin={mediaCrossOrigin(item.url)}
          controls
          className="w-full max-h-72 rounded-xl mb-3 bg-black"
        />
      )}
      {item.kind === 'audio' && item.url && (
        <audio src={item.url} controls className="w-full mb-3" />
      )}
      <div className="text-sm text-foreground font-medium truncate" title={item.name}>
        {item.name || `item-${item.id}`}
      </div>
      <div className="text-xs text-muted-foreground mb-3">
        创建于 {new Date(item.createdAt).toLocaleString()}
      </div>
      <button
        onClick={() => mutation.mutate()}
        disabled={mutation.isPending}
        className="w-full py-2.5 bg-accent hover:bg-accent text-white rounded-xl text-sm font-medium disabled:opacity-60 border-none cursor-pointer transition-colors"
      >
        {mutation.isPending ? '应用中…' : '应用到画布'}
      </button>
    </div>
  );
}
