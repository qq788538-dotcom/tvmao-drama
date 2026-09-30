// 前端与本地 sidecar 之间的唯一入口：端口和 token 只从这里取。
// sidecar 的端口每次启动都不同，所以**任何地方都不要把拼好的地址存起来**
// ——存进画布的话，重启后全部失效（spec S3）。
import { invoke } from '@tauri-apps/api/core';

export interface ServerInfo {
  port: number;
  token: string;
}

let cached: Promise<ServerInfo> | null = null;

/** 取 sidecar 的端口和 token。成功后缓存；失败不缓存，下次调用会重试。 */
export function getServerInfo(): Promise<ServerInfo> {
  if (!cached) {
    const pending = invoke<ServerInfo>('server_info');
    cached = pending;
    pending.catch(() => {
      if (cached === pending) cached = null;
    });
  }
  return cached;
}

export function buildHttpUrl(info: ServerInfo, path: string): string {
  return `http://127.0.0.1:${info.port}${path}`;
}

function withToken(url: string, token: string): string {
  return `${url}${url.includes('?') ? '&' : '?'}token=${encodeURIComponent(token)}`;
}

/** <img>/<video> 和 mediabunny 用的地址：它们带不了 Authorization 头，token 放查询参数。 */
export function buildMediaUrl(info: ServerInfo, path: string): string {
  return withToken(buildHttpUrl(info, path), info.token);
}

/** 浏览器没法给 WebSocket 握手设请求头，token 同样放查询参数。 */
export function buildWsUrl(info: ServerInfo, path: string): string {
  return withToken(`ws://127.0.0.1:${info.port}${path}`, info.token);
}

export function authHeaders(info: ServerInfo): { Authorization: string } {
  return { Authorization: `Bearer ${info.token}` };
}

/** 仅供测试清缓存。 */
export function resetServerInfoForTest(): void {
  cached = null;
}
