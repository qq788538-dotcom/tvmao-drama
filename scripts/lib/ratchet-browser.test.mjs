import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildBrowserUrl, devOrigins, makeTokens, parseArgs, parseReadyLine } from './ratchet-browser.mjs';

test('解析 sidecar 的 READY 行', () => {
  assert.equal(parseReadyLine('READY 51234'), 51234);
  assert.equal(parseReadyLine('READY 51234\r'), 51234);
  assert.equal(parseReadyLine('READY 0'), null);
  assert.equal(parseReadyLine('READY 70000'), null);
  assert.equal(parseReadyLine('starting'), null);
});

test('两个 token 都够长且不同', () => {
  const { token, cliToken } = makeTokens();
  assert.ok(token.length >= 32 && cliToken.length >= 32);
  assert.notEqual(token, cliToken);
});

test('浏览器地址带端口和 token 参数', () => {
  const url = new URL(buildBrowserUrl({ webPort: 5173, sidecarPort: 4321, token: 't'.repeat(64), path: '/canvas/2' }));
  assert.equal(url.origin, 'http://localhost:5173');
  assert.equal(url.pathname, '/canvas/2');
  assert.equal(url.searchParams.get('ratchet_port'), '4321');
  assert.equal(url.searchParams.get('ratchet_token'), 't'.repeat(64));
});

test('Origin 白名单只含本次开发服务器', () => {
  assert.equal(devOrigins(5173), 'http://localhost:5173,http://127.0.0.1:5173');
});

test('参数解析', () => {
  assert.deepEqual(parseArgs([]).webPort, 5173);
  const o = parseArgs(['--data-dir', '/tmp/x', '--web-port', '5200', '--no-vite']);
  assert.equal(o.dataDir, '/tmp/x');
  assert.equal(o.webPort, 5200);
  assert.equal(o.vite, false);
  assert.throws(() => parseArgs(['--web-port', 'abc']), /合法端口/);
  assert.throws(() => parseArgs(['--bogus']), /未知/);
});
