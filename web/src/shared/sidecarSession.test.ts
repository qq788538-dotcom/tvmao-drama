import { afterEach, describe, expect, it } from 'vitest';
import {
  getSidecarSession,
  resetSidecarSessionForTest,
  setSidecarSession,
  sidecarApiBaseUrl,
  sidecarToken,
  sidecarWsUrl,
} from './sidecarSession';

describe('sidecarSession', () => {
  afterEach(() => resetSidecarSessionForTest());

  it('会话未建立时退回参考仓的相对路径、空 token', () => {
    expect(getSidecarSession()).toBeNull();
    expect(sidecarToken()).toBe('');
    expect(sidecarApiBaseUrl()).toBe('/api/v1');
    expect(sidecarWsUrl('/api/v1/agent/ws')).toBe('/api/v1/agent/ws');
  });

  it('建立会话后给出 127.0.0.1 上的 REST / WS 地址和 token', () => {
    setSidecarSession({ port: 51234, token: 'tok' });
    expect(sidecarToken()).toBe('tok');
    expect(sidecarApiBaseUrl()).toBe('http://127.0.0.1:51234/api/v1');
    // WS 地址不带 token：AgentClient 自己拼 ?project_id=&token=
    expect(sidecarWsUrl('/api/v1/agent/ws')).toBe('ws://127.0.0.1:51234/api/v1/agent/ws');
  });
});
