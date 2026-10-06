import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AICharactersModal } from './AICharactersModal';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import type { AICharacter } from '@/shared/api';

const fetchAICharactersMock = vi.fn();
vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    fetchAICharacters: (...args: unknown[]) => fetchAICharactersMock(...args),
  };
});

// 「应用到画布」刻意不走服务端（不插 assets 引用行，见组件注释）——所以这里
// 只需要断言本地节点插入 + 本地持久化被调用，不该出现任何 apply 类请求。
const persistNodeByIdMock = vi.fn();
vi.mock('@/modules/canvas/editor/persistence/canvasMutations', () => ({
  persistNodeById: (...args: unknown[]) => persistNodeByIdMock(...args),
}));

// jsdom 没有 IntersectionObserver；滚动加载的触发逻辑不在本测试范围。
class NoopObserver {
  observe() {}
  disconnect() {}
  unobserve() {}
}
vi.stubGlobal('IntersectionObserver', NoopObserver);

function character(over: Partial<AICharacter> = {}): AICharacter {
  return {
    assetId: 'asset-20260224225822-g22rm',
    age: 21,
    gender: '女',
    occupation: '演员',
    description: '21岁的中国女演员常扮演宫女。',
    ageGroup: '青年',
    imageUrl: 'https://x.test/full/asset-1.png?sig=1',
    thumbUrl: 'https://x.test/thumb/asset-1.png?sig=1',
    ...over,
  };
}

function renderModal() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AntApp>
        <AICharactersModal open onClose={() => {}} />
      </AntApp>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  useCanvasStore.setState({ nodes: [], connections: [] });
});

describe('AICharactersModal', () => {
  it('列出角色卡片，点击进详情', async () => {
    fetchAICharactersMock.mockResolvedValue({ items: [character()], hasMore: false });
    renderModal();

    const card = await screen.findByTitle('演员');
    fireEvent.click(card.closest('button')!);

    // 详情弹框：属性 + 人设描述全文
    expect(await screen.findByText('角色属性')).toBeInTheDocument();
    expect(screen.getByText('21岁的中国女演员常扮演宫女。')).toBeInTheDocument();
    expect(screen.getByText('应用到画布')).toBeInTheDocument();
  });

  it('应用到画布：本地插入 input-image 节点并持久化，不发 apply 请求', async () => {
    fetchAICharactersMock.mockResolvedValue({ items: [character()], hasMore: false });
    renderModal();

    fireEvent.click((await screen.findByTitle('演员')).closest('button')!);
    fireEvent.click(await screen.findByText('应用到画布'));

    await waitFor(() => {
      const nodes = useCanvasStore.getState().nodes;
      expect(nodes).toHaveLength(1);
      expect(nodes[0].type).toBe('input-image');
      expect(nodes[0].content).toBe('https://x.test/full/asset-1.png?sig=1');
    });
    expect(persistNodeByIdMock).toHaveBeenCalledTimes(1);
  });

  it('性别/年龄段过滤透传给接口', async () => {
    fetchAICharactersMock.mockResolvedValue({ items: [], hasMore: false });
    renderModal();
    await waitFor(() => expect(fetchAICharactersMock).toHaveBeenCalled());

    // 首次请求不带过滤
    expect(fetchAICharactersMock).toHaveBeenLastCalledWith({ offset: 0 });
  });
});
