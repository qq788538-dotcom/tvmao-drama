// 宫格切分的交互层：
//   1. GridSplitMenu —— 「宫格切分」下拉（4/9/16/25 宫格 + 自定义 hover 行列选择器）；
//   2. GridSplitModeBar —— 选完规格后的模式工具条（✕ / 已选计数 / 创建分镜组 /
//      「生成高清图片·2倍」占位——付费超分，暂不实现，
//      保留按钮但禁点、hover 提示未实现）；
//   3. GridSplitOverlay —— 叠在节点上的网格：行列编号、逐格点选/反选。
// 状态由 ImageActionsBar 托管（模式生命周期要跨越工具条显隐，同 MultiAnglePanel）。
import { useState } from 'react';
import { ChevronDown, Grid3x3, Sparkles, X } from 'lucide-react';
import { GRID_PRESETS, MAX_CUSTOM_GRID } from './gridSplitMath';

export interface GridSplitMode {
  rows: number;
  cols: number;
  selected: Set<string>; // `${row}-${col}`，0 基
}

const menuItemCls =
  'w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-[13px] border-none cursor-pointer transition-colors bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60';

// 下拉容器样式抄同文件邻居「九宫格」下拉，保持两个菜单视觉一致。
const menuPanelCls =
  'rounded-2xl border border-solid border-white/25 bg-muted p-1 shadow-[0_12px_32px_rgba(0,0,0,0.55)]';

