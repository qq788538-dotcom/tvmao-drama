// 订阅 CLI 对当前项目画布的写入（CLI spec 第 6 节）。收到更新的版本后，等画布空闲再整拉；
// 断线自动重连（原生 EventSource 收到非 200 会直接放弃重试，所以手动重连），重连成功后比较一次版本补漏。
import { useCallback, useEffect, useRef } from 'react';
import { getSnapshot } from '@/shared/api';
import { buildEventSourceUrl } from '@/shared/server';
import { currentSidecarSession } from '@/shared/sidecarSession';
import { installPointerTracker, isCanvasIdle, reloadCanvasFromServer } from '../persistence/externalChanges';

const IDLE_RETRY_MS = 500;
const MAX_BACKOFF_MS = 30_000;

interface ExternalChangeEvent {
  projectId: number;
  version: number;
  source: string;
}

export function useExternalChanges(opts: {
  projectId: number;
  enabled: boolean;
  initialVersion: number;
  onReloaded?: (version: number) => void;
}): { noteLocalVersion: (version: number) => void } {
  const { projectId, enabled, initialVersion } = opts;
  const onReloadedRef = useRef(opts.onReloaded);
  onReloadedRef.current = opts.onReloaded;
  const noteLocalRef = useRef<(version: number) => void>(() => {});

  useEffect(() => {
    if (!enabled) return;
    let disposed = false;
    let loaded = initialVersion; // 最近一次整拉（或首次加载）的快照版本；只给事件判断用
    // 前端已知"内容都看过"的服务端版本：整拉版本，加上紧接其后的本地写入回执。只给重连补漏比较用。
    // 不能拿它做事件判断：CLI 写成 v5、本地回执 v6 先到时，v6 虽含 CLI 的改动，前端却没看到。
    // 回执只在连号时推进：跳号说明中间有别人写过（可能是断线期间丢了事件的 CLI 写入），留给重连补漏。
    let knownServerVersion = initialVersion;
    let wanted = 0; // 已知需要追上的最高版本
    let es: EventSource | null = null;
    let opened = false;
    let retry = 0;
    let reloading = false;
    let idleTimer: ReturnType<typeof setTimeout> | undefined;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    const removeTracker = installPointerTracker();

    const schedule = (ms = IDLE_RETRY_MS) => {
      clearTimeout(idleTimer);
      idleTimer = setTimeout(() => void tryReload(), ms);
    };

    const tryReload = async () => {
      if (disposed || reloading || wanted <= loaded) return;
      if (!isCanvasIdle()) {
        schedule();
        return;
      }
      reloading = true;
      let v: number | null = null;
      try {
        v = await reloadCanvasFromServer(projectId);
      } catch {
        v = null;
      } finally {
        reloading = false;
      }
      if (disposed) return;
      if (v === null) {
        schedule();
        return;
      }
      loaded = Math.max(loaded, v);
      knownServerVersion = Math.max(knownServerVersion, v);
      onReloadedRef.current?.(v);
      if (wanted > loaded) schedule();
    };

    noteLocalRef.current = (version: number) => {
      if (version === knownServerVersion + 1) knownServerVersion = version;
    };

    const want = (version: number) => {
      if (version <= loaded) return;
      wanted = Math.max(wanted, version);
      void tryReload();
    };

    // 用户松手或输入框失焦时立刻再试一次，不用干等定时器
    const onSettled = () => {
      if (wanted > loaded) schedule(0);
    };
    window.addEventListener('pointerup', onSettled);
    document.addEventListener('focusout', onSettled);

    const reconnect = () => {
      if (disposed) return;
      const delay = Math.min(1000 * 2 ** retry, MAX_BACKOFF_MS);
      retry += 1;
      retryTimer = setTimeout(() => void connect(), delay);
    };

    const connect = async () => {
      const info = await currentSidecarSession();
      if (disposed) return;
      if (!info) {
        reconnect();
        return;
      }
      const src = new EventSource(buildEventSourceUrl(info, `/api/v1/projects/${projectId}/events`));
      es = src;
      src.onopen = () => {
        retry = 0;
        if (opened) {
          // 断线期间的事件收不到了：比较一次版本补上
          getSnapshot(projectId)
            .then((s) => !disposed && s.version > knownServerVersion && want(s.version))
            .catch(() => {});
        }
        opened = true;
      };
      src.onmessage = (m) => {
        let e: ExternalChangeEvent;
        try {
          e = JSON.parse(m.data) as ExternalChangeEvent;
        } catch {
          return;
        }
        if (e.source !== 'cli' || e.projectId !== projectId) return;
        want(e.version);
      };
      src.onerror = () => {
        src.close();
        if (es === src) es = null;
        reconnect();
      };
    };
    void connect();

    return () => {
      disposed = true;
      noteLocalRef.current = () => {};
      es?.close();
      clearTimeout(idleTimer);
      clearTimeout(retryTimer);
      window.removeEventListener('pointerup', onSettled);
      document.removeEventListener('focusout', onSettled);
      removeTracker();
    };
  }, [projectId, enabled, initialVersion]);

  // 本地 mutation 回执的版本：由 configureCanvasPersistence 的 onVersion 转发进来
  const noteLocalVersion = useCallback((version: number) => noteLocalRef.current(version), []);
  return { noteLocalVersion };
}
