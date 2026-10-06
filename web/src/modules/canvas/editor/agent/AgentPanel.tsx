// apps/web/src/canvas/agent/AgentPanel.tsx
//
// 承载 agent 对话的右侧抽屉。视觉结构是 apps/web/src/panels/ChatPanel.tsx 的
// 1:1 移植（抽屉外壳、会话下拉、消息区、附件预览、输入栏）——唯一的区别在数据层：
//   - REST 聊天补全 → 经 AgentClient 走 WebSocket
//   - ChatMessageRecord[]   → AgentMessage[]（来自 useAgentStore）
//   - 模型下拉            → skill chip + skill 选择按钮
//
// 注意：agent thread 的 CRUD 在 API 上还是只读的（暂时没有 delete/rename
// 端点），所以会话下拉里跳过了那些操作。

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { nanoid } from 'nanoid';
import {
  Bot,
  ChevronDown,
  Check,
  Copy,
  ImagePlus,
  Loader2,
  Square,
  Paperclip,
  Pencil,
  Plus,
  Send,
  Sparkles,
  Trash2,
  X,
  Zap,
} from 'lucide-react';

import {
  createAgentThread,
  deleteAgentThread,
  listAgentMessages,
  listAgentThreads,
  updateAgentThread,
  uploadAsset,
  type AgentMessageDTO,
  type AgentSkillDTO,
} from '@/shared/api';
import { useCanvasStore } from '../store';
import { AgentClient } from './agentClient';
import { useAgentStore, type AgentMessage } from './agentStore';
import { handoffContent, shouldConsumeHandoff, useAgentHandoff } from './agentHandoff';
import { buildCanvasSummary } from './canvasSummary';
import { extractMentions } from './mentionParser';
import { buildInlineSections, INLINE_TEXT_MAX, splitTextChips } from './mentionInline';
import { MentionInput, type MentionInputHandle } from './MentionInput';
import { MentionPicker } from './MentionPicker';
import { MessageBubble } from './MessageBubble';
import { SkillCreateModal } from './SkillCreateModal';
import { SkillPopover } from './SkillPopover';
import type { Artifact, ContentBlock, Mention } from './types';
import { resolveSidecarWsEndpoint, sidecarWsUrl } from '@/shared/sidecarSession';

export interface AgentPanelProps {
  projectId: number;
  jwt: string;
  open: boolean;
  /** 面板宽度是受控 props——真源在宿主页（CanvasEditorPage）。
      故事板模式下覆盖层要按同一个宽度让位（挤压布局），两处必须
      同帧更新，宽度只能有一份 state。 */
  width: number;
  onWidthChange: (w: number) => void;
  onClose: () => void;
  onArtifact?: (ctx: { thread_id: string; action_id: string; artifact: Artifact }) => void;
}

// 文本附件拒收上限（spec 2026-08-31），与服务端 read_attachment 的 10MB cap 一致。
const ATTACH_MAX_BYTES = 10 * 1024 * 1024;

const PANEL_WIDTH_MIN = 380;
const PANEL_WIDTH_MAX = 720;
const PANEL_WIDTH_DEFAULT = 480;
const PANEL_WIDTH_STORAGE_KEY = 'tvmao.agentPanelWidth';

/** 宿主页做惰性初始化用（全局一个值，所有画布共用）。 */
export function loadAgentPanelWidth(): number {
  if (typeof window === 'undefined') return PANEL_WIDTH_DEFAULT;
  const raw = window.localStorage.getItem(PANEL_WIDTH_STORAGE_KEY);
  const v = raw ? parseInt(raw, 10) : NaN;
  if (Number.isFinite(v)) {
    return Math.min(PANEL_WIDTH_MAX, Math.max(PANEL_WIDTH_MIN, v));
  }
  return PANEL_WIDTH_DEFAULT;
}

export function saveAgentPanelWidth(w: number): void {
  try {
    window.localStorage.setItem(PANEL_WIDTH_STORAGE_KEY, String(w));
  } catch { /* ignore */ }
}

// 懒加载页大小。一"轮"对话落 DB 2~3 行（user+assistant+tool），30 条 ≈ 10~15 轮
// ≈ 6 屏以上缓冲；更小则常态进面板要连拉两页。设计依据见
// docs/2026-08-13-agent-chat-cursor-lazy-load-design.md（D1，用户拍板 30）。
const PAGE_SIZE = 30;

