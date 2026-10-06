import { describe, expect, it } from 'vitest';

import { buildInlineSections } from './mentionInline';
import { sectionPreview, splitBubbleText } from './chatInlineSections';

const NOVEL = '第一章 雨夜\n他推开门，雨声灌了进来。\n\n第二章 追逐\n巷子尽头没有光。';

describe('splitBubbleText', () => {
  it('无内联段的普通消息原样返回', () => {
    const pieces = splitBubbleText('帮我写个短片剧本');
    expect(pieces).toEqual([{ type: 'text', text: '帮我写个短片剧本' }]);
  });

  // 往返断言：解析的输入用真的 buildInlineSections 产出——mentionInline 改
  // 拼接格式而没同步这里的正则时，这条会当场红。
  it('识别 buildInlineSections 真实产物（节点 + 文件），正文逐字保留', () => {
    const raw =
      '用这两份写分镜' +
      buildInlineSections([
        { artifact_id: 'a1', name: '剧本初稿', kind: 'text', text: NOVEL },
        { artifact_id: 'a2', name: '小说.txt', kind: 'file', text: '正文A\n正文B' },
      ]);
    const pieces = splitBubbleText(raw);
    expect(pieces).toHaveLength(3);
    expect(pieces[0]).toEqual({ type: 'text', text: '用这两份写分镜' });
    expect(pieces[1]).toMatchObject({ type: 'section', kind: 'node', name: '剧本初稿', content: NOVEL });
    expect(pieces[2]).toMatchObject({ type: 'section', kind: 'file', name: '小说.txt', content: '正文A\n正文B' });
  });

  it('正文内含 ``` 围栏行不切碎内容', () => {
    const tricky = '前文\n```\n代码样例\n```\n后文';
    const raw = buildInlineSections([{ artifact_id: 'a1', name: 'N', kind: 'file', text: tricky }]);
    const pieces = splitBubbleText('看看' + raw);
    const section = pieces.find((p) => p.type === 'section');
    expect(section).toMatchObject({ content: tricky });
  });

  // 契约：内联段永远在消息**末尾**（AgentPanel 的 plainText = 用户文字 +
  // buildInlineSections(...)），段后不会再有正文。段后跟了普通文字的输入无法
  // 与「正文里恰好含 ``` 行」无歧义区分，解析器选择整段不折叠（原样渲染），
  // 宁可少折叠也不能错切内容。
  it('段后跟普通文字（非 composer 产物）时不折叠、原样保留', () => {
    const raw =
      buildInlineSections([{ artifact_id: 'a1', name: 'N', kind: 'text', text: '正文' }]) +
      '\n\n按第二章展开';
    const pieces = splitBubbleText(raw);
    expect(pieces).toEqual([{ type: 'text', text: raw }]);
  });
});

describe('sectionPreview', () => {
  it('取首行非空文本并截尾', () => {
    expect(sectionPreview('\n\n第一章 雨夜\n后续')).toBe('第一章 雨夜');
    expect(sectionPreview('x'.repeat(80), 10)).toBe(`${'x'.repeat(10)}…`);
  });
});
