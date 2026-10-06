import { afterEach, describe, expect, it } from 'vitest';
import { isApiWriteAllowed, setApiWritesBlocked } from './apiGuards';

afterEach(() => setApiWritesBlocked(false));

describe('apiGuards 只读模式写入闸', () => {
  it('不在只读模式时一律放行', () => {
    expect(isApiWriteAllowed('post', '/projects/1/snapshot')).toBe(true);
  });

  it('只读模式挡住写方法、放行读方法', () => {
    setApiWritesBlocked(true);
    expect(isApiWriteAllowed('post', '/projects/1/snapshot')).toBe(false);
    expect(isApiWriteAllowed('put', '/projects/1/nodes/n1')).toBe(false);
    expect(isApiWriteAllowed('delete', '/projects/1/nodes/n1')).toBe(false);
    expect(isApiWriteAllowed('get', '/projects/1/snapshot')).toBe(true);
  });

  // 桌面端没有分享、团队观看、登录身份（spec X2、X4），参考仓为这些功能开的口子
  // 全部去掉：只读态下写请求一律拦住。
  it('没有放行名单：只读态下抢锁、身份换发、分享克隆都被拦住', () => {
    setApiWritesBlocked(true);
    for (const url of ['/projects/14/canvas/owner/claim', '/auth/switch-context', '/workflows/by-token/abc/clone']) {
      expect(isApiWriteAllowed('post', url)).toBe(false);
    }
  });
});
