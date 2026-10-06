// 「发送到画布」副本的扑克牌式堆叠偏移，锚定在 source 节点的右上角。
// 第一个副本位于 source 右侧 +60px；之后每个副本左移 -30px、下移 +25px，
// 像一手牌那样扇形展开，匹配 Tapnow-Studio-PP-3.8.7 的
// 「发送到画布层叠」行为（changelog v3.7.x）。
export function pokerStackOffset(count: number): { dx: number; dy: number } {
  return { dx: 60 - count * 30, dy: count * 25 };
}
