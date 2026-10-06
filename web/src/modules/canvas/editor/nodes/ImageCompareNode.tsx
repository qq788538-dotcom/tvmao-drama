import { useCallback, useRef, useState } from 'react';
import { Maximize2, Split } from 'lucide-react';
import { useCanvasStore } from '../store';
import { ThumbImg } from '../ThumbImg';
import { NodeShell } from '../NodeShell';

export function ImageCompareNode({ id }: { id: string }) {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === id));
  const getConnectedInputs = useCanvasStore((s) => s.getConnectedInputs);
  const setLightbox = useCanvasStore((s) => s.setLightbox);

  const [sliderPos, setSliderPos] = useState(50);
  const containerRef = useRef<HTMLDivElement>(null);
  const dragging = useRef(false);

  if (!node) return null;

  const linked = getConnectedInputs(id);
  const imgA = linked.images[0];
  const imgB = linked.images[1];

  const handleMove = useCallback((clientX: number) => {
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect) return;
    const pct = ((clientX - rect.left) / rect.width) * 100;
    setSliderPos(Math.max(0, Math.min(100, pct)));
  }, []);

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      e.preventDefault();
      dragging.current = true;
      handleMove(e.clientX);
      const onMove = (ev: MouseEvent) => {
        if (dragging.current) handleMove(ev.clientX);
      };
      const onUp = () => {
        dragging.current = false;
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
      };
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    },
    [handleMove],
  );

  return (
    <NodeShell
      node={node}
      inputs={2}
      outputs={0}
      minWidth={400}
      minHeight={300}
    >
      {imgA && imgB ? (
        <div
          ref={containerRef}
          className="relative flex-1 rounded-md overflow-hidden bg-background border border-border select-none"
          onMouseDown={onMouseDown}
        >
          {/* B 图层（全宽，在底层） */}
          <ThumbImg
            src={imgB}
            alt="对比 B"
            className="absolute inset-0 w-full h-full object-contain"
            draggable={false}
          />
          {/* A 图层（被滑块裁切） */}
          <div
            className="absolute inset-0 overflow-hidden"
            style={{ width: `${sliderPos}%` }}
          >
            <ThumbImg
              src={imgA}
              alt="对比 A"
              className="w-full h-full object-contain"
              style={{ width: containerRef.current?.clientWidth ?? '100%' }}
              draggable={false}
            />
          </div>
          {/* 滑块分割线 */}
          <div
            className="absolute top-0 bottom-0 w-0.5 bg-white/80 cursor-col-resize z-10"
            style={{ left: `${sliderPos}%` }}
          >
            <div className="absolute top-1/2 -translate-y-1/2 -translate-x-1/2 w-6 h-6 rounded-full bg-white/90 border-2 border-sidebar-foreground shadow flex items-center justify-center">
              <Split size={12} className="text-muted-foreground" />
            </div>
          </div>
          {/* 角标 */}
          <div className="absolute top-2 left-2 text-[10px] px-1.5 py-0.5 rounded bg-black/60 text-white">
            A
          </div>
          <div className="absolute top-2 right-2 text-[10px] px-1.5 py-0.5 rounded bg-black/60 text-white">
            B
          </div>
        </div>
      ) : (
        <div className="flex-1 border border-dashed border-input rounded-md flex items-center justify-center text-muted-foreground text-xs text-center px-4">
          {!imgA && !imgB
            ? '连接两个图片节点进行对比'
            : '需要连接两个图片节点（当前仅有 1 个）'}
        </div>
      )}
      {(imgA || imgB) && (
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground shrink-0">
          <span>拖动滑块对比两张图片</span>
          {imgA && (
            <button
              className="ml-auto text-sidebar-foreground hover:text-white transition-colors"
              onClick={(e) => {
                e.stopPropagation();
                setLightbox({ url: imgA, kind: 'image', title: '对比图 A' });
              }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              <Maximize2 size={12} />
            </button>
          )}
        </div>
      )}
    </NodeShell>
  );
}
