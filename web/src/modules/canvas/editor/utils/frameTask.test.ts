import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createFrameTask } from './frameTask';
import { stubFrames } from './manualFrames.testing';

describe('createFrameTask', () => {
  let frames: ReturnType<typeof stubFrames>;
  beforeEach(() => {
    frames = stubFrames();
  });
  afterEach(() => vi.unstubAllGlobals());

  it('同一帧内多次 schedule 只执行一次', () => {
    const run = vi.fn();
    const task = createFrameTask(run);
    task.schedule();
    task.schedule();
    task.schedule();
    expect(frames.count()).toBe(1);
    expect(run).not.toHaveBeenCalled();
    frames.runFrame();
    expect(run).toHaveBeenCalledTimes(1);
    // 下一帧可以再次登记。
    task.schedule();
    frames.runFrame();
    expect(run).toHaveBeenCalledTimes(2);
  });

  it('flush 立即执行未到帧的任务，之后那一帧不再重复执行', () => {
    const run = vi.fn();
    const task = createFrameTask(run);
    task.schedule();
    task.flush();
    expect(run).toHaveBeenCalledTimes(1);
    frames.runFrame();
    expect(run).toHaveBeenCalledTimes(1);
    // 没有待执行任务时 flush 是 no-op。
    task.flush();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('cancel 丢掉未到帧的任务', () => {
    const run = vi.fn();
    const task = createFrameTask(run);
    task.schedule();
    task.cancel();
    frames.runFrame();
    expect(run).not.toHaveBeenCalled();
  });
});
