import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DRAG_TRANSIENT_THROTTLE_MS,
  isTransientChannelOpen,
  parseTransientDragPositions,
  resetDragTransientThrottle,
  sendDragTransient,
  sendNodeEditingTransient,
  setTransientChannel,
  shouldSendDragTransient,
  type OutgoingTransientFrame,
} from './transientChannel';

describe('瞬态通道', () => {
  let sent: OutgoingTransientFrame[] = [];

  beforeEach(() => {
    vi.useFakeTimers();
    sent = [];
    setTransientChannel({
      projectId: 14,
      send: (frame) => {
        sent.push(frame);
        return true;
      },
    });
  });

  afterEach(() => {
    setTransientChannel(null);
    vi.useRealTimers();
  });

  it('拖拽帧带项目 id、kind=drag 与绝对坐标', () => {
    sendDragTransient([{ id: 'n1', x: 10, y: 20 }]);
    expect(sent).toEqual([
      {
        type: 'canvas_transient',
        project_id: 14,
        kind: 'drag',
        positions: [{ id: 'n1', x: 10, y: 20 }],
      },
    ]);
  });

  // 节流是这条流的成本闸：一次两秒的拖拽 mousemove 上百次，不节流就是上百帧
  // 广播，每帧还要在服务端扇出给项目下所有连接。
  it('200ms 窗口内连发多次只出一帧，跨过窗口才出下一帧', () => {
    resetDragTransientThrottle();
    sendDragTransient([{ id: 'n1', x: 1, y: 1 }]);
    vi.advanceTimersByTime(50);
    sendDragTransient([{ id: 'n1', x: 2, y: 2 }]);
    vi.advanceTimersByTime(50);
    sendDragTransient([{ id: 'n1', x: 3, y: 3 }]);
    expect(sent).toHaveLength(1);
    expect(sent[0].positions).toEqual([{ id: 'n1', x: 1, y: 1 }]);

    vi.advanceTimersByTime(DRAG_TRANSIENT_THROTTLE_MS);
    sendDragTransient([{ id: 'n1', x: 9, y: 9 }]);
    expect(sent).toHaveLength(2);
    expect(sent[1].positions).toEqual([{ id: 'n1', x: 9, y: 9 }]);
  });

  it('重置节流窗口后下一帧立刻发出（每次拖拽的起手帧不被上一次吞掉）', () => {
    sendDragTransient([{ id: 'n1', x: 1, y: 1 }]);
    vi.advanceTimersByTime(10);
    sendDragTransient([{ id: 'n1', x: 2, y: 2 }]);
    expect(sent).toHaveLength(1);

    resetDragTransientThrottle();
    sendDragTransient([{ id: 'n1', x: 3, y: 3 }]);
    expect(sent).toHaveLength(2);
  });

  it('编辑提示帧不节流：进入/退出与心跳都要如实发出', () => {
    sendNodeEditingTransient('n1', true);
    sendNodeEditingTransient('n1', true);
    sendNodeEditingTransient('n1', false);
    expect(sent.map((f) => f.editing)).toEqual([true, true, false]);
    expect(sent[0]).toMatchObject({ kind: 'node_editing', node_id: 'n1', project_id: 14 });
  });

  it('空坐标批不发帧', () => {
    expect(sendDragTransient([])).toBe(false);
    expect(sent).toHaveLength(0);
  });

  // 个人项目零帧的机制保证：页面根本不注册通道，所有发送点自动退化成 no-op。
  it('通道未注册（个人项目）时一帧都发不出去', () => {
    setTransientChannel(null);
    expect(isTransientChannelOpen()).toBe(false);
    expect(sendDragTransient([{ id: 'n1', x: 1, y: 1 }])).toBe(false);
    expect(sendNodeEditingTransient('n1', true)).toBe(false);
    expect(sent).toHaveLength(0);
  });

  // 【L4】WS 断着时每帧都返回 false。若这也算「刚发过」，重连成功后的头 200ms
  // 仍然发不出去——队友那边的节点会先卡一下再跳，而这段恰是最需要实时的重连瞬间。
  it('发送失败（WS 未 OPEN）不消耗节流窗口，连上后立刻能发', () => {
    let open = false;
    const delivered: OutgoingTransientFrame[] = [];
    setTransientChannel({
      projectId: 14,
      send: (f) => {
        if (!open) return false;
        delivered.push(f);
        return true;
      },
    });

    expect(sendDragTransient([{ id: 'n1', x: 1, y: 1 }])).toBe(false);
    vi.advanceTimersByTime(10);
    expect(sendDragTransient([{ id: 'n1', x: 2, y: 2 }])).toBe(false);

    open = true;
    // 距上一次「尝试发送」只过了 10ms：若失败也算消耗窗口，这一帧会被吞掉。
    expect(sendDragTransient([{ id: 'n1', x: 3, y: 3 }])).toBe(true);
    expect(delivered).toHaveLength(1);
    // 成功那帧才真正开窗：紧接着的一帧照常被节流。
    vi.advanceTimersByTime(10);
    expect(sendDragTransient([{ id: 'n1', x: 4, y: 4 }])).toBe(false);
    expect(delivered).toHaveLength(1);
  });

  it('重新注册通道会重置节流窗口（换连接不该吞掉第一帧）', () => {
    sendDragTransient([{ id: 'n1', x: 1, y: 1 }]);
    const later: OutgoingTransientFrame[] = [];
    setTransientChannel({ projectId: 15, send: (f) => { later.push(f); return true; } });
    sendDragTransient([{ id: 'n1', x: 2, y: 2 }]);
    expect(later).toHaveLength(1);
    expect(later[0].project_id).toBe(15);
  });
});

