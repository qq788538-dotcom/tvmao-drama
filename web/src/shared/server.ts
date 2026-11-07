// 前端与本地 sidecar 之间的唯一入口：端口和 token 只从这里取。
// sidecar 的端口每次启动都不同，所以**任何地方都不要把拼好的地址存起来**
// ——存进画布的话，重启后全部失效。
import { invoke as tauriInvoke } from '@tauri-apps/api/core';
import { listen as tauriListen, type UnlistenFn } from '@tauri-apps/api/event';

/** 不在 Tauri 壳里（纯浏览器调试）：没有 sidecar 可连。 */
export class NotTauriError extends Error {
  constructor() {
    super('请在桌面应用中运行');
    this.name = 'NotTauriError';
  }
}

function isTauri(): boolean {
  return typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
}

/*
 * 浏览器验证模式（只在开发构建生效，生产包里 import.meta.env.DEV 被静态替换成 false，整段被删掉）。
 *
 * macOS 的 Tauri 用 WKWebView，没有 DevTools 协议，自动化验证驱动不了桌面窗口。
 * 所以由 scripts/ratchet/browser-mode.mjs 单独起一个 sidecar（临时数据目录），再起 Vite，
 * 用 `http://localhost:5173/?ratchet_port=<端口>&ratchet_token=<token>` 打开。
 * 这里读到这两个参数后存进 sessionStorage 并从地址栏去掉，之后的路由跳转、刷新都沿用。
 * 只替代"取端口和 token"和"sidecar 状态"两件事；其余壳命令（数据目录、对话框等）照旧报“请在桌面应用中运行”。
 */
const BROWSER_SESSION_KEY = 'ratchet.browserSession';
const BROWSER_PARAMS = ['ratchet_port', 'ratchet_token'] as const;

function validSession(v: unknown): ServerInfo | null {
  if (!v || typeof v !== 'object') return null;
  const { port, token } = v as Partial<ServerInfo>;
  if (typeof port !== 'number' || !Number.isInteger(port) || port <= 0 || port >= 65536) return null;
  if (typeof token !== 'string' || token.length < 32) return null;
  return { port, token };
}

/** 浏览器验证模式的会话；不在该模式（或是生产构建）返回 null。 */
export function browserSession(): ServerInfo | null {
  if (!import.meta.env.DEV || typeof window === 'undefined' || isTauri()) return null;
  try {
    const url = new URL(window.location.href);
    if (url.searchParams.has('ratchet_port') || url.searchParams.has('ratchet_token')) {
      const info = validSession({
        port: Number(url.searchParams.get('ratchet_port')),
        token: url.searchParams.get('ratchet_token') ?? '',
      });
      for (const k of BROWSER_PARAMS) url.searchParams.delete(k);
      window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash);
      if (info) {
        window.sessionStorage.setItem(BROWSER_SESSION_KEY, JSON.stringify(info));
        return info;
      }
    }
    const saved = window.sessionStorage.getItem(BROWSER_SESSION_KEY);
    return saved ? validSession(JSON.parse(saved)) : null;
  } catch {
    return null;
  }
}

function invoke<T>(...a: [cmd: string, args?: Record<string, unknown>]): Promise<T> {
  if (isTauri()) return tauriInvoke<T>(...a);
  const session = browserSession();
  if (session) {
    const [cmd] = a;
    // 浏览器模式下 sidecar 由启动脚本管，前端无法重启它：restart 直接返回当前会话
    if (cmd === 'server_info' || cmd === 'restart_sidecar') return Promise.resolve(session as T);
    if (cmd === 'sidecar_status') return Promise.resolve({ status: 'ready' } as T);
  }
  return Promise.reject(new NotTauriError());
}

function listen<T>(name: string, cb: (e: { payload: T }) => void): Promise<UnlistenFn> {
  // 纯浏览器没有事件通道：返回空的 unlisten，不 reject
  return isTauri() ? tauriListen<T>(name, cb) : Promise.resolve(() => undefined);
}

export interface ServerInfo {
  port: number;
  token: string;
}

let cached: Promise<ServerInfo> | null = null;
let invalidatorRegistered = false;

