// PromptMentionEditor — 生成节点提示词编辑器，支持 @ 引用上游连线媒体。
// canonical = 字符串，内嵌 @[图片:<nodeId>]/@[视频:<nodeId>]/@[音频:<nodeId>] 标记。
// 用 nodeId 而非序号绑定身份：连线增删时序号会漂移（删掉图片1后原图片2变成图片1），
// id 稳定——断线清理精准、chip 显示的序号(图片 2)在渲染时按当前连线顺序实时计算。
// 图片/视频/音频各自独立计数（不用全局统一的 @1/@2/@3）。
// 机制同项目管理的 StoryboardPromptEditor（contentEditable 非受控 + chip span +
// canonical 字符串），但按模块隔离原则独立实现，不跨模块 import。
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { ConnectedMediaRef } from './types';
import { MENTION_RE, mentionToken } from './utils/mentions';
import { ThumbImg } from './ThumbImg';
import { useThumbUrls } from './thumbs';

const LABEL_BY_KIND: Record<ConnectedMediaRef['kind'], string> = {
  image: '图片',
  video: '视频',
  audio: '音频',
};
const KIND_BY_LABEL: Record<string, ConnectedMediaRef['kind']> = {
  图片: 'image',
  视频: 'video',
  音频: 'audio',
};
// 正则的单一真源在 utils/mentions.ts——store 拷贝节点时也要用它重写 nodeId。

// 带展示序号的引用：label = 「图片 2」，序号在同类型内独立计数。
export interface LabeledMediaRef extends ConnectedMediaRef {
  label: string;
}

// 为连线引用计算各类型独立序号标签（图片 1..N / 视频 1..M / 音频 1..K）。
export function labelRefs(refs: ConnectedMediaRef[]): LabeledMediaRef[] {
  const counters: Record<ConnectedMediaRef['kind'], number> = { image: 0, video: 0, audio: 0 };
  return refs.map((r) => {
    counters[r.kind] += 1;
    return { ...r, label: `${LABEL_BY_KIND[r.kind]} ${counters[r.kind]}` };
  });
}

// 提交前序列化：@[图片:<id>] → 「图片2」纯文本（按当前连线里同类型的位置），
// 上游模型拿到的是干净文本。引用已不在连线里的 token 直接剔除（正常情况下
// 断线清理已经做过，这里是提交路径的最后兜底）。
export function serializeMentionPrompt(value: string, refs: ConnectedMediaRef[]): string {
  const labeled = labelRefs(refs);
  // 第三段原文只服务于断线还原，提交给模型的永远是「图片N」——替换回调不读它。
  return value.replace(MENTION_RE, (_m, kindLabel: string, nodeId: string) => {
    const kind = KIND_BY_LABEL[kindLabel];
    const hit = labeled.find((r) => r.nodeId === nodeId && r.kind === kind);
    return hit ? hit.label.replace(' ', '') : '';
  });
}

