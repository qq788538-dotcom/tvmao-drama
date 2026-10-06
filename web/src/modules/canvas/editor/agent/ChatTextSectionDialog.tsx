// 聊天气泡文本卡片的「大窗编辑」弹层——给**画布上没有实体**的内容用：txt/md
// 文件附件、或引用的文本节点已被删除（编辑的是气泡里存的发送时快照）。
// 有实体的文本节点卡片走 nodes/text/TextExpandDialog（真编辑器、保存写回节点），
// 不进这里。
//
// 结构从 TextExpandDialog 复制（portal 到 body / 截断指针滚轮事件 /
// canvas-portal-surface 的理由见那边注释），差异只有两点：
//   1. 内容存在本地 ref，不 commit 到任何节点——没有节点可写；
//   2. 底部多一个「保存为文本节点」：编辑成果的唯一去处是落成新画布文本节点
//     （用户拍板的语义），落点取当前视口中心，落完平移过去让用户看到它。
import { useRef } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { EditorContent } from '@tiptap/react';

import { useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { MAX_TEXT_LEN, useMarkdownEditor } from '../nodes/text/useMarkdownEditor';
import { TextToolbar } from '../nodes/text/TextToolbar';
import { CharCount } from '../nodes/text/CharCount';

export function ChatTextSectionDialog({
  name,
  initialMarkdown,
  onClose,
}: {
  name: string;
  initialMarkdown: string;
  onClose: () => void;
}) {
  const draftRef = useRef(initialMarkdown);
  const readOnly = useCanvasStore((s) => s.readOnly);
  const editor = useMarkdownEditor({
    value: initialMarkdown,
    onChange: (md) => {
      draftRef.current = md;
    },
    autoFocus: true,
  });

  const saveAsNode = () => {
    const cs = useCanvasStore.getState();
    // world→screen 是 screen = world*zoom + view.x（centerNode 同款反推）：
    // 视口中心的世界坐标 = (vp/2 - view)/zoom。
    const { x, y, zoom } = cs.view;
    const world = {
      x: (window.innerWidth / 2 - x) / zoom,
      y: (window.innerHeight / 2 - y) / zoom,
    };
    const id = cs.addNode('text-node', world);
    if (!id) return; // readOnly 下 addNode 返回 ''（按钮已隐藏，双保险）
    cs.updateNodeSettings(id, { text: draftRef.current, name });
    persistNodeById(id);
    cs.centerNode(id);
    onClose();
  };

  return createPortal(
    <div
      className="fixed inset-0 z-[1000] bg-black/60 flex items-center justify-center"
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) onClose();
      }}
      onWheel={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.stopPropagation()}
    >
      <div className="canvas-portal-surface w-[min(900px,92vw)] h-[82vh] bg-card border border-border rounded-2xl shadow-2xl flex flex-col overflow-hidden">
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-border shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            <span className="text-[13px] text-foreground truncate max-w-[240px]" title={name}>
              {name}
            </span>
            {editor && <TextToolbar editor={editor} />}
          </div>
          <div className="flex items-center gap-3">
            {editor && <CharCount editor={editor} limit={MAX_TEXT_LEN} />}
            {!readOnly && (
              <button
                type="button"
                onClick={saveAsNode}
                className="px-2.5 py-1 rounded-md text-[12px] bg-blue-600 hover:bg-blue-500 text-white border-none cursor-pointer transition-colors"
                title="把当前内容落成一个画布文本节点"
              >
                保存为文本节点
              </button>
            )}
            <button
              type="button"
              onClick={onClose}
              title="关闭"
              className="w-7 h-7 rounded-md flex items-center justify-center border-none cursor-pointer bg-transparent text-muted-foreground hover:text-foreground hover:bg-accent/60 transition-colors"
            >
              <X size={16} />
            </button>
          </div>
        </div>
        <div
          className="flex-1 overflow-y-auto custom-scrollbar px-6 py-4 cursor-text"
          onClick={(e) => {
            if (e.target === e.currentTarget) editor?.chain().focus('end').run();
          }}
        >
          <EditorContent editor={editor} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
