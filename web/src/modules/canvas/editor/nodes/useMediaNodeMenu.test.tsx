import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useMediaNodeMenu } from './useMediaNodeMenu';
import { useCanvasStore } from '../store';
import { App as AntApp } from 'antd';

// 合规校验状态走合批器；image kind 会触发这条查询，这里 stub 掉避免真实网络调用。
const loadSeedanceStatusMock = vi.fn().mockResolvedValue(undefined);
vi.mock('../batchQueries', () => ({
  loadSeedanceStatus: (...args: unknown[]) => loadSeedanceStatusMock(...args),
}));

// 「在访达中显示」最终由 sidecar 打开文件夹；这里只验证菜单把哪个地址交了出去。
const revealMock = vi.fn();
vi.mock('@/desktop/localMedia', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/desktop/localMedia')>()),
  revealInFileManager: (...args: unknown[]) => revealMock(...args),
}));

function setPlatform(value: string) {
  Object.defineProperty(window.navigator, 'platform', { value, configurable: true });
}

function renderProbeInApp(props: { url?: string; kind: 'image' | 'video' | 'audio'; nodeId: string }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <Probe {...props} />
      </AntApp>
    </QueryClientProvider>,
  );
}

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
      {menu.badge}
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
      expect(screen.getAllByText('Seedance 合规校验').length).toBeGreaterThan(0);
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
    const item = screen.getByText('Seedance 合规校验').closest('button')!;
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
    expect(screen.getByText('Seedance 合规校验').closest('button')!).not.toBeDisabled();
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
    const item = screen.getByText('Seedance 合规校验').closest('button')!;
    expect(item).toBeDisabled();
    expect(item.getAttribute('title')).toContain('已通过校验');
  });

  it('? 悬浮显示对应类型的要求条件', async () => {
    renderProbe({ url: 'http://x.mp3', kind: 'audio', nodeId: 'n-1' });
    openMenu();
    fireEvent.mouseEnter(screen.getByLabelText('查看「Seedance 合规校验」说明'));
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

  it('「Seedance 合规校验」固定是菜单首项（用户拍板）', () => {
    renderProbe({ url: 'http://x.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    const menu = screen.getByText('删除').closest('.canvas-menu-enter')!;
    const buttons = menu.querySelectorAll('button');
    expect(buttons[0].textContent).toContain('Seedance 合规校验');
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

describe('useMediaNodeMenu 在访达中显示（S3）', () => {
  const LOCAL = 'http://127.0.0.1:5123/media/u/1/a.png?token=t';

  beforeEach(() => {
    revealMock.mockReset();
    revealMock.mockResolvedValue(undefined);
    setPlatform('MacIntel');
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  afterEach(() => setPlatform(''));

  it('本机文件：macOS 显示「在访达中显示」，点击把节点的媒体地址交给 sidecar 并收起菜单', async () => {
    renderProbeInApp({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    await userEvent.click(screen.getByText('在访达中显示'));
    expect(revealMock).toHaveBeenCalledWith(LOCAL);
    expect(screen.queryByText('在访达中显示')).toBeNull();
  });

  it('Windows 上叫「在资源管理器中显示」', () => {
    setPlatform('Win32');
    renderProbe({ url: LOCAL, kind: 'video', nodeId: 'n-1' });
    openMenu();
    expect(screen.getByText('在资源管理器中显示')).toBeInTheDocument();
  });

  it('内容还是上游外链（未保存到本地）：置灰并说明原因，不发请求', async () => {
    renderProbe({ url: 'https://upstream.example.com/out.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    const item = screen.getByText('在访达中显示').closest('button')!;
    expect(item).toBeDisabled();
    expect(item.getAttribute('title')).toContain('还没有保存到本地');
    await userEvent.click(item);
    expect(revealMock).not.toHaveBeenCalled();
  });

  it('没有内容时置灰', () => {
    renderProbe({ url: undefined, kind: 'image', nodeId: 'n-1' });
    openMenu();
    expect(screen.getByText('在访达中显示').closest('button')!).toBeDisabled();
  });

  it('sidecar 报错时把原因提示给用户', async () => {
    revealMock.mockRejectedValue({ response: { data: { error: '文件不存在，可能已被删除' } } });
    renderProbeInApp({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    await userEvent.click(screen.getByText('在访达中显示'));
    expect(await screen.findByText('文件不存在，可能已被删除')).toBeInTheDocument();
  });

  it('「在访达中显示」排在「保存到素材库」之后、「删除」之前', () => {
    renderProbe({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    const labels = screen.getAllByRole('button').map((b) => b.textContent ?? '');
    const save = labels.findIndex((t) => t.includes('保存到素材库'));
    const reveal = labels.findIndex((t) => t.includes('在访达中显示'));
    const del = labels.findIndex((t) => t.includes('删除'));
    expect(save).toBeGreaterThanOrEqual(0);
    expect(reveal).toBe(save + 1);
    expect(del).toBe(reveal + 1);
  });
});

describe('useMediaNodeMenu 「未保存到本地」角标（S5）', () => {
  it('生成结果还是上游外链时，badge 里有「未保存到本地」和「重试」', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'g1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
        status: 'succeeded', content: 'https://upstream.example.com/1.png', settings: {},
      }],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
    renderProbe({ url: 'https://upstream.example.com/1.png', kind: 'image', nodeId: 'g1' });
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('本机文件不挂这个角标', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'g1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
        status: 'succeeded', content: 'http://127.0.0.1:5123/media/k', settings: {},
      }],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
    renderProbe({ url: 'http://127.0.0.1:5123/media/k', kind: 'image', nodeId: 'g1' });
    expect(screen.queryByText('未保存到本地')).toBeNull();
  });
});