export function GridSplitMenu({ onPick }: { onPick: (rows: number, cols: number) => void }) {
  const [customOpen, setCustomOpen] = useState(false);
  const [hoverCell, setHoverCell] = useState<{ r: number; c: number } | null>(null);

  const stopMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };

  return (
    <div className={`absolute left-0 top-[calc(100%+10px)] w-44 z-50 ${menuPanelCls}`}>
      {GRID_PRESETS.map((p) => (
        <button
          key={p.label}
          type="button"
          onMouseDown={stopMouseDown}
          onClick={(e) => {
            e.stopPropagation();
            onPick(p.rows, p.cols);
          }}
          className={menuItemCls}
        >
          <span className="truncate">{p.label}</span>
        </button>
      ))}
      <div className="h-px bg-white/15 my-1 mx-2" />
      {/* 自定义：hover 弹右侧行列选择器（「表格插入」式交互）。开合挂在
          「行 + 面板」的公共容器上，鼠标从行移进面板不会中途关闭。 */}
      <div
        className="relative"
        onMouseEnter={() => setCustomOpen(true)}
        onMouseLeave={() => {
          setCustomOpen(false);
          setHoverCell(null);
        }}
      >
        <button type="button" onMouseDown={stopMouseDown} className={menuItemCls}>
          <span className="flex-1 text-left">自定义</span>
          <ChevronDown size={12} className="-rotate-90 opacity-60" />
        </button>
        {customOpen && (
          <div className={`absolute left-[calc(100%+8px)] top-0 p-3 z-50 ${menuPanelCls}`}>
            <div className="text-xs text-foreground/60 mb-2 whitespace-nowrap">
              {hoverCell ? `${hoverCell.r + 1} × ${hoverCell.c + 1}` : '自定义宫格'}
            </div>
            <div
              className="grid gap-1"
              style={{ gridTemplateColumns: `repeat(${MAX_CUSTOM_GRID}, 24px)` }}
            >
              {Array.from({ length: MAX_CUSTOM_GRID * MAX_CUSTOM_GRID }, (_, i) => {
                const r = Math.floor(i / MAX_CUSTOM_GRID);
                const c = i % MAX_CUSTOM_GRID;
                const lit = hoverCell !== null && r <= hoverCell.r && c <= hoverCell.c;
                return (
                  <button
                    key={i}
                    type="button"
                    aria-label={`${r + 1}×${c + 1}`}
                    onMouseDown={stopMouseDown}
                    onMouseEnter={() => setHoverCell({ r, c })}
                    onClick={(e) => {
                      e.stopPropagation();
                      onPick(r + 1, c + 1);
                    }}
                    className={`w-6 h-6 rounded border-none cursor-pointer transition-colors ${
                      lit ? 'bg-accent' : 'bg-white/10 hover:bg-white/20'
                    }`}
                  />
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export function GridSplitModeBar({
  mode,
  busy,
  onClose,
  onCreate,
}: {
  mode: GridSplitMode;
  busy: boolean;
  onClose: () => void;
  onCreate: () => void;
}) {
  const n = mode.selected.size;
  const canCreate = n > 0 && !busy;
  return (
    <>
      <button
        type="button"
        title="退出宫格切分"
        onMouseDown={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        onClick={(e) => {
          e.stopPropagation();
          onClose();
        }}
        className="w-8 h-8 rounded-lg flex items-center justify-center border-none cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60 transition-colors"
      >
        <X size={14} />
      </button>
      <div className="w-px h-4 bg-border mx-1 shrink-0" />
      <div className="flex items-center gap-1.5 px-2 text-[13px] text-foreground/60 whitespace-nowrap">
        <Grid3x3 size={14} />
        <span>{n === 0 ? '请选择宫格进行操作' : `已选 ${n} 个宫格`}</span>
      </div>
      <button
        type="button"
        onMouseDown={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        onClick={(e) => {
          e.stopPropagation();
          if (canCreate) onCreate();
        }}
        className={`h-8 px-2 rounded-lg flex items-center gap-1.5 border-none text-[13px] whitespace-nowrap transition-colors ${
          canCreate
            ? 'cursor-pointer bg-transparent text-foreground/80 hover:text-foreground hover:bg-accent/60'
            : 'cursor-not-allowed bg-transparent text-foreground/30'
        }`}
      >
        {busy ? '切分中…' : '创建分镜组'}
      </button>
      <div className="w-px h-4 bg-border mx-1 shrink-0" />
      {/* 「生成高清图片 · 2倍」是付费超分，暂不实现。
          不用原生 disabled——部分浏览器不给禁用元素弹 title，hover 提示会失效。 */}
      <span title="该功能未实现">
        <span className="h-8 px-2 rounded-lg flex items-center gap-1.5 text-[13px] whitespace-nowrap cursor-not-allowed text-foreground/30 select-none">
          <Sparkles size={14} />
          生成高清图片
        </span>
      </span>
      <span title="该功能未实现">
        <span className="h-8 px-2 rounded-lg flex items-center gap-1 text-[13px] whitespace-nowrap cursor-not-allowed text-foreground/30 select-none">
          2倍
          <ChevronDown size={12} />
        </span>
      </span>
    </>
  );
}

export function GridSplitOverlay({
  mode,
  onToggle,
}: {
  mode: GridSplitMode;
  onToggle: (key: string) => void;
}) {
  return (
    <div
      className="absolute inset-0 z-30 grid"
      style={{
        gridTemplateColumns: `repeat(${mode.cols}, 1fr)`,
        gridTemplateRows: `repeat(${mode.rows}, 1fr)`,
      }}
      // 网格层接管节点上的鼠标事件：点格子不能被画布当成拖节点/框选起点。
      onMouseDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {Array.from({ length: mode.rows * mode.cols }, (_, i) => {
        const r = Math.floor(i / mode.cols);
        const c = i % mode.cols;
        const key = `${r}-${c}`;
        const active = mode.selected.has(key);
        return (
          <button
            key={key}
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onToggle(key);
            }}
            className={`flex items-center justify-center cursor-pointer transition-colors border-solid ${
              active
                ? 'border-2 border-cyan-400 bg-cyan-400/15'
                : 'border border-white/40 bg-transparent hover:bg-white/10'
            }`}
          >
            <span className="text-xs text-white/85 [text-shadow:0_1px_2px_rgba(0,0,0,0.8)] select-none">
              {r + 1}-{c + 1}
            </span>
          </button>
        );
      })}
    </div>
  );
}
