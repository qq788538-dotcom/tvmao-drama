// ScriptPromptEditor — 脚本分镜提示词编辑器，@ 引用步骤 2 资产。
// 复制适配自 ../../PromptMentionEditor.tsx（AI 生图/生视频节点的 @ 编辑器）：
// contentEditable 非受控 + chip span + canonical 字符串的机制原样保留，只把
// 引用语义从「画布节点 @[图片:<nodeId>]」换成 skill 语义「资产 @[角色-名称]」。
// 两处刻意不同：
//   - chip label 显示资产名本身（张三），不是「图片 N」序号——名称就是外键；
//   - 断链 token（资产表里没有这个名字）不剔除，渲染成红色 chip 提醒用户处理，
//     与分镜表的断链红点同一语义（悄悄删用户的 token 比可见的断链更吓人）。
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ThumbImg } from '../../ThumbImg';
import { useThumbUrls } from '../../thumbs';
import { LABEL_BY_KIND, SCRIPT_TOKEN_RE, type TokenKind } from './scriptTokens';

const KIND_BY_LABEL: Record<string, TokenKind> = { 角色: 'character', 场景: 'scene', 物品: 'prop' };
// 无图资产的 chip 占位符：类型首字。
const ICON_BY_KIND: Record<TokenKind, string> = { character: '角', scene: '景', prop: '物' };

// 编辑器消费的资产引用：url = linkedNode 的原图（缩略图由 thumbs 层换取）。
export interface ScriptAssetOption {
  kind: TokenKind;
  name: string;
  url?: string;
}

type Seg = { type: 'text'; text: string } | { type: 'token'; kind: TokenKind; name: string };

