import { useEffect } from 'react';
import { api, getCanvasTask } from '@/shared/api';
import { useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';
import { clearPollStalled, usePollStalled } from './pollStall';

// 同一节点在画布和故事板详情里各挂一份组件时(P31),两份 hook 都会起定时器。这里按
// nodeId 登记订阅者,只有最先登记的那份真正发请求,其余只空转排下一轮;先登记的那份
// 卸载后,下一份在自己的下一轮自动接手。
const reconcileOwners = new Map<string, Set<symbol>>();

function joinOwners(nodeId: string, token: symbol): () => void {
  let set = reconcileOwners.get(nodeId);
  if (!set) {
    set = new Set();
    reconcileOwners.set(nodeId, set);
  }
  set.add(token);
  return () => {
    const cur = reconcileOwners.get(nodeId);
    if (!cur) return;
    cur.delete(token);
    if (cur.size === 0) reconcileOwners.delete(nodeId);
  };
}

function isOwner(nodeId: string, token: symbol): boolean {
  const set = reconcileOwners.get(nodeId);
  return set ? set.values().next().value === token : false;
}

// 「永久生成中」死态的自愈对账,管两种情况:
//
// 1. 丢了 taskId。页面得知生成完成的唯一途径是节点组件里「status=generating && taskId」
//    驱动的轮询——taskId 要等提交请求的响应回来才写入节点,这中间只要断一次(响应丢失、
//    落库竞态+刷新),节点就卡在 generating 且没人再看它一眼,而后端其实早已把结果
//    权威写回快照。这里低频按 nodeId 向后端找回任务号,只补写 taskId,让现有轮询 effect
//    自己接管——不重复任何节点各自的结果写回逻辑。
// 2. 轮询挂起(pollStalled,P23)。主轮询连续够不着后端后不判失败、停下来交到这里:低频
//    查一次任务状态,只要后端答话(不管任务进行到哪),就清掉挂起标记,让主轮询 effect
//    重新接管并按原逻辑写回结果。
//
// 两种情况都是连续多轮 404(任务根本不存在)才判死节点,别让它无限转圈;网络抖动/5xx
// 不算。挂起对账另有轮数上限,到了就停,只留 NodeShell 上的「点击重新检查」。
export function useReconcileCanvasTask(
  nodeId: string,
  opts: {
    initialDelayMs?: number;
    intervalMs?: number;
    maxMisses?: number;
    maxStalledChecks?: number;
  } = {},
): void {
  const node = useCanvasStore((s) => s.nodesById.get(nodeId));
  const updateNode = useCanvasStore((s) => s.updateNode);
  const stalled = usePollStalled(node?.taskId);
  const generating = node?.status === 'generating';
  const taskId = node?.taskId;
  const mode: 'lost' | 'stalled' | null = !generating
    ? null
    : !taskId
      ? 'lost'
      : stalled
        ? 'stalled'
        : null;

  // 首查延迟默认 5s:正常提交路径几百毫秒内就会补上 taskId,别为每次生成都
  // 多打一发;间隔 30s——这是兜底不是主路径,与 3s 的主轮询错开数量级。
  const initialDelay = opts.initialDelayMs ?? 5000;
  const interval = opts.intervalMs ?? 30000;
  const maxMisses = opts.maxMisses ?? 4;
  // 挂起对账默认最多 20 轮(约 10 分钟):sidecar 更久都没回来,多半要用户介入,
  // 不在后台一直敲。
  const maxStalledChecks = opts.maxStalledChecks ?? 20;

  useEffect(() => {
    if (!mode) return undefined;
    const projectId = Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]);
    if (mode === 'lost' && !projectId) return undefined;

    const token = Symbol(nodeId);
    const leave = joinOwners(nodeId, token);
    let cancelled = false;
    let misses = 0;
    let checks = 0;
    let timer: ReturnType<typeof setTimeout>;

    const markGone = () => {
      updateNode(nodeId, {
        status: 'failed',
        error: '生成任务未找到,可能提交未成功,请重试',
        taskId: undefined,
      });
      persistNodeById(nodeId);
    };

    const tick = async () => {
      if (!isOwner(nodeId, token)) {
        // 另一份组件在对账,这份只排下一轮,等它卸载后接手。
        timer = setTimeout(tick, interval);
        return;
      }
      try {
        if (mode === 'lost') {
          const { data } = await api.get<{ taskId: number }>('/canvas-tasks/by-node', {
            params: { projectId, nodeId },
          });
          if (cancelled) return;
          if (data?.taskId) {
            // 只补 taskId:mode 随之翻成 null,本 effect 退场,
            // 现有轮询 effect(以 status+taskId 为 key)立即接管并拿到状态。
            updateNode(nodeId, { taskId: data.taskId });
            return;
          }
        } else {
          await getCanvasTask(taskId!);
          if (cancelled) return;
          // 后端答话了:清挂起标记,主轮询 effect 重新接管(终态也由它照常写回)。
          clearPollStalled(taskId!);
          return;
        }
      } catch (err: any) {
        if (cancelled) return;
        // 只有 404(确定查无)计入判死;网络抖动/5xx 不算——那说明后端暂时够不着,
        // 任务可能好好活着,误判死会把用户等着的结果标成失败。
        if (err?.response?.status === 404) {
          misses += 1;
          if (misses >= maxMisses) {
            markGone();
            if (mode === 'stalled') clearPollStalled(taskId!);
            return;
          }
        } else {
          misses = 0;
        }
      }
      if (mode === 'stalled') {
        checks += 1;
        if (checks >= maxStalledChecks) return;
      }
      timer = setTimeout(tick, interval);
    };

    // 挂起对账不用 5s 首查:主轮询刚连续失败了好几轮,马上再敲大概率还是够不着。
    timer = setTimeout(tick, mode === 'lost' ? initialDelay : interval);
    return () => {
      cancelled = true;
      clearTimeout(timer);
      leave();
    };
  }, [mode, taskId, nodeId, updateNode, initialDelay, interval, maxMisses, maxStalledChecks]);
}
