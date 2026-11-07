// 浏览器验证模式的纯函数部分（scripts/ratchet/browser-mode.mjs 用），单独放这里便于 node --test。
import { randomBytes } from 'node:crypto';

/** sidecar 就绪时在 stdout 打一行 `READY <端口>`（server/cmd/sidecar/main.go）。 */
export function parseReadyLine(line) {
  const m = /^READY (\d{1,5})\s*$/.exec(line);
  if (!m) return null;
  const port = Number(m[1]);
  return port > 0 && port < 65536 ? port : null;
}

/** sidecar 要求 token 至少 32 个字符，网页 token 与 CLI token 不能相同。 */
export function makeTokens() {
  const token = randomBytes(32).toString('hex');
  let cliToken = randomBytes(32).toString('hex');
  while (cliToken === token) cliToken = randomBytes(32).toString('hex');
  return { token, cliToken };
}

/** 前端（web/src/shared/server.ts 的 browserSession）读这两个参数。 */
export function buildBrowserUrl({ webPort, sidecarPort, token, path = '/' }) {
  const u = new URL(path, `http://localhost:${webPort}`);
  u.searchParams.set('ratchet_port', String(sidecarPort));
  u.searchParams.set('ratchet_token', token);
  return u.toString();
}

/** sidecar 的 Origin 白名单：只放行本次 Vite 开发服务器。 */
export function devOrigins(webPort) {
  return [`http://localhost:${webPort}`, `http://127.0.0.1:${webPort}`].join(',');
}

const FLAGS = { '--data-dir': 'dataDir', '--sidecar-bin': 'sidecarBin', '--web-port': 'webPort', '--state': 'stateFile' };

export function parseArgs(argv) {
  const opts = { dataDir: null, sidecarBin: null, webPort: 5173, stateFile: '.ratchet/state/browser-mode.json', vite: true };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--no-vite') {
      opts.vite = false;
      continue;
    }
    if (a === '-h' || a === '--help') {
      opts.help = true;
      continue;
    }
    const key = FLAGS[a];
    if (!key || i + 1 >= argv.length) throw new Error(`未知或缺少值的参数：${a}`);
    opts[key] = argv[++i];
  }
  opts.webPort = Number(opts.webPort);
  if (!Number.isInteger(opts.webPort) || opts.webPort <= 0 || opts.webPort >= 65536) {
    throw new Error('--web-port 不是合法端口');
  }
  return opts;
}

/**
 * 把文本里的 token 换成 ***：既替换给定的 token 原文，也替换任何 `ratchet_token=…` / `token=…` 查询参数。
 * 证据文件、stdout、错误信息出门前都要过一遍（Playwright 的导航错误会带完整 URL）。
 */
export function redact(text, ...tokens) {
  let s = String(text);
  for (const t of tokens) if (t) s = s.split(t).join('***');
  return s.replace(/((?:ratchet_)?token=)[^&\s"']+/g, '$1***');
}

/** 给终端/日志看的就绪摘要：url 去掉 token，完整地址只在 0600 的 state 文件里。 */
export function publicState(state) {
  return { ...state, url: redact(state.url) };
}
