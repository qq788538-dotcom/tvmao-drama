import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Ban, Trash2 } from 'lucide-react';
import { MIN_SHOT_SECONDS, type ColorMark } from './scriptData';

// 编辑器的三件套：白底黑字保存按钮 / 深色内嵌输入框 / 弹层辅助文案。
// 集中成常量导出（DialogueEditor、ComposeDialog 也用），改一处全弹层同步。
export const SAVE_BTN = 'rounded-lg bg-white px-4 py-1.5 text-sm font-medium text-black hover:opacity-90 border-none cursor-pointer';
export const FIELD_BOX = 'rounded-lg border border-white/10 bg-black/40 px-3 py-2 text-sm text-foreground outline-none focus:border-white/25';
export const HINT_TEXT = 'text-xs text-muted-foreground';

// 所有格内编辑器共用的弹出容器：负责三件事——
// 1. 定位（相对触发单元格 absolute 悬浮在下方）；
// 2. 点击外部/按 Esc 关闭；
// 3. 阻止内部 mousedown/click 冒泡到行/单元格，避免误触行的拖拽或再次触发单元格
//    onClick。click 必须一并挡住而不只挡 mousedown——DialogueEditor 的保存按钮
//    点击若冒泡到「对白」td 的 onClick（无条件重新 setDialogueRowId(row.id)，
//    不像其它格子那样是 toggle），会在保存关闭的同一 tick 里被冒泡出去的 click
//    重新掀开，表现为“保存后弹层又自己弹回来”。
//
// 关闭前先 flush：CanvasEditor.tsx 的全局 onMouseDown（约 122-206 行）对非
// INPUT/TEXTAREA/SELECT/BUTTON 的 mousedown 目标会 preventDefault()——本面板虽然
// portal 到 body，但 React 合成事件仍按组件树冒泡到 CanvasEditor，于是"点其它
// 单元格/面板空白处"时浏览器不会把焦点从当前编辑器的 input/textarea 上移走，
// onBlur 驱动的保存链路可能压根不触发（同一根因见 NodeShell.tsx:139-156 的重命名
// 输入框，那里用的是 document 捕获阶段监听器兜底）。这里同款处理：
//   - outside-click / Esc 关闭路径：先调 onBeforeClose 落一次值，再关。
//   - 父组件切换 openCell 导致本弹层被直接摘掉（用户没等 blur 触发就点了下一格）：
//     卸载 effect 兜底调 onBeforeClose，但不再调 onClose——openCell 早被新点击的
//     单元格改写过，这里重复 onClose 会把刚打开的新弹层错误地关掉。
// onBeforeClose 必须自己做幂等/dirty 判断（各编辑器用 savedRef 实现），否则同一次
// 关闭会经两条路径各触发一次，重复写入。
// 导出给 DialogueEditor 复用——对白编辑器同样需要 outside-click/Esc
// 关闭 + 阻止 mousedown 冒泡这套行为，没必要另起一份。它是「保存制」（不接
// onBeforeClose 自动落库，点外部直接丢弃草稿），细节见 DialogueEditor.tsx。
export function PopoverShell({
  children,
  onClose,
  onBeforeClose,
  className = '',
  matchAnchorWidth = false,
}: {
  children: React.ReactNode;
  onClose: () => void;
  onBeforeClose?: () => void;
  className?: string;
  /** 弹层最小宽=锚点(父元素)实测宽。fixed 定位下 min-w-full 的百分比是相对
   * viewport 的(会撑满全屏),下拉要贴合触发按钮宽度只能量出来设像素值。 */
  matchAnchorWidth?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  // ref 存最新的 onBeforeClose：effect 的依赖只挂 onClose，不必每次父组件重渲染
  // （每次打字都会重建 commit 闭包）就重新订阅一次 document 监听器。
  const onBeforeCloseRef = useRef(onBeforeClose);
  onBeforeCloseRef.current = onBeforeClose;

  // fixed 定位：弹层原先 absolute 挂在 td 里，会被表格圆角容器的 overflow-hidden
  // 裁掉（景别下拉只露出第一项）、也会在最右列溢出视口。改 fixed 后脱离一切
  // overflow 裁切上下文；挂载后量一次自身尺寸做视口钳制（右缘内收、越底上翻）。
  // 量测前 visibility:hidden，避免在左上角闪一帧。
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    const anchor = el?.parentElement?.getBoundingClientRect();
    if (!el || !anchor) return;
    if (matchAnchorWidth) el.style.minWidth = `${Math.round(anchor.width)}px`;
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    let left = Math.max(8, Math.min(anchor.left, window.innerWidth - w - 12));
    let top = anchor.bottom + 8;
    if (top + h > window.innerHeight - 8) top = Math.max(8, anchor.top - h - 8);
    setPos({ top, left });
  }, []);

  useEffect(() => {
    const closeWith = () => {
      onBeforeCloseRef.current?.();
      onClose();
    };
    const onMouseDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) closeWith();
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeWith();
    };
    // capture=true（第三参 true）：必须抢在 CanvasEditor 背景 mousedown 的
    // preventDefault 之前拿到事件，见上方函数注释。
    document.addEventListener('mousedown', onMouseDown, true);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onMouseDown, true);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [onClose]);

  // 卸载兜底：只 flush 不 close，理由见上方函数注释。
  useEffect(() => () => { onBeforeCloseRef.current?.(); }, []);

  return (
    <div
      ref={ref}
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
      style={pos ? { top: pos.top, left: pos.left } : { visibility: 'hidden', top: 0, left: 0 }}
      className={`fixed z-50 rounded-2xl border border-white/10 bg-[#1f1f1f] p-4 shadow-2xl ${className}`}
    >
      {children}
    </div>
  );
}

