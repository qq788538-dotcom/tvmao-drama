import { describe, expect, it } from 'vitest';
import { candidateOptions, roleLabel } from './agentRoles';

describe('agentRoles', () => {
  it('已知角色给中文名，未知角色原样返回', () => {
    expect(roleLabel('planner')).toBe('主 Agent');
    expect(roleLabel('utility')).toBe('上下文压缩');
    expect(roleLabel('subagent_audio')).toBe('音频子 Agent');
    expect(roleLabel('subagent_video')).toBe('视频子 Agent');
    expect(roleLabel('x')).toBe('x');
  });
  it('当前模型不在候选里时显示为不可用项', () => {
    const view = { data: [], candidates: [{ modelId: 'a', name: 'A' }] };
    expect(candidateOptions(view, 'a')).toEqual([{ value: 'a', label: 'A（a）' }]);
    expect(candidateOptions(view, 'gone')[1]).toEqual({ value: 'gone', label: 'gone（已停用或不存在）', disabled: true });
  });
});
