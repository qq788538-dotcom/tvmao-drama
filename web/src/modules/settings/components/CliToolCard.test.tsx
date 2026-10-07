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

const base = {
  commandPath: '/Users/u/.miaoju/bin/miaoju',
  binDir: '/Users/u/.miaoju/bin',
  skillEntry: '/Applications/喵剧.app/Contents/Resources/cli-skill/SKILL.md',
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
    expect((await screen.findAllByText(/重新打开终端/)).length).toBeGreaterThan(0);
  });

  it('asks to reinstall when version mismatched', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: false });
    renderCard(<CliToolCard />);
    expect(await screen.findByText('版本不一致')).toBeTruthy();
    expect(screen.getByRole('button', { name: '重新安装' })).toBeTruthy();
  });

  it('shows ai prompt with skill path and codex config', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: true });
    renderCard(<CliToolCard />);
    expect(await screen.findByText(`请阅读 ${base.skillEntry}，学会使用喵剧 CLI`)).toBeTruthy();
    expect(screen.getByText(/network_access = true/)).toBeTruthy();
  });

  it('uninstalls', async () => {
    status.mockResolvedValue({ ...base, installed: true, upToDate: true });
    uninstall.mockResolvedValue({ ...base, installed: false, upToDate: false });
    renderCard(<CliToolCard />);
    fireEvent.click(await screen.findByRole('button', { name: /卸\s*载/ }));
    await waitFor(() => expect(uninstall).toHaveBeenCalled());
  });
});
