import { describe, expect, it } from 'vitest';
import {
  composeFrame, eraseAt, formatTime, newFrame, pushOp, redo, undo,
  type AnnoOp,
} from './engine';

const rect = (id: string): AnnoOp => ({
  id, tool: 'rect', points: [{ x: 10, y: 10 }, { x: 100, y: 80 }],
});

describe('engine 撤销/重做', () => {
  it('pushOp 追加并清空 redo 栈', () => {
    let f = newFrame(2);
    f = pushOp(f, rect('a'));
    f = undo(f);
    f = pushOp(f, rect('b'));
    expect(f.ops.map((o) => o.id)).toEqual(['b']);
    expect(f.redoStack).toEqual([]); // 新笔画后旧的重做分支必须作废
  });
  it('undo/redo 往返', () => {
    let f = pushOp(pushOp(newFrame(0), rect('a')), rect('b'));
    f = undo(f);
    expect(f.ops.map((o) => o.id)).toEqual(['a']);
    f = redo(f);
    expect(f.ops.map((o) => o.id)).toEqual(['a', 'b']);
    expect(redo(f)).toBe(f); // 空 redo 栈原样返回
  });
  it('不可变：pushOp 不改入参', () => {
    const f0 = newFrame(0);
    pushOp(f0, rect('a'));
    expect(f0.ops).toEqual([]);
  });
});

describe('eraseAt 命中删除', () => {
  it('点中矩形边缘删除该 op，未命中原样返回', () => {
    const f = pushOp(newFrame(0), rect('a'));
    expect(eraseAt(f, 10, 40, 8).ops).toEqual([]); // 左边缘上
    expect(eraseAt(f, 500, 500, 8)).toBe(f);       // 远处未命中
  });
  it('pen 折线按线段距离命中', () => {
    const pen: AnnoOp = { id: 'p', tool: 'pen', points: [{ x: 0, y: 0 }, { x: 100, y: 0 }] };
    const f = pushOp(newFrame(0), pen);
    expect(eraseAt(f, 50, 5, 8).ops).toEqual([]);   // 线段中点附近
    expect(eraseAt(f, 50, 30, 8).ops).toHaveLength(1);
  });
});

describe('formatTime', () => {
  it('mm:ss 补零', () => {
    expect(formatTime(0)).toBe('00:00');
    expect(formatTime(2.4)).toBe('00:02');
    expect(formatTime(75)).toBe('01:15');
  });
});

describe('composeFrame', () => {
  it('输出画布尺寸 = 指定原始尺寸（jsdom 无 2d 渲染，断言尺寸与不抛错即可）', () => {
    const src = document.createElement('canvas');
    src.width = 320; src.height = 180;
    const out = composeFrame(src, [rect('a')], 320, 180);
    expect(out.width).toBe(320);
    expect(out.height).toBe(180);
  });
});
