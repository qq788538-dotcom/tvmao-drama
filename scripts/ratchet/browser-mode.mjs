#!/usr/bin/env node
// 浏览器验证模式启动器（棘轮 Ratchet 的量规 Gauge 用）。
//
// macOS 的 Tauri 用 WKWebView，没有 DevTools 协议，自动化验证驱动不了桌面窗口。这里绕开壳：
//   1. 用临时数据目录单独起一个 sidecar（绝不碰日常使用的数据目录），Origin 只放行本次 Vite；
//   2. 起 Vite 开发服务器；
//   3. 打印带 ratchet_port / ratchet_token 参数的地址，前端（web/src/shared/server.ts）据此连上 sidecar。
// 之后用 Playwright 等工具驱动这个地址即可截图、录屏、取 console。
//
// 用法（仓库根目录）：
//   node scripts/ratchet/browser-mode.mjs [--data-dir 目录] [--sidecar-bin 路径] [--web-port 5173] [--no-vite]
// 就绪后在 stdout 打一行 JSON：{"ready":true,"url":...,"endpointDir":...,"sidecarPort":...,"webPort":...,"dataDir":...}，
// endpointDir 给 miaoju 用：`MIAOJU_ENDPOINT_DIR=<endpointDir> miaoju project list`。
// 同时写到 --state 指定的文件（默认 .ratchet/state/browser-mode.json，已在 .gitignore 里）。
// Ctrl-C 或 SIGTERM 退出：关 sidecar（关闭 stdin 即优雅退出）、关 Vite、删掉自建的临时数据目录。
//
// 不计费：只启动服务，不调用任何生成接口。
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import { buildBrowserUrl, devOrigins, makeTokens, parseArgs, parseReadyLine } from '../lib/ratchet-browser.mjs';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const READY_TIMEOUT_MS = 60_000;

function log(msg) {
  process.stderr.write(`[browser-mode] ${msg}\n`);
}

function buildSidecar(outDir) {
  const bin = join(outDir, process.platform === 'win32' ? 'sidecar.exe' : 'sidecar');
  log('构建 sidecar（go build ./cmd/sidecar）…');
  const r = spawnSync('go', ['build', '-o', bin, './cmd/sidecar'], { cwd: join(ROOT, 'server'), stdio: ['ignore', 'inherit', 'inherit'] });
  if (r.status !== 0) throw new Error('sidecar 构建失败');
  return bin;
}

function startSidecar(bin, dataDir, webPort, tokens) {
  return new Promise((resolvePort, reject) => {
    const child = spawn(bin, ['--data-dir', dataDir, '--origins', devOrigins(webPort)], {
      stdio: ['pipe', 'pipe', 'inherit'],
    });
    // sidecar 从 stdin 读两行 token；stdin 一关就退出，所以保持打开
    child.stdin.write(`${tokens.token}\n${tokens.cliToken}\n`);
    const timer = setTimeout(() => reject(new Error(`sidecar ${READY_TIMEOUT_MS / 1000} 秒内没有就绪`)), READY_TIMEOUT_MS);
    const rl = createInterface({ input: child.stdout });
    rl.on('line', (line) => {
      const port = parseReadyLine(line);
      if (port) {
        clearTimeout(timer);
        resolvePort({ child, port });
      }
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      reject(new Error(`sidecar 提前退出（退出码 ${code}）`));
    });
  });
}

async function waitHttp(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url);
      if (r.ok) return;
    } catch {
      // 还没起来
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`${url} ${timeoutMs / 1000} 秒内没有响应`);
}

function startVite(webPort) {
  const child = spawn('npx', ['vite', '--port', String(webPort), '--strictPort', '--host', '127.0.0.1'], {
    cwd: join(ROOT, 'web'),
    stdio: ['ignore', 'ignore', 'inherit'],
    detached: process.platform !== 'win32',
  });
  return child;
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  if (opts.help) {
    process.stdout.write('用法：node scripts/ratchet/browser-mode.mjs [--data-dir 目录] [--sidecar-bin 路径] [--web-port 5173] [--no-vite] [--state 文件]\n');
    return;
  }
  const work = mkdtempSync(join(tmpdir(), 'ratchet-browser-'));
  const ownDataDir = !opts.dataDir;
  const dataDir = opts.dataDir ? resolve(opts.dataDir) : join(work, 'data');
  const tokens = makeTokens();
  const children = [];

  let cleaned = false;
  const cleanup = () => {
    if (cleaned) return;
    cleaned = true;
    for (const c of children) {
      try {
        if (c.stdin) c.stdin.end(); // sidecar：关 stdin 即优雅退出
        else if (c.pid && process.platform !== 'win32') process.kill(-c.pid, 'SIGTERM'); // vite 进程组
        else c.kill('SIGTERM');
      } catch {
        // 已退出
      }
    }
    // 给 sidecar 留时间关库后再删临时目录
    setTimeout(() => {
      rmSync(work, { recursive: true, force: true });
      process.exit(0);
    }, 3000);
  };
  process.on('SIGINT', cleanup);
  process.on('SIGTERM', cleanup);

  try {
    const bin = opts.sidecarBin ? resolve(opts.sidecarBin) : buildSidecar(work);
    const { child: sidecar, port: sidecarPort } = await startSidecar(bin, dataDir, opts.webPort, tokens);
    children.push(sidecar);
    log(`sidecar 就绪：127.0.0.1:${sidecarPort}，数据目录 ${dataDir}`);

    if (opts.vite) {
      children.push(startVite(opts.webPort));
      await waitHttp(`http://127.0.0.1:${opts.webPort}/`, 60_000);
      log(`Vite 就绪：http://localhost:${opts.webPort}`);
    }

    // 给 miaoju CLI 写一份地址文件（格式同 cli/internal/endpoint），用 MIAOJU_ENDPOINT_DIR 指过来，
    // CLI 就连这个临时 sidecar，而不是用户正在用的喵剧。
    const endpointDir = join(work, 'endpoint');
    mkdirSync(endpointDir, { recursive: true, mode: 0o700 });
    writeFileSync(
      join(endpointDir, 'cli-endpoint.json'),
      JSON.stringify({ version: 1, port: sidecarPort, token: tokens.cliToken, appVersion: 'ratchet-browser-mode' }) + '\n',
      { mode: 0o600 },
    );

    const state = {
      ready: true,
      endpointDir,
      url: buildBrowserUrl({ webPort: opts.webPort, sidecarPort, token: tokens.token }),
      sidecarPort,
      webPort: opts.webPort,
      dataDir,
      ownDataDir,
      pid: process.pid,
    };
    const statePath = resolve(ROOT, opts.stateFile);
    mkdirSync(dirname(statePath), { recursive: true });
    // 地址里带网页 token：只写进不入库的 state 文件，权限 0600
    writeFileSync(statePath, JSON.stringify({ ...state, cliToken: tokens.cliToken }, null, 2) + '\n', { mode: 0o600 });
    process.stdout.write(JSON.stringify(state) + '\n');
    log('按 Ctrl-C 结束并清理');
    sidecar.on('exit', (code) => {
      log(`sidecar 退出（${code}）`);
      cleanup();
    });
  } catch (e) {
    log(String(e instanceof Error ? e.message : e));
    cleanup();
    process.exitCode = 1;
  }
}

main();
