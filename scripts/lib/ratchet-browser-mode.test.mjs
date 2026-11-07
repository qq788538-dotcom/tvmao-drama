// browser-mode.mjs 的退出码与 stdout 脱敏回归：用假的 sidecar（sh 脚本），不构建、不起 Vite。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const LAUNCHER = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'ratchet', 'browser-mode.mjs');

function fakeSidecar(dir, body) {
  const p = join(dir, 'sidecar.sh');
  // 真 sidecar 先从 stdin 读两行 token
  writeFileSync(p, `#!/bin/sh\nread a\nread b\n${body}\n`);
  chmodSync(p, 0o755);
  return p;
}

/** 起 launcher；onReady 拿到就绪那行后可以给它发信号。返回 { code, stdout, state }。 */
function runLauncher(dir, bin, onReady) {
  const stateFile = join(dir, 'state.json');
  return new Promise((done) => {
    const child = spawn(process.execPath, [LAUNCHER, '--sidecar-bin', bin, '--no-vite', '--state', stateFile], {
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let state = null;
    child.stdout.on('data', (d) => {
      stdout += d;
      if (!state && stdout.includes('"ready":true')) {
        state = JSON.parse(readFileSync(stateFile, 'utf8'));
        onReady?.(child);
      }
    });
    child.on('exit', (code) => done({ code, stdout, state, stateLeft: existsSync(stateFile) }));
  });
}

test('sidecar 没就绪就退出：launcher 退出码非零', { timeout: 20_000 }, async () => {
  const dir = mkdtempSync(join(tmpdir(), 'ratchet-bm-test-'));
  try {
    const r = await runLauncher(dir, fakeSidecar(dir, 'exit 7'));
    assert.notEqual(r.code, 0);
    assert.equal(r.state, null);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('sidecar 就绪后意外退出：launcher 退出码非零，state 文件被删', { timeout: 20_000 }, async () => {
  const dir = mkdtempSync(join(tmpdir(), 'ratchet-bm-test-'));
  try {
    const r = await runLauncher(dir, fakeSidecar(dir, 'echo "READY 32123"\nsleep 1\nexit 7'));
    assert.ok(r.state, '应先就绪');
    assert.notEqual(r.code, 0);
    assert.equal(r.stateLeft, false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('正常 SIGTERM 收尾：退出码 0；stdout 不含网页 token 和 CLI token', { timeout: 20_000 }, async () => {
  const dir = mkdtempSync(join(tmpdir(), 'ratchet-bm-test-'));
  try {
    // 关 stdin 即退出，和真 sidecar 一样
    const r = await runLauncher(dir, fakeSidecar(dir, 'echo "READY 32124"\ncat >/dev/null\nexit 0'), (c) => c.kill('SIGTERM'));
    assert.equal(r.code, 0);
    const token = new URL(r.state.url).searchParams.get('ratchet_token');
    assert.ok(token && token.length >= 32);
    assert.ok(!r.stdout.includes(token));
    assert.ok(!r.stdout.includes(r.state.cliToken));
    assert.equal(r.stateLeft, false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
