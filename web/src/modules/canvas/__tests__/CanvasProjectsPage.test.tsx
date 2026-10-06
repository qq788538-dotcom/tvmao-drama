import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasProjectsPage } from '../CanvasProjectsPage';
import { queryClient } from '@/shared/queryClient';

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

const listProjectsPage = vi.fn();
const duplicateProject = vi.fn();
const createProject = vi.fn();

vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    listProjectsPage: (...args: unknown[]) => listProjectsPage(...args),
    duplicateProject: (...args: unknown[]) => duplicateProject(...args),
    createProject: (...args: unknown[]) => createProject(...args),
    listProjectImages: vi.fn(async () => []),
    setProjectCover: vi.fn(async () => undefined),
  };
});

// jsdom 没有 IntersectionObserver。存一份实例，测试里手动触发「哨兵进入视口」。
let ioCallback: IntersectionObserverCallback | null = null;
beforeEach(() => {
  queryClient.clear();
  navigate.mockReset();
  duplicateProject.mockReset();
  createProject.mockReset();
  // 必须 reset：mock 的调用计数是跨用例累加的，漏了它「翻页调用了几次」这种
  // 断言就会数到前面用例的账上。
  listProjectsPage.mockReset();
  ioCallback = null;
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      constructor(cb: IntersectionObserverCallback) {
        ioCallback = cb;
      }
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  );
  listProjectsPage.mockImplementation(async () => ({
    items: [
      {
        id: 1,
        name: '有封面的项目',
        description: '描述一',
        updatedAt: '2026-08-07T09:38:08Z',
        storageBytes: 196000000,
        coverUrl: 'https://signed.example/cover.png',
      },
    ],
    total: 1,
  }));
});

function renderPage() {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <AntApp>
          <CanvasProjectsPage />
        </AntApp>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function openMenu() {
  const more = await screen.findByLabelText('更多操作');
  fireEvent.click(more);
}

describe('CanvasProjectsPage', () => {
  it('卡片显示标题、日期与存储大小', async () => {
    renderPage();
    await screen.findByText('有封面的项目');
    expect(screen.getByText('2026-08-07')).toBeTruthy();
    // 存储大小是刻意保留的：这页是全站唯一能看到单项目占多少空间的地方。
    expect(screen.getByText('187 MB')).toBeTruthy();
  });

  it('封面加载失败时退回占位图，而不是留一个破图标', async () => {
    const { container } = renderPage();
    await screen.findByText('有封面的项目');
    // 封面是装饰性图片（alt=""），a11y role 是 presentation 不是 img，
    // 只能按标签选。
    const img = container.querySelector('.tn-cover img') as HTMLImageElement;
    expect(img).toBeTruthy();
    fireEvent.error(img);
    await waitFor(() => {
      expect(container.querySelector('.tn-cover--empty')).toBeTruthy();
    });
  });

  it('菜单five项齐全', async () => {
    renderPage();
    await screen.findByText('有封面的项目');
    await openMenu();
    for (const label of ['打开', '重命名', '修改封面', '创建副本', '删除项目']) {
      expect(await screen.findByText(label)).toBeTruthy();
    }
  });

  it('「开始创作」直接建未命名项目并进画布，不弹表单', async () => {
    createProject.mockResolvedValue({ id: 77, name: '未命名' });
    renderPage();
    fireEvent.click(await screen.findByText('开始创作'));
    await waitFor(() => expect(createProject).toHaveBeenCalledWith('未命名', ''));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/canvas/77'));
  });

  // 这条是本页最要紧的断言。用户拍板「不弹存储确认框」+「复制完直接跳画布」，
  // 两条合起来意味着点下去毫无前置反馈；而复制大项目要几秒才跳走。没有遮罩的话
  // 用户会以为没点中而连点——连点就是复制出好几份，真实吃掉几倍存储配额。
  it('复制期间弹出不可关闭的 loading 遮罩，完成后跳进新副本', async () => {
    let resolveDup: (id: number) => void = () => {};
    duplicateProject.mockImplementation(
      () => new Promise<number>((res) => { resolveDup = res; }),
    );

    renderPage();
    await screen.findByText('有封面的项目');
    await openMenu();
    fireEvent.click(await screen.findByText('创建副本'));

    // 遮罩出现，且没有任何关闭途径（没有 X、没有取消按钮）。
    const mask = await screen.findByText('正在复制项目…');
    expect(mask).toBeTruthy();
    expect(document.querySelector('.ant-modal-close')).toBeNull();

    resolveDup(999);
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/canvas/999'));
    await waitFor(() => expect(screen.queryByText('正在复制项目…')).toBeNull());
  });

  // 失败路径必须关掉遮罩，否则用户被永久卡在遮罩后面，只能刷新页面。
  it('复制失败时关闭遮罩', async () => {
    duplicateProject.mockRejectedValue({ response: { data: { error: '存储空间不足' } } });

    renderPage();
    await screen.findByText('有封面的项目');
    await openMenu();
    fireEvent.click(await screen.findByText('创建副本'));

    await waitFor(() => expect(screen.queryByText('正在复制项目…')).toBeNull());
    expect(navigate).not.toHaveBeenCalled();
  });

  it('哨兵进入视口时拉下一页', async () => {
    listProjectsPage.mockImplementation(async (_limit: number, offset: number) => ({
      items: [
        {
          id: offset + 1,
          name: `项目 ${offset + 1}`,
          description: '',
          updatedAt: '2026-08-07T09:38:08Z',
          storageBytes: 0,
        },
      ],
      total: 2, // 第一页只回 1 条 → 还有下一页
    }));

    renderPage();
    await screen.findByText('项目 1');
    expect(listProjectsPage).toHaveBeenCalledTimes(1);

    ioCallback?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);

    await waitFor(() => expect(listProjectsPage).toHaveBeenCalledTimes(2));
    // 第二页的 offset 必须是已加载条数，不是页码。
    expect(listProjectsPage).toHaveBeenLastCalledWith(24, 1);
    await screen.findByText('项目 2');
  });
});
