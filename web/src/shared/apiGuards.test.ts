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

  // 团队观看模式正是靠 readOnly 实现的：抢锁请求要是被这道闸自己拦下，
  // 「接管编辑」点了永远没反应（而且是静默的——按钮 loading 完就恢复原样）。
  it('放行「接管编辑」的抢锁请求，否则观看者永远接管不了', () => {
    setApiWritesBlocked(true);
    expect(isApiWriteAllowed('post', '/projects/14/canvas/owner/claim')).toBe(true);
    // 只放行 claim 本身，同前缀的其它锁端点不跟着开口子。
    expect(isApiWriteAllowed('post', '/projects/14/canvas/owner/heartbeat')).toBe(false);
  });

  // 401 拦截器的死身份自愈自己就发 POST /auth/switch-context。被这道闸拦住的话，
  // 「观看中被移出团队」会退化成画布静默冻住（自愈第一步就打不出去）。
  it('放行身份自愈的换发请求，否则只读态下自愈第一步就发不出去', () => {
    setApiWritesBlocked(true);
    expect(isApiWriteAllowed('post', '/auth/switch-context')).toBe(true);
  });

  it('分享 viewer 的克隆仍在放行名单里（既有行为）', () => {
    setApiWritesBlocked(true);
    expect(isApiWriteAllowed('post', '/workflows/by-token/abc/clone')).toBe(true);
  });
});
