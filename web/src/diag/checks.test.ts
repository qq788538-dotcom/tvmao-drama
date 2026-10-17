import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectionChecks, diagMediaPath, formatReport } from './checks';

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

describe('诊断结果脱敏', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('检查抛出的错误里带 token 的 URL：detail 和 formatReport 都不含 token 明文', async () => {
    const secret = 'SECRET-TOKEN-123';
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error(`Error fetching http://127.0.0.1:1/api/v1/ping?token=${secret}: 500`);
      }),
    );
    class FailingSocket {
      onopen: (() => void) | null = null;
      onmessage: ((ev: { data: unknown }) => void) | null = null;
      onerror: (() => void) | null = null;
      onclose: ((ev: { code: number }) => void) | null = null;
      constructor() {
        setTimeout(() => this.onerror?.(), 0);
      }
      send() {}
      close() {}
    }
    vi.stubGlobal('WebSocket', FailingSocket);

    const results = await connectionChecks({ port: 1, token: secret });
    const ping = results[0];
    expect(ping.ok).toBe(false);
    expect(ping.detail).toContain('token=***');
    expect(ping.detail).not.toContain(secret);
    expect(formatReport(results)).not.toContain(secret);
  });

  it('formatReport 对外部传入的结果（如上传失败的 String(e)）同样脱敏', () => {
    const report = formatReport([
      { name: '上传测试视频', ok: false, detail: 'Error: GET /x?a=1&token=abc 404', ms: 0 },
    ]);
    expect(report).toContain('&token=***');
    expect(report).not.toContain('token=abc');
  });
});
