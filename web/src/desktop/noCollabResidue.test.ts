// 守护：桌面端删除了多人协作。瞬态通道、在线名册、远端 op 合并、在途登记都已删除；
// 以后如果有代码把它们带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = [
  'modules/canvas/editor/transientChannel.ts',
  'modules/canvas/editor/presenceStore.ts',
  'modules/canvas/editor/interactionRegistry.ts',
  'modules/canvas/editor/hooks/useNodeEditingBroadcast.ts',
];

// 注意不按 beginInteraction 查：导演台 InspectorControls 有个同名但无关的局部函数。
const FORBIDDEN =
  /\b(applyRemoteOp|applyTransientPositions|transientPositions|clearTransientPositions|resolveNodePosition|RemoteCanvasOp|RemoteNodeStateOp|setRemoteNodeStateSink|setPendingLocalPatchProvider|deferredRemoteOps|clearDeferredRemoteOps|registerInFlightPatch|inFlightNodePatches|useCanvasPresenceStore|sendDragTransient|sendNodeEditingTransient|setTransientChannel)\b/;

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

describe('协作残留已清理', () => {
  it('协作专用模块已删除', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用瞬态通道、在线名册、远端 op 和在途登记', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
