import { describe, expect, it } from 'vitest';
import { isIconUrl, lucideByName } from './presetIcon';

describe('isIconUrl', () => {
  it('URL 形态判真，图标名判假', () => {
    expect(isIconUrl('https://a/x.svg')).toBe(true);
    expect(isIconUrl('http://a/x.png')).toBe(true);
    expect(isIconUrl('//cdn/x.svg')).toBe(true);
    expect(isIconUrl('/static/x.svg')).toBe(true);
    expect(isIconUrl('data:image/png;base64,x')).toBe(true);
    expect(isIconUrl('clock-3')).toBe(false);
    expect(isIconUrl('grid-3x3')).toBe(false);
  });
});

describe('lucideByName', () => {
  it('kebab 名解析到组件（与后台图标库的转换互为逆运算）', () => {
    expect(lucideByName('clock-3')).toBeTruthy();
    expect(lucideByName('grid-3x3')).toBeTruthy();
    expect(lucideByName('axis-3d')).toBeTruthy();
    expect(lucideByName('arrow-up-right')).toBeTruthy();
    expect(lucideByName('user')).toBeTruthy();
  });

  it('未知名字返回 null（调用方落魔法棒兜底）', () => {
    expect(lucideByName('not-a-real-icon-xyz')).toBeNull();
    expect(lucideByName('')).toBeNull();
  });
});
