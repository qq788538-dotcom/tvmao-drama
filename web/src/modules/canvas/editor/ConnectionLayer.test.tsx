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
      connections: [{ id: 'c1', from: 'a', to: 'b' }],
      selectedIds: new Set<string>(),
      transientPositions: {},
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

  // 队友拖拽途中的坐标只存在于瞬态覆盖层（nodes[].x/y 一动不动）。连线不读覆盖层
  // 的话，队友拖一个连着线的节点时节点走、线不走，看上去像连线断了。
  it('连线跟随远端瞬态坐标，而 nodes 本身不被改写', () => {
    const source = nodes();
    const { container } = render(<ConnectionLayer nodes={source} />);
    const before = container.querySelector('path')!.getAttribute('d');

    act(() => {
      useCanvasStore.setState({
        transientPositions: { a: { x: 900, y: 500, expireAt: Date.now() + 10_000 } },
      });
    });
    const { container: after } = render(<ConnectionLayer nodes={source} />);
    expect(after.querySelector('path')!.getAttribute('d')).not.toBe(before);
    // 覆盖层严禁回写节点：一旦进了 nodes，整节点 diff 就会把中间坐标持久化。
    expect(source[0].x).toBe(0);
  });
});
