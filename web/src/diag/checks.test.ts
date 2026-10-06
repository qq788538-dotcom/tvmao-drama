import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectionChecks, diagMediaPath } from './checks';

describe('diagMediaPath', () => {
  it('文件名做 URL 编码（空格、中文、#、?）', () => {
    expect(diagMediaPath('样片 1#?.mp4')).toBe('/api/v1/diag/media/%E6%A0%B7%E7%89%87%201%23%3F.mp4');
  });
});

describe('connectionChecks WebSocket 回显', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('服务端在回显前关闭连接：立刻判失败并带上 close code', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"ok":true,"version":"t"}', { status: 401 })));
    class ClosingSocket {
      onopen: (() => void) | null = null;
      onmessage: ((ev: { data: unknown }) => void) | null = null;
      onerror: (() => void) | null = null;
      onclose: ((ev: { code: number }) => void) | null = null;
      constructor() {
        setTimeout(() => this.onclose?.({ code: 1008 }), 0);
      }
      send() {}
      close() {}
    }
    vi.stubGlobal('WebSocket', ClosingSocket);

    const results = await connectionChecks({ port: 1, token: 't' });
    const ws = results.find((r) => r.name === 'WebSocket 回显')!;

    expect(ws.ok).toBe(false);
    expect(ws.detail).toContain('1008');
    expect(ws.ms).toBeLessThan(4000);
  });
});