// mousemove 每帧都调，而组装坐标是「多选数 × 节点数」的线性查找；60fps 下六帧
// 里有五帧会被节流丢掉。前置判断必须和内部节流判定完全一致，否则要么白算、要么
// 漏发。
describe('shouldSendDragTransient（热路径前置判断）', () => {
  let sent: OutgoingTransientFrame[] = [];

  beforeEach(() => {
    vi.useFakeTimers();
    sent = [];
    setTransientChannel({ projectId: 14, send: (f) => { sent.push(f); return true; } });
  });

  afterEach(() => {
    setTransientChannel(null);
    vi.useRealTimers();
  });

  it('与内部节流同步：发过之后窗口内为 false，过窗后回 true', () => {
    expect(shouldSendDragTransient()).toBe(true);
    sendDragTransient([{ id: 'n1', x: 1, y: 1 }]);
    expect(shouldSendDragTransient()).toBe(false);
    vi.advanceTimersByTime(DRAG_TRANSIENT_THROTTLE_MS);
    expect(shouldSendDragTransient()).toBe(true);
  });

  it('没有通道（个人项目）恒 false——一帧坐标都不用组装', () => {
    setTransientChannel(null);
    expect(shouldSendDragTransient()).toBe(false);
  });
});

describe('parseTransientDragPositions', () => {
  it('丢弃坏条目而不是整帧作废（缺 x/y 会把节点弹到 NaN 坐标）', () => {
    expect(
      parseTransientDragPositions([
        { id: 'n1', x: 1, y: 2 },
        { id: 'n2', x: '3', y: 4 },
        { id: '', x: 1, y: 1 },
        { id: 'n3', y: 4 },
        { id: 'n4', x: Number.NaN, y: 4 },
        null,
        { id: 'n5', x: 7, y: 8 },
      ]),
    ).toEqual([
      { id: 'n1', x: 1, y: 2 },
      { id: 'n5', x: 7, y: 8 },
    ]);
  });

  it('非数组一律空批', () => {
    expect(parseTransientDragPositions(undefined)).toEqual([]);
    expect(parseTransientDragPositions({ id: 'n1' })).toEqual([]);
  });
});
