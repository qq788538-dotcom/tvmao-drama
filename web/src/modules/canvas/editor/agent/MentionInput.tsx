// MentionInput——一个等价于 textarea 的 contenteditable，`@` mention 渲染成
// 原子 chip span，用户没法只删掉一部分。在 chip 右边缘按退格会删掉整个 chip
//（这是 contenteditable 父元素里 contenteditable=false 内联元素的浏览器原生行为）。
//
// 每个 chip 带一个小小的内联缩略图 + 名字，让用户能直观确认引用的素材；hover
// 时会在 chip 上方/下方弹出更大的预览。视频预览只显示首帧——永远不会自动播放。
//
// 传输上规范的文本格式仍然是 `@[name](artifact_id)` token，所以调用方
//（extractMentions、agent payload）不用改——读取时我们把 chip 序列化回 token。

import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';

export interface MentionPick {
  artifact_id: string;
  name: string;
  /** 可选 URL，用于渲染 chip 缩略图 + hover 预览。 */
  url?: string;
  /** 'image' / 'video' 显示媒体缩略图。'audio' 显示一个喇叭图标，
   *  hover 时弹出一个小的 <audio controls>。'file' 是文本附件
   *  （txt/md）；只显示文件名 + 'T' 图标，内容通过 `text` 挂在 chip 上，
   *  发送时内联进去。'text' 是画布文字节点的引用——跟 'file' 同一套
   *  「无 URL、正文内联」机制，区别只在发送时正文从画布现读（节点还在
   *  编辑，chip 上的快照可能已过期）。 */
  kind?: 'image' | 'video' | 'audio' | 'file' | 'text';
  /** kind='file'/'text' 且正文超过内联阈值时的全文字数（正文不挂 chip，
   *  发送时走 read_attachment 通道）。 */
  charCount?: number;
  /** kind='file'/'text' chip 的内联文本内容。 */
  text?: string;
}

export interface RichMention extends MentionPick {}

export interface MentionInputHandle {
  /** 读取当前规范值（带 `@[name](id)` token）。 */
  getValue: () => string;
  /** 读取所有 chip 的完整数据（url / kind / text）——发送流程用它来
   *  构建多模态 mention payload + 内联文件内容。 */
  getMentions: () => RichMention[];
  /** 完全替换内容（例如发送后清空）。 */
  setValue: (next: string) => void;
  /** 弹掉光标处的部分 `@query` 并在原位插入一个 chip。如果编辑器当前没有
   *  聚焦，chip 会插入到上次已知的光标位置（在 blur 时保存）——给 + 按钮的
   *  上传流程用，这样附件不会总是落到末尾。 */
  insertMention: (m: MentionPick) => void;
  /** 聚焦编辑器。 */
  focus: () => void;
}

export interface MentionInputProps {
  /** 规范值或当前活跃的 `@query` 变化时触发。 */
  onChange: (value: string, active: ActiveMention | null) => void;
  /** 回车（不按 shift、不在输入法 composing 中）——通常调用 send()。 */
  /** 粘贴进输入框的图片文件——通常是附件处理器。 */
  onPasteImage?: (file: File) => void;
  placeholder?: string;
  className?: string;
}

export interface ActiveMention {
  /** `@` 之后的部分文本，用来过滤 picker。 */
  query: string;
}

const TOKEN_RE = /@\[([^\]]+)\]\(([^)]+)\)/g;