export function AgentPanel({ projectId, jwt, open, width, onWidthChange, onClose, onArtifact }: AgentPanelProps) {
  // 实时输入值（规范化的 `@[name](id)` token）——发送时从 MentionInput 取；
  // 只是镜像进 state 用来控制发送按钮的可用态。
  const [inputEmpty, setInputEmpty] = useState(true);

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const startX = e.clientX;
    const startW = width;
    // 拖拽期间锁定光标 + 禁掉 body 上的文本选区，
    // 避免拖到一半时闪烁 / 变成 I 形光标。
    const prevCursor = document.body.style.cursor;
    const prevUserSelect = document.body.style.userSelect;
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    const onMove = (ev: MouseEvent) => {
      // 面板钉在视口右侧——鼠标往左移时宽度变大。
      const dx = startX - ev.clientX;
      const next = Math.min(PANEL_WIDTH_MAX, Math.max(PANEL_WIDTH_MIN, startW + dx));
      onWidthChange(next);
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = prevCursor;
      document.body.style.userSelect = prevUserSelect;
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  const [skillOpen, setSkillOpen] = useState(false);
  const [selectedSkill, setSelectedSkill] = useState<AgentSkillDTO | null>(null);
  const [mentionActive, setMentionActive] = useState<{ query: string } | null>(null);
  const [threadMenuOpen, setThreadMenuOpen] = useState(false);
  const [uploadingAttachment, setUploadingAttachment] = useState(false);
  // 【L7】sending 与 thread 绑定（记「哪个 thread 在等回复」而不是布尔值）：
  // 布尔版本下，sending 中切到另一个末条为已完成 assistant 的线程，完成检测
  // effect 会拿错线程的消息误翻 sending=false，原线程完成后也不再 hydrate。
  const [sendingThreadId, setSendingThreadId] = useState<string | null>(null);
  // 运行中的 actionId——停止按钮发 abort 帧要按 (thread, action) 精确匹配。
  // 用 ref 不用 state：它只在点停止那一刻被读，不驱动渲染。
  const sendingActionIdRef = useRef<string | null>(null);
  // 【M2】WS 未连通时发送失败的可见报错（内联在输入框上方）。
  const [sendError, setSendError] = useState<string | null>(null);
  // 「threads 列表已为哪个 projectId 拉回」。新项目 threads 为空，靠
  // threads.length 分不清「还没拉」和「拉回是空」；交接单若在拉回前就 send() 建了
  // thread，晚到的 setThreads([]) 会把这个新 thread 从列表里抹掉。
  const [threadsLoadedFor, setThreadsLoadedFor] = useState<number | null>(null);

  const canvasNodes = useCanvasStore((s) => s.nodes);

  const threads = useAgentStore((s) => s.threads);
  const setThreads = useAgentStore((s) => s.setThreads);
  const currentThreadId = useAgentStore((s) => s.currentThreadId);
  const selectThread = useAgentStore((s) => s.selectThread);
  const setCurrentProjectId = useAgentStore((s) => s.setCurrentProjectId);
  const handleFrame = useAgentStore((s) => s.handleFrame);
  const appendUserMessage = useAgentStore((s) => s.appendUserMessage);
  const hydrateMessages = useAgentStore((s) => s.hydrateMessages);
  const hydrateTail = useAgentStore((s) => s.hydrateTail);
  const prependOlderMessages = useAgentStore((s) => s.prependOlderMessages);
  const setLoadingOlder = useAgentStore((s) => s.setLoadingOlder);
  const currentPageState = useAgentStore((s) => (currentThreadId ? s.pageState[currentThreadId] : undefined));
  const setConnectionState = useAgentStore((s) => s.setConnectionState);
  const setArtifactListener = useAgentStore((s) => s.setArtifactListener);
  const messages = useAgentStore((s) => (currentThreadId ? s.messages[currentThreadId] ?? [] : []));
  // 完成检测（L7）要看 sendingThreadId 自己的消息表——它未必是当前展示的 thread。
  const messagesByThread = useAgentStore((s) => s.messages);
  const connectionState = useAgentStore((s) => s.connectionState);
  const pendingSkillDraft = useAgentStore((s) => s.pendingSkillDraft);
  const clearPendingSkillDraft = useAgentStore((s) => s.clearPendingSkillDraft);

  const clientRef = useRef<AgentClient | null>(null);
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  const messagesScrollerRef = useRef<HTMLDivElement | null>(null);
  // 看门狗用：最近一次收到任意 frame 的时间戳。长时间没有任何 frame 说明流
  // 大概率已死（终止帧在掉线窗口里丢了），用来兜底退出「正在思考」。
  const lastFrameAtRef = useRef(0);
  const inputRef = useRef<MentionInputHandle | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  // 【H4】REST re-hydrate 与实时帧的竞态防线：每个 thread 一个世代号。发起
  // hydrate 时世代 +1 并记住快照；响应回来时世代若又前进了（期间用户发了新一轮
  // 消息，或发起了更新的 hydrate），说明本地已有比该响应更新的内容——整表覆盖
  // 会把新一轮的 user 气泡和 pending 占位一起抹掉，之后的 delta / action_end
  // 找不到挂载点全部丢失。过期响应必须整个丢弃。
  const hydrateEpochRef = useRef<Record<string, number>>({});
  const bumpHydrateEpoch = (tid: string) => {
    hydrateEpochRef.current[tid] = (hydrateEpochRef.current[tid] ?? 0) + 1;
    return hydrateEpochRef.current[tid];
  };
  // 所有「把某 thread 对齐到服务器最新状态」的入口统一走这里，保证都吃到
  // 世代守卫。thread id 全局唯一，所以按 tid 记世代即可，无需再掺 projectId。
  // 懒加载改造后这里只拉最近一页（PAGE_SIZE 条）；hydrateTail 的合并规则会
  // 保留用户已上拉加载的更早窗口，所以六个触发点（初始/切线程/轮次完成对账/
  // 看门狗/重连/迟到 action_end）的语义与旧整表覆盖等价、且不再抹旧页。
  const hydrateThread = (tid: string) => {
    const epoch = bumpHydrateEpoch(tid);
    return listAgentMessages(projectId, tid, { limit: PAGE_SIZE })
      .then((page) => {
        if (hydrateEpochRef.current[tid] !== epoch) return; // 已有更新的一轮，丢弃
        hydrateTail(tid, page.data.map(dtoToAgentMessage), page.hasMore);
      })
      .catch((err) => {
        console.error('listAgentMessages failed', err);
        // 必须复位翻页锁：本次 hydrate 已把世代推进，在途的 loadOlder 响应会被
        // 世代守卫丢弃且不自己复位（它以为 hydrateTail 会复位）；hydrate 又失败
        // 到不了 hydrateTail——两头都不复位的话 loadingOlder 永远卡 true，该
        // thread 从此翻不了页（审查 F4，已被独立验证复现）。
        setLoadingOlder(tid, false);
      });
  };

  // ===== 向上滚动懒加载更早历史（设计文档 §5.3） =====
  const topSentinelRef = useRef<HTMLDivElement | null>(null);
  // prepend 前的滚动几何快照——落地后在 paint 前做 scrollTop 补偿用。
  const prependAdjustRef = useRef<{ height: number; top: number } | null>(null);

  const loadOlder = () => {
    const tid = currentThreadId;
    if (!tid) return;
    const st = useAgentStore.getState().pageState[tid];
    if (!st?.hasMore || st.loadingOlder) return;
    // 游标 = 窗口里最早的**服务端**消息（本地乐观消息没有 createdAt，跳过）。
    const list = useAgentStore.getState().messages[tid] ?? [];
    const oldest = list.find((m) => m.createdAt);
    if (!oldest) return;
    // 【H4】的翻页变体：记录世代但**不推进**。期间发生过 tail hydrate（新轮次
    // 完成/看门狗/切回本线程）说明窗口已被重排，旧页拼进去会错位——直接丢弃，
    // 代价只是用户再滚一下重新触发。
    const epoch = hydrateEpochRef.current[tid] ?? 0;
    setLoadingOlder(tid, true);
    listAgentMessages(projectId, tid, { limit: PAGE_SIZE, before: oldest.id })
      .then((page) => {
        if ((hydrateEpochRef.current[tid] ?? 0) !== epoch) return; // 世代已变（loadingOlder 已被 hydrateTail 复位）
        const scroller = messagesScrollerRef.current;
        if (scroller) prependAdjustRef.current = { height: scroller.scrollHeight, top: scroller.scrollTop };
        prependOlderMessages(tid, page.data.map(dtoToAgentMessage), page.hasMore);
      })
      .catch((err) => {
        console.error('load older agent messages failed', err);
        setLoadingOlder(tid, false);
      });
  };

  // prepend 落地后、浏览器 paint 前，把视口钉回原内容位置。不能赌 CSS
  // overflow-anchor（Safari 不支持）；不补偿的话上面插入的一页会把正在看的
  // 内容顶出视口。每次渲染都跑但无快照即返回，只有 prepend 那一次真正生效。
  useLayoutEffect(() => {
    const adjust = prependAdjustRef.current;
    if (!adjust) return;
    prependAdjustRef.current = null;
    const scroller = messagesScrollerRef.current;
    if (!scroller) return;
    scroller.scrollTop = adjust.top + (scroller.scrollHeight - adjust.height);
  });

  // 【M1】看门狗强退过（sending 被兜底翻掉）的 thread 集合。重连成功后后端续传
  // 的帧没有 pending 气泡可挂、会被整批丢弃，且不再有任何 re-hydrate 触发点——
  // 这里记下来，等连接恢复 / action_end 帧到达时补 hydrate，保证最终态最终对齐。
  const watchdogFiredThreadsRef = useRef<Set<string>>(new Set());

  // 启动 WS client
  useEffect(() => {
    if (!jwt) return;
    setCurrentProjectId(projectId);
    setConnectionState('connecting');
    const c = new AgentClient({
      url: sidecarWsUrl('/api/v1/agent/ws'),
      token: jwt,
      projectId,
      // sidecar 重启后端口和 token 都会变：每次自动重连都重新取，不沿用旧地址。
      resolveEndpoint: () => resolveSidecarWsEndpoint('/api/v1/agent/ws'),
    });
    // 【L6】状态回调只认「当前 client」：effect 重建 client 后，旧 socket 的
    // close 事件是异步派发的、可能晚到，不加守卫会把新连接的 connectionState
    // 打回 closed，让 12s 看门狗对健康连接误开火。
    c.onOpen(() => { if (clientRef.current === c) setConnectionState('open'); });
    c.onClose(() => { if (clientRef.current === c) setConnectionState('closed'); });
    c.onFrame(handleFrame);
    c.onFrame(() => { lastFrameAtRef.current = Date.now(); }); // 看门狗心跳
    // 【M1】被看门狗强退过的 thread：它的 pending 气泡已被抹掉，续传帧全部被
    // store 丢弃。action_end 一到说明该轮真正结束、DB 已有完整消息，此刻补一次
    // hydrate 是把最终回复捞回来的确定时机。
    c.onFrame((f) => {
      if (f.type === 'action_end' && f.thread_id && watchdogFiredThreadsRef.current.has(f.thread_id)) {
        watchdogFiredThreadsRef.current.delete(f.thread_id);
        void hydrateThread(f.thread_id);
      }
    });
    c.connect();
    clientRef.current = c;
    if (onArtifact) setArtifactListener(onArtifact);
    return () => {
      if (clientRef.current === c) clientRef.current = null;
      c.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jwt, projectId]);

  // 【M1】重连成功时，对看门狗强退过的 thread 补一次 hydrate：若该轮在断线期间
  // 已经结束（action_end 再也不会来了），这是唯一能把最终回复捞回来的时机。这里
  // 不清集合——后端此刻可能还在跑，真正的结束对齐交给上面的 action_end 兜底；
  // 已结束场景下集合残留只是让下次重连多一个幂等 GET，无害。
  useEffect(() => {
    if (connectionState !== 'open') return;
    setSendError(null); // 连接恢复，「未连通」的发送报错不再成立
    for (const tid of watchdogFiredThreadsRef.current) void hydrateThread(tid);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectionState]);

  // 切项目时拉取该项目的 threads + hydrate 首个 thread 的历史。
  // currentThreadId / messages 是「全局」store——SPA 内从项目 A 切到项目 B 时，
  // 若不先清掉上个项目残留的选中 thread，新项目（尤其是没有任何 thread 的新建
  // 项目）会继续渲染上个项目的会话。所以这里换项目必须无条件重置选择，再按新
  // 项目重选首个 thread（无 thread 则保持空）。不能再用 `!currentThreadId` 守卫
  // ——那正是串台的根源。
  useEffect(() => {
    if (!projectId) return;
    // 【M6】过期响应守卫：项目 A→B 快速切换时，A 的慢响应晚到会把 B 已就位的
    // 线程列表覆盖回 A 的，并 selectThread 到 A 的 thread——之后发消息全落在 A，
    // 而本连接注册的是 B，帧被 project guard 丢弃、永久转圈。effect 已重跑
    //（cancelled=true）就整个丢弃旧响应。
    let cancelled = false;
    setCurrentProjectId(projectId);
    selectThread(null);
    setThreadsLoadedFor(null);
    void listAgentThreads(projectId).then((rows) => {
      if (cancelled) return;
      setThreads(rows.map((r) => ({ id: r.id, title: r.title, updatedAt: r.updated_at })));
      if (rows.length > 0) {
        const first = rows[0];
        selectThread(first.id);
        void hydrateThread(first.id);
      }
      setThreadsLoadedFor(projectId);
    });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, setCurrentProjectId]);

  // 跟随到底部模式。聊天会在每次布局变化时钉在底边，直到用户往上滚动看历史；
  // 收到新消息或切换 thread 时重新启用钉底。这在慢网络下也好使：切换 5 秒后才解码
  // 完成的图片照样会被跟踪，因为 ResizeObserver 会持续触发。
  const pinnedRef = useRef(true);
  const PIN_THRESHOLD_PX = 40;

  // 跟踪手动滚动，以进入 / 离开钉底模式。
  useEffect(() => {
    const scroller = messagesScrollerRef.current;
    if (!scroller) return;
    const onScroll = () => {
      const dist = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
      pinnedRef.current = dist < PIN_THRESHOLD_PX;
    };
    scroller.addEventListener('scroll', onScroll, { passive: true });
    return () => scroller.removeEventListener('scroll', onScroll);
  }, []);

  // 在面板的整个生命周期里重新钉底 + 观察内容。**尾部**新内容（尾消息 id 变化
  // ——发送、流式开始、hydrate 对账、切换 thread）会重新启用钉底，无视当前滚动
  // 位置。依赖故意用尾消息 id 而不是 messages.length：向上懒加载 prepend 一页
  // 也会让 length 增加，但那是用户正在往回看历史，绝不能把人强拽回底部
  // （设计文档 R2）；prepend 不动尾部，尾 id 不变即不触发。
  const tailMessageId = messages.length > 0 ? messages[messages.length - 1].id : '';
  useEffect(() => {
    const scroller = messagesScrollerRef.current;
    if (!scroller) return;
    // 这些触发条件下总是重新钉底，这样滚上去看历史的用户在新消息到达或打开
    // thread 时会被带回底部——跟 Slack/Discord 的 UX 一致。
    pinnedRef.current = true;
    const pin = () => {
      if (!pinnedRef.current) return;
      scroller.scrollTop = scroller.scrollHeight;
    };
    pin();
    const ro = new ResizeObserver(pin);
    for (const child of Array.from(scroller.children)) {
      ro.observe(child);
    }
    // 同时捕获 <img>/<video> 的异步 load 事件——ResizeObserver 对小图替换的
    // 零增量 resize 可能会批处理 / 漏掉。
    const media = scroller.querySelectorAll('img, video');
    media.forEach((el) => {
      el.addEventListener('load', pin);
      el.addEventListener('loadedmetadata', pin);
    });
    return () => {
      ro.disconnect();
      media.forEach((el) => {
        el.removeEventListener('load', pin);
        el.removeEventListener('loadedmetadata', pin);
      });
    };
  }, [tailMessageId, currentThreadId]);

  // 顶部哨兵：滚到距顶 200px 内自动加载更早一页。依赖里带 messages.length 是
  // 刻意的——每次列表变化重建 observer，而 IntersectionObserver 在 observe 时
  // 总会立刻回报一次当前相交状态，这一条性质同时解决两个边界：
  //   1. 初始页/新加载页不足以填满视口（哨兵仍可见）→ 自动续拉直到填满；
  //   2. 用户停在顶部不动、prepend 后哨兵仍在 200px 内 → 无需等下一次滚动手势。
  // 钉在底部的常态下哨兵在视口外，初始回报是 not-intersecting，不会误触发。
  const hasMoreOlder = currentPageState?.hasMore ?? false;
  useEffect(() => {
    if (!hasMoreOlder) return;
    if (typeof IntersectionObserver === 'undefined') return; // jsdom 测试环境没有
    const sentinel = topSentinelRef.current;
    const scroller = messagesScrollerRef.current;
    if (!sentinel || !scroller) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) loadOlder();
      },
      { root: scroller, rootMargin: '200px 0px 0px 0px' },
    );
    io.observe(sentinel);
    return () => io.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasMoreOlder, currentThreadId, messages.length]);

  // 跟踪发送状态——一旦最新的 assistant 消息不再 pending（action_end frame 把
  // pending 翻成 false）就关掉。完成时从服务器重新 hydrate thread，让实时视图跟
  // 持久化的对齐：流式 frame 会把多轮的一次对话塌缩成单条消息（index 碰撞会丢掉
  // 最后的文本回复），而 DB 每轮各保留一行。重新拉取能得到干净的视图。
  //
  // 【L7】只看 sendingThreadId 自己的消息表：用户可能已经切去看别的 thread，
  // 按「当前展示的 thread」判断会误翻状态、漏掉原线程的完成 hydrate。
  // 【H4】hydrate 走 hydrateThread（世代守卫）：用户在响应在途时发下一条消息会
  // 把世代推进，晚到的旧响应被丢弃，不会抹掉新一轮的 user 气泡和 pending 占位。
  useEffect(() => {
    const tid = sendingThreadId;
    if (!tid) return;
    const list = messagesByThread[tid] ?? [];
    const last = list[list.length - 1];
    if (last && last.role === 'assistant' && !last.pending) {
      setSendingThreadId(null);
      void hydrateThread(tid);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messagesByThread, sendingThreadId]);

  // 看门狗兜底：正常情况下 action_end（或 error 帧）会把 sending 翻成 false。但
  // 若 WS 在长任务里掉线、终止帧落在断连窗口里丢了，那个对账就永远不触发、前端
  // 永久转圈。这里在 sending 期间起一个轮询：
  //   - 连接已断且短暂宽限后仍未恢复（重连失败）→ 12s 静默即兜底；
  //   - 连接还在但长时间一帧都没有 → 21min 静默才兜底。
  // 两条阈值分工明确：**掉线那条才是主力**（永久转圈的真实成因几乎都是断连丢帧），
  // 「连接开着却一帧不来」是罕见情况，因为后端只要还活着，至少会送出 action_end。
  //
  // 为什么是 21min 而不是原来的 6.5min：6.5min 只够覆盖 DynamicTool 的默认 5min
  // 轮询，但模型中心允许把 maxWait 配成真实耗时（grok video 那类配到 1200s=20min），
  // 后端单次 Run 的预算更是 60min。阈值卡在 6.5min 意味着一旦真配了慢模型，后端
  // 正干得好好的、前端先判死退出转圈，产物只能等事后 hydrate 补。21min 压过
  // 1200s 这个已知的最大单工具等待，再慢的配置属于后台配置问题，不该由前端兜。
  useEffect(() => {
    if (!sendingThreadId) return;
    const STALL_CLOSED_MS = 12_000;
    const STALL_OPEN_MS = 1_260_000;
    const tid = sendingThreadId;
    const id = setInterval(() => {
      const silent = Date.now() - lastFrameAtRef.current;
      const dead = connectionState === 'closed' || connectionState === 'error';
      if ((dead && silent > STALL_CLOSED_MS) || silent > STALL_OPEN_MS) {
        // 【M1】记下被强退的 thread：此刻的 hydrate 只是半成品兜底，后端 Run 很
        // 可能还活着；重连成功 / action_end 到达时会对这些 thread 再补 hydrate。
        watchdogFiredThreadsRef.current.add(tid);
        void hydrateThread(tid);
        setSendingThreadId(null);
      }
    }, 4000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sendingThreadId, connectionState, projectId]);

  const currentThread = useMemo(
    () => threads.find((t) => t.id === currentThreadId) ?? null,
    [threads, currentThreadId],
  );

  // 已经内联渲染在 assistant 文本回复里的媒体 URL——用来 dedupe：tool_event 缩略图
  // 只有在它的图片/视频已经显示在某条 assistant 消息的 markdown 里时才隐藏。回复里
  // 没带 URL 的纯生成则保留缩略图（那是它唯一的来源）。
  const shownImageUrls = useMemo(() => {
    const re = /https?:\/\/[^\s"'<>)]+?\.(?:png|jpe?g|webp|gif|mp4|webm|mov)(?:\?[^\s"'<>)]*)?/gi;
    const set = new Set<string>();
    for (const m of messages) {
      if (m.role !== 'assistant') continue;
      for (const b of m.blocks) {
        if (b.type === 'text' && b.text) {
          for (const u of b.text.match(re) ?? []) set.add(u);
        }
      }
    }
    return set;
  }, [messages]);

  const switchThread = async (id: string) => {
    selectThread(id);
    setThreadMenuOpen(false);
    await hydrateThread(id);
  };

  const createNewThread = async () => {
    try {
      const t = await createAgentThread(projectId, '新对话');
      setThreads([{ id: t.id, title: t.title, updatedAt: new Date().toISOString() }, ...threads]);
      selectThread(t.id);
      hydrateMessages(t.id, []);
    } finally {
      setThreadMenuOpen(false);
    }
  };

  const renameThread = async (id: string, newTitle: string) => {
    try {
      await updateAgentThread(projectId, id, newTitle);
      setThreads(threads.map((t) => (t.id === id ? { ...t, title: newTitle } : t)));
    } catch (err) {
      console.error('updateAgentThread failed', err);
    }
  };

  const removeThread = async (id: string) => {
    try {
      await deleteAgentThread(projectId, id);
      const remaining = threads.filter((t) => t.id !== id);
      setThreads(remaining);
      if (currentThreadId === id) {
        const next = remaining[0]?.id ?? null;
        if (next) {
          selectThread(next);
          await hydrateThread(next);
        } else {
          selectThread('' as unknown as string);
        }
      }
    } catch (err) {
      console.error('deleteAgentThread failed', err);
    }
  };

  // 把一个文件归类到我们的 chip 类型。
  const detectKind = (file: File): 'image' | 'video' | 'audio' | 'file' | null => {
    const t = file.type.toLowerCase();
    const n = file.name.toLowerCase();
    if (t.startsWith('image/')) return 'image';
    if (t.startsWith('video/')) return 'video';
    if (t.startsWith('audio/')) return 'audio';
    if (
      t === 'text/plain' ||
      t === 'text/markdown' ||
      n.endsWith('.txt') ||
      n.endsWith('.md')
    ) {
      return 'file';
    }
    return null;
  };

  const handleFileAttach = async (file: File) => {
    const kind = detectKind(file);
    if (!kind) return;
    setUploadingAttachment(true);
    try {
      let text: string | undefined;
      let charCount: number | undefined;
      if (kind === 'file') {
        // 分流（spec 2026-08-31）：≤100KB 全文内联进消息（不截断——本就在
        // 阈值内）；>100KB 不内联，chip 只留字数，发送时走 mentions[]，由
        // agent 用 read_attachment 按 artifact_id 分段读。>10MB 直接拒收。
        if (file.size > ATTACH_MAX_BYTES) {
          setSendError('文件超过 10MB，无法作为附件上传');
          return;
        }
        const raw = await file.text();
        if (raw.length > INLINE_TEXT_MAX) {
          charCount = raw.length;
        } else {
          text = raw;
        }
      }
      const asset = await uploadAsset(file, projectId);
      const artifactId = asset.id
        ? `asset_${asset.id}`
        : `att_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`;
      if (kind === 'file' && charCount != null && !asset.id) {
        // 大文件通道完全依赖服务端 asset id（read_attachment 按 asset_<id> 找
        // 对象）；拿不到 id 就没有任何路径能把内容交给 agent，明确报错优于
        // 静默发一个读不到的引用。
        setSendError('上传未返回素材 id，无法引用该大文件');
        return;
      }
      inputRef.current?.insertMention({
        artifact_id: artifactId,
        name: file.name,
        url: asset.url,
        kind,
        text,
        charCount,
      });
    } catch (err) {
      console.error('upload attachment failed', err);
    } finally {
      setUploadingAttachment(false);
    }
  };

  const onFilesChosen = async (list: FileList | null) => {
    if (!list) return;
    for (const f of Array.from(list)) {
      if (detectKind(f)) {
        // eslint-disable-next-line no-await-in-loop
        await handleFileAttach(f);
      }
    }
  };

  // skillOverride：交接单消费时 setSelectedSkill 与 send 在同一 tick，闭包里的
  // selectedSkill 还是旧值，只能显式传入。undefined = 照旧读 state。
  const send = async (skillOverride?: AgentSkillDTO | null) => {
    const skill = skillOverride === undefined ? selectedSkill : skillOverride;
    const raw = (inputRef.current?.getValue() ?? '').trim();
    if (!raw) return;
    if (sendingThreadId) return;

    // 直接从输入里取 chip 列表（带 kind/url/text），再
    // 剥掉 @[name](id) token 得到干净的纯文本。
    const richMentions = inputRef.current?.getMentions() ?? [];
    const { plainText: tokenStripped } = extractMentions(raw);
    // 把 kind='file'（文本附件）/ kind='text'（画布文字节点）chip 的正文内联
    // 进来——这些没有 image_url 语义，作为代码块追加（buildInlineSections，
    // 统一 50KB 上限）。文字节点的正文发送时从画布现读：chip 插入后用户可能
    // 还在编辑节点，chip 上存的快照只作节点已删除时的兜底。
    const findChipNode = (artifactId: string) =>
      canvasNodes.find((n) => {
        const meta = n.settings as { agentMeta?: { artifactId?: string } } | undefined;
        return meta?.agentMeta?.artifactId === artifactId || n.id === artifactId;
      });
    const textChips = richMentions
      .filter((m) => m.kind === 'file' || m.kind === 'text')
      .map((m) => {
        if (m.kind !== 'text') return m;
        const node = findChipNode(m.artifact_id);
        const fresh = (node?.settings as { text?: unknown } | undefined)?.text;
        return typeof fresh === 'string' && fresh ? { ...m, text: fresh } : m;
      });
    // 分流（spec 2026-08-31）：≤100KB 的正文内联；超阈值（含上传时就没带
    // text 的大文件 chip）改走 mentions[]，agent 用 read_attachment 分段读。
    // 文字节点的正文是上面从画布现读的，所以阈值判断必须放在现读之后。
    const { inline: inlineChips, oversize: oversizeChips } = splitTextChips(textChips);
    // 输入框直接粘贴的正文超阈值：禁止提交并提示转附件——粘贴文本没有 asset，
    // read_attachment 无处可读，内联又会把历史撑爆，两条路都不通。
    if (tokenStripped.length > INLINE_TEXT_MAX) {
      setSendError('输入内容过长，请将长文本保存为 .txt/.md 文件后作为附件上传');
      return;
    }
    const plainText = tokenStripped + buildInlineSections(inlineChips);
    // 媒体 chip（image/video/audio）放进 mentions[] payload，这样 agent 能把它们
    // 作为多模态部件附上（kind 让后端分流：视频/音频转文本引用而不是挂 image
    // part）。对于 canvas-node 类型的 mention，我们仍然去 canvasNodes 里
    // 查最新的 URL（chip 插入之后 URL 可能已经重新签名过）。
    const finalMentions: Mention[] = richMentions
      .filter((m) => m.kind !== 'file' && m.kind !== 'text')
      .map((m) => {
        const node = findChipNode(m.artifact_id);
        const url =
          (typeof node?.content === 'string' ? node.content : '') || m.url || '';
        const kind: Mention['kind'] =
          m.kind === 'video' || m.kind === 'audio' ? m.kind : 'image';
        return { artifact_id: m.artifact_id, name: m.name, url, kind };
      });
    for (const m of oversizeChips) {
      // 文字节点：artifact_id 必须是画布节点 id（服务端按快照 node.id 找正文），
      // chip 上存的可能是 agentMeta.artifactId，用 findChipNode 还原真节点 id。
      const node = m.kind === 'text' ? findChipNode(m.artifact_id) : undefined;
      finalMentions.push({
        artifact_id: node?.id ?? m.artifact_id,
        name: m.name,
        kind: m.kind as Mention['kind'],
        char_count: m.text ? m.text.length : m.charCount ?? 0,
      });
    }

    let threadId = currentThreadId;
    if (!threadId) {
      const t = await createAgentThread(projectId, plainText.slice(0, 20) || '新对话');
      threadId = t.id;
      setThreads([{ id: t.id, title: t.title, updatedAt: new Date().toISOString() }, ...threads]);
      selectThread(t.id);
    }
    const actionId = nanoid();
    // 【M2】先确认真的发出去，再做清输入框 / append 气泡这些副作用。WS 非 OPEN
    // 时 sendUserMessage 返回 false——消息根本没上网，此时若照旧清空输入，用户
    // 输入就进了黑洞（本地气泡随后还会被看门狗 hydrate 抹掉、零报错）。失败时
    // 原样保留输入并给出可见提示，等连接恢复（M3 慢速重连兜底）再重试。
    const sent = clientRef.current?.sendUserMessage({
      thread_id: threadId,
      action_id: actionId,
      content: plainText,
      mentions: finalMentions,
      skill_id: skill?.id,
    }) === true;
    if (!sent) {
      setSendError('连接已断开，消息未发送；请等待连接恢复后重试');
      return;
    }
    setSendError(null);
    inputRef.current?.setValue('');
    setInputEmpty(true);
    setMentionActive(null);
    setSendingThreadId(threadId);
    sendingActionIdRef.current = actionId;
    lastFrameAtRef.current = Date.now(); // 看门狗从发送时刻起计静默
    // 【H4】新一轮已经开始：推进世代，作废所有在途的旧 hydrate 响应——它们晚到
    // 时不能整表覆盖，否则会抹掉下面这条 user 气泡和服务端即将建立的 pending 占位。
    bumpHydrateEpoch(threadId);
    const userBlocks: ContentBlock[] = [];
    if (skill) {
      userBlocks.push({ type: 'skill', id: skill.id, name: skill.name });
    }
    userBlocks.push({ type: 'text', text: plainText });
    // 镜像 richMentions 用于本地渲染——保留 kind，这样气泡能挑对缩略图
    //（image / video / audio）。
    for (const m of richMentions) {
      if (m.kind === 'file') continue;
      if (!m.url) continue;
      const blockType: 'image' | 'video' | 'audio' =
        m.kind === 'video' ? 'video' : m.kind === 'audio' ? 'audio' : 'image';
      userBlocks.push({ type: blockType, url: m.url, name: m.name });
    }
    appendUserMessage({
      id: `u_${actionId}`,
      thread_id: threadId,
      action_id: actionId,
      role: 'user',
      blocks: userBlocks,
    });
    // skill 是 per-message 的——发送后清掉它，让 chip 不再悬在输入框上方。
    // 它现在归属于已发送的那条消息气泡里。
    setSelectedSkill(null);
  };

  // 消费首页「故事灵感」入口的交接单：WS 已通且本项目 threads 已拉回才动手
  //（原因见 shouldConsumeHandoff）。take() 取一次即清，StrictMode 双跑也只发一条。
  useEffect(() => {
    if (!shouldConsumeHandoff(connectionState, threadsLoadedFor, projectId)) return;
    const h = useAgentHandoff.getState().take(projectId);
    if (!h) return;
    const input = inputRef.current;
    if (!input) return;
    const content = handoffContent(h);
    // 顺序不能反：setValue 的 renderInto 会清空 DOM、按 token 重建 chip 时丢掉
    // url/kind，先插 chip 再 setValue 等于把附件废掉。focus 要在 insertMention 之
    // 前：它按当前 selection 定位插入点，焦点若还在页面别处，chip 会插到编辑器外。
    input.setValue(content);
    input.focus();
    for (const m of h.mentions) input.insertMention(m);
    setSelectedSkill(h.skill);
    if (!content) return; // 无字无 skill：只把附件放进输入框，交给用户补一句
    void send(h.skill);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectionState, threadsLoadedFor, projectId]);

  return (
    <div
      className="chat-panel fixed top-0 right-0 bottom-0 z-[900] bg-card border-l border-border flex flex-col shadow-2xl"
      style={{
        width,
        transform: open ? 'translateX(0)' : 'translateX(100%)',
        // 拖拽时禁掉 transform 过渡，让宽度变化感觉跟手。
        // 滑入动画只在 open/close 时触发。
        transition: 'transform 200ms ease',
      }}
      onMouseDown={(e) => e.stopPropagation()}
      onWheel={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.stopPropagation()}
    >
      {/* 左边缘的调整大小手柄。横跨面板的左边框，这样从画布那侧也能发现。
          一直显示一条淡淡的竖线，hover 时高亮。min/max 已写死（380–720px）。 */}
      <div
        onMouseDown={startResize}
        className="absolute top-0 bottom-0 cursor-col-resize z-[1000] group"
        style={{ left: -4, width: 10 }}
        title="拖动调整宽度"
      >
        <div className="absolute left-1/2 -translate-x-1/2 top-0 bottom-0 w-[2px] bg-border group-hover:bg-blue-500 transition-colors" />
      </div>
      {/* 头部 */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
        <Bot size={14} className="text-blue-400 shrink-0" />
        <div className="flex-1 min-w-0 relative">
          <button
            className="flex items-center gap-1.5 w-full px-2 py-1 text-xs text-foreground bg-popover hover:bg-accent rounded border border-input cursor-pointer"
            onClick={() => setThreadMenuOpen((v) => !v)}
            title={currentThread?.title ?? '新对话'}
          >
            <span className="truncate flex-1 text-left">
              {currentThread?.title ?? '新对话'}
            </span>
            <ChevronDown size={12} className="text-muted-foreground shrink-0" />
          </button>
          {threadMenuOpen && (
            <>
              <div className="fixed inset-0 z-10" onClick={() => setThreadMenuOpen(false)} />
              <div className="absolute left-0 right-0 top-full mt-1 bg-popover border border-input rounded shadow-xl z-20 max-h-80 overflow-auto">
                <button
                  className="w-full flex items-center gap-2 px-3 py-2 text-xs text-blue-400 bg-transparent hover:bg-popover border-none border-b border-b-border text-left cursor-pointer"
                  onClick={() => void createNewThread()}
                >
                  <Plus size={12} />
                  新建对话
                </button>
                {threads.length === 0 && (
                  <div className="px-3 py-3 text-[11px] text-muted-foreground text-center">还没有对话</div>
                )}
                {threads.map((t) => (
                  <ThreadRow
                    key={t.id}
                    id={t.id}
                    title={t.title}
                    active={t.id === currentThreadId}
                    onSelect={() => void switchThread(t.id)}
                    onRename={(newTitle) => void renameThread(t.id, newTitle)}
                    onDelete={() => {
                      if (confirm(`确定删除会话「${t.title || '新对话'}」吗？`)) void removeThread(t.id);
                    }}
                  />
                ))}
              </div>
            </>
          )}
        </div>
        <span className="text-[10px] text-muted-foreground shrink-0">{connectionState}</span>
        <button
          className="text-muted-foreground hover:text-foreground transition-colors cursor-pointer bg-transparent border-none p-0"
          onClick={onClose}
          title="关闭"
        >
          <X size={16} />
        </button>
      </div>

      {/* 消息区 */}
      <div
        ref={messagesScrollerRef}
        className="flex-1 overflow-auto p-3 flex flex-col gap-3 custom-scrollbar"
      >
        {/* 懒加载哨兵：还有更早历史时挂在列表最顶。滚近它就自动拉上一页；
            没有更早的（含到顶）时整个卸载，安静停止（设计拍板 D3：不加
            「没有更早消息」提示）。 */}
        {hasMoreOlder && (
          <div ref={topSentinelRef} className="shrink-0 flex items-center justify-center py-1">
            {currentPageState?.loadingOlder && (
              <Loader2 size={12} className="animate-spin text-muted-foreground" />
            )}
          </div>
        )}
        {messages.length === 0 && (
          <div className="flex-1 flex flex-col items-center justify-center text-muted-foreground text-xs gap-2 text-center px-8">
            <Sparkles size={28} className="text-muted-foreground" />
            <div>还没有对话，跟 agent 说点啥就开始啦</div>
          </div>
        )}
        {messages.map((m) => (
          <MessageBubble key={m.id} msg={m} dedupeUrls={shownImageUrls} />
        ))}
        {/* 「思考中」只在 sending 的那个 thread 正被展示时出现——用户切去看别的
            thread 时不该在无关会话里转圈（L7：sending 与 thread 绑定）。 */}
        {sendingThreadId === currentThreadId && sendingThreadId !== null && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 size={12} className="animate-spin" />
            正在思考…
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* 已选中的 skill chip */}
      {selectedSkill && (
        <div className="flex items-center gap-2 px-3 py-1.5 border-t border-border bg-popover/60 shrink-0">
          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] text-blue-100 bg-blue-950/60 border border-blue-700/50">
            <Zap size={11} />
            {selectedSkill.name}
            <button
              type="button"
              onClick={() => setSelectedSkill(null)}
              className="text-blue-200/70 hover:text-white bg-transparent border-none p-0 cursor-pointer ml-0.5"
              title="移除技能"
            >
              <X size={10} />
            </button>
          </span>
        </div>
      )}

      {/* （附件预览行已移除——上传的文件现在作为原子 @ chip 直接插到
          MentionInput 的光标处。） */}

      {/* 输入 + 控件（Lovart 风格的堆叠布局——输入在上、操作栏在下，
          都放在同一个圆角卡片里）。 */}
      <div className="border-t border-border px-3 py-2 shrink-0 relative">
        {/* 【M2】发送失败（WS 未连通）的内联报错。连接恢复或下次发送成功时清除。 */}
        {sendError && (
          <div className="px-1 pb-1.5 text-[11px] text-red-400">{sendError}</div>
        )}
        <div className="bg-background border border-input rounded-xl focus-within:border-blue-500 transition-colors">
          <MentionInput
            ref={inputRef}
            placeholder='跟 agent 说点啥… 输入 "@" 引用画布节点'
            className="!border-none !bg-transparent !rounded-none !min-h-[64px] !max-h-40 !p-3"
            onChange={(value, active) => {
              setInputEmpty(value.trim().length === 0);
              setMentionActive(active);
            }}
            onPasteImage={(f) => void handleFileAttach(f)}
          />
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,video/*,audio/*,.txt,.md,text/plain,text/markdown"
            className="hidden"
            onChange={(e) => {
              void onFilesChosen(e.target.files);
              e.target.value = '';
            }}
          />
          {/* 同一卡片内的操作栏。左：+（附件）+ ⚡ skill；右：发送。 */}
          <div className="flex items-center justify-between gap-2 px-2 pb-2 pt-1">
            <div className="flex items-center gap-1">
              <button
                className="w-8 h-8 flex items-center justify-center text-sidebar-foreground hover:text-blue-400 rounded-full hover:bg-accent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed bg-transparent border-none"
                onClick={() => fileInputRef.current?.click()}
                disabled={uploadingAttachment}
                title="添加图片"
              >
                {uploadingAttachment ? <Loader2 size={14} className="animate-spin" /> : <Plus size={16} />}
              </button>
              <button
                className="w-8 h-8 flex items-center justify-center text-sidebar-foreground hover:text-blue-400 rounded-full hover:bg-accent cursor-pointer bg-transparent border-none"
                onClick={() => setSkillOpen((v) => !v)}
                title="选择 skill"
              >
                <Zap size={14} />
              </button>
            </div>
            {sendingThreadId !== null ? (
              <button
                className="w-9 h-9 flex items-center justify-center text-white bg-zinc-600 hover:bg-red-600 rounded-full border-none cursor-pointer transition-colors"
                onClick={() => {
                  // 停止 = 掐掉本轮 agent 的思考循环。已消耗的 LLM 轮次照常结算、
                  // 已提交的生成任务继续跑完照常扣费（用户拍板的计费边界）。
                  if (sendingThreadId && sendingActionIdRef.current) {
                    clientRef.current?.abort(sendingThreadId, sendingActionIdRef.current);
                  }
                }}
                title="停止生成（已提交的任务会继续执行并正常扣费）"
              >
                <Square size={12} fill="currentColor" />
              </button>
            ) : (
              <button
                className="w-9 h-9 flex items-center justify-center text-white bg-blue-600 hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed rounded-full border-none cursor-pointer transition-colors"
                onClick={() => void send()}
                disabled={inputEmpty}
                title="发送"
              >
                <Send size={15} />
              </button>
            )}
          </div>
        </div>
        {mentionActive && (
          <MentionPicker
            query={mentionActive.query}
            nodes={buildCanvasSummary(canvasNodes)}
            onClose={() => setMentionActive(null)}
            onPick={(node) => {
              const fullNode = canvasNodes.find((n) => n.id === node.id);
              const settings = fullNode?.settings as
                | { agentMeta?: { artifactId?: string } }
                | undefined;
              const artifactId = settings?.agentMeta?.artifactId ?? node.id;
              inputRef.current?.insertMention({
                artifact_id: artifactId,
                name: node.name || node.id,
                url: node.url,
                kind: node.kind ?? 'image',
                // 文字节点把正文快照挂到 chip 上——发送时优先从画布现读，
                // 这份快照只作节点已删除时的兜底（见 send() 的 inlineChips）。
                text: node.kind === 'text' ? node.text : undefined,
              });
              setMentionActive(null);
            }}
          />
        )}
      </div>
      <SkillPopover
        open={skillOpen}
        onClose={() => setSkillOpen(false)}
        onSelect={setSelectedSkill}
      />
      <SkillCreateModal
        draft={pendingSkillDraft}
        onClose={clearPendingSkillDraft}
        onSaved={clearPendingSkillDraft}
      />
    </div>
  );
}

function ThreadRow({
  id,
  title,
  active,
  onSelect,
  onRename,
  onDelete,
}: {
  id: string;
  title: string;
  active: boolean;
  onSelect: () => void;
  onRename: (newTitle: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title);
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
            if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
              onRename(draft.trim() || title);
              setEditing(false);
            }
            if (e.key === 'Escape') {
              setDraft(title);
              setEditing(false);
            }
          }}
          onBlur={() => {
            onRename(draft.trim() || title);
            setEditing(false);
          }}
          className="flex-1 bg-background border border-input rounded px-1.5 py-0.5 text-xs outline-none"
        />
      ) : (
        <span className="truncate flex-1" title={title}>{title || '新对话'}</span>
      )}
      <button
        className="text-muted-foreground hover:text-blue-400 opacity-0 group-hover:opacity-100 shrink-0 bg-transparent border-none p-0.5 cursor-pointer"
        onClick={(e) => {
          e.stopPropagation();
          setDraft(title);
          setEditing(true);
        }}
        title="重命名"
      >
        <Pencil size={11} />
      </button>
      <button
        className="text-muted-foreground hover:text-red-400 opacity-0 group-hover:opacity-100 shrink-0 bg-transparent border-none p-0.5 cursor-pointer"
        onClick={(e) => {
          e.stopPropagation();
          onDelete();
        }}
        title="删除"
      >
        <Trash2 size={11} />
      </button>
      <span className="sr-only">{id}</span>
    </div>
  );
}

function dtoToAgentMessage(dto: AgentMessageDTO): AgentMessage {
  const blocks = (Array.isArray(dto.content_blocks) ? dto.content_blocks : []) as ContentBlock[];
  return {
    id: dto.id,
    thread_id: dto.thread_id,
    action_id: dto.action_id,
    // createdAt 是 hydrateTail 前缀切分与翻页游标的排序键，服务端消息必须带上；
    // 本地乐观消息不带（视为最新），见 agentStore 的 isOrderedBefore。
    createdAt: dto.created_at,
    role: dto.role,
    blocks,
  };
}