// 时长编辑器：单个数字输入框，失焦即保存。clamp 到 [MIN_SHOT_SECONDS, MAX_SHOT_SECONDS]
// 而不是拒绝非法输入——用户敲个 99 这种明显越界值，落一个合法边界值比原地报错更顺手。
export function DurationEditor({
  value,
  max,
  onSave,
  onClose,
}: {
  value: number;
  /** 上限 = 节点设置的主切割时长 durationSec */
  max: number;
  onSave: (next: number) => void;
  onClose: () => void;
}) {
  const [text, setText] = useState(String(value));
  // 幂等闸门：outside-click/Esc 关闭路径和卸载兜底路径都会调 save，同一次编辑
  // 只准真正写一次，避免卸载兜底把 outside-click 路径已经存过的值再存一遍
  // （这里是同一个值所以重复写无害，但守住这个闸门是通用约定，其它编辑器靠它
  // 防止"卸载兜底用旧 text 覆盖刚才按钮点击存的新值"这类真实的坏场景）。
  const savedRef = useRef(false);

  const save = () => {
    if (savedRef.current) return;
    savedRef.current = true;
    const n = Number(text);
    const next = Number.isFinite(n) ? Math.min(max, Math.max(MIN_SHOT_SECONDS, Math.round(n))) : value;
    onSave(next);
  };
  const commit = () => {
    save();
    onClose();
  };

  return (
    <PopoverShell onClose={onClose} onBeforeClose={save} className="w-[360px]">
      <input
        type="number"
        autoFocus
        min={MIN_SHOT_SECONDS}
        max={max}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
        }}
        className={`w-full ${FIELD_BOX} text-base [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none`}
      />
      <div className={`mt-2 whitespace-nowrap ${HINT_TEXT}`}>
        范围 {MIN_SHOT_SECONDS}–{max} 秒；失焦自动保存
      </div>
      <div className="mt-3 flex items-center justify-between border-t border-white/10 pt-3">
        <span className={HINT_TEXT}>失焦自动保存</span>
        <button type="button" onClick={commit} className={SAVE_BTN}>保存</button>
      </div>
    </PopoverShell>
  );
}

