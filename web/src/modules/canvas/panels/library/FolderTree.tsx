import { useMemo, useState } from 'react';
import { Dropdown } from 'antd';
import {
  AlertCircle,
  ChevronDown,
  ChevronRight,
  Folder,
  FolderOpen,
  FolderPlus,
  FolderInput,
  Image as ImageIcon,
  Loader2,
  MoreHorizontal,
  Music,
  Pencil,
  Trash2,
  UserRound,
  Video,
} from 'lucide-react';
import type { LibraryFolder, LibraryItem } from './types';
import { mediaCrossOrigin } from '../../editor/utils/mediaCrossOrigin';

type InlineEdit =
  | { kind: 'create-child'; folderId: number; value: string }
  | { kind: 'rename'; folderId: number; value: string }
  | { kind: 'rename-item'; itemId: number; value: string }
  | null;

interface Props {
  folders: LibraryFolder[];
  items: LibraryItem[];
  selectedFolderId: number | null;
  onSelectFolder: (id: number) => void;
  onSelectItem: (item: LibraryItem) => void;
  onHoverItem?: (item: LibraryItem | null, anchorRect?: DOMRect) => void;
  onCreateChild?: (parent: LibraryFolder, name: string) => void;
  onRenameFolder?: (folder: LibraryFolder, name: string) => void;
  onMoveFolder?: (folder: LibraryFolder) => void;
  onDeleteFolder?: (folder: LibraryFolder) => void;
  onRenameItem?: (item: LibraryItem, name: string) => void;
  onMoveItem?: (item: LibraryItem) => void;
  onDeleteItem?: (item: LibraryItem) => void;
}

