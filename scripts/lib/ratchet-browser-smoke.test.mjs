// browser-smoke.mjs 回归：页面网络一直不空闲时也要能判完。
// 真实情况是画布的 EventSource 长连接（/events）在 macOS 上让 networkidle 永远等不到；
// Linux 的 Chromium 不把 EventSource 算进 networkidle，复现不了，所以假页面再加一个每 200ms 的轮询，
// 让“网络永不空闲”在任何平台都成立。用一个本地假服务同时当前端和 sidecar，不构建、不起 Vite。没装 Playwright 时跳过。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const SMOKE = join(ROOT, 'scripts', 'ratchet', 'browser-smoke.mjs');
const TASK = 'T-9990';

let hasPlaywright = true;
try {
  createRequire(import.meta.url)('playwright');
} catch {
  hasPlaywright = false;
}
const skip = hasPlaywright ? false : '没装 playwright';

/** 假前端 + 假 sidecar：页面先拉一个 API，再开一个永不结束的 EventSource 和一个轮询，最后画 nodes 个节点。 */
function fakeApp(nodes) {
  const streams = [];
  const server = createServer((req, res) => {
    const u = new URL(req.url, 'http://x');
    if (u.pathname === '/api/v1/projects/p1/events') {
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
      res.write(': hi\n\n');
      streams.push(res);
      return;
    }
    if (u.pathname.startsWith('/api/')) {
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{"ok":true}');
      return;
    }
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(`<!doctype html><body><div id="c"></div><script>
      fetch('/api/v1/projects/p1/canvas').then(() => {
        new EventSource('/api/v1/projects/p1/events');
        setInterval(() => fetch('/api/v1/ping').catch(() => {}), 200);
        setTimeout(() => {
          for (let i = 0; i < ${nodes}; i++) {
            const d = document.createElement('div');
            d.setAttribute('data-node-id', 'n' + i);
            d.textContent = 'node ' + i;
            document.getElementById('c').appendChild(d);
          }
        }, 300);
      });
    </script></body>`);
  });
  return new Promise((ok) =>
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      const close = () => new Promise((done) => {
        for (const s of streams) s.destroy();
        server.closeAllConnections?.();
        server.close(done);
      });
      ok({ port, close });
    }),
  );
}

function runSmoke(port, extra) {
  const dir = mkdtempSync(join(tmpdir(), 'ratchet-smoke-'));
  const stateFile = join(dir, 'state.json');
  writeFileSync(
    stateFile,
    JSON.stringify({ url: `http://127.0.0.1:${port}/?ratchet_port=${port}&ratchet_token=tok123`, sidecarPort: port, cliToken: 'cli456' }),
  );
  return new Promise((done) => {
    const child = spawn(process.execPath, [SMOKE, '--task', TASK, '--sub', 'test', '--state', stateFile, ...extra], {
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    child.on('exit', (code) => {
      rmSync(dir, { recursive: true, force: true });
      let summary = null;
      try {
        summary = JSON.parse(readFileSync(join(ROOT, '.ratchet', 'evidence', TASK, 'test', 'smoke.json'), 'utf8'));
      } catch {
        // 没写出来
      }
      rmSync(join(ROOT, '.ratchet', 'evidence', TASK), { recursive: true, force: true });
      done({ code, stdout, stderr, summary });
    });
  });
}

test('页面网络永不空闲（EventSource + 轮询）：等到节点数对上就判过，不卡在 networkidle', { skip, timeout: 40_000 }, async () => {
  const app = await fakeApp(2);
  try {
    const r = await runSmoke(app.port, ['--path', '/canvas/p1', '--wait-selector', '[data-node-id]', '--expect-count', '2']);
    assert.equal(r.code, 0, r.stdout + r.stderr);
    assert.equal(r.summary.ok, true);
    assert.equal(r.summary.waitFor.count, 2);
    assert.doesNotMatch(r.stdout + JSON.stringify(r.summary), /tok123|cli456/);
  } finally {
    await app.close();
  }
});

test('节点数和预期不一致：超时后判失败并写明原因', { skip, timeout: 40_000 }, async () => {
  const app = await fakeApp(1);
  try {
    const r = await runSmoke(app.port, ['--wait-selector', '[data-node-id]', '--expect-count', '2', '--timeout', '4000']);
    assert.equal(r.code, 1, r.stdout + r.stderr);
    assert.equal(r.summary.ok, false);
    assert.match(r.summary.reason, /\[data-node-id\].*2.*1/);
  } finally {
    await app.close();
  }
});

test('不给等待条件：DOM 就绪 + 有成功的 sidecar 请求就判过', { skip, timeout: 40_000 }, async () => {
  const app = await fakeApp(0);
  try {
    const r = await runSmoke(app.port, []);
    assert.equal(r.code, 0, r.stdout + r.stderr);
    assert.ok(r.summary.apiCalls.some((c) => c.status === 200));
  } finally {
    await app.close();
  }
});