export const MentionInput = forwardRef<MentionInputHandle, MentionInputProps>(function MentionInput(
  { onChange, onPasteImage, placeholder, className },
  ref,
) {
  const rootRef = useRef<HTMLDivElement>(null);
  // 记住焦点离开编辑器时（例如用户点了上传按钮）的光标位置。insertMention 下次
  // 调用时会还原它，这样附件落在用户当时输入的位置，而不是末尾。
  const savedRangeRef = useRef<Range | null>(null);
  // Hover 预览状态——用固定的视口坐标，让弹窗能逃出 overflow:hidden 的祖先元素。
  const [hover, setHover] = useState<{
    url: string;
    kind: 'image' | 'video';
    left: number;
    top: number;
  } | null>(null);

  // 遍历 DOM 子节点，产出规范的 token 形式字符串。
  const serialize = (root: HTMLElement): string => {
    let out = '';
    const walk = (n: Node) => {
      if (n.nodeType === Node.TEXT_NODE) {
        out += n.textContent ?? '';
        return;
      }
      if (n instanceof HTMLElement) {
        if (n.dataset.mentionId) {
          const name = n.dataset.mentionName ?? '';
          out += `@[${name}](${n.dataset.mentionId})`;
          return;
        }
        if (n.tagName === 'BR') {
          out += '\n';
          return;
        }
        n.childNodes.forEach(walk);
      }
    };
    root.childNodes.forEach(walk);
    return out;
  };

  // 在同一个 text node 里从光标往回走，直到最近的 `@`（遇到空白则表示没有活跃
  // mention），以此检测「用户正在输入 mention 的过程中」。
  const computeActive = (root: HTMLElement): ActiveMention | null => {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return null;
    const r = sel.getRangeAt(0);
    if (!r.collapsed) return null;
    if (!root.contains(r.startContainer)) return null;
    const node = r.startContainer;
    if (node.nodeType !== Node.TEXT_NODE) return null;
    const text = (node.textContent ?? '').slice(0, r.startOffset);
    for (let i = text.length - 1; i >= 0; i--) {
      const c = text[i];
      if (c === '@') return { query: text.slice(i + 1) };
      if (c === ' ' || c === '\n' || c === '\t') return null;
    }
    return null;
  };

  const emit = () => {
    if (!rootRef.current) return;
    const value = serialize(rootRef.current);
    const active = computeActive(rootRef.current);
    onChange(value, active);
  };

  // 为一个 mention 构建 chip span。contenteditable=false 让它变成原子的。
  const makeChipNode = (m: MentionPick): HTMLSpanElement => {
    const span = document.createElement('span');
    span.dataset.mentionId = m.artifact_id;
    span.dataset.mentionName = m.name;
    if (m.url) span.dataset.mentionUrl = m.url;
    if (m.kind) span.dataset.mentionKind = m.kind;
    span.contentEditable = 'false';
    span.className =
      'inline-flex items-center gap-1 px-1.5 py-0.5 mx-0.5 rounded text-[11px] bg-blue-600/20 text-blue-200 border border-blue-500/40 select-none align-middle';

    // 小预览。video 用 preload="metadata"，这样不播放就能显示首帧；
    // file 类型则换成一个小的文本图标块。
    if (m.kind === 'file' || m.kind === 'text') {
      // 'text'（画布文字节点）复用 'file' 的图标块——两者都是「无 URL、正文
      // 内联」的 chip，视觉上没必要造第二种形态。
      const icon = document.createElement('span');
      icon.className =
        'w-4 h-4 inline-flex items-center justify-center rounded bg-blue-500/30 text-[9px] font-mono pointer-events-none';
      icon.textContent = 'T';
      span.appendChild(icon);
      if (m.text) span.dataset.mentionText = m.text;
      if (m.charCount != null) span.dataset.mentionCharCount = String(m.charCount);
    } else if (m.kind === 'audio') {
      const icon = document.createElement('span');
      icon.className =
        'w-4 h-4 inline-flex items-center justify-center rounded bg-blue-500/30 text-[10px] pointer-events-none';
      icon.textContent = '♪';
      span.appendChild(icon);
    } else if (m.url) {
      if (m.kind === 'video') {
        const v = document.createElement('video');
        v.src = m.url;
        v.muted = true;
        v.preload = 'metadata';
        v.playsInline = true;
        v.className = 'w-4 h-4 rounded object-cover bg-black/40 pointer-events-none';
        span.appendChild(v);
      } else {
        const img = document.createElement('img');
        img.src = m.url;
        img.alt = '';
        img.className = 'w-4 h-4 rounded object-cover bg-black/40 pointer-events-none';
        span.appendChild(img);
      }
    }

    const label = document.createTextNode(`@${m.name}`);
    span.appendChild(label);
    return span;
  };

  // 解析规范 token 字符串并重建 DOM 子节点。给 setValue 用（例如发送后清空）。
  // 实时输入不会调它——我们绝不在编辑过程中重渲染 contentEditable，那样会丢光标。
  const renderInto = (root: HTMLElement, value: string) => {
    root.innerHTML = '';
    let last = 0;
    TOKEN_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = TOKEN_RE.exec(value))) {
      if (m.index > last) {
        root.appendChild(document.createTextNode(value.slice(last, m.index)));
      }
      root.appendChild(makeChipNode({ name: m[1], artifact_id: m[2] }));
      last = m.index + m[0].length;
    }
    if (last < value.length) {
      root.appendChild(document.createTextNode(value.slice(last)));
    }
  };

  useImperativeHandle(ref, () => ({
    getValue: () => (rootRef.current ? serialize(rootRef.current) : ''),
    getMentions: () => {
      const root = rootRef.current;
      if (!root) return [];
      const out: RichMention[] = [];
      root.querySelectorAll<HTMLElement>('[data-mention-id]').forEach((el) => {
        out.push({
          artifact_id: el.dataset.mentionId ?? '',
          name: el.dataset.mentionName ?? '',
          url: el.dataset.mentionUrl,
          kind: (el.dataset.mentionKind as MentionPick['kind']) ?? undefined,
          text: el.dataset.mentionText,
          charCount: el.dataset.mentionCharCount != null ? Number(el.dataset.mentionCharCount) : undefined,
        });
      });
      return out;
    },
    setValue: (next: string) => {
      if (!rootRef.current) return;
      renderInto(rootRef.current, next);
      emit();
    },
    insertMention: (mention) => {
      const root = rootRef.current;
      if (!root) return;
      let sel = window.getSelection();
      // 如果焦点已经离开编辑器（用户点了上传按钮等），把光标还原到保存的
      // range，这样 chip 落在用户当时输入的位置，而不是末尾。
      const currentRangeInRoot =
        sel && sel.rangeCount > 0 && root.contains(sel.getRangeAt(0).startContainer);
      if (!currentRangeInRoot && savedRangeRef.current) {
        root.focus();
        sel = window.getSelection();
        if (sel) {
          sel.removeAllRanges();
          sel.addRange(savedRangeRef.current);
        }
      }
      if (!sel || sel.rangeCount === 0) {
        // 仍然没有光标——追加到末尾。
        root.appendChild(makeChipNode(mention));
        root.appendChild(document.createTextNode(' '));
        emit();
        return;
      }
      const range = sel.getRangeAt(0);
      // 擦掉当前 text node 里光标之前的 `@query` 子串，然后在该位置插入
      // chip + 一个末尾空格。
      const node = range.startContainer;
      if (node.nodeType === Node.TEXT_NODE) {
        const text = node.textContent ?? '';
        const before = text.slice(0, range.startOffset);
        const after = text.slice(range.startOffset);
        const atIdx = before.lastIndexOf('@');
        if (atIdx >= 0) {
          node.textContent = before.slice(0, atIdx) + after;
          // 把光标定位到 @ 原来所在的位置。
          const newRange = document.createRange();
          newRange.setStart(node, atIdx);
          newRange.collapse(true);
          sel.removeAllRanges();
          sel.addRange(newRange);
        }
      }
      // 在当前光标处插入 chip + 末尾空格。
      const chip = makeChipNode(mention);
      const trailing = document.createTextNode(' ');
      const r2 = sel.rangeCount > 0 ? sel.getRangeAt(0) : null;
      if (r2) {
        r2.insertNode(trailing);
        r2.insertNode(chip);
        // 光标放在末尾空格之后。
        const after = document.createRange();
        after.setStartAfter(trailing);
        after.collapse(true);
        sel.removeAllRanges();
        sel.addRange(after);
      } else {
        root.appendChild(chip);
        root.appendChild(trailing);
      }
      root.focus();
      emit();
    },
    focus: () => rootRef.current?.focus(),
  }));

  // 通过 `data-empty` 属性显示 / 隐藏一个纯 CSS 的 placeholder，
  // 由一条同级规则读取（在 globals 里定义）。
  useEffect(() => {
    if (!rootRef.current) return;
    const el = rootRef.current;
    const update = () => {
      el.dataset.empty = el.textContent?.length === 0 && el.children.length === 0 ? 'true' : 'false';
    };
    update();
    const obs = new MutationObserver(update);
    obs.observe(el, { childList: true, subtree: true, characterData: true });
    return () => obs.disconnect();
  }, []);

  // chip 的委托式 hover 预览。当指针进入一个带 data-mention-url 的 chip 时，
  // 在它上方弹出更大的缩略图。audio chip 没有 hover 弹窗——按设计它们从不自动预览。
  const onChipOver = (e: React.MouseEvent) => {
    const t = e.target as HTMLElement;
    const chip = t.closest('[data-mention-url]') as HTMLElement | null;
    if (!chip) {
      setHover(null);
      return;
    }
    const rawKind = chip.dataset.mentionKind;
    if (rawKind === 'audio') {
      setHover(null);
      return;
    }
    const url = chip.dataset.mentionUrl ?? '';
    const kind: 'image' | 'video' = rawKind === 'video' ? 'video' : 'image';
    const rect = chip.getBoundingClientRect();
    setHover({ url, kind, left: rect.left, top: rect.top });
  };
  const onChipOut = (e: React.MouseEvent) => {
    const next = e.relatedTarget as HTMLElement | null;
    if (next && (next.closest?.('[data-mention-url]') || next.closest?.('.mention-hover-preview'))) {
      return;
    }
    setHover(null);
  };

  return (
    <>
      <div
        ref={rootRef}
        contentEditable
        role="textbox"
        aria-multiline="true"
        data-placeholder={placeholder ?? ''}
        onInput={emit}
        onMouseOver={onChipOver}
        onMouseOut={onChipOut}
        onBlur={() => {
          const sel = window.getSelection();
          if (sel && sel.rangeCount > 0) {
            const r = sel.getRangeAt(0);
            if (rootRef.current?.contains(r.startContainer)) {
              savedRangeRef.current = r.cloneRange();
            }
          }
        }}
        onKeyDown={(e) => {
          // 回车不发送：聊天里回车想换行的概率远大于想发送，
          // 误发一条就白跑一轮 agent。发送只走右下角按钮。
          // 这里不能直接放行默认行为：Chrome 对纯 Enter 会把后续内容包进 <div> 块，
          // 而 serialize 只对 <br> 出 '\n'，块边界会被拼成一行——所以统一
          // preventDefault 后手动插换行（与 Shift+Enter 的原生 <br> 行为一致，
          // 根元素是 whitespace-pre-wrap，'\n' 能正常显示）。
          if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
            e.preventDefault();
            document.execCommand('insertLineBreak');
          }
        }}
        onPaste={(e) => {
          const imgs = Array.from(e.clipboardData.items)
            .filter((it) => it.type.startsWith('image/'))
            .map((it) => it.getAsFile())
            .filter(Boolean) as File[];
          if (imgs.length > 0) {
            e.preventDefault();
            if (onPasteImage) imgs.forEach(onPasteImage);
            return;
          }
          // 文本粘贴时剥掉格式，避免用户拖进带样式的 HTML，那会跟我们的
          // chip 布局冲突。
          e.preventDefault();
          const text = e.clipboardData.getData('text/plain');
          if (!text) return;
          document.execCommand('insertText', false, text);
        }}
        className={`mention-input flex-1 resize-none bg-background border border-input focus:border-blue-500 rounded p-2 text-xs text-foreground outline-none placeholder:text-muted-foreground max-h-32 overflow-y-auto min-h-[44px] whitespace-pre-wrap break-words ${className ?? ''}`}
        suppressContentEditableWarning
      />
      {hover &&
        createPortal(
          <div
            className="mention-hover-preview fixed z-[10000] bg-card border border-input rounded-md shadow-2xl p-1 pointer-events-none"
            style={{
              left: Math.max(8, hover.left),
              top: Math.max(8, hover.top - 200 - 8),
              maxWidth: 256,
            }}
          >
            {hover.kind === 'video' ? (
              <video
                src={hover.url} crossOrigin={mediaCrossOrigin(hover.url)}
                muted
                preload="metadata"
                playsInline
                className="block max-w-[240px] max-h-[200px] rounded bg-black"
              />
            ) : (
              <img
                src={hover.url}
                crossOrigin={mediaCrossOrigin(hover.url)}
                alt=""
                className="block max-w-[240px] max-h-[200px] rounded object-contain bg-black"
              />
            )}
          </div>,
          document.body,
        )}
    </>
  );
});
