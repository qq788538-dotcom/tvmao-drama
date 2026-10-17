import { describe, expect, it } from 'vitest';
import {
  DEFAULT_DURATION_SEC,
  MIN_SHOT_SECONDS,
  createScriptRow,
  defaultScriptSettings,
  readScriptSettings,
  renumberRows,
  scriptStepStats,
} from './scriptData';

describe('scriptData（skill 对齐版）', () => {
  it('createScriptRow 产出合法默认行', () => {
    const row = createScriptRow();
    expect(row.id).toHaveLength(8);
    expect(row.duration).toBe(MIN_SHOT_SECONDS);
    expect(row.name).toBe('');
    expect(row.prompt).toBe('');
    expect(row.colorMark).toBe('');
  });

  it('readScriptSettings 容错：空/半残 settings 补默认，durationSec 越界回退', () => {
    expect(readScriptSettings(undefined)).toEqual(defaultScriptSettings());
    const patched = readScriptSettings({ rows: [{ id: 'x', name: '开场', prompt: 'p' }], durationSec: 2 });
    expect(patched.rows[0].name).toBe('开场');
    expect(patched.rows[0].duration).toBe(MIN_SHOT_SECONDS);
    expect(patched.durationSec).toBe(DEFAULT_DURATION_SEC);
    expect(patched.assets.characters).toEqual([]);
  });

  it('readScriptSettings 对旧 schema（散字段版）降级：认不出的字段丢弃不迁移', () => {
    const legacy = readScriptSettings({
      rows: [{ id: 'r1', plotDescription: '雨夜', shotSize: '远景', dialogueLines: [] }],
    });
    expect(legacy.rows[0].prompt).toBe('');
    expect(legacy.rows[0].name).toBe('');
    expect((legacy.rows[0] as unknown as Record<string, unknown>).plotDescription).toBeUndefined();
  });

  it('renumberRows 按数组序重排镜号', () => {
    const rows = [createScriptRow(), createScriptRow()];
    expect(renumberRows(rows).map((r) => r.shotNumber)).toEqual([1, 2]);
  });

  it('scriptStepStats 两步计数：分镜数 + 资产配图进度', () => {
    const s = defaultScriptSettings();
    s.rows = [createScriptRow(), createScriptRow()];
    s.assets.characters = [
      { id: 'a', name: 'A', description: '', linkedNodeId: 'n1' },
      { id: 'b', name: 'B', description: '', linkedNodeId: '' },
    ];
    s.assets.props = [{ id: 'c', name: 'C', description: '', linkedNodeId: 'n2' }];
    expect(scriptStepStats(s)).toEqual({ shots: 2, assetsReady: 2, assetsTotal: 3 });
  });
});
