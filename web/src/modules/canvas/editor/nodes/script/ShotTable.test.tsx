import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { useCanvasStore } from '../../store';
import { ShotTable, rowIssues } from './ShotTable';
import { createScriptRow, defaultScriptSettings } from './scriptData';

function setup(rows: unknown[] = [], extra: Record<string, unknown> = {}) {
  useCanvasStore.setState({
    nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: { rows, ...extra } }],
    connections: [], readOnly: false,
  });
  return render(<ShotTable nodeId="s1" onOpenPrompt={() => {}} />);
}

describe('ShotTable（skill 对齐版）', () => {
  beforeEach(() => useCanvasStore.setState({ nodes: [], connections: [] }));

  it('5 列表头齐全（散字段列已移除）', () => {
    setup();
    for (const h of ['镜号', '分镜简述', '时长', '提示词', '操作']) {
      expect(screen.getByText(h)).toBeInTheDocument();
    }
    for (const gone of ['画面描述', '景别', '光影氛围', '对白·旁白', '音效', '运镜', '最终提示词']) {
      expect(screen.queryByText(gone)).toBeNull();
    }
  });

  it('添加分镜 → 行出现且镜号=1', () => {
    setup();
    fireEvent.click(screen.getByTestId('script-add-shot'));
    expect(screen.getByTestId('shot-row-1')).toBeInTheDocument();
  });

  it('时长编辑器 clamp 到 4~15（M1 恒定上限，存量 durationSec 污染值被忽略）', () => {
    setup([{ id: 'r1', duration: 5 }], { durationSec: 10 }); // 早期默认值残留
    fireEvent.click(screen.getByTestId('cell-duration-r1'));
    const input = screen.getByRole('spinbutton');
    fireEvent.change(input, { target: { value: '99' } });
    fireEvent.blur(input);
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as { duration: number }[];
    expect(rows[0].duration).toBe(15);
  });

  it('分镜简述失焦保存；点提示词列回调 rowId', () => {
    const onOpenPrompt = vi.fn();
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: { rows: [{ id: 'r1' }] } }],
      connections: [], readOnly: false,
    });
    render(<ShotTable nodeId="s1" onOpenPrompt={onOpenPrompt} />);
    fireEvent.click(screen.getByTestId('cell-name-r1'));
    const ta = screen.getByRole('textbox');
    fireEvent.change(ta, { target: { value: '开场' } });
    fireEvent.blur(ta);
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as { name: string }[];
    expect(rows[0].name).toBe('开场');
    fireEvent.click(screen.getByTestId('cell-prompt-r1'));
    expect(onOpenPrompt).toHaveBeenCalledWith('r1');
  });

  it('行菜单删除该行后镜号重排', () => {
    setup([{ id: 'r1' }, { id: 'r2' }]);
    fireEvent.click(screen.getByTestId('row-menu-r1'));
    fireEvent.click(screen.getByText('删除该行'));
    const rows = useCanvasStore.getState().nodes[0].settings!.rows as { shotNumber: number }[];
    expect(rows).toHaveLength(1);
    expect(rows[0].shotNumber).toBe(1);
  });

  it('断链 token 显示红点', () => {
    setup([{ id: 'r1', prompt: '镜头1：@[角色-不存在]走过。' }]);
    expect(screen.getByTestId('row-issues-r1')).toBeInTheDocument();
  });

  it('提示词列 token 渲染为 chip（带资产名），断链 chip 标红', () => {
    setup(
      [{ id: 'r1', prompt: '镜头1：@[角色-张三]与@[场景-无]同框。' }],
      { assets: { characters: [{ id: 'c1', name: '张三', description: '', linkedNodeId: '' }], scenes: [], props: [] } },
    );
    const cell = screen.getByTestId('cell-prompt-r1');
    const chips = cell.querySelectorAll('[data-mention]');
    expect(chips).toHaveLength(2);
    expect((chips[0] as HTMLElement).dataset.broken).toBeUndefined();
    expect((chips[1] as HTMLElement).dataset.broken).toBe('1');
    expect(cell.textContent).toContain('张三');
  });

  it('readOnly=true 时点单元格不出现编辑器，添加/行菜单 disabled', () => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: { rows: [{ id: 'r1', duration: 5 }] } }],
      connections: [], readOnly: false,
    });
    render(<ShotTable nodeId="s1" onOpenPrompt={() => {}} readOnly />);
    fireEvent.click(screen.getByTestId('cell-duration-r1'));
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument();
    expect(screen.getByTestId('script-add-shot')).toBeDisabled();
    expect(screen.getByTestId('row-menu-r1')).toBeDisabled();
  });
});

describe('rowIssues', () => {
  it('简述超长/时长越界/断链各出一条', () => {
    const s = defaultScriptSettings();
    const row = { ...createScriptRow(), name: '一二三四五六七八九十一二三四五六', duration: 20, prompt: '@[物品-无此物]' };
    const issues = rowIssues(row, s.assets, 10);
    expect(issues).toHaveLength(3);
  });
  it('合法行零问题', () => {
    const s = defaultScriptSettings();
    s.assets.characters = [{ id: 'c1', name: '张三', description: '', linkedNodeId: '' }];
    const row = { ...createScriptRow(), name: '开场', duration: 8, prompt: '镜头1：@[角色-张三]。' };
    expect(rowIssues(row, s.assets, 10)).toEqual([]);
  });
});
