import { describe, expect, it } from 'vitest';
import { ownerTokenMark } from './observeMode';
import { newTeamOwnerToken } from './teamOwnerToken';

describe('newTeamOwnerToken', () => {
  it('形状是 team:<8位hex>', () => {
    expect(newTeamOwnerToken()).toMatch(/^team:[0-9a-f]{8}$/);
  });

  // 撞车的代价不是「重复一次」而是「协作静默失效」：两个客户端指纹相同时，
  // 各自都会把对方的 op 当成自己的回声跳过，画布再也不同步且零报错。
  it('每次调用都是新值（每标签页一个）', () => {
    const tokens = new Set(Array.from({ length: 64 }, () => newTeamOwnerToken()));
    expect(tokens.size).toBe(64);
  });

  // 服务端 canvasowner.SourceMark 取的是末段前 8 位，前端 ownerTokenMark 同规则。
  // 两边算不出同一个值的话，回声检测永远不命中——自己的每一次写都会被自己
  // 再合并一遍（幂等所以看不出来），而缺口检测会因为 version 没推进而误判。
  it('指纹与服务端同规则：取末段前 8 位，team: 前缀不参与', () => {
    const token = newTeamOwnerToken();
    expect(ownerTokenMark(token)).toBe(token.slice('team:'.length));
    expect(ownerTokenMark(token)).toHaveLength(8);
  });
});
