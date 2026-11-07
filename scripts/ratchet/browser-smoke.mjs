#!/usr/bin/env node
// 浏览器验证模式的冒烟取证：打开 browser-mode.mjs 给出的地址，确认前端连上了 sidecar，
// 截图并记录 console 与页面错误，证据写进 .ratchet/evidence/<任务号>/。
//
// 用法（先在另一个终端跑 browser-mode.mjs）：
//   node scripts/ratchet/browser-smoke.mjs --task T-0001 [--path /] [--sub f1] [--state .ratchet/state/browser-mode.json]
//   --sub：证据写进 .ratchet/evidence/<任务号>/<sub>/，避免和同一任务的其他冒烟互相覆盖。
// 需要 Playwright：仓库不把它列为依赖，先 `npm i -g playwright && npx playwright install chromium`。
// 退出码：0 = 页面连上 sidecar 且无页面错误；1 = 失败（原因见 stderr 和 evidence 里的 smoke.json）。
// 不计费：只打开页面、截图，不点击任何生成按钮。
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

function arg(name, fallback) {
  const i = process.argv.indexOf(name);
  return i > 0 && i + 1 < process.argv.length ? process.argv[i + 1] : fallback;
}

function loadPlaywright() {
  const require = createRequire(import.meta.url);
  for (const base of [ROOT, process.cwd(), process.env.NODE_PATH].filter(Boolean)) {
    try {
      return require(require.resolve('playwright', { paths: [base] }));
    } catch {
      // 试下一个
    }
  }
  try {
    return require('playwright');
  } catch {
    throw new Error('找不到 playwright：npm i -g playwright && npx playwright install chromium');
  }
}

async function main() {
  const task = arg('--task');
  if (!task || !/^T-\d{4,}$/.test(task)) throw new Error('缺少 --task T-0001');
  const state = JSON.parse(readFileSync(resolve(ROOT, arg('--state', '.ratchet/state/browser-mode.json')), 'utf8'));
  const url = new URL(state.url);
  url.pathname = arg('--path', '/');
  const sub = arg('--sub', '');
  if (sub && !/^[A-Za-z0-9_-]+$/.test(sub)) throw new Error('--sub 只能是字母、数字、- 和 _');
  const out = resolve(ROOT, '.ratchet', 'evidence', task, sub);
  mkdirSync(out, { recursive: true });

  const { chromium } = loadPlaywright();
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const consoleLines = [];
  const pageErrors = [];
  const apiCalls = [];
  page.on('console', (m) => consoleLines.push(`[${m.type()}] ${m.text()}`));
  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('response', (r) => {
    if (r.url().includes(`127.0.0.1:${state.sidecarPort}/api/`)) apiCalls.push({ url: r.url().replace(/token=[^&]+/, 'token=***'), status: r.status() });
  });

  let ok = true;
  let reason = '';
  try {
    await page.goto(url.toString(), { waitUntil: 'networkidle', timeout: 60_000 });
    const body = await page.locator('body').innerText();
    if (/请在桌面应用中运行/.test(body)) {
      ok = false;
      reason = '前端没有进入浏览器验证模式（仍显示“请在桌面应用中运行”）';
    } else if (!apiCalls.some((c) => c.status < 400)) {
      ok = false;
      reason = '没有观察到成功的 sidecar API 请求';
    }
  } catch (e) {
    ok = false;
    reason = String(e);
  }
  if (pageErrors.length) {
    ok = false;
    reason = reason || `页面有 ${pageErrors.length} 个未捕获错误`;
  }
  await page.screenshot({ path: join(out, 'smoke.png'), fullPage: false });
  await browser.close();

  writeFileSync(join(out, 'console.log'), consoleLines.join('\n') + '\n');
  const summary = { ok, reason, path: url.pathname, apiCalls, pageErrors, screenshot: 'smoke.png' };
  writeFileSync(join(out, 'smoke.json'), JSON.stringify(summary, null, 2) + '\n');
  process.stdout.write(JSON.stringify({ ok, reason, evidence: out }) + '\n');
  if (!ok) process.exitCode = 1;
}

main().catch((e) => {
  process.stderr.write(`[browser-smoke] ${e instanceof Error ? e.message : e}\n`);
  process.exitCode = 1;
});
