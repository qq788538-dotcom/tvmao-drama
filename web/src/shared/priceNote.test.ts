import { describe, expect, it } from 'vitest';
import { resolvePriceNote } from '@/shared/priceNote';

describe('resolvePriceNote', () => {
  const table = '480p:70积分/秒|720p:150积分/秒';

  it('匹配当前分辨率只显示对应档', () => {
    expect(resolvePriceNote(table, '480p')).toBe('70积分/秒');
    expect(resolvePriceNote(table, '720p')).toBe('150积分/秒');
  });

  it('分辨率大小写不一致也能匹配（uiSchema 720P vs 配置 720p）', () => {
    expect(resolvePriceNote(table, '720P')).toBe('150积分/秒');
    expect(resolvePriceNote('1080P:300积分/秒|720p:150积分/秒', '1080p')).toBe('300积分/秒');
  });

  it('当前分辨率没配档位时列出全部档（可读形式，不吐原始配置串）', () => {
    expect(resolvePriceNote(table, '1080p')).toBe('480p 70积分/秒 · 720p 150积分/秒');
  });

  it('普通文案（无 | 分段）原样返回——存量模型行为不变', () => {
    expect(resolvePriceNote('100积分/分钟', '720p')).toBe('100积分/分钟');
    expect(resolvePriceNote('约100积分/分钟，按实际用量结算', '')).toBe(
      '约100积分/分钟，按实际用量结算',
    );
  });

  it('单段带冒号仍当普通文案（分档表至少两段）', () => {
    expect(resolvePriceNote('720p:150积分/秒', '720p')).toBe('720p:150积分/秒');
  });

  it('任一段缺冒号或键/文案为空 → 整串当普通文案', () => {
    expect(resolvePriceNote('480p:70积分/秒|一口价', '480p')).toBe('480p:70积分/秒|一口价');
    expect(resolvePriceNote('480p:|720p:150积分/秒', '480p')).toBe('480p:|720p:150积分/秒');
    expect(resolvePriceNote(':70积分/秒|720p:150积分/秒', '720p')).toBe(
      ':70积分/秒|720p:150积分/秒',
    );
  });

  it('空/未配置 priceNote 原样透传', () => {
    expect(resolvePriceNote(undefined, '720p')).toBeUndefined();
    expect(resolvePriceNote('', '720p')).toBe('');
  });

  it('键与文案两侧空白被裁掉', () => {
    expect(resolvePriceNote(' 480p : 70积分/秒 | 720p : 150积分/秒 ', '480p')).toBe('70积分/秒');
  });
});
