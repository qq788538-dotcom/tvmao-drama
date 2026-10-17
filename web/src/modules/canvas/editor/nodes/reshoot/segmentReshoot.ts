// 片段重拍的纯逻辑层:多选区(≤5 段)的增删拖拽钳制 + 时间标签。
// 架构是「每段切片 → 各铺一个 gen-video 节点」,一节点一任务一次上游调用,
// generation 管道零改动。纯函数,不碰 store,便于单测钉住边界。
//
// 与 trim/videoTrim.ts 的单选区工具是两套:那边是 [0,duration] 内的一个区间,
// 这边每段的活动范围还受相邻段夹挤(段与段不许重叠),钳制规则不同,硬套会把
// 两边都搞复杂。

// 单段时长下限。上游 seedance edit 对过短片段没意义,取 4 秒。
export const RESHOOT_MIN_RANGE_SEC = 4;
// 单段时长上限。seedance 2.5 edit 单次输入最长 30s(约定按 30s 做,
// 未实测上游校验;如果上游素材校验拒 >15s,收这个常量即可,交互不用改)。
export const RESHOOT_MAX_RANGE_SEC = 30;
// 最多可选段数。
export const RESHOOT_MAX_RANGES = 5;
// 源视频时长下限,低于它不给进重拍(文案:仅支持 ≥4 秒)。
export const RESHOOT_MIN_SOURCE_SEC = 4;
// 「整段重拍」(不选段)时源视频的上限——超过它模型单次吃不下,必须选段。
export const RESHOOT_WHOLE_MAX_SEC = 30;
// 新增段的期望时长:夹在 4~30 之间取 10s,一次点击出一段可直接用的选区。
export const RESHOOT_DEFAULT_RANGE_SEC = 10;

// 留空段落写入的中性指令。gen-video 的提交闸门要求提示词非空(保护所有既有
// 路径,不为重拍开洞),「留空=原样重跑」的语义靠这句固定文案落地。
export const RESHOOT_REROLL_PROMPT = '按原视频内容重新生成一次，保持画面主体、场景与运镜不变';

// 重拍固定使用的视频编辑模型,写死:不做后台配置层,
// 模型中心没配这个 modelId 时入口才报错提示。改用别的模型=改这一处 + 发版。
export const RESHOOT_MODEL_ID = 'doubao-seedance-2-5-260628-edit';

export interface ReshootRange {
  id: string;
  start: number;
  end: number;
  // 该段的修改意图,空串=原样重拍(提交时替换为 RESHOOT_REROLL_PROMPT)。
  intent: string;
}

let rangeSeq = 0;
function nextRangeId(): string {
  rangeSeq += 1;
  return `r${Date.now().toString(36)}-${rangeSeq}`;
}

export function sortRanges(ranges: ReshootRange[]): ReshootRange[] {
  return [...ranges].sort((a, b) => a.start - b.start);
}

// 包含时刻 t 的空档(两侧被相邻段或视频首尾夹住的可用区间)。t 落在已有段
// 内部返回 null——点已有段是选中它,不是加新段。
export function gapAt(
  ranges: ReshootRange[],
  t: number,
  duration: number,
): { start: number; end: number } | null {
  let lo = 0;
  let hi = duration;
  for (const r of sortRanges(ranges)) {
    if (t >= r.start && t <= r.end) return null;
    if (r.end <= t) lo = Math.max(lo, r.end);
    if (r.start >= t) hi = Math.min(hi, r.start);
  }
  if (hi - lo < RESHOOT_MIN_RANGE_SEC) return null;
  return { start: lo, end: hi };
}

// 在时刻 t 新增一段:以 t 为起点铺 RESHOOT_DEFAULT_RANGE_SEC,放不下就往回退,
// 空档太窄(<4s)或段数已满返回原数组(调用方比较引用判断是否成功)。
export function addRangeAt(
  ranges: ReshootRange[],
  t: number,
  duration: number,
): ReshootRange[] {
  if (ranges.length >= RESHOOT_MAX_RANGES) return ranges;
  const gap = gapAt(ranges, t, duration);
  if (!gap) return ranges;
  const len = Math.min(RESHOOT_DEFAULT_RANGE_SEC, RESHOOT_MAX_RANGE_SEC, gap.end - gap.start);
  let start = Math.min(Math.max(t, gap.start), gap.end - len);
  start = Math.max(start, gap.start);
  return sortRanges([...ranges, { id: nextRangeId(), start, end: start + len, intent: '' }]);
}

// 拖拽某段的把手/整体平移。活动范围被相邻段与 [0,duration] 夹住,时长钳在
// [4,30]。规则与 trim 的 dragSelection 同源但边界不同(那边是全轴自由)。
export function dragRange(
  ranges: ReshootRange[],
  id: string,
  kind: 'left' | 'right' | 'move',
  dt: number,
  duration: number,
): ReshootRange[] {
  const sorted = sortRanges(ranges);
  const idx = sorted.findIndex((r) => r.id === id);
  if (idx < 0) return ranges;
  const cur = sorted[idx];
  const lo = idx > 0 ? sorted[idx - 1].end : 0;
  const hi = idx < sorted.length - 1 ? sorted[idx + 1].start : duration;
  let { start, end } = cur;
  if (kind === 'move') {
    const len = end - start;
    start = Math.min(Math.max(start + dt, lo), hi - len);
    end = start + len;
  } else if (kind === 'left') {
    start = Math.min(Math.max(start + dt, lo, end - RESHOOT_MAX_RANGE_SEC), end - RESHOOT_MIN_RANGE_SEC);
  } else {
    end = Math.max(Math.min(end + dt, hi, start + RESHOOT_MAX_RANGE_SEC), start + RESHOOT_MIN_RANGE_SEC);
  }
  if (start < lo || end > hi || end - start < RESHOOT_MIN_RANGE_SEC) return ranges;
  return sorted.map((r) => (r.id === id ? { ...r, start, end } : r));
}

export function removeRange(ranges: ReshootRange[], id: string): ReshootRange[] {
  return ranges.filter((r) => r.id !== id);
}

export function updateRangeIntent(
  ranges: ReshootRange[],
  id: string,
  intent: string,
): ReshootRange[] {
  return ranges.map((r) => (r.id === id ? { ...r, intent } : r));
}

// mm:ss 标签(秒向下取整)。
export function formatTimeLabel(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}

// 段落交给 gen-video 节点的最终提示词:有意图带上时间段上下文,留空用固定
// 重跑文案。时间段写进提示词只为让用户回看节点时知道这段来自哪里——切片
// 本身已经只含该段画面,模型不需要这个信息。
export function rangePrompt(r: ReshootRange): string {
  const intent = r.intent.trim();
  if (!intent) return RESHOOT_REROLL_PROMPT;
  return intent;
}
