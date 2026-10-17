// 「故事板详情面板里用原图、不弹 Lightbox；画布上照旧」这条规则横跨多个
// 节点组件，所以集中测在一处——分散到各节点的测试文件里，下次再加一种媒体
// 节点时没人会想起来还有这条规则要遵守。
//
// 曾经就是这么漏的：先只改了 GenImageNode，用户在 PreviewNode 上又撞见一次。
import { fireEvent, render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PreviewNode } from '../editor/nodes/PreviewNode';
import { InputImageNode } from '../editor/nodes/InputImageNode';
import { StoryboardModeContext } from './StoryboardContext';
import { useCanvasStore } from '../editor/store';
import type { CanvasNode } from '../editor/types';

const imageURL = 'https://example.test/original.png';
const thumbURL = `${imageURL}?x-tos-process=w400`;

// useThumbUrl 返回缩略图；故事板态下组件应当绕开它直接用原图。
vi.mock('../editor/thumbs', () => ({
  useThumbUrl: (url?: string | null) => (url ? `${url}?x-tos-process=w400` : undefined),
}));

function seed(node: Partial<CanvasNode> & { id: string; type: CanvasNode['type'] }) {
  useCanvasStore.setState({
    nodes: [{
      x: 0, y: 0, width: 280, height: 280, status: 'succeeded', settings: {},
      ...node,
    } as CanvasNode],
    connections: [],
    selectedIds: new Set<string>(),
    lightbox: null,
  });
}

function renderIn(storyboard: boolean, el: React.ReactElement) {
  // 这些节点内部用 react-query 拉模型/预估，没有 Provider 会直接抛。
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const inner = storyboard
    ? <StoryboardModeContext.Provider value={true}>{el}</StoryboardModeContext.Provider>
    : el;
  return render(<QueryClientProvider client={queryClient}>{inner}</QueryClientProvider>);
}

describe('PreviewNode 在故事板详情面板里', () => {
  beforeEach(() => {
    seed({ id: 'p1', type: 'preview', content: imageURL, settings: { kind: 'image' } });
  });

  it('用原图而不是缩略图', async () => {
    const { container } = renderIn(true, <PreviewNode id="p1" />);
    await waitFor(() => {
      expect(container.querySelector(`img[src="${imageURL}"]`)).not.toBeNull();
    });
    expect(container.querySelector(`img[src="${thumbURL}"]`)).toBeNull();
  });

  it('双击不再弹 Lightbox', async () => {
    const { container } = renderIn(true, <PreviewNode id="p1" />);
    const img = await waitFor(() => {
      const el = container.querySelector(`img[src="${imageURL}"]`);
      expect(el).not.toBeNull();
      return el!;
    });

    fireEvent.doubleClick(img.parentElement as HTMLElement);
    expect(useCanvasStore.getState().lightbox).toBeNull();
  });

  it('画布模式不受影响：仍用缩略图，双击仍弹 Lightbox', async () => {
    const { container } = renderIn(false, <PreviewNode id="p1" />);
    const img = await waitFor(() => {
      const el = container.querySelector(`img[src="${thumbURL}"]`);
      expect(el).not.toBeNull();
      return el!;
    });

    fireEvent.doubleClick(img.parentElement as HTMLElement);
    expect(useCanvasStore.getState().lightbox).toMatchObject({ url: imageURL, kind: 'image' });
  });

  // hover 时浮出的放大图标只是在暗示「双击可放大」。双击已经拿掉了，图标留着
  // 就是骗人——用户照着提示双击，什么都不会发生。
  it('不再显示 hover 放大图标（那是双击可放大的暗示，而双击已移除）', async () => {
    const { container } = renderIn(true, <PreviewNode id="p1" />);
    await waitFor(() => {
      expect(container.querySelector(`img[src="${imageURL}"]`)).not.toBeNull();
    });
    expect(container.querySelector('.group-hover\\:bg-black\\/30')).toBeNull();
  });

  it('画布模式仍有放大图标——那里双击确实能放大', async () => {
    const { container } = renderIn(false, <PreviewNode id="p1" />);
    await waitFor(() => {
      expect(container.querySelector(`img[src="${thumbURL}"]`)).not.toBeNull();
    });
    expect(container.querySelector('.group-hover\\:bg-black\\/30')).not.toBeNull();
  });
});

describe('InputImageNode 在故事板详情面板里', () => {
  beforeEach(() => {
    seed({ id: 'i1', type: 'input-image', content: imageURL });
  });

  it('用原图而不是缩略图，且双击不弹 Lightbox', async () => {
    const { container } = renderIn(true, <InputImageNode id="i1" />);
    const img = await waitFor(() => {
      const el = container.querySelector(`img[src="${imageURL}"]`);
      expect(el).not.toBeNull();
      return el!;
    });
    expect(container.querySelector(`img[src="${thumbURL}"]`)).toBeNull();

    fireEvent.doubleClick(img.parentElement as HTMLElement);
    expect(useCanvasStore.getState().lightbox).toBeNull();
  });

  it('画布模式不受影响', async () => {
    const { container } = renderIn(false, <InputImageNode id="i1" />);
    const img = await waitFor(() => {
      const el = container.querySelector(`img[src="${thumbURL}"]`);
      expect(el).not.toBeNull();
      return el!;
    });

    fireEvent.doubleClick(img.parentElement as HTMLElement);
    expect(useCanvasStore.getState().lightbox).toMatchObject({ url: imageURL, kind: 'image' });
  });
});

// 视频类（VideoInputNode / preview kind=video）没有「缩略图 vs 原图」这回事——
// 直接放 content，改的只是「详情面板里不再双击弹 Lightbox」。这里没给它独立
// 用例：VideoInputNode 的播放区要等 loadedmetadata 拿到 videoMeta 才渲染，
// jsdom 不解码视频、那个事件永远不来，测出来只会是个永真的空壳。
// 它的行为与上面两个图片节点是同一处改动（storyboardMode ? undefined : onDoubleClick），
// 由图片类的用例覆盖同一条规则。
