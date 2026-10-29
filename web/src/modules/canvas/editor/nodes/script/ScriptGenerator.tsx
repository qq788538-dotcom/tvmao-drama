import { useEffect, useRef, useState } from 'react';
import { ArrowUp, Loader2 } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import {
  getCanvasScriptStyles,
  getCanvasScriptTask,
  submitCanvasScriptTask,
} from '@/shared/api';
import { useCanvasStore } from '../../store';
import { flushNodePatch } from '../../persistence/canvasMutations';
import { Select } from 'antd';
import { useScriptSettings } from './useScriptSettings';
import { importCandidate } from './importCandidate';
import { ScriptStyleSelect } from './ScriptStyleSelect';
import { MIN_SHOT_SECONDS, DEFAULT_DURATION_SEC, MAX_SCRIPT_LEN, readScriptSettings } from './scriptData';

// 「剧本生成分镜脚本」生成器：脚本节点下方的浮动输入框。
// 用户贴剧本 + 选风格（后台配置，映射 skill）+ 选最长秒数（DURATION_SEC，skill
// 必传）→ 提交异步任务 → 轮询 → succeeded 后把 candidate.json 导入节点。
// 没有参考图 chips——用户明确砍掉（生成只吃文字剧本）。

const POLL_INTERVAL_MS = 3000;
// 连续这么多次 404 才判定任务行已不存在（约 6s）：单次 404 可能是提交刚返回、
// 读到了还没提交完的事务，或者 sidecar 重启的瞬间；401/网络错误/5xx 不计数。
const MAX_NOT_FOUND = 3;
const TASK_GONE_ERROR = '生成任务已不存在，请重新提交';

// 轮询挂在 ScriptNode 层（不是面板里）：taskId 持久化在 settings.gen 里，任务在
// 服务端跑，面板关掉/页面刷新都不该中断观察——只要节点还在画布上就继续轮询。
export function useScriptGenPolling(nodeId: string) {
  const { settings, patch } = useScriptSettings(nodeId);
  const taskId = settings.gen.taskId;
  // patch/settings 每次渲染都是新引用，放依赖里会让 interval 反复重建；用 ref
  // 保持最新引用，effect 只依赖 taskId 本身。
  const latest = useRef({ patch, settings });
  latest.current = { patch, settings };

  useEffect(() => {
    if (!taskId) return;
    let stopped = false;
    let notFound = 0;
    const tick = async () => {
      let st;
      try {
        st = await getCanvasScriptTask(taskId);
      } catch (err: any) {
        if (stopped) return;
        // 只有 404（确定查无）计入判死，同 useReconcileCanvasTask：401/网络抖动/5xx
        // 说明后端暂时够不着，任务可能好好活着，下一轮再试。判死后清 taskId 并写
        // 错误，用户才能重新提交，不至于永久转圈（P12）。
        if (err?.response?.status === 404) {
          notFound += 1;
          if (notFound >= MAX_NOT_FOUND) {
            const { patch: p, settings: cur } = latest.current;
            p({ gen: { ...cur.gen, taskId: 0, error: TASK_GONE_ERROR } });
          }
        }
        return;
      }
      if (stopped || !st) return;
      notFound = 0;
      const { patch: p, settings: cur } = latest.current;
      // 任务属于别的节点：多半是复制/粘贴把 taskId 带进了副本（存量数据，新副本
      // 在 sanitizeNodeCopy 里已清掉）。不认领，清掉 taskId，否则会把别人的分镜
      // 导入这里、覆盖已有内容（P11）。
      if (st.nodeId && st.nodeId !== nodeId) {
        p({ gen: { ...cur.gen, taskId: 0, error: '' } });
        return;
      }
      if (st.status === 'succeeded') {
        try {
          const imported = importCandidate(st.result ?? '', cur);
          p({ ...imported, gen: { ...cur.gen, taskId: 0, error: '' } });
        } catch (e) {
          p({ gen: { ...cur.gen, taskId: 0, error: e instanceof Error ? e.message : '导入失败' } });
        }
      } else if (st.status === 'failed') {
        p({ gen: { ...cur.gen, taskId: 0, error: st.error || '生成失败' } });
      }
    };
    void tick();
    const timer = window.setInterval(() => void tick(), POLL_INTERVAL_MS);
    return () => {
      stopped = true;
      window.clearInterval(timer);
    };
  }, [taskId, nodeId]);
}

