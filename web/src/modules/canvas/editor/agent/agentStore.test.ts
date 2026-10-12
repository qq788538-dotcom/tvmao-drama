import { describe, it, expect, beforeEach } from 'vitest';
import { useAgentStore, isOrderedBefore, type AgentMessage } from './agentStore';

describe('agentStore', () => {
  beforeEach(() => {
    useAgentStore.setState({
      threads: [],
      currentThreadId: null,
      messages: {},
      connectionState: 'idle',
      artifactListener: null,
      pendingSkillDraft: null,
      currentProjectId: null,
    });
  });

  it('selectThread sets currentThreadId', () => {
    useAgentStore.getState().selectThread('thr_1');
    expect(useAgentStore.getState().currentThreadId).toBe('thr_1');
  });

  it('handleFrame text_delta appends to the pending assistant message', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_1' });
    s.handleFrame({
      type: 'content_block_start', thread_id: 'thr_1', action_id: 'act_1',
      index: 0, block: { type: 'text' },
    });
    s.handleFrame({
      type: 'content_block_delta', thread_id: 'thr_1', action_id: 'act_1',
      index: 0, delta: { type: 'text_delta', text: 'hi' },
    });
    s.handleFrame({
      type: 'content_block_delta', thread_id: 'thr_1', action_id: 'act_1',
      index: 0, delta: { type: 'text_delta', text: ' there' },
    });
    s.handleFrame({
      type: 'content_block_stop', thread_id: 'thr_1', action_id: 'act_1', index: 0,
    });
    const msgs = useAgentStore.getState().messages['thr_1'];
    const last = msgs[msgs.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.blocks[0]).toMatchObject({ type: 'text', text: 'hi there' });
  });

  it('tool_event block recorded and emits onArtifact via listener', () => {
    const arts: { thread_id: string; action_id: string; artifact: { artifact_id: string } }[] = [];
    useAgentStore.getState().setArtifactListener((ctx) => arts.push(ctx));
    useAgentStore.getState().handleFrame({
      type: 'content_block_start', thread_id: 'thr_1', action_id: 'act_1', index: 5,
      block: {
        type: 'tool_event', name: 'echo_image', id: 'tu_1',
        artifacts: [{ artifact_type: 'image_info', artifact_id: 'art_1', url: 'http://x/1.png', name: '柴犬' }],
      },
    });
    expect(arts).toHaveLength(1);
    expect(arts[0].artifact.artifact_id).toBe('art_1');
    expect(arts[0].thread_id).toBe('thr_1');
  });

  it('ignores frames from another project', () => {
    const arts: { thread_id: string; action_id: string; artifact: { artifact_id: string } }[] = [];
    const s = useAgentStore.getState();
    s.setCurrentProjectId(2);
    s.setArtifactListener((ctx) => arts.push(ctx));
    s.handleFrame({
      type: 'content_block_start',
      project_id: 1,
      thread_id: 'thr_other',
      action_id: 'act_other',
      index: 0,
      block: {
        type: 'tool_event',
        artifacts: [{ artifact_type: 'image_info', artifact_id: 'art_other', url: 'http://x/other.png' }],
      },
    });
    expect(arts).toHaveLength(0);
    expect(useAgentStore.getState().messages.thr_other).toBeUndefined();
  });

  it('action_end clears pending flag on assistant', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_1' });
    s.handleFrame({ type: 'action_end', thread_id: 'thr_1', action_id: 'act_1', stop_reason: 'end_turn' });
    const msgs = useAgentStore.getState().messages['thr_1'];
    expect(msgs[msgs.length - 1].pending).toBeFalsy();
  });

  it('skill_definition artifact sets pendingSkillDraft', () => {
    useAgentStore.getState().handleFrame({
      type: 'content_block_start', thread_id: 't1', action_id: 'a1', index: 5,
      block: {
        type: 'tool_event', name: 'output_skill_definition', id: 'tu_1',
        artifacts: [{ artifact_type: 'skill_definition', artifact_id: 'sd_1', name: 'My Skill', skill: { id: 'my_skill', name: 'My Skill' } }],
      },
    });
    expect(useAgentStore.getState().pendingSkillDraft?.id).toBe('my_skill');
  });

  it('跨轮全局递增的不连续 index 不产生 sparse array（洞位补空 block）', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_1' });
    // 第一轮：index 0 正常写入。
    s.handleFrame({
      type: 'content_block_start', thread_id: 'thr_1', action_id: 'act_1',
      index: 0, block: { type: 'text' },
    });
    s.handleFrame({
      type: 'content_block_delta', thread_id: 'thr_1', action_id: 'act_1',
      index: 0, delta: { type: 'text_delta', text: '第一轮' },
    });
    // 第二轮：index 全局递增（后端 018a35bf），从 3 起跳——1、2 两个序号
    // 从未以 content_block_start 送达本气泡（属于不入流的内部块/丢帧窗口）。
    s.handleFrame({
      type: 'content_block_start', thread_id: 'thr_1', action_id: 'act_1',
      index: 3, block: { type: 'text' },
    });
    s.handleFrame({
      type: 'content_block_delta', thread_id: 'thr_1', action_id: 'act_1',
      index: 3, delta: { type: 'text_delta', text: '第二轮' },
    });
    const msgs = useAgentStore.getState().messages['thr_1'];
    const blocks = msgs[msgs.length - 1].blocks;
    expect(blocks).toHaveLength(4);
    // 没有洞：MessageBubble 的 blocks.map 不会碰到 undefined。
    expect(blocks.every((b) => b != null)).toBe(true);
    expect(blocks[0]).toMatchObject({ type: 'text', text: '第一轮' });
    expect(blocks[3]).toMatchObject({ type: 'text', text: '第二轮' });
  });

  it('delta 先于 start 到达高位 index 时同样补洞', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_1' });
    // 极端乱序：没有任何 content_block_start，delta 直接落在 index 2。
    s.handleFrame({
      type: 'content_block_delta', thread_id: 'thr_1', action_id: 'act_1',
      index: 2, delta: { type: 'text_delta', text: 'x' },
    });
    const msgs = useAgentStore.getState().messages['thr_1'];
    const blocks = msgs[msgs.length - 1].blocks;
    expect(blocks).toHaveLength(3);
    expect(blocks.every((b) => b != null)).toBe(true);
    expect(blocks[2]).toMatchObject({ type: 'text', text: 'x' });
  });

  it('text_info artifact 走 artifactListener（与媒体产物同一条落画布通道）', () => {
    const arts: { artifact: { artifact_type?: string; text?: string } }[] = [];
    useAgentStore.getState().setArtifactListener((ctx) => arts.push(ctx));
    useAgentStore.getState().handleFrame({
      type: 'content_block_start', thread_id: 'thr_1', action_id: 'act_1', index: 0,
      block: {
        type: 'tool_event', name: 'create_text_node', id: 'tu_1',
        artifacts: [{ artifact_type: 'text_info', artifact_id: 'txt_1', name: '短片剧本《暴雨单》', text: '正文全文' }],
      },
    });
    expect(arts).toHaveLength(1);
    expect(arts[0].artifact.artifact_type).toBe('text_info');
    expect(arts[0].artifact.text).toBe('正文全文');
  });

  it('error frame appends a visible error bubble', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({
      type: 'error', thread_id: 'thr_1', action_id: 'act_x',
      error: { code: 'no_planner', message: 'configure admin' },
    });
    const msgs = useAgentStore.getState().messages['thr_1'];
    const last = msgs[msgs.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.blocks[0].text).toContain('configure admin');
  });

  it('error frame clears the pending assistant so 思考中 can stop', () => {
    const s = useAgentStore.getState();
    s.handleFrame({ type: 'thread_start', thread_id: 'thr_1' });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_1' });
    // action_start 建了一条 pending assistant
    let msgs = useAgentStore.getState().messages['thr_1'];
    expect(msgs.find((m) => m.id === 'pending_act_1')?.pending).toBe(true);
    s.handleFrame({
      type: 'error', thread_id: 'thr_1', action_id: 'act_1',
      error: { code: 'max_rounds', message: 'boom' },
    });
    msgs = useAgentStore.getState().messages['thr_1'];
    // 原 pending 被翻成非 pending（否则永久转圈），且追加了错误气泡
    expect(msgs.find((m) => m.id === 'pending_act_1')?.pending).toBe(false);
    expect(msgs[msgs.length - 1].blocks[0].text).toContain('max_rounds');
  });
});

