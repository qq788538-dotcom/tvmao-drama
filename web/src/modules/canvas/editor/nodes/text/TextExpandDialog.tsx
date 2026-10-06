// 「展开编辑」全屏弹层。portal 到 document.body：TextNode 挂在 CanvasEditor 的
// transform:translate/scale 容器里，transform 祖先会成为内部 `fixed` 定位后代的
// 包含块——不 portal 出去的话，`fixed inset-0` 在画布缩放（zoom≠1）时不再等于
// 视口，遮罩和面板整体错位。参照 GenVideoNode.tsx / ScriptNode.tsx 同款处理。
//
// 弹层持有独立的编辑器实例，与节点内联编辑器不同时存在（弹层打开时节点显示
// 只读渲染）——两个实例落库走同一条 commitTextDraft，store 是唯一真源。
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { EditorContent } from '@tiptap/react';
import { MAX_TEXT_LEN } from './useMarkdownEditor';
import { useMarkdownEditor } from './useMarkdownEditor';
import { TextToolbar } from './TextToolbar';
import { commitTextDraft, flushTextDraft } from './textNodeDraft';
import { CharCount } from './CharCount';

export function TextExpandDialog({
  nodeId,
  initialMarkdown,
  onClose,
}: {
  nodeId: string;
  initialMarkdown: string;
  onClose: () => void;
}) {
  const editor = useMarkdownEditor({
    value: initialMarkdown,
    onChange: (md) => commitTextDraft(nodeId, md),
    autoFocus: true,
  });

  const close = () => {
    void flushTextDraft(nodeId);
    onClose();
  };

  return createPortal(
    <div
      className="fixed inset-0 z-[1000] bg-black/60 flex items-center justify-center"
      // portal 只改 DOM 挂载点，React 合成事件仍沿组件树冒泡回 NodeShell/画布
      //（参照 DirectorDeskNode 的注意事项），这里统一截断指针/滚轮事件。
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => {
        e.stopPropagation();
        if (e.target === e.currentTarget) close();
      }}
      onWheel={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.stopPropagation()}
    >
      {/* canvas-portal-surface：portal 到 body 后继承不到 .canvas-root 的
          color——不补的话弹层里正文变成默认亮白，与节点内观感不一致。 */}
      <div className="canvas-portal-surface w-[min(900px,92vw)] h-[82vh] bg-card border border-border rounded-2xl shadow-2xl flex flex-col overflow-hidden">
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-border shrink-0">
          {editor && <TextToolbar editor={editor} />}
          <div className="flex items-center gap-3">
            {editor && <CharCount editor={editor} limit={MAX_TEXT_LEN} />}
            <button
              type="button"
              onClick={close}
              title="关闭"
              className="w-7 h-7 rounded-md flex items-center justify-center border-none cursor-pointer bg-transparent text-muted-foreground hover:text-foreground hover:bg-accent/60 transition-colors"
            >
              <X size={16} />
            </button>
          </div>
        </div>
        <div
          className="flex-1 overflow-y-auto custom-scrollbar px-6 py-4 cursor-text"
          // 点击空白区也把光标送进编辑器末尾，别让用户以为「点了没反应」。
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
