import { act, fireEvent, render } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { ConnectionLayer } from './ConnectionLayer';
import { useCanvasStore } from './store';
import type { CanvasNode } from './types';

function makeNode(id: string, x: number): CanvasNode {
  return {
    id,
    type: 'gen-image',
    x,
    y: 0,
    width: 280,
    height: 280,
    settings: {},
    status: 'idle',
  };
}

describe('ConnectionLayer 电流层', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [makeNode('a', 0), makeNode('b', 400)],
      connections: [{ id: 'c1', from: 'a', to: 'b' }],
      selectedIds: new Set<string>(),
    });
  });

  it('未选中未悬停时不渲染电流层', () => {
    const { container } = render(<ConnectionLayer />);
    expect(container.querySelector('.connection-flow')).toBeNull();
  });

  it('端点节点被选中时渲染电流层', () => {
    useCanvasStore.setState({ selectedIds: new Set(['a']) });
    const { container } = render(<ConnectionLayer />);
    expect(container.querySelector('.connection-flow')).toBeTruthy();
  });

  it('悬停连线时渲染电流层，移开后消失', () => {
    const { container } = render(<ConnectionLayer />);
    const group = container.querySelector('.connection-group')!;
    fireEvent.mouseEnter(group);
    expect(container.querySelector('.connection-flow')).toBeTruthy();
    fireEvent.mouseLeave(group);
    expect(container.querySelector('.connection-flow')).toBeNull();
  });
});

describe('ConnectionLayer 自行订阅', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [makeNode('a', 0), makeNode('b', 400)],
      connections: [{ id: 'c1', from: 'a', to: 'b' }],
      selectedIds: new Set<string>(),
      view: { x: 0, y: 0, zoom: 1 },
    });
  });

  // 不再从父组件收 nodes：节点移动、连线增删都直接从 store 跟上。
  it('节点移动后连线端点跟随，删连线后不再渲染', () => {
    const { container } = render(<ConnectionLayer />);
    const firstPath = () => container.querySelector('.connection-group path')!.getAttribute('d');
    expect(firstPath()).toMatch(/^M 280 140 /);

    act(() => useCanvasStore.getState().moveNodes(['a'], 20, 10));
    expect(firstPath()).toMatch(/^M 300 150 /);

    act(() => useCanvasStore.setState({ connections: [] }));
    expect(container.querySelector('.connection-group')).toBeNull();
  });
});
