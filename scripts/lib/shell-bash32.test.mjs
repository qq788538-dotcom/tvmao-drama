// macOS 自带的 bash 3.2 会把紧跟在 $VAR 后面的非 ASCII 字节（如中文全角逗号）当成变量名的一部分，
// 配合 set -u 直接报 unbound variable（pi 在 tvmao-drama #36 复验 verify-f1.sh 时撞到）。
// 规则：.sh 里 $VAR 后面紧跟非 ASCII 字符时，必须写成 ${VAR}。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

function shFiles(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...shFiles(p));
    else if (name.endsWith('.sh')) out.push(p);
  }
  return out;
}

function bareVarsBeforeNonAscii(text) {
  const hits = [];
  text.split('\n').forEach((line, i) => {
    for (const m of line.matchAll(/\$([A-Za-z_][A-Za-z0-9_]*)(?=[^\x00-\x7f])/g)) hits.push(`${i + 1}: $${m[1]}`);
  });
  return hits;
}

test('规则本身：$VAR 后接中文要报，${VAR} 和后接 ASCII 的不报', () => {
  assert.deepEqual(bareVarsBeforeNonAscii('echo "项目 $PID，节点"'), ['1: $PID']);
  assert.deepEqual(bareVarsBeforeNonAscii('echo "项目 ${PID}，节点 $IMG → x"'), []);
});

test('scripts/ 下所有 .sh 在 bash 3.2 下不会把中文吃进变量名', () => {
  const bad = [];
  for (const f of shFiles(join(ROOT, 'scripts'))) {
    for (const h of bareVarsBeforeNonAscii(readFileSync(f, 'utf8'))) bad.push(`${relative(ROOT, f)}:${h}`);
  }
  assert.deepEqual(bad, [], '改成 ${VAR}：\n' + bad.join('\n'));
});
