import { memo, useState } from 'react';
import { useCanvasStore } from './store';

// 自己订阅 nodesById / connections，不再从 CanvasEditor 收 nodes：父组件在平移
//（每帧改 view）、框选（每帧改 marquee）时都会重渲染，连线层只在节点几何、连线
// 或选中真的变了时才需要重算全部路径（P14）。memo 挡住的就是父组件的这些重渲染。
export const ConnectionLayer = memo(function ConnectionLayer() {
  const connections = useCanvasStore((s) => s.connections);
  const nodeMap = useCanvasStore((s) => s.nodesById);
  const removeConnection = useCanvasStore((s) => s.removeConnection);
  const selectedIds = useCanvasStore((s) => s.selectedIds);
  const [hoveredId, setHoveredId] = useState<string | null>(null);

  return (
    <svg
      className="absolute overflow-visible pointer-events-none"
      style={{ left: 0, top: 0, width: 1, height: 1 }}
    >
      {connections.map((conn) => {
        const from = nodeMap.get(conn.from);
        const to = nodeMap.get(conn.to);
        if (!from || !to) return null;

        const startX = from.x + from.width;
        const startY = from.y + from.height / 2;
        const endX = to.x;
        const endY = to.y + to.height / 2;
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
});
