import { describe, expect, it } from 'vitest';
import {
  continuationPrompt,
  dragContinuationSelection,
  initialContinuationSelection,
  CONTINUATION_DEFAULT_PROMPT,
  CONTINUATION_MAX_RANGE_SEC,
  CONTINUATION_MIN_RANGE_SEC,
} from './videoContinuation';

describe('initialContinuationSelection', () => {
  it('长视频默认选结尾 30 秒(续写从结尾续,省一次拖拽)', () => {
    expect(initialContinuationSelection(136.5)).toEqual({ start: 106.5, end: 136.5 });
  });

  it('短于 30 秒时全选', () => {
    expect(initialContinuationSelection(10)).toEqual({ start: 0, end: 10 });
  });
});

describe('dragContinuationSelection', () => {
  const sel = { start: 100, end: 120 };

  it('左把手收缩不短于最小时长', () => {
    const r = dragContinuationSelection('left', sel, 100, 200);
    expect(r).toEqual({ start: 120 - CONTINUATION_MIN_RANGE_SEC, end: 120 });
  });

  it('左把手扩展不超过最大时长', () => {
    const r = dragContinuationSelection('left', sel, -100, 200);
    expect(r).toEqual({ start: 120 - CONTINUATION_MAX_RANGE_SEC, end: 120 });
  });

  it('右把手扩展不超过最大时长也不越过视频末尾', () => {
    expect(dragContinuationSelection('right', sel, 100, 200)).toEqual({
      start: 100,
      end: 100 + CONTINUATION_MAX_RANGE_SEC,
    });
    expect(dragContinuationSelection('right', sel, 100, 125)).toEqual({ start: 100, end: 125 });
  });

  it('整体平移钳在 [0, duration] 且保持时长', () => {
    expect(dragContinuationSelection('move', sel, -200, 200)).toEqual({ start: 0, end: 20 });
    expect(dragContinuationSelection('move', sel, 200, 200)).toEqual({ start: 180, end: 200 });
  });

  it('4 秒边缘源视频上钳制交叉时退回原选区', () => {
    const tiny = { start: 0, end: 4 };
    expect(dragContinuationSelection('left', tiny, 2, 4)).toEqual(tiny);
  });
});

describe('continuationPrompt', () => {
  it('有内容用内容', () => {
    expect(continuationPrompt(' 镜头拉远,主角转身离开 ')).toBe('镜头拉远,主角转身离开');
  });

  it('留空用固定兜底文案(gen-video 提交闸门要求提示词非空)', () => {
    expect(continuationPrompt('   ')).toBe(CONTINUATION_DEFAULT_PROMPT);
  });
});
