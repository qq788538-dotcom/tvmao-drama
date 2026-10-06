import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  submitCanvasTask: vi.fn().mockResolvedValue({ taskId: 7, status: 'processing' }),
  currentProjectId: () => 42,
}));
import { submitCanvasTask } from '@/shared/api';
import { AddAssetDialog } from './AddAssetDialog';

function renderDialog(models: unknown[] = [{ modelId: 'lib-image', name: 'Lib Image', type: 'image' }]) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queryClient.setQueryData(['runtime-models'], models);
  return render(
    <QueryClientProvider client={queryClient}>
      <AddAssetDialog nodeId="s1" section="scenes" sectionLabel="场景" onClose={() => {}} />
    </QueryClientProvider>,
  );
}

describe('AddAssetDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 1000, y: 500, width: 300, height: 220,
          settings: { rows: [], assets: { characters: [], scenes: [], props: [] } } },
        { id: 'imgA', type: 'input-image', x: 0, y: 0, width: 200, height: 200,
          content: 'https://x/a.png', settings: { name: '角色 1' } },
      ],
      connections: [], readOnly: false,
    });
  });

  it('从当前画布选择：点选后连线 + 建资产（名称取节点名）', () => {
    renderDialog();
    fireEvent.click(screen.getByTestId('add-asset-tab-canvas'));
    fireEvent.click(screen.getByTestId('pick-node-imgA'));
    const st = useCanvasStore.getState();
    expect(st.connections.some((c) => c.from === 'imgA' && c.to === 's1')).toBe(true);
    const assets = (st.nodes[0].settings! as { assets: { scenes: { name: string; linkedNodeId: string }[] } }).assets;
    expect(assets.scenes).toHaveLength(1);
    expect(assets.scenes[0]).toMatchObject({ name: '角色 1', linkedNodeId: 'imgA' });
  });

  it('默认停在「从当前画布选择」tab', () => {
    renderDialog();
    expect(screen.getByTestId('pick-node-imgA')).toBeInTheDocument();
  });

  it('AI生成：确认后建 gen-image 节点+连线+资产，并提交一次生成', async () => {
    renderDialog();
    fireEvent.click(screen.getByTestId('add-asset-tab-ai'));
    fireEvent.change(screen.getByTestId('asset-gen-prompt'), { target: { value: '赛博朋克修理铺' } });
    fireEvent.click(screen.getByTestId('asset-gen-confirm'));
    await vi.waitFor(() => expect(submitCanvasTask).toHaveBeenCalledTimes(1));
    const st = useCanvasStore.getState();
    const gen = st.nodes.find((n) => n.type === 'gen-image')!;
    expect(gen.settings).toMatchObject({ prompt: '赛博朋克修理铺', modelId: 'lib-image' });
    expect(st.connections.some((c) => c.from === gen.id && c.to === 's1')).toBe(true);
    const assets = (st.nodes.find((n) => n.id === 's1')!.settings! as { assets: { scenes: { linkedNodeId: string }[] } }).assets;
    expect(assets.scenes[0].linkedNodeId).toBe(gen.id);
    expect(submitCanvasTask).toHaveBeenCalledWith(expect.objectContaining({
      projectId: 42, modelId: 'lib-image', nodeId: gen.id,
      inputs: expect.objectContaining({ prompt: '赛博朋克修理铺' }),
    }));
  });

  it('同名自动去重加序号', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 1000, y: 500, width: 300, height: 220,
          settings: { rows: [], assets: { characters: [], scenes: [{ id: 'x', name: '角色 1', description: '', linkedNodeId: '' }], props: [] } } },
        { id: 'imgA', type: 'input-image', x: 0, y: 0, width: 200, height: 200,
          content: 'https://x/a.png', settings: { name: '角色 1' } },
      ],
      connections: [], readOnly: false,
    });
    renderDialog();
    fireEvent.click(screen.getByTestId('add-asset-tab-canvas'));
    fireEvent.click(screen.getByTestId('pick-node-imgA'));
    const assets = (useCanvasStore.getState().nodes[0].settings! as { assets: { scenes: { name: string }[] } }).assets;
    expect(assets.scenes.map((a) => a.name)).toEqual(['角色 1', '角色 12']);
  });
});
