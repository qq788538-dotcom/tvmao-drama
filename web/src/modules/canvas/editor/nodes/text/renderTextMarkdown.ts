// 只读态的 markdown → HTML 渲染。与 MessageBubble.tsx 同一套 marked+DOMPurify，
// 但不做图片/视频 anchor 替换——文本节点只承载文字。
import { marked } from 'marked';
import DOMPurify from 'dompurify';

export function renderTextMarkdown(src: string): string {
  const html = marked.parse(src, { async: false, breaks: true, gfm: true }) as string;
  // 禁掉 <del>/<s>/<strike>——marked+GFM 会误把中文标点密集的文本整段裹进
  // <del>（同 MessageBubble 的教训）。文本节点里正经的删除线极少，剥掉很安全。
  return DOMPurify.sanitize(html, { FORBID_TAGS: ['del', 's', 'strike'] });
}
