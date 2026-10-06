import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useCanvasStore } from '../../store';
import { useScriptSettings } from './useScriptSettings';

vi.mock('../../persistence/canvasMutations', () => ({ scheduleNodePatch: vi.fn() }));
import { scheduleNodePatch } from '../../persistence/canvasMutations';

describe('useScriptSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220, settings: {} }],
      connections: [], readOnly: false,
    });
  });

  it('addRow 写入 store 并调度落库', () => {
    const { result } = renderHook(() => useScriptSettings('s1'));
    act(() => result.current.addRow());
    expect(result.current.settings.rows).toHaveLength(1);
    expect(result.current.settings.rows[0].shotNumber).toBe(1);
    expect(scheduleNodePatch).toHaveBeenCalledWith('s1', expect.objectContaining({ settings: expect.anything() }));
  });

  it('updateRow 纯 merge：改 name/prompt 即所得', () => {
    const { result } = renderHook(() => useScriptSettings('s1'));
    act(() => result.current.addRow());
    const rowId = result.current.settings.rows[0].id;
    act(() => result.current.updateRow(rowId, { name: '开场', prompt: '镜头1：…' }));
    expect(result.current.settings.rows[0].name).toBe('开场');
    expect(result.current.settings.rows[0].prompt).toBe('镜头1：…');
  });

  it('removeRow 后镜号重排；同一 act 内连续 addRow 不互相覆盖', () => {
    const { result } = renderHook(() => useScriptSettings('s1'));
    act(() => { result.current.addRow(); result.current.addRow(); });
    expect(result.current.settings.rows).toHaveLength(2);
    const first = result.current.settings.rows[0].id;
    act(() => result.current.removeRow(first));
    expect(result.current.settings.rows.map((r) => r.shotNumber)).toEqual([1]);
  });
});
