import { describe, expect, it, vi } from 'vitest';
import { saveAnnotations, type SaveDeps } from './save';
import { newFrame, pushOp, type AnnoOp } from './engine';

const op: AnnoOp = { id: 'a', tool: 'rect', points: [{ x: 0, y: 0 }, { x: 1, y: 1 }] };
const entry = (t: number, note: string) => ({ ...pushOp(newFrame(t), op), note });

function okDeps(): SaveDeps & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    captureFrame: vi.fn(async (e: { timeSec: number }) => { calls.push(`cap:${e.timeSec}`); return new Blob(['x']); }),
    // materialize 是「建节点+上传」二合一（见 save.ts 的 SaveDeps 注释），测试里
    // 不关心 blob/filename 内容，只按下标记一次调用顺序即可。
    materialize: vi.fn(async (_blob: Blob, _filename: string, i: number) => { calls.push(`node:${i}`); return `n-${i}`; }),
    connect: vi.fn((nodeId: string) => { calls.push(`conn:${nodeId}`); }),
    appendPrompt: vi.fn((line: string) => { calls.push(`prompt:${line}`); }),
  };
}

describe('saveAnnotations', () => {
  it('逐条：抓帧→建节点+上传→连线→追加提示词，格式逐字', async () => {
    const deps = okDeps();
    const res = await saveAnnotations([entry(2, '项链变成黄金项链')], deps);
    expect(res).toEqual({ ok: 1 });
    expect(deps.appendPrompt).toHaveBeenCalledWith('@[图片:n-0] 00:02 视频帧标注 项链变成黄金项链');
    expect(deps.calls).toEqual(['cap:2', 'node:0', 'conn:n-0', 'prompt:@[图片:n-0] 00:02 视频帧标注 项链变成黄金项链']);
  });
  it('第二条建节点/上传失败：停点报告，第一条产物保留、失败条目之后不执行', async () => {
    const deps = okDeps();
    (deps.materialize as ReturnType<typeof vi.fn>)
      .mockImplementationOnce(async (_b: Blob, _f: string, i: number) => { deps.calls.push(`node:${i}`); return `n-${i}`; })
      .mockImplementationOnce(async () => { throw new Error('boom'); });
    const res = await saveAnnotations([entry(0, 'a'), entry(2, 'b'), entry(4, 'c')], deps);
    expect(res.ok).toBe(1);
    expect(res.failedAt).toBe(1);
    expect(res.error).toContain('boom');
    expect(deps.materialize).toHaveBeenCalledTimes(2); // 第一条建成、第二条即失败，第三条未执行
    expect(deps.captureFrame).toHaveBeenCalledTimes(2);    // 第三条未执行
  });
  it('说明可选：留空照常保存，追加行只有时间戳+标记词、行尾无悬空空格', async () => {
    const deps = okDeps();
    const res = await saveAnnotations([entry(0, '  ')], deps);
    expect(res).toEqual({ ok: 1 });
    expect(deps.appendPrompt).toHaveBeenCalledWith('@[图片:n-0] 00:00 视频帧标注');
  });
});
