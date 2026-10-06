import { fireEvent, render } from '@testing-library/react';
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
      connections: [{ id: 'c1', from: 'a', to: 'b' }],
      selectedIds: new Set<string>(),
    });
  });

  const nodes = () => [makeNode('a', 0), makeNode('b', 400)];

  it('未选中未悬停时不渲染电流层', () => {
    const { container } = render(<ConnectionLayer nodes={nodes()} />);
    expect(container.querySelector('.connection-flow')).toBeNull();
  });

  it('端点节点被选中时渲染电流层', () => {
    useCanvasStore.setState({ selectedIds: new Set(['a']) });
    const { container } = render(<ConnectionLayer nodes={nodes()} />);
    expect(container.querySelector('.connection-flow')).toBeTruthy();
  });

  it('悬停连线时渲染电流层，移开后消失', () => {
    const { container } = render(<ConnectionLayer nodes={nodes()} />);
    const group = container.querySelector('.connection-group')!;
    fireEvent.mouseEnter(group);
    expect(container.querySelector('.connection-flow')).toBeTruthy();
    fireEvent.mouseLeave(group);
    expect(container.querySelector('.connection-flow')).toBeNull();
  });
});
