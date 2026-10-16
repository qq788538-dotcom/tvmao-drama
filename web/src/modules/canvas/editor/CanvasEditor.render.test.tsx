import { act, render } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { beforeEach, expect, it, vi } from 'vitest';

// 节点组件换成计数桩：本文件只测 CanvasEditor 是否把无关更新扇出到每个节点。
const renders = vi.hoisted(() => ({ count: new Map<string, number>() }));
vi.mock('./registry', () => {
  const Stub = ({ id }: { id: string }) => {
    renders.count.set(id, (renders.count.get(id) ?? 0) + 1);
    return <div data-node-id={id} />;
  };
  return {
    nodeComponents: new Proxy({}, { get: () => Stub }),
    nodeMenuItems: [],
    UPLOAD_MENU_TYPE: '__upload__',
  };
});

import { CanvasEditor } from './CanvasEditor';
import { useCanvasStore } from './store';

beforeEach(() => {
  renders.count.clear();
  useCanvasStore.setState({
    nodes: [
      { id: 'n1', type: 'text-node', x: 0, y: 0, width: 100, height: 100 },
      { id: 'n2', type: 'text-node', x: 300, y: 0, width: 100, height: 100 },
    ],
    connections: [],
    view: { x: 0, y: 0, zoom: 1 },
    selectedIds: new Set<string>(),
    readOnly: false,
  });
});

// 回归：平移/缩放每帧改 view、拖动每帧改 nodes，CanvasEditor 重渲染时原先会把
// 全部节点组件整棵重跑；节点只该在自身订阅的数据变化时重渲染。
it('平移/缩放与拖动单个节点不会让其它节点组件重渲染', () => {
  render(
    <AntApp>
      <CanvasEditor />
    </AntApp>,
  );
  const base1 = renders.count.get('n1') ?? 0;
  const base2 = renders.count.get('n2') ?? 0;
  expect(base1).toBeGreaterThan(0);

  act(() => useCanvasStore.getState().setView({ x: 40, y: 20, zoom: 1.5 }));
  act(() => useCanvasStore.getState().moveNodes(['n1'], 10, 0));

  expect(renders.count.get('n1')).toBe(base1);
  expect(renders.count.get('n2')).toBe(base2);
});
