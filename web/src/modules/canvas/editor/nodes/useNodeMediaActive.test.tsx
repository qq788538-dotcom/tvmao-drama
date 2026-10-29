import { describe, expect, it, beforeEach } from 'vitest';
import { act, render } from '@testing-library/react';
import { useCanvasStore } from '../store';
import { StoryboardModeContext } from '../../storyboard/StoryboardContext';
import { isNodeNearViewport, useNodeMediaActive } from './useNodeMediaActive';
import type { CanvasNode } from '../types';

const VP_W = 1000;
const VP_H = 800;

describe('isNodeNearViewport', () => {
  const rect = { x: 0, y: 0, width: 100, height: 100 };

  it('视口内为 true', () => {
    expect(isNodeNearViewport(rect, { x: 0, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(true);
  });

  it('视口外一屏边距内仍为 true，超出为 false', () => {
    // 节点在世界坐标 x=1500，视口右边界 1000，一屏边距到 2000。
    expect(isNodeNearViewport({ ...rect, x: 1500 }, { x: 0, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(true);
    expect(isNodeNearViewport({ ...rect, x: 2100 }, { x: 0, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(false);
    expect(isNodeNearViewport({ ...rect, x: -1200 }, { x: 0, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(false);
    expect(isNodeNearViewport({ ...rect, y: 1700 }, { x: 0, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(false);
  });

  it('按 view 平移与缩放换算：screen = world*zoom + view.x', () => {
    // zoom=0.5 时视口覆盖世界 0..2000，加一屏边距到 4000。
    expect(isNodeNearViewport({ ...rect, x: 3900 }, { x: 0, y: 0, zoom: 0.5 }, VP_W, VP_H)).toBe(true);
    expect(isNodeNearViewport({ ...rect, x: 4100 }, { x: 0, y: 0, zoom: 0.5 }, VP_W, VP_H)).toBe(false);
    // 平移到世界 5000 附近：view.x = -5000。
    expect(isNodeNearViewport({ ...rect, x: 5200 }, { x: -5000, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(true);
    expect(isNodeNearViewport(rect, { x: -5000, y: 0, zoom: 1 }, VP_W, VP_H)).toBe(false);
  });
});

function node(patch: Partial<CanvasNode> = {}): CanvasNode {
  return { id: 'v1', type: 'video-input', x: 0, y: 0, width: 300, height: 200, ...patch } as CanvasNode;
}

function Probe({ id }: { id: string }) {
  return <span data-testid="p">{String(useNodeMediaActive(id))}</span>;
}

describe('useNodeMediaActive', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', { value: VP_W + 56, configurable: true });
    Object.defineProperty(window, 'innerHeight', { value: VP_H + 56, configurable: true });
    useCanvasStore.setState({
      nodes: [node({ x: 10000 })],
      view: { x: 0, y: 0, zoom: 1 },
      trimmingNodeId: null,
      croppingNodeId: null,
      reshootNodeId: null,
      continuationNodeId: null,
    });
  });

  it('远离视口为 false，平移过去变 true', () => {
    const { getByTestId } = render(<Probe id="v1" />);
    expect(getByTestId('p').textContent).toBe('false');
    act(() => useCanvasStore.setState({ view: { x: -10000, y: 0, zoom: 1 } }));
    expect(getByTestId('p').textContent).toBe('true');
  });

  it('剪辑/裁剪中强制活跃', () => {
    const { getByTestId } = render(<Probe id="v1" />);
    act(() => useCanvasStore.setState({ trimmingNodeId: 'v1' }));
    expect(getByTestId('p').textContent).toBe('true');
    act(() => useCanvasStore.setState({ trimmingNodeId: null, croppingNodeId: 'v1' }));
    expect(getByTestId('p').textContent).toBe('true');
    act(() => useCanvasStore.setState({ croppingNodeId: null }));
    expect(getByTestId('p').textContent).toBe('false');
  });

  it('故事板里恒为活跃', () => {
    const { getByTestId } = render(
      <StoryboardModeContext.Provider value>
        <Probe id="v1" />
      </StoryboardModeContext.Provider>,
    );
    expect(getByTestId('p').textContent).toBe('true');
  });
});
