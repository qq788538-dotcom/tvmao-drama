// 下一个由 agent 生成的 preview 节点应该落在画布上的什么位置。
//
// 曾经是 +30/+25 的「扑克牌堆叠」：一次 action 铺十几个分镜节点时后一张压着
// 前一张，标题被遮、点不中、也看不出顺序（线上截图为证）。改成规则网格：
// 一行 AGENT_GRID_COLS 个，排满换行往下，同行 y 相同、同列 x 相同。

/** 一行几个。 */
export const AGENT_GRID_COLS = 5;
// 格子步长（中心距——addNode 收的 worldPosition 就是节点中心）。取这么宽是因为
// 节点框会随生成比例变形：nodeBoxFromDims 让短边固定 280、长边 = 280×比例，
// 21:9 时长边到 653。720/780 保证用户切到最极端比例也不碰邻居，还剩几十像素
// 的视觉间隙。行距比列距再宽一点，给标题栏/工具条的溢出留余量。
export const AGENT_GRID_COL_STEP = 720;
export const AGENT_GRID_ROW_STEP = 780;

// 超过这个值的数字不当序号看。agent 起的名字里可能混进年份、金额、时长
//（「2026年的对峙」「赔了5000块」），照单全收会把节点甩到第 400 行外的荒野。
// 一次 action 铺两百个以上节点不是真实场景，越界的一律退回「最小空位」。
const MAX_SEQ = 200;

/**
 * 从产物名里取序号。agent 的编号写法五花八门——「第13段·施工对峙」「镜头2」
 * 「Shot 3」「1. 开场」——统一取第一个阿拉伯数字。取不到返回 null。
 */
export function seqFromName(name?: string | null): number | null {
  if (!name) return null;
  const m = name.match(/\d+/);
  if (!m) return null;
  const n = Number(m[0]);
  if (!Number.isFinite(n) || n > MAX_SEQ) return null;
  return n;
}

/**
 * 给一个产物分配格子号，并在 taken 上记账。
 *
 * 序号 N 直接落第 N-1 格（1-based 是绝大多数写法），这样不必等一次 action 的
 * 产物全部到齐——流式到达就能各就各位，也不用回头搬已经落好的节点。
 * 撞车（重号、0-based、序号缺失）一律顺延到最近的空格子：顺序仍然单调，
 * 且永远不重叠。
 */
export function claimAgentSlot(taken: Set<number>, name?: string | null): number {
  const seq = seqFromName(name);
  let slot = seq === null ? 0 : Math.max(0, seq - 1);
  while (taken.has(slot)) slot += 1;
  taken.add(slot);
  return slot;
}

/** 格子号 → 世界坐标（节点中心）。 */
export function agentDropPointFor(anchor: { anchorX: number; anchorY: number }, slot: number) {
  const col = slot % AGENT_GRID_COLS;
  const row = Math.floor(slot / AGENT_GRID_COLS);
  return {
    x: anchor.anchorX + col * AGENT_GRID_COL_STEP,
    y: anchor.anchorY + row * AGENT_GRID_ROW_STEP,
  };
}