export function parseScriptPrompt(value: string): Seg[] {
  const out: Seg[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  SCRIPT_TOKEN_RE.lastIndex = 0;
  while ((m = SCRIPT_TOKEN_RE.exec(value))) {
    if (m.index > last) out.push({ type: 'text', text: value.slice(last, m.index) });
    out.push({ type: 'token', kind: KIND_BY_LABEL[m[1]], name: m[2] });
    last = m.index + m[0].length;
  }
  if (last < value.length) out.push({ type: 'text', text: value.slice(last) });
  return out;
}

function buildChip(kind: TokenKind, name: string, opt?: ScriptAssetOption, thumbUrl?: string): HTMLSpanElement {
  const span = document.createElement('span');
  span.contentEditable = 'false';
  span.dataset.mention = '1';
  span.dataset.kind = kind;
  span.dataset.name = name;
  span.className = 'cv-mention-chip';
  if (!opt) span.dataset.broken = '1'; // 断链：资产表没有这个名字
  if (opt?.url) {
    const im = document.createElement('img');
    if (thumbUrl) im.src = thumbUrl;
    im.dataset.rawUrl = opt.url;
    im.alt = name;
    im.className = 'cv-mention-chip-img';
    span.appendChild(im);
  } else {
    const ic = document.createElement('span');
    ic.className = 'cv-mention-chip-ic';
    ic.textContent = ICON_BY_KIND[kind];
    span.appendChild(ic);
  }
  const t = document.createElement('span');
  t.className = 'cv-mention-chip-text';
  t.textContent = name;
  span.appendChild(t);
  return span;
}

function buildFragment(value: string, options: ScriptAssetOption[], thumbOf: (url: string) => string | undefined): DocumentFragment {
  const fragment = document.createDocumentFragment();
  for (const seg of parseScriptPrompt(value)) {
    if (seg.type === 'text') {
      fragment.appendChild(document.createTextNode(seg.text));
      continue;
    }
    const opt = options.find((o) => o.kind === seg.kind && o.name === seg.name);
    fragment.appendChild(buildChip(seg.kind, seg.name, opt, opt?.url ? thumbOf(opt.url) : undefined));
  }
  return fragment;
}

// DOM → canonical。逐行建模的理由与实现同 PromptMentionEditor.domToString。
const BLOCK_TAGS = new Set(['DIV', 'P']);

function linesOf(nodes: Node[]): string[] {
  const lines: string[] = [];
  const cur = () => {
    if (lines.length === 0) lines.push('');
    return lines.length - 1;
  };
  const append = (s: string) => {
    lines[cur()] += s;
  };
  nodes.forEach((node, i) => {
    if (node.nodeType === Node.TEXT_NODE) {
      append(node.textContent || '');
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const el = node as HTMLElement;
    if (el.dataset.mention) {
      append(`@[${LABEL_BY_KIND[(el.dataset.kind as TokenKind) || 'character']}-${el.dataset.name}]`);
      return;
    }
    if (el.tagName === 'BR') {
      cur();
      if (i < nodes.length - 1) lines.push('');
      return;
    }
    const inner = linesOf(Array.from(el.childNodes));
    if (BLOCK_TAGS.has(el.tagName)) {
      lines.push(...(inner.length ? inner : ['']));
      return;
    }
    if (inner.length) {
      append(inner[0]);
      lines.push(...inner.slice(1));
    }
  });
  return lines;
}

export function scriptDomToString(root: HTMLElement): string {
  return linesOf(Array.from(root.childNodes)).join('\n');
}

const KIND_RANK: Record<TokenKind, number> = { character: 0, scene: 1, prop: 2 };

export function ScriptPromptEditor({
  value,
  options,
  placeholder,
  readOnly = false,
  onChange,
  onBlur,
  className,
}: {
  value: string;
  /** 步骤 2 资产选项（角色/场景/物品拉平），@ 菜单与 chip 渲染共用 */
  options: ScriptAssetOption[];
  placeholder?: string;
  readOnly?: boolean;
  onChange: (v: string) => void;
  onBlur?: () => void;
  className?: string;
}) {
  const edRef = useRef<HTMLDivElement>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; range: Range } | null>(null);
  const [active, setActive] = useState(0);
  const [empty, setEmpty] = useState(true);
  const menuOptions = [...options].sort((a, b) => KIND_RANK[a.kind] - KIND_RANK[b.kind]);
  const optionsFingerprint = options.map((o) => `${o.kind}:${o.name}:${o.url ?? ''}`).join('|');
  const thumbMap = useThumbUrls(options.filter((o) => o.url).map((o) => o.url!));
  const thumbOf = (url: string) => thumbMap[url];

  function rebuild() {
    const ed = edRef.current;
    if (!ed) return;
    ed.innerHTML = '';
    ed.appendChild(buildFragment(value, options, thumbOf));
    setEmpty(ed.textContent?.trim() === '' && !ed.querySelector('[data-mention]'));
  }

  useEffect(() => {
    const ed = edRef.current;
    if (!ed) return;
    if (scriptDomToString(ed) !== value) rebuild();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  useEffect(() => {
    rebuild();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [optionsFingerprint]);

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
    onChange(scriptDomToString(ed));
    setEmpty(ed.textContent?.trim() === '' && !ed.querySelector('[data-mention]'));
  }

  function handleInput(e: React.FormEvent<HTMLDivElement>) {
    const ne = e.nativeEvent as InputEvent;
    const typedAt = ne.data === '@' && (ne.inputType === 'insertText' || ne.inputType === 'insertCompositionText');
    if (typedAt && options.length > 0) {
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

  function pick(opt: ScriptAssetOption) {
    const m = menu;
    setMenu(null);
    if (!m) return;
    const ed = edRef.current!;
    ed.focus();
    const range = m.range;
    if (range.startOffset > 0) range.setStart(range.startContainer, range.startOffset - 1);
    range.deleteContents();
    const chip = buildChip(opt.kind, opt.name, opt, opt.url ? thumbOf(opt.url) : undefined);
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

  function handlePaste(e: React.ClipboardEvent) {
    e.preventDefault();
    const text = e.clipboardData.getData('text/plain');
    if (!text) return;
    document.execCommand('insertText', false, text);
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (!menu) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      setMenu(null);
      return;
    }
    if (!menuOptions.length) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((i) => (i + 1) % menuOptions.length);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((i) => (i - 1 + menuOptions.length) % menuOptions.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      pick(menuOptions[active]);
    }
  }

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={edRef}
        contentEditable={!readOnly}
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        aria-label="分镜提示词"
        data-testid="prompt-editor-textarea"
        className={`cv-mention-editor custom-scrollbar ${className ?? ''}`}
        style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', overflowY: 'auto' }}
        onInput={handleInput}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        onBlur={() => { emit(); onBlur?.(); }}
        onMouseDown={(e) => e.stopPropagation()}
      />
      {empty && placeholder && <div className="cv-mention-placeholder">{placeholder}</div>}

      {menu &&
        createPortal(
          <>
            <div className="cv-mention-menu-mask" onMouseDown={() => setMenu(null)} />
            <ul className="cv-mention-menu" style={{ left: menu.x, top: menu.y, zIndex: 2100 }}>
              {menuOptions.map((o, idx) => (
                <li
                  key={`${o.kind}:${o.name}`}
                  className={`cv-mention-menu-item${idx === active ? ' cv-mention-menu-item-active' : ''}`}
                  onMouseEnter={() => setActive(idx)}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    pick(o);
                  }}
                >
                  {o.url ? (
                    <ThumbImg src={o.url} alt={o.name} className="cv-mention-opt-img" />
                  ) : (
                    <span className="cv-mention-opt-ic">{ICON_BY_KIND[o.kind]}</span>
                  )}
                  <span className="cv-mention-opt-text">{LABEL_BY_KIND[o.kind]}-{o.name}</span>
                </li>
              ))}
            </ul>
          </>,
          document.body,
        )}
    </div>
  );
}

// 只读 inline 渲染：分镜表「提示词」列用。同一套 chip 视觉（cv-mention-chip +
// data-kind 配色 + 断链红），但纯 JSX 展示不进 contentEditable——表格单元格
// 只需要看，点开大编辑器才编辑。
export function ScriptPromptInline({
  prompt,
  options,
}: {
  prompt: string;
  options: ScriptAssetOption[];
}) {
  const segs = parseScriptPrompt(prompt);
  return (
    <>
      {segs.map((seg, i) => {
        if (seg.type === 'text') return <span key={i}>{seg.text}</span>;
        const opt = options.find((o) => o.kind === seg.kind && o.name === seg.name);
        return (
          <span
            key={i}
            data-mention="1"
            data-kind={seg.kind}
            {...(opt ? {} : { 'data-broken': '1' })}
            className="cv-mention-chip"
          >
            {opt?.url ? (
              <ThumbImg src={opt.url} alt={seg.name} className="cv-mention-chip-img" />
            ) : (
              <span className="cv-mention-chip-ic">{ICON_BY_KIND[seg.kind]}</span>
            )}
            <span className="cv-mention-chip-text">{seg.name}</span>
          </span>
        );
      })}
    </>
  );
}
