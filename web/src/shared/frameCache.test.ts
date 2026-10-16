import { afterEach, describe, expect, it, vi } from 'vitest';

// 手写的最小 IndexedDB 替身：只覆盖 frameCache 用到的那几步（open → transaction → delete → complete）。
function fakeIndexedDB() {
  const dbs: Array<Record<string, any>> = [];
  const open = vi.fn(() => {
    const req: Record<string, any> = {};
    queueMicrotask(() => {
      if (open.mock.calls.length === 1 && failFirst) {
        req.error = new Error('open failed');
        req.onerror?.();
        return;
      }
      const db: Record<string, any> = {
        objectStoreNames: { contains: () => true },
        close: vi.fn(),
        transaction: vi.fn(() => {
          const tx: Record<string, any> = {
            objectStore: () => ({ delete: () => queueMicrotask(() => tx.oncomplete?.()) }),
          };
          return tx;
        }),
      };
      dbs.push(db);
      req.result = db;
      req.onsuccess?.();
    });
    return req;
  });
  let failFirst = false;
  return {
    open,
    dbs,
    failFirstOpen() {
      failFirst = true;
    },
  };
}

describe('frameCache 连接缓存', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it('连接被关闭（close 事件）后下次调用重新打开，而不是一直用死连接', async () => {
    const idb = fakeIndexedDB();
    vi.stubGlobal('indexedDB', { open: idb.open });
    const { deleteFrames } = await import('./frameCache');
    await deleteFrames('a');
    await deleteFrames('b');
    expect(idb.open).toHaveBeenCalledTimes(1);
    idb.dbs[0].onclose();
    await deleteFrames('c');
    expect(idb.open).toHaveBeenCalledTimes(2);
    expect(idb.dbs[1].transaction).toHaveBeenCalledTimes(1);
  });

  it('打开失败不缓存，下次调用重试', async () => {
    const idb = fakeIndexedDB();
    idb.failFirstOpen();
    vi.stubGlobal('indexedDB', { open: idb.open });
    const { deleteFrames } = await import('./frameCache');
    await expect(deleteFrames('a')).rejects.toThrow('open failed');
    await deleteFrames('a');
    expect(idb.open).toHaveBeenCalledTimes(2);
  });

  it('versionchange 时让出连接', async () => {
    const idb = fakeIndexedDB();
    vi.stubGlobal('indexedDB', { open: idb.open });
    const { deleteFrames } = await import('./frameCache');
    await deleteFrames('a');
    idb.dbs[0].onversionchange();
    expect(idb.dbs[0].close).toHaveBeenCalled();
    await deleteFrames('b');
    expect(idb.open).toHaveBeenCalledTimes(2);
  });
});
