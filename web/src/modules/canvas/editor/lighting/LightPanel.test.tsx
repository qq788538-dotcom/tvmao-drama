import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchLightControlConfig, listRuntimeModels, type ModelConfig } from '@/shared/api';
import { useCanvasStore } from '../store';
import { LightPanel } from './LightPanel';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  fetchLightControlConfig: vi.fn(),
  listRuntimeModels: vi.fn(),
  submitCanvasTask: vi.fn(),
  uploadAsset: vi.fn(),
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

describe('LightPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchLightControlConfig).mockResolvedValue({ models: ['img-a'], presets: [] });
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

  it('底栏不显示预扣积分或计费说明（X3）', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <AntApp>
          <LightPanel sourceNodeId="src" onClose={() => {}} />
        </AntApp>
      </QueryClientProvider>,
    );

    expect(screen.getByText('重置参数')).toBeInTheDocument();
    await waitFor(() => expect(listRuntimeModels).toHaveBeenCalled());
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 500));
    });
    expect(screen.queryByText(/积分/)).toBeNull();
  });
});