// ===== 懒加载：hydrateTail / prependOlderMessages =====
// 设计文档 docs/2026-08-13-agent-chat-cursor-lazy-load-design.md 的 R1/R5/R9。
describe('agentStore 懒加载', () => {
  const srv = (id: string, at: string): AgentMessage => ({
    id, thread_id: 'thr_1', action_id: 'a', role: 'assistant', blocks: [], createdAt: at,
  });

  beforeEach(() => {
    useAgentStore.setState({
      threads: [], currentThreadId: null, messages: {}, pageState: {},
      connectionState: 'idle', artifactListener: null, pendingSkillDraft: null,
      currentProjectId: null,
    });
  });

  it('F3：RFC3339Nano 裁尾零不破坏时间序（裸字典序会反）', () => {
    // Go 的 marshaller 会把 .000 裁掉：同一秒内 "05Z" 字典序 > "05.123Z"，
    // 但时间上更早。timestampSortKey 归一化后必须比出正确顺序。
    const whole = srv('a', '2026-01-01T00:00:05Z');
    const frac = srv('b', '2026-01-01T00:00:05.123Z');
    expect(isOrderedBefore(whole, frac)).toBe(true);
    expect(isOrderedBefore(frac, whole)).toBe(false);
    const short = srv('c', '2026-01-01T00:00:05.48Z');
    const long = srv('d', '2026-01-01T00:00:05.482913Z');
    expect(isOrderedBefore(short, long)).toBe(true);
    // 数值相等但表示不同（.5 vs .500）→ 时间键相等，落到 id tie-break
    const p = srv('e', '2026-01-01T00:00:05.5Z');
    const q = srv('f', '2026-01-01T00:00:05.500Z');
    expect(isOrderedBefore(p, q)).toBe(true); // e < f
    expect(isOrderedBefore(q, p)).toBe(false);
  });

  it('F2：tail 与现有窗口无交集（中间有缺行）→ 整窗重置不留洞', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m1', '2026-01-01T00:00:01Z'), srv('m2', '2026-01-01T00:00:02Z')], true);
    // 服务端在窗口外新落了一整页以上，对账 tail 与旧窗口完全不重叠
    s.hydrateTail('thr_1', [srv('m40', '2026-01-01T00:01:40Z'), srv('m41', '2026-01-01T00:01:41Z')], true);
    const st = useAgentStore.getState();
    expect(st.messages['thr_1'].map((m) => m.id)).toEqual(['m40', 'm41']); // 旧前缀被丢弃
    expect(st.pageState['thr_1'].hasMore).toBe(true); // 用 tail 页自己的 hasMore
  });

  it('isOrderedBefore：双键序 + 乐观消息视为最新', () => {
    const a = srv('m1', '2026-01-01T00:00:01Z');
    const b = srv('m2', '2026-01-01T00:00:02Z');
    const tieA = srv('m3', '2026-01-01T00:00:03Z');
    const tieB = srv('m4', '2026-01-01T00:00:03Z');
    const local = { ...srv('pending_x', ''), createdAt: undefined };
    expect(isOrderedBefore(a, b)).toBe(true);
    expect(isOrderedBefore(b, a)).toBe(false);
    expect(isOrderedBefore(tieA, tieB)).toBe(true); // 同刻靠 id tie-break
    expect(isOrderedBefore(local, a)).toBe(false); // 乐观消息不早于任何服务端消息
    expect(isOrderedBefore(a, local)).toBe(true);
  });

  it('R1：tail 对账保留已上拉加载的更早前缀', () => {
    const s = useAgentStore.getState();
    // 窗口 = 上拉出来的旧页 [m1,m2] + 当前尾页 [m3,m4]
    s.prependOlderMessages('thr_1', [srv('m3', '03'), srv('m4', '04')], false);
    s.prependOlderMessages('thr_1', [srv('m1', '01'), srv('m2', '02')], true);
    // 新一轮完成，对账 hydrate 只带最近一页 [m3,m4,m5]
    s.hydrateTail('thr_1', [srv('m3', '03'), srv('m4', '04'), srv('m5', '05')], true);
    const ids = useAgentStore.getState().messages['thr_1'].map((m) => m.id);
    expect(ids).toEqual(['m1', 'm2', 'm3', 'm4', 'm5']); // 旧页没被抹掉
  });

  it('tail 对账丢弃本地乐观消息（与旧整表覆盖语义一致）', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m1', '01')], false);
    s.appendUserMessage({ id: 'local_u', thread_id: 'thr_1', action_id: '', role: 'user', blocks: [] });
    s.handleFrame({ type: 'action_start', thread_id: 'thr_1', action_id: 'act_9' });
    // 该轮完成后的对账带回持久化的 [m1,m2,m3]
    s.hydrateTail('thr_1', [srv('m1', '01'), srv('m2', '02'), srv('m3', '03')], false);
    const ids = useAgentStore.getState().messages['thr_1'].map((m) => m.id);
    expect(ids).toEqual(['m1', 'm2', 'm3']); // local_u / pending_act_9 已被覆盖掉
  });

  it('空 tail 清空整表（空 thread 语义不变）', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m1', '01')], false);
    s.hydrateTail('thr_1', [], false);
    expect(useAgentStore.getState().messages['thr_1']).toEqual([]);
  });

  it('R9：merge 按 id 去重（前缀与 tail 交叠的脏数据兜底）', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m1', '01'), srv('m2', '02')], false);
    // 脏数据：m2 出现在 tail 里、同时又以更早的 createdAt 残留在现有列表
    useAgentStore.setState((st) => ({
      messages: { ...st.messages, thr_1: [srv('m2', '00'), ...st.messages['thr_1']] },
    }));
    s.hydrateTail('thr_1', [srv('m2', '02'), srv('m3', '03')], false);
    const ids = useAgentStore.getState().messages['thr_1'].map((m) => m.id);
    expect(ids.filter((id) => id === 'm2')).toHaveLength(1);
  });

  it('prependOlderMessages 前插并更新 hasMore/loadingOlder', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m3', '03')], true);
    s.setLoadingOlder('thr_1', true);
    s.prependOlderMessages('thr_1', [srv('m1', '01'), srv('m2', '02')], false);
    const st = useAgentStore.getState();
    expect(st.messages['thr_1'].map((m) => m.id)).toEqual(['m1', 'm2', 'm3']);
    expect(st.pageState['thr_1']).toEqual({ hasMore: false, loadingOlder: false });
  });

  it('保留前缀时 hasMore 不被 tail 页的值收窄', () => {
    const s = useAgentStore.getState();
    s.hydrateTail('thr_1', [srv('m3', '03')], true);
    s.prependOlderMessages('thr_1', [srv('m2', '02')], true); // 还有更早
    // 对账 hydrate 的 hasMore 只描述 tail[0] 之前——沿用窗口自己的 true
    s.hydrateTail('thr_1', [srv('m3', '03'), srv('m4', '04')], true);
    expect(useAgentStore.getState().pageState['thr_1'].hasMore).toBe(true);
  });
});

