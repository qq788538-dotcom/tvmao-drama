import { create } from 'zustand';

import type { Artifact, ContentBlock, Frame, SkillDraft } from './types';

export interface WorkflowStepState {
  name: string;
  status: 'running' | 'done' | 'error';
}

export interface AgentThread {
  id: string;
  title: string;
  updatedAt: string;
}

export interface AgentMessage {
  id: string;
  thread_id: string;
  action_id: string;
  role: 'user' | 'assistant' | 'tool';
  blocks: ContentBlock[];
  /** 服务端持久化时间（DTO 的 created_at 原样透传）。本地乐观消息（user 气泡、
   *  pending 占位、err_*）没有它——排序上视为「最新」，hydrateTail 合并时天然
   *  落在 tail 覆盖区而不是保留前缀里。 */
  createdAt?: string;
  pending?: boolean;
  workflowSteps?: WorkflowStepState[];
}

/** 每个 thread 的懒加载翻页状态。缺省（undefined）等价 hasMore=false。 */
export interface ThreadPageState {
  hasMore: boolean;
  loadingOlder: boolean;
}

type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error';

interface ArtifactCtx {
  thread_id: string;
  action_id: string;
  artifact: Artifact;
}
type ArtifactListener = (ctx: ArtifactCtx) => void;

interface AgentStoreState {
  threads: AgentThread[];
  currentThreadId: string | null;
  currentProjectId: number | null;
  messages: Record<string, AgentMessage[]>;
  pageState: Record<string, ThreadPageState>;
  connectionState: ConnectionState;
  artifactListener: ArtifactListener | null;
  pendingSkillDraft: SkillDraft | null;

  setThreads: (ts: AgentThread[]) => void;
  selectThread: (id: string | null) => void;
  setCurrentProjectId: (id: number | null) => void;
  setConnectionState: (s: ConnectionState) => void;
  setArtifactListener: (fn: ArtifactListener) => void;
  appendUserMessage: (m: AgentMessage) => void;
  hydrateMessages: (threadId: string, msgs: AgentMessage[]) => void;
  hydrateTail: (threadId: string, tail: AgentMessage[], hasMore: boolean) => void;
  prependOlderMessages: (threadId: string, older: AgentMessage[], hasMore: boolean) => void;
  setLoadingOlder: (threadId: string, loading: boolean) => void;
  handleFrame: (f: Frame) => void;
  clearPendingSkillDraft: () => void;
}

/**
 * 把 RFC3339 时间戳规范成可字典序比较的排序键。裸字符串比较是错的：Go 的
 * time.Time JSON（RFC3339Nano）会裁掉小数部分的尾零，于是 "…05Z" 与
 * "…05.123Z" 比较时 'Z' > '.'，时间上更早的反而排后（审查 F3，已复现证实）。
 * 这里拆成「Date.parse 的毫秒（15 位零填充，跨时区偏移格式也对齐）+ 补齐到
 * 9 位的小数串（保住微秒）」。解析失败原样返回（测试里的合成短串走这条，
 * 退化为普通字典序）。
 */
export function timestampSortKey(s: string): string {
  const ms = Date.parse(s);
  if (Number.isNaN(ms)) return s;
  const frac = (/\.(\d+)/.exec(s)?.[1] ?? '').padEnd(9, '0');
  return `${String(ms).padStart(15, '0')}.${frac}`;
}

/**
 * (createdAt, id) 双键序上 a 是否严格早于 b——与服务端
 * `ORDER BY created_at ASC, id ASC` 完全同构，这是 hydrateTail 前缀切分正确性
 * 的根基。时间比较走 timestampSortKey（见其注释：裸字符串比较有尾零裁剪坑）。
 * 没有 createdAt 的本地乐观消息视为「最新」：永远不早于任何服务端消息。
 */
export function isOrderedBefore(a: AgentMessage, b: AgentMessage): boolean {
  if (!a.createdAt) return false;
  if (!b.createdAt) return true;
  const ka = timestampSortKey(a.createdAt);
  const kb = timestampSortKey(b.createdAt);
  if (ka !== kb) return ka < kb;
  return a.id < b.id;
}

