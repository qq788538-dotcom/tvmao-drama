// apps/web/src/canvas/agent/MessageBubble.tsx
//
// 气泡外壳视觉沿用参考仓 ChatPanel 的 MessageBubble（桌面端未迁移 ChatPanel，spec X7）
//（rounded-2xl、用户 justify-end / assistant justify-start、右下角 hover
// 才显示的复制按钮）。block 渲染则是 agent 专属——遍历 msg.blocks 渲染
// text / tool_use / tool_event（artifact 缩略图）/ tool_result，再加上下方的
// WorkflowProgress 折叠面板。

import { useMemo, useState } from 'react';
import { Check, Copy, FileText, Link2, Pencil, Plus, Trash2, Unlink, Zap, type LucideIcon } from 'lucide-react';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

import type { AgentMessage } from './agentStore';
import type { Artifact } from './types';
import { parseCanvasOpPayload } from './canvasOps';
import { useCanvasStore } from '../store';
import { useThumbUrls } from '../thumbs';
import { WorkflowProgress } from './WorkflowProgress';
import { mediaCrossOrigin } from '../utils/mediaCrossOrigin';
import { sectionPreview, splitBubbleText, type InlineSection } from './chatInlineSections';
import { ChatTextSectionDialog } from './ChatTextSectionDialog';
import { TextExpandDialog } from '../nodes/text/TextExpandDialog';

marked.setOptions({ breaks: true, gfm: true });

function renderMarkdown(src: string): string {
  // DOMPurify 会剥掉危险的标签/属性；之后再 dangerouslySetInnerHTML 是安全的。
  // 禁掉 <del>/<s>/<strike>——marked+GFM 有时会误把中文标点密集的文本整段裹进
  // <del>（整个回复从头到尾被裹住）。agent 回复里正经的删除线极少，所以剥掉很安全。
  let html = marked.parse(src, { async: false }) as string;
  // 裸图片 URL 会被自动转成 <a>——把指向图片文件的 anchor 换成内联 <img>，
  // 这样聊天里直接显示图片，而不是一条长长的签名 URL 链接。
  // 原图 URL 放进 data-original、先不给 src：展示走缩略图（MarkdownBlock 换取
  // 到之后回填 src），点击放大从 data-original 取原图。直接给 src 会先下载
  // 整张原图，缩略图就白做了。
  html = html.replace(
    /<a [^>]*href="([^"]+\.(?:png|jpe?g|webp|gif)(?:\?[^"]*)?)"[^>]*>.*?<\/a>/gi,
    '<img data-original="$1" alt="" />',
  );
  // 视频 URL 同样处理。样式跟图片对齐：只显示从 metadata 取的首帧（不带控件），
  // 加上 `data-video`，这样点击委托会打开 lightbox，而不是让用户去摆弄原生播放器。
  html = html.replace(
    /<a [^>]*href="([^"]+\.(?:mp4|webm|mov)(?:\?[^"]*)?)"[^>]*>.*?<\/a>/gi,
    '<video src="$1" data-video="1" preload="metadata" muted playsinline></video>',
  );
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ['del', 's', 'strike'],
    ADD_TAGS: ['video'],
    ADD_ATTR: ['preload', 'muted', 'playsinline', 'data-video', 'data-original'],
  });
}

// marked 输出的属性值是 HTML 实体编码的（签名 URL 里的 & 变成 &amp;），
// 换取缩略图前要还原成真实 URL；回填 src 时再编码回去。
function decodeAttr(s: string): string {
  return s.replace(/&amp;/g, '&').replace(/&quot;/g, '"').replace(/&#39;/g, "'");
}

function encodeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;');
}

const IMG_ORIGINAL_RE = /<img data-original="([^"]+)"/g;

