// 用户气泡里内联长文本的「展示层折叠」解析器。
//
// 背景：文本节点 / txt 文件附件的正文由 buildInlineSections（mentionInline.ts）
// 以固定格式拼进消息文本并整体落库——发给 agent 的就是这个拼好的字符串，
// 1:1 不可动。但小说剧本几千字原样铺在气泡里会把对话刷得没法看，所以渲染时
// 把这些段识别出来折叠成卡片，点卡片弹大窗看全文。**只动显示，不动数据**：
// 解析失败（格式对不上）就原样渲染，历史消息与新消息走同一条路。
//
// 格式真源在 mentionInline.ts：
//   \n\n【引用画布文本节点《名字》】\n```\n正文\n```
//   \n\n[文件 名字]\n```\n正文\n```
// 改那边的拼接格式必须同步这里的正则，chatInlineSections.test.ts 用真的
// buildInlineSections 产物做往返断言来钉住这条同构。

export interface InlineSection {
  kind: 'node' | 'file';
  name: string;
  content: string;
}

export type BubblePiece = { type: 'text'; text: string } | ({ type: 'section' } & InlineSection);

// 头部 + 代码围栏。围栏内容用非贪婪匹配，靠 lookahead 锚定「下一个段头或
// 字符串结尾」——正文里偶发的 ``` 行不会把段切碎（除非它恰好后跟另一个段头，
// 那种输入本就无法无歧义解析，宁可少折叠也不能错切内容）。
const SECTION_RE =
  /(?:【引用画布文本节点《([^》]+)》】|\[文件 ([^\]\n]+)\])\n```\n([\s\S]*?)\n```(?=\s*(?:【引用画布文本节点《|\[文件 |$))/g;

export function splitBubbleText(text: string): BubblePiece[] {
  const pieces: BubblePiece[] = [];
  let last = 0;
  SECTION_RE.lastIndex = 0;
  for (const m of text.matchAll(SECTION_RE)) {
    const idx = m.index ?? 0;
    if (idx > last) {
      const before = text.slice(last, idx);
      // 段与段之间只剩 buildInlineSections 拼的空行时不产出空 text 片。
      if (before.trim() !== '') pieces.push({ type: 'text', text: before.replace(/\n+$/, '') });
    }
    pieces.push({
      type: 'section',
      kind: m[1] !== undefined ? 'node' : 'file',
      name: (m[1] ?? m[2]).trim(),
      content: m[3],
    });
    last = idx + m[0].length;
  }
  if (pieces.length === 0) return [{ type: 'text', text }];
  if (last < text.length) {
    const rest = text.slice(last);
    if (rest.trim() !== '') pieces.push({ type: 'text', text: rest.replace(/^\n+/, '') });
  }
  return pieces;
}

// 卡片上的预览行：取正文第一行非空文本，超长截尾。只用于展示。
export function sectionPreview(content: string, max = 60): string {
  const line = content.split('\n').find((l) => l.trim() !== '') ?? '';
  const t = line.trim();
  return t.length > max ? `${t.slice(0, max)}…` : t;
}
