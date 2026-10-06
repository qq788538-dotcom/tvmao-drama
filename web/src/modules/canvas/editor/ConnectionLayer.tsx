import { useState } from 'react';
import { resolveNodePosition, useCanvasStore } from './store';
import type { CanvasNode } from './types';

export function ConnectionLayer({ nodes }: { nodes: CanvasNode[] }) {
  const connections = useCanvasStore((s) => s.connections);
  const removeConnection = useCanvasStore((s) => s.removeConnection);
  const selectedIds = useCanvasStore((s) => s.selectedIds);
  // 队友拖拽途中的瞬态坐标覆盖层。连线必须跟着一起走，否则队友拖一个有连线的
  // 节点时，节点动而线不动——看上去像连线断了。**只读不写**：坐标只在这里参与
  // 画路径，绝不回写进 nodes（那会让瞬态中间值进持久化 diff，正是 T4 H1 的坑）。
  // 个人项目这张表恒为初始空对象，引用不变 → 不产生任何多余渲染。
  const transientPositions = useCanvasStore((s) => s.transientPositions);
  const [hoveredId, setHoveredId] = useState<string | null>(null);

  const nodeMap = new Map(nodes.map((n) => [n.id, n]));

  return (
    <svg
      className="absolute overflow-visible pointer-events-none"
      style={{ left: 0, top: 0, width: 1, height: 1 }}
    >
      {connections.map((conn) => {
        const from = nodeMap.get(conn.from);
        const to = nodeMap.get(conn.to);
        if (!from || !to) return null;

        const fromAt = resolveNodePosition(from, transientPositions[from.id]);
        const toAt = resolveNodePosition(to, transientPositions[to.id]);
        const startX = fromAt.x + from.width;
        const startY = fromAt.y + from.height / 2;
        const endX = toAt.x;
        const endY = toAt.y + to.height / 2;
        const dist = Math.abs(endX - startX);
        const dx = Math.max(52, Math.min(180, dist * 0.42));
        const path = `M ${startX} ${startY} C ${startX + dx} ${startY}, ${endX - dx} ${endY}, ${endX} ${endY}`;
        const midX = (startX + endX) / 2;
        const midY = (startY + endY) / 2;
        const selected = selectedIds.has(conn.from) || selectedIds.has(conn.to);
        const hovered = hoveredId === conn.id;
        const stroke = hovered || selected ? '#60a5fa' : '#71717a';
        const glow = hovered ? '#1d4ed8' : '#09090b';
        const deleteOpacity = hovered ? 1 : 0.25;

        return (
          <g
            key={conn.id}
            className="connection-group pointer-events-auto"
            onMouseEnter={() => setHoveredId(conn.id)}
            onMouseLeave={() => setHoveredId((cur) => (cur === conn.id ? null : cur))}
          >
            <path d={path} stroke={glow} strokeWidth={6} strokeOpacity={hovered ? 0.95 : 0.9} fill="none" />
            <path d={path} stroke={stroke} strokeWidth={2.5} fill="none" strokeOpacity={hovered ? 1 : 0.92} />

            {(selected || hovered) && (
              // 「电流」层：短亮条沿路径从上游滑向下游（dashoffset 动画在
              // canvas.css 的 .connection-flow）。只在 hover/选中时渲染——
              // 常驻的话大画布上满屏爬光条，视觉噪音太大。
              <path
                d={path}
                stroke="#93c5fd"
                strokeWidth={2}
                fill="none"
                strokeLinecap="round"
                strokeDasharray="14 78"
                className="connection-flow"
              />
            )}

            <circle cx={startX} cy={startY} r={4.5} fill={stroke} stroke="#09090b" strokeWidth={2} />
            <circle cx={endX} cy={endY} r={4.5} fill={stroke} stroke="#09090b" strokeWidth={2} />

            <path
              d={path}
              stroke="transparent"
              strokeWidth={18}
              fill="none"
              className="connection-hit cursor-pointer"
              onClick={(e) => {
                e.stopPropagation();
              }}
            />
            {/* 红点——可见的删除按钮。点击 handler 挂在这里，让中心可点击。 */}
            <circle
              cx={midX}
              cy={midY}
              r={6.5}
              fill="#ef4444"
              opacity={deleteOpacity}
              className="connection-delete transition-opacity cursor-pointer"
              onClick={(e) => {
                e.stopPropagation();
                removeConnection(conn.id);
              }}
            />
            {/* 更大的透明命中区域，方便在红点周围更容易点中 */}
            <circle
              cx={midX}
              cy={midY}
              r={11}
              fill="transparent"
              pointerEvents="all"
              className="cursor-pointer"
              onClick={(e) => {
                e.stopPropagation();
                removeConnection(conn.id);
              }}
            />
          </g>
        );
      })}
    </svg>
  );
}
