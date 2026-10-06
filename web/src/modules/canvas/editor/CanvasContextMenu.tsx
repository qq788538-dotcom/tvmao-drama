import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { App as AntApp } from 'antd';
import { useCanvasStore } from './store';
import { openUploadPicker } from './utils/uploadPicker';
import { readClipboardImageFiles } from './utils/clipboardImages';
import { readMirror, type NodeClipboardPayload } from './utils/nodeClipboard';
import { renderRejected } from './utils/rejectedFiles';

/** 快捷键**文案**按平台切换（Mac ⌘ / 其余 Ctrl，Windows 重做惯例是 Ctrl+Y）。
 *  只影响显示——真实按键处理在 CanvasEditor 的 onKey，metaKey/ctrlKey 都认，
 *  Ctrl+Shift+Z 和 Ctrl+Y 也都能重做。每次渲染现算而不是模块级常量，
 *  测试里 stub navigator 才能覆盖两种平台。 */
function shortcutLabels() {
  const isMac =
    typeof navigator !== 'undefined' &&
    /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent);
  return isMac
    ? { undo: '⌘Z', redo: '⇧⌘Z', paste: '⌘V' }
    : { undo: 'Ctrl+Z', redo: 'Ctrl+Y', paste: 'Ctrl+V' };
}

interface CanvasContextMenuProps {
  x: number;
  y: number;
  /** 上传/粘贴产物的世界坐标落点（右键位置）。 */
  worldX: number;
  worldY: number;
  /** 点「添加节点」——宿主原位切换到添加节点菜单，而不是这里再画一份节点清单。 */
  onAddNode: () => void;
  /** 剪贴板里是「复制节点」payload 时的落地——宿主调 store.pasteNodes。 */
  onPasteNodes: (payload: NodeClipboardPayload) => void;
  /** 剪贴板图片就绪后交给宿主落节点——排布/只读保护/并发上传复用拖拽那套
   *  uploadDroppedFiles，菜单不重写。 */
  onPasteFiles: (files: File[]) => void;
  /** 剪贴板文字 → 文字节点。 */
  onPasteText: (text: string) => void;
  onClose: () => void;
}

/**
 * 画布背景右键菜单（对标 libtv）：上传 / 添加节点 / 撤销 / 重做 / 粘贴。
 * 双击背景仍直接弹添加节点菜单（ContextMenu），两个入口分工不同。
 */
export function CanvasContextMenu({
  x,
  y,
  worldX,
  worldY,
  onAddNode,
  onPasteNodes,
  onPasteFiles,
  onPasteText,
  onClose,
}: CanvasContextMenuProps) {
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const undoLen = useCanvasStore((s) => s.undoStack.length);
  const redoLen = useCanvasStore((s) => s.redoStack.length);
  const readOnly = useCanvasStore((s) => s.readOnly);
  const { modal, message } = AntApp.useApp();
  const ref = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ left: x, top: y });

  // 外点关闭 + Escape 关闭：与 ContextMenu 同一套交互。
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

  // 贴边翻转：菜单超出视口时往回夹。
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

  const handleUpload = () => {
    openUploadPicker({
      worldX,
      worldY,
      onRejected: (name, reason) =>
        modal.error({ title: '这个文件不支持', content: renderRejected([{ name, reason }]) }),
    });
    onClose();
  };

  // 菜单粘贴的优先级与 ⌘V 一致：节点 → 图片 → 文字。差异只在读取通道——鼠标
  // 点击没有 paste 事件，节点走 localStorage 镜像，图片/文字走异步 clipboard API
  //（该 API 读不到视频文件，这是浏览器边界；⌘V 路径可以贴视频）。
  // 先关菜单再异步读剪贴板：AntApp 的 message/modal 句柄在组件卸载后仍可用
  //（挂在 App 级 holder 上），toast 不会因为菜单关了而丢。
  const handlePaste = () => {
    onClose();
    const mirror = readMirror();
    if (mirror) {
      onPasteNodes(mirror);
      return;
    }
    void (async () => {
      const res = await readClipboardImageFiles();
      if (res.status === 'ok') {
        onPasteFiles(res.files);
        return;
      }
      let text = '';
      try {
        text = (await navigator.clipboard.readText()) ?? '';
      } catch {
        // 没权限/不支持——落到下面的统一提示。
      }
      if (text.trim()) {
        onPasteText(text);
        return;
      }
      if (res.status === 'denied') message.warning('浏览器未授权读取剪贴板');
      else message.info('剪贴板中没有可粘贴的内容');
    })();
  };

  const shortcuts = shortcutLabels();
  // 弹出时不预读剪贴板做置灰——navigator.clipboard.read 会触发权限弹窗（用户
  // 拍板：粘贴常亮，点了没图才提示）。
  const groups: Array<
    Array<{ key: string; label: string; shortcut?: string; disabled?: boolean; onPick: () => void }>
  > = [
    [{ key: 'upload', label: '上传', disabled: readOnly, onPick: handleUpload }],
    [{ key: 'add-node', label: '添加节点', onPick: onAddNode }],
    [
      { key: 'undo', label: '撤销', shortcut: shortcuts.undo, disabled: undoLen === 0, onPick: () => { undo(); onClose(); } },
      { key: 'redo', label: '重做', shortcut: shortcuts.redo, disabled: redoLen === 0, onPick: () => { redo(); onClose(); } },
    ],
    [{ key: 'paste', label: '粘贴', shortcut: shortcuts.paste, disabled: readOnly, onPick: handlePaste }],
  ];

  return (
    <div
      ref={ref}
      // 与 ContextMenu/NodeContextMenu 同一套面板样式：实底 + 提亮边框 + 重投影。
      className="fixed z-[100] w-56 overflow-hidden rounded-2xl border border-solid border-white/25 bg-muted canvas-menu-enter p-1 shadow-[0_12px_32px_rgba(0,0,0,0.55)]"
      style={{ left: position.left, top: position.top }}
      onContextMenu={(e) => e.preventDefault()}
      onMouseDown={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
    >
      {groups.map((items, gi) => (
        <div key={gi} className={gi > 0 ? 'mt-1 pt-1 border-t border-border/70' : ''}>
          {items.map((item) => (
            <button
              key={item.key}
              disabled={item.disabled}
              className={
                'flex w-full items-center justify-between gap-4 rounded-lg px-3 py-2.5 text-left text-sm border-none bg-transparent outline-none transition-colors duration-150 ' +
                (item.disabled
                  ? 'text-muted-foreground/50 cursor-default'
                  : 'text-foreground cursor-pointer hover:bg-accent')
              }
              onClick={() => {
                if (!item.disabled) item.onPick();
              }}
            >
              <span>{item.label}</span>
              {item.shortcut ? (
                <span className={item.disabled ? 'text-xs text-muted-foreground/50' : 'text-xs text-muted-foreground'}>
                  {item.shortcut}
                </span>
              ) : null}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