// 没有对象存储 URL、但必须渲染成 chip 的 artifact 类型——hasVisible 的 url
// 过滤和 tool_event 渲染共用这一份清单（分开维护的话新类型很容易只改一处，
// 消息就会被 url 过滤整条吞掉）。前三个是「落新节点」类产物，后五个是画布
// 操作指令（canvasOps 执行）。export 仅为测试。
export const NO_URL_CHIP_TYPES: ReadonlySet<string> = new Set([
  'text_info',
  'gen_image_node_info',
  'gen_video_node_info',
  'node_create_info',
  'node_update_info',
  'node_delete_info',
  'connection_create_info',
  'connection_delete_info',
]);

// chip 的展示文案 / 图标 / 定位方式。定位分两种：
//   - byArtifact：节点 id 是前端落节点时才生成的，只能按 settings.agentMeta
//     .artifactId 反查（text_info / gen_*_node_info / node_create_info）；
//   - byNodeId：payload 里带着现成节点 id（node_update_info）。
// 不给 locate 的是静态说明 chip——删除/断线的对象已经不在画布上，连线创建
// 的「产物」是一条边而非节点，都没有可定位的目标。
/**
 * 节点在 chip 上的显示名：有 settings.name 就用它，否则退回 id。
 *
 * 后端 emit 画布操作时 name 字段填的就是节点 id——它读的是服务端快照，那里
 * 也有名字，但把「显示什么」定在后端就意味着改名之后历史 chip 全是旧名。
 * 显示层查画布是唯一能反映当前状态的位置。内部标识始终是 id：定位、去重、
 * 存在性检查一律不碰这个函数。
 *
 * 用 getState() 而不是订阅：nodes 数组在画布操作时高频换引用，订阅它会让每条
 * 聊天消息（含 markdown 渲染）跟着重渲染。代价是节点改名后已渲染的 chip 不会
 * 立刻刷新——chip 是历史记录，这点滞后不影响识别。
 *
 * 查不到节点时自然退回 id，删除类 chip 因此不需要特判：目标已经不在画布上。
 */
function nodeDisplayName(nodeId: string, frozen?: unknown): string {
  if (!nodeId) return '';
  // 链条固定为：画布现查（最新名）→ 执行时定格名（payload node_name/from_name/
  // to_name，节点已删或已改名时的兜底）→ id（存量老消息没有定格名）。所有操作
  // chip 分支统一走这里，新增操作类型自动继承「有名显示名，没名才显示 id」。
  const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
  const name = (node?.settings as { name?: unknown } | undefined)?.name;
  if (typeof name === 'string' && name.trim() !== '') return name.trim();
  if (typeof frozen === 'string' && frozen.trim() !== '') return frozen.trim();
  return nodeId;
}

function chipDescriptor(a: Artifact): {
  label: string;
  Icon: LucideIcon;
  locate?: { kind: 'byArtifact' } | { kind: 'byNodeId'; nodeId: string };
} {
  const p = parseCanvasOpPayload(a.payload) ?? {};
  // 后端连线类 artifact 的 name 已是「from → to」形态，payload 缺失时兜底用它。
  const pair =
    typeof p.from === 'string' && typeof p.to === 'string'
      ? `${nodeDisplayName(p.from, p.from_name)} → ${nodeDisplayName(p.to, p.to_name)}`
      : a.name ?? '';
  switch (a.artifact_type) {
    case 'node_create_info':
      // 后端把 name 填成节点类型（如 gen-image）。
      return { label: a.name ? `已创建 ${a.name} 节点` : '已创建节点', Icon: Plus, locate: { kind: 'byArtifact' } };
    case 'node_update_info': {
      // 后端把 name 填成节点 id；payload.node_id 是同一个值，优先用它查显示名。
      const nodeId = typeof p.node_id === 'string' ? p.node_id : typeof a.name === 'string' ? a.name : '';
      const shown = nodeDisplayName(nodeId, p.node_name);
      return {
        label: shown ? `已修改节点 ${shown}` : '已修改节点',
        Icon: Pencil,
        locate: nodeId ? { kind: 'byNodeId', nodeId } : undefined,
      };
    }
    case 'node_delete_info': {
      const nodeId = typeof p.node_id === 'string' ? p.node_id : typeof a.name === 'string' ? a.name : '';
      const shown = nodeDisplayName(nodeId, p.node_name);
      return { label: shown ? `已删除节点 ${shown}` : '已删除节点', Icon: Trash2 };
    }
    case 'connection_create_info':
      return { label: `连线 ${pair}`.trim(), Icon: Link2 };
    case 'connection_delete_info':
      return { label: `断开 ${pair}`.trim(), Icon: Unlink };
    case 'gen_image_node_info':
      return { label: a.name || 'AI 绘图节点', Icon: FileText, locate: { kind: 'byArtifact' } };
    case 'gen_video_node_info':
      return { label: a.name || 'AI 视频节点', Icon: FileText, locate: { kind: 'byArtifact' } };
    default:
      // text_info（chip 家族的初代成员）。
      return { label: a.name || '文本', Icon: FileText, locate: { kind: 'byArtifact' } };
  }
}

