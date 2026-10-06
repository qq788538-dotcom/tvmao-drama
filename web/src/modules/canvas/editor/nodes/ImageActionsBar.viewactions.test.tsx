// 工具条右侧两个查看类入口的行为约定：「下载」= 隐藏 <a download> 触发浏览器
// 下载（不建节点、不扣积分）；「放大查看」= 打开全局 Lightbox（与双击节点图片
// 同一个弹框）。两者取图都是 content 优先、上游引用态退回连线图。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ImageActionsBar } from './ImageActionsBar';
import { useCanvasStore } from '../store';

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    fetchGenImagePresets: vi.fn().mockResolvedValue({ models: [], presets: [] }),
  };
});

vi.mock('../persistence/canvasMutations', () => ({
  persistNodeById: vi.fn(),
  scheduleNodePatch: vi.fn(),
  flushNodePatch: vi.fn(),
}));

function setStore(content: string) {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'input-image' as const,
        x: 100,
        y: 200,
        width: 300,
        height: 240,
        status: 'succeeded' as const,
        content,
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
    lightbox: null,
  });
}

function renderBar() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <ImageActionsBar nodeId="src-1" visible />
      </AntApp>
    </QueryClientProvider>,
  );
}

describe('ImageActionsBar 下载/放大查看', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setStore('https://example.test/ref.png');
  });

  it('下载：用节点图片 URL 触发 <a download> 点击', async () => {
    // jsdom 里真点 <a href> 会报 not-implemented 的 navigation，拦掉 click 本身。
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {});
    renderBar();
    await userEvent.click(await screen.findByRole('button', { name: '下载' }));

    expect(clickSpy).toHaveBeenCalledTimes(1);
    const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement;
    expect(anchor.href).toBe('https://example.test/ref.png');
    expect(anchor).toHaveAttribute('download');
    // 不产生任何节点/连线副作用。
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('放大查看：打开全局 Lightbox，kind=image', async () => {
    renderBar();
    await userEvent.click(await screen.findByRole('button', { name: '放大查看' }));

    expect(useCanvasStore.getState().lightbox).toMatchObject({
      url: 'https://example.test/ref.png',
      kind: 'image',
    });
  });

  it('没有图片时两个按钮都只提示，不下载不开弹框', async () => {
    setStore('');
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {});
    renderBar();
    await userEvent.click(await screen.findByRole('button', { name: '下载' }));
    await userEvent.click(await screen.findByRole('button', { name: '放大查看' }));

    expect(await screen.findByText('当前节点没有可下载的图片')).toBeInTheDocument();
    expect(await screen.findByText('当前节点没有可预览的图片')).toBeInTheDocument();
    expect(clickSpy).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().lightbox).toBeNull();
  });
});
