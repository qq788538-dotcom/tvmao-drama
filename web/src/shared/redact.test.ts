import { describe, expect, it } from 'vitest';
import { redactSecrets } from './redact';

describe('redactSecrets', () => {
  it('把查询串里的 token 值替换成 ***（? 和 & 两种位置）', () => {
    expect(redactSecrets('GET http://127.0.0.1:5/api/v1/diag/media/a.mp4?token=abc%2F123: 500'))
      .toBe('GET http://127.0.0.1:5/api/v1/diag/media/a.mp4?token=***: 500');
    expect(redactSecrets('ws://127.0.0.1:5/ws?project_id=7&token=xyz&x=1'))
      .toBe('ws://127.0.0.1:5/ws?project_id=7&token=***&x=1');
  });

  it('一段文字里出现多次都替换', () => {
    expect(redactSecrets('a?token=1 b&token=2')).toBe('a?token=*** b&token=***');
  });

  it('Bearer 头的值也替换', () => {
    expect(redactSecrets('Authorization: Bearer s3cr3t')).toBe('Authorization: Bearer ***');
  });

  it('不含 token 的文字原样返回', () => {
    expect(redactSecrets('HTTP 500 mytoken=1')).toBe('HTTP 500 mytoken=1');
  });
});
