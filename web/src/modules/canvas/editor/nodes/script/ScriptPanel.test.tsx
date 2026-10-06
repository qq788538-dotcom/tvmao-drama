import { beforeEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useCanvasStore } from '../../store';
import { ScriptPanel } from './ScriptPanel';

describe('ScriptPanel（两步制）', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} }],
      connections: [], readOnly: false,
    });
  });

  it('只有两步；空表自动补第 1 镜；第三步不存在', () => {
    render(<ScriptPanel nodeId="s1" onClose={() => {}} />);
    expect(screen.getByTestId('script-step-1')).toBeInTheDocument();
    expect(screen.getByTestId('script-step-2')).toBeInTheDocument();
    expect(screen.queryByTestId('script-step-3')).toBeNull();
    expect(screen.queryByText('合成提示词')).toBeNull();
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as unknown[];
    expect(rows).toHaveLength(1);
    expect(screen.getByText('1/2 完成后可批量生视频')).toBeInTheDocument();
  });

  it('点提示词列打开大编辑器', () => {
    render(<ScriptPanel nodeId="s1" onClose={() => {}} />);
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as { id: string }[];
    fireEvent.click(screen.getByTestId(`cell-prompt-${rows[0].id}`));
    expect(screen.getByTestId('prompt-editor-dialog')).toBeInTheDocument();
  });

  it('步骤 2 渲染资产面板且无下一步按钮（已是最后一步）', () => {
    render(<ScriptPanel nodeId="s1" onClose={() => {}} />);
    fireEvent.click(screen.getByTestId('script-step-2'));
    expect(screen.getByTestId('script-assets-panel')).toBeInTheDocument();
    expect(screen.queryByText('→ 下一步：合成提示词')).toBeNull();
  });
});
