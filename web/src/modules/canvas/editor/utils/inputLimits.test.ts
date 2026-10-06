import { describe, expect, it } from 'vitest';
import { inputLimitViolation } from './inputLimits';

const counts = (video: number, image = 0, audio = 0) => ({ video, image, audio });

describe('inputLimitViolation', () => {
  it('未配置一律通过', () => {
    expect(inputLimitViolation(undefined, counts(5))).toBeNull();
    expect(inputLimitViolation(null, counts(5))).toBeNull();
    expect(inputLimitViolation({}, counts(5))).toBeNull();
  });
  it('恰好满足 min=max=1 通过', () => {
    expect(inputLimitViolation({ video: { min: 1, max: 1 } }, counts(1))).toBeNull();
  });
  it('超上限——文案与后端 input_limits.go 逐字一致', () => {
    expect(inputLimitViolation({ video: { min: 1, max: 1 } }, counts(2))).toBe(
      '该模型最多支持连接 1 个视频，当前连接了 2 个',
    );
  });
  it('低于下限', () => {
    expect(inputLimitViolation({ video: { min: 1, max: 1 } }, counts(0))).toBe(
      '该模型需要连接至少 1 个视频',
    );
  });
  it('max 0 = 禁止连接', () => {
    expect(inputLimitViolation({ image: { max: 0 } }, counts(0, 1))).toBe(
      '该模型不支持连接图片',
    );
  });
  it('未约束的类型不限', () => {
    expect(inputLimitViolation({ video: { min: 1, max: 1 } }, counts(1, 9, 9))).toBeNull();
  });
});
