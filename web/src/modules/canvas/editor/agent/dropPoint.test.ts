import { describe, it, expect } from 'vitest';
import {
  AGENT_GRID_COL_STEP,
  AGENT_GRID_COLS,
  AGENT_GRID_ROW_STEP,
  agentDropPointFor,
  claimAgentSlot,
  seqFromName,
} from './dropPoint';

const ANCHOR = { anchorX: 1000, anchorY: 500 };

describe('agentDropPointFor', () => {
  it('第一个格子落在锚点', () => {
    expect(agentDropPointFor(ANCHOR, 0)).toEqual({ x: 1000, y: 500 });
  });

  it('同一行只动 x，一行满 5 个后换行只动 y', () => {
    expect(agentDropPointFor(ANCHOR, 4)).toEqual({ x: 1000 + 4 * AGENT_GRID_COL_STEP, y: 500 });
    expect(agentDropPointFor(ANCHOR, 5)).toEqual({ x: 1000, y: 500 + AGENT_GRID_ROW_STEP });
    expect(agentDropPointFor(ANCHOR, 12)).toEqual({
      x: 1000 + 2 * AGENT_GRID_COL_STEP,
      y: 500 + 2 * AGENT_GRID_ROW_STEP,
    });
  });

  it('同列 x 严格相同、同行 y 严格相同', () => {
    const pts = Array.from({ length: 13 }, (_, i) => agentDropPointFor(ANCHOR, i));
    for (let i = 0; i < pts.length; i++) {
      const sameCol = pts.filter((_, j) => j % AGENT_GRID_COLS === i % AGENT_GRID_COLS);
      const sameRow = pts.filter(
        (_, j) => Math.floor(j / AGENT_GRID_COLS) === Math.floor(i / AGENT_GRID_COLS),
      );
      expect(new Set(sameCol.map((p) => p.x)).size).toBe(1);
      expect(new Set(sameRow.map((p) => p.y)).size).toBe(1);
    }
  });

  it('步长宽到装得下最极端比例的节点框（21:9 → 长边 653）', () => {
    expect(AGENT_GRID_COL_STEP).toBeGreaterThan(Math.round((280 * 21) / 9));
    expect(AGENT_GRID_ROW_STEP).toBeGreaterThan(Math.round((280 * 21) / 9));
  });
});

describe('seqFromName', () => {
  it('吃得下 agent 的各种编号写法', () => {
    expect(seqFromName('第13段·施工对峙')).toBe(13);
    expect(seqFromName('镜头2')).toBe(2);
    expect(seqFromName('Shot 3')).toBe(3);
    expect(seqFromName('1. 开场')).toBe(1);
    expect(seqFromName('7')).toBe(7);
  });

  it('没有阿拉伯数字就是没序号', () => {
    expect(seqFromName('救护车受阻')).toBeNull();
    expect(seqFromName('')).toBeNull();
    expect(seqFromName(undefined)).toBeNull();
  });

  it('大得不像序号的数字不认——年份/金额会把节点甩到荒野', () => {
    expect(seqFromName('2026年的对峙')).toBeNull();
    expect(seqFromName('赔了5000块')).toBeNull();
  });
});

describe('claimAgentSlot', () => {
  it('乱序到达也按序号各就各位', () => {
    const taken = new Set<number>();
    // 截图里的真实到达顺序：agent 并发产出，13 先于 1 回来。
    expect(claimAgentSlot(taken, '第13段·施工对峙')).toBe(12);
    expect(claimAgentSlot(taken, '第9段·救护车受阻')).toBe(8);
    expect(claimAgentSlot(taken, '第1段·狭窄通道')).toBe(0);
    expect(claimAgentSlot(taken, '第5段·盖房阻挠')).toBe(4);
  });

  it('重号顺延到下一个空格子，不叠在一起', () => {
    const taken = new Set<number>();
    expect(claimAgentSlot(taken, '第3段·甲')).toBe(2);
    expect(claimAgentSlot(taken, '第3段·乙')).toBe(3);
    expect(claimAgentSlot(taken, '第4段·丙')).toBe(4);
  });

  it('无序号的填最小空位', () => {
    const taken = new Set<number>();
    expect(claimAgentSlot(taken, '第2段·有号')).toBe(1);
    expect(claimAgentSlot(taken, '无号甲')).toBe(0);
    expect(claimAgentSlot(taken, '无号乙')).toBe(2);
  });

  it('0-based 编号仍然单调不重叠', () => {
    const taken = new Set<number>();
    const slots = [0, 1, 2, 3].map((n) => claimAgentSlot(taken, `镜头${n}`));
    expect(new Set(slots).size).toBe(slots.length);
    expect([...slots].sort((a, b) => a - b)).toEqual(slots);
  });

  it('13 个节点各占一格，无一重叠', () => {
    const taken = new Set<number>();
    const names = [13, 9, 8, 6, 7, 3, 4, 1, 2, 11, 5, 10, 12].map((n) => `第${n}段`);
    const pts = names.map((n) => agentDropPointFor(ANCHOR, claimAgentSlot(taken, n)));
    expect(new Set(pts.map((p) => `${p.x},${p.y}`)).size).toBe(13);
  });
});
