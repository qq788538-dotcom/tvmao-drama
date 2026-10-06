// 音频分区的卡片网格。
//
// 音频跟其它三个分区的形态不同，所以不复用 StoryboardCard/ZoneList：
// 音频没有可看的画面，信息量只有「图标 + 时长 + 名字」，做成竖方块比横条
// 省地方，一行能并排放好几个。
//
// 高度策略：**自适应内容，最多三行**。一个音频时这栏就一行高，左栏剩下的
// 高度全归下面那个槽位；音频变多了自动长高，到三行封顶、内部纵向滚动。
// 不用横向滚动——横向滚会把后面的卡片藏在视口外，而这栏的全部价值就是
// 一眼扫完有哪些音频。
import { Music } from 'lucide-react';
import { displayLabelFor } from '../editor/NodeShell';
import type { CanvasNode } from '../editor/types';

// 一张卡片的总高：96 方块 + 6 间距 + 16 文字 = 118；行与行之间 12。
// 三行封顶 = 118*3 + 12*2 = 378。写成常量是为了让这几个数的来历留在代码里，
// 下次调方块尺寸时知道 378 要跟着重算。
const CARD_H = 118;
const ROW_GAP = 12;
const MAX_ROWS = 3;
const MAX_H = CARD_H * MAX_ROWS + ROW_GAP * (MAX_ROWS - 1);

/** 秒 → mm:ss。拿不到时长就不显示，不猜也不填 00:00。 */
function formatDuration(seconds: unknown): string | null {
  const s = typeof seconds === 'number' ? seconds : Number(seconds);
  if (!Number.isFinite(s) || s <= 0) return null;
  const m = Math.floor(s / 60);
  const rest = Math.floor(s % 60);
  return `${String(m).padStart(2, '0')}:${String(rest).padStart(2, '0')}`;
}

function AudioCard({
  node,
  selected,
  onSelect,
}: {
  node: CanvasNode;
  selected: boolean;
  onSelect: (nodeId: string) => void;
}) {
  const title = displayLabelFor(node);
  const duration = formatDuration(node.settings?.duration);

  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={() => onSelect(node.id)}
      className="shrink-0 w-[96px] bg-transparent border-none p-0 cursor-pointer flex flex-col gap-1.5 items-center"
    >
      <div
        className={`w-[96px] h-[96px] rounded-lg bg-card flex flex-col items-center justify-center gap-1 ${
          selected ? 'ring-1 ring-primary' : ''
        }`}
      >
        <Music size={22} className="text-muted-foreground" />
        {duration && <span className="text-muted-foreground text-[11px]">{duration}</span>}
      </div>
      <span className="w-full text-center text-sidebar-foreground text-[11px] truncate" title={title}>
        {title}
      </span>
    </button>
  );
}

export function AudioGrid({
  nodes,
  focusedId,
  onSelect,
}: {
  nodes: CanvasNode[];
  focusedId: string | null;
  onSelect: (nodeId: string) => void;
}) {
  if (nodes.length === 0) {
    return (
      <div className="h-[80px] flex items-center justify-center text-muted-foreground text-xs">
        暂无音频
      </div>
    );
  }

  return (
    <div className="overflow-y-auto px-3 pb-3" style={{ maxHeight: MAX_H }}>
      <div className="flex flex-wrap" style={{ gap: ROW_GAP }}>
        {nodes.map((n) => (
          <AudioCard key={n.id} node={n} selected={n.id === focusedId} onSelect={onSelect} />
        ))}
      </div>
    </div>
  );
}
