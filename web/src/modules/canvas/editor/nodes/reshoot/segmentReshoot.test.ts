import { describe, expect, it } from 'vitest';
import {
  addRangeAt,
  dragRange,
  gapAt,
  formatTimeLabel,
  rangePrompt,
  removeRange,
  sortRanges,
  updateRangeIntent,
  RESHOOT_MAX_RANGES,
  RESHOOT_MAX_RANGE_SEC,
  RESHOOT_MIN_RANGE_SEC,
  RESHOOT_REROLL_PROMPT,
  type ReshootRange,
} from './segmentReshoot';

const mk = (id: string, start: number, end: number, intent = ''): ReshootRange => ({
  id,
  start,
  end,
  intent,
});

describe('gapAt', () => {
  it('空画布返回全轴', () => {
    expect(gapAt([], 50, 136)).toEqual({ start: 0, end: 136 });
  });
  it('落在已有段内部返回 null', () => {
    expect(gapAt([mk('a', 10, 20)], 15, 136)).toBeNull();
  });
  it('被相邻段夹出正确空档', () => {
    expect(gapAt([mk('a', 10, 20), mk('b', 60, 80)], 40, 136)).toEqual({ start: 20, end: 60 });
  });
  it('空档不足最小段长返回 null', () => {
    expect(gapAt([mk('a', 10, 20), mk('b', 22, 40)], 21, 136)).toBeNull();
  });
});

describe('addRangeAt', () => {
  it('默认铺 10s,起点即点击时刻', () => {
    const out = addRangeAt([], 50, 136);
    expect(out).toHaveLength(1);
    expect(out[0].start).toBe(50);
    expect(out[0].end).toBe(60);
  });
  it('贴近视频尾部时往回退,不越界', () => {
    const out = addRangeAt([], 133, 136);
    expect(out[0].end).toBe(136);
    expect(out[0].end - out[0].start).toBe(10);
  });
  it('空档比默认段长窄时铺满空档', () => {
    const out = addRangeAt([mk('a', 0, 10), mk('b', 16, 30)], 12, 136);
    const added = out.find((r) => r.id !== 'a' && r.id !== 'b')!;
    expect(added.start).toBe(10);
    expect(added.end).toBe(16);
  });
  it('段数已满不再新增', () => {
    const five = Array.from({ length: RESHOOT_MAX_RANGES }, (_, i) => mk(`r${i}`, i * 20, i * 20 + 5));
    expect(addRangeAt(five, 110, 136)).toBe(five);
  });
  it('点在已有段上不新增', () => {
    const one = [mk('a', 10, 20)];
    expect(addRangeAt(one, 15, 136)).toBe(one);
  });
});

describe('dragRange', () => {
  it('右把手不超过单段 30s 上限', () => {
    const out = dragRange([mk('a', 10, 20)], 'a', 'right', 100, 136);
    expect(out[0].end - out[0].start).toBe(RESHOOT_MAX_RANGE_SEC);
  });
  it('左把手不短于 4s 下限', () => {
    const out = dragRange([mk('a', 10, 20)], 'a', 'left', 100, 136);
    expect(out[0].end - out[0].start).toBe(RESHOOT_MIN_RANGE_SEC);
  });
  it('平移被相邻段挡住', () => {
    const out = dragRange([mk('a', 10, 20), mk('b', 25, 35)], 'a', 'move', 100, 136);
    expect(out.find((r) => r.id === 'a')!.end).toBe(25);
  });
  it('平移保持时长不变', () => {
    const out = dragRange([mk('a', 10, 20)], 'a', 'move', 5, 136);
    expect(out[0]).toMatchObject({ start: 15, end: 25 });
  });
});

describe('增删改', () => {
  it('removeRange 删除指定段', () => {
    expect(removeRange([mk('a', 0, 10), mk('b', 20, 30)], 'a')).toHaveLength(1);
  });
  it('updateRangeIntent 只改目标段', () => {
    const out = updateRangeIntent([mk('a', 0, 10), mk('b', 20, 30)], 'b', '换白天');
    expect(out.find((r) => r.id === 'b')!.intent).toBe('换白天');
    expect(out.find((r) => r.id === 'a')!.intent).toBe('');
  });
  it('sortRanges 按起点升序', () => {
    expect(sortRanges([mk('b', 20, 30), mk('a', 0, 10)]).map((r) => r.id)).toEqual(['a', 'b']);
  });
});

describe('rangePrompt / formatTimeLabel', () => {
  it('留空回退到固定重跑文案', () => {
    expect(rangePrompt(mk('a', 0, 10, '  '))).toBe(RESHOOT_REROLL_PROMPT);
  });
  it('有意图原样返回', () => {
    expect(rangePrompt(mk('a', 0, 10, '把台灯换成白色'))).toBe('把台灯换成白色');
  });
  it('mm:ss 标签', () => {
    expect(formatTimeLabel(57.8)).toBe('0:57');
    expect(formatTimeLabel(128.2)).toBe('2:08');
  });
});