// 断线清理：引用了「已不在连线里的节点」的 @ 标记——由文字引用转换来的（带原文段）
// 还原成原来的写法（`@张三` / `@[角色-张三]`），重连后 linkNamedMentions 会再把它变回
// chip；从 @ 菜单手选的（无原文）直接剔除。调用方在 effect 里监听连线集合变化
// 调用——删连线、删上游节点、undo/redo 全部路径统一覆盖。
export function stripDeadMentions(value: string, refs: ConnectedMediaRef[]): string {
  return value.replace(MENTION_RE, (m, kindLabel: string, nodeId: string, origin?: string) => {
    const kind = KIND_BY_LABEL[kindLabel];
    if (refs.some((r) => r.nodeId === nodeId && r.kind === kind)) return m;
    return origin ? `@${origin}` : '';
  });
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// 文字引用 → chip：`@[角色-张三]` / `@[场景-张三]` / `@[物品-张三]` / `@[张三]` / `@张三`，
// 「张三」与连线里某个**图片**节点的自定义名相同时换成带原文的标记（原文 = `@` 后的
// 字面，断线还原用）。对不上名字的一字不动——名字是唯一依据，不做模糊匹配。
// 裸写法 `@张三` 没有边界符（中文不分词），按连线名做前缀匹配，同时连着「张三」和
// 「张三丰」时取最长的。只扫描已有标记之外的文本段，已是 chip 的不重复处理。
// 调用方与 stripDeadMentions 放在同一个 effect（依赖 prompt + 连线指纹），编辑与连线
// 两个触发时机自然都覆盖。
const SCRIPT_TOKEN_LABELS = '角色|场景|物品';
export function linkNamedMentions(value: string, refs: ConnectedMediaRef[]): string {
  const named = refs.filter((r) => r.kind === 'image' && r.name);
  if (named.length === 0 || !value.includes('@')) return value;
  // 最长名优先：前缀关系的名字（张三 / 张三丰）先试长的。同名多个取连线顺序第一个。
  const byLen = [...named].sort((a, b) => b.name!.length - a.name!.length);
  const alt = byLen.map((r) => escapeRe(r.name!)).join('|');
  const bracketed = new RegExp(`@\\[(?:(?:${SCRIPT_TOKEN_LABELS})-)?(${alt})\\]`, 'g');
  const bare = new RegExp(`@(${alt})`, 'g');
  const tokenFor = (name: string, origin: string) => {
    const hit = named.find((r) => r.name === name)!;
    return mentionToken(LABEL_BY_KIND.image, hit.nodeId, origin);
  };
  return parseMentionPrompt(value)
    .map((seg) => {
      if (seg.type !== 'text') return mentionToken(LABEL_BY_KIND[seg.kind], seg.nodeId, seg.origin);
      return seg.text
        .replace(bracketed, (m, name: string) => tokenFor(name, m.slice(1)))
        .replace(bare, (m, name: string) => tokenFor(name, m.slice(1)));
    })
    .join('');
}

type Seg =
  | { type: 'text'; text: string }
  | { type: 'mention'; kind: ConnectedMediaRef['kind']; nodeId: string; origin?: string };

export function parseMentionPrompt(value: string): Seg[] {
  const out: Seg[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  MENTION_RE.lastIndex = 0;
  while ((m = MENTION_RE.exec(value))) {
    if (m.index > last) out.push({ type: 'text', text: value.slice(last, m.index) });
    // origin 只在有值时占字段，无原文的标记解析结果与旧版逐字相同。
    out.push({ type: 'mention', kind: KIND_BY_LABEL[m[1]], nodeId: m[2], ...(m[3] ? { origin: m[3] } : {}) });
    last = m.index + m[0].length;
  }
  if (last < value.length) out.push({ type: 'text', text: value.slice(last) });
  return out;
}

function buildChip(ref: LabeledMediaRef, thumbUrl?: string, origin?: string): HTMLSpanElement {
  const span = document.createElement('span');
  span.contentEditable = 'false';
  span.dataset.mention = '1';
  span.dataset.kind = ref.kind;
  span.dataset.nodeId = ref.nodeId;
  // 原文跟着 chip 走，DOM → canonical 时写回标记，断线还原才有据可依。
  if (origin) span.dataset.origin = origin;
  span.className = 'cv-mention-chip';
  if (ref.kind === 'image') {
    const im = document.createElement('img');
    // 只挂缩略图,绝不挂原图:pending 时留空占位,由 thumbs effect 解析后原位回填
    // ——先挂原图会触发一次全尺寸下载,缩略图机制就白费了。
    if (thumbUrl) im.src = thumbUrl;
    im.dataset.rawUrl = ref.url;
    im.alt = ref.label;
    im.className = 'cv-mention-chip-img';
    span.appendChild(im);
  } else {
    // 视频/音频没有稳定的缩略图，用类型符号占位（视频▶ / 音频♪）。
    const ic = document.createElement('span');
    ic.className = 'cv-mention-chip-ic';
    ic.textContent = ref.kind === 'video' ? '▶' : '♪';
    span.appendChild(ic);
  }
  const t = document.createElement('span');
  t.className = 'cv-mention-chip-text';
  t.textContent = ref.label;
  span.appendChild(t);
  return span;
}

// 光标锚点：零宽空格。chip 是 contentEditable=false——当它顶在行首（前面没有
// 可编辑文本节点）时，浏览器没有位置可以放插入符，光标永远移不到 chip 前面
// （高级编辑追加的行全是 chip 开头，实机踩中）。在行首/行尾紧邻 chip 处补一个
// 不可见的 ZWSP 文本节点给光标落脚；linesOf 解析时剥掉，canonical 里永远不含它
// （否则 rebuild 的「DOM 序列化 ≠ 外部值」判等会死循环重建）。
const CARET_ANCHOR = '\u200B';

// 重建 DOM 用 Chrome 原生的「每行一个 <div> 块」模型，而不是字面 \n 文本。
// 两个模型 linesOf 都能解析，但编辑操作只认块模型：\n 文本模型下在行首 chip 前
// 退格，Chrome 的规范化会往行首塞 <br> 占位——上方凭空多出一个空行（实机踩中）。
// 让重建产物与 Chrome 自己敲 Enter 的产物同构，退格合行等操作就落在浏览器的
// 原生路径上，不再产生 \n/<br> 混排的畸形结构。空行 = <div><br></div>（Chrome
// 自己的表示法，linesOf 已按空行解析）。
function buildFragment(value: string, labeled: LabeledMediaRef[], thumbOf: (url: string) => string | undefined): DocumentFragment {
  const fragment = document.createDocumentFragment();
  // 空值保持空 DOM（不渲染 <div><br></div> 占位行）：占位行会让首次输入落在
  // 块外/块内的位置漂移，序列化凭空多出一个空行；空编辑器交给浏览器自己起步。
  if (value === '') return fragment;
  const lines = value.split('\n');
  lines.forEach((line) => {
    const div = document.createElement('div');
    if (line === '') {
      div.appendChild(document.createElement('br'));
      fragment.appendChild(div);
      return;
    }
    const segs = parseMentionPrompt(line);
    segs.forEach((seg, i) => {
      if (seg.type === 'text') {
        div.appendChild(document.createTextNode(seg.text));
        return;
      }
      const ref = labeled.find((r) => r.nodeId === seg.nodeId && r.kind === seg.kind);
      // 找不到引用（理论上已被断线清理剔除）：不渲染，等 canonical 被清理后自然消失。
      if (!ref) return;
      if (i === 0) div.appendChild(document.createTextNode(CARET_ANCHOR));
      div.appendChild(buildChip(ref, thumbOf(ref.url), seg.origin));
      if (i === segs.length - 1) div.appendChild(document.createTextNode(CARET_ANCHOR));
    });
    fragment.appendChild(div);
  });
  return fragment;
}

// contentEditable 里换行有三种形态，实测 Chrome（white-space:pre-wrap）：
//   按 Enter / 粘贴多行 → 块元素   A<div>B</div>
//   连按两次 Enter      → 空块     A<div><br></div><div>B</div>
//   Shift+Enter         → 字面 \n  A\nB
// 只认 <br> 会把块边界整个吃掉，多行提示词序列化成一整行——而且当场看不出来
// （DOM 里的块还在分行显示），要等 rebuild（连线增删/重进页面）才暴露。
const BLOCK_TAGS = new Set(['DIV', 'P']);
// 只列这两个：编辑器内容全部由 contentEditable 自己产出或 execCommand('insertText')
// 插入纯文本（handlePaste 只取 text/plain），进不来别的块标签。

// DOM → canonical 字符串。按「行」建模：块元素各自成行，<br> 断行，行间用 \n 连接。
// contentEditable 可能把文本或 chip 包进任意层容器，必须递归读取；命中 chip 后
// 不能继续读取它的展示子节点，否则会把「图片 2」「▶」写进提示词。
function linesOf(nodes: Node[]): string[] {
  const lines: string[] = [];
  // 保证有「当前行」可写——首节点是 <br> 或块时，前面的空行也要占位。
  const cur = () => {
    if (lines.length === 0) lines.push('');
    return lines.length - 1;
  };
  const append = (s: string) => {
    lines[cur()] += s;
  };
  nodes.forEach((node, i) => {
    if (node.nodeType === Node.TEXT_NODE) {
      // 剥掉光标锚点 ZWSP（见 CARET_ANCHOR）：它只服务于光标定位，进了 canonical
      // 会破坏 rebuild 判等，还会随提交流进提示词。
      append((node.textContent || '').replace(/\u200B/g, ''));
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const el = node as HTMLElement;
    if (el.dataset.mention) {
      append(
        mentionToken(
          LABEL_BY_KIND[(el.dataset.kind as ConnectedMediaRef['kind']) || 'image'],
          el.dataset.nodeId || '',
          el.dataset.origin,
        ),
      );
      return;
    }
    if (el.tagName === 'BR') {
      cur();
      // 一串节点末尾的 <br> 是浏览器撑行高的占位符（<div>B<br></div>），不是用户
      // 敲的换行——算进去每行都会凭空多一个空行。
      if (i < nodes.length - 1) lines.push('');
      return;
    }
    const inner = linesOf(Array.from(el.childNodes));
    if (BLOCK_TAGS.has(el.tagName)) {
      // 块自成一行：lines 已有内容时这就是「另起一行」，lines 为空时它直接成为
      // 首行——所以开头的块不会凭空顶出一个前导换行。空块（<div><br></div>，
      // 即用户敲出来的空行）inner 为空，补一个空行占位。
      lines.push(...(inner.length ? inner : ['']));
      return;
    }
    // 非块级容器（<span>/<b>…）透明展开：首行接在当前行尾，其余照常成行。
    if (inner.length) {
      append(inner[0]);
      lines.push(...inner.slice(1));
    }
  });
  return lines;
}

export function domToString(root: HTMLElement): string {
  return linesOf(Array.from(root.childNodes)).join('\n');
}

export function PromptMentionEditor({
  value,
  refs,
  placeholder,
  onChange,
  onBlur,
  className,
  firstLineIndent = 0,
}: {
  value: string;
  refs: ConnectedMediaRef[];
  placeholder?: string;
  onChange: (v: string) => void;
  // 失焦回调：让调用方在用户结束输入时立即 flush（局部保存的 prompt debounce
  // 需要 blur 时提前落库，不用等 1.5s）。
  onBlur?: () => void;
  className?: string;
  // 首行左侧让位宽度(px)：调用方在编辑器首行行首叠了元素（绘图节点的预设
  // chip）时传入，text-indent 让首行文字/placeholder 从元素右侧接着排、
  // 换行后回到行首——chip 本体不进 contenteditable 文档流（进了会被序列化
  // 进提示词），这是「视觉内嵌」方案的编辑器侧半边。
  firstLineIndent?: number;
}) {
  const edRef = useRef<HTMLDivElement>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; range: Range } | null>(null);
  const [active, setActive] = useState(0);
  const [empty, setEmpty] = useState(true);
  const labeled = labelRefs(refs);
  // @ 菜单排序与缩略图行一致:按类型分组(图片→视频→音频),组内保持连线顺序。
  // 只影响菜单展示顺序;label/序列化下标仍按连线顺序算,与提交数组严格对齐。
  const KIND_RANK: Record<ConnectedMediaRef['kind'], number> = { image: 0, video: 1, audio: 2 };
  const menuRefs = [...labeled].sort((a, b) => KIND_RANK[a.kind] - KIND_RANK[b.kind]);
  const refsFingerprint = refs.map((r) => `${r.kind}:${r.nodeId}`).join('|');
  // 图片一律走展示层缩略图(后端换取 400w 签名 URL),原图只在提交 inputs 里出现。
  const thumbMap = useThumbUrls(refs.filter((r) => r.kind === 'image').map((r) => r.url));
  const thumbOf = (url: string) => thumbMap[url];

  function rebuild() {
    const ed = edRef.current;
    if (!ed) return;
    ed.innerHTML = '';
    ed.appendChild(buildFragment(value, labelRefs(refs), thumbOf));
    setEmpty(ed.textContent?.trim() === '' && !ed.querySelector('[data-mention]'));
  }

  // 非受控（受控 contenteditable 会光标抖动）：只在「外部值 ≠ DOM 序列化值」时重建——
  // 覆盖 snippet 插入 / 断线清理等外部写入；自己敲键盘 emit 的回流是 no-op。
  // 连线增删（refsFingerprint 变）也要重建：chip 上显示的序号(图片 2)可能整体漂移。
  useEffect(() => {
    const ed = edRef.current;
    if (!ed) return;
    if (domToString(ed) === value) return;
    // 正在输入时被外部改写（典型：刚敲完 `@张三`，对账 effect 把它换成 chip 回流），
    // 重建 innerHTML 会把光标丢到编辑器外/开头，用户接着打字就落错位置。恢复到
    // 末尾：这类改写几乎都发生在正在输入的行尾（敲字、粘贴脚本片段）。
    const focused = document.activeElement === ed;
    rebuild();
    if (focused) {
      const sel = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(ed);
      range.collapse(false);
      sel?.removeAllRanges();
      sel?.addRange(range);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  useEffect(() => {
    rebuild();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refsFingerprint]);

  const thumbsFingerprint = Object.values(thumbMap).join('|');
  useEffect(() => {
    const ed = edRef.current;
    if (!ed) return;
    ed.querySelectorAll<HTMLImageElement>('img.cv-mention-chip-img[data-raw-url]').forEach((im) => {
      const t = thumbMap[im.dataset.rawUrl ?? ''];
      if (t && im.src !== t) im.src = t;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [thumbsFingerprint]);

  function emit() {
    const ed = edRef.current;
    if (!ed) return;
    onChange(domToString(ed));
    setEmpty(ed.textContent?.trim() === '' && !ed.querySelector('[data-mention]'));
  }

  function handleInput(e: React.FormEvent<HTMLDivElement>) {
    const ne = e.nativeEvent as InputEvent;
    // IME(中文输入法)下 '@' 可能以 insertCompositionText 形态到达,一并识别。
    const typedAt = ne.data === '@' && (ne.inputType === 'insertText' || ne.inputType === 'insertCompositionText');
    if (typedAt && refs.length > 0) {
      const sel = window.getSelection();
      if (sel && sel.rangeCount) {
        const range = sel.getRangeAt(0).cloneRange();
        let rect = range.getBoundingClientRect();
        if (rect.left === 0 && rect.top === 0) rect = edRef.current!.getBoundingClientRect();
        setMenu({ x: rect.left, y: rect.bottom + 4, range });
        setActive(0);
      }
    } else {
      setMenu(null);
    }
    emit();
  }

  function pick(ref: LabeledMediaRef) {
    const m = menu;
    setMenu(null);
    if (!m) return;
    const ed = edRef.current!;
    ed.focus();
    // 删掉刚输入的 '@'，插入 chip + 尾随空格。
    const range = m.range;
    if (range.startOffset > 0) range.setStart(range.startContainer, range.startOffset - 1);
    range.deleteContents();
    const chip = buildChip(ref, thumbOf(ref.url));
    range.insertNode(chip);
    const space = document.createTextNode(' ');
    chip.after(space);
    const sel = window.getSelection();
    const after = document.createRange();
    after.setStartAfter(space);
    after.collapse(true);
    sel?.removeAllRanges();
    sel?.addRange(after);
    emit();
  }

  // 粘贴时剥掉格式：Word/网页复制会往剪贴板写带内联样式的 HTML，contentEditable
  // 默认粘贴会把这坨样式节点塞进 DOM，跟 chip 布局冲突、暗色下还看不清。只取
  // text/plain 以纯文本插入（与 Agent 输入框 MentionInput 同款做法）。execCommand
  // 会触发 input 事件，emit() 随 handleInput 一并同步 canonical 值。
  function handlePaste(e: React.ClipboardEvent) {
    e.preventDefault();
    const text = e.clipboardData.getData('text/plain');
    if (!text) return;
    document.execCommand('insertText', false, text);
  }

  // 回车自己做标准行块分裂，不交给 Chrome 默认行为：光标贴着行首 chip/零宽锚点
  // 时，Chrome 的 insertParagraph 不做块分裂、而是把 <br> 塞进块内部——块内前导
  // <br> 渲染并解析出一个凭空的空行（实机 DOM 取证：<div><br>{chip}…</div>）。
  // 手动分裂后回车语义确定：光标前内容留原行、光标后内容进新行，一次一行。
  function splitLineAtCaret(): boolean {
    const ed = edRef.current;
    const sel = window.getSelection();
    if (!ed || !sel || sel.rangeCount === 0) return false;
    const range = sel.getRangeAt(0);
    if (!ed.contains(range.startContainer)) return false;
    range.deleteContents(); // 有选区时回车 = 先删选中内容再断行（浏览器同语义）
    // 找光标所在的顶层行块（重建后每行都是 ed 的直接子 <div>）
    let block: HTMLElement | null = null;
    for (let n: Node | null = range.startContainer; n && n !== ed; n = n.parentNode) {
      if (n.parentNode === ed && n.nodeType === Node.ELEMENT_NODE && (n as HTMLElement).tagName === 'DIV') {
        block = n as HTMLElement;
        break;
      }
    }
    if (!block) return false; // 裸文本起步态等非块结构：退回浏览器默认
    const rest = document.createRange();
    rest.setStart(range.startContainer, range.startOffset);
    rest.setEndAfter(block.lastChild ?? block);
    const newDiv = document.createElement('div');
    newDiv.appendChild(rest.extractContents());
    block.after(newDiv);
    // 空块要有 <br> 撑行高，否则光标进不去/行高塌陷（Chrome 空行的原生表示法）。
    if (!block.hasChildNodes()) block.appendChild(document.createElement('br'));
    if (!newDiv.hasChildNodes()) newDiv.appendChild(document.createElement('br'));
    const caret = document.createRange();
    caret.setStart(newDiv, 0);
    caret.collapse(true);
    sel.removeAllRanges();
    sel.addRange(caret);
    emit();
    return true;
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!menu && e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      if (splitLineAtCaret()) e.preventDefault();
      return;
    }
    if (!menu) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      setMenu(null);
      return;
    }
    if (!menuRefs.length) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (i + 1) % menuRefs.length);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (i - 1 + menuRefs.length) % menuRefs.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      pick(menuRefs[active]);
    }
  }

  return (
    <div className="relative">
      <div
        ref={edRef}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        aria-label="生成提示词"
        className={`cv-mention-editor custom-scrollbar ${className ?? ''}`}
        style={{
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          overflowY: 'auto',
          // 只缩进整块的首行；回车产生的后续行（浏览器包成 div 子块）由
          // canvas.css 的 `.cv-mention-editor > div { text-indent: 0 }` 归零。
          textIndent: firstLineIndent > 0 ? firstLineIndent : undefined,
        }}
        onInput={handleInput}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        onBlur={() => { emit(); onBlur?.(); }}
        onMouseDown={(e) => e.stopPropagation()}
      />
      {empty && placeholder && (
        <div
          className="cv-mention-placeholder"
          style={firstLineIndent > 0 ? { textIndent: firstLineIndent } : undefined}
        >
          {placeholder}
        </div>
      )}

      {menu &&
        createPortal(
          <>
            <div className="cv-mention-menu-mask" onMouseDown={() => setMenu(null)} />
            <ul className="cv-mention-menu" style={{ left: menu.x, top: menu.y }}>
            {menuRefs.map((r, idx) => (
              <li
                key={`${r.kind}:${r.nodeId}`}
                className={`cv-mention-menu-item${idx === active ? ' cv-mention-menu-item-active' : ''}`}
                onMouseEnter={() => setActive(idx)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  pick(r);
                }}
              >
                {r.kind === 'image' ? (
                  <ThumbImg src={r.url} alt={r.label} className="cv-mention-opt-img" />
                ) : (
                  <span className="cv-mention-opt-ic">{r.kind === 'video' ? '▶' : '♪'}</span>
                )}
                <span className="cv-mention-opt-text">{r.label}</span>
              </li>
            ))}
            </ul>
          </>,
          document.body,
        )}
    </div>
  );
}
