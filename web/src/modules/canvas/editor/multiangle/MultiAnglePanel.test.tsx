import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchMultiAngleConfig, listRuntimeModels, type ModelConfig } from '@/shared/api';
import { useCanvasStore } from '../store';
import { MultiAnglePanel } from './MultiAnglePanel';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  fetchMultiAngleConfig: vi.fn(),
  listRuntimeModels: vi.fn(),
  submitCanvasTask: vi.fn(),
}));

const model: ModelConfig = {
  id: 1,
  type: 'image',
  name: '绘图模型A',
  modelId: 'img-a',
  basePath: '',
  priceNote: '100积分/张',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {},
};

describe('MultiAnglePanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchMultiAngleConfig).mockResolvedValue({ models: ['img-a'], presets: [] });
    vi.mocked(listRuntimeModels).mockResolvedValue([model]);
    useCanvasStore.setState({
      nodes: [{
        id: 'src', type: 'input-image', x: 0, y: 0, width: 260, height: 260,
        status: 'succeeded', content: 'http://127.0.0.1:5123/media/a.png', settings: {},
      }],
      connections: [],
      selectedIds: new Set<string>(),
      readOnly: false,
    });
  });

  // 桌面端不计费：底栏不显示「⚡ 预扣积分」或 priceNote 计费说明。
  it('底栏不显示预扣积分或计费说明（X3）', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <AntApp>
          <MultiAnglePanel sourceNodeId="src" onClose={() => {}} />
        </AntApp>
      </QueryClientProvider>,
    );

    expect((await screen.findAllByText('绘图模型A')).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 500));
    });
    expect(screen.queryByText(/积分/)).toBeNull();
  });
});