// assistant 文本块单独成组件：缩略图 hook 不能进 blocks.map 循环。
// 内联图展示走缩略图（换取完成前无 src 占位），点击放大用 data-original 原图。
// export 仅为测试。
export function MarkdownBlock({ text }: { text: string }) {
  const setLightbox = useCanvasStore((s) => s.setLightbox);
  const rawHtml = useMemo(() => renderMarkdown(text), [text]);
  const imgUrls = useMemo(() => {
    const out: string[] = [];
    for (const m of rawHtml.matchAll(IMG_ORIGINAL_RE)) out.push(decodeAttr(m[1]));
    return out;
  }, [rawHtml]);
  const thumbs = useThumbUrls(imgUrls);
  const html = useMemo(
    () =>
      rawHtml.replace(IMG_ORIGINAL_RE, (full, orig: string) => {
        const thumb = thumbs[decodeAttr(orig)];
        if (!thumb) return full;
        // 在 DOMPurify 之后回填 src——值来自我方 /media/thumbs 响应，可信。
        return `<img loading="lazy" decoding="async" src="${encodeAttr(thumb)}" data-original="${orig}"`;
      }),
    [rawHtml, thumbs],
  );
  return (
    <div
      className="markdown-body"
      onClick={(e) => {
        // 点击内联图片 / 视频打开 lightbox。图片 src 是缩略图，放大必须
        // 用 data-original 里的原图。
        const t = e.target as HTMLElement;
        if (t.tagName === 'IMG') {
          const img = t as HTMLImageElement;
          const url = img.dataset.original || img.src;
          if (url) setLightbox({ url, kind: 'image', title: '' });
        } else if (t.tagName === 'VIDEO') {
          const src = (t as HTMLVideoElement).src;
          if (src) setLightbox({ url: src, kind: 'video', title: '' });
        }
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

// 用户气泡里的内联长文本卡片（文本节点引用 / txt 文件附件的折叠形态）。
// 点击：能在画布上按名字找到对应文本节点 → 打开节点自己的展开编辑弹层
// （编辑写回节点，看到的是节点当前内容而非发送时快照）；找不到（文件附件、
// 或节点已删）→ 打开 ChatTextSectionDialog 编辑快照，保存落成新文本节点。
function InlineSectionCard({ section }: { section: InlineSection }) {
  const [dialog, setDialog] = useState<null | { nodeId: string; markdown: string } | { snapshot: true }>(null);
  const preview = sectionPreview(section.content);
  const open = () => {
    if (section.kind === 'node') {
      const node = useCanvasStore
        .getState()
        .nodes.find(
          (n) =>
            n.type === 'text-node' &&
            // 没起名的文本节点，chip 名落的是节点 id——两种都认。
            ((n.settings as { name?: unknown } | undefined)?.name === section.name ||
              n.id === section.name),
        );
      if (node) {
        const text = (node.settings as { text?: unknown } | undefined)?.text;
        setDialog({ nodeId: node.id, markdown: typeof text === 'string' ? text : '' });
        return;
      }
    }
    setDialog({ snapshot: true });
  };
  return (
    <>
      <button
        type="button"
        onClick={open}
        title="点击在大窗中查看/编辑全文"
        className="mt-1 w-full text-left flex items-start gap-2 px-2.5 py-2 rounded-lg border border-white/25 bg-white/10 hover:bg-white/20 cursor-pointer transition-colors"
      >
        <FileText size={14} className="shrink-0 mt-0.5 text-white/80" />
        <span className="min-w-0">
          <span className="block text-[12px] font-medium truncate">
            {section.name}
            <span className="ml-1.5 font-normal text-white/70">{section.content.length} 字</span>
          </span>
          {preview && <span className="block text-[11px] text-white/70 truncate">{preview}</span>}
        </span>
      </button>
      {dialog && 'nodeId' in dialog && (
        <TextExpandDialog
          nodeId={dialog.nodeId}
          initialMarkdown={dialog.markdown}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog && 'snapshot' in dialog && (
        <ChatTextSectionDialog
          name={section.name}
          initialMarkdown={section.content}
          onClose={() => setDialog(null)}
        />
      )}
    </>
  );
}

// text_info 产物 chip（agent 创建文本节点后落在回复里的那颗）。点击行为对标
// 用户气泡的 InlineSectionCard（用户拍板「左右对齐」）：按 agentMeta.artifactId
// 反查到画布节点 → 开节点自己的展开编辑弹层；节点已删 → 用落库的 artifact.text
// 快照开 ChatTextSectionDialog，保存落成新节点。原「点击平移定位」被弹框取代
// ——弹的就是那个节点的内容，定位诉求由此天然满足。
function TextInfoChip({ a, chipCls }: { a: Artifact; chipCls: string }) {
  const [dialog, setDialog] = useState<null | { nodeId: string; markdown: string } | { snapshot: true }>(null);
  const label = a.name || '文本';
  const open = () => {
    const node = useCanvasStore.getState().nodes.find((n) => {
      const meta = n.settings as { agentMeta?: { artifactId?: string } } | undefined;
      return meta?.agentMeta?.artifactId === a.artifact_id;
    });
    if (node) {
      const text = (node.settings as { text?: unknown } | undefined)?.text;
      setDialog({ nodeId: node.id, markdown: typeof text === 'string' ? text : '' });
      return;
    }
    setDialog({ snapshot: true });
  };
  return (
    <>
      <button
        type="button"
        title="点击在大窗中查看/编辑全文"
        onClick={open}
        className={`${chipCls} hover:bg-popover cursor-pointer`}
      >
        <FileText size={12} className="text-blue-400 shrink-0" />
        <span className="truncate">{label}</span>
      </button>
      {dialog && 'nodeId' in dialog && (
        <TextExpandDialog
          nodeId={dialog.nodeId}
          initialMarkdown={dialog.markdown}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog && 'snapshot' in dialog && (
        <ChatTextSectionDialog
          name={label}
          initialMarkdown={a.text ?? ''}
          onClose={() => setDialog(null)}
        />
      )}
    </>
  );
}

// 用户文本块：内联长文本段折叠成卡片，其余文字照常。export 仅为测试。
export function UserTextBlock({ text }: { text: string }) {
  const pieces = useMemo(() => splitBubbleText(text), [text]);
  return (
    <div className="whitespace-pre-wrap">
      {pieces.map((p, i) =>
        p.type === 'text' ? (
          <span key={i}>{p.text}</span>
        ) : (
          <InlineSectionCard key={i} section={p} />
        ),
      )}
    </div>
  );
}

export function MessageBubble({
  msg,
  dedupeUrls,
}: {
  msg: AgentMessage;
  dedupeUrls?: Set<string>;
}) {
  const isUser = msg.role === 'user';
  const [copied, setCopied] = useState(false);
  const setLightbox = useCanvasStore((s) => s.setLightbox);

  // 这条消息到底有没有东西可渲染。一条只带 tool_result（隐藏）+ tool_event 的
  // tool 消息，如果它的 artifact 全被 dedupe 掉了就会塌缩成空——这种空气泡外壳直接跳过。
  const hasVisible = useMemo(() => {
    if (msg.pending) return true;
    if (msg.workflowSteps && msg.workflowSteps.length > 0) return true;
    return msg.blocks.some((b) => {
      if (!b) return false; // 洞位兜底，与下方渲染循环的守卫成对
      switch (b.type) {
        case 'text':
          return !!b.text;
        case 'tool_use':
        case 'skill':
          return true;
        case 'image':
        case 'video':
        case 'audio':
          return !!b.url;
        case 'tool_event':
          return (b.artifacts ?? []).some(
            (a) =>
              // chip 类（text_info / gen_*_node_info / 画布操作五件套）没有 url，
              // 但要渲染成操作/定位 chip，不能被 url 过滤连坐吞掉。
              NO_URL_CHIP_TYPES.has(a.artifact_type) ||
              (a.artifact_type !== 'skill_definition' && a.url && !dedupeUrls?.has(a.url)),
          );
        default:
          return false; // tool_result 永远不渲染
      }
    });
  }, [msg.blocks, msg.pending, msg.workflowSteps, dedupeUrls]);

  // 把所有 text block 拍平成一个字符串供复制按钮用——tool_use / tool_event 不参与，
  // 只复制可见正文。
  const copyText = useMemo(() => {
    return msg.blocks
      .filter((b) => b && b.type === 'text' && b.text)
      .map((b) => b.text)
      .join('\n');
  }, [msg.blocks]);

  async function copyToClipboard() {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(copyText);
      } else {
        // 非安全上下文（HTTP）下的兜底路径。临时塞一个 textarea，
        // 用 document.execCommand 复制。
        const ta = document.createElement('textarea');
        ta.value = copyText;
        ta.style.position = 'fixed';
        ta.style.left = '-9999px';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 无操作：UI 只是不会切到「已复制」状态而已。
    }
  }

  if (!hasVisible) return null;

  return (
    <div
      id={`agent-msg-${msg.action_id}`}
      className={`group flex ${isUser ? 'justify-end' : 'justify-start'}`}
    >
      <div
        className={`max-w-[85%] relative rounded-2xl px-3 py-2 text-[12px] leading-relaxed break-words ${
          isUser
            ? 'bg-blue-600 text-white'
            : 'bg-popover text-foreground border border-input'
        } ${msg.pending ? 'opacity-80' : ''}`}
      >
        {msg.blocks.map((b, i) => {
          // 兜底：流式 index 全局递增后 blocks 可能有洞（store 写入前已补齐为
          // 空 text block，这里再挡一道，防未来别的写入路径漏补）。
          if (!b) return null;
          if (b.type === 'text') {
            const text = b.text ?? '';
            if (isUser) {
              return <UserTextBlock key={i} text={text} />;
            }
            return <MarkdownBlock key={i} text={text} />;
          }
          if (b.type === 'skill') {
            return (
              <div key={i} className="mb-1.5">
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] bg-white/15 text-white">
                  <Zap size={11} />
                  {b.name || b.id}
                  {/* 用户没点过任何东西，气泡上凭空出现一个 chip——不标出来他只会
                      困惑"这标签哪来的"。 */}
                  {b.auto && (
                    <span
                      className="px-1 rounded bg-white/20 text-[10px] leading-4"
                      title="根据你的消息内容自动匹配到的技能"
                    >
                      自动
                    </span>
                  )}
                </span>
              </div>
            );
          }
          if (b.type === 'image' && b.url) {
            const url = b.url;
            const title = b.name ?? '';
            return (
              <img
                key={i}
                src={url}
                crossOrigin={mediaCrossOrigin(url)}
                alt={title}
                title="点击放大"
                onClick={() => setLightbox({ url, kind: 'image', title })}
                className="rounded mt-1 w-24 h-24 object-cover border border-white/20 cursor-zoom-in hover:opacity-90 transition-opacity"
              />
            );
          }
          if ((b.type === 'file' || b.type === 'text_ref') && (b.name || b.id)) {
            // 大文本附件引用（正文未内联）：历史气泡里画一个名字+字数的 chip，
            // 让用户知道这条消息带了哪个大文件。
            return (
              <span
                key={i}
                className="inline-flex items-center gap-1 mr-1 mb-1 px-2 py-0.5 rounded-full text-[11px] text-emerald-100 bg-emerald-950/60 border border-emerald-700/50"
              >
                <FileText size={11} />
                <span className="max-w-[180px] truncate">{b.name || b.id}</span>
                {b.char_count ? <span className="opacity-70">约{b.char_count}字</span> : null}
              </span>
            );
          }
          if (b.type === 'video' && b.url) {
            const url = b.url;
            const title = b.name ?? '';
            return (
              <div
                key={i}
                className="relative w-24 h-24 mt-1 rounded border border-white/20 overflow-hidden cursor-zoom-in hover:opacity-90 transition-opacity inline-block"
                onClick={() => setLightbox({ url, kind: 'video', title })}
                title="点击播放"
              >
                <video
                  src={url} crossOrigin={mediaCrossOrigin(url)}
                  muted
                  preload="metadata"
                  playsInline
                  className="w-full h-full object-cover pointer-events-none"
                />
                <div className="absolute inset-0 flex items-center justify-center bg-black/30 text-white/90 text-lg pointer-events-none">
                  ▶
                </div>
              </div>
            );
          }
          if (b.type === 'audio' && b.url) {
            const url = b.url;
            const title = b.name ?? '';
            return (
              <button
                key={i}
                type="button"
                onClick={() => setLightbox({ url, kind: 'audio', title })}
                title="点击播放"
                className="mt-1 inline-flex items-center gap-2 px-2 py-1 rounded border border-white/20 bg-popover/60 hover:bg-popover text-foreground text-[12px] cursor-pointer max-w-full"
              >
                <span className="text-blue-300 text-base leading-none">♪</span>
                <span className="truncate">{title || '音频'}</span>
              </button>
            );
          }
          if (b.type === 'tool_use') {
            return (
              <div
                key={i}
                className="text-muted-foreground text-[11px] italic flex items-center gap-1 mt-1"
              >
                <span>⏵</span>
                <span>调用 {b.display_name || b.name}</span>
              </div>
            );
          }
          if (b.type === 'tool_event' && b.artifacts) {
            // 无 url 的 chip 类（text_info / gen_*_node_info / 画布操作五件套）
            // 单独走「chip」渲染：产物要么已落成画布节点、要么是一次画布操作，
            // 气泡里留一个锚——可定位的（有目标节点）渲染成按钮，点了把视口
            // 平移过去；删除/断线这类没有可定位目标的渲染成静态说明。
            const chips = b.artifacts.filter((a) => NO_URL_CHIP_TYPES.has(a.artifact_type));
            const items = b.artifacts.filter(
              (a) =>
                a.artifact_type !== 'skill_definition' &&
                !NO_URL_CHIP_TYPES.has(a.artifact_type) &&
                a.url &&
                !dedupeUrls?.has(a.url),
            );
            if (items.length === 0 && chips.length === 0) return null;
            const chipCls =
              'inline-flex items-center gap-1.5 px-2 py-1 rounded border border-white/20 bg-popover/60 text-foreground text-[12px] max-w-full';
            return (
              <div key={i} className="flex flex-wrap gap-1 mt-1.5">
                {chips.map((a) => {
                  if (a.artifact_type === 'text_info') {
                    return <TextInfoChip key={a.artifact_id} a={a} chipCls={chipCls} />;
                  }
                  const { label, Icon, locate } = chipDescriptor(a);
                  if (!locate) {
                    return (
                      <span key={a.artifact_id} className={chipCls}>
                        <Icon size={12} className="text-blue-400 shrink-0" />
                        <span className="truncate">{label}</span>
                      </span>
                    );
                  }
                  return (
                    <button
                      key={a.artifact_id}
                      type="button"
                      title="定位到画布节点"
                      onClick={() => {
                        const cs = useCanvasStore.getState();
                        const nodeId =
                          locate.kind === 'byNodeId'
                            ? locate.nodeId
                            : cs.nodes.find((n) => {
                                const meta = n.settings as
                                  | { agentMeta?: { artifactId?: string } }
                                  | undefined;
                                return meta?.agentMeta?.artifactId === a.artifact_id;
                              })?.id;
                        // byNodeId 也要过存在性检查——节点可能已被后续操作删掉。
                        if (nodeId && cs.nodes.some((n) => n.id === nodeId)) cs.centerNode(nodeId);
                      }}
                      className={`${chipCls} hover:bg-popover cursor-pointer`}
                    >
                      <Icon size={12} className="text-blue-400 shrink-0" />
                      <span className="truncate">{label}</span>
                    </button>
                  );
                })}
                {items.map((a) => (
                  a.artifact_type === 'audio_info' ? (
                    // 音频产物没有画面，<img>/<video> 都是破图——按 audio block
                    // 同款样式给一个可点开 lightbox 的小块。
                    <button
                      key={a.artifact_id}
                      type="button"
                      title="点击播放"
                      onClick={() => setLightbox({ url: a.url!, kind: 'audio', title: a.name ?? '' })}
                      className="inline-flex items-center gap-1.5 px-2 py-1 rounded border border-white/20 bg-popover/60 hover:bg-popover text-foreground text-[12px] cursor-pointer max-w-full"
                    >
                      <span className="text-blue-300 text-base leading-none">♪</span>
                      <span className="truncate">{a.name || '音频'}</span>
                    </button>
                  ) : (
                  <div
                    key={a.artifact_id}
                    className="w-20 h-20 rounded overflow-hidden border border-white/20 bg-black/20"
                  >
                    {a.artifact_type === 'video_info' ? (
                      <video
                        src={a.url} crossOrigin={mediaCrossOrigin(a.url)}
                        className="w-full h-full object-cover"
                        muted
                        playsInline
                        preload="metadata"
                      />
                    ) : (
                      <img
                        src={a.url}
                        crossOrigin={mediaCrossOrigin(a.url)}
                        alt={a.name ?? ''}
                        className="w-full h-full object-cover"
                      />
                    )}
                  </div>
                  )
                ))}
              </div>
            );
          }
          // tool_result 是给 LLM 看的内部管道（裸 URL / 状态文本）——不渲染给用户。
          // 成功时显示 tool_event 缩略图；失败则由 planner 的回复来呈现。
          return null;
        })}
        <WorkflowProgress msg={msg} />

        {/* 复制按钮——任何带文本内容的消息都会显示。定位 + hover 才显示
            与参考仓 ChatPanel 的气泡一致。 */}
        {copyText && (
          <button
            onClick={copyToClipboard}
            title={copied ? '已复制' : '复制'}
            className={`absolute -bottom-2 ${isUser ? 'left-1' : 'right-1'} w-6 h-6 flex items-center justify-center rounded border bg-card border-input text-sidebar-foreground hover:text-white hover:border-input opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer p-0`}
          >
            {copied ? <Check size={11} className="text-emerald-400" /> : <Copy size={11} />}
          </button>
        )}
      </div>
    </div>
  );
}
