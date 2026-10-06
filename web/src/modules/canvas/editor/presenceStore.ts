// 协作在场信息（在线成员名册 + 谁正在编辑哪个节点）的前端状态。
//
// 为什么不塞进 canvas store：它不是画布数据——不进快照、不进 undo 栈、不落库、
// 断线即失。混进 canvas store 的话，getSnapshot()/load() 这类「画布内容」的
// 出入口就要处处记得把它排除掉。单独一个小 store，画布那边一行不用改。
//
// 帧契约见 apps/api/internal/agent/envelope.go：
//   canvas_presence: { members: [{actor_user_id, email}] }（全量重发，不是增量）
//   canvas_transient(kind=node_editing): { actor_user_id, node_id, editing }
import { create } from 'zustand';

export interface PresenceMember {
  actorUserId: number;
  /** 后端查不到用户时会是空串（名册不该因为查不到显示名就少一个人）。 */
  email: string;
}

/**
 * 「正在编辑」的存活时间。发送端每 3s 续一帧（useNodeEditingBroadcast），
 * 5s 收不到续帧就判定对方已经走了——**必须有这条超时**：编辑态的退出帧
 * （editing:false）走的是同一条会断的 WS，对方崩溃/断网/直接关标签页时那一帧
 * 永远不会到，角标就会永久挂在节点上，指着一个早就不在的人。
 */
export const NODE_EDITING_TTL_MS = 5_000;

export interface RemoteNodeEditor {
  actorUserId: number;
}

interface PresenceState {
  members: PresenceMember[];
  /** nodeId → 正在编辑它的**远端**成员。本端自己的编辑不进这里（角标是给
   *  「别人在动这个节点」用的，提示自己毫无意义）。 */
  editors: Record<string, RemoteNodeEditor>;
  setMembers: (members: PresenceMember[]) => void;
  noteNodeEditing: (nodeId: string, actorUserId: number, editing: boolean) => void;
  reset: () => void;
}

// TTL 定时器挂在模块级而不是 state 里：它是资源句柄，进 state 只会让每次续帧
// 都产生一次无意义的订阅通知（句柄变了但画面没变）。
const expiryTimers = new Map<string, ReturnType<typeof setTimeout>>();

function clearExpiry(nodeId: string): void {
  const timer = expiryTimers.get(nodeId);
  if (timer === undefined) return;
  clearTimeout(timer);
  expiryTimers.delete(nodeId);
}

export const useCanvasPresenceStore = create<PresenceState>((set) => ({
  members: [],
  editors: {},

  setMembers: (members) => set({ members }),

  noteNodeEditing: (nodeId, actorUserId, editing) => {
    if (!nodeId) return;
    if (!editing) {
      // 退出帧**必须认人**：槽位是单值的，两个人先后编辑同一节点时，后进来的人
      // 一退出就会把前一个人的角标连同 TTL 定时器一起抹掉——对方明明还在编辑，
      // 提示却消失，要等他下一次 3s 心跳才回来。不是自己的槽位就整条帧忽略
      // （连 clearExpiry 都不能做，那会顺手掐掉别人的存活计时）。
      const current = useCanvasPresenceStore.getState().editors[nodeId];
      if (!current || current.actorUserId !== actorUserId) return;
      clearExpiry(nodeId);
      set((s) => {
        if (!s.editors[nodeId]) return {};
        const editors = { ...s.editors };
        delete editors[nodeId];
        return { editors };
      });
      return;
    }
    // 进入/续帧：重置该节点的 TTL。换人接管同一节点时定时器也跟着归属新的人。
    clearExpiry(nodeId);
    expiryTimers.set(
      nodeId,
      setTimeout(() => {
        expiryTimers.delete(nodeId);
        set((s) => {
          if (!s.editors[nodeId]) return {};
          const editors = { ...s.editors };
          delete editors[nodeId];
          return { editors };
        });
      }, NODE_EDITING_TTL_MS),
    );
    set((s) => {
      // 续帧（同一人、同一节点）不写 state：每 3s 换一次对象引用会让所有订阅
      // 这个节点的组件白重渲染一次。
      const current = s.editors[nodeId];
      if (current && current.actorUserId === actorUserId) return {};
      return { editors: { ...s.editors, [nodeId]: { actorUserId } } };
    });
  },

  reset: () => {
    for (const timer of expiryTimers.values()) clearTimeout(timer);
    expiryTimers.clear();
    set({ members: [], editors: {} });
  },
}));

/** 离开画布/断连时清场。名册与编辑角标都是「此刻这条连接看到的」，留到下一个
 *  项目就是一屏幻影。 */
export function resetCanvasPresence(): void {
  useCanvasPresenceStore.getState().reset();
}

/** 成员显示名：邮箱拿不到（后端查库失败）时退回通称，不显示裸 id——用户不认识
 *  那个数字，还会以为是什么错误代码。 */
export function memberLabel(member: PresenceMember | undefined): string {
  const email = member?.email?.trim() ?? '';
  return email || '另一位成员';
}

/**
 * 节点角标文案。null = 没有远端成员在编辑它。
 * 返回的是字符串（值相等即相等），所以可以直接当 zustand selector 用，不会因为
 * 每次算出新对象而触发多余渲染。
 */
export function nodeEditingLabel(state: PresenceState, nodeId: string): string | null {
  const editor = state.editors[nodeId];
  if (!editor) return null;
  const member = state.members.find((m) => m.actorUserId === editor.actorUserId);
  return `${memberLabel(member)} 正在编辑`;
}

/** 入站 canvas_presence 名册解析。actor=0 不该出现（后端已挡），这里再挡一次：
 *  它是「个人身份」的哨兵值，混进名册会显示成一个不存在的成员。 */
export function parsePresenceMembers(value: unknown): PresenceMember[] {
  if (!Array.isArray(value)) return [];
  const members: PresenceMember[] = [];
  for (const item of value) {
    if (typeof item !== 'object' || item === null) continue;
    const raw = item as Record<string, unknown>;
    const actorUserId = raw.actor_user_id;
    if (typeof actorUserId !== 'number' || !Number.isFinite(actorUserId) || actorUserId <= 0) continue;
    members.push({
      actorUserId,
      email: typeof raw.email === 'string' ? raw.email : '',
    });
  }
  return members;
}