export function FolderTree({
  folders,
  items,
  selectedFolderId,
  onSelectFolder,
  onSelectItem,
  onHoverItem,
  onCreateChild,
  onRenameFolder,
  onMoveFolder,
  onDeleteFolder,
  onRenameItem,
  onMoveItem,
  onDeleteItem,
}: Props) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [inlineEdit, setInlineEdit] = useState<InlineEdit>(null);

  const childMap = useMemo(() => {
    const m = new Map<number | null, LibraryFolder[]>();
    folders.forEach((f) => {
      const k = f.parentId ?? null;
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(f);
    });
    return m;
  }, [folders]);

  const itemsByFolder = useMemo(() => {
    const m = new Map<number, LibraryItem[]>();
    items.forEach((it) => {
      if (!m.has(it.folderId)) m.set(it.folderId, []);
      m.get(it.folderId)!.push(it);
    });
    return m;
  }, [items]);

  function toggle(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function commitInline() {
    if (!inlineEdit) return;
    const name = inlineEdit.value.trim();
    if (!name) {
      setInlineEdit(null);
      return;
    }
    if (inlineEdit.kind === 'create-child') {
      const parent = folders.find((f) => f.id === inlineEdit.folderId);
      if (parent && onCreateChild) onCreateChild(parent, name);
    } else if (inlineEdit.kind === 'rename') {
      const f = folders.find((x) => x.id === inlineEdit.folderId);
      if (f && f.name !== name && onRenameFolder) onRenameFolder(f, name);
    } else if (inlineEdit.kind === 'rename-item') {
      const item = items.find((x) => x.id === inlineEdit.itemId);
      if (item && item.name !== name && onRenameItem) onRenameItem(item, name);
    }
    setInlineEdit(null);
  }

  function cancelInline() {
    setInlineEdit(null);
  }

  function renderInlineCreateRow(depth: number) {
    if (!inlineEdit || inlineEdit.kind !== 'create-child') return null;
    return (
      <div
        className="flex items-center gap-1 px-2 py-1.5"
        style={{ paddingLeft: 8 + depth * 16 }}
        onClick={(e) => e.stopPropagation()}
      >
        <span style={{ width: 14 }} />
        <Folder size={16} className="text-sidebar-foreground" />
        <input
          autoFocus
          type="text"
          value={inlineEdit.value}
          onChange={(e) =>
            setInlineEdit({ ...inlineEdit, value: e.target.value } as InlineEdit)
          }
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              commitInline();
            } else if (e.key === 'Escape') {
              e.preventDefault();
              cancelInline();
            }
          }}
          onBlur={commitInline}
          placeholder="文件夹名称"
          className="flex-1 min-w-0 bg-popover text-foreground px-2 py-1 rounded-full text-sm outline-none border border-input focus:border-input"
        />
      </div>
    );
  }

  function renderFolder(f: LibraryFolder, depth: number) {
    const isOpen = expanded.has(f.id);
    const isSelected = selectedFolderId === f.id;
    const children = childMap.get(f.id) ?? [];
    const itemsHere = isOpen ? itemsByFolder.get(f.id) ?? [] : [];
    const isRenaming = inlineEdit?.kind === 'rename' && inlineEdit.folderId === f.id;
    const isCreatingChildHere =
      inlineEdit?.kind === 'create-child' && inlineEdit.folderId === f.id;

    const menuItems = [
      onCreateChild && {
        key: 'new-child',
        icon: <FolderPlus size={14} />,
        label: '新建文件夹',
        onClick: ({ domEvent }: { domEvent?: any }) => {
          domEvent?.stopPropagation?.();
          setExpanded((prev) => {
            const next = new Set(prev);
            next.add(f.id);
            return next;
          });
          setInlineEdit({ kind: 'create-child', folderId: f.id, value: '' });
        },
      },
      onRenameFolder && {
        key: 'rename',
        icon: <Pencil size={14} />,
        label: '重命名',
        onClick: ({ domEvent }: { domEvent?: any }) => {
          domEvent?.stopPropagation?.();
          setInlineEdit({ kind: 'rename', folderId: f.id, value: f.name });
        },
      },
      onMoveFolder && {
        key: 'move',
        icon: <FolderInput size={14} />,
        label: '移动到...',
        onClick: ({ domEvent }: { domEvent?: any }) => {
          domEvent?.stopPropagation?.();
          onMoveFolder(f);
        },
      },
      onDeleteFolder && { type: 'divider' as const },
      onDeleteFolder && {
        key: 'delete',
        icon: <Trash2 size={14} />,
        label: '删除',
        danger: true,
        onClick: ({ domEvent }: { domEvent?: any }) => {
          domEvent?.stopPropagation?.();
          onDeleteFolder(f);
        },
      },
    ].filter(Boolean) as any[];

    return (
      <div key={f.id}>
        {isRenaming ? (
          <div
            className="flex items-center gap-1 px-2 py-1.5"
            style={{ paddingLeft: 8 + depth * 16 }}
          >
            {isOpen ? (
              <ChevronDown size={14} className="text-muted-foreground" />
            ) : (
              <ChevronRight size={14} className="text-muted-foreground" />
            )}
            {isOpen ? (
              <FolderOpen size={16} className="text-sidebar-foreground" />
            ) : (
              <Folder size={16} className="text-sidebar-foreground" />
            )}
            <input
              autoFocus
              value={inlineEdit!.value}
              onChange={(e) =>
                setInlineEdit({ ...inlineEdit!, value: e.target.value } as InlineEdit)
              }
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  commitInline();
                } else if (e.key === 'Escape') {
                  e.preventDefault();
                  cancelInline();
                }
              }}
              onBlur={commitInline}
              onClick={(e) => e.stopPropagation()}
              className="flex-1 min-w-0 bg-popover text-foreground px-2 py-0.5 rounded text-sm outline-none border border-input focus:border-input"
            />
          </div>
        ) : (
          <div
            className={`group flex items-center gap-1 px-2 py-1.5 cursor-pointer hover:bg-popover ${
              isSelected ? 'bg-popover' : ''
            }`}
            style={{ paddingLeft: 8 + depth * 16 }}
            onClick={() => {
              toggle(f.id);
              onSelectFolder(f.id);
            }}
          >
            {isOpen ? (
              <ChevronDown size={14} className="text-muted-foreground" />
            ) : (
              <ChevronRight size={14} className="text-muted-foreground" />
            )}
            {isOpen ? (
              <FolderOpen size={16} className="text-sidebar-foreground" />
            ) : (
              <Folder size={16} className="text-sidebar-foreground" />
            )}
            <span className="text-sm text-foreground truncate flex-1 min-w-0">{f.name}</span>
                        {f.itemCount > 0 && (
              <span className="text-xs text-muted-foreground mr-1 group-hover:opacity-0 transition-opacity">
                {f.itemCount}
              </span>
            )}
            {menuItems.length > 0 && (
              <Dropdown
                menu={{ items: menuItems }}
                trigger={['click']}
                placement="bottomRight"
              >
                <button
                  onClick={(e) => e.stopPropagation()}
                  className="opacity-0 group-hover:opacity-100 p-1 text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer"
                  title="更多操作"
                >
                  <MoreHorizontal size={14} />
                </button>
              </Dropdown>
            )}
          </div>
        )}

        {isOpen && (
          <div>
            {children.map((c) => renderFolder(c, depth + 1))}
            {isCreatingChildHere && renderInlineCreateRow(depth + 1)}
            {!isCreatingChildHere &&
              itemsHere.length === 0 &&
              children.length === 0 && (
                <div
                  className="text-xs text-muted-foreground italic"
                  style={{
                    paddingLeft: 8 + (depth + 1) * 16,
                    paddingTop: 6,
                    paddingBottom: 6,
                  }}
                >
                  该文件夹暂无素材
                </div>
              )}
            {itemsHere.map((it) => {
              const isRenamingItem =
                inlineEdit?.kind === 'rename-item' && inlineEdit.itemId === it.id;
              const itemMenuItems = [
                onRenameItem && {
                  key: 'rename',
                  icon: <Pencil size={14} />,
                  label: '重命名',
                  onClick: ({ domEvent }: { domEvent?: any }) => {
                    domEvent?.stopPropagation?.();
                    setInlineEdit({ kind: 'rename-item', itemId: it.id, value: it.name });
                  },
                },
                onMoveItem && {
                  key: 'move',
                  icon: <FolderInput size={14} />,
                  label: '移动到...',
                  onClick: ({ domEvent }: { domEvent?: any }) => {
                    domEvent?.stopPropagation?.();
                    onMoveItem(it);
                  },
                },
                onDeleteItem && { type: 'divider' as const },
                onDeleteItem && {
                  key: 'delete',
                  icon: <Trash2 size={14} />,
                  label: '删除',
                  danger: true,
                  onClick: ({ domEvent }: { domEvent?: any }) => {
                    domEvent?.stopPropagation?.();
                    onDeleteItem(it);
                  },
                },
              ].filter(Boolean) as any[];

              return isRenamingItem ? (
                <div
                  key={it.id}
                  className="flex items-center gap-2 px-2 py-1.5"
                  style={{ paddingLeft: 8 + (depth + 1) * 16 }}
                >
                  {it.kind === 'image' && <ImageIcon size={14} className="text-muted-foreground" />}
                  {it.kind === 'video' && <Video size={14} className="text-muted-foreground" />}
                  {it.kind === 'audio' && <Music size={14} className="text-muted-foreground" />}
                  <input
                    autoFocus
                    value={inlineEdit.value}
                    onChange={(e) =>
                      setInlineEdit({ ...inlineEdit, value: e.target.value } as InlineEdit)
                    }
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault();
                        commitInline();
                      } else if (e.key === 'Escape') {
                        e.preventDefault();
                        cancelInline();
                      }
                    }}
                    onBlur={commitInline}
                    onClick={(e) => e.stopPropagation()}
                    className="flex-1 min-w-0 bg-popover text-foreground px-2 py-0.5 rounded text-sm outline-none border border-input focus:border-input"
                  />
                </div>
              ) : (
                <div
                  key={it.id}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData(
                      'application/x-tvmao-library-item-id',
                      String(it.id),
                    );
                    e.dataTransfer.effectAllowed = 'copy';
                  }}
                  onDoubleClick={() => onSelectItem(it)}
                  onClick={() => onSelectItem(it)}
                  onMouseEnter={(e) =>
                    onHoverItem?.(
                      it,
                      (e.currentTarget as HTMLElement).getBoundingClientRect(),
                    )
                  }
                  onMouseLeave={() => onHoverItem?.(null)}
                  className="group flex items-center gap-2 px-2 py-1.5 cursor-pointer hover:bg-popover"
                  style={{ paddingLeft: 8 + (depth + 1) * 16 }}
                >
                  {it.kind === 'image' &&
                    (it.url ? (
                      <img src={it.url} crossOrigin={mediaCrossOrigin(it.url)} alt="" className="w-6 h-6 rounded object-cover" />
                    ) : (
                      <ImageIcon size={14} className="text-muted-foreground" />
                    ))}
                  {it.kind === 'video' && <Video size={14} className="text-muted-foreground" />}
                  {it.kind === 'audio' && <Music size={14} className="text-muted-foreground" />}
                  <span className="text-sm text-sidebar-foreground truncate flex-1 min-w-0">
                    {it.name || `item-${it.id}`}
                  </span>
                  {itemMenuItems.length > 0 && (
                    <Dropdown
                      menu={{ items: itemMenuItems }}
                      trigger={['click']}
                      placement="bottomRight"
                    >
                      <button
                        onClick={(e) => e.stopPropagation()}
                        className="opacity-0 group-hover:opacity-100 p-1 text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer"
                        title="更多操作"
                      >
                        <MoreHorizontal size={14} />
                      </button>
                    </Dropdown>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    );
  }

  const roots = childMap.get(null) ?? [];
  if (roots.length === 0) {
    return (
      <div className="px-3 py-6 text-center text-xs text-muted-foreground">
        还没有文件夹。点右上角 + 新建文件夹。
      </div>
    );
  }
  return <div className="py-1">{roots.map((f) => renderFolder(f, 0))}</div>;
}
