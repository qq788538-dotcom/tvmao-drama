import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NODE_EDITING_TTL_MS,
  memberLabel,
  nodeEditingLabel,
  parsePresenceMembers,
  resetCanvasPresence,
  useCanvasPresenceStore,
} from './presenceStore';

const state = () => useCanvasPresenceStore.getState();

describe('presence 名册', () => {
  beforeEach(() => resetCanvasPresence());

  it('全量替换而不是增量合并（后端每次进出都重发整份）', () => {
    state().setMembers([{ actorUserId: 3, email: 'a@x.com' }, { actorUserId: 9, email: 'b@x.com' }]);
    state().setMembers([{ actorUserId: 9, email: 'b@x.com' }]);
    expect(state().members).toEqual([{ actorUserId: 9, email: 'b@x.com' }]);
  });

  it('reset 清空名册与编辑角标（换项目不留幻影）', () => {
    state().setMembers([{ actorUserId: 3, email: 'a@x.com' }]);
    state().noteNodeEditing('n1', 3, true);
    resetCanvasPresence();
    expect(state().members).toEqual([]);
    expect(state().editors).toEqual({});
  });

  it('parsePresenceMembers 丢弃 actor<=0（那是「个人身份」的哨兵值）与坏条目', () => {
    expect(
      parsePresenceMembers([
        { actor_user_id: 3, email: 'a@x.com' },
        { actor_user_id: 0, email: 'ghost@x.com' },
        { actor_user_id: '9', email: 'b@x.com' },
        { actor_user_id: 9 },
        null,
      ]),
    ).toEqual([
      { actorUserId: 3, email: 'a@x.com' },
      { actorUserId: 9, email: '' },
    ]);
    expect(parsePresenceMembers(undefined)).toEqual([]);
  });

  it('邮箱查不到时退回通称，不显示裸 id', () => {
    expect(memberLabel({ actorUserId: 3, email: '' })).toBe('另一位成员');
    expect(memberLabel(undefined)).toBe('另一位成员');
    expect(memberLabel({ actorUserId: 3, email: 'a@x.com' })).toBe('a@x.com');
  });
});

describe('「正在编辑」角标的 TTL', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    resetCanvasPresence();
  });
  afterEach(() => {
    resetCanvasPresence();
    vi.useRealTimers();
  });

  it('按 actor 在名册里查邮箱拼出角标文案', () => {
    state().setMembers([{ actorUserId: 7, email: 'mate@x.com' }]);
    state().noteNodeEditing('n1', 7, true);
    expect(nodeEditingLabel(state(), 'n1')).toBe('mate@x.com 正在编辑');
    expect(nodeEditingLabel(state(), 'n2')).toBeNull();
  });

  it('名册里没有这个 actor 时仍然显示提示（退回通称）', () => {
    state().noteNodeEditing('n1', 42, true);
    expect(nodeEditingLabel(state(), 'n1')).toBe('另一位成员 正在编辑');
  });

  // 退出帧走的是会断的同一条 WS：对方崩溃/断网/直接关标签页时那帧永远不到，
  // 没有 TTL 的话角标永久挂着，指向一个早就不在的人。
  it('无续帧超过 TTL 自动清除', () => {
    state().noteNodeEditing('n1', 7, true);
    vi.advanceTimersByTime(NODE_EDITING_TTL_MS - 1);
    expect(state().editors.n1).toBeDefined();
    vi.advanceTimersByTime(2);
    expect(state().editors.n1).toBeUndefined();
  });

  it('续帧保活：3s 一帧的心跳下角标不会到点消失', () => {
    state().noteNodeEditing('n1', 7, true);
    vi.advanceTimersByTime(3_000);
    state().noteNodeEditing('n1', 7, true);
    vi.advanceTimersByTime(3_000);
    expect(state().editors.n1).toBeDefined();
    // 心跳一停，仍然按 TTL 过期。
    vi.advanceTimersByTime(NODE_EDITING_TTL_MS + 1);
    expect(state().editors.n1).toBeUndefined();
  });

  it('editing:false 立刻清除，并撤掉待触发的过期定时器', () => {
    state().noteNodeEditing('n1', 7, true);
    state().noteNodeEditing('n1', 7, false);
    expect(state().editors.n1).toBeUndefined();
    // 定时器若没撤，这一跳会在「别人接手编辑」之后把新角标误删。
    state().noteNodeEditing('n1', 8, true);
    vi.advanceTimersByTime(NODE_EDITING_TTL_MS - 1);
    expect(state().editors.n1).toEqual({ actorUserId: 8 });
  });

  // 槽位是单值的：不认人的话，B 退出就把 A 的角标和 TTL 一起抹了——A 明明还在
  // 编辑，提示却消失，要等他下一次 3s 心跳才回来。
  it('别人的退出帧不清掉当前编辑者的角标，也不掐掉他的 TTL', () => {
    state().noteNodeEditing('n1', 7, true);
    state().noteNodeEditing('n1', 8, false);
    expect(state().editors.n1).toEqual({ actorUserId: 7 });
    // TTL 仍按 7 号那帧计时：没被 8 号的退出帧顺手 clearExpiry 掉。
    vi.advanceTimersByTime(NODE_EDITING_TTL_MS + 1);
    expect(state().editors.n1).toBeUndefined();
  });

  it('同一人的续帧不产生新的 state 对象（避免每 3s 白重渲染一次）', () => {
    state().noteNodeEditing('n1', 7, true);
    const first = state().editors;
    state().noteNodeEditing('n1', 7, true);
    expect(state().editors).toBe(first);
  });
});
