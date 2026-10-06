import { useEffect, useMemo, useState } from 'react';
import { Modal, Button, Input } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Folder, FolderTree, Plus } from 'lucide-react';
import { createLibraryFolder, listLibraryFolders } from '@/shared/api';
import type { LibraryFolder } from './types';

// 'root' 代表移到最外层（根目录），对应后端 clearParent。
type Selection = number | 'root' | null;

interface Props {
  open: boolean;
  onCancel: () => void;
  onConfirm: (folderId: number | null) => void; // null = 根目录
  // showRootOption：顶部展示「根目录」可选项（移动目录时用；上传素材必须落到具体目录，不传）。
  showRootOption?: boolean;
  // isFolderDisabled：返回 true 的目录置灰不可选（如真人图库子树、被移动目录自身子树）。
  isFolderDisabled?: (f: LibraryFolder) => boolean;
}

export function SelectFolderModal({
  open,
  onCancel,
  onConfirm,
  showRootOption,
  isFolderDisabled,
}: Props) {
  const queryClient = useQueryClient();
  const [selectedId, setSelectedId] = useState<Selection>(null);
  const [creatingName, setCreatingName] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const foldersQuery = useQuery({
    queryKey: ['library-folders'],
    queryFn: listLibraryFolders,
    enabled: open,
  });

  // 关闭时清空选择，避免下次打开残留上一次的选中项。
  useEffect(() => {
    if (!open) {
      setSelectedId(null);
      setExpanded(new Set());
      setCreatingName(null);
    }
  }, [open]);

  const childMap = useMemo(() => {
    const m = new Map<number | null, LibraryFolder[]>();
    (foldersQuery.data ?? []).forEach((f) => {
      const k = f.parentId ?? null;
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(f);
    });
    return m;
  }, [foldersQuery.data]);

  const createMut = useMutation({
    mutationFn: (name: string) => createLibraryFolder(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      setCreatingName(null);
    },
  });

  function toggle(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function renderRow(f: LibraryFolder, depth: number) {
    const isOpen = expanded.has(f.id);
    const children = childMap.get(f.id) ?? [];
    const hasChildren = children.length > 0;
    const isSelected = selectedId === f.id;
    const disabled = isFolderDisabled?.(f) ?? false;

    return (
      <div key={f.id}>
        <div
          onClick={() => {
            if (!disabled) setSelectedId(f.id);
          }}
          className={`flex items-center gap-1 px-3 py-2 rounded ${
            disabled
              ? 'text-muted-foreground opacity-40 cursor-not-allowed'
              : isSelected
                ? 'bg-blue-600 text-white cursor-pointer'
                : 'text-foreground hover:bg-popover cursor-pointer'
          }`}
          style={{ paddingLeft: 12 + depth * 16 }}
        >
          {hasChildren ? (
            <button
              onClick={(e) => {
                e.stopPropagation();
                toggle(f.id);
              }}
              className="p-0.5 -ml-0.5 bg-transparent border-none cursor-pointer text-current opacity-70 hover:opacity-100"
            >
              <ChevronRight
                size={14}
                className={`transition-transform ${isOpen ? 'rotate-90' : ''}`}
              />
            </button>
          ) : (
            <span style={{ width: 18 }} />
          )}
          <Folder size={16} />
          <span className="text-sm flex-1 truncate">{f.name}</span>
          <span className="text-xs opacity-60">{f.itemCount} 项</span>
        </div>
        {isOpen && children.map((c) => renderRow(c, depth + 1))}
      </div>
    );
  }

  const roots = childMap.get(null) ?? [];

  return (
    <Modal title="选择文件夹" open={open} onCancel={onCancel} footer={null} width={420}>
      <div className="space-y-1 max-h-[360px] overflow-y-auto">
        {showRootOption && (
          <div
            onClick={() => setSelectedId('root')}
            className={`flex items-center gap-1 px-3 py-2 rounded cursor-pointer ${
              selectedId === 'root'
                ? 'bg-blue-600 text-white'
                : 'text-foreground hover:bg-popover'
            }`}
            style={{ paddingLeft: 12 }}
          >
            <span style={{ width: 18 }} />
            <FolderTree size={16} />
            <span className="text-sm flex-1 truncate">根目录（移到最外层）</span>
          </div>
        )}
        {roots.map((f) => renderRow(f, 0))}
        {roots.length === 0 && !foldersQuery.isLoading && (
          <div className="text-center text-xs text-muted-foreground py-4">
            还没有文件夹。先新建一个。
          </div>
        )}
      </div>
      <div className="mt-3">
        {creatingName === null ? (
          <Button icon={<Plus size={14} />} onClick={() => setCreatingName('')}>
            新建文件夹
          </Button>
        ) : (
          <div className="flex gap-2">
            <Input
              value={creatingName}
              onChange={(e) => setCreatingName(e.target.value)}
              placeholder="输入名称"
              onPressEnter={() => creatingName && createMut.mutate(creatingName)}
              autoFocus
            />
            <Button
              type="primary"
              loading={createMut.isPending}
              onClick={() => creatingName && createMut.mutate(creatingName)}
            >
              创建
            </Button>
            <Button onClick={() => setCreatingName(null)}>取消</Button>
          </div>
        )}
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <Button onClick={onCancel}>取消</Button>
        <Button
          type="primary"
          disabled={selectedId === null}
          onClick={() => {
            if (selectedId === null) return;
            onConfirm(selectedId === 'root' ? null : selectedId);
          }}
        >
          确认
        </Button>
      </div>
    </Modal>
  );
}
