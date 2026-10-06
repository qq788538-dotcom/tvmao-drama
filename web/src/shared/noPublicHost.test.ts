// 守护：前端源码里不得写死任何公网地址（spec S12 未定、E7 字体本地化、SEC4 CSP）。
// 媒体和接口地址一律由 sidecar 会话给出；需要外链的功能（如 F7 角色库图片）把
// 地址写进配置，而不是写进前端代码。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');
// 被引号/括号直接包住的 http(s) 字面量；注释里的说明性 URL 不算。
const URL_LITERAL = /['"`(]https?:\/\/(?!127\.0\.0\.1[:/]|localhost[:/]|www\.w3\.org\/)[^'"`)\s]+/g;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|css)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

describe('前端源码不写死公网地址', () => {
  it('src 下非测试文件没有公网 URL 字面量', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const text = readFileSync(file, 'utf8');
      for (const m of text.matchAll(URL_LITERAL)) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
