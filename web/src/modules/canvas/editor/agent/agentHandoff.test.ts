import { describe, it, expect, beforeEach } from 'vitest';
import { useAgentHandoff, handoffContent, shouldConsumeHandoff } from './agentHandoff';
import type { AgentSkillDTO } from '@/shared/api';

const skill = { id: 'sk_1', name: '短片工作室' } as AgentSkillDTO;

describe('agentHandoff', () => {
  beforeEach(() => useAgentHandoff.setState({ pending: null }));

  it('take 取一次即清，第二次拿不到', () => {
    useAgentHandoff.getState().set({ projectId: 7, text: 'hi', skill: null, mentions: [] });
    expect(useAgentHandoff.getState().take(7)?.text).toBe('hi');
    expect(useAgentHandoff.getState().take(7)).toBeNull();
  });

  it('projectId 对不上既不返回也不清空', () => {
    useAgentHandoff.getState().set({ projectId: 7, text: 'hi', skill: null, mentions: [] });
    expect(useAgentHandoff.getState().take(8)).toBeNull();
    expect(useAgentHandoff.getState().peek(7)).not.toBeNull();
  });

  it('peek 不消费', () => {
    useAgentHandoff.getState().set({ projectId: 7, text: 'hi', skill: null, mentions: [] });
    expect(useAgentHandoff.getState().peek(7)?.text).toBe('hi');
    expect(useAgentHandoff.getState().peek(7)?.text).toBe('hi');
  });

  it('handoffContent：有字用字，只有 skill 时生成「请使用技能」', () => {
    expect(handoffContent({ text: ' 写个故事 ', skill })).toBe('写个故事');
    expect(handoffContent({ text: '  ', skill })).toBe('请使用「短片工作室」技能开始创作');
    expect(handoffContent({ text: '', skill: null })).toBe('');
  });

  it('shouldConsumeHandoff：WS 未 open 或 threads 未为本项目拉回都不消费', () => {
    expect(shouldConsumeHandoff('open', 7, 7)).toBe(true);
    expect(shouldConsumeHandoff('connecting', 7, 7)).toBe(false);
    expect(shouldConsumeHandoff('open', null, 7)).toBe(false);
    // 切项目后 threadsLoadedFor 还是上个项目的值——不能拿上个项目的就绪态发本项目的单
    expect(shouldConsumeHandoff('open', 6, 7)).toBe(false);
  });
});
