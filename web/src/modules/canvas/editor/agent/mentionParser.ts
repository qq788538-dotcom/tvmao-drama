// 聊天输入里的 mention 以 `@[name](artifact_id)` token 形式存储。
// 它们能在光标 / 重渲染周期里存活下来，因为之后没法从裸的 `@name` 还原出
// artifact_id——我们在 raw 字符串里保留结构化形式，只在显示 + 提取 payload 时
// 拍平成 `@name`。

export interface MentionData {
  artifact_id: string;
  name: string;
}

export interface ExtractResult {
  plainText: string;
  mentions: MentionData[];
}

// extractMentions 把 `@[name](artifact_id)` token 转成纯 `@name` 用于显示，
// 并提取出 artifact_id 列表（保持顺序）。
export function extractMentions(raw: string): ExtractResult {
  const re = /@\[([^\]]+)\]\(([^)]+)\)/g;
  const mentions: MentionData[] = [];
  const plain = raw.replace(re, (_, name: string, id: string) => {
    mentions.push({ artifact_id: id, name });
    return `@${name}`;
  });
  return { plainText: plain, mentions };
}

// findActiveMention 从光标处往回走，直到遇到空白或 `@`。
// 用来检测「用户正在输入一个 mention」，这样 picker 能带着部分查询词打开。
export function findActiveMention(
  raw: string,
  cursorPos: number,
): { startIndex: number; query: string } | null {
  let i = cursorPos - 1;
  while (i >= 0) {
    const c = raw[i];
    if (c === '@') return { startIndex: i, query: raw.slice(i + 1, cursorPos) };
    if (c === ' ' || c === '\n' || c === '\t') return null;
    i--;
  }
  return null;
}

// applyMentionInsert 把当前活跃的 `@query` 子串（含开头的 `@`）替换成正式的
// `@[name](artifact_id) ` token + 末尾空格。返回新文本以及光标应该落到的位置。
export function applyMentionInsert(
  raw: string,
  start: number,
  cursor: number,
  pick: MentionData,
): { next: string; nextCursor: number } {
  const insert = `@[${pick.name}](${pick.artifact_id}) `;
  const next = raw.slice(0, start) + insert + raw.slice(cursor);
  return { next, nextCursor: start + insert.length };
}
