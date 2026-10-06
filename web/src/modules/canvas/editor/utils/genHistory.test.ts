import { describe, expect, it } from 'vitest';
import {
  appendHistoryItem,
  historyPromptAt,
  normalizeHistory,
  readPendingGen,
} from './genHistory';

describe('normalizeHistory', () => {
  it('把老快照的 string[] 升成对象条目——存量画布的读路径', () => {
    expect(normalizeHistory(['media://a', 'media://b'])).toEqual([
      { url: 'media://a' },
      { url: 'media://b' },
    ]);
  });

  it('新格式原样透传，保留 prompt/params/at', () => {
    const raw = [{ url: 'media://a', prompt: '猫', params: { ratio: '1:1' }, at: '2026-07-27T00:00:00Z' }];
    expect(normalizeHistory(raw)).toEqual(raw);
  });

  it('新老混排也能读——老条目生成过新图后就是这个形状', () => {
    expect(normalizeHistory(['media://old', { url: 'media://new', prompt: '橘猫' }])).toEqual([
      { url: 'media://old' },
      { url: 'media://new', prompt: '橘猫' },
    ]);
  });

  it('没有 history 但有 content 时，把 content 当作唯一一版（更早期节点的形状）', () => {
    expect(normalizeHistory(undefined, 'media://only')).toEqual([{ url: 'media://only' }]);
    expect(normalizeHistory([], 'media://only')).toEqual([{ url: 'media://only' }]);
  });

  it('history 为空且没有 content 时返回空数组', () => {
    expect(normalizeHistory(undefined)).toEqual([]);
    expect(normalizeHistory(null)).toEqual([]);
  });

  it('丢弃脏条目，避免渲染出空白版本格', () => {
    expect(normalizeHistory(['', null, { prompt: '没有 url' }, { url: 'media://ok' }, 42])).toEqual([
      { url: 'media://ok' },
    ]);
  });

  it('条目里类型不对的字段按缺失处理，不把脏值带进 UI', () => {
    expect(normalizeHistory([{ url: 'media://a', prompt: 123, params: 'x', at: 7 }])).toEqual([
      { url: 'media://a', prompt: undefined, params: undefined, at: undefined },
    ]);
  });
});

describe('historyPromptAt', () => {
  // 这条是「老数据零变化」的核心保证：没有自带 prompt 就回退到节点当前 prompt，
  // 于是调用方的 `viewingPrompt !== prompt` 恒为 false，「本版提示词」不出现。
  it('老条目回退到节点当前的 prompt', () => {
    const items = normalizeHistory(['media://a', 'media://b']);
    expect(historyPromptAt(items, 0, '当前这句')).toBe('当前这句');
    expect(historyPromptAt(items, 1, '当前这句')).toBe('当前这句');
  });

  it('新条目用自带的 prompt', () => {
    const items = normalizeHistory([{ url: 'media://a', prompt: '第一版' }]);
    expect(historyPromptAt(items, 0, '当前这句')).toBe('第一版');
  });

  it('越界下标回退到当前 prompt，不炸', () => {
    expect(historyPromptAt([], 3, '当前这句')).toBe('当前这句');
  });

  it('自带空字符串时按「就是空」处理，不回退——用户可能真的没写提示词', () => {
    const items = normalizeHistory([{ url: 'media://a', prompt: '' }]);
    expect(historyPromptAt(items, 0, '当前这句')).toBe('');
  });
});

describe('appendHistoryItem', () => {
  it('追加带提示词与入参的新版本', () => {
    const next = appendHistoryItem(
      [{ url: 'media://a' }],
      'media://b',
      { prompt: '橘猫逆光', params: { ratio: '16:9' } },
      '2026-07-27T10:00:00Z',
    );
    expect(next).toEqual([
      { url: 'media://a' },
      { url: 'media://b', prompt: '橘猫逆光', params: { ratio: '16:9' }, at: '2026-07-27T10:00:00Z' },
    ]);
  });

  it('没有快照时只记 url——退化成老行为，不写空字段', () => {
    expect(appendHistoryItem([], 'media://b')).toEqual([{ url: 'media://b' }]);
  });

  it('空 prompt / 空 params 不写进条目，避免读回来是「空提示词」而不是「没记录」', () => {
    expect(appendHistoryItem([], 'media://b', { prompt: '', params: {} })).toEqual([
      { url: 'media://b' },
    ]);
  });

  it('不改动传入的数组（store 里是不可变更新）', () => {
    const prev = [{ url: 'media://a' }];
    appendHistoryItem(prev, 'media://b');
    expect(prev).toEqual([{ url: 'media://a' }]);
  });

  // 后端 sink 也会写同一条（用户关页面时它是唯一写入方），页面开着时两边都写。
  // 不去重的话，快照被重新拉过的时机就会长出重复版本。
  it('同 URL 已存在时原样返回，不长出重复版本', () => {
    const prev = [{ url: 'media://a' }, { url: 'media://b', prompt: '后端写的' }];
    const next = appendHistoryItem(prev, 'media://b', { prompt: '前端又算了一遍' });
    expect(next).toBe(prev);
    expect(next).toHaveLength(2);
    // 已有条目不被覆盖。
    expect(next[1].prompt).toBe('后端写的');
  });
});

describe('readPendingGen', () => {
  it('读回提交时落库的快照', () => {
    expect(readPendingGen({ prompt: '猫', params: { ratio: '1:1' } })).toEqual({
      prompt: '猫',
      params: { ratio: '1:1' },
    });
  });

  it('形状不对时当作没有——宁可不记 prompt，也不能记错', () => {
    expect(readPendingGen(undefined)).toBeUndefined();
    expect(readPendingGen('猫')).toBeUndefined();
    expect(readPendingGen({})).toBeUndefined();
    expect(readPendingGen({ prompt: 123 })).toBeUndefined();
  });
});
