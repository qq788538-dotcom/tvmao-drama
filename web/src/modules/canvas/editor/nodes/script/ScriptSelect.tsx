import { useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { PopoverShell } from './cellEditors';

// 自定义下拉：原生 <select> 的弹出菜单是系统样式（亮色、无法定制），在暗色
// 弹框里非常扎眼。这里用按钮 + PopoverShell 暗色列表替代，视觉与全套弹层一致。
// 只覆盖「枚举单选」这一种场景——脚本模块内的下拉全是这个形态。
export interface ScriptSelectOption {
  value: string;
  label: string;
}

export function ScriptSelect({
  value,
  options,
  onChange,
  ariaLabel,
  disabled = false,
  className = '',
}: {
  value: string;
  options: ScriptSelectOption[];
  onChange: (v: string) => void;
  ariaLabel?: string;
  disabled?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const current = options.find((o) => o.value === value);

  return (
    <div ref={wrapRef} className={`relative ${className}`}>
      <button
        type="button"
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between gap-1.5 rounded-lg border border-white/10 bg-black/40 px-2.5 py-1.5 text-sm text-foreground cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
      >
        <span className="truncate">{current?.label ?? '请选择'}</span>
        <ChevronDown size={14} className="shrink-0 text-muted-foreground" />
      </button>
      {open && (
        <PopoverShell onClose={() => setOpen(false)} matchAnchorWidth className="max-h-64 w-max overflow-y-auto !p-1.5">
          <div role="listbox" aria-label={ariaLabel}>
            {options.map((o) => (
              <button
                key={o.value}
                type="button"
                role="option"
                aria-selected={o.value === value}
                onClick={() => { onChange(o.value); setOpen(false); }}
                className={
                  'block w-full whitespace-nowrap rounded-lg bg-transparent border-none cursor-pointer px-3 py-2 text-left text-sm '
                  + (o.value === value ? 'bg-white/10 text-foreground' : 'text-foreground hover:bg-white/5')
                }
              >
                {o.label}
              </button>
            ))}
          </div>
        </PopoverShell>
      )}
    </div>
  );
}
