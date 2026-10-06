import { useEffect, useMemo, useState } from 'react';
import { Modal, Button, Input, TreeSelect } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Music, Video as VideoIcon } from 'lucide-react';
import { listLibraryFolders } from '@/shared/api';
import type { LibraryFolder } from '@/modules/canvas/panels/library/types';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

const NAME_MAX = 20;

export interface SaveToLibraryModalProps {
  open: boolean;
  kind: 'image' | 'video' | 'audio';
  previewUrl?: string;
  defaultName: string;
  saving?: boolean;
  onCancel: () => void;
  onConfirm: (folderId: number, name: string) => void;
}

// 把扁平目录列表转成 TreeSelect 需要的树。目录对媒体类型没有任何限制——
// Seedance 合规校验已经不再依赖「图片放在哪个目录」了。
function toTreeData(folders: LibraryFolder[]): any[] {
  const childMap = new Map<number | null, LibraryFolder[]>();
  folders.forEach((f) => {
    const k = f.parentId ?? null;
    if (!childMap.has(k)) childMap.set(k, []);
    childMap.get(k)!.push(f);
  });
  const build = (f: LibraryFolder): any => ({
    title: f.name,
    value: f.id,
    children: (childMap.get(f.id) ?? []).map(build),
  });
  return (childMap.get(null) ?? []).map(build);
}

export function SaveToLibraryModal({
  open,
  kind,
  previewUrl,
  defaultName,
  saving,
  onCancel,
  onConfirm,
}: SaveToLibraryModalProps) {
  const [name, setName] = useState(defaultName);
  const [folderId, setFolderId] = useState<number | undefined>();

  const foldersQuery = useQuery({
    queryKey: ['library-folders'],
    queryFn: listLibraryFolders,
    enabled: open,
  });

  // 每次打开都重置，避免上一次的输入/选择残留。
  useEffect(() => {
    if (open) {
      setName(defaultName);
      setFolderId(undefined);
    }
  }, [open, defaultName]);

  const treeData = useMemo(() => toTreeData(foldersQuery.data ?? []), [foldersQuery.data]);

  const trimmed = name.trim();
  const canSave = !!trimmed && folderId != null && !saving;

  return (
    <Modal title="保存资产" open={open} onCancel={onCancel} footer={null} width={720}>
      <div className="flex gap-6 pt-2">
        <div className="w-[260px] shrink-0">
          <div className="w-full aspect-square rounded bg-popover flex items-center justify-center overflow-hidden">
            {kind === 'image' && previewUrl ? (
              <img src={previewUrl} crossOrigin={mediaCrossOrigin(previewUrl)} alt="" className="w-full h-full object-contain" />
            ) : kind === 'video' && previewUrl ? (
              <video src={previewUrl} crossOrigin={mediaCrossOrigin(previewUrl)} className="w-full h-full object-contain" muted />
            ) : kind === 'audio' ? (
              <Music size={48} className="text-muted-foreground" />
            ) : (
              <VideoIcon size={48} className="text-muted-foreground" />
            )}
          </div>
          <div className="mt-2 text-sm text-muted-foreground truncate">{trimmed || defaultName}</div>
        </div>

        <div className="flex-1 min-w-0 space-y-4">
          <div>
            <div className="text-sm text-muted-foreground mb-1.5">
              资产名称 <span className="text-red-400">*</span>
            </div>
            <Input
              value={name}
              maxLength={NAME_MAX}
              showCount
              onChange={(e) => setName(e.target.value)}
              placeholder="给这份素材起个名字"
              autoFocus
            />
          </div>

          <div>
            <div className="text-sm text-muted-foreground mb-1.5">
              保存位置 <span className="text-red-400">*</span>
            </div>
            <TreeSelect
              className="w-full"
              value={folderId}
              onChange={(v) => setFolderId(v)}
              treeData={treeData}
              loading={foldersQuery.isLoading}
              placeholder="请选择文件夹"
              notFoundContent={foldersQuery.isLoading ? '加载中…' : '还没有文件夹'}
            />
          </div>
        </div>
      </div>

      <div className="mt-6 flex justify-end gap-2">
        <Button onClick={onCancel}>取消</Button>
        <Button
          type="primary"
          loading={saving}
          disabled={!canSave}
          onClick={() => folderId != null && onConfirm(folderId, trimmed)}
        >
          保存
        </Button>
      </div>
    </Modal>
  );
}
