// 守护：桌面端隐藏运维入口（spec F10 隐藏 Skill JSON 导入导出、S10 不做项目导入导出），
// 相关弹窗和超管门控已删除；以后从参考仓同步代码时如果带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = [
  'modules/canvas/editor/WorkflowImportModal.tsx',
  'modules/canvas/editor/WorkflowExportModal.tsx',
  'modules/canvas/editor/workflowSerializer.ts',
  'shared/permissions.ts',
];

const FORBIDDEN = /\b(useIsSuperAdmin|WorkflowImportModal|WorkflowExportModal|compileToSkill|decodeFromSkill)\b/;

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

describe('运维弹窗已删除（F10、S10）', () => {
  it('弹窗、序列化器、超管门控文件不存在', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用它们', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
