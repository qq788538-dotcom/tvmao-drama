import { useEffect, useRef, useState } from 'react';

/**
 * 生成计时条。`startedAt` 是用户点「生成」的那一刻（毫秒时间戳），由节点从持久化的
 * `settings.pendingGen.submittedAt` 读出并传入——刷新页面后 hook 重新挂载，
 * 若以挂载时刻当起点，计时会从 0 重来，用户看到的「用时」就是假的。
 *
 * 起点取提交时刻而不是服务端任务创建时刻：对用户而言按下按钮就开始等了，
 * 队列排队也算在内，且排队这件事不该暴露给用户。
 *
 * 不传 `startedAt`（存量快照里没有这个字段、或 PreviewNode 这类由 agent 帧
 * 驱动 status 的节点）时回退到挂载时刻，维持旧行为。
 */
export function useGenerationTimer(isGenerating: boolean, startedAt?: number) {
  const [elapsed, setElapsed] = useState(0);
  const [finalDuration, setFinalDuration] = useState<number | null>(null);
  const startRef = useRef<number | null>(null);
  const rafRef = useRef<number>(0);

  useEffect(() => {
    if (isGenerating) {
      setFinalDuration(null);
      startRef.current = startedAt ?? Date.now();
      const tick = () => {
        if (startRef.current) {
          setElapsed((Date.now() - startRef.current) / 1000);
        }
        rafRef.current = requestAnimationFrame(tick);
      };
      rafRef.current = requestAnimationFrame(tick);
      return () => cancelAnimationFrame(rafRef.current);
    }
    // 生成结束
    if (startRef.current) {
      const duration = (Date.now() - startRef.current) / 1000;
      setFinalDuration(duration);
      startRef.current = null;
    }
    setElapsed(0);
    // startedAt 与 isGenerating 同批写入（提交时一起落 settings/status），
    // 只以 isGenerating 为 key 就够；不加 startedAt 是为了避免 effect 重跑打断计时。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isGenerating]);

  return { elapsed, finalDuration };
}
