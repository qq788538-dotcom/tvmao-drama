import { afterEach, describe, expect, it, vi } from 'vitest';
import { loadCanvasViewMode, saveCanvasViewMode } from './localCanvasView';

const KEY = 'tvmao:canvas:viewMode';

afterEach(() => {
  window.localStorage.clear();
  vi.restoreAllMocks();
});

describe('loadCanvasViewMode', () => {
  it('没有记录时默认「工作流」', () => {
    expect(loadCanvasViewMode()).toBe('canvas');
  });

  it('读回上次存的值', () => {
    saveCanvasViewMode('storyboard');
    expect(loadCanvasViewMode()).toBe('storyboard');
    saveCanvasViewMode('canvas');
    expect(loadCanvasViewMode()).toBe('canvas');
  });

  // localStorage 里的东西不可信：用户手改过、或以后视图种类增删过，都可能读出
  // 一个当前不存在的模式。不校验就会拿它去渲染一个不存在的视图。
  it('值非法时回落到默认，不把脏值透传出去', () => {
    window.localStorage.setItem(KEY, 'timeline');
    expect(loadCanvasViewMode()).toBe('canvas');

    window.localStorage.setItem(KEY, '');
    expect(loadCanvasViewMode()).toBe('canvas');

    window.localStorage.setItem(KEY, '{"mode":"storyboard"}');
    expect(loadCanvasViewMode()).toBe('canvas');
  });

  // 隐私模式 / 被禁用 / iframe 跨域都会让 localStorage 抛异常。存个视图偏好
  // 而已，不该把整个画布页搞崩。
  it('localStorage 抛异常时回落到默认而不是抛出去', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError');
    });
    expect(() => loadCanvasViewMode()).not.toThrow();
    expect(loadCanvasViewMode()).toBe('canvas');
  });
});

describe('saveCanvasViewMode', () => {
  it('写入失败不抛，本次使用不受影响', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError');
    });
    expect(() => saveCanvasViewMode('storyboard')).not.toThrow();
  });

  // 全局一个键，不带 projectID —— 这是它跟 localView（按项目存 pan/zoom）
  // 的关键区别：用户选的是「我习惯用哪种方式看项目」，不是「这个项目该怎么看」。
  it('用的是不带项目号的全局键，所有画布共用', () => {
    saveCanvasViewMode('storyboard');
    expect(window.localStorage.getItem(KEY)).toBe('storyboard');

    // 只落一个键，且键名里不含任何项目号——按项目存的话每个画布会各记各的，
    // 那就不是「全局共用」了。（Storage 的键不是自有属性，Object.keys 拿不到，
    // 只能用 key(i) 遍历。）
    const keys = Array.from({ length: window.localStorage.length }, (_, i) => window.localStorage.key(i));
    expect(keys).toEqual([KEY]);
  });
});
