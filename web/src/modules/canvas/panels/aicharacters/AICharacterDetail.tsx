import { createPortal } from 'react-dom';
import { ChevronLeft, ChevronRight, X } from 'lucide-react';
import type { AICharacter } from '@/shared/api';
import { mediaCrossOrigin } from '@/modules/canvas/editor/utils/mediaCrossOrigin';

interface Props {
  items: AICharacter[];
  index: number;
  onNavigate: (index: number) => void;
  onApply: (c: AICharacter) => void;
  onClose: () => void;
}

// 角色详情弹框（对标上游的角色详情页，按我们的数据裁剪：每个角色只有一张立绘，
// 属性只有 性别/年龄/年龄段/职业 四项 + 人设描述全文）。
export function AICharacterDetail({ items, index, onNavigate, onApply, onClose }: Props) {
  const c = items[index];
  if (!c) return null;

  const attrs: Array<[string, string]> = [
    ['性别', c.gender],
    ['年龄', `${c.age}岁`],
    ['年龄段', c.ageGroup],
    ['职业', c.occupation],
  ];

  return createPortal(
    <div
      className="fixed inset-0 z-[120] bg-black/70 flex items-center justify-center p-6"
      onClick={onClose}
    >
      <div
        className="w-full max-w-4xl max-h-[90vh] bg-card border border-border rounded-2xl shadow-2xl flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 px-5 py-4 border-b border-border">
          <h3 className="text-foreground text-lg font-semibold m-0 truncate">{c.occupation}</h3>
          <span className="text-muted-foreground text-sm shrink-0">
            {c.gender} · {c.ageGroup}
          </span>
          <div className="flex-1" />
          <button
            onClick={() => onNavigate(index - 1)}
            disabled={index <= 0}
            className="w-8 h-8 flex items-center justify-center rounded-md bg-popover hover:bg-accent border border-input text-foreground cursor-pointer disabled:opacity-40 disabled:cursor-default"
            aria-label="上一个角色"
          >
            <ChevronLeft size={16} />
          </button>
          <button
            onClick={() => onNavigate(index + 1)}
            disabled={index >= items.length - 1}
            className="w-8 h-8 flex items-center justify-center rounded-md bg-popover hover:bg-accent border border-input text-foreground cursor-pointer disabled:opacity-40 disabled:cursor-default"
            aria-label="下一个角色"
          >
            <ChevronRight size={16} />
          </button>
          <button
            onClick={onClose}
            className="w-8 h-8 flex items-center justify-center rounded-md bg-transparent border-none text-muted-foreground hover:text-foreground cursor-pointer"
            aria-label="关闭详情"
          >
            <X size={18} />
          </button>
        </div>

        <div className="flex flex-1 min-h-0">
          <div className="w-[45%] shrink-0 bg-black/40 flex items-center justify-center p-4">
            <img
              src={c.imageUrl}
              crossOrigin={mediaCrossOrigin(c.imageUrl)}
              alt={c.occupation}
              className="max-w-full max-h-[70vh] object-contain rounded-xl"
            />
          </div>
          <div className="flex-1 flex flex-col p-6 overflow-y-auto">
            <div className="text-foreground font-semibold mb-3">角色属性</div>
            <div className="grid grid-cols-2 gap-x-8 gap-y-3 mb-6">
              {attrs.map(([label, value]) => (
                <div key={label} className="flex items-baseline gap-3">
                  <span className="text-muted-foreground text-sm shrink-0">{label}</span>
                  <span className="text-foreground text-sm">{value}</span>
                </div>
              ))}
            </div>
            <div className="text-foreground font-semibold mb-2">人设描述</div>
            <p className="text-muted-foreground text-sm leading-relaxed m-0 whitespace-pre-wrap">
              {c.description}
            </p>
            <div className="flex-1" />
            <button
              onClick={() => onApply(c)}
              className="mt-6 w-full py-2.5 bg-accent hover:bg-accent text-white rounded-xl text-sm font-medium border-none cursor-pointer transition-colors"
            >
              应用到画布
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
