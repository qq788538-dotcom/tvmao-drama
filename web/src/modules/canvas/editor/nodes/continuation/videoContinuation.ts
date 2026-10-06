// 智能续写的纯逻辑层:单选区(前置参考片段)的钳制 + 默认选区 + 提示词兜底。
// 对标 liblib 的「智能续写」(实机逆向 2026-08-17:选 4~30s 前置片段 → 以其为
// 参考续写生成),但架构照本仓「片段重拍」的路走:切片 → 上传 → 铺
// 「切片 preview 节点 + gen-video 节点」,生成管道零改动,由用户手动点生成。
//
// 与 reshoot/segmentReshoot.ts 的多选区、trim/videoTrim.ts 的自由单选区都不同:
// 这里是「单选区 + 时长钳在 [4,30]」,规则最简单,单独一份纯函数便于钉住边界。

// 续写模型写死(用户拍板 2026-08-17):不加后台配置项,「模型中心已配置且启用
// 该 id」就是唯一开关——前端在 /models 运行时列表里找得到它才放行入口。
export const CONTINUATION_MODEL_ID = 'doubao-seedance-2-5-260628-extend';

// 前置参考片段的时长边界,liblib 同款(它的 i18n 常量 4/30 秒)。
export const CONTINUATION_MIN_RANGE_SEC = 4;
export const CONTINUATION_MAX_RANGE_SEC = 30;
// 源视频时长下限:不足最小选区长度就没得选。
export const CONTINUATION_MIN_SOURCE_SEC = CONTINUATION_MIN_RANGE_SEC;

// 续写内容留空时的兜底提示词。gen-video 的提交闸门要求提示词非空(不为续写
// 开洞),「留空=自然续写」的语义靠这句固定文案落地——同 reshoot 的
// RESHOOT_REROLL_PROMPT 先例。
export const CONTINUATION_DEFAULT_PROMPT = '自然延续视频内容，保持画面主体、场景与运镜风格一致，继续生成后续画面';

export interface ContinuationSelection {
  start: number;
  end: number;
}

// 初始选区:默认选**结尾** min(30, duration) 秒——续写语义上绝大多数人要从
// 结尾往后续,默认选尾段省一次拖拽(与 liblib 默认选开头不同,设计讨论用户拍板)。
export function initialContinuationSelection(duration: number): ContinuationSelection {
  return { start: Math.max(0, duration - CONTINUATION_MAX_RANGE_SEC), end: duration };
}

// 拖拽把手/整体平移。时长钳在 [4,30]、范围钳在 [0,duration]。
// 与 trim 的 dragSelection 结构同源,但那边只有下限没有上限,硬套要给它加
// max 参数并改动已钉死的 golden 行为,不如各自一份(同 reshoot 的取舍)。
export function dragContinuationSelection(
  kind: 'left' | 'right' | 'move',
  sel0: ContinuationSelection,
  dt: number,
  duration: number,
): ContinuationSelection {
  let { start, end } = sel0;
  if (kind === 'move') {
    const len = end - start;
    start = Math.min(Math.max(start + dt, 0), Math.max(duration - len, 0));
    end = start + len;
  } else if (kind === 'left') {
    start = Math.min(
      Math.max(start + dt, 0, end - CONTINUATION_MAX_RANGE_SEC),
      end - CONTINUATION_MIN_RANGE_SEC,
    );
  } else {
    end = Math.max(
      Math.min(end + dt, duration, start + CONTINUATION_MAX_RANGE_SEC),
      start + CONTINUATION_MIN_RANGE_SEC,
    );
  }
  // 极短源视频(4s 边缘)下上面的钳制可能交叉,兜底退回原选区。
  if (start < 0 || end > duration || end - start < CONTINUATION_MIN_RANGE_SEC) return sel0;
  return { start, end };
}

// 交给 gen-video 节点的最终提示词:有内容用内容,留空用固定兜底文案。
export function continuationPrompt(intent: string): string {
  const t = intent.trim();
  return t || CONTINUATION_DEFAULT_PROMPT;
}
