import { beforeEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useCanvasStore } from '../../store';
import { PromptEditorDialog } from './PromptEditorDialog';

function seed(prompt: string, withAsset = true) {
  useCanvasStore.setState({
    nodes: [
      { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: {
          rows: [{ id: 'r1', shotNumber: 1, name: '', duration: 5, colorMark: '', prompt }],
          assets: {
            characters: withAsset ? [{ id: 'c1', name: '张三', description: '', linkedNodeId: 'img1' }] : [],
            scenes: [], props: [],
          },
        } },
      { id: 'img1', type: 'input-image', x: 0, y: 0, width: 200, height: 200, content: 'https://x/1.png' },
    ],
    connections: [], readOnly: false,
  });
}

describe('PromptEditorDialog（chip 富文本版）', () => {
  beforeEach(() => seed(''));

  it('token 渲染为 chip（缩略图+资产名），断链红 chip + 底部提示', () => {
    seed('镜头1：@[角色-张三]出场，@[场景-不存在]。');
    render(<PromptEditorDialog nodeId="s1" rowId="r1" onClose={() => {}} />);
    const ed = screen.getByTestId('prompt-editor-textarea');
    const chips = ed.querySelectorAll('[data-mention]');
    expect(chips).toHaveLength(2);
    expect((chips[0] as HTMLElement).dataset.name).toBe('张三');
    expect((chips[0] as HTMLElement).dataset.broken).toBeUndefined();
    expect((chips[1] as HTMLElement).dataset.broken).toBe('1');
    expect(screen.getByText('场景-不存在（断链）')).toBeInTheDocument();
  });

  it('编辑（contentEditable input）失焦保存 canonical prompt，chip 序列化回 token', () => {
    seed('@[角色-张三]');
    render(<PromptEditorDialog nodeId="s1" rowId="r1" onClose={() => {}} />);
    const ed = screen.getByTestId('prompt-editor-textarea');
    ed.appendChild(document.createTextNode('走进修理铺。'));
    fireEvent.input(ed);
    fireEvent.blur(ed);
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as { prompt: string }[];
    expect(rows[0].prompt).toBe('@[角色-张三]走进修理铺。');
  });
});
