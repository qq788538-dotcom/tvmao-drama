import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { useCanvasStore } from '../store';

// 合规校验状态走合批器；image kind 会触发这条查询，这里 stub 掉避免真实网络调用。
const loadSeedanceStatusMock = vi.fn().mockResolvedValue(undefined);
vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: (...args: unknown[]) => loadSeedanceStatusMock(...args),
}));

function Probe({
  url,
  kind,
  nodeId,
}: {
  url?: string;
  kind: 'image' | 'video' | 'audio';
  nodeId: string;
}) {
  const menu = useMediaNodeMenu({ url, kind, nodeId });
  return (
    <div data-testid="probe" onContextMenu={menu.onContextMenu}>
      probe
      {menu.overlay}
    </div>
  );
}

function renderProbe(props: { url?: string; kind: 'image' | 'video' | 'audio'; nodeId: string }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <Probe {...props} />
    </QueryClientProvider>,
  );
}

function openMenu() {
  fireEvent.contextMenu(screen.getByTestId('probe'), { clientX: 10, clientY: 10 });
}

// jsdom 不真的加载媒体，所以测试要自己拿到 useMediaDuration 创建的那个
// video/audio 元素，喂一个 duration 再派发 loadedmetadata。
function spyMediaCreation(collected: HTMLMediaElement[]) {
  const realCreate = document.createElement.bind(document);
  return vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
    const el = realCreate(tag as keyof HTMLElementTagNameMap);
    if (tag === 'video' || tag === 'audio') collected.push(el as HTMLMediaElement);
    return el;
  });
}

// 「擦除字幕」「视频转高清」两个入口 2026-08-14 迁到了 VideoActionsBar 工具条
// （spawn 行为测试也在 VideoActionsBar.test.tsx），右键菜单里不该再出现。
describe('useMediaNodeMenu 后处理入口已迁走', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('video kind 的菜单不含「擦除字幕」「视频转高清」', () => {
    renderProbe({ url: 'http://x.mp4', kind: 'video', nodeId: 'src-1' });
    openMenu();
    expect(screen.queryByText('擦除字幕')).toBeNull();
    expect(screen.queryByText('视频转高清')).toBeNull();
  });
});

describe('useMediaNodeMenu 合规校验（图片/视频/音频）', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('三类素材都能提交合规校验', () => {
    for (const kind of ['image', 'video', 'audio'] as const) {
      renderProbe({ url: `http://x.${kind}`, kind, nodeId: 'n-1' });
      openMenu();
      expect(screen.getAllByText('Seedance2.0 合规校验').length).toBeGreaterThan(0);
      cleanup();
    }
  });

  it('时长在 2~30s 之外的视频不能点击校验，并说明原因', async () => {
    // jsdom 不真的加载媒体，所以手动喂一个 duration 再派发 loadedmetadata——
    // 这正是 useMediaDuration 依赖的那条路径。
    const created: HTMLMediaElement[] = [];
    const spy = spyMediaCreation(created);

    renderProbe({ url: 'http://x.mp4', kind: 'video', nodeId: 'n-1' });
    expect(created.length).toBe(1);
    Object.defineProperty(created[0], 'duration', { value: 35, configurable: true });
    await act(async () => {
      created[0].dispatchEvent(new Event('loadedmetadata'));
    });
    spy.mockRestore();

    openMenu();
    const item = screen.getByText('Seedance2.0 合规校验').closest('button')!;
    expect(item).toBeDisabled();
    expect(item.getAttribute('title')).toContain('35.0s');
  });

  it('时长合规的视频可以点击', async () => {
    const created: HTMLMediaElement[] = [];
    const spy = spyMediaCreation(created);

    renderProbe({ url: 'http://x.mp4', kind: 'video', nodeId: 'n-1' });
    Object.defineProperty(created[0], 'duration', { value: 8, configurable: true });
    await act(async () => {
      created[0].dispatchEvent(new Event('loadedmetadata'));
    });
    spy.mockRestore();

    openMenu();
    expect(screen.getByText('Seedance2.0 合规校验').closest('button')!).not.toBeDisabled();
  });

  // 已 active 的素材（自己校验过的，或公共角色库那种天生 active 的）再提交
  // 只是空转甚至 403，所以置灰并说明。版本轮换的重新校验由后台 worker 做。
  it('已通过校验的素材不能再点，hint 说明无需重复提交', async () => {
    loadSeedanceStatusMock.mockResolvedValueOnce({
      objectKey: 'k',
      status: 'active',
      assetId: 'asset-x',
    });
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    // 等状态查询落地后再开菜单
    await act(async () => {});
    openMenu();
    const item = screen.getByText('Seedance2.0 合规校验').closest('button')!;
    expect(item).toBeDisabled();
    expect(item.getAttribute('title')).toContain('已通过校验');
  });

  it('? 悬浮显示对应类型的要求条件', async () => {
    renderProbe({ url: 'http://x.mp3', kind: 'audio', nodeId: 'n-1' });
    openMenu();
    fireEvent.mouseEnter(screen.getByLabelText('查看「Seedance2.0 合规校验」说明'));
    expect(screen.getByText('待校验音频要求')).toBeInTheDocument();
    expect(screen.getByText('2 ~ 15 秒')).toBeInTheDocument();
  });
});

describe('useMediaNodeMenu 「删除」', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-1',
          type: 'gen-image',
          x: 0,
          y: 0,
          width: 280,
          height: 280,
          status: 'succeeded',
          content: 'http://x.png',
          settings: {},
        },
      ],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  it('「删除」是菜单最后一项', () => {
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    const del = screen.getByText('删除');
    const menu = del.closest('.canvas-menu-enter')!;
    const buttons = menu.querySelectorAll('button');
    expect(buttons[buttons.length - 1].textContent).toBe('删除');
  });

  it('「Seedance2.0 合规校验」固定是菜单首项（用户拍板）', () => {
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    const menu = screen.getByText('删除').closest('.canvas-menu-enter')!;
    const buttons = menu.querySelectorAll('button');
    expect(buttons[0].textContent).toContain('Seedance2.0 合规校验');
  });

  it('「删除」标红警示', () => {
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    expect(screen.getByText('删除').closest('button')!.className).toContain('text-red-400');
  });

  it('点击「删除」把节点从画布移除', async () => {
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    await userEvent.click(screen.getByText('删除'));
    expect(useCanvasStore.getState().nodes.find((n) => n.id === 'n-1')).toBeUndefined();
  });
});
