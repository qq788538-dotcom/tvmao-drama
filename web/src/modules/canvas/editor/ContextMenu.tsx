import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { History } from 'lucide-react';
import { useCanvasStore } from './store';
import { nodeMenuItems, UPLOAD_MENU_TYPE } from './registry';
import { openUploadPicker } from './utils/uploadPicker';
import { renderRejected } from './utils/rejectedFiles';

interface ContextMenuProps {
  x: number;
  y: number;
  worldX: number;
  worldY: number;
  /** 设置后，新添加的节点会自动从这个 source 节点连过来 */
  sourceNodeId?: string;
  /** 「从生成历史选择」入口——弹框由 CanvasEditor 托管（菜单点击即关闭，
   *  弹框生命周期不能挂在菜单子树里）。 */
  onPickFromHistory?: () => void;
  onClose: () => void;
}

const GROUP_LABELS = {
  input: '输入',
  generate: '生成',
  workflow: '工作流',
  output: '输出',
} as const;

export function ContextMenu({ x, y, worldX, worldY, sourceNodeId, onPickFromHistory, onClose }: ContextMenuProps) {
  const addNode = useCanvasStore((s) => s.addNode);
  const addConnection = useCanvasStore((s) => s.addConnection);
  const { modal } = AntApp.useApp();
  const ref = useRef<HTMLDivElement>(null);

  /** 「上传」走 openUploadPicker 共用入口（与右键菜单同一套：选文件 →
   *  按 MIME 建节点 → 上传），这里只负责拒收弹框和收菜单。 */
  const handleUploadClick = () => {
    openUploadPicker({
      worldX,
      worldY,
      sourceNodeId,
      onRejected: (name, reason) =>
        modal.error({ title: '这个文件不支持', content: renderRejected([{ name, reason }]) }),
    });
    onClose();
  };
  const [position, setPosition] = useState({ left: x, top: y });

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const keyHandler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('mousedown', handler);
    window.addEventListener('keydown', keyHandler);
    return () => {
      window.removeEventListener('mousedown', handler);
      window.removeEventListener('keydown', keyHandler);
    };
  }, [onClose]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const margin = 12;
    const width = el.offsetWidth;
    const height = el.offsetHeight;
    const maxLeft = Math.max(margin, window.innerWidth - width - margin);
    const maxTop = Math.max(margin, window.innerHeight - height - margin);
    setPosition({
      left: Math.min(Math.max(margin, x), maxLeft),
      top: Math.min(Math.max(margin, y), maxTop),
    });
  }, [x, y]);

  const grouped = useMemo(() => ({
    input: nodeMenuItems.filter((item) => item.group === 'input'),
    generate: nodeMenuItems.filter((item) => item.group === 'generate'),
    workflow: nodeMenuItems.filter((item) => item.group === 'workflow'),
    output: nodeMenuItems.filter((item) => item.group === 'output'),
  }), []);

  return (
    <div
      ref={ref}
      // 实底 + 提亮边框 + 重投影，与 NodeContextMenu 同一套面板样式——半透明
      // bg-card/85 叠深色节点上分不出边界（见 NodeContextMenu 处的说明）。
      className="fixed z-[100] w-72 overflow-hidden rounded-2xl border border-solid border-white/25 bg-muted canvas-menu-enter p-1 shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
      style={{ left: position.left, top: position.top }}
      onContextMenu={(e) => e.preventDefault()}
      onMouseDown={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
    >
      <div className="px-3 py-2">
        <div className="text-sm text-foreground">添加节点</div>
      </div>

      <div className="max-h-[480px] overflow-y-auto custom-scrollbar">
        {(['generate', 'input', 'workflow', 'output'] as const).map((groupKey, idx) => {
            const items = grouped[groupKey];
            if (items.length === 0) return null;
            return (
              <div key={groupKey} className={idx > 0 ? 'mt-1 pt-1 border-t border-border/70' : ''}>
                <div className="px-3 pt-1 pb-0.5 text-xs text-muted-foreground">
                  {GROUP_LABELS[groupKey]}
                </div>
                <div className="flex flex-col">
                  {items.map((item) => {
                    const Icon = item.icon;
                    return (
                      <button
                        key={item.type}
                        role="option"
                        className="group flex w-full items-center gap-2 rounded-lg p-2 text-left text-sm text-foreground transition-all duration-150 ease-out hover:bg-accent border-none bg-transparent cursor-pointer outline-none"
                        onClick={() => {
                          if (item.type === UPLOAD_MENU_TYPE) {
                            handleUploadClick();
                            return;
                          }
                          const newNodeId = addNode(item.type, { x: worldX, y: worldY });
                          if (sourceNodeId && newNodeId) {
                            addConnection(sourceNodeId, newNodeId);
                          }
                          onClose();
                        }}
                      >
                        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-popover text-sidebar-foreground">
                          <Icon size={16} />
                        </span>
                        <span className="relative min-w-0 flex-1 h-10 overflow-hidden context-menu-text">
                          <span className="absolute left-0 right-0 top-0 flex flex-col transition-transform duration-200 ease-out context-menu-text-inner">
                            <span className="block text-sm leading-tight truncate text-foreground">
                              {item.label}
                            </span>
                            {item.hint ? (
                              <span className="block mt-0.5 text-xs leading-tight truncate text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity duration-200 ease-out">
                                {item.hint}
                              </span>
                            ) : null}
                          </span>
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            );
          })}
      </div>

      {/* 最后一行：从生成历史选择（「添加资源」区）。 */}
      {onPickFromHistory && (
        <div className="mt-1 pt-1 border-t border-border/70">
          <button
            role="option"
            className="group flex w-full items-center gap-2 rounded-lg p-2 text-left text-sm text-foreground transition-all duration-150 ease-out hover:bg-accent border-none bg-transparent cursor-pointer outline-none"
            onClick={() => {
              onPickFromHistory();
              onClose();
            }}
          >
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-popover text-sidebar-foreground">
              <History size={16} />
            </span>
            <span className="text-sm text-foreground">从生成历史选择</span>
          </button>
        </div>
      )}
    </div>
  );
}
