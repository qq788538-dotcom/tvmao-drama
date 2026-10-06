import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { AgentClient } from './agentClient';
import type { Frame } from './types';

class FakeWS {
  static instances: FakeWS[] = [];
  static OPEN = 1;
  readyState = FakeWS.OPEN;
  url: string;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  sent: string[] = [];
  constructor(url: string) { this.url = url; FakeWS.instances.push(this); }
  send(s: string) { this.sent.push(s); }
  close() { this.onclose?.(new CloseEvent('close')); }
  emit(data: object) { this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) })); }
}

describe('AgentClient', () => {
  beforeEach(() => {
    FakeWS.instances = [];
    // @ts-expect-error 替换全局对象
    globalThis.WebSocket = FakeWS;
  });
  afterEach(() => {
    // @ts-expect-error 还原
    delete globalThis.WebSocket;
  });

  it('connects with token via query param and dispatches frames', () => {
    const handler = vi.fn();
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 'jwt-x' });
    client.onFrame(handler);
    client.connect();
    expect(FakeWS.instances[0].url).toBe('/api/v1/agent/ws?token=jwt-x');
    FakeWS.instances[0].onopen?.(new Event('open'));
    FakeWS.instances[0].emit({ type: 'thread_start', thread_id: 'thr_1' });
    expect(handler).toHaveBeenCalledWith({ type: 'thread_start', thread_id: 'thr_1' } satisfies Frame);
  });

  it('connects with project_id in query param when provided', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 'jwt-x', projectId: 7 });
    client.connect();
    expect(FakeWS.instances[0].url).toBe('/api/v1/agent/ws?project_id=7&token=jwt-x');
  });

  it('appends token correctly when url already has a query string', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws?foo=bar', token: 'jwt-x' });
    client.connect();
    expect(FakeWS.instances[0].url).toBe('/api/v1/agent/ws?foo=bar&token=jwt-x');
  });

  it('does not append token when url already carries it (buildWsUrl)', () => {
    const client = new AgentClient({ url: 'ws://127.0.0.1:5555/api/v1/agent/ws?token=a%2Bb', projectId: 7 });
    client.connect();
    expect(FakeWS.instances[0].url).toBe('ws://127.0.0.1:5555/api/v1/agent/ws?token=a%2Bb&project_id=7');
  });

  it('encodes token in URL', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 'a/b+c=' });
    client.connect();
    expect(FakeWS.instances[0].url).toBe('/api/v1/agent/ws?token=a%2Fb%2Bc%3D');
  });

  it('sendUserMessage JSON-encodes payload after open', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    FakeWS.instances[0].onopen?.(new Event('open'));
    client.sendUserMessage({ thread_id: 'thr_1', action_id: 'act_1', content: '你好' });
    expect(JSON.parse(FakeWS.instances[0].sent[0])).toEqual({
      type: 'user_message', thread_id: 'thr_1', action_id: 'act_1', content: '你好',
    });
  });

  it('sendUserMessage forwards mentions and never includes canvas_summary', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    FakeWS.instances[0].onopen?.(new Event('open'));
    client.sendUserMessage({
      thread_id: 't1',
      action_id: 'a1',
      content: 'hello',
      mentions: [{ artifact_id: 'art_1', name: '柴犬', url: 'http://x/1.png' }],
    });
    const sent = JSON.parse(FakeWS.instances[0].sent[0]);
    expect(sent.mentions).toHaveLength(1);
    expect(sent.mentions[0]).toEqual({ artifact_id: 'art_1', name: '柴犬', url: 'http://x/1.png' });
    // canvas_summary 绝对不能出现在传输里——chat 跟画布上下文是隔离的；
    // mention 是我们唯一会发送的信号。
    expect(sent.canvas_summary).toBeUndefined();
  });

  it('sendUserMessage forwards skill_id when provided', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    FakeWS.instances[0].onopen?.(new Event('open'));
    client.sendUserMessage({
      thread_id: 'thr_1', action_id: 'act_1', content: '生成一段视频', skill_id: 'seedance_v2',
    });
    expect(JSON.parse(FakeWS.instances[0].sent[0])).toEqual({
      type: 'user_message', thread_id: 'thr_1', action_id: 'act_1', content: '生成一段视频',
      skill_id: 'seedance_v2',
    });
  });

  it('sendUserMessage returns false and drops payload when socket not open (M2)', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    // 不触发 onopen——readyState 保持 0
    FakeWS.instances[0].readyState = 0;
    // 【M2】必须把「没发出去」告诉调用方——否则输入框被清空、气泡已 append，
    // 用户输入就静默丢了。
    const ok = client.sendUserMessage({ thread_id: 'thr_1', action_id: 'act_1', content: 'x' });
    expect(ok).toBe(false);
    expect(FakeWS.instances[0].sent).toHaveLength(0);
  });

  it('sendUserMessage returns true when the socket is open (M2)', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    FakeWS.instances[0].onopen?.(new Event('open'));
    const ok = client.sendUserMessage({ thread_id: 'thr_1', action_id: 'act_1', content: 'x' });
    expect(ok).toBe(true);
    expect(FakeWS.instances[0].sent).toHaveLength(1);
  });

  it('abort sends abort frame', () => {
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
    client.connect();
    FakeWS.instances[0].onopen?.(new Event('open'));
    client.abort('thr_1', 'act_1');
    expect(JSON.parse(FakeWS.instances[0].sent[0])).toEqual({
      type: 'abort', thread_id: 'thr_1', action_id: 'act_1',
    });
  });

  it('auto-reconnects after an unexpected close', () => {
    vi.useFakeTimers();
    try {
      const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
      client.connect();
      expect(FakeWS.instances).toHaveLength(1);
      // 模拟代理 idle-timeout 掐断（非主动 close）
      FakeWS.instances[0].onclose?.(new CloseEvent('close'));
      // 首次退避 1s 后应建一条新连接
      vi.advanceTimersByTime(1000);
      expect(FakeWS.instances).toHaveLength(2);
      expect(FakeWS.instances[1].url).toBe('/api/v1/agent/ws?token=t');
    } finally {
      vi.useRealTimers();
    }
  });

  it('falls back to a slow retry loop after fast retries are exhausted (M3)', () => {
    vi.useFakeTimers();
    try {
      const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
      client.connect();
      // 连开即断 10 次，把快速退避档（1s→30s 封顶）全部耗尽。
      const fastDelays = [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000, 30000, 30000];
      for (const d of fastDelays) {
        FakeWS.instances[FakeWS.instances.length - 1].onclose?.(new CloseEvent('close'));
        vi.advanceTimersByTime(d);
      }
      expect(FakeWS.instances).toHaveLength(11); // 初始 1 条 + 10 次快速重连
      // 【M3】第 11 次断开：旧实现从此永久死亡（唯一恢复是刷新页面），
      // 新实现降级到每 60s 一次的慢速循环。
      FakeWS.instances[10].onclose?.(new CloseEvent('close'));
      vi.advanceTimersByTime(59_000);
      expect(FakeWS.instances).toHaveLength(11);
      vi.advanceTimersByTime(1_000);
      expect(FakeWS.instances).toHaveLength(12);
      // 慢速循环持续有效，不是只多试一次。
      FakeWS.instances[11].onclose?.(new CloseEvent('close'));
      vi.advanceTimersByTime(60_000);
      expect(FakeWS.instances).toHaveLength(13);
    } finally {
      vi.useRealTimers();
    }
  });

  it('resets the retry counter only after the connection stays open for 30s (L5)', () => {
    vi.useFakeTimers();
    try {
      const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
      client.connect();
      // 【L5】「握手成功即被服务端关闭」：30s 内断开不能清零计数，否则退避
      // 永远停在 1s，变成无限热重连。
      FakeWS.instances[0].onopen?.(new Event('open'));
      FakeWS.instances[0].onclose?.(new CloseEvent('close'));
      vi.advanceTimersByTime(1000); // 第 1 次退避 1s
      expect(FakeWS.instances).toHaveLength(2);
      FakeWS.instances[1].onopen?.(new Event('open'));
      FakeWS.instances[1].onclose?.(new CloseEvent('close'));
      // 计数没被 onopen 清零 → 第 2 次退避是 2s，1s 时不应有新连接。
      vi.advanceTimersByTime(1000);
      expect(FakeWS.instances).toHaveLength(2);
      vi.advanceTimersByTime(1000);
      expect(FakeWS.instances).toHaveLength(3);
      // 这次稳定保持 open 满 30s → 计数清零，下一次断开重新从 1s 快速重试。
      FakeWS.instances[2].onopen?.(new Event('open'));
      vi.advanceTimersByTime(30_000);
      FakeWS.instances[2].onclose?.(new CloseEvent('close'));
      vi.advanceTimersByTime(1000);
      expect(FakeWS.instances).toHaveLength(4);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not reconnect after an intentional close()', () => {
    vi.useFakeTimers();
    try {
      const client = new AgentClient({ url: '/api/v1/agent/ws', token: 't' });
      client.connect();
      client.close(); // 主动关闭 → 不应重连
      vi.advanceTimersByTime(60_000);
      expect(FakeWS.instances).toHaveLength(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
