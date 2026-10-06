import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

// 只验证路由：页面本身各有测试，这里换成占位。
vi.mock('@/modules/canvas/CanvasProjectsPage', () => ({ CanvasProjectsPage: () => <div>projects-page</div> }));
vi.mock('@/modules/canvas/CanvasEditorPage', () => ({ CanvasEditorPage: () => <div>editor-page</div> }));
vi.mock('@/diag/DiagPage', () => ({ DiagPage: () => <div>diag-page</div> }));
vi.mock('@/desktop/StartupRedirect', () => ({ StartupRedirect: () => <div>startup-page</div> }));
vi.mock('@/modules/settings/SettingsRoot', () => ({
  SettingsRoot: ({ onBack }: { onBack?: () => void }) => (
    <div>
      settings-root
      <button onClick={onBack}>back</button>
    </div>
  ),
}));

import { App } from './App';

describe('App 路由', () => {
  it('/settings 挂设置页；没有上一页时 ← 回启动入口', async () => {
    render(
      <MemoryRouter initialEntries={['/settings']}>
        <App />
      </MemoryRouter>,
    );
    expect(screen.getByText('settings-root')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'back' }));
    await waitFor(() => expect(screen.getByText('startup-page')).toBeInTheDocument());
  });
});