/**
 * 全局监听状态事件、清端口缓存，不依赖任何组件挂载：sidecar 自动重启后端口和 token 都会变，
 * Gate 之外（或 Gate 挂载之前）缓存过的调用方下次也能拿到新值。第一次写缓存时注册，只注册一次。
 */
function ensureCacheInvalidator(): void {
  if (invalidatorRegistered) return;
  invalidatorRegistered = true;
  try {
    listen(SIDECAR_STATE_EVENT, () => {
      cached = null;
    }).catch(() => {
      invalidatorRegistered = false;
    });
  } catch {
    // 非 Tauri 环境（纯浏览器调试）没有事件通道
    invalidatorRegistered = false;
  }
}

/** 记入缓存；失败时清掉，下次调用会重试。 */
function track(pending: Promise<ServerInfo>): Promise<ServerInfo> {
  ensureCacheInvalidator();
  cached = pending;
  pending.catch(() => {
    if (cached === pending) cached = null;
  });
  return pending;
}

/** 取 sidecar 的端口和 token。sidecar 启动中时会等到就绪或失败。成功后缓存；失败不缓存。 */
export function getServerInfo(): Promise<ServerInfo> {
  return cached ?? track(invoke<ServerInfo>('server_info'));
}

/** sidecar 状态，与 src-tauri/src/lifecycle.rs 的 SidecarStatus 一致（不含 token）。 */
export type SidecarStatus =
  | { status: 'starting' }
  /** 启动前正在迁移数据目录（设置页更改数据目录后重启的那一次）。waiting：在等上一次迁移结束。 */
  | { status: 'migrating'; waiting?: boolean }
  | { status: 'ready' }
  | { status: 'failed'; reason: string };

/** 壳广播 sidecar 状态变化的事件名，与 src-tauri/src/lib.rs 的 SIDECAR_STATE_EVENT 一致。 */
export const SIDECAR_STATE_EVENT = 'sidecar-state';

export function getSidecarStatus(): Promise<SidecarStatus> {
  return invoke<SidecarStatus>('sidecar_status');
}

/** 重启 sidecar（失败页“重试”、切换数据目录后）。端口和 token 都会变，缓存随之更新。 */
export function restartSidecar(): Promise<ServerInfo> {
  return track(invoke<ServerInfo>('restart_sidecar'));
}

/** 订阅 sidecar 状态变化。任何变化都清掉端口缓存：重启后端口和 token 都会变。 */
export function onSidecarStatus(cb: (s: SidecarStatus) => void): Promise<UnlistenFn> {
  return listen<SidecarStatus>(SIDECAR_STATE_EVENT, (e) => {
    cached = null;
    cb(e.payload);
  });
}

/** 与 src-tauri/src/data_dir.rs 的 DataDirInfo 一致。 */
export interface DataDirInfo {
  current: string;
  default: string;
  custom: boolean;
}

/** 当前数据目录。location.json 损坏时 reject（带原因）。 */
export function getDataDir(): Promise<DataDirInfo> {
  return invoke<DataDirInfo>('get_data_dir');
}

/**
 * 失败页“改用默认数据目录”：删掉自定义目录配置再重启 sidecar。
 * 只改用哪个目录，不搬、不删原目录里的数据。
 */
export async function switchToDefaultDataDir(): Promise<ServerInfo> {
  try {
    await invoke<DataDirInfo>('set_data_dir', { path: null });
  } catch (e) {
    throw new DataDirSwitchError(String(e));
  }
  return restartSidecar();
}

/** 切换配置这一步失败（此时没有重启）。重启失败则照常是 restart_sidecar 的原因字符串，由失败页显示。 */
export class DataDirSwitchError extends Error {
  constructor(reason: string) {
    super(`无法切换到默认数据目录：${reason}`);
    this.name = 'DataDirSwitchError';
  }
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

/** EventSource 同样带不了请求头，token 放查询参数。 */
export function buildEventSourceUrl(info: ServerInfo, path: string): string {
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
  invalidatorRegistered = false;
  try {
    window.sessionStorage.removeItem(BROWSER_SESSION_KEY);
  } catch {
    // 非浏览器环境
  }
}
