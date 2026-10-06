import { Empty, Modal } from 'antd';
import { ThumbImg } from '@/modules/canvas/editor/ThumbImg';

export interface GalleryImage {
  nodeId: string;
  url: string;
}

interface Props {
  open: boolean;
  images: GalleryImage[];
  onClose: () => void;
  onSelect: (url: string) => void;
}

// 投稿封面「历史图库」选图层——用户在小菜单里选「本地上传」/「历史图库」二选一，
// 这个弹层承接后者：不新增后端接口，图片来源就是当前画布已有的图片节点
// （content 已经是可展示的 URL，画布 store 里全都有，没必要为「挑一张当封面」
// 这种轻量操作再开一条查询链路）。是否禁用「历史图库」这个菜单项（画布没有图
// 时）由调用方（TvShowSubmitModal）判断——这里的空态只是防御性兜底。
export function CoverGalleryModal({ open, images, onClose, onSelect }: Props) {
  return (
    <Modal open={open} onCancel={onClose} footer={null} title="历史图库" width={560} destroyOnClose>
      {images.length === 0 ? (
        <Empty description="当前画布还没有图片" />
      ) : (
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(96px, 1fr))',
            gap: 12,
            maxHeight: 360,
            overflowY: 'auto',
          }}
        >
          {images.map((img) => (
            <button
              key={img.nodeId}
              type="button"
              data-testid={`cover-gallery-item-${img.nodeId}`}
              onClick={() => {
                onSelect(img.url);
                onClose();
              }}
              style={{
                padding: 0,
                border: '1px solid rgba(0,0,0,0.12)',
                borderRadius: 8,
                overflow: 'hidden',
                cursor: 'pointer',
                aspectRatio: '1 / 1',
                background: 'none',
              }}
            >
              <ThumbImg
                src={img.url}
                alt="封面候选"
                style={{ width: '100%', height: '100%', objectFit: 'cover' }}
              />
            </button>
          ))}
        </div>
      )}
    </Modal>
  );
}
