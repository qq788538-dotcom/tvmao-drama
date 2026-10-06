import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FolderTree } from './FolderTree';
import type { LibraryFolder, LibraryItem } from './types';

const folderA: LibraryFolder = {
  id: 1, name: '普通目录A', itemCount: 1,
  createdAt: '', updatedAt: '',
};
const normalFolder: LibraryFolder = {
  id: 2, name: '普通', itemCount: 0, createdAt: '', updatedAt: '',
};
const sampleItem: LibraryItem = {
  id: 10, folderId: 1, url: '', sizeBytes: 1, mimeType: 'image/png', kind: 'image',
  source: 'upload', name: 'p1', createdAt: '',
};

function renderTree(
  items: LibraryItem[] = [],
  itemCallbacks: {
    onRenameItem?: (item: LibraryItem, name: string) => void;
    onMoveItem?: (item: LibraryItem) => void;
    onDeleteItem?: (item: LibraryItem) => void;
  } = {},
) {
  return render(
    <FolderTree
      folders={[folderA, normalFolder]}
      items={items}
      selectedFolderId={null}
      onSelectFolder={() => {}}
      onSelectItem={() => {}}
      onCreateChild={vi.fn()}
      onRenameFolder={vi.fn()}
      onMoveFolder={vi.fn()}
      onDeleteFolder={vi.fn()}
      {...itemCallbacks}
    />,
  );
}

describe('FolderTree', () => {

  it('文件菜单提供三项操作，并可用行内输入框重命名', async () => {
    const onRenameItem = vi.fn();
    renderTree([sampleItem], {
      onRenameItem,
      onMoveItem: vi.fn(),
      onDeleteItem: vi.fn(),
    });

    fireEvent.click(screen.getByText('普通目录A'));
    const itemRow = screen.getByText('p1').closest('[draggable="true"]');
    expect(itemRow).not.toBeNull();
    const itemMenuButton = within(itemRow as HTMLElement).getByTitle('更多操作');
    fireEvent.click(itemMenuButton);

    expect(await screen.findByText('重命名')).toBeTruthy();
    expect(screen.getByText('移动到...')).toBeTruthy();
    expect(screen.getByText('删除')).toBeTruthy();

    fireEvent.click(screen.getByText('重命名'));
    const input = screen.getByDisplayValue('p1');
    fireEvent.change(input, { target: { value: '新文件名' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onRenameItem).toHaveBeenCalledWith(sampleItem, '新文件名');
  });
});
