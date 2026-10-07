// @vitest-environment jsdom
import '../testing/domSetup';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const status = vi.fn();
const install = vi.fn();
const uninstall = vi.fn();
vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  cliToolStatus: () => status(),
  installCliTool: () => install(),
  uninstallCliTool: () => uninstall(),
}));

import { CliToolCard } from './CliToolCard';

function renderCard(ui: ReactElement) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AntApp>{ui}</AntApp>
    </QueryClientProvider>,
  );
}

const PROMPT = '请帮我安装喵剧命令行工具 miaoju，并学会使用它。依次执行：\n1. mkdir -p ~/.miaoju/bin && ln -sf "/Applications/喵剧.app/Contents/MacOS/miaoju-cli" ~/.miaoju/bin/miaoju';

const base = {
  commandPath: '/Users/u/.miaoju/bin/miaoju',
  binDir: '/Users/u/.miaoju/bin',
  skillEntry: '/Applications/喵剧.app/Contents/Resources/cli-skill/SKILL.md',
  skills: [
    { agent: 'claude', path: '/Users/u/.claude/skills/miaoju-cli', available: true, installed: true, conflict: false, outdated: false, error: null },
    {
      agent: 'codex',
      path: '/Users/u/.codex/skills/miaoju-cli',
      available: false,
      installed: false,
      conflict: false,
      outdated: false,
      error: null,
    },
  ],
  agentPrompt: PROMPT,
};

describe('CliToolCard', () => {
  beforeEach(() => {
    status.mockReset();
    install.mockReset();
    uninstall.mockReset();
  });

  it('installs when not installed and shows reopen-terminal hint', async () => {
    status.mockResolvedValue({ ...base, installed: false, upToDate: false });
    install.mockResolvedValue({ ...base, installed: true, upToDate: true });
    renderCard(<CliToolCard />);
    await screen.findByText('未安装'); // 状态加载前按钮是禁用的
    fireEvent.click(screen.getByRole('button', { name: /安\s*装/ }));
    await waitFor(() => expect(install).toHaveBeenCalled());
    expect(
      (await screen.findAllByText('重新打开终端后输入 miaoju --help 验证；在 Claude Code、Codex 里新开会话即可使用。')).length,
    ).toBeGreaterThan(0);
  });

  it('asks to reinstall when version mismatched', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: false });
    renderCard(<CliToolCard />);
    expect(await screen.findByText('版本不一致')).toBeTruthy();
    expect(screen.getByRole('button', { name: '重新安装' })).toBeTruthy();
  });

  it('shows guide, skill status per agent and codex config on the default tab', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: true });
    renderCard(<CliToolCard />);
    expect(await screen.findByText(/Claude Code：已安装/)).toBeTruthy();
    expect(screen.getByText(/Codex：未检测到 Codex（~\/\.codex 不存在）/)).toBeTruthy();
    expect(screen.getByRole('tab', { name: '一键安装', selected: true })).toBeTruthy();
    expect(screen.getAllByText(/喵剧必须保持打开/).length).toBeGreaterThan(0);
    expect(screen.getByText(/network_access = true/)).toBeTruthy();
    expect(screen.queryByText(/请阅读 .*SKILL\.md/)).toBeNull();
  });

  it('reports a conflicting user-owned skill dir', async () => {
    status.mockResolvedValue({
      ...base,
      installed: true,
      upToDate: true,
      skills: [
        { ...base.skills[0], installed: false, conflict: true },
        { ...base.skills[1], available: true, installed: true },
      ],
    });
    renderCard(<CliToolCard />);
    expect(
      await screen.findByText(`Claude Code：已存在同名目录，未覆盖：${base.skills[0].path}`),
    ).toBeTruthy();
    expect(screen.getByText(/Codex：已安装/)).toBeTruthy();
  });

  it('shows outdated and per-agent errors', async () => {
    status.mockResolvedValue({
      ...base,
      installed: true,
      upToDate: true,
      skills: [
        { ...base.skills[0], outdated: true },
        { ...base.skills[1], available: true, error: '创建 /Users/u/.codex/skills 失败：权限不足' },
      ],
    });
    renderCard(<CliToolCard />);
    expect(await screen.findByText('Claude Code：版本不一致，需要重新安装')).toBeTruthy();
    expect(screen.getByText('Codex：安装失败：创建 /Users/u/.codex/skills 失败：权限不足')).toBeTruthy();
  });

  it('refreshes status when install fails', async () => {
    status.mockResolvedValue({ ...base, installed: false, upToDate: false });
    install.mockRejectedValue(new Error('boom'));
    renderCard(<CliToolCard />);
    await screen.findByText('未安装');
    expect(status).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: /安\s*装/ }));
    await waitFor(() => expect(status).toHaveBeenCalledTimes(2));
  });

  it('shows the agent prompt on the "let AI install" tab', async () => {
    status.mockResolvedValue({ ...base, installed: false, upToDate: false });
    renderCard(<CliToolCard />);
    fireEvent.click(await screen.findByRole('tab', { name: '让 AI 助手安装' }));
    expect(await screen.findByText(/把下面这段话发给 Claude Code 或 Codex/)).toBeTruthy();
    expect(
      screen.getByText((_, el) => el?.tagName === 'PRE' && el.textContent === PROMPT),
    ).toBeTruthy();
  });

  it('uninstalls', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: true });
    uninstall.mockResolvedValue({ ...base, installed: false, upToDate: false });
    renderCard(<CliToolCard />);
    fireEvent.click(await screen.findByRole('button', { name: /卸\s*载/ }));
    await waitFor(() => expect(uninstall).toHaveBeenCalled());
  });
});
