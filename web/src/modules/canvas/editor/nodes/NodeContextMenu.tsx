import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

export interface NodeMenuItem {
  key: string;
  label: string;
  onPick: () => void;
  disabled?: boolean;
  danger?: boolean; // 破坏性动作（删除）标红警示
  hint?: string; // 置灰原因/补充说明，hover 显示
  // tip 有值时在标签右侧挂一个 ? ，hover 展开这块内容。用来放「素材要求」这类
  // 篇幅超出一行 title 的说明——原生 title 撑不住多行排版。
  tip?: ReactNode;
  // 快捷键**文案**（如 ⌘C），只负责显示；真实按键处理在 CanvasEditor 的全局
  // 监听。对标 LibTV 右键菜单右对齐的快捷键提示。
  shortcut?: string;
}

// TipButton 是菜单项右侧的 ?。卡片必须 portal 到 body：菜单容器是
// `w-52 overflow-hidden`，嵌在里面的卡片会被直接裁掉。
function TipButton({ content, label }: { content: ReactNode; label: string }) {
  const anchorRef = useRef<HTMLSpanElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);

  const open = () => setRect(anchorRef.current?.getBoundingClientRect() ?? null);
  const close = () => setRect(null);

  // 优先放锚点右侧；右侧空间不够就翻到左侧。垂直方向以锚点为中心，再夹进视口。
  const CARD_W = 300;
  let left = 0;
  let top = 0;
  if (rect) {
    left = rect.right + 10;
    if (left + CARD_W > window.innerWidth - 8) left = Math.max(8, rect.left - CARD_W - 10);
    top = Math.max(8, Math.min(rect.top - 8, window.innerHeight - 200));
  }

  return (
    <>
      <span
        ref={anchorRef}
        // 按菜单项命名——一个菜单里可能有多个 ?（标配两项+合规校验都带 tip），
        // 写死同一个 label 无障碍上无法区分，测试也没法精确选中。
        aria-label={`查看「${label}」说明`}
        // 圆圈描边用 muted-foreground 而不是 border token——菜单底是 bg-muted，
        // border-border 和它几乎同色，圆圈糊没了只剩一个孤问号（用户实测对比
        // LibTV 的 ⓘ 圈型标记）。
        className="shrink-0 w-3.5 h-3.5 flex items-center justify-center rounded-full border border-solid border-muted-foreground/60 text-[9px] leading-none text-muted-foreground cursor-help select-none hover:text-foreground hover:border-foreground"
        onMouseEnter={open}
        onMouseLeave={close}
        // ? 只负责解释，不该顺手触发这一项的动作。
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        onMouseDown={(e) => e.stopPropagation()}
      >
        ?
      </span>
      {rect &&
        createPortal(
          <div
            className="fixed z-[310] rounded-xl border border-solid border-white/25 bg-muted p-3 shadow-[0_12px_32px_rgba(0,0,0,0.55)] text-xs leading-relaxed text-foreground"
            style={{ left, top, width: CARD_W }}
          >
            {content}
          </div>,
          document.body,
        )}
    </>
  );
}

// 生成结果节点的右键菜单。原来 GenImageNode / GenVideoNode 各抄了一份只有
// 「发送到画布」一项的实现，加第二项时没必要抄第三遍——抽出来共用。
export function NodeContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number;
  y: number;
  items: NodeMenuItem[];
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ left: x, top: y });

  useEffect(() => {
    const md = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const kd = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('mousedown', md);
    window.addEventListener('keydown', kd);
    return () => {
      window.removeEventListener('mousedown', md);
      window.removeEventListener('keydown', kd);
    };
  }, [onClose]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const margin = 12;
    const maxLeft = Math.max(margin, window.innerWidth - el.offsetWidth - margin);
    const maxTop = Math.max(margin, window.innerHeight - el.offsetHeight - margin);
    setPosition({
      left: Math.min(Math.max(margin, x), maxLeft),
      top: Math.min(Math.max(margin, y), maxTop),
    });
  }, [x, y]);

  // portal 到 document.body，让菜单逃出画布视口那个带
  // `transform: translate(...) scale(...)` 的祖先（CanvasEditor.tsx）。
  // 没有这个的话，`position: fixed` 会相对那个被 transform 的祖先来定位——
  // clientX/clientY 会被缩放+偏移，在常见的 zoom/pan 值下把菜单推出屏幕。
  return createPortal(
    <div
      ref={ref}
      // 实底 + 提亮边框 + 重投影：菜单叠在深色节点上时，半透明 bg-card/85 会和
      // 节点糊成一片（用户实测「边框分界不明显、透明看着费眼睛」）。bg-muted(#2b2b2b)
      // 比节点面(#1f1f1f)亮一档，对标上游右键菜单的实底面板。
      className="fixed z-[300] w-52 overflow-hidden rounded-2xl border border-solid border-white/25 bg-muted canvas-menu-enter p-1 shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
      style={{ left: position.left, top: position.top }}
      onContextMenu={(e) => e.preventDefault()}
      onMouseDown={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
    >
      {items.map((it) => (
        // 行容器承接 hover 高亮：快捷键文案在 button 之外，高亮挂 button 上的话
        // 鼠标滑到快捷键那截行尾会掉色。
        <div
          key={it.key}
          className={`flex items-center rounded-lg ${it.disabled ? '' : 'hover:bg-accent'}`}
        >
          <button
            type="button"
            disabled={it.disabled}
            title={it.hint}
            // ? 紧跟在文字后面（对标 LibTV「复制节点 ⍰」），所以 TipButton 嵌进
            // button 内部——它是 span（合法嵌套），自己 stopPropagation，点 ? 不会
            // 触发这一项；button 保持 flex-1，整行空白区仍可点。
            className={`flex-1 min-w-0 flex items-center gap-1.5 text-left px-3 py-2 rounded-lg text-sm border-none bg-transparent outline-none ${
              it.disabled
                ? 'text-muted-foreground opacity-50 cursor-not-allowed'
                : it.danger
                  ? 'text-red-400 cursor-pointer'
                  : 'text-foreground cursor-pointer'
            }`}
            onClick={() => {
              if (it.disabled) return;
              it.onPick();
            }}
            onMouseDown={(e) => e.stopPropagation()}
          >
            <span className="truncate">{it.label}</span>
            {it.tip ? <TipButton content={it.tip} label={it.label} /> : null}
          </button>
          {it.shortcut ? (
            <span className="shrink-0 mr-2.5 text-xs text-muted-foreground/70 select-none">{it.shortcut}</span>
          ) : null}
        </div>
      ))}
    </div>,
    document.body,
  );
}
