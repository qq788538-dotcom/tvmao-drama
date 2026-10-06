// 字数计数徽标。独立组件是因为它要订阅编辑器 transaction 才能实时刷新——
// 挂在 TextNode/弹层里会带着整个节点一起重渲染，没必要。
import { useEffect, useState } from 'react';
import type { Editor } from '@tiptap/react';

export function CharCount({ editor, limit }: { editor: Editor; limit: number }) {
  const [count, setCount] = useState<number>(() => editor.storage.characterCount.characters());
  useEffect(() => {
    const update = () => setCount(editor.storage.characterCount.characters());
    update();
    editor.on('transaction', update);
    return () => {
      editor.off('transaction', update);
    };
  }, [editor]);

  return (
    <span className={`text-[11px] ${count >= limit ? 'text-red-400' : 'text-muted-foreground/70'}`}>
      {count}/{limit}
    </span>
  );
}
