import type { CanvasNode, Connection } from '../types';

interface GraphNode {
  id: string;
  node: CanvasNode;
  parents: string[];
  children: string[];
  level: number;
  barycenter?: number;
}

export function autoArrangeNodes(
  allNodes: CanvasNode[],
  connections: Connection[],
  selectedIds: Set<string>,
): CanvasNode[] {
  const nodesToArrange = allNodes.filter((n) => selectedIds.has(n.id));
  if (nodesToArrange.length < 2) return allNodes;

  const targetNodeIds = new Set(nodesToArrange.map((n) => n.id));

  // 构建图
  const graph: Record<string, GraphNode> = {};
  nodesToArrange.forEach((n) => {
    graph[n.id] = { id: n.id, node: n, parents: [], children: [], level: 0 };
  });

  connections.forEach((conn) => {
    if (targetNodeIds.has(conn.from) && targetNodeIds.has(conn.to)) {
      graph[conn.from].children.push(conn.to);
      graph[conn.to].parents.push(conn.from);
    }
  });

  // 最长路径分层（Longest Path Layering）
  let roots = Object.values(graph).filter((n) => n.parents.length === 0);
  if (roots.length === 0 && nodesToArrange.length > 0) {
    roots = [Object.values(graph)[0]];
  }

  const queue: Array<{ node: GraphNode; lvl: number }> = roots.map((r) => ({ node: r, lvl: 0 }));
  while (queue.length > 0) {
    const { node, lvl } = queue.shift()!;
    if (lvl >= node.level) {
      node.level = lvl;
      node.children.forEach((childId) => {
        const child = graph[childId];
        if (child && lvl < 20) {
          queue.push({ node: child, lvl: lvl + 1 });
        }
      });
    }
  }

  // 构建各层
  const maxLevel = Math.max(...Object.values(graph).map((n) => n.level));
  const layers: GraphNode[][] = Array.from({ length: maxLevel + 1 }, () => []);
  Object.values(graph).forEach((n) => layers[n.level].push(n));

  // 先按 Y 位置做初始排序
  layers.forEach((layer) => layer.sort((a, b) => a.node.y - b.node.y));

  // 交叉最小化——迭代重心法（Iterative Barycenter，跑 3 遍）
  for (let i = 0; i < 3; i++) {
    // 正向扫描
    for (let l = 1; l < layers.length; l++) {
      const layer = layers[l];
      layer.forEach((n) => {
        if (n.parents.length > 0) {
          let sum = 0;
          n.parents.forEach((pid) => {
            const idx = layers[l - 1].indexOf(graph[pid]);
            if (idx !== -1) sum += idx;
          });
          n.barycenter = sum / n.parents.length;
        } else {
          n.barycenter = layers[l].indexOf(n);
        }
      });
      layer.sort((a, b) => (a.barycenter ?? 0) - (b.barycenter ?? 0));
    }

    // 反向扫描
    for (let l = layers.length - 2; l >= 0; l--) {
      const layer = layers[l];
      layer.forEach((n) => {
        if (n.children.length > 0) {
          let sum = 0;
          n.children.forEach((cid) => {
            const idx = layers[l + 1].indexOf(graph[cid]);
            if (idx !== -1) sum += idx;
          });
          n.barycenter = sum / n.children.length;
        } else {
          n.barycenter = layers[l].indexOf(n);
        }
      });
      layer.sort((a, b) => (a.barycenter ?? 0) - (b.barycenter ?? 0));
    }
  }

  // 坐标分配
  const startX = Math.min(...nodesToArrange.map((n) => n.x));
  const startY = Math.min(...nodesToArrange.map((n) => n.y));
  const H_SPACING = 150;
  const V_SPACING = 40;

  let currentX = startX;
  const updatedMap = new Map<string, CanvasNode>();

  layers.forEach((layer) => {
    if (layer.length === 0) return;
    const maxW = Math.max(...layer.map((n) => n.node.width || 260));
    let currentY = startY;

    layer.forEach((gn) => {
      updatedMap.set(gn.id, { ...gn.node, x: currentX, y: currentY });
      currentY += (gn.node.height || 200) + V_SPACING;
    });

    currentX += maxW + H_SPACING;
  });

  return allNodes.map((node) => updatedMap.get(node.id) ?? node);
}
