const DB_NAME = 'tvmao_frame_cache';
const DB_VERSION = 1;
const STORE_NAME = 'frames';

// 超过这个时长的帧会在初始化时自动清掉。改这个值来调整保留时长
// （当前是 3 天）。
const EXPIRY_MS = 3 * 24 * 60 * 60 * 1000;

let dbPromise: Promise<IDBDatabase> | null = null;

function openDB(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        const store = db.createObjectStore(STORE_NAME, { keyPath: 'id' });
        store.createIndex('nodeId', 'nodeId', { unique: false });
        store.createIndex('savedAt', 'savedAt', { unique: false });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

export interface CachedFrameSet {
  id: string;
  nodeId: string;
  frames: Array<{ url: string; time: number; filename?: string }>;
  selectedKeyframes: Array<{ url: string; time: number; filename?: string }>;
  savedAt: number;
}

export async function saveFrames(
  nodeId: string,
  frames: Array<{ url: string; time: number; filename?: string }>,
  selectedKeyframes: Array<{ url: string; time: number; filename?: string }>,
): Promise<void> {
  const db = await openDB();
  const tx = db.transaction(STORE_NAME, 'readwrite');
  const store = tx.objectStore(STORE_NAME);
  const record: CachedFrameSet = {
    id: nodeId,
    nodeId,
    frames,
    selectedKeyframes,
    savedAt: Date.now(),
  };
  store.put(record);
  await new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function loadFrames(nodeId: string): Promise<CachedFrameSet | null> {
  const db = await openDB();
  const tx = db.transaction(STORE_NAME, 'readonly');
  const store = tx.objectStore(STORE_NAME);
  const req = store.get(nodeId);
  return new Promise((resolve, reject) => {
    req.onsuccess = () => {
      const record = req.result as CachedFrameSet | undefined;
      if (!record) { resolve(null); return; }
      if (Date.now() - record.savedAt > EXPIRY_MS) {
        deleteFrames(nodeId).catch(() => {});
        resolve(null);
        return;
      }
      resolve(record);
    };
    req.onerror = () => reject(req.error);
  });
}

export async function deleteFrames(nodeId: string): Promise<void> {
  const db = await openDB();
  const tx = db.transaction(STORE_NAME, 'readwrite');
  tx.objectStore(STORE_NAME).delete(nodeId);
  await new Promise<void>((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function purgeExpired(): Promise<number> {
  const db = await openDB();
  const tx = db.transaction(STORE_NAME, 'readwrite');
  const store = tx.objectStore(STORE_NAME);
  const cutoff = Date.now() - EXPIRY_MS;
  let deleted = 0;

  return new Promise((resolve, reject) => {
    const req = store.openCursor();
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) { resolve(deleted); return; }
      const record = cursor.value as CachedFrameSet;
      if (record.savedAt < cutoff) {
        cursor.delete();
        deleted++;
      }
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
    tx.oncomplete = () => resolve(deleted);
  });
}
