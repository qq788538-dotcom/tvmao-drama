// 单个分区的滚动列表。节点数会过百（用户判断），全渲染会把带视频的卡片
// 一次性挂满 DOM，所以这里直接上虚拟化：滚动距离照旧是全量高度，实际挂载的
// 只有可视区那几张。
//
// estimateSize 只是首屏估值；真实高度由 measureElement 在挂载后测出来回填
// ——图片卡片高度随图片比例变，固定行高会让滚动条和内容对不上。
//
// 不写单测：jsdom 里所有元素高度都是 0，虚拟化算出来的可视区恒为空或全量，
// 测了也测不出真实行为。正确性由 Task 6 的布局渲染路径测试 + 实际数据量的
// 手动验证兜住——这是有意识的取舍，不是漏了。
import { useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { StoryboardCard } from './StoryboardCard';
import type { CanvasNode } from '../editor/types';

export function ZoneList({
  nodes,
  focusedId,
  onSelect,
  emptyText = '暂无内容',
}: {
  nodes: CanvasNode[];
  focusedId: string | null;
  onSelect: (nodeId: string) => void;
  emptyText?: string;
}) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const virtualizer = useVirtualizer({
    count: nodes.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 200,
    overscan: 4,
  });

  if (nodes.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-muted-foreground text-xs">
        {emptyText}
      </div>
    );
  }

  return (
    <div ref={scrollRef} className="flex-1 min-h-0 overflow-auto">
      <div style={{ height: virtualizer.getTotalSize(), position: 'relative', width: '100%' }}>
        {virtualizer.getVirtualItems().map((item) => {
          const node = nodes[item.index];
          return (
            <div
              key={node.id}
              data-index={item.index}
              ref={virtualizer.measureElement}
              style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                transform: `translateY(${item.start}px)`,
              }}
            >
              <StoryboardCard node={node} selected={node.id === focusedId} onSelect={onSelect} />
              {/* 条目之间的分割线。挂在条目内部的末尾而不是用 border-b —— 虚拟
                  列表里 border 会算进 measureElement 量到的高度，滚动位置跟着漂。
                  最后一条不画：列表末尾悬一条线像是还有内容没加载出来。

                  颜色不用 border-border 那个 token（rgba(255,255,255,0.10)）：
                  面板底色是 #262626=rgb(38,38,38)，叠上 10% 白只有 rgb(60,60,60)，
                  1px 线在这种对比度下肉眼看不见——线画了等于没画。0.18 是实测能
                  看清又不喧宾夺主的下限。面板底色再变亮的话这个值要跟着重估。 */}
              {item.index < nodes.length - 1 && (
                <div
                  data-testid="zone-list-divider"
                  className="mx-2"
                  style={{ borderTop: '1px solid rgba(255,255,255,0.18)' }}
                />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
