// ChatPanel——右侧抽屉，承载 project-scoped 的聊天会话。
//
// 结构 + UX 移植自备份的单文件应用
// Tapnow-Studio-PP-3.8.7/src/App.jsx 第 34348-34795 行附近：
//   - 固定在右侧的抽屉，translate-x 动画
//   - 会话列表下拉（创建 / 切换 / 删除 / 重命名）
//   - 气泡（assistant 在左、user 在右），Markdown 走 marked+DOMPurify
//   - textarea + Enter 发送 / Shift+Enter 换行
//   - 图片附件以 base64 data URL 内联进请求
//
// 跟备份用 localStorage 持久化不同，这里的会话是按项目存在服务端的，
// 所以切换项目会加载不同的历史记录。

import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import { mediaCrossOrigin } from '../editor/utils/mediaCrossOrigin';
import {
  Check,
  Copy,
  ImagePlus,
  Loader2,
  MessagesSquare,
  Paperclip,
  Plus,
  Send,
  Trash2,
  Pencil,
  X,
  Sparkles,
  ChevronDown,
} from 'lucide-react';
import {
  appendChatMessage,
  chatCompletion,
  ChatFileAttachment,
  ChatMessageRecord,
  ChatSession,
  ChatMessage as LLMMessage,
  createChatSession,
  deleteChatSession,
  listChatMessages,
  listChatSessions,
  listRuntimeModels,
  updateChatSession,
  uploadAsset,
} from '@/shared/api';

// 每一轮重放给 LLM 的聊天历史消息上限。跟备份的 MAX_HISTORY_MESSAGES 对齐，
// 让 token 用量保持可控。
const MAX_REPLAY_MESSAGES = 20;

marked.setOptions({ breaks: true, gfm: true });

function renderMarkdown(src: string): string {
  // DOMPurify 会剥掉危险的标签/属性；之后再 dangerouslySetInnerHTML 是安全的。
  // 我们也禁掉删除线标签——marked+GFM 偶尔会把中文标点密集的文本误标成 <del>
  //（整段回复从头到尾被裹住），而聊天里正经的删除线极少，所以剥掉很安全。
  return DOMPurify.sanitize(marked.parse(src, { async: false }) as string, {
    FORBID_TAGS: ['del', 's', 'strike'],
  });
}

// 把一个原始 File 转成 ChatFileAttachment。
// - 媒体（image/video/audio/doc）→ 通过 /api/v1/assets 上传到对象存储，
//   这样 URL 稳定、计入用户的存储配额，且在所属项目删除时自动清理。
// - 代码 / 文本文件保持内联（不占存储，模型直接在 ```fenced``` 代码块里看到内容）。
async function fileToAttachment(file: File, projectId: number | undefined): Promise<ChatFileAttachment> {
  const t = file.type.toLowerCase();
  const kind: ChatFileAttachment['type'] =
    t.startsWith('image/') ? 'image'
    : t.startsWith('video/') ? 'video'
    : t.startsWith('audio/') ? 'audio'
    : t.startsWith('text/') || /(\.(md|txt|json|js|ts|tsx|py|go|sql|ya?ml))$/i.test(file.name)
      ? 'code'
      : 'doc';

  if (kind === 'code') {
    const text = await file.text();
    return { type: 'code', name: file.name, mime: file.type, text };
  }
  const asset = await uploadAsset(file, projectId);
  return { type: kind, name: file.name, mime: file.type, url: asset.url };
}

// 把附件转成 OpenAI 兼容的 content part，用于用户消息。图片用 image_url；
// 其他类型以文本占位符列出，这样模型至少能看到「附了一个文件」这层上下文。
function buildUserContent(text: string, files: ChatFileAttachment[]): any {
  if (files.length === 0) return text;
  const parts: any[] = [];
  if (text) parts.push({ type: 'text', text });
  for (const f of files) {
    if (f.type === 'image' && f.url) {
      parts.push({ type: 'image_url', image_url: { url: f.url } });
    } else if (f.type === 'code' && f.text) {
      parts.push({ type: 'text', text: `[File: ${f.name ?? 'file'}]\n\`\`\`\n${f.text}\n\`\`\`` });
    } else {
      parts.push({ type: 'text', text: `[Attached ${f.type}: ${f.name ?? 'file'}]` });
    }
  }
  return parts;
}

