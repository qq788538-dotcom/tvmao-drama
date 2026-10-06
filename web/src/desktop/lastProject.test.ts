import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clearLastProjectId, loadLastProjectId, saveLastProjectId } from './lastProject';

describe('lastProject', () => {
  beforeEach(() => localStorage.clear());

  it('没有记录时返回 null', () => {
    expect(loadLastProjectId()).toBeNull();
  });

  it('存了就能读回来；清掉后又是 null', () => {
    saveLastProjectId(42);
    expect(loadLastProjectId()).toBe(42);
    clearLastProjectId();
    expect(loadLastProjectId()).toBeNull();
  });

  it('非法值（被手改过的存储）当作没有记录', () => {
    localStorage.setItem('miaoju.lastProjectId', 'abc');
    expect(loadLastProjectId()).toBeNull();
    localStorage.setItem('miaoju.lastProjectId', '-3');
    expect(loadLastProjectId()).toBeNull();
  });

  it('不存非法 id', () => {
    saveLastProjectId(0);
    saveLastProjectId(Number.NaN);
    expect(loadLastProjectId()).toBeNull();
  });

  it('存储抛错时读返回 null、写不抛', () => {
    const getSpy = vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError');
    });
    const setSpy = vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceeded');
    });
    expect(loadLastProjectId()).toBeNull();
    expect(() => saveLastProjectId(7)).not.toThrow();
    getSpy.mockRestore();
    setSpy.mockRestore();
  });
});
