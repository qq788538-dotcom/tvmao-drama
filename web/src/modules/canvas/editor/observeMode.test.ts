import { describe, expect, it } from 'vitest';
import { HOLDER_STALE_LIMIT_SECONDS, ownerTokenMark, shouldObserve } from './observeMode';

describe('shouldObserve', () => {
  const held = { held: true, editorActorId: 9, isSelf: false, staleSeconds: 0 };

  it('没人持锁 → 不观看（照旧 Claim）', () => {
    expect(shouldObserve({ held: false, editorActorId: 0, isSelf: false, staleSeconds: 0 })).toBe(false);
  });

  it('探测失败（null）→ 不观看：探测本身故障不该把人锁在门外', () => {
    expect(shouldObserve(null)).toBe(false);
    expect(shouldObserve(undefined)).toBe(false);
  });

  it('持锁的是自己另一个 tab → 不观看，走既有的 steal 语义', () => {
    expect(shouldObserve({ ...held, isSelf: true })).toBe(false);
  });

  it('队友持锁且心跳新鲜 → 观看', () => {
    expect(shouldObserve(held)).toBe(true);
    expect(shouldObserve({ ...held, staleSeconds: HOLDER_STALE_LIMIT_SECONDS })).toBe(true);
  });

  // 锁 TTL 24h 且关页面不释放：只看 held 的话，有人早上开一下就关掉浏览器，
  // 全团队会被顶在只读里一整天。
  it('持锁者早已没有心跳（超过阈值）→ 不观看，照旧 Claim 抢过来', () => {
    expect(shouldObserve({ ...held, staleSeconds: HOLDER_STALE_LIMIT_SECONDS + 1 })).toBe(false);
    expect(shouldObserve({ ...held, staleSeconds: 86_400 })).toBe(false);
  });

  it('锁龄未知(-1) 按保守处理 → 观看，宁可多点一次接管也不误踢队友', () => {
    expect(shouldObserve({ ...held, staleSeconds: -1 })).toBe(true);
  });
});

describe('ownerTokenMark', () => {
  it('取末段的前 8 位——与后端 canvasowner.SourceMark 同规则', () => {
    expect(ownerTokenMark('7:9:0123456789abcdef0123456789abcdef')).toBe('01234567');
  });

  // 热升级期间 Redis 里两段式旧锁值仍在。按整串截前 8 位会把 userID 截进去，
  // 跟服务端算的对不上，于是自己的每次改动都让自己回拉一次快照。
  it('两段式旧锁值同样取末段，不受前缀长度影响', () => {
    expect(ownerTokenMark('12345:abcdef0123456789')).toBe('abcdef01');
  });

  it('观看者没有 token → 空串（调用方据此恒刷新）', () => {
    expect(ownerTokenMark(null)).toBe('');
    expect(ownerTokenMark(undefined)).toBe('');
    expect(ownerTokenMark('')).toBe('');
  });
});
