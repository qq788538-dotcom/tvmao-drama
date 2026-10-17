// 文本节点的 Tiptap 编辑器工厂。编辑态是 ProseMirror 文档树，存储态是 markdown
// 字符串（settings.text）——纯文本是合法 markdown，所以存量节点零迁移。选
// markdown 而不是 ProseMirror JSON：这个字段要直接喂给下游生成节点当兜底
// prompt（store.getConnectedInputs → texts），也要让 agent 直接读写，序列化
// 格式必须对 LLM/人类可读。
import { useRef } from 'react';
import { useEditor, type Editor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import Placeholder from '@tiptap/extension-placeholder';
import CharacterCount from '@tiptap/extension-character-count';
import { Markdown } from 'tiptap-markdown';

// 文本节点的字数「提示」阈值——只用来把 CharCount 徽标标红,不拦输入、不截断、
// 不拦保存（宁可红着也不许丢字）。曾经它同时是 Tiptap
// CharacterCount 的 limit,超限的粘贴被 filterTransaction 直接吃掉,用户以为贴
// 进去了、实际只存了前一万字。
// 阈值 1 万,与脚本生成器的 MAX_SCRIPT_LEN
// 解耦。脚本生成器仍是 5000(前端 ScriptGenerator 自查 + 后端
// canvasscript.MaxScriptRunes 权威拦截)——超长文本节点连过去时由生成器在自己的
// 边界上报错,不会静默截断。
export const MAX_TEXT_LEN = 10000;

export interface MarkdownEditorOptions {
  /** 初始 markdown。编辑器不受控——外部变更不会回灌，进入编辑态时重新挂载取值。 */
  value: string;
  onChange: (markdown: string) => void;
  onBlur?: () => void;
  autoFocus?: boolean;
}

export function useMarkdownEditor({ value, onChange, onBlur, autoFocus }: MarkdownEditorOptions): Editor | null {
  // handlePaste 的闭包在 useEditor 返回前就要创建，拿不到 editor——用 ref 转一手。
  const editorRef = useRef<Editor | null>(null);
  const editor = useEditor({
    extensions: [
      // 工具栏只暴露 H1-H3；其余 StarterKit 默认项（引用/代码块
      // 等）保留 markdown 解析能力但不给按钮——agent 写入的内容可能带这些语法，
      // 解析器砍掉的话会原样露出符号。
      StarterKit.configure({ heading: { levels: [1, 2, 3] } }),
      // html:false——settings.text 会被拼进生成 prompt 与 agent 上下文，允许内嵌
      // HTML 等于给这两条下游开了注入口子。
      // transformPastedText——粘贴的 markdown 原文（# 标题、**粗体** 等）按
      // markdown 解析成富文本；默认关着时会被当纯文本转义，用户贴一份 md
      // 文档进来满屏都是符号。transformCopiedText 对称：从编辑器复制出去的
      // 是 markdown 原文，与存储格式一致。
      Markdown.configure({
        html: false,
        breaks: true,
        transformPastedText: true,
        transformCopiedText: true,
      }),
      Placeholder.configure({ placeholder: '输入内容…' }),
      // 故意不传 limit。Tiptap CharacterCount 的 limit 是一道 filterTransaction:
      // 任何让文档超过上限的输入/粘贴会被整条拒绝或截断,用户贴一份长稿进来
      // 只进去前一万字,剩下的无声丢失。MAX_TEXT_LEN 在这里只是 CharCount 的
      // 标红阈值(超了变红提醒),不是硬闸——settings.text 存的是整块 markdown,
      // 后端快照无长度限制,超限照样全量保存。下游真有长度约束的地方
      // (canvasscript.MaxScriptRunes=5000)在自己的边界上报错,不由这里代劳。
      CharacterCount,
    ],
    content: value,
    autofocus: autoFocus ? 'end' : false,
    editorProps: {
      // markdown-body 复用聊天气泡的 markdown 样式——编辑态和只读态长得一致，
      // 切换时内容不跳动。
      attributes: { class: 'markdown-body tiptap-text-editor outline-none text-sm' },
      // 粘贴一律取纯文本按 markdown 解析。transformPastedText 只对「剪贴板仅有
      // text/plain」的粘贴生效——从 VSCode/Typora/飞书/网页复制都会额外带
      // text/html 通道，ProseMirror 优先吃 HTML，md 原文就按字面进来了（# 满屏）。
      // 本编辑器的原生格式就是 markdown，纯文本优先是正确语义；代价是从
      // Word/网页复制的富文本样式不保真——那不是本节点的使用场景。
      handlePaste: (_view, event) => {
        const ed = editorRef.current;
        const text = event.clipboardData?.getData('text/plain');
        if (!ed || !text) return false;
        event.preventDefault();
        // insertContent 已被 tiptap-markdown 补丁（insertContentAt 对字符串走
        // markdown parser）——直接喂原文即可。不要先手动 parser.parse 再插入：
        // 那样 HTML 结果会被再当 markdown 解析一遍，html:false 之下标签全被转义。
        ed.commands.insertContent(text);
        return true;
      },
    },
    onUpdate: ({ editor }) => {
      onChange(editor.storage.markdown.getMarkdown());
    },
    onBlur: () => {
      onBlur?.();
    },
  });
  editorRef.current = editor;
  return editor;
}
