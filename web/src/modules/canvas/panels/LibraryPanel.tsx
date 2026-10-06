import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Dropdown, Input, Modal, App as AntApp } from 'antd';
import { ChevronLeft, Plus, Search, UserRound } from 'lucide-react';
import {
  createLibraryFolder,
  deleteLibraryItem,
  deleteLibraryFolder,
  listLibraryFolders,
  listLibraryItems,
  moveLibraryFolder,
  renameLibraryFolder,
  updateLibraryItem,
  uploadLibraryItem,
} from '@/shared/api';
import type { LibraryFolder, LibraryItem } from './library/types';
import { FolderTree } from './library/FolderTree';
import { ItemDetail } from './library/ItemDetail';
import { SelectFolderModal } from './library/SelectFolderModal';
import { DeleteFolderConfirm } from './library/DeleteFolderConfirm';
import { byIdMap, isSelfOrDescendant } from './library/moveRules';
import { AICharactersModal } from './aicharacters/AICharactersModal';

interface Props {
  open: boolean;
  projectId: number;
  onClose: () => void;
}

interface PendingDelete {
  folder: LibraryFolder;
  itemCount: number;
  childCount: number;
}

export function LibraryPanel({ open, projectId, onClose }: Props) {
  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();

  const [search, setSearch] = useState('');
  const [selectedFolderId, setSelectedFolderId] = useState<number | null>(null);
  const [hoveredItem, setHoveredItem] = useState<LibraryItem | null>(null);
  const [hoverAnchor, setHoverAnchor] = useState<DOMRect | undefined>();
  const hoverHideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const [creatingFolder, setCreatingFolder] = useState(false);
  const [newFolderName, setNewFolderName] = useState('');
  const [pickFolderForUpload, setPickFolderForUpload] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<PendingDelete | null>(null);
  const [aiCharactersOpen, setAiCharactersOpen] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pendingUploadFolderRef = useRef<number | null>(null);

  const uploadMut = useMutation({
    mutationFn: ({ folderId, file }: { folderId: number; file: File }) =>
      uploadLibraryItem(folderId, file),
    onSuccess: () => {
      message.success('上传成功');
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      queryClient.invalidateQueries({ queryKey: ['me'] });
    },
    onError: (e: any) =>
      message.error(e?.response?.data?.error || e.message || '上传失败'),
  });

  const foldersQuery = useQuery({
    queryKey: ['library-folders'],
    queryFn: listLibraryFolders,
    enabled: open,
  });

  // 总是拉取 item，这样展开的文件夹下能显示数量。
  const itemsQuery = useQuery({
    queryKey: ['library-items', search],
    queryFn: () => listLibraryItems({ search: search || undefined, limit: 500 }),
    enabled: open,
    // 有图片仍在 Seedance 入库中 → 5s 轮询直到 active/failed_permanent（对齐项目管理 AssetTab）。
    // 'failed' 是瞬时态：worker 下一拍会自动重注册回 pending，所以也要继续轮询，
    // 否则界面会卡在红色「入库失败」直到用户手动刷新；只有 'failed_permanent' 才是终态、停止轮询。
  });

  useEffect(() => {
    if (!open) {
      setHoveredItem(null);
      setCreatingFolder(false);
      setNewFolderName('');
    }
  }, [open]);

  function showPreview(item: LibraryItem, anchor?: DOMRect) {
    if (hoverHideTimerRef.current) {
      clearTimeout(hoverHideTimerRef.current);
      hoverHideTimerRef.current = null;
    }
    setHoveredItem(item);
    if (anchor) setHoverAnchor(anchor);
  }

  function schedulePreviewHide() {
    if (hoverHideTimerRef.current) clearTimeout(hoverHideTimerRef.current);
    hoverHideTimerRef.current = setTimeout(() => setHoveredItem(null), 350);
  }

  function cancelPreviewHide() {
    if (hoverHideTimerRef.current) {
      clearTimeout(hoverHideTimerRef.current);
      hoverHideTimerRef.current = null;
    }
  }

  // 顶部的 + 总是创建根文件夹。要建子文件夹，用行级 ⋯ 菜单里的「新建文件夹」项。
  const createFolderMut = useMutation({
    mutationFn: (name: string) => createLibraryFolder(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      setCreatingFolder(false);
      setNewFolderName('');
    },
    onError: (e: any) =>
      message.error(e?.response?.data?.error || e.message || '创建失败'),
  });

  const renameFolderMut = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) => renameLibraryFolder(id, name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      message.success('已重命名');
    },
    onError: (e: any) => message.error(e?.response?.data?.error || e.message || '重命名失败'),
  });

  const moveFolderMut = useMutation({
    mutationFn: ({ id, parentId }: { id: number; parentId: number | null }) =>
      moveLibraryFolder(id, parentId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      message.success('已移动');
    },
    onError: (e: any) => message.error(e?.response?.data?.error || e.message || '移动失败'),
  });

  const [moveTarget, setMoveTarget] = useState<LibraryFolder | null>(null);
  const [itemMoveTarget, setItemMoveTarget] = useState<LibraryItem | null>(null);

  const renameItemMut = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      updateLibraryItem(id, { name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      queryClient.invalidateQueries({ queryKey: ['me'] });
      message.success('已重命名');
    },
    onError: (e: any) => message.error(e?.response?.data?.error || e.message || '重命名失败'),
  });

  const moveItemMut = useMutation({
    mutationFn: ({ id, folderId }: { id: number; folderId: number }) =>
      updateLibraryItem(id, { folderId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      queryClient.invalidateQueries({ queryKey: ['me'] });
      message.success('已移动');
    },
    onError: (e: any) => message.error(e?.response?.data?.error || e.message || '移动失败'),
  });

  const deleteItemMut = useMutation({
    mutationFn: (item: LibraryItem) => deleteLibraryItem(item.id).then(() => item),
    onSuccess: (item) => {
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      queryClient.invalidateQueries({ queryKey: ['me'] });
      message.success(`已删除 ${item.name || `item-${item.id}`}`);
    },
    onError: (e: any) => message.error(e?.response?.data?.error || e.message || '删除失败'),
  });

  const foldersById = byIdMap(foldersQuery.data ?? []);

  const tryDeleteFolderMut = useMutation({
    mutationFn: (folder: LibraryFolder) =>
      deleteLibraryFolder(folder.id, false).then(() => folder),
    onSuccess: (folder) => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      message.success(`已删除 ${folder.name}`);
    },
    onError: (e: any, folder) => {
      const status = e?.response?.status;
      const data = e?.response?.data;
      if (status === 409) {
        setPendingDelete({
          folder,
          itemCount: data?.itemCount ?? 0,
          childCount: data?.childCount ?? 0,
        });
        return;
      }
      message.error(data?.error || e.message || '删除失败');
    },
  });

  if (!open) return null;

  return (
    <div
      ref={panelRef}
      className="w-[360px] bg-card border border-border rounded-xl shadow-xl shadow-black/40 z-30 flex flex-col shrink-0 relative overflow-hidden"
    >
      <div className="flex items-center gap-2 px-4 py-3 border-b border-border">
        <button
          onClick={onClose}
          className="text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer flex items-center"
          aria-label="关闭素材库"
        >
          <ChevronLeft size={18} />
        </button>
        <h2 className="text-foreground font-semibold leading-none m-0">素材库</h2>
        <div className="flex-1" />
        <button
          onClick={() => setAiCharactersOpen(true)}
          className="h-8 px-2.5 flex items-center gap-1.5 rounded-md bg-popover hover:bg-accent border border-input text-foreground text-sm cursor-pointer transition-colors"
          title="虚拟人像库"
        >
          <UserRound size={15} />
          虚拟人像库
        </button>
        <Dropdown
          menu={{
            items: [
              {
                key: 'new-folder',
                label: '新建文件夹',
                onClick: () => {
                  setCreatingFolder(true);
                  setNewFolderName('');
                },
              },
              {
                key: 'upload',
                label: '上传资源',
                onClick: () => setPickFolderForUpload(true),
              },
            ],
          }}
          trigger={['click']}
        >
          <button
            className="w-8 h-8 flex items-center justify-center rounded-md bg-popover hover:bg-accent border border-input text-foreground cursor-pointer transition-colors"
            title="新建"
          >
            <Plus size={16} />
          </button>
        </Dropdown>
      </div>

      <div className="px-3 py-3 w-full">
        <div className="flex items-center gap-2 px-4 py-2.5 rounded-full bg-popover focus-within:bg-accent transition-colors w-full">
          <Search size={16} className="text-muted-foreground shrink-0" />
          <input
            type="text"
            placeholder="搜索"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="flex-1 min-w-0 bg-transparent text-foreground placeholder:text-muted-foreground text-sm outline-none border-none p-0"
          />
        </div>
      </div>

      {creatingFolder && (
        <div className="px-3 py-2 border-b border-border flex gap-2">
          <Input
            value={newFolderName}
            onChange={(e) => setNewFolderName(e.target.value)}
            onPressEnter={() => newFolderName && createFolderMut.mutate(newFolderName)}
            placeholder="文件夹名称"
            autoFocus
            size="small"
          />
          <button
            onClick={() => newFolderName && createFolderMut.mutate(newFolderName)}
            disabled={!newFolderName || createFolderMut.isPending}
            className="px-2 text-xs bg-blue-600 hover:bg-blue-700 disabled:opacity-60 text-white rounded border-none cursor-pointer"
          >
            创建
          </button>
          <button
            onClick={() => setCreatingFolder(false)}
            className="px-2 text-xs text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer"
          >
            取消
          </button>
        </div>
      )}

      <div className="flex-1 overflow-y-auto">
        <FolderTree
          folders={foldersQuery.data ?? []}
          items={itemsQuery.data?.data ?? []}
          selectedFolderId={selectedFolderId}
          onSelectFolder={setSelectedFolderId}
          onSelectItem={(it) => showPreview(it)}
          onHoverItem={(it, rect) => {
            if (it) showPreview(it, rect);
            else schedulePreviewHide();
          }}
          onCreateChild={(parent, name) => {
            createLibraryFolder(name, parent.id)
              .then(() => {
                queryClient.invalidateQueries({ queryKey: ['library-folders'] });
              })
              .catch((e: any) => {
                message.error(e?.response?.data?.error || e.message || '创建失败');
              });
          }}
          onRenameFolder={(f, name) => renameFolderMut.mutate({ id: f.id, name })}
          onMoveFolder={(f) => setMoveTarget(f)}
          onDeleteFolder={(f) => tryDeleteFolderMut.mutate(f)}
          onRenameItem={(item, name) => renameItemMut.mutate({ id: item.id, name })}
          onMoveItem={(item) => setItemMoveTarget(item)}
          onDeleteItem={(item) => {
            Modal.confirm({
              title: `确认删除文件「${item.name || `item-${item.id}`}」？`,
              okText: '删除',
              okButtonProps: { danger: true },
              cancelText: '取消',
              onOk: () => deleteItemMut.mutateAsync(item),
            });
          }}
        />
      </div>

      {hoveredItem && (
        <ItemDetail
          item={hoveredItem}
          projectId={projectId}
          anchorRect={hoverAnchor}
          onClose={() => setHoveredItem(null)}
          onMouseEnter={cancelPreviewHide}
          onMouseLeave={schedulePreviewHide}
        />
      )}

      <SelectFolderModal
        open={pickFolderForUpload}
        onCancel={() => setPickFolderForUpload(false)}
        onConfirm={(id) => {
          setPickFolderForUpload(false);
          if (id === null) return; // 上传不显示根目录选项，理论上不会为 null
          pendingUploadFolderRef.current = id;
          // 弹窗关闭后立刻打开原生文件选择器。
          setTimeout(() => fileInputRef.current?.click(), 0);
        }}
      />
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*,video/*,audio/*"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = '';
          const folderId = pendingUploadFolderRef.current;
          pendingUploadFolderRef.current = null;
          if (file && folderId) uploadMut.mutate({ folderId, file });
        }}
      />
      <DeleteFolderConfirm
        open={pendingDelete !== null}
        folderId={pendingDelete?.folder.id ?? null}
        itemCount={pendingDelete?.itemCount ?? 0}
        childCount={pendingDelete?.childCount ?? 0}
        onCancel={() => setPendingDelete(null)}
        onDeleted={() => setPendingDelete(null)}
      />
      <SelectFolderModal
        open={moveTarget !== null}
        onCancel={() => setMoveTarget(null)}
        showRootOption
        isFolderDisabled={
          // 唯一的限制：不能移进自己或自己的后代（成环）。
          moveTarget ? (f) => isSelfOrDescendant(f, moveTarget.id, foldersById) : undefined
        }
        onConfirm={(parentId) => {
          if (moveTarget) {
            moveFolderMut.mutate({ id: moveTarget.id, parentId });
          }
          setMoveTarget(null);
        }}
      />
      <SelectFolderModal
        open={itemMoveTarget !== null}
        onCancel={() => setItemMoveTarget(null)}
        onConfirm={(folderId) => {
          if (itemMoveTarget && folderId !== null) {
            moveItemMut.mutate({ id: itemMoveTarget.id, folderId });
          }
          setItemMoveTarget(null);
        }}
      />
      <AICharactersModal open={aiCharactersOpen} onClose={() => setAiCharactersOpen(false)} />
    </div>
  );
}
