import { useState } from 'react';
import { Keyboard } from 'lucide-react';

// 画布右下角的快捷键提示：一行速览 + 行头键盘图标 hover 即弹出完整面板
// （对标 Lovart 的快捷键弹层，栏间竖线分割）。清单与
// apps/web/src/modules/canvas/README.md 同源，改任一侧记得同步另一侧。
// 按键名按操作系统显示：Mac 用 ⌘/⇧，Windows 用 Ctrl/Shift。

type Platform = 'mac' | 'win';

function detectPlatform(): Platform {
  const sig = `${navigator.platform ?? ''} ${navigator.userAgent ?? ''}`;
  return /mac|iphone|ipad/i.test(sig) ? 'mac' : 'win';
}

interface Row {
  label: string;
  keys: string[];
}

function sections(platform: Platform): Array<{ title: string; rows: Row[] }> {
  const mod = platform === 'mac' ? '⌘' : 'Ctrl';
  const shift = platform === 'mac' ? '⇧' : 'Shift';
  return [
    {
      title: '缩放',
      rows: [
        { label: '放大', keys: [mod, '+'] },
        { label: '缩小', keys: [mod, '−'] },
        { label: '适应画布', keys: [mod, '0'] },
        { label: '触控板', keys: ['双指捏合'] },
        { label: '鼠标', keys: [mod, '滚轮'] },
      ],
    },
    {
      title: '移动画布',
      rows: [
        { label: '键盘', keys: ['Space', '拖动'] },
        { label: '触控板', keys: ['双指滚动'] },
        { label: '鼠标', keys: ['按住空白拖动'] },
      ],
    },
    {
      title: '其他',
      rows: [
        { label: '撤销', keys: [mod, 'Z'] },
        { label: '重做', keys: [mod, shift, 'Z'] },
        { label: '拷贝选中', keys: [mod, 'D'] },
        { label: '删除', keys: ['Del'] },
      ],
    },
  ];
}

function KeyChip({ text }: { text: string }) {
  return (
    <span className="inline-flex h-7 min-w-7 items-center justify-center rounded-md border border-solid border-input bg-popover px-2 text-xs text-foreground whitespace-nowrap">
      {text}
    </span>
  );
}

export function ShortcutsHint({ platform }: { platform?: Platform }) {
  const [open, setOpen] = useState(false);
  const os = platform ?? detectPlatform();
  const mod = os === 'mac' ? '⌘' : 'Ctrl';

  return (
    // 面板是本容器的 absolute 子元素，且用 pb 补掉与提示条之间的视觉间隙，
    // 指针从图标移进面板全程不离开容器——onMouseLeave 不会中途误关。
    <div className="fixed right-4 bottom-4 z-40" onMouseLeave={() => setOpen(false)}>
      {open && (
        <div className="absolute bottom-full right-0 pb-2">
          {/* 每处边框必须显式带 border-solid：本项目 tailwind 配置 preflight:false
              （避免与 AntD reset 打架），div 的 border-style 默认是 none——只给
              宽度和颜色的 Tailwind 边框类画不出任何东西。上两版边框/竖线
              "类都生成了却看不见"，根因就是它（button 能显示是浏览器自带
              border-style 的运气）。竖线颜色也不用 border-input(15% 白，深色底
              上隐形)，显式取 20%/25% 白。 */}
          <div className="w-max max-w-[92vw] rounded-2xl border border-solid border-white/25 bg-card px-2 py-6 shadow-2xl">
            <div className="grid grid-cols-1 sm:grid-cols-3">
              {sections(os).map((sec, idx) => (
                <div
                  key={sec.title}
                  className={`min-w-[230px] px-6${idx > 0 ? ' border-0 border-solid sm:border-l sm:border-white/20' : ''}`}
                >
                  <div className="mb-4 text-sm font-medium text-primary">{sec.title}</div>
                  <div className="flex flex-col gap-3">
                    {sec.rows.map((row) => (
                      <div key={row.label} className="flex items-center justify-between gap-6">
                        <span className="text-sm text-muted-foreground whitespace-nowrap">
                          {row.label}
                        </span>
                        <span className="flex items-center gap-1">
                          {row.keys.map((k, i) => (
                            <KeyChip key={i} text={k} />
                          ))}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* 速览条本身不描边——它是常驻元素，加框会在画布上多出一个抢眼的矩形。
          需要被看见的是里面那个键盘图标按钮，框只给它。 */}
      <div
        data-testid="shortcuts-strip"
        className="flex items-center gap-2.5 text-[11px] text-muted-foreground bg-card/80 rounded px-3 py-1.5 backdrop-blur"
      >
        <button
          type="button"
          aria-label="查看全部快捷键"
          title="查看全部快捷键"
          onMouseEnter={() => setOpen(true)}
          onClick={() => setOpen((v) => !v)}
          className="flex items-center justify-center rounded-md border border-solid border-input bg-popover p-1.5 text-foreground hover:text-primary hover:border-primary/60 transition-colors cursor-pointer"
        >
          <Keyboard size={20} />
        </button>
        <span>
          右键/双击添加节点 · 拖端口连线 · 拖空白处平移 · Ctrl+拖框选 · 捏合/{mod}
          +滚轮缩放 · Del 删除 · Ctrl+Z 撤销
        </span>
      </div>
    </div>
  );
}
