// 守护：CSP 的 img-src / media-src 放行 https:（上游外链回退要能预览），
// connect-src 不放行 https（接口只走 127.0.0.1 sidecar）。
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const conf = JSON.parse(
  readFileSync(join(__dirname, '../../../src-tauri/tauri.conf.json'), 'utf8'),
) as { app: { security: { csp: string } } };

function directive(name: string): string[] {
  const part = conf.app.security.csp
    .split(';')
    .map((s) => s.trim())
    .find((s) => s.startsWith(`${name} `));
  expect(part, `缺少 ${name}`).toBeTruthy();
  return part!.split(/\s+/).slice(1);
}

describe('tauri CSP', () => {
  it('img-src 与 media-src 放行 https:', () => {
    expect(directive('img-src')).toContain('https:');
    expect(directive('media-src')).toContain('https:');
  });
  it('connect-src 不放行 https', () => {
    const c = directive('connect-src');
    expect(c).not.toContain('https:');
    expect(c.some((v) => v.startsWith('https://'))).toBe(false);
  });
});
