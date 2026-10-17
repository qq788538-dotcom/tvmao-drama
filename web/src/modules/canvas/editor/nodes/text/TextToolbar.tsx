// 文本节点的富文本工具栏（按钮集：H1/H2/H3/正文/粗体/斜体/无序/有序/
// 分割线/复制/展开）。既用于节点上方的浮动条，也用于展开编辑弹层的头部。
import { useEffect, useState } from 'react';
import {
  Bold,
  Check,
  Copy,
  Heading1,
  Heading2,
  Heading3,
  Italic,
  List,
  ListOrdered,
  Maximize2,
  Minus,
  Pilcrow,
} from 'lucide-react';
import type { Editor } from '@tiptap/react';

function ToolbarButton({
  title,
  active,
  onAction,
  children,
}: {
  title: string;
  active?: boolean;
  onAction: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      title={title}
      // preventDefault：按钮抢焦点会触发编辑器 blur → 退出编辑态，工具栏就点不了
      // 第二下。stopPropagation：别让 mousedown 冒泡到画布开始拖节点/框选。
      onMouseDown={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
      onClick={(e) => {
        e.stopPropagation();
        onAction();
      }}
      className={`w-7 h-7 rounded-md flex items-center justify-center border-none cursor-pointer transition-colors ${
        active ? 'bg-accent text-foreground' : 'bg-transparent text-muted-foreground hover:text-foreground hover:bg-accent/60'
      }`}
    >
      {children}
    </button>
  );
}

function Divider() {
  return <div className="w-px h-4 bg-border mx-0.5 shrink-0" />;
}

export function TextToolbar({ editor, onExpand }: { editor: Editor; onExpand?: () => void }) {
  // Tiptap 编辑器不是 React 状态——isActive 的变化不会触发重渲染，必须订阅
  // transaction 手动 tick，否则按钮高亮永远停在挂载时刻。
  const [, setTick] = useState(0);
  useEffect(() => {
    const bump = () => setTick((t) => t + 1);
    editor.on('transaction', bump);
    return () => {
      editor.off('transaction', bump);
    };
  }, [editor]);

  const [copied, setCopied] = useState(false);

  const iconSize = 14;
  return (
    <div
      className="flex items-center gap-0.5 px-1.5 py-1 rounded-xl bg-card border border-border shadow-xl whitespace-nowrap"
      onMouseDown={(e) => e.stopPropagation()}
    >
      <ToolbarButton
        title="标题 1"
        active={editor.isActive('heading', { level: 1 })}
        onAction={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
      >
        <Heading1 size={iconSize} />
      </ToolbarButton>
      <ToolbarButton
        title="标题 2"
        active={editor.isActive('heading', { level: 2 })}
        onAction={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
      >
        <Heading2 size={iconSize} />
      </ToolbarButton>
      <ToolbarButton
        title="标题 3"
        active={editor.isActive('heading', { level: 3 })}
        onAction={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
      >
        <Heading3 size={iconSize} />
      </ToolbarButton>
      <ToolbarButton
        title="正文"
        active={editor.isActive('paragraph')}
        onAction={() => editor.chain().focus().setParagraph().run()}
      >
        <Pilcrow size={iconSize} />
      </ToolbarButton>
      <Divider />
      <ToolbarButton
        title="粗体"
        active={editor.isActive('bold')}
        onAction={() => editor.chain().focus().toggleBold().run()}
      >
        <Bold size={iconSize} />
      </ToolbarButton>
      <ToolbarButton
        title="斜体"
        active={editor.isActive('italic')}
        onAction={() => editor.chain().focus().toggleItalic().run()}
      >
        <Italic size={iconSize} />
      </ToolbarButton>
      <Divider />
      <ToolbarButton
        title="无序列表"
        active={editor.isActive('bulletList')}
        onAction={() => editor.chain().focus().toggleBulletList().run()}
      >
        <List size={iconSize} />
      </ToolbarButton>
      <ToolbarButton
        title="有序列表"
        active={editor.isActive('orderedList')}
        onAction={() => editor.chain().focus().toggleOrderedList().run()}
      >
        <ListOrdered size={iconSize} />
      </ToolbarButton>
      <Divider />
      <ToolbarButton title="分割线" onAction={() => editor.chain().focus().setHorizontalRule().run()}>
        <Minus size={iconSize} />
      </ToolbarButton>
      <Divider />
      <ToolbarButton
        title="复制内容"
        onAction={() => {
          void navigator.clipboard.writeText(editor.storage.markdown.getMarkdown()).then(() => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1200);
          });
        }}
      >
        {copied ? <Check size={iconSize} className="text-green-500" /> : <Copy size={iconSize} />}
      </ToolbarButton>
      {onExpand && (
        <ToolbarButton title="展开编辑" onAction={onExpand}>
          <Maximize2 size={iconSize} />
        </ToolbarButton>
      )}
    </div>
  );
}
