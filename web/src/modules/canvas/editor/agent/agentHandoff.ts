// 首页「故事灵感」入口 → 画布 Agent 面板的一次性交接单。
//
// 首页发送那一下已经把项目建好、附件传完，剩下的「打开面板 + 发出第一条消息」
// 必须由 AgentPanel 自己做——发送要走它的 send()（建 thread / 乐观气泡 / skill
// 清理），首页够不到 WS 也不该复制那套逻辑。所以交接的是「意图」不是「动作」。
//
// 为什么是内存而不是 URL / sessionStorage：
// - URL 会把整段提示词留在历史记录里，后退再前进就重发一遍；
// - sessionStorage 能扛刷新，但刷新丢掉的只是文字（附件已上传进项目），
//   为这个边界加持久化 + 过期清理不值。
// take() 取一次即清，保证同一张单永远只发一条消息。
import { create } from 'zustand';
import type { AgentSkillDTO } from '@/shared/api';
import type { MentionPick } from './MentionInput';

export interface AgentHandoff {
  projectId: number;
  /** 纯文本提示词，可为空（只选了 skill）。 */
  text: string;
  skill: AgentSkillDTO | null;
  /** 已上传到该项目的附件，字段与 MentionInput.insertMention 入参同构。 */
  mentions: MentionPick[];
}

interface HandoffState {
  pending: AgentHandoff | null;
  set: (h: AgentHandoff) => void;
  /** 只看不取——CanvasEditorPage 用它决定要不要在挂载时直接打开面板。 */
  peek: (projectId: number) => AgentHandoff | null;
  /** 取走并清空。projectId 对不上返回 null 且不清（单是别的项目的）。 */
  take: (projectId: number) => AgentHandoff | null;
}

export const useAgentHandoff = create<HandoffState>((set, get) => ({
  pending: null,
  set: (h) => set({ pending: h }),
  peek: (projectId) => {
    const p = get().pending;
    return p && p.projectId === projectId ? p : null;
  },
  take: (projectId) => {
    const p = get().pending;
    if (!p || p.projectId !== projectId) return null;
    set({ pending: null });
    return p;
  },
}));

/** 只选 skill 没写字时，发出去的消息正文——空 content 不发（send() 会直接返回），
 *  又不能替用户编需求，就明确告诉 agent「用这个技能开始」。 */
export function handoffContent(h: Pick<AgentHandoff, 'text' | 'skill'>): string {
  const t = h.text.trim();
  if (t) return t;
  return h.skill ? `请使用「${h.skill.name}」技能开始创作` : '';
}

/** 面板是否已就绪到可以消费交接单。两个条件缺一不可：WS 未 OPEN 时
 *  sendUserMessage 直接返回 false（消息进黑洞）；threads 未拉回时 send() 新建的
 *  thread 会被晚到的 setThreads([]) 抹掉（见 AgentPanel 的 threadsLoadedFor）。 */
export function shouldConsumeHandoff(
  connectionState: string,
  threadsLoadedFor: number | null,
  projectId: number,
): boolean {
  return connectionState === 'open' && threadsLoadedFor === projectId;
}