// 通用文本编辑器：画面描述 / 光影氛围 / 音效三列共用，只是 placeholder 不同。
export function TextCellEditor({
  value,
  placeholder,
  hint,
  rows = 3,
  widthClass = 'w-[380px]',
  onSave,
  onClose,
}: {
  value: string;
  placeholder?: string;
  /** 底栏左侧辅助文案（如「输入@选择资产；失焦自动保存」）；不传则底栏只有保存按钮。 */
  hint?: string;
  rows?: number;
  widthClass?: string;
  onSave: (next: string) => void;
  onClose: () => void;
}) {
  const [text, setText] = useState(value);
  // 幂等闸门，理由同 DurationEditor 的 savedRef。
  const savedRef = useRef(false);

  const save = () => {
    if (savedRef.current) return;
    savedRef.current = true;
    onSave(text);
  };
  const commit = () => {
    save();
    onClose();
  };

  return (
    <PopoverShell onClose={onClose} onBeforeClose={save} className={widthClass}>
      <textarea
        autoFocus
        rows={rows}
        value={text}
        placeholder={placeholder}
        onChange={(e) => setText(e.target.value)}
        onBlur={commit}
        className={`w-full resize-none ${FIELD_BOX}`}
      />
      <div className={`mt-3 flex items-center ${hint ? 'justify-between' : 'justify-end'}`}>
        {hint && <span className={HINT_TEXT}>{hint}</span>}
        <button type="button" onClick={commit} className={SAVE_BTN}>保存</button>
      </div>
    </PopoverShell>
  );
}

// 行颜色标记 5 色定义：与 scriptData.ColorMark 保持一致。色值走实体色（高饱和
// 大圆点），清除态并入圆点行首位（带斜杠圆），不再单列文字按钮。
const COLOR_MARKS: { value: Exclude<ColorMark, ''>; label: string; swatch: string }[] = [
  { value: 'red', label: '红', swatch: 'bg-[#f26d6d]' },
  { value: 'yellow', label: '黄', swatch: 'bg-[#f2b705]' },
  { value: 'green', label: '绿', swatch: 'bg-[#19c37d]' },
  { value: 'blue', label: '蓝', swatch: 'bg-[#38bdf8]' },
  { value: 'gray', label: '灰', swatch: 'bg-[#b3b3b3]' },
];

// 行菜单：「请选择颜色」标题 + 一行大圆点（首位=清除，带斜杠圆）+
// 分隔线 + 垃圾桶图标「删除该行」。
export function RowMenu({
  onMark,
  onClear,
  onDelete,
  onClose,
}: {
  onMark: (color: Exclude<ColorMark, ''>) => void;
  onClear: () => void;
  onDelete: () => void;
  onClose: () => void;
}) {
  return (
    <PopoverShell onClose={onClose} className="w-64">
      <div className={`mb-2.5 ${HINT_TEXT}`}>请选择颜色</div>
      <div className="flex items-center gap-2.5 pb-3">
        <button
          type="button"
          title="清除颜色"
          aria-label="清除颜色"
          onClick={() => { onClear(); onClose(); }}
          className="flex h-7 w-7 items-center justify-center rounded-full border border-white/25 bg-transparent text-muted-foreground hover:border-white/50 hover:text-foreground cursor-pointer"
        >
          <Ban size={14} />
        </button>
        {COLOR_MARKS.map((c) => (
          <button
            key={c.value}
            type="button"
            title={c.label}
            aria-label={`标记为${c.label}`}
            onClick={() => { onMark(c.value); onClose(); }}
            className={`h-7 w-7 rounded-full border-none cursor-pointer hover:opacity-80 ${c.swatch}`}
          />
        ))}
      </div>
      <button
        type="button"
        onClick={() => { onDelete(); onClose(); }}
        className="flex w-full items-center gap-2 rounded-lg bg-transparent border-t border-white/10 pt-3 pb-1 px-1 text-left text-sm text-red-400 hover:text-red-300 border-x-0 border-b-0 cursor-pointer"
      >
        <Trash2 size={15} />
        删除该行
      </button>
    </PopoverShell>
  );
}
