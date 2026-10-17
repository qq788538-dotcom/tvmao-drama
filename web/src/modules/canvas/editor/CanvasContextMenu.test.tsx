import { App as AntApp } from 'antd';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasContextMenu } from './CanvasContextMenu';
import { useCanvasStore } from './store';
import type { CanvasSnapshot } from './types';

// thumbs.ts 等间接依赖会碰 api——stub 掉，jsdom 里不发真实请求。
vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    uploadAsset: vi.fn(),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});

const emptySnapshot: CanvasSnapshot = {
  nodes: [],
  connections: [],
  view: { x: 0, y: 0, zoom: 1 },
};

function resetStore(partial?: Partial<Parameters<typeof useCanvasStore.setState>[0]>) {
  useCanvasStore.setState({
    nodes: [],
    connections: [],
    selectedIds: new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    readOnly: false,
    undoStack: [],
    redoStack: [],
    ...partial,
  });
}

function renderMenu(overrides?: Partial<React.ComponentProps<typeof CanvasContextMenu>>) {
  const props = {
    x: 10,
    y: 10,
    worldX: 100,
    worldY: 200,
    onAddNode: vi.fn(),
    onPasteNodes: vi.fn(),
    onPasteFiles: vi.fn(),
    onPasteText: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  const utils = render(
    <AntApp>
      <CanvasContextMenu {...props} />
    </AntApp>,
  );
  return { ...utils, props };
}

function setPlatform(value: string) {
  Object.defineProperty(window.navigator, 'platform', { value, configurable: true });
}

describe('CanvasContextMenu（画布右键菜单）', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetStore();
    setPlatform('');
    // 节点剪贴板镜像存 localStorage——不清的话上一条用例复制的节点会让
    // 「粘贴走图片/文字分支」的用例翻车（镜像优先级最高）。
    localStorage.clear();
  });

  it('五个菜单项齐全，且没有「保存到我的资产」', () => {
    renderMenu();
    for (const label of ['上传', '添加节点', '撤销', '重做', '粘贴']) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    expect(screen.queryByText('保存到我的资产')).toBeNull();
  });

  it('撤销/重做按栈长置灰；点击调 store 的 undo/redo 并收菜单', () => {
    const undo = vi.fn();
    const redo = vi.fn();
    // undoStack 有货、redoStack 为空 → 撤销可点、重做置灰。
    useCanvasStore.setState({ undoStack: [emptySnapshot], redoStack: [], undo, redo });
    const { props } = renderMenu();

    const undoBtn = screen.getByText('撤销').closest('button')!;
    const redoBtn = screen.getByText('重做').closest('button')!;
    expect(undoBtn.disabled).toBe(false);
    expect(redoBtn.disabled).toBe(true);

    fireEvent.click(undoBtn);
    expect(undo).toHaveBeenCalledTimes(1);
    expect(props.onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(redoBtn);
    expect(redo).not.toHaveBeenCalled();
  });

  it('点「添加节点」交给宿主原位切菜单，自己不关闭（宿主换 kind 即卸载本菜单）', () => {
    const { props } = renderMenu();
    fireEvent.click(screen.getByText('添加节点'));
    expect(props.onAddNode).toHaveBeenCalledTimes(1);
  });

  it('只读模式下上传/粘贴置灰', () => {
    resetStore({ readOnly: true });
    renderMenu();
    expect(screen.getByText('上传').closest('button')!.disabled).toBe(true);
    expect(screen.getByText('粘贴').closest('button')!.disabled).toBe(true);
  });

  it('快捷键文案：非 Mac 平台显示 Ctrl 系', () => {
    setPlatform('Win32');
    renderMenu();
    expect(screen.getByText('Ctrl+Z')).toBeTruthy();
    expect(screen.getByText('Ctrl+Y')).toBeTruthy();
    expect(screen.getByText('Ctrl+V')).toBeTruthy();
  });

  it('快捷键文案：Mac 平台显示 ⌘ 系', () => {
    setPlatform('MacIntel');
    renderMenu();
    expect(screen.getByText('⌘Z')).toBeTruthy();
    expect(screen.getByText('⇧⌘Z')).toBeTruthy();
    expect(screen.getByText('⌘V')).toBeTruthy();
  });

  it('粘贴：剪贴板有图 → 把 File 交给 onPasteFiles', async () => {
    const blob = new Blob(['fake-png'], { type: 'image/png' });
    Object.defineProperty(window.navigator, 'clipboard', {
      value: {
        read: vi.fn().mockResolvedValue([
          { types: ['text/plain', 'image/png'], getType: vi.fn().mockResolvedValue(blob) },
        ]),
      },
      configurable: true,
    });
    const onPasteFiles = vi.fn();
    const { props } = renderMenu({ onPasteFiles });
    await act(async () => {
      fireEvent.click(screen.getByText('粘贴'));
    });
    expect(props.onClose).toHaveBeenCalled();
    expect(onPasteFiles).toHaveBeenCalledTimes(1);
    const files = onPasteFiles.mock.calls[0][0] as File[];
    expect(files).toHaveLength(1);
    expect(files[0].type).toBe('image/png');
  });

  it('粘贴：镜像里有「复制节点」payload → 最高优先，交给 onPasteNodes', async () => {
    localStorage.setItem(
      'tvmao:canvas-node-clipboard',
      JSON.stringify({
        format: 'tvmao-canvas-nodes',
        version: 1,
        sourceProjectId: 7,
        nodes: [
          { id: 'n-a', type: 'input-image', x: 0, y: 0, width: 260, height: 260, settings: {} },
        ],
        connections: [],
      }),
    );
    const { props } = renderMenu();
    await act(async () => {
      fireEvent.click(screen.getByText('粘贴'));
    });
    expect(props.onPasteNodes).toHaveBeenCalledTimes(1);
    expect(props.onPasteFiles).not.toHaveBeenCalled();
  });

  it('粘贴：没图但有文字 → 交给 onPasteText 建文字节点', async () => {
    Object.defineProperty(window.navigator, 'clipboard', {
      value: {
        read: vi.fn().mockResolvedValue([{ types: ['text/plain'], getType: vi.fn() }]),
        readText: vi.fn().mockResolvedValue('一段剪贴板文字'),
      },
      configurable: true,
    });
    const { props } = renderMenu();
    await act(async () => {
      fireEvent.click(screen.getByText('粘贴'));
    });
    expect(props.onPasteText).toHaveBeenCalledWith('一段剪贴板文字');
    expect(props.onPasteFiles).not.toHaveBeenCalled();
  });

  it('粘贴：图和文字都没有 → 不建节点，提示无可粘贴内容', async () => {
    Object.defineProperty(window.navigator, 'clipboard', {
      value: {
        read: vi.fn().mockResolvedValue([{ types: ['text/plain'], getType: vi.fn() }]),
        readText: vi.fn().mockResolvedValue(''),
      },
      configurable: true,
    });
    const { props } = renderMenu();
    await act(async () => {
      fireEvent.click(screen.getByText('粘贴'));
    });
    expect(props.onPasteFiles).not.toHaveBeenCalled();
    expect(props.onPasteText).not.toHaveBeenCalled();
    expect(await screen.findByText('剪贴板中没有可粘贴的内容')).toBeTruthy();
  });

  it('粘贴：clipboard.read 被拒 → 提示未授权', async () => {
    Object.defineProperty(window.navigator, 'clipboard', {
      value: { read: vi.fn().mockRejectedValue(new DOMException('denied', 'NotAllowedError')) },
      configurable: true,
    });
    const { props } = renderMenu();
    await act(async () => {
      fireEvent.click(screen.getByText('粘贴'));
    });
    expect(props.onPasteFiles).not.toHaveBeenCalled();
    expect(await screen.findByText('浏览器未授权读取剪贴板')).toBeTruthy();
  });
});
