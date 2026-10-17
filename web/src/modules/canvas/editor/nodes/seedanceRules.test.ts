import { describe, expect, it } from 'vitest';
import {
  SEEDANCE_REQUIREMENTS,
  checkSeedanceDuration,
} from './seedanceRules';

describe('checkSeedanceDuration', () => {
  it('范围内可提交（含边界）:视频 2~30、音频 2~15', () => {
    expect(checkSeedanceDuration('video', 2).ok).toBe(true);
    expect(checkSeedanceDuration('video', 30).ok).toBe(true);
    expect(checkSeedanceDuration('audio', 15).ok).toBe(true);
  });

  it('越界拒绝并给出带实际时长的原因;上限按类型分', () => {
    const tooShort = checkSeedanceDuration('video', 1.4);
    expect(tooShort.ok).toBe(false);
    expect(tooShort.reason).toContain('1.4s');

    expect(checkSeedanceDuration('video', 31).ok).toBe(false);
    // 音频没跟着视频放宽:16s 音频仍拒,且报错写的是音频自己的上限
    const audio = checkSeedanceDuration('audio', 16);
    expect(audio.ok).toBe(false);
    expect(audio.reason).toContain('2~15');
  });

  it('图片没有时长概念，恒可提交', () => {
    expect(checkSeedanceDuration('image', null).ok).toBe(true);
    expect(checkSeedanceDuration('image', 99).ok).toBe(true);
  });

  // 探测失败放行是明确决策：本地探测只是体验优化，卡死用户比误提交更糟，
  // 权威判定始终在上游。
  it('时长未知（探测失败/未完成）放行', () => {
    expect(checkSeedanceDuration('video', null).ok).toBe(true);
    expect(checkSeedanceDuration('audio', null).ok).toBe(true);
  });
});

describe('SEEDANCE_REQUIREMENTS', () => {
  it('三类素材各有一份要求文案，时长与校验逻辑同源:视频 2~30、音频 2~15', () => {
    expect(SEEDANCE_REQUIREMENTS.image.title).toBe('待校验图像要求');
    const video = SEEDANCE_REQUIREMENTS.video.items.find((i) => i.label === '时长');
    expect(video?.value).toBe('2 ~ 30 秒');
    const audio = SEEDANCE_REQUIREMENTS.audio.items.find((i) => i.label === '时长');
    expect(audio?.value).toBe('2 ~ 15 秒');
  });
});
