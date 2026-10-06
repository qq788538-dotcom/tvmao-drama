import { useEffect, useMemo, useRef, useState } from 'react';
import { FileText, Search } from 'lucide-react';

import type { NodeSummary } from './types';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export interface MentionPickerProps {
  query: string;
  nodes: NodeSummary[];
  onPick: (node: NodeSummary) => void;
  onClose?: () => void;
}

// 当用户正在输入 `@mention` 时，MentionPicker 会浮在聊天输入框上方。顶部有一个
// 专门的搜索框，画布节点很多时让用户能按名字模糊匹配；搜索框会预填 `@` 之后输入的
// 内容并自动聚焦，这样继续输入能自然地接上。
export function MentionPicker({ query, nodes, onPick, onClose }: MentionPickerProps) {
  const [searchQuery, setSearchQuery] = useState(query);
  const inputRef = useRef<HTMLInputElement>(null);

  // 每当 picker 带着新的 `@query` 重新打开时，保持搜索词同步。
  useEffect(() => {
    setSearchQuery(query);
  }, [query]);

  // 自动聚焦搜索框，这样即使焦点在视觉上已经从编辑器移走，继续输入仍能过滤。
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const ql = searchQuery.toLowerCase();
  const matches = useMemo(
    () =>
      nodes.filter(
        (n) => (n.name ?? '').toLowerCase().includes(ql) || n.id.toLowerCase().includes(ql),
      ),
    [nodes, ql],
  );
  return (
    <div
      className="absolute left-3 bottom-full mb-1 w-72 bg-popover border-2 border-solid border-blue-500/60 rounded-xl shadow-2xl z-30 flex flex-col overflow-hidden ring-1 ring-black/40"
      onMouseDown={(e) => e.stopPropagation()}
    >
      <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-border">
        <Search size={12} className="text-muted-foreground shrink-0" />
        <input
          ref={inputRef}
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault();
              onClose?.();
              return;
            }
            if (e.key === 'Enter' && matches.length > 0 && !e.nativeEvent.isComposing) {
              e.preventDefault();
              onPick(matches[0]);
            }
          }}
          placeholder="搜索画布节点…"
          className="flex-1 bg-transparent border-none outline-none text-xs text-foreground placeholder:text-muted-foreground"
        />
      </div>
      <div className="max-h-52 overflow-y-auto custom-scrollbar">
      {matches.length === 0 ? (
        <div className="px-3 py-3 text-[11px] text-muted-foreground text-center">无匹配节点</div>
      ) : (
        matches.map((n) => {
        const isVideo = n.kind === 'video';
        const isAudio = n.kind === 'audio';
        const isText = n.kind === 'text';
        return (
          <button
            key={n.id}
            type="button"
            onMouseDown={(e) => {
              e.preventDefault();
              onPick(n);
            }}
            className="flex items-center gap-2 px-2 py-1.5 w-full text-left bg-transparent border-none border-b border-b-border hover:bg-popover cursor-pointer"
          >
            {isText ? (
              // 文字节点没有 URL/缩略图，用文档图标占同一格位，行高与媒体行对齐。
              <div className="w-7 h-7 rounded shrink-0 bg-popover flex items-center justify-center text-blue-300">
                <FileText size={14} />
              </div>
            ) : isAudio ? (
              <div className="w-7 h-7 rounded shrink-0 bg-popover flex items-center justify-center text-blue-300 text-base">
                ♪
              </div>
            ) : isVideo && n.url ? (
              <video
                src={n.url} crossOrigin={mediaCrossOrigin(n.url)}
                muted
                // preload="none"：候选列表不再截断（canvasSummary 全量返回），
                // 大画布下敲一个 @ 就会同时挂上百个媒体请求；缩略帧不值这个
                // 并发代价，视频行退化为占位底色即可。
                preload="none"
                playsInline
                className="w-7 h-7 object-cover rounded shrink-0 bg-background pointer-events-none"
              />
            ) : n.url ? (
              <img
                src={n.url}
                crossOrigin={mediaCrossOrigin(n.url)}
                alt=""
                // 同上：全量候选下只加载滚进视口的缩略图。
                loading="lazy"
                className="w-7 h-7 object-cover rounded shrink-0 bg-background"
              />
            ) : null}
            <div className="text-xs min-w-0 flex-1">
              <div className="font-semibold text-foreground truncate">{n.name || '(未命名)'}</div>
              <div className="text-muted-foreground text-[11px] truncate">
                {n.type} · {n.id.slice(0, 6)}
              </div>
            </div>
          </button>
        );
      })
      )}
      </div>
    </div>
  );
}
