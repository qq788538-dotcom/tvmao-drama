// 每帧最多执行一次的任务：拖动、滚轮这类高频输入先把增量攒在调用方，
// schedule() 只登记「下一帧要提交一次」，同一帧内多次 schedule 只跑一次 run。
// 高刷鼠标/触控板一帧能来好几个事件，每个都 set store 会让订阅方一帧重渲染多次（P5、P21）。
//
// flush() 用在松手 / 失焦这类收尾点：把还没等到下一帧的那次提交立刻做掉，
// 调用方随后读 store 拿到的就是最终状态（落库、写本地视口都依赖这一点）。
// cancel() 用在卸载：丢掉没提交的那一帧，不再碰 store。
export interface FrameTask {
  schedule: () => void;
  flush: () => void;
  cancel: () => void;
}

export function createFrameTask(run: () => void): FrameTask {
  let handle: number | null = null;
  return {
    schedule() {
      if (handle !== null) return;
      handle = requestAnimationFrame(() => {
        handle = null;
        run();
      });
    },
    flush() {
      if (handle === null) return;
      cancelAnimationFrame(handle);
      handle = null;
      run();
    },
    cancel() {
      if (handle === null) return;
      cancelAnimationFrame(handle);
      handle = null;
    },
  };
}
