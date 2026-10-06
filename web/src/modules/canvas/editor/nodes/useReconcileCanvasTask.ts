import { useEffect } from 'react';
import { api } from '@/shared/api';
import { useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';

// 「永久生成中」死态的自愈对账。
//
// 页面得知生成完成的唯一途径是节点组件里「status=generating && taskId」驱动的
// 轮询——taskId 要等提交请求的响应回来才写入节点,这中间只要断一次(响应丢失、
// 落库竞态+刷新),节点就卡在 generating 且没人再看它一眼,而后端其实早已把结果
// 权威写回快照。这个 hook 盯住这个死态:低频按 nodeId 向后端找回任务号,只补写
// taskId,让现有轮询 effect 自己接管——不重复任何节点各自的结果写回逻辑。
// 连续多轮 404(任务根本没提交成功)才判死节点,别让它无限转圈。
export function useReconcileCanvasTask(
  nodeId: string,
  opts: { initialDelayMs?: number; intervalMs?: number; maxMisses?: number } = {},
): void {
  const node = useCanvasStore((s) => s.nodes.find((n) => n.id === nodeId));
  const updateNode = useCanvasStore((s) => s.updateNode);
  const needsReconcile = node?.status === 'generating' && !node?.taskId;

  // 首查延迟默认 5s:正常提交路径几百毫秒内就会补上 taskId,别为每次生成都
  // 多打一发;间隔 30s——这是兜底不是主路径,与 3s 的主轮询错开数量级。
  const initialDelay = opts.initialDelayMs ?? 5000;
  const interval = opts.intervalMs ?? 30000;
  const maxMisses = opts.maxMisses ?? 4;

  useEffect(() => {
    if (!needsReconcile) return undefined;
    const projectId = Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]);
    if (!projectId) return undefined;

    let cancelled = false;
    let misses = 0;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      try {
        const { data } = await api.get<{ taskId: number }>('/canvas-tasks/by-node', {
          params: { projectId, nodeId },
        });
        if (cancelled) return;
        if (data?.taskId) {
          // 只补 taskId:effect 依赖里 needsReconcile 随之翻 false,本 hook 退场,
          // 现有轮询 effect(以 status+taskId 为 key)立即接管并拿到状态。
          updateNode(nodeId, { taskId: data.taskId });
          return;
        }
      } catch (err: any) {
        if (cancelled) return;
        // 只有 404(确定查无)计入判死;网络抖动/5xx 不算——那说明后端暂时够不着,
        // 任务可能好好活着,误判死会把用户等着的结果标成失败。
        if (err?.response?.status === 404) {
          misses += 1;
          if (misses >= maxMisses) {
            updateNode(nodeId, {
              status: 'failed',
              error: '生成任务未找到,可能提交未成功,请重试',
              taskId: undefined,
            });
            persistNodeById(nodeId);
            return;
          }
        }
      }
      timer = setTimeout(tick, interval);
    };

    timer = setTimeout(tick, initialDelay);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [needsReconcile, nodeId, updateNode, initialDelay, interval, maxMisses]);
}
