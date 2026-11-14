import { useCallback, useEffect, useRef, useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useCanvasStore } from './store';

const MAP_W = 200;
const MAP_H = 140;
const PADDING = 0.1;

const NODE_COLORS: Record<string, string> = {
  'input-image': '#38bdf8',
  'text-node': '#a78bfa',
  'gen-image': '#34d399',
  'gen-video': '#f472b6',
  'gen-audio': '#fbbf24',
  'video-input': '#3b82f6',
  'image-compare': '#22d3ee',
  'local-save': '#fb923c',
  'preview': '#fbbf24',
  'director-desk': '#c084fc',
};

export function Minimap() {
  const nodes = useCanvasStore((s) => s.nodes);
  const connections = useCanvasStore((s) => s.connections);
  const view = useCanvasStore((s) => s.view);
  const setView = useCanvasStore((s) => s.setView);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [collapsed, setCollapsed] = useState(false);
  const dragging = useRef(false);

  const getBounds = useCallback(() => {
    if (nodes.length === 0) return null;
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.x + n.width > maxX) maxX = n.x + n.width;
      if (n.y + n.height > maxY) maxY = n.y + n.height;
    }
    const padX = (maxX - minX) * PADDING || 100;
    const padY = (maxY - minY) * PADDING || 100;
    return { minX: minX - padX, minY: minY - padY, maxX: maxX + padX, maxY: maxY + padY };
  }, [nodes]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || collapsed) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    // 只在尺寸（含 dpr 变化）不同时才设 width/height：给 canvas 赋尺寸会清空
    // 位图并重建 backing store，平移/拖动每帧重画时没必要（P32）。也因此缩放
    // 改用 setTransform 设成绝对值——scale 是在现有变换上累乘，不重设尺寸的话
    // 每帧都会再乘一次 dpr。
    const dpr = window.devicePixelRatio || 1;
    const pixelW = MAP_W * dpr;
    const pixelH = MAP_H * dpr;
    if (canvas.width !== pixelW) canvas.width = pixelW;
    if (canvas.height !== pixelH) canvas.height = pixelH;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    // 清空
    ctx.clearRect(0, 0, MAP_W, MAP_H);
    ctx.fillStyle = '#0a0a0c';
    ctx.fillRect(0, 0, MAP_W, MAP_H);

    const bounds = getBounds();
    if (!bounds) return;

    const bw = bounds.maxX - bounds.minX;
    const bh = bounds.maxY - bounds.minY;
    if (bw <= 0 || bh <= 0) return;

    const scale = Math.min(MAP_W / bw, MAP_H / bh);
    const offsetX = (MAP_W - bw * scale) / 2;
    const offsetY = (MAP_H - bh * scale) / 2;

    const toMap = (wx: number, wy: number) => ({
      x: (wx - bounds.minX) * scale + offsetX,
      y: (wy - bounds.minY) * scale + offsetY,
    });

    // 画 connection
    ctx.strokeStyle = '#3f3f46';
    ctx.lineWidth = 0.5;
    // 平移/拖动每帧都重画：按 id 建索引，别对每条连线线性扫两遍节点（O(E·N)）。
    const byId = new Map(nodes.map((n) => [n.id, n]));
    for (const conn of connections) {
      const from = byId.get(conn.from);
      const to = byId.get(conn.to);
      if (!from || !to) continue;
      const a = toMap(from.x + from.width, from.y + from.height / 2);
      const b = toMap(to.x, to.y + to.height / 2);
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }

    // 画 node
    for (const n of nodes) {
      const p = toMap(n.x, n.y);
      const w = Math.max(2, n.width * scale);
      const h = Math.max(2, n.height * scale);
      ctx.fillStyle = NODE_COLORS[n.type] || '#71717a';
      ctx.globalAlpha = 0.8;
      ctx.fillRect(p.x, p.y, w, h);
      ctx.globalAlpha = 1;
    }

    // 画 viewport 矩形
    // viewport 的世界坐标：需要 canvas 容器的实际尺寸
    // 这里用窗口尺寸减去侧栏来近似
    const vpW = (window.innerWidth - 56) / view.zoom;
    const vpH = (window.innerHeight - 56) / view.zoom;
    const vpX = -view.x / view.zoom;
    const vpY = -view.y / view.zoom;

    const vp1 = toMap(vpX, vpY);
    const vp2 = toMap(vpX + vpW, vpY + vpH);
    const vw = vp2.x - vp1.x;
    const vh = vp2.y - vp1.y;

    ctx.strokeStyle = '#3b82f6';
    ctx.lineWidth = 1.5;
    ctx.strokeRect(vp1.x, vp1.y, vw, vh);
    ctx.fillStyle = 'rgba(59, 130, 246, 0.08)';
    ctx.fillRect(vp1.x, vp1.y, vw, vh);
  }, [nodes, connections, view, collapsed, getBounds]);

  const handleMapClick = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const bounds = getBounds();
      if (!bounds) return;
      const rect = canvasRef.current?.getBoundingClientRect();
      if (!rect) return;

      const bw = bounds.maxX - bounds.minX;
      const bh = bounds.maxY - bounds.minY;
      const scale = Math.min(MAP_W / bw, MAP_H / bh);
      const offsetX = (MAP_W - bw * scale) / 2;
      const offsetY = (MAP_H - bh * scale) / 2;

      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;

      const worldX = (mx - offsetX) / scale + bounds.minX;
      const worldY = (my - offsetY) / scale + bounds.minY;

      const vpW = (window.innerWidth - 56) / view.zoom;
      const vpH = (window.innerHeight - 56) / view.zoom;

      setView({
        x: -(worldX - vpW / 2) * view.zoom,
        y: -(worldY - vpH / 2) * view.zoom,
        zoom: view.zoom,
      });
    },
    [getBounds, view, setView],
  );

  const onMouseDown = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      e.stopPropagation();
      e.preventDefault();
      dragging.current = true;
      handleMapClick(e);

      const onMove = (ev: MouseEvent) => {
        if (!dragging.current) return;
        const rect = canvasRef.current?.getBoundingClientRect();
        if (!rect) return;
        const bounds = getBounds();
        if (!bounds) return;

        const bw = bounds.maxX - bounds.minX;
        const bh = bounds.maxY - bounds.minY;
        const scale = Math.min(MAP_W / bw, MAP_H / bh);
        const offsetX = (MAP_W - bw * scale) / 2;
        const offsetY = (MAP_H - bh * scale) / 2;

        const mx = ev.clientX - rect.left;
        const my = ev.clientY - rect.top;
        const worldX = (mx - offsetX) / scale + bounds.minX;
        const worldY = (my - offsetY) / scale + bounds.minY;

        const vpW = (window.innerWidth - 56) / view.zoom;
        const vpH = (window.innerHeight - 56) / view.zoom;

        setView({
          x: -(worldX - vpW / 2) * view.zoom,
          y: -(worldY - vpH / 2) * view.zoom,
          zoom: view.zoom,
        });
      };

      const onUp = () => {
        dragging.current = false;
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };

      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    },
    [handleMapClick, getBounds, view, setView],
  );

  if (nodes.length === 0) return null;

  return (
    <div
      className="fixed right-4 bottom-14 z-30 rounded-lg overflow-hidden border border-input shadow-lg"
      style={{ width: MAP_W }}
      onWheel={(e) => e.stopPropagation()}
      onMouseDown={(e) => e.stopPropagation()}
      onContextMenu={(e) => { e.preventDefault(); e.stopPropagation(); }}
    >
      <div className="flex items-center justify-between px-2 py-1 bg-card/95 border-b border-border">
        <span className="text-[9px] text-muted-foreground select-none">小地图</span>
        <button
          onClick={() => setCollapsed((v) => !v)}
          className="p-0.5 rounded bg-transparent border-none cursor-pointer text-muted-foreground hover:text-sidebar-foreground"
        >
          {collapsed ? <ChevronUp size={10} /> : <ChevronDown size={10} />}
        </button>
      </div>
      {!collapsed && (
        <canvas
          ref={canvasRef}
          style={{ width: MAP_W, height: MAP_H, display: 'block', cursor: 'crosshair' }}
          onMouseDown={onMouseDown}
        />
      )}
    </div>
  );
}
