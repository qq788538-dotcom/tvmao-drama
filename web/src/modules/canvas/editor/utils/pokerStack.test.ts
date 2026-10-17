import { describe, it, expect } from 'vitest';
import { pokerStackOffset } from './pokerStack';

describe('pokerStackOffset', () => {
  it('count=0 → 第一张紧贴右上方 (+60, 0)', () => {
    expect(pokerStackOffset(0)).toEqual({ dx: 60, dy: 0 });
  });

  it('count=1 → 左下偏 30/25', () => {
    expect(pokerStackOffset(1)).toEqual({ dx: 30, dy: 25 });
  });

  it('count=2 → 累积偏 60/50', () => {
    expect(pokerStackOffset(2)).toEqual({ dx: 0, dy: 50 });
  });

  it('count=3 → dx 可以为负（已叠到 gen-image 左边）', () => {
    expect(pokerStackOffset(3)).toEqual({ dx: -30, dy: 75 });
  });
});
