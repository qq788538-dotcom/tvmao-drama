import { describe, expect, it } from 'vitest';
import { MENTION_RE, remapMentionNodeIds } from './mentions';

describe('MENTION_RE', () => {
  it('解析不带原文的标记', () => {
    const m = [...'@[图片:n-1]'.matchAll(MENTION_RE)][0];
    expect([m[1], m[2], m[3]]).toEqual(['图片', 'n-1', undefined]);
  });
  it('原文可以是裸名字或带方括号的写法', () => {
    expect([...'@[图片:n-1|张三]'.matchAll(MENTION_RE)][0][3]).toBe('张三');
    expect([...'@[图片:n-1|[角色-张三]]'.matchAll(MENTION_RE)][0][3]).toBe('[角色-张三]');
  });
});

describe('remapMentionNodeIds', () => {
  it('拷贝节点重写 nodeId 时保留原文段', () => {
    const map = new Map([['n-1', 'n-9']]);
    expect(remapMentionNodeIds('@[图片:n-1|[角色-张三]]和@[图片:n-1]', map)).toBe('@[图片:n-9|[角色-张三]]和@[图片:n-9]');
  });
  it('映射表里没有的原样保留', () => {
    expect(remapMentionNodeIds('@[图片:n-2|张三]', new Map([['n-1', 'n-9']]))).toBe('@[图片:n-2|张三]');
  });
});
