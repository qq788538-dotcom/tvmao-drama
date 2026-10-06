import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { MAX_TEXT_LEN, useMarkdownEditor } from './useMarkdownEditor';

// 超限不许丢字。曾经 CharacterCount 带 limit=MAX_TEXT_LEN，那是一道
// filterTransaction：粘贴一份超过一万字的稿子，超出部分被整条吃掉，界面只把
// 计数标红，用户以为贴进去了、实际存的是被砍过的半截。上限从此只是标红阈值。
describe('文本节点字数上限', () => {
  it('插入超过上限的内容全量保留，不截断', async () => {
    const long = '字'.repeat(MAX_TEXT_LEN + 500);
    const { result } = renderHook(() =>
      useMarkdownEditor({ value: '', onChange: () => {} }),
    );
    await waitFor(() => expect(result.current).not.toBeNull());

    const editor = result.current!;
    act(() => {
      editor.commands.insertContent(long);
    });

    expect(editor.storage.characterCount.characters()).toBe(long.length);
    expect(editor.getText()).toContain(long);
  });
});
