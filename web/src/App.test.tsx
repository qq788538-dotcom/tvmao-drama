import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// 只验证路由：页面本身各有测试，这里换成占位。
vi.mock('@/modules/canvas/CanvasProjectsPage', () => ({ CanvasProjectsPage: () => <div>projects-page</div> }));
vi.mock('@/modules/canvas/CanvasEditorPage', () => ({ CanvasEditorPage: () => <div>editor-page</div> }));
vi.mock('@/diag/DiagPage', () => ({ DiagPage: () => <div>diag-page</div> }));
vi.mock('@/desktop/StartupRedirect', () => ({ StartupRedirect: () => <div>startup-page</div> }));
vi.mock('@/modules/canvas/ProjectsModal', () => ({ ProjectsModal: () => null }));
vi.mock('@/modules/settings/SettingsModal', () => ({ SettingsModal: () => <div>settings-modal</div> }));

import { App } from './App';
import { useSettingsModal } from '@/modules/settings/settingsModalStore';

describe('App 路由', () => {
  beforeEach(() => useSettingsModal.setState({ open: false, tab: 'general' }));

  it('/settings 兼容路由：打开设置弹框并回启动入口，不 404', async () => {
    render(
      <MemoryRouter initialEntries={['/settings']}>
        <App />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText('startup-page')).toBeInTheDocument());
    expect(useSettingsModal.getState().open).toBe(true);
    expect(screen.getByText('settings-modal')).toBeInTheDocument();
  });
});
