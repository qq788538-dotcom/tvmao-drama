import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { useCanvasStore, useConnectedInputs } from './store';
import type { CanvasNode } from './types';

// 回归：节点组件原先订阅的是稳定的 getConnectedInputs 函数引用、在渲染期调用，
// 连线变化（撤销连线、agent 增删连线）不会触发它们重渲染，参考图/输入数一直是
// 旧值。useConnectedInputs 必须随连线和上游内容变化，且无关更新时引用不变。

const img = (id: string, url: string, x = 0): CanvasNode => ({
  id,
  type: 'input-image',
  x,
  y: 0,
  width: 100,
  height: 100,
  content: url,
});

beforeEach(() => {
  useCanvasStore.setState({
    nodes: [img('a', 'https://x/a.png'), { id: 'g', type: 'gen-image', x: 300, y: 0, width: 100, height: 100 }],
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    undoStack: [],
    redoStack: [],
    readOnly: false,
  });
});

describe('useConnectedInputs', () => {
  it('连线增删（含撤销）后返回新的输入', () => {
    const { result } = renderHook(() => useConnectedInputs('g'));
    expect(result.current.images).toEqual([]);

    act(() => useCanvasStore.getState().addConnection('a', 'g'));
    expect(result.current.images).toEqual(['https://x/a.png']);

    // 撤销只回滚 connections，nodes 引用不变——原先节点组件在这里不会重渲染。
    act(() => useCanvasStore.getState().undo());
    expect(result.current.images).toEqual([]);
  });

  it('上游内容变化时更新，无关更新（拖动/平移）保持同一引用', () => {
    useCanvasStore.setState({ connections: [{ id: 'c1', from: 'a', to: 'g' }] });
    const { result } = renderHook(() => useConnectedInputs('g'));
    const first = result.current;
    expect(first.refs).toEqual([{ nodeId: 'a', kind: 'image', url: 'https://x/a.png', name: undefined }]);

    act(() => useCanvasStore.getState().moveNodes(['a', 'g'], 10, 10));
    act(() => useCanvasStore.getState().setView({ x: 5, y: 5, zoom: 1.2 }));
    expect(result.current).toBe(first);

    act(() => useCanvasStore.getState().updateNode('a', { content: 'https://x/b.png' }));
    expect(result.current.images).toEqual(['https://x/b.png']);
    expect(result.current).not.toBe(first);
  });

  // 读的是 store 派生索引：直接 setState 换掉 nodes（自动排布、外部整拉）也要跟上。
  it('直接 setState 替换上游节点时跟随索引更新', () => {
    useCanvasStore.setState({ connections: [{ id: 'c1', from: 'a', to: 'g' }] });
    const { result } = renderHook(() => useConnectedInputs('g'));
    expect(result.current.images).toEqual(['https://x/a.png']);

    act(() => useCanvasStore.setState((s) => ({
      nodes: s.nodes.map((n) => (n.id === 'a' ? { ...n, content: 'https://x/c.png' } : n)),
    })));
    expect(result.current.images).toEqual(['https://x/c.png']);

    act(() => useCanvasStore.setState((s) => ({ nodes: s.nodes.filter((n) => n.id !== 'a') })));
    expect(result.current.images).toEqual([]);
  });
});
