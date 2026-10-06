// 瞬态帧（拖拽实时流 / 「正在编辑」提示）的**发送口**，外加入站载荷的解析。
//
// 为什么是模块级单例而不是 React context：发送方是 NodeShell / TextNode 这类
// 深层节点组件——拖拽的 mousemove 里逐帧调用，画布上有几十个实例；接收方是
// 页面持有的那条 WS。走 context 的话每个节点都要多订阅一个 provider，而画布里
// 已有的同类跨层通道（store 的 canvasOpSink、interactionRegistry）全是模块级
// 单例。同一张画布同一时刻只有一条 WS、只有一次拖拽，单例正好够用。
//
// **团队 only**：页面只在团队身份下注册通道（个人项目根本不注册），所以个人
// 项目一帧都发不出去——调用点不必再各自判一次身份。
//
// 帧契约见 apps/api/internal/agent/envelope.go（服务端只保留 kind/positions/
// node_id/editing，actor_user_id 一律由服务端覆写，客户端自报的会被丢弃）。

/** 拖拽瞬态帧里的一条绝对坐标。绝对而非增量：丢帧不需要补偿，下一帧直接覆盖。 */
export interface TransientDragPosition {
  id: string;
  x: number;
  y: number;
}

export interface OutgoingTransientFrame {
  type: 'canvas_transient';
  project_id: number;
  kind: 'drag' | 'node_editing';
  positions?: TransientDragPosition[];
  node_id?: string;
  editing?: boolean;
}

/** 返回值是「有没有真的发上网」（WS 非 OPEN 时为 false）。瞬态帧丢了不补，
 *  所以调用方不需要处理 false——留着返回值只为测试与调试能看见。 */
export type TransientSend = (frame: OutgoingTransientFrame) => boolean;

interface TransientChannel {
  projectId: number;
  send: TransientSend;
}

let channel: TransientChannel | null = null;

/**
 * 注册/注销瞬态通道。页面在**团队身份**下建好 WS 后注册，卸载/切项目/断连
 * 时传 null 注销。注销必须把节流窗口一并重置：换了连接还沿用上一条的时刻，
 * 新连接的第一帧可能被吃掉 200ms。
 */
export function setTransientChannel(next: TransientChannel | null): void {
  channel = next;
  lastDragSentAt = 0;
}

export function isTransientChannelOpen(): boolean {
  return channel !== null;
}

/** 拖拽瞬态帧的节流窗口。200ms ≈ 5fps：队友那边看着连贯，又不会把一次
 *  两秒的拖拽变成上百帧广播（每帧都要在服务端扇出给项目下所有连接）。 */
export const DRAG_TRANSIENT_THROTTLE_MS = 200;

let lastDragSentAt = 0;

/** 重置节流窗口，让下一次拖拽的**第一帧**立刻发出去（前沿节流）。
 *  不重置的话，两次拖拽间隔小于 200ms 时第二次的起手帧会被吞掉。 */
export function resetDragTransientThrottle(): void {
  lastDragSentAt = 0;
}

/**
 * 这一帧「值不值得组装坐标」。给 mousemove 这种每帧都调的热路径做前置判断：
 * 组装一批坐标是多选数 × 节点数的线性查找，而 60fps 下 5/6 的帧会被节流直接
 * 丢弃——先问一句就能把这些白算全省掉。判定与 sendDragTransient 内部完全一致
 * （通道在不在 + 节流窗过没过），真正的节流仍以发送时那次判断为准。
 */
export function shouldSendDragTransient(): boolean {
  if (!channel) return false;
  return Date.now() - lastDragSentAt >= DRAG_TRANSIENT_THROTTLE_MS;
}

export function sendDragTransient(positions: TransientDragPosition[]): boolean {
  if (!channel || positions.length === 0) return false;
  const now = Date.now();
  if (now - lastDragSentAt < DRAG_TRANSIENT_THROTTLE_MS) return false;
  const sent = channel.send({
    type: 'canvas_transient',
    project_id: channel.projectId,
    kind: 'drag',
    positions,
  });
  // **只有真的发上网才消耗节流窗口**。WS 断着的时候每一帧都会返回 false，若在
  // 这之前就把窗口标记成「刚发过」，重连成功后的头 200ms 仍然发不出去——队友
  // 那边的节点会先卡一下再跳，而这一段恰好是最需要实时性的重连瞬间。
  if (sent) lastDragSentAt = now;
  return sent;
}

/** 「我开始/停止编辑这个节点」。不节流——它本来就只在进入/退出编辑态与 3s
 *  心跳时发（心跳见 hooks/useNodeEditingBroadcast），频率远低于拖拽。 */
export function sendNodeEditingTransient(nodeId: string, editing: boolean): boolean {
  if (!channel || !nodeId) return false;
  return channel.send({
    type: 'canvas_transient',
    project_id: channel.projectId,
    kind: 'node_editing',
    node_id: nodeId,
    editing,
  });
}

/**
 * 入站 drag 帧的坐标解析。**坏条目丢弃而不是整帧丢弃**：瞬态帧一帧一个样，
 * 为一条脏数据放弃整批只会让其余节点卡住不动；而缺 x/y 的条目直接落进 store
 * 会把节点弹到 NaN 坐标（画布上整块消失）。
 */
export function parseTransientDragPositions(value: unknown): TransientDragPosition[] {
  if (!Array.isArray(value)) return [];
  const parsed: TransientDragPosition[] = [];
  for (const item of value) {
    if (typeof item !== 'object' || item === null) continue;
    const { id, x, y } = item as Record<string, unknown>;
    if (typeof id !== 'string' || id === '') continue;
    if (typeof x !== 'number' || !Number.isFinite(x)) continue;
    if (typeof y !== 'number' || !Number.isFinite(y)) continue;
    parsed.push({ id, x, y });
  }
  return parsed;
}
