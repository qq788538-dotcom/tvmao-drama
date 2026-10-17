import type { Frame, Mention } from './types';

export interface AgentClientOptions {
  /** 完整 WS 地址。桌面端由 buildWsUrl 生成，已带 ?token=。 */
  url: string;
  /** 旧写法：单独传 token 由这里拼进查询参数。桌面端不传。 */
  token?: string;
  projectId?: number;
  /**
   * 桌面端：自动重连前重新取 WS 地址（不带 token）和 token。sidecar 重启后端口和
   * token 都会变，沿用构造时的旧值只会一直连不上。失败（sidecar 还在重启）时按退避重试。
   */
  resolveEndpoint?: () => Promise<{ url: string; token: string }>;
}

type FrameHandler = (f: Frame) => void;

// 快速退避的次数上限（≈3.5min 耗尽）；耗尽后进入慢速循环的间隔。
const FAST_RETRY_LIMIT = 10;
const SLOW_RETRY_MS = 60_000;
// 连接保持 open 多久才把重试计数清零（见 stableOpenTimer）。
const STABLE_OPEN_MS = 30_000;

// AgentClient 是对浏览器 WebSocket 的薄封装，处理 /api/v1/agent/ws 用的
// 「token 放 query string」鉴权方式（桌面端由 buildWsUrl 负责；浏览器没法在 WS 握手时设 Authorization 头）。
export class AgentClient {
  private opts: AgentClientOptions;
  private ws?: WebSocket;
  private handlers: FrameHandler[] = [];
  private openHandlers: (() => void)[] = [];
  private closeHandlers: (() => void)[] = [];
  // 自动重连：线上 WS 被反向代理 idle-timeout 掐断时，后端 Run 仍在
  // context.Background() 上活着、sink 是用户级广播——只要客户端重新连上，剩余
  // 帧就会自动续到新连接。没有重连的话，掉一次线就再也收不到 action_end，前端
  // 永久转圈。intentionalClose 区分用户主动关闭（不该重连）。
  private intentionalClose = false;
  private reconnectAttempts = 0;
  private reconnectTimer?: ReturnType<typeof setTimeout>;
  // 【L5】重试计数不能在 onopen 立即清零：「握手成功即被服务端关闭」型故障
  //（如后端启动中、代理返回后立刻断）会变成 ~1s 间隔的无限热重连。改成连接
  // 稳定保持 open 一段时间后才算「恢复正常」，届时才把计数清零。
  private stableOpenTimer?: ReturnType<typeof setTimeout>;

  constructor(opts: AgentClientOptions) {
    this.opts = opts;
  }

  connect() {
    const params = new URLSearchParams();
    if (this.opts.projectId && this.opts.projectId > 0) {
      params.set('project_id', String(this.opts.projectId));
    }
    if (this.opts.token) params.set('token', this.opts.token);
    const sep = this.opts.url.includes('?') ? '&' : '?';
    const qs = params.toString();
    const url = qs ? `${this.opts.url}${sep}${qs}` : this.opts.url;
    this.ws = new WebSocket(url);
    this.ws.onopen = () => {
      // 见 stableOpenTimer 的注释：保持 open 30s 才清零重试计数，防热重连风暴。
      this.stableOpenTimer = setTimeout(() => {
        this.reconnectAttempts = 0;
        this.stableOpenTimer = undefined;
      }, STABLE_OPEN_MS);
      this.openHandlers.forEach((h) => h());
    };
    this.ws.onmessage = (e) => {
      try {
        const f: Frame = JSON.parse(e.data);
        this.handlers.forEach((h) => h(f));
      } catch {
        // 忽略无法解析的 frame
      }
    };
    this.ws.onclose = () => {
      // 连接没撑过稳定期就断了 → 撤销「即将清零计数」，让退避继续按次数增长。
      if (this.stableOpenTimer) { clearTimeout(this.stableOpenTimer); this.stableOpenTimer = undefined; }
      this.closeHandlers.forEach((h) => h());
      if (!this.intentionalClose) this.scheduleReconnect();
    };
  }

  // scheduleReconnect 用指数退避重连，封顶 30s。前 FAST_RETRY_LIMIT 次是快速退避；
  // 【M3】耗尽后不放弃——降级为每 60s 一次的慢速循环。持续断网（合盖休眠、地铁）
  // ≈3.5min 就能耗尽快速档，若就此永久死亡，唯一恢复手段是刷新页面；慢速循环让
  // 网络恢复后连接最多 60s 内自愈，期间 connectionState 保持 closed，UI 能如实
  // 显示「连接已断开」。
  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    const delay =
      this.reconnectAttempts >= FAST_RETRY_LIMIT
        ? SLOW_RETRY_MS
        : Math.min(30_000, 1000 * 2 ** this.reconnectAttempts);
    this.reconnectAttempts += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      if (this.intentionalClose) return;
      const resolve = this.opts.resolveEndpoint;
      if (!resolve) {
        this.connect();
        return;
      }
      resolve().then(
        ({ url, token }) => {
          if (this.intentionalClose) return;
          this.opts = { ...this.opts, url, token };
          this.connect();
        },
        () => {
          if (!this.intentionalClose) this.scheduleReconnect();
        },
      );
    }, delay);
  }

  close() {
    this.intentionalClose = true;
    if (this.reconnectTimer) { clearTimeout(this.reconnectTimer); this.reconnectTimer = undefined; }
    if (this.stableOpenTimer) { clearTimeout(this.stableOpenTimer); this.stableOpenTimer = undefined; }
    this.ws?.close();
  }

  onFrame(h: FrameHandler) { this.handlers.push(h); }
  onOpen(h: () => void) { this.openHandlers.push(h); }
  onClose(h: () => void) { this.closeHandlers.push(h); }

  // 【M2】返回是否真的发出。WS 非 OPEN 时消息根本没上网，若静默吞掉，调用方
  // 已经清空输入框、append 了本地气泡——用户输入就彻底丢了。调用方必须先拿到
  // true 再做这些副作用。
  sendUserMessage(p: {
    thread_id: string;
    action_id: string;
    content: string;
    mentions?: Mention[];
    skill_id?: string;
  }): boolean {
    return this.send({ type: 'user_message', ...p });
  }

  abort(thread_id: string, action_id: string) {
    this.send({ type: 'abort', thread_id, action_id });
  }

  private send(f: Frame): boolean {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(f));
      return true;
    }
    return false;
  }
}
