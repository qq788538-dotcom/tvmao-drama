// 视频工具条右侧两个查看类入口的行为约定（镜像 ImageActionsBar.viewactions）：
// 「下载」= 隐藏 <a download> 触发浏览器下载（不建节点）；
// 「放大查看」= 打开全局 Lightbox（kind=video，与双击节点视频同一个弹框）。
// 取 URL 都用宿主传入的 url prop——三个宿主各自已解析好当前视频。
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { VideoActionsBar } from './VideoActionsBar';
import { useCanvasStore } from '../store';

// 理由同 VideoActionsBar.test.tsx：trim 条挂载后会真去 mediabunny 抽帧。
vi.mock('./trim/VideoTrimBar', () => ({
  VideoTrimBar: () => <div data-testid="trim-bar" />,
}));

function setStore() {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'src-1',
        type: 'gen-video' as const,
        x: 100,
        y: 200,
        width: 320,
        height: 200,
        status: 'succeeded' as const,
        content: 'https://example.test/clip.mp4',
        settings: {},
      },
    ],
    connections: [],
    selectedIds: new Set(['src-1']),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
    trimmingNodeId: null,
    lightbox: null,
  });
}

function renderBar(url?: string) {
  // 片段重拍配置走 useQuery,包 QueryClientProvider(同 ImageActionsBar 测试)。
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <VideoActionsBar nodeId="src-1" visible url={url} />
      </AntApp>
    </QueryClientProvider>,
  );
}

describe('VideoActionsBar 下载/放大查看', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setStore();
  });

  it('下载：用宿主传入的视频 URL 触发 <a download> 点击', async () => {
    // jsdom 里真点 <a href> 会报 not-implemented 的 navigation，拦掉 click 本身。
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {});
    renderBar('https://example.test/clip.mp4');
    await userEvent.click(await screen.findByRole('button', { name: '下载' }));

    expect(clickSpy).toHaveBeenCalledTimes(1);
    const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement;
    expect(anchor.href).toBe('https://example.test/clip.mp4');
    expect(anchor).toHaveAttribute('download');
    // 不产生任何节点/连线副作用。
    expect(useCanvasStore.getState().nodes).toHaveLength(1);
    expect(useCanvasStore.getState().connections).toHaveLength(0);
  });

  it('放大查看：打开全局 Lightbox，kind=video、title 按节点类型取', async () => {
    renderBar('https://example.test/clip.mp4');
    await userEvent.click(await screen.findByRole('button', { name: '放大查看' }));

    expect(useCanvasStore.getState().lightbox).toMatchObject({
      url: 'https://example.test/clip.mp4',
      kind: 'video',
      title: 'AI 视频',
    });
  });

  it('没有视频 URL 时两个按钮都只提示，不下载不开弹框', async () => {
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => {});
    renderBar(undefined);
    await userEvent.click(await screen.findByRole('button', { name: '下载' }));
    await userEvent.click(await screen.findByRole('button', { name: '放大查看' }));

    expect(await screen.findByText('当前节点没有可下载的视频')).toBeInTheDocument();
    expect(await screen.findByText('当前节点没有可预览的视频')).toBeInTheDocument();
    expect(clickSpy).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().lightbox).toBeNull();
  });
});
