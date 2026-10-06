import { fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { PromptMentionEditor, domToString, labelRefs, linkNamedMentions, parseMentionPrompt, serializeMentionPrompt, stripDeadMentions } from './PromptMentionEditor';
import type { ConnectedMediaRef } from './types';

// 缩略图解析走网络，纯 DOM 结构测试不需要它。
vi.mock('./thumbs', () => ({ useThumbUrls: () => ({}) }));

const refs: ConnectedMediaRef[] = [
  { nodeId: 'n-aud', kind: 'audio', url: 'https://x/a.mp3' },
  { nodeId: 'n-img1', kind: 'image', url: 'https://x/1.png' },
  { nodeId: 'n-img2', kind: 'image', url: 'https://x/2.png' },
  { nodeId: 'n-vid', kind: 'video', url: 'https://x/v.mp4' },
];

describe('labelRefs', () => {
  it('图片/视频/音频各自独立计数', () => {
    expect(labelRefs(refs).map((r) => r.label)).toEqual(['音频 1', '图片 1', '图片 2', '视频 1']);
  });
});

describe('serializeMentionPrompt', () => {
  it('token 按当前连线顺序序列化为「图片N」纯文本', () => {
    const out = serializeMentionPrompt('看 @[图片:n-img2] 配 @[音频:n-aud] 和 @[视频:n-vid]', refs);
    expect(out).toBe('看 图片2 配 音频1 和 视频1');
  });

  it('引用已断线的 token 直接剔除（提交路径兜底）', () => {
    expect(serializeMentionPrompt('前@[图片:n-gone]后', refs)).toBe('前后');
  });

  it('删除前面的连线后序号跟随左移', () => {
    // n-img1 断线：n-img2 从「图片2」变成「图片1」。
    const remaining = refs.filter((r) => r.nodeId !== 'n-img1');
    expect(serializeMentionPrompt('@[图片:n-img2]', remaining)).toBe('图片1');
  });
});

describe('stripDeadMentions', () => {
  it('剔除引用已断线节点的标记，保留其余原文', () => {
    const v = 'a @[图片:n-img1] b @[图片:n-gone] c';
    expect(stripDeadMentions(v, refs)).toBe('a @[图片:n-img1] b  c');
  });

  it('kind 不匹配也算失效（同 id 不同类型不能顶替）', () => {
    expect(stripDeadMentions('@[视频:n-img1]', refs)).toBe('');
  });

  it('无死引用时原样返回', () => {
    const v = '@[音频:n-aud] x';
    expect(stripDeadMentions(v, refs)).toBe(v);
  });
});

// 下面这些 innerHTML 全部是 Chrome 实测采集的：在 white-space:pre-wrap 的
// contentEditable 里做对应操作后读 innerHTML，不是臆造的形态。
describe('domToString', () => {
  const html = (s: string) => {
    const d = document.createElement('div');
    d.innerHTML = s;
    return domToString(d);
  };

  it('粘贴多行：<div> 块边界算换行（回归用例——原来整段挤成一行）', () => {
    expect(html('L1<div>L2</div><div>L3</div>')).toBe('L1\nL2\nL3');
  });

  it('按 Enter 换行', () => {
    expect(html('A<div>B</div>')).toBe('A\nB');
  });

  it('首行也是块时不多出前导换行', () => {
    expect(html('<div>A</div><div>B</div>')).toBe('A\nB');
  });

  it('空行（连按两次 Enter / 粘贴含空行）保留', () => {
    expect(html('A<div><br></div><div>B</div>')).toBe('A\n\nB');
  });

  it('末尾 Enter 只算一个换行——块里的 <br> 是浏览器撑高度的占位', () => {
    expect(html('A<div><br></div>')).toBe('A\n');
  });

  it('块末尾的占位 <br> 不重复断行', () => {
    expect(html('<div>A<br></div><div>B</div>')).toBe('A\nB');
  });

  it('行首的 <br> 照常断行', () => {
    expect(html('<br>A')).toBe('\nA');
  });

  it('Shift+Enter 产出的字面 \\n 原样保留（pre-wrap 下 Chrome 不插 <br>）', () => {
    expect(html('A\nB')).toBe('A\nB');
  });

  it('空编辑器', () => {
    expect(html('')).toBe('');
  });

  it('chip 序列化成 token，不读它的展示文本', () => {
    expect(html('看<span data-mention="1" data-kind="image" data-node-id="n-img1"><img><span>图片 1</span></span>行')).toBe(
      '看@[图片:n-img1]行',
    );
  });

  it('跨行的 chip：块边界与 token 同时正确', () => {
    expect(html('A<div><span data-mention="1" data-kind="video" data-node-id="n-vid">▶视频 1</span>B</div>')).toBe(
      'A\n@[视频:n-vid]B',
    );
  });

  it('contentEditable 把内容包进非块级容器时透明展开', () => {
    expect(html('<span><b>A</b>B</span>')).toBe('AB');
  });
});

describe('光标锚点 ZWSP', () => {
  const html = (s: string) => {
    const d = document.createElement('div');
    d.innerHTML = s;
    return domToString(d);
  };

  it('domToString 剥掉 ZWSP——锚点绝不进 canonical（否则 rebuild 判等死循环）', () => {
    expect(html('\u200B<span data-mention="1" data-kind="image" data-node-id="n-1"></span>\u200B 00:03')).toBe(
      '@[图片:n-1] 00:03',
    );
    expect(html('A\u200BB')).toBe('AB');
  });
});

describe('重建 DOM 的块模型', () => {
  const refs: ConnectedMediaRef[] = [
    { kind: 'image', nodeId: 'n-1', url: 'https://example.test/1.png' },
    { kind: 'image', nodeId: 'n-2', url: 'https://example.test/2.png' },
  ];

  it('多行值往返一致：每行一个 <div> 块、行首 chip 前有 ZWSP 锚点', () => {
    // 块模型是防「chip 前退格凭空多一空行」的关键：\n 文本模型下 Chrome 的
    // 删除规范化会塞 <br> 占位，块模型让退格合行走浏览器原生路径。
    const value = '叮当猫过新年\n@[图片:n-1] 00:03 视频帧标注\n@[图片:n-2] 00:06 视频帧标注';
    const { container } = render(
      <PromptMentionEditor value={value} refs={refs} onChange={() => {}} />,
    );
    const ed = container.querySelector('[contenteditable]') as HTMLElement;
    expect(domToString(ed)).toBe(value); // ZWSP 剥净、块界折回 \n，判等不死循环
    const rows = ed.querySelectorAll(':scope > div');
    expect(rows).toHaveLength(3);
    expect(rows[1].firstChild?.textContent).toBe('\u200B'); // 行首 chip 前的光标锚点
    expect(rows[1].querySelector('[data-mention]')).not.toBeNull();
  });

  // 回车不再交给 Chrome 默认行为：光标贴着行首 chip/锚点时它会把 <br> 塞进块内
  // 制造凭空空行（实机取证 <div><br>{chip}…</div>）。手动分裂语义确定：一次一行。
  it('回车做确定性行块分裂：光标处一分为二，不产生多余空行', () => {
    const onChange = vi.fn();
    const { container } = render(
      <PromptMentionEditor value={'AB\n@[图片:n-1] 00:00'} refs={refs} onChange={onChange} />,
    );
    const ed = container.querySelector('[contenteditable]') as HTMLElement;
    const textNode = ed.querySelectorAll(':scope > div')[0].firstChild as Text; // 'AB'
    const sel = window.getSelection()!;
    const r = document.createRange();
    r.setStart(textNode, 1);
    r.collapse(true);
    sel.removeAllRanges();
    sel.addRange(r);
    fireEvent.keyDown(ed, { key: 'Enter' });
    expect(domToString(ed)).toBe('A\nB\n@[图片:n-1] 00:00');
    expect(onChange).toHaveBeenLastCalledWith('A\nB\n@[图片:n-1] 00:00');
  });

  it('空行渲染成 <div><br></div>（Chrome 自己的空行表示法）且往返保留', () => {
    const value = 'A\n\nB';
    const { container } = render(
      <PromptMentionEditor value={value} refs={[]} onChange={() => {}} />,
    );
    const ed = container.querySelector('[contenteditable]') as HTMLElement;
    expect(domToString(ed)).toBe(value);
    expect(ed.querySelectorAll(':scope > div')[1].innerHTML).toBe('<br>');
  });
});

describe('parseMentionPrompt', () => {
  it('拆分文本与提及片段', () => {
    expect(parseMentionPrompt('前@[图片:n-img1]后')).toEqual([
      { type: 'text', text: '前' },
      { type: 'mention', kind: 'image', nodeId: 'n-img1' },
      { type: 'text', text: '后' },
    ]);
  });
});

// ===== 命名引用：@[角色-张三] / @[张三] / @张三 ↔ chip 的闭环 =====
// 连线里的图片节点名（settings.name）与文字写法对上就变 chip；chip 标记里带原文，
// 断线时还原成原文而不是删掉，重连后再变回 chip。
const namedRefs: ConnectedMediaRef[] = [
  { nodeId: 'n-zs', kind: 'image', url: 'https://x/zs.png', name: '张三' },
  { nodeId: 'n-ls', kind: 'image', url: 'https://x/ls.png', name: '李四' },
  { nodeId: 'n-noname', kind: 'image', url: 'https://x/nn.png' },
];

describe('linkNamedMentions', () => {
  it('三种写法都按节点名换成带原文的 chip 标记', () => {
    expect(linkNamedMentions('@[角色-张三]走进房间', namedRefs)).toBe('@[图片:n-zs|[角色-张三]]走进房间');
    expect(linkNamedMentions('@[张三]走进房间', namedRefs)).toBe('@[图片:n-zs|[张三]]走进房间');
    expect(linkNamedMentions('@张三走进房间', namedRefs)).toBe('@[图片:n-zs|张三]走进房间');
  });
  it('名字对不上的原样保留', () => {
    expect(linkNamedMentions('@[角色-王五]和@王五', namedRefs)).toBe('@[角色-王五]和@王五');
  });
  it('已经是 chip 标记的不重复处理', () => {
    const v = '@[图片:n-zs|张三]和@[图片:n-ls]';
    expect(linkNamedMentions(v, namedRefs)).toBe(v);
  });
  it('同一行多个引用各自命中', () => {
    expect(linkNamedMentions('@张三对@李四说', namedRefs)).toBe('@[图片:n-zs|张三]对@[图片:n-ls|李四]说');
  });
  it('名字互为前缀时取最长的（张三 vs 张三丰）', () => {
    const refs2: ConnectedMediaRef[] = [
      { nodeId: 'a', kind: 'image', url: 'https://x/a.png', name: '张三' },
      { nodeId: 'b', kind: 'image', url: 'https://x/b.png', name: '张三丰' },
    ];
    expect(linkNamedMentions('@张三丰出场', refs2)).toBe('@[图片:b|张三丰]出场');
  });
  it('只认图片节点（视频/音频同名不匹配）', () => {
    const refs2: ConnectedMediaRef[] = [{ nodeId: 'v', kind: 'video', url: 'https://x/v.mp4', name: '张三' }];
    expect(linkNamedMentions('@张三', refs2)).toBe('@张三');
  });
});

describe('stripDeadMentions 还原原文', () => {
  it('带原文的标记断线后还原成原来的写法', () => {
    expect(stripDeadMentions('@[图片:n-zs|[角色-张三]]走进房间', [])).toBe('@[角色-张三]走进房间');
    expect(stripDeadMentions('@[图片:n-zs|张三]走进房间', [])).toBe('@张三走进房间');
  });
  it('不带原文的（@ 菜单手选的）仍然删掉', () => {
    expect(stripDeadMentions('@[图片:n-zs]走进房间', [])).toBe('走进房间');
  });
  it('断线→重连闭环：原文回来后再次命中变回 chip', () => {
    const linked = linkNamedMentions('@张三走进房间', namedRefs);
    const restored = stripDeadMentions(linked, []);
    expect(restored).toBe('@张三走进房间');
    expect(linkNamedMentions(restored, namedRefs)).toBe(linked);
  });
});

describe('带原文标记的解析与序列化', () => {
  it('parseMentionPrompt 带出 origin', () => {
    expect(parseMentionPrompt('@[图片:n-zs|[角色-张三]]')).toEqual([
      { type: 'mention', kind: 'image', nodeId: 'n-zs', origin: '[角色-张三]' },
    ]);
  });
  it('serializeMentionPrompt 提交时仍是「图片N」，原文不外泄', () => {
    expect(serializeMentionPrompt('@[图片:n-zs|张三]出场', namedRefs)).toBe('图片1出场');
  });
  it('DOM 往返保留原文（chip 的 data-origin）', () => {
    const { container } = render(
      <PromptMentionEditor value="@[图片:n-zs|[角色-张三]]走" refs={namedRefs} onChange={() => {}} />,
    );
    const ed = container.querySelector('[contenteditable]') as HTMLElement;
    expect(ed.querySelector('[data-mention]')?.textContent).toContain('图片 1');
    expect(domToString(ed)).toBe('@[图片:n-zs|[角色-张三]]走');
  });
});