// 用户点停止后端发 code=aborted——渲染成中性「已停止生成。」，不带「错误」字样；
// 其它 code 维持原报错格式（上面两个用例）。
it('error 帧 code=aborted 渲染中性文案而非红色报错', () => {
  const s = useAgentStore.getState();
  s.handleFrame({ type: 'thread_start', thread_id: 'thr_ab' });
  s.handleFrame({
    type: 'error', thread_id: 'thr_ab', action_id: 'a1',
    error: { code: 'aborted', message: '已停止生成' },
  });
  const msgs = useAgentStore.getState().messages['thr_ab'];
  const text = msgs[msgs.length - 1].blocks[0].text ?? '';
  expect(text).toBe('已停止生成。');
  expect(text).not.toContain('错误');
});

// 轮次以 error 帧收尾后，完成对账的 hydrateTail 不能把本地错误气泡抹掉——
// 错误只存在于前端，抹掉后用户看到的就是「回复闪一下就没了」。
it('hydrateTail 保留本轮的本地错误气泡，排在同 action 的服务端消息之后', () => {
  useAgentStore.setState({ messages: {}, pageState: {}, currentProjectId: null });
  const s = useAgentStore.getState();
  s.handleFrame({ type: 'action_start', thread_id: 'thr_e', action_id: 'act_e' });
  s.handleFrame({ type: 'error', thread_id: 'thr_e', action_id: 'act_e', error: { code: 'llm', message: 'overloaded' } });
  s.hydrateTail('thr_e', [
    { id: 'u1', thread_id: 'thr_e', action_id: 'act_e', role: 'user', blocks: [{ type: 'text', text: '你好' }], createdAt: '2026-01-01T00:00:01Z' },
    { id: 'a1', thread_id: 'thr_e', action_id: 'act_e', role: 'assistant', blocks: [{ type: 'text', text: '你好！有什么' }], createdAt: '2026-01-01T00:00:02Z' },
  ], false);
  const msgs = useAgentStore.getState().messages['thr_e'];
  expect(msgs.map((m) => m.id.startsWith('err_') ? 'err' : m.id)).toEqual(['u1', 'a1', 'err']);
  expect(msgs[2].blocks[0].text).toContain('overloaded');
});
