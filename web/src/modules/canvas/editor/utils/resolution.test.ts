import { describe, it, expect } from 'vitest';
import { calculateResolution, nodeBoxFromDims } from './resolution';

describe('nodeBoxFromDims', () => {
  it('square dims → 280×280', () => {
    expect(nodeBoxFromDims(1024, 1024)).toEqual({ width: 280, height: 280 });
  });

  it('landscape pins height to 280 and scales width', () => {
    // 3840×2160 → 高 280，宽 round(280 × 3840/2160) = 498
    expect(nodeBoxFromDims(3840, 2160)).toEqual({ width: 498, height: 280 });
  });

  it('portrait pins width to 280 and scales height', () => {
    // 2160×3840 → 宽 280，高 round(280 × 3840/2160) = 498
    expect(nodeBoxFromDims(2160, 3840)).toEqual({ width: 280, height: 498 });
  });
});

describe('calculateResolution — explicit WxH passthrough', () => {
  it('returns an explicit WxH resolution verbatim, ignoring ratio', () => {
    expect(calculateResolution('1:1', '3840x2160')).toEqual({ str: '3840x2160', w: 3840, h: 2160 });
    expect(calculateResolution('16:9', '2160x3840')).toEqual({ str: '2160x3840', w: 2160, h: 3840 });
  });

  it('snaps explicit dims to multiples of 16', () => {
    // 5404 → round(5404/16)*16 = 5408
    expect(calculateResolution('9:16', '3040x5404')).toEqual({ str: '3040x5408', w: 3040, h: 5408 });
  });
});
