// 本次启动的 sidecar 会话（端口 + token）的同步入口。
//
// getServerInfo()（./server.ts）是异步的，而 axios 拦截器、AgentClient 构造这些地方
// 需要同步拿到地址。所以 main.tsx 在渲染任何页面之前先 await 一次 getServerInfo()，
// 成功后调 setSidecarSession() 存进这里，之后全局同步读取。
//
// 只存在内存里：端口和 token 每次启动都会变，**不要**把这里拼出来的地址写进画布
// 或 localStorage（spec S3）。
import { buildHttpUrl, type ServerInfo } from './server';

let session: ServerInfo | null = null;

export function setSidecarSession(info: ServerInfo | null): void {
  session = info;
}

export function getSidecarSession(): ServerInfo | null {
  return session;
}

/** 本次启动的 token；会话未建立（单元测试、纯浏览器调试）时为空串。 */
export function sidecarToken(): string {
  return session?.token ?? '';
}

/**
 * REST 接口的 baseURL。会话未建立时退回参考仓的相对路径 `/api/v1`，
 * 让不关心 sidecar 的单元测试保持原样。
 */
export function sidecarApiBaseUrl(): string {
  return session ? buildHttpUrl(session, '/api/v1') : '/api/v1';
}

/**
 * WebSocket 地址，**不带 token**：AgentClient 会自己把 token 和 project_id 拼进
 * 查询串（参考仓的约定），这里再拼一次就重复了。会话未建立时原样返回 path。
 */
export function sidecarWsUrl(path: string): string {
  return session ? `ws://127.0.0.1:${session.port}${path}` : path;
}

/** 仅供测试清状态。 */
export function resetSidecarSessionForTest(): void {
  session = null;
}