// ============================================================================

interface Props {
  open: boolean;
  projectId: number;
  onClose: () => void;
}

export function ChatPanel({ open, projectId, onClose }: Props) {
  const qc = useQueryClient();
  const [currentSessionId, setCurrentSessionId] = useState<number | null>(null);
  const [input, setInput] = useState('');
  const [files, setFiles] = useState<ChatFileAttachment[]>([]);
  const [selectedModelId, setSelectedModelId] = useState<string>('');
  const [sessionMenuOpen, setSessionMenuOpen] = useState(false);
  const [sending, setSending] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const listEndRef = useRef<HTMLDivElement>(null);

  // ---- 查询 --------------------------------------------------------
  const sessionsQuery = useQuery({
    queryKey: ['chat-sessions', projectId],
    queryFn: () => listChatSessions(projectId),
    enabled: open && projectId > 0,
  });

  const modelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });
  const textModels = useMemo(
    () => (modelsQuery.data ?? []).filter((m) => m.type === 'text'),
    [modelsQuery.data],
  );

  const messagesQuery = useQuery({
    queryKey: ['chat-messages', projectId, currentSessionId],
    queryFn: () => listChatMessages(projectId, currentSessionId!),
    enabled: open && !!currentSessionId,
  });

  // ---- effects --------------------------------------------------------
  // 存在会话时自动选第一个；否则在首次发送时惰性创建。
  useEffect(() => {
    if (!open || !sessionsQuery.data) return;
    if (currentSessionId == null && sessionsQuery.data.length > 0) {
      setCurrentSessionId(sessionsQuery.data[0].id);
    }
    if (currentSessionId != null && !sessionsQuery.data.find((s) => s.id === currentSessionId)) {
      // 当前会话在别处被删了——回退到第一个。
      setCurrentSessionId(sessionsQuery.data[0]?.id ?? null);
    }
  }, [open, sessionsQuery.data, currentSessionId]);

  useEffect(() => {
    if (!selectedModelId && textModels[0]) setSelectedModelId(textModels[0].modelId);
  }, [selectedModelId, textModels]);

  // 新消息到达时滚动到底部。
  useEffect(() => {
    listEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messagesQuery.data, sending]);

  // ---- mutations ------------------------------------------------------
  const createMutation = useMutation({
    mutationFn: (body?: { title?: string; defaultModel?: string }) => createChatSession(projectId, body),
    onSuccess: (s) => {
      qc.invalidateQueries({ queryKey: ['chat-sessions', projectId] });
      setCurrentSessionId(s.id);
      setSessionMenuOpen(false);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (sessionId: number) => deleteChatSession(projectId, sessionId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['chat-sessions', projectId] }),
  });

  const renameMutation = useMutation({
    mutationFn: ({ sessionId, title }: { sessionId: number; title: string }) =>
      updateChatSession(projectId, sessionId, { title }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['chat-sessions', projectId] }),
  });

  // ---- 发送流程 ------------------------------------------------------
  async function ensureSession(): Promise<number> {
    if (currentSessionId) return currentSessionId;
    const s = await createChatSession(projectId, {
      title: (input.trim().slice(0, 20) || '新对话'),
      defaultModel: selectedModelId,
    });
    qc.invalidateQueries({ queryKey: ['chat-sessions', projectId] });
    setCurrentSessionId(s.id);
    return s.id;
  }

  async function send() {
    const text = input.trim();
    if (!text && files.length === 0) return;
    if (!selectedModelId) return;
    if (sending) return;

    setSending(true);
    try {
      const sessionId = await ensureSession();

      // 1. 持久化用户消息
      await appendChatMessage(projectId, sessionId, {
        role: 'user',
        content: text,
        files: files.length > 0 ? files : undefined,
        modelId: selectedModelId,
      });
      setInput('');
      setFiles([]);
      // 乐观重新拉取
      await qc.invalidateQueries({ queryKey: ['chat-messages', projectId, sessionId] });

      // 2. 用最近的历史记录构建 llm payload
      const history = (messagesQuery.data ?? []).slice(-MAX_REPLAY_MESSAGES);
      const llmMessages: LLMMessage[] = [
        { role: 'system', content: '你是一名多模态 AI 助手，请结合整个对话上下文给出连贯的回答。' },
        ...history.map((m) => ({ role: m.role as 'user' | 'assistant' | 'system', content: m.content })),
        { role: 'user', content: buildUserContent(text, files) as any },
      ];

      // 3. 调用上游
      try {
        const resp = await chatCompletion({
          model: selectedModelId,
          messages: llmMessages,
          temperature: 0.7,
        });
        const raw = resp.choices?.[0]?.message?.content ?? '';
        const content = Array.isArray(raw) ? (raw as any[]).map((p: any) => p.text || '').join('') : raw;
        await appendChatMessage(projectId, sessionId, {
          role: 'assistant',
          content: content || '(空响应)',
          modelId: selectedModelId,
        });
      } catch (err: any) {
        // newapi 有时把 error 作为对象返回（{message, type, code}），
        // 有时又是字符串。两种情况都拍平成可读的字符串。
        const raw = err?.response?.data?.error;
        const errText = typeof raw === 'string'
          ? raw
          : (raw?.message ?? raw?.code ?? err?.message ?? '调用失败');
        await appendChatMessage(projectId, sessionId, {
          role: 'assistant',
          content: `Error: ${errText}`,
          modelId: selectedModelId,
          isError: true,
        });
      }
      qc.invalidateQueries({ queryKey: ['chat-messages', projectId, sessionId] });
    } finally {
      setSending(false);
    }
  }

  // ---- 文件输入 -----------------------------------------------------
  const fileInputRef = useRef<HTMLInputElement>(null);
  async function onFilesChosen(list: FileList | null) {
    if (!list) return;
    const added: ChatFileAttachment[] = [];
    for (const f of Array.from(list)) {
      try { added.push(await fileToAttachment(f, projectId)); } catch { /* 忽略 */ }
    }
    setFiles((prev) => [...prev, ...added]);
  }

  // ---- 渲染 ---------------------------------------------------------
  const currentSession = (sessionsQuery.data ?? []).find((s) => s.id === currentSessionId);
  const messages = messagesQuery.data ?? [];

  return (
    <div
      className="chat-panel fixed top-0 right-0 bottom-0 z-[900] bg-background border-l border-border flex flex-col shadow-2xl transition-transform"
      style={{
        width: 420,
        transform: open ? 'translateX(0)' : 'translateX(100%)',
      }}
    >
      {/* 头部 */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
        <MessagesSquare size={14} className="text-blue-400 shrink-0" />
        <div className="flex-1 min-w-0 relative">
          <button
            className="flex items-center gap-1.5 w-full px-2 py-1 text-xs text-foreground bg-card hover:bg-popover rounded border border-input cursor-pointer"
            onClick={() => setSessionMenuOpen((v) => !v)}
          >
            <span className="truncate flex-1 text-left">
              {currentSession?.title ?? '新对话'}
            </span>
            <ChevronDown size={12} className="text-muted-foreground shrink-0" />
          </button>
          {sessionMenuOpen && (
            <>
              <div className="fixed inset-0 z-10" onClick={() => setSessionMenuOpen(false)} />
              <div className="absolute left-0 right-0 top-full mt-1 bg-card border border-input rounded shadow-xl z-20 max-h-80 overflow-auto">
                <button
                  className="w-full flex items-center gap-2 px-3 py-2 text-xs text-blue-400 bg-transparent hover:bg-popover border-none border-b border-b-border text-left cursor-pointer"
                  onClick={() => createMutation.mutate({ defaultModel: selectedModelId })}
                >
                  <Plus size={12} />
                  新建会话
                </button>
                {(sessionsQuery.data ?? []).map((s) => (
                  <SessionRow
                    key={s.id}
                    session={s}
                    active={s.id === currentSessionId}
                    onSelect={() => { setCurrentSessionId(s.id); setSessionMenuOpen(false); }}
                    onDelete={() => {
                      if (confirm(`确定删除会话「${s.title}」吗？`)) deleteMutation.mutate(s.id);
                    }}
                    onRename={(newTitle) => renameMutation.mutate({ sessionId: s.id, title: newTitle })}
                  />
                ))}
                {(sessionsQuery.data ?? []).length === 0 && !sessionsQuery.isLoading && (
                  <div className="px-3 py-3 text-[11px] text-muted-foreground text-center">还没有会话</div>
                )}
              </div>
            </>
          )}
        </div>
        <button
          className="text-muted-foreground hover:text-foreground transition-colors cursor-pointer bg-transparent border-none p-0"
          onClick={onClose}
          title="关闭"
        >
          <X size={16} />
        </button>
      </div>

      {/* 消息区 */}
      <div className="flex-1 overflow-auto p-3 flex flex-col gap-3 custom-scrollbar">
        {!currentSessionId && (
          <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground text-xs gap-2 text-center px-8">
            <Sparkles size={28} className="text-muted-foreground" />
            <div>还没有对话，点击输入框发送第一条消息即可开始</div>
          </div>
        )}
        {messages.map((m) => (
          <MessageBubble key={m.id} message={m} />
        ))}
        {sending && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 size={12} className="animate-spin" />
            正在思考…
          </div>
        )}
        <div ref={listEndRef} />
      </div>

      {/* 附件预览 */}
      {files.length > 0 && (
        <div className="flex gap-2 overflow-x-auto px-3 py-2 border-t border-border bg-card/50 shrink-0">
          {files.map((f, i) => (
            <div key={i} className="relative w-12 h-12 rounded border border-input bg-background overflow-hidden group shrink-0">
              {f.type === 'image' && f.url ? (
                <img src={f.url} crossOrigin={mediaCrossOrigin(f.url)} className="w-full h-full object-cover" alt="" />
              ) : (
                <div className="w-full h-full flex items-center justify-center text-[9px] text-sidebar-foreground px-1 text-center break-all">
                  {f.name ?? f.type}
                </div>
              )}
              <button
                className="absolute top-0 right-0 w-4 h-4 bg-black/70 hover:bg-red-600 text-white rounded-bl flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity border-none cursor-pointer"
                onClick={() => setFiles((prev) => prev.filter((_, idx) => idx !== i))}
                title="移除"
              >
                <X size={9} />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* 输入 + 控件 */}
      <div className="border-t border-border px-3 py-2 flex flex-col gap-2 shrink-0">
        <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <select
            className="flex-1 min-w-0 max-w-[220px] px-2 py-0.5 rounded text-[11px] bg-popover hover:bg-accent text-foreground border border-input outline-none cursor-pointer font-mono"
            value={selectedModelId}
            onChange={(e) => setSelectedModelId(e.target.value)}
          >
            {textModels.length === 0 && <option value="">（后台需配置 type=text 模型）</option>}
            {textModels.map((m) => (
              <option key={m.modelId} value={m.modelId}>{m.name}</option>
            ))}
          </select>
          <span className="text-muted-foreground">Enter 发送 · Shift+Enter 换行</span>
        </div>
        <div className="flex items-end gap-2">
          <button
            className="w-8 h-8 flex items-center justify-center text-sidebar-foreground hover:text-blue-400 rounded border border-input bg-card hover:bg-popover cursor-pointer"
            onClick={() => fileInputRef.current?.click()}
            title="添加附件"
          >
            <Paperclip size={14} />
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,video/*,audio/*,.pdf,.doc,.docx,.xls,.xlsx,.txt,.md,.js,.ts,.tsx,.py,.json,.csv,.html,.css"
            className="hidden"
            onChange={(e) => { onFilesChosen(e.target.files); e.target.value = ''; }}
          />
          <textarea
            ref={textareaRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void send();
              }
            }}
            onPaste={(e) => {
              // 粘贴图片 → 直接作为附件（跟备份一样）。
              const imgs = Array.from(e.clipboardData.items)
                .filter((it) => it.type.startsWith('image/'))
                .map((it) => it.getAsFile())
                .filter(Boolean) as File[];
              if (imgs.length > 0) {
                e.preventDefault();
                const dt = new DataTransfer();
                imgs.forEach((f) => dt.items.add(f));
                onFilesChosen(dt.files);
              }
            }}
            placeholder="输入消息… (Enter 发送)"
            rows={2}
            className="flex-1 resize-none bg-background border border-input focus:border-blue-500 rounded p-2 text-xs text-foreground outline-none placeholder:text-muted-foreground max-h-32"
          />
          <button
            className="w-8 h-8 flex items-center justify-center text-white bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed rounded border-none cursor-pointer"
            onClick={send}
            disabled={sending || (!input.trim() && files.length === 0) || !selectedModelId}
            title="发送"
          >
            {sending ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
          </button>
        </div>
      </div>
    </div>
  );
}

// --------------------------------------------------------------- 子组件
function SessionRow({
  session,
  active,
  onSelect,
  onDelete,
  onRename,
}: {
  session: ChatSession;
  active: boolean;
  onSelect: () => void;
  onDelete: () => void;
  onRename: (newTitle: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(session.title);
  return (
    <div
      className={`group flex items-center gap-1 px-3 py-1.5 text-xs cursor-pointer border-b border-border ${active ? 'bg-blue-950/40 text-blue-100' : 'text-sidebar-foreground hover:bg-popover'}`}
      onClick={() => (editing ? null : onSelect())}
    >
      {editing ? (
        <input
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => {
            if (e.key === 'Enter') { onRename(draft.trim() || session.title); setEditing(false); }
            if (e.key === 'Escape') { setDraft(session.title); setEditing(false); }
          }}
          onBlur={() => { onRename(draft.trim() || session.title); setEditing(false); }}
          className="flex-1 bg-background border border-input rounded px-1.5 py-0.5 text-xs outline-none"
        />
      ) : (
        <span className="truncate flex-1">{session.title}</span>
      )}
      <span className="text-[10px] text-muted-foreground shrink-0">{session.messageCount ?? 0}</span>
      <button
        className="text-muted-foreground hover:text-blue-400 opacity-0 group-hover:opacity-100 shrink-0 bg-transparent border-none p-0.5 cursor-pointer"
        onClick={(e) => { e.stopPropagation(); setEditing(true); }}
        title="重命名"
      >
        <Pencil size={11} />
      </button>
      <button
        className="text-muted-foreground hover:text-red-400 opacity-0 group-hover:opacity-100 shrink-0 bg-transparent border-none p-0.5 cursor-pointer"
        onClick={(e) => { e.stopPropagation(); onDelete(); }}
        title="删除"
      >
        <Trash2 size={11} />
      </button>
    </div>
  );
}

function MessageBubble({ message }: { message: ChatMessageRecord }) {
  const isUser = message.role === 'user';
  const isError = message.isError;
  const [copied, setCopied] = useState(false);

  async function copyToClipboard() {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(message.content);
      } else {
        // 非安全上下文（HTTP）下的兜底路径。临时塞一个 textarea，
        // 用 document.execCommand 复制。
        const ta = document.createElement('textarea');
        ta.value = message.content;
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

  return (
    <div className={`group flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div
        className={`max-w-[85%] relative rounded-2xl px-3 py-2 text-[12px] leading-relaxed break-words ${
          isError
            ? 'bg-red-950/40 text-red-200 border border-red-700/50'
            : isUser
              ? 'bg-blue-600 text-white'
              : 'bg-popover text-foreground border border-input'
        }`}
      >
        {message.files && message.files.length > 0 && (
          <div className="flex gap-1 flex-wrap mb-1.5">
            {message.files.map((f, i) => (
              <div key={i} className="w-16 h-16 rounded overflow-hidden border border-white/20 bg-black/20">
                {f.type === 'image' && f.url ? (
                  <img src={f.url} crossOrigin={mediaCrossOrigin(f.url)} className="w-full h-full object-cover" alt="" />
                ) : (
                  <div className="w-full h-full flex items-center justify-center text-[9px] px-1 text-center break-all">
                    <ImagePlus size={12} className="opacity-60" />
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
        {isUser ? (
          <div className="whitespace-pre-wrap">{message.content}</div>
        ) : (
          <div
            className="markdown-body"
            dangerouslySetInnerHTML={{ __html: renderMarkdown(message.content) }}
          />
        )}

        {/* 复制按钮——任何非空消息都会显示。定位在气泡右下角，hover 时淡入。 */}
        {message.content && (
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
