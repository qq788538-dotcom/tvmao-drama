import { describe, expect, it } from 'vitest';
import { describeAngle, describeLight, templateError } from './promptTemplates';

describe('promptTemplates', () => {
  it('模板可为空；非空时必须保留占位符', () => {
    expect(templateError('', '{angle}')).toBeNull();
    expect(templateError('  ', '{angle}')).toBeNull();
    expect(templateError('改变视角：{angle}', '{angle}')).toBeNull();
    expect(templateError('改变视角', '{angle}')).toContain('{angle}');
  });
  it('角度与打光参数描述', () => {
    expect(describeAngle({ key: 'k', name: 'n', rotation: 90, tilt: -30, zoom: 'closeup', prompt: '' })).toBe('水平 90° · 俯仰 -30° · 特写');
    expect(describeLight({ key: 'k', name: 'n', direction: 'left', brightness: 0, lightColor: '#ff0000', prompt: '' })).toBe('方向 left · 亮度 0 · 颜色 #ff0000');
    expect(describeLight({ key: 'k', name: 'n', prompt: 'x' })).toBe('仅附加提示词');
  });
});
