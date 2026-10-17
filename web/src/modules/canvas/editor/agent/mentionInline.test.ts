import { describe, it, expect } from 'vitest';

import { INLINE_TEXT_MAX, buildInlineSections, capInlineText, splitTextChips } from './mentionInline';

describe('buildInlineSections（无 URL chip 的正文内联）', () => {
  it('text chip（画布文字节点）内联成《名称》标注 + 代码块', () => {
    const out = buildInlineSections([
      { artifact_id: 'n-1', name: '短片剧本《暴雨单》', kind: 'text', text: '第一场 雨夜' },
    ]);
    expect(out).toBe('\n\n【引用画布文本节点《短片剧本《暴雨单》》】\n```\n第一场 雨夜\n```');
  });

  it('file chip 保持既有 [文件 name] 格式，两类可混排且按 chip 顺序拼接', () => {
    const out = buildInlineSections([
      { artifact_id: 'a1', name: 'notes.md', kind: 'file', text: 'nn' },
      { artifact_id: 'n-1', name: '剧本', kind: 'text', text: 'tt' },
    ]);
    expect(out).toBe('\n\n[文件 notes.md]\n```\nnn\n```\n\n【引用画布文本节点《剧本》】\n```\ntt\n```');
  });

  it('媒体 chip（有 URL、走 mentions[]）与无正文的 chip 都被忽略', () => {
    const out = buildInlineSections([
      { artifact_id: 'i1', name: '图', kind: 'image', url: 'http://x/1.png' },
      { artifact_id: 'v1', name: '片', kind: 'video', url: 'http://x/1.mp4' },
      { artifact_id: 't1', name: '空文字节点', kind: 'text' },
    ]);
    expect(out).toBe('');
  });

  it('超过 50KB 的文字节点正文被截断并附说明（与文件附件同一上限）', () => {
    const raw = 'x'.repeat(INLINE_TEXT_MAX + 10);
    const out = buildInlineSections([
      { artifact_id: 'n-1', name: '长剧本', kind: 'text', text: raw },
    ]);
    expect(out).toContain('…(已截断，剩余 10 字符)');
    // 截断发生在正文层面：内联总长受控。
    expect(out.length).toBeLessThan(INLINE_TEXT_MAX + 200);
  });

  it('capInlineText 未超限时原样返回', () => {
    expect(capInlineText('abc')).toBe('abc');
  });
});

describe('splitTextChips', () => {
  const mk = (kind: 'file' | 'text' | 'image', text?: string, charCount?: number) =>
    ({ artifact_id: 'a', name: 'n', kind, text, charCount }) as never;

  it('阈值内正文内联，超阈值与无正文（大文件 chip）走 oversize', () => {
    const small = mk('file', '短文本');
    const big = mk('text', 'x'.repeat(INLINE_TEXT_MAX + 1));
    const noText = mk('file', undefined, 500_000); // 上传时超阈值，chip 不带正文
    const media = mk('image');
    const { inline, oversize } = splitTextChips([small, big, noText, media]);
    expect(inline).toEqual([small]);
    expect(oversize).toEqual([big, noText]);
  });

  it('恰好等于阈值仍内联（边界含端点）', () => {
    const edge = mk('file', 'x'.repeat(INLINE_TEXT_MAX));
    expect(splitTextChips([edge]).inline).toEqual([edge]);
  });
});
