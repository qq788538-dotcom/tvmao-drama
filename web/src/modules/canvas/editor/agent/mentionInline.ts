// mentionInline —— 无 URL chip（.txt/.md 文件附件、画布文字节点）的正文内联。
//
// 这两类 chip 没有对象存储 URL，进不了 mentions[]（后端 buildUserMessage 只
// 处理带 URL 的媒体引用），唯一能到达 planner 的通道是把正文拼进消息文本。
// 原先这段逻辑内联在 AgentPanel.send() 里、只服务文件附件；文字节点接入后
// 抽到这里，两类走同一套代码块格式与同一个 50KB 上限，避免两份格式漂移。

import type { RichMention } from './MentionInput';

// 内联/工具分流阈值（spec 2026-08-31）：≤ 该值的正文直接内联进消息（一次到位
// 零往返）；超过的走「引用行 + read_attachment 分段读取」通道，不再截断——
// 内联正文进历史每轮重放，大文本一条消息就能超过 backbone 窗口。
export const INLINE_TEXT_MAX = 100 * 1024;

// capInlineText 超限截断并附说明——不报错不静默丢，跟文件附件同一措辞。
export function capInlineText(raw: string): string {
  if (raw.length <= INLINE_TEXT_MAX) return raw;
  return `${raw.slice(0, INLINE_TEXT_MAX)}\n…(已截断，剩余 ${raw.length - INLINE_TEXT_MAX} 字符)`;
}

// buildInlineSections 把 file / text 两类 chip 的正文各拼成一段代码块，按 chip
// 顺序连接；调用方把返回值追加到消息文本末尾。media chip（有 URL，走 mentions[]）
// 在这里被忽略。超过阈值的 chip 已在 send() 分流到 read_attachment 通道，
// 正常到不了这里；capInlineText 只是防御性兜底。
export function buildInlineSections(chips: RichMention[]): string {
  return chips
    .filter((m) => (m.kind === 'file' || m.kind === 'text') && m.text)
    .map((m) =>
      m.kind === 'text'
        ? `\n\n【引用画布文本节点《${m.name}》】\n\`\`\`\n${capInlineText(m.text!)}\n\`\`\``
        : `\n\n[文件 ${m.name}]\n\`\`\`\n${capInlineText(m.text!)}\n\`\`\``,
    )
    .join('');
}

// splitTextChips 把 file/text chip 按内联阈值分成两拨：inline 直接拼进消息，
// oversize 走 mentions[] + read_attachment 通道。抽成纯函数是为了可测——
// 这条分流错了要么历史被大文本撑爆（该走工具的进了内联），要么 agent 拿到
// 一个读不到正文的引用（该内联的进了工具通道却没有 asset）。
export function splitTextChips(chips: RichMention[]): {
  inline: RichMention[];
  oversize: RichMention[];
} {
  const textChips = chips.filter((m) => m.kind === 'file' || m.kind === 'text');
  const inline = textChips.filter((m) => !!m.text && m.text.length <= INLINE_TEXT_MAX);
  const oversize = textChips.filter((m) => !(m.text && m.text.length <= INLINE_TEXT_MAX));
  return { inline, oversize };
}
