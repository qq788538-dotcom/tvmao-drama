import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  submitCanvasTask: vi.fn().mockResolvedValue({ taskId: 1, status: 'processing' }),
  currentProjectId: () => 42,
}));
import { submitCanvasTask } from '@/shared/api';
import { BatchVideoDialog } from './BatchVideoDialog';

function renderDialog(models: unknown[] = [{ modelId: 'seedance-1', name: 'Seedance', type: 'video' }]) {
  const queryClient = new QueryClient();
  queryClient.setQueryData(['runtime-models'], models);
  return render(
    <QueryClientProvider client={queryClient}>
      <BatchVideoDialog nodeId="s1" onClose={() => {}} />
    </QueryClientProvider>,
  );
}

const assets = {
  characters: [
    { id: 'c1', name: '张三', description: '', linkedNodeId: 'img1' },
    { id: 'c2', name: '王大爷', description: '', linkedNodeId: '' },
  ],
  scenes: [], props: [],
};

describe('BatchVideoDialog（skill 对齐版）', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
          settings: {
            rows: [
              { id: 'r1', shotNumber: 1, name: '开场', duration: 5, colorMark: '', prompt: '镜头1：@[角色-张三]。' },
              { id: 'r2', shotNumber: 2, name: '', duration: 5, colorMark: '', prompt: '' },
              { id: 'r3', shotNumber: 3, name: '缺图镜', duration: 5, colorMark: '', prompt: '镜头1：@[角色-王大爷]。' },
            ],
            assets,
          } },
        { id: 'img1', type: 'input-image', x: -400, y: 0, width: 200, height: 200, content: 'https://x/1.png' },
      ],
      connections: [], readOnly: false,
    });
  });

  it('默认只勾无缺图警告的镜；空 prompt 禁选；缺图镜显示黄色警告', () => {
    renderDialog();
    expect(screen.getByTestId('batch-video-confirm')).toHaveTextContent('(1)');
    expect(screen.getByText('提示词为空，无法创建')).toBeInTheDocument();
    expect(screen.getByTestId('row-missing-r3')).toHaveTextContent('角色-王大爷');
  });

  it('确认后只铺节点（含翻译后的 prompt），不提交任何生成任务', () => {
    renderDialog();
    fireEvent.click(screen.getByTestId('batch-video-confirm'));
    const vns = useCanvasStore.getState().nodes.filter((n) => n.type === 'gen-video');
    expect(vns).toHaveLength(1);
    expect(vns[0].settings).toMatchObject({
      videoPrompt: '镜头1：@[图片:img1]。',
      modelId: 'seedance-1',
      scriptRowId: 'r1',
    });
    expect(submitCanvasTask).not.toHaveBeenCalled();
  });

  it('缺图镜可强选，token 退化为纯名字', () => {
    renderDialog();
    const r3 = screen.getByText('缺图镜').closest('label')!.querySelector('input[type="checkbox"]') as HTMLInputElement;
    fireEvent.click(r3);
    fireEvent.click(screen.getByTestId('batch-video-confirm'));
    const vns = useCanvasStore.getState().nodes.filter((n) => n.type === 'gen-video');
    const r3Node = vns.find((n) => n.settings?.scriptRowId === 'r3')!;
    expect(r3Node.settings?.videoPrompt).toBe('镜头1：王大爷。');
  });
});
