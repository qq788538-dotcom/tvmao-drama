import { describe, it, expect } from 'vitest';

import {
  applyMentionInsert,
  extractMentions,
  findActiveMention,
} from './mentionParser';

describe('extractMentions', () => {
  it('handles empty input', () => {
    const r = extractMentions('');
    expect(r.plainText).toBe('');
    expect(r.mentions).toEqual([]);
  });

  it('flattens a single mention token', () => {
    const r = extractMentions('@[柴犬](art_1) please redraw');
    expect(r.plainText).toBe('@柴犬 please redraw');
    expect(r.mentions).toEqual([{ artifact_id: 'art_1', name: '柴犬' }]);
  });

  it('keeps mentions in source order', () => {
    const r = extractMentions('@[a](id1) @[b](id2)');
    expect(r.plainText).toBe('@a @b');
    expect(r.mentions).toEqual([
      { artifact_id: 'id1', name: 'a' },
      { artifact_id: 'id2', name: 'b' },
    ]);
  });
});

describe('findActiveMention', () => {
  it('detects an open mention before the cursor', () => {
    expect(findActiveMention('abc @sh', 7)).toEqual({ startIndex: 4, query: 'sh' });
  });

  it('returns null when cursor is past a space', () => {
    expect(findActiveMention('abc @sh def', 8)).toBeNull();
  });

  it('returns null when there is no @ before the cursor', () => {
    expect(findActiveMention('hello', 5)).toBeNull();
  });
});

describe('applyMentionInsert', () => {
  it('replaces the active `@query` with a formal token', () => {
    const r = applyMentionInsert('hi @sh', 3, 6, { artifact_id: 'x', name: 'shiba' });
    expect(r.next).toBe('hi @[shiba](x) ');
    // 光标落在我们追加的末尾空格之后（字符串长度 15）。
    expect(r.nextCursor).toBe(15);
  });
});
