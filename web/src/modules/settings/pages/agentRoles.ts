import type { AgentModelsView } from '../api';

const ROLE_LABELS: Record<string, string> = {
  planner: '主 Agent',
  utility: '上下文压缩',
  subagent_audio: '音频子 Agent',
  subagent_video: '视频子 Agent',
};

export function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role;
}

export function candidateOptions(view: AgentModelsView, current: string) {
  const opts: { value: string; label: string; disabled?: boolean }[] = view.candidates.map((c) => ({ value: c.modelId, label: `${c.name}（${c.modelId}）` }));
  if (current && !opts.some((o) => o.value === current)) opts.push({ value: current, label: `${current}（已停用或不存在）`, disabled: true });
  return opts;
}
