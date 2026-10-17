// 守护：桌面端删除了积分计费和估价 UI。估价接口、估价合批器、单价说明解析都已删除，
// 以后如果有代码把它们带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = ['shared/priceNote.ts', 'modules/canvas/editor/hooks/useDebouncedValue.ts'];

const FORBIDDEN =
  /\b(loadEstimate|estimateCost|estimateCostBatch|EstimateItem|fetchCanvasPostprocessEstimate|CanvasPostprocessEstimate\w*|resolvePriceNote|useDebouncedValue)\b|['"]@\/shared\/priceNote['"]/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

// 去掉注释再查：说明性文字里提到旧名字不算残留。
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

describe('估价与积分代码已删除', () => {
  it('相关文件不存在', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用估价接口、合批器和单价说明', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
