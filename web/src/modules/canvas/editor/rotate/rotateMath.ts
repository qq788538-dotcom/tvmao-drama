// 旋转与镜像的纯计算侧（可单测，不碰 DOM/store）：外接矩形与结果节点显示
// 尺寸。交互与烘焙分别在 RotatePreview.tsx / bakeRotate.ts。
//
// 角度语义：顺时针、0–360 整数（对标 liblib 的输入框）。CSS rotate() 与
// canvas ctx.rotate() 在 y 轴向下的坐标系里正角度都是顺时针，预览（CSS）和
// 烘焙（canvas）共用同一个角度值即可，无需换算。

/** 角度输入的合法化：非法输入落 0，超界钳到 [0,360]，四舍五入成整数。 */
export function clampAngle(raw: number): number {
  if (!Number.isFinite(raw)) return 0;
  return Math.min(360, Math.max(0, Math.round(raw)));
}

/**
 * w×h 的矩形绕中心顺时针转 angle 度后的外接矩形（同样以中心对齐）。
 * 90 的倍数正好退化成宽高互换/不变；任意角度用 |cos|/|sin| 的经典公式。
 * round 收整数：这个尺寸既当烘焙画布的像素尺寸也当预览盒的世界尺寸，
 * 小数像素会让 toBlob 出的图带半透明边。
 */
export function rotatedBox(w: number, h: number, angleDeg: number): { width: number; height: number } {
  const rad = (angleDeg * Math.PI) / 180;
  const cos = Math.abs(Math.cos(rad));
  const sin = Math.abs(Math.sin(rad));
  return {
    width: Math.max(1, Math.round(w * cos + h * sin)),
    height: Math.max(1, Math.round(w * sin + h * cos)),
  };
}

/**
 * 结果节点的显示尺寸。**必须与 InputImageNode 的 auto-fit 公式逐字一致**
 * （targetArea 290²、下限 200）——上传完成后节点的 auto-fit effect 会按缩略图
 * 宽高比重算尺寸，公式一致时重算结果与建节点时相同，节点不会在上传完成的
 * 瞬间跳动。与 gridSplitMath.cellNodeSize 是同一条约定（各自贴各自的场景注释，
 * 公式本体三行，不值得为它抽共享模块）。
 */
export function rotatedNodeSize(aspect: number): { width: number; height: number } {
  const targetArea = 290 * 290;
  const h = Math.sqrt(targetArea / aspect);
  const w = h * aspect;
  return { width: Math.max(200, Math.round(w)), height: Math.max(200, Math.round(h)) };
}

// 预览盒/结果节点距源节点右缘的间距（世界单位），与宫格切分同款视觉节奏。
export const ROTATE_OFFSET_X = 160;
