// 守护：除 shared/saveToDisk.ts（非 Tauri 回退）外，前端不得再用 <a download> / file-saver / window.open(blob)
// 触发下载。Tauri（wry 0.57）没注册 on_download 时 macOS WebView 会直接取消这类下载，点了没反应。
// 一律改用 saveMediaToDisk / saveBlobToDisk / saveManyMediaToDisk。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');
const ALLOWED = new Set(['shared/saveToDisk.ts']);
const PATTERNS = [
  /\.download\s*=/, // a.download = ...
  // JSX <a href=... download>（要求带 href，注释里提到 "<a download>" 不算）
  /<a\b[^>]*\bhref=[^>]*\sdownload\b/s,
  /<a\b[^>]*\sdownload\b[^>]*\bhref=/s,
  /from\s+['"]file-saver['"]/,
  /window\.open\(\s*URL\.createObjectURL/,
];

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

describe('前端下载统一走 saveToDisk', () => {
  it('src 下非测试文件不直接用 <a download> / file-saver', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const rel = relative(SRC, file).split('\\').join('/');
      if (ALLOWED.has(rel)) continue;
      const text = readFileSync(file, 'utf8');
      for (const re of PATTERNS) if (re.test(text)) hits.push(`${rel}: ${re}`);
    }
    expect(hits).toEqual([]);
  });
});