export const useAgentStore = create<AgentStoreState>((set, get) => ({
  threads: [],
  currentThreadId: null,
  currentProjectId: null,
  messages: {},
  pageState: {},
  connectionState: 'idle',
  artifactListener: null,
  pendingSkillDraft: null,

  setThreads: (threads) => set({ threads }),
  selectThread: (id) => set({ currentThreadId: id }),
  setCurrentProjectId: (id) => set({ currentProjectId: id }),
  setConnectionState: (s) => set({ connectionState: s }),
  setArtifactListener: (fn) => set({ artifactListener: fn }),
  clearPendingSkillDraft: () => set({ pendingSkillDraft: null }),

  appendUserMessage: (m) =>
    set((state) => {
      const list = state.messages[m.thread_id] ?? [];
      return { messages: { ...state.messages, [m.thread_id]: [...list, m] } };
    }),

  // hydrateMessages 是老的「整表覆盖」，现在只剩 createNewThread 清空新 thread
  // 一个调用方；对服务器状态的对齐一律走下面的 hydrateTail。
  hydrateMessages: (threadId, msgs) =>
    set((state) => ({
      messages: { ...state.messages, [threadId]: msgs },
      pageState: { ...state.pageState, [threadId]: { hasMore: false, loadingOlder: false } },
    })),

  // hydrateTail 把 thread 对齐到服务器最新一页，同时**保留**用户已上拉加载的
  // 更早窗口：新列表 = [现有列表中严格早于 tail[0] 的消息] ++ tail。
  //  - 已加载的旧页落在保留前缀里——每轮对话完成后的对账 hydrate 不再把它们
  //    抹掉（设计文档 R1，滚动跳变的根源）；
  //  - 本地乐观消息（无 createdAt）不早于任何服务端消息 → 不落前缀 → 被 tail
  //    覆盖丢弃，与旧整表覆盖对它们的处置一致（【H4】保证不会误伤在途新轮次）；
  //  - tail 为空（空 thread）→ 整表清空，与旧行为一致。
  // hasMore 语义是「tail[0] 之前是否还有」；若保留了前缀，我们窗口的最早端比
  // tail[0] 更早，此时沿用已有的 hasMore——翻页游标始终取自窗口最早那条，
  // 就算这里放宽了也只是多一次返回空页的请求，自我纠正。
  hydrateTail: (threadId, tail, hasMore) =>
    set((state) => {
      const existing = state.messages[threadId] ?? [];
      let list: AgentMessage[];
      let nextHasMore = hasMore;
      if (tail.length === 0) {
        list = [];
      } else {
        const head = tail[0];
        const tailIds = new Set(tail.map((m) => m.id));
        // 邻接性判定（审查 F2，已被独立验证构造出反例）：只有当 tail 与现有
        // 窗口按 id 有交集时，才能证明两段之间没有缺行——一次 Run 最多落
        // 1+2×20=41 行（超过一页 30），看门狗/断线/另一标签页推进都可能让
        // 服务端多出整页以上的新行；此时 tail 与旧窗口中间隔着一段谁也没
        // 加载的消息，硬拼会造成永远补不上的隐形空洞（翻页游标在洞更早侧，
        // 只会往更早翻）。无交集且窗口里确有服务端消息 → 整窗重置为 tail，
        // 宁可丢缓存窗口也不留洞。
        const overlaps = existing.some((m) => tailIds.has(m.id));
        const hadServerRows = existing.some((m) => m.createdAt);
        if (!overlaps && hadServerRows) {
          list = tail;
        } else {
          // tailIds 去重是防御极端脏数据（时钟回拨导致双键切分失效）的兜底，
          // 正常路径上前缀与 tail 无交集。
          const prefix = existing.filter((m) => isOrderedBefore(m, head) && !tailIds.has(m.id));
          list = [...prefix, ...tail];
          if (prefix.length > 0) {
            nextHasMore = state.pageState[threadId]?.hasMore ?? true;
          }
        }
      }
      list = keepLocalErrors(existing, list);
      return {
        messages: { ...state.messages, [threadId]: list },
        pageState: { ...state.pageState, [threadId]: { hasMore: nextHasMore, loadingOlder: false } },
      };
    }),

  prependOlderMessages: (threadId, older, hasMore) =>
    set((state) => {
      const existing = state.messages[threadId] ?? [];
      const ids = new Set(existing.map((m) => m.id));
      const fresh = older.filter((m) => !ids.has(m.id));
      return {
        messages: { ...state.messages, [threadId]: [...fresh, ...existing] },
        pageState: { ...state.pageState, [threadId]: { hasMore, loadingOlder: false } },
      };
    }),

  setLoadingOlder: (threadId, loading) =>
    set((state) => ({
      pageState: {
        ...state.pageState,
        [threadId]: {
          hasMore: state.pageState[threadId]?.hasMore ?? false,
          loadingOlder: loading,
        },
      },
    })),

  handleFrame: (f) => {
    const currentProjectId = get().currentProjectId;
    if (f.project_id && currentProjectId && f.project_id !== currentProjectId) return;
    const tid = f.thread_id;
    if (!tid) return;
    const state = get();
    const list = state.messages[tid] ?? [];

    if (f.type === 'action_start' && f.action_id) {
      const newAssistant: AgentMessage = {
        id: `pending_${f.action_id}`,
        thread_id: tid,
        action_id: f.action_id,
        role: 'assistant',
        blocks: [],
        pending: true,
      };
      set({ messages: { ...state.messages, [tid]: [...list, newAssistant] } });
      return;
    }

    if (f.type === 'content_block_start' && f.block) {
      const target = lastPendingAssistant(list);
      if (target) {
        const idx = f.index ?? target.blocks.length;
        const blocks = padBlocksTo([...target.blocks], idx);
        blocks[idx] = { ...f.block };
        replaceLastPendingAssistant(state, set, tid, list, { ...target, blocks });
      }

      if (f.block.type === 'tool_event' && f.block.artifacts) {
        for (const a of f.block.artifacts) {
          if (a.artifact_type === 'skill_definition' && a.skill) {
            set({ pendingSkillDraft: a.skill });
            continue;
          }
          if (state.artifactListener) {
            state.artifactListener({ thread_id: tid, action_id: f.action_id ?? '', artifact: a });
          }
        }
      }
      return;
    }

    if (f.type === 'step_start') {
      const target = lastPendingAssistant(list);
      if (!target) return;
      const stepName = f.block?.text ?? '';
      const steps: WorkflowStepState[] = [
        ...(target.workflowSteps ?? []),
        { name: stepName, status: 'running' },
      ];
      replaceLastPendingAssistant(state, set, tid, list, { ...target, workflowSteps: steps });
      return;
    }

    if (f.type === 'step_end') {
      const target = lastPendingAssistant(list);
      if (!target || !target.workflowSteps) return;
      const steps = target.workflowSteps.slice();
      for (let i = steps.length - 1; i >= 0; i--) {
        if (steps[i].status === 'running') {
          steps[i] = {
            ...steps[i],
            status: f.stop_reason === 'ok' ? 'done' : 'error',
          };
          break;
        }
      }
      replaceLastPendingAssistant(state, set, tid, list, { ...target, workflowSteps: steps });
      return;
    }

    if (f.type === 'content_block_delta' && f.delta) {
      const target = lastPendingAssistant(list);
      if (!target) return;
      const idx = f.index ?? 0;
      const blocks = padBlocksTo([...target.blocks], idx);
      const cur = blocks[idx] ?? { type: 'text' as const };
      if (f.delta.type === 'text_delta') {
        blocks[idx] = { ...cur, text: (cur.text ?? '') + (f.delta.text ?? '') };
      } else if (f.delta.type === 'input_json_delta') {
        const prev = typeof cur.input === 'string' ? cur.input : '';
        blocks[idx] = { ...cur, input: prev + (f.delta.partial_json ?? '') };
      }
      replaceLastPendingAssistant(state, set, tid, list, { ...target, blocks });
      return;
    }

    if (f.type === 'action_end') {
      const target = lastPendingAssistant(list);
      if (target) replaceLastPendingAssistant(state, set, tid, list, { ...target, pending: false });
      return;
    }

    if (f.type === 'error' && f.error) {
      // 收到 error 帧时，把仍 pending 的那条 assistant 翻成非 pending——否则它会
      // 一直停在「思考中」状态。后端现在 error 后还会跟一条 action_end（sendFatal），
      // 但 error 帧先到，这里就先解掉，不依赖后续帧一定送达。
      const pendingTarget = lastPendingAssistant(list);
      const cleared = pendingTarget
        ? list.map((m) => (m === pendingTarget ? { ...m, pending: false } : m))
        : list;
      const errMsg: AgentMessage = {
        id: `err_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
        thread_id: tid,
        action_id: f.action_id ?? '',
        role: 'assistant',
        blocks: [
          {
            type: 'text',
            // 用户点停止是主动行为不是故障——aborted 渲染成中性提示，
            // 不带「错误」字样（后端 sendFatal 对 ctx 取消固定发这个 code）。
            text:
              f.error.code === 'aborted'
                ? '已停止生成。'
                : `[错误 ${f.error.code}] ${f.error.message}`,
          },
        ],
      };
      set({ messages: { ...state.messages, [tid]: [...cleared, errMsg] } });
    }
  },
}));

// padBlocksTo 把 blocks 补齐到能安全写入 idx 的长度，洞位填空 text block。
// 流式帧的 index 现在是整个 action 内全局递增（后端 018a35bf 起，多轮不再从 0
// 重算），而同一 action 的所有轮共用一条 pending 气泡——若某些序号从未以
// content_block_start 送达本气泡（断线窗口丢帧、或该序号属于不入流的内部块），
// 直接 `blocks[idx]=…` 会产出 sparse array，MessageBubble 的 blocks.map 遇到
// undefined 就崩。空 text block 渲染为空、hasVisible 判 false，语义上等于
// 「这个序号没内容」，且不破坏「index 即数组下标」的现有对位关系。
function padBlocksTo(blocks: ContentBlock[], idx: number): ContentBlock[] {
  for (let i = 0; i < idx; i++) {
    if (blocks[i] === undefined) blocks[i] = { type: 'text', text: '' };
  }
  return blocks;
}

// keepLocalErrors 把 error 帧生成的本地错误气泡（err_*，服务端不落库）重新插回
// hydrate 后的列表：放在同 action 最后一条服务端消息之后。轮次以错误收尾时完成
// 对账必然会 hydrate，不保留的话错误提示连同上下文「闪一下就没了」。同 action
// 在新列表里已无任何消息（如被压缩）则丢弃，避免孤立气泡。
function keepLocalErrors(existing: AgentMessage[], list: AgentMessage[]): AgentMessage[] {
  const errs = existing.filter((m) => m.id.startsWith('err_') && m.action_id);
  if (errs.length === 0) return list;
  const out = list.slice();
  for (const e of errs) {
    if (out.some((m) => m.id === e.id)) continue;
    let at = -1;
    for (let i = out.length - 1; i >= 0; i--) {
      if (out[i].action_id === e.action_id) { at = i; break; }
    }
    if (at >= 0) out.splice(at + 1, 0, e);
  }
  return out;
}

function lastPendingAssistant(list: AgentMessage[]): AgentMessage | undefined {
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].role === 'assistant' && list[i].pending) return list[i];
  }
  return undefined;
}

function replaceLastPendingAssistant(
  state: AgentStoreState,
  set: (partial: Partial<AgentStoreState>) => void,
  tid: string,
  list: AgentMessage[],
  next: AgentMessage,
) {
  const newList = list.slice();
  for (let i = newList.length - 1; i >= 0; i--) {
    if (newList[i].role === 'assistant' && newList[i].pending) {
      newList[i] = next;
      break;
    }
  }
  set({ messages: { ...state.messages, [tid]: newList } });
}