// AntD Select/Cascader 弹层默认 portal 到 body——画布是 transform 缩放容器，
// 自绘 PopoverShell 的 fixed 定位在里面 containing block 错乱会飘到别处。
const DURATION_OPTIONS = Array.from(
  { length: DEFAULT_DURATION_SEC - MIN_SHOT_SECONDS + 1 },
  (_, i) => {
    const v = MIN_SHOT_SECONDS + i;
    return { value: v, label: `最长 ${v}s` };
  },
);

export function ScriptGenerator({ nodeId }: { nodeId: string }) {
  const { settings, patch, setScriptPrompt } = useScriptSettings(nodeId);
  const gen = settings.gen;
  const running = gen.taskId > 0;
  const [submitting, setSubmitting] = useState(false);
  // 剧本文本：本地 draft 驱动输入框，每次输入同时经 setScriptPrompt 写进 store、
  // 只对 scriptPrompt 一个字段排防抖落库。不能等 blur/submit 再写——点画布空白处
  // 取消选中时焦点不离开 textarea，面板直接卸载，草稿就丢了（P13）。
  const [draft, setDraft] = useState(settings.scriptPrompt);

  const stylesQuery = useQuery({
    queryKey: ['canvas-script-styles'],
    queryFn: getCanvasScriptStyles,
    staleTime: 60_000,
    retry: false,
  });
  const styles = stylesQuery.data?.styles ?? [];
  const candidates = stylesQuery.data?.modelCandidates ?? [];
  const model = gen.model;
  const selectedModel = candidates.find((c) => c.modelId === model);
  // 上游文字节点的内容也是剧本来源：连线 text → script 即接入，多个按连线顺序
  // 拼接，提交时排在输入框文本前面。selector 返回拼接串（string 相等即不重渲染）。
  const upstreamText = useCanvasStore((s) =>
    s.connections
      .filter((c) => c.to === nodeId)
      .map((c) => s.nodes.find((n) => n.id === c.from))
      .filter((n) => n?.type === 'text-node')
      .map((n) => ((n!.settings?.text as string | undefined) ?? '').trim())
      .filter(Boolean)
      .join('\n\n'),
  );
  // 风格标识串 "name" 或 "name version"；默认第一个由 ScriptStyleSelect 的
  // autoSelect 回写到 settings.gen.style。
  const style = gen.style;

  // 默认选第一个候选；存量节点里存的模型若已被移出候选也回落第一项——
  // 白名单外提交不了，回显一个选不了的值没有意义。
  // 用 store 现读最新 gen 而不是这次渲染闭包里的 gen：同一次 query 结果落地会
  // 与下面 ScriptStyleSelect 的 autoSelect 效应同帧触发，子组件（先跑）先把
  // style 写回 store，若这里仍拼接闭包里的旧 gen 会把刚写回的 style 覆盖掉。
  useEffect(() => {
    if (!candidates.length) return;
    if (candidates.some((c) => c.modelId === model)) return;
    const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
    const freshGen = readScriptSettings(node?.settings as Record<string, unknown> | undefined).gen;
    patch({ gen: { ...freshGen, model: candidates[0].modelId } });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [candidates]);

  // 实际提交的剧本 = 上游文字节点内容 + 输入框内容（都可单独为空，合并非空即可）。
  const effectiveScript = [upstreamText, draft.trim()].filter(Boolean).join('\n\n');
  const overLimit = effectiveScript.length > MAX_SCRIPT_LEN;
  // 存量节点存的模型可能已被移出候选；仅判非空会让按钮可点并把内部 slug 暴露给用户，
  // 必须要求是当前候选列表里活着的一项。
  const canSubmit = !running && !submitting && effectiveScript !== '' && !overLimit && style !== '' && !!selectedModel;

  async function submit() {
    if (!canSubmit) return;
    const projectId =
      Number(window.location.pathname.split('/canvas/')[1]?.split('/')[0]) || 0;
    setSubmitting(true);
    try {
      const res = await submitCanvasScriptTask({
        projectId,
        nodeId,
        style,
        durationSec: gen.durationSec,
        script: effectiveScript,
        model,
      });
      patch({
        scriptPrompt: draft,
        gen: { ...gen, taskId: res.taskId, style, error: '' },
      });
    } catch (e: any) {
      patch({
        scriptPrompt: draft,
        gen: { ...gen, style, error: e?.response?.data?.error || '提交失败，请重试' },
      });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      data-testid="script-generator"
      className="bg-card rounded-[20px] border border-border shadow-xl flex flex-col"
      onMouseDown={(e) => e.stopPropagation()}
    >
      {upstreamText !== '' && (
        <div data-testid="script-generator-upstream" className="mx-3 mt-3 -mb-1 text-[11px] text-muted-foreground">
          已接入上游文字节点 {upstreamText.length} 字，将与下方输入一起提交
        </div>
      )}
      <textarea
        data-testid="script-generator-input"
        value={draft}
        disabled={running}
        maxLength={MAX_SCRIPT_LEN}
        placeholder="描述剧情片段、故事，为你生成分镜脚本"
        onChange={(e) => {
          setDraft(e.target.value);
          setScriptPrompt(e.target.value);
        }}
        onBlur={() => {
          void flushNodePatch(nodeId);
        }}
        className="script-borders mx-3 mt-3 mb-1 min-h-[96px] max-h-[240px] resize-none bg-transparent text-sm text-foreground placeholder:text-muted-foreground/60 border-none outline-none custom-scrollbar disabled:opacity-60"
      />
      <div
        data-testid="script-generator-count"
        className={`mx-3 -mt-0.5 mb-1 text-right text-[11px] ${overLimit ? 'text-red-400' : 'text-muted-foreground/70'}`}
      >
        {effectiveScript.length}/{MAX_SCRIPT_LEN}
      </div>
      {gen.error && !running && (
        <div data-testid="script-generator-error" className="mx-3 mb-1 text-xs text-red-400">
          {gen.error}
        </div>
      )}
      <div className="px-3 pb-3 pt-1 flex items-center gap-2">
        {styles.length > 0 ? (
          <ScriptStyleSelect
            value={style}
            disabled={running}
            styles={styles}
            loading={stylesQuery.isLoading}
            onChange={(v) => patch({ gen: { ...gen, style: v } })}
          />
        ) : (
          <span className="text-xs text-muted-foreground px-1">
            {stylesQuery.isLoading ? '加载风格中…' : '暂无可用风格'}
          </span>
        )}
        <Select
          aria-label="最长秒数"
          value={gen.durationSec}
          disabled={running}
          options={DURATION_OPTIONS}
          onChange={(v) => patch({ gen: { ...gen, durationSec: v } })}
          style={{ minWidth: 104 }}
          popupMatchSelectWidth={false}
          placement="topLeft"
        />
        {/* 选项只给模型名。 */}
        <Select
          aria-label="生成模型"
          value={selectedModel ? model : undefined}
          disabled={running}
          placeholder={stylesQuery.isLoading ? '加载模型…' : '暂无可用模型'}
          options={candidates.map((c) => ({ value: c.modelId, label: c.displayName }))}
          onChange={(v) => patch({ gen: { ...gen, model: v } })}
          style={{ minWidth: 150 }}
          popupMatchSelectWidth={false}
          placement="topLeft"
        />
        <div className="flex-1" />
        {running ? (
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2 size={14} className="animate-spin" />
            分镜生成中…
          </span>
        ) : (
          <>
            <button
              type="button"
              data-testid="script-generator-submit"
              disabled={!canSubmit}
              onClick={() => void submit()}
              className="w-8 h-8 rounded-full border-none flex items-center justify-center cursor-pointer transition-colors bg-foreground text-background disabled:opacity-30 disabled:cursor-not-allowed"
              title={
                effectiveScript === ''
                  ? '请先输入剧情描述或连接文字节点'
                  : overLimit
                    ? `剧本最长 ${MAX_SCRIPT_LEN} 字`
                    : style === ''
                      ? '暂无可用风格'
                      : !selectedModel
                        ? '暂无可用模型'
                        : '生成分镜脚本'
              }
            >
              {submitting ? <Loader2 size={14} className="animate-spin" /> : <ArrowUp size={15} />}
            </button>
          </>
        )}
      </div>
    </div>
  );
}
