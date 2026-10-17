// 画布页的错误分支与失权提示。与 CanvasEditorPage.test.tsx 分开放：
// 这里用真实的 canvasMutations，好触发 owner 失权事件。
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

const messageError = vi.fn();
const getProject = vi.fn();
const getSnapshot = vi.fn();

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  const App = Object.assign(({ children }: { children: ReactNode }) => <>{children}</>, {
    useApp: () => ({
      message: { error: messageError, success: vi.fn(), warning: vi.fn(), info: vi.fn() },
      modal: {},
      notification: {},
    }),
  });
  return { ...actual, App };
});
vi.mock('@/modules/canvas/editor/CanvasEditor', () => ({ CanvasEditor: () => <div data-testid="canvas-editor" /> }));
vi.mock('@/modules/canvas/editor/Sidebar', () => ({ Sidebar: () => null }));
vi.mock('@/modules/canvas/editor/Lightbox', () => ({ Lightbox: () => null }));
vi.mock('@/modules/canvas/editor/Minimap', () => ({ Minimap: () => null }));
vi.mock('@/modules/canvas/editor/ShortcutsHint', () => ({ ShortcutsHint: () => null }));
vi.mock('@/modules/canvas/panels/HistoryPanel', () => ({ HistoryPanel: () => null }));
vi.mock('@/modules/canvas/panels/LibraryPanel', () => ({ LibraryPanel: () => null }));
vi.mock('@/modules/canvas/storyboard/StoryboardView', () => ({ StoryboardView: () => null }));
vi.mock('@/modules/canvas/editor/agent/AgentPanel', () => ({
  AgentPanel: () => null,
  loadAgentPanelWidth: () => 400,
  saveAgentPanelWidth: () => undefined,
}));
vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    getProject: (id: number) => getProject(id),
    getSnapshot: (id: number) => getSnapshot(id),
    listRuntimeModels: vi.fn(async () => []),
  };
});

import { CanvasEditorPage } from './CanvasEditorPage';
import { emitOwnerLostForTest } from '@/modules/canvas/editor/persistence/canvasMutations';
import { loadLastProjectId } from '@/desktop/lastProject';

function httpError(status: number): AxiosError {
  const config = { headers: new AxiosHeaders() } as InternalAxiosRequestConfig;
  return new AxiosError(`Request failed with status code ${status}`, String(status), config, undefined, {
    status,
    statusText: '',
    headers: {},
    config,
    data: {},
  });
}

function renderPage(path = '/canvas/14') {
  const qc = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
          <Route path="/diag" element={<div>diag-page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const okSnapshot = { payload: { nodes: [], connections: [] }, version: 1, syncedAt: null };

describe('CanvasEditorPage 错误分支', () => {
  beforeEach(() => {
    localStorage.clear();
    messageError.mockReset();
    getProject.mockReset().mockResolvedValue({ id: 14, name: 'P', description: '' });
    getSnapshot.mockReset();
  });

  it('快照 401：不卡在 Loading，显示会话失效提示，并给出「重试」和「环境诊断」', async () => {
    getSnapshot.mockRejectedValue(httpError(401));
    renderPage();

    expect(await screen.findByText(/本地服务会话已失效/)).toBeInTheDocument();
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /重\s*试/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '环境诊断' })).toBeInTheDocument();
    // 401 重试也不会好（token 对不上），不浪费一次自动重试。
    expect(getSnapshot).toHaveBeenCalledTimes(1);
  });

  it('点「重试」重新拉快照，成功后进入画布', async () => {
    getSnapshot.mockRejectedValueOnce(httpError(401)).mockResolvedValue(okSnapshot);
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: /重\s*试/ }));
    expect(await screen.findByTestId('canvas-editor')).toBeInTheDocument();
  });

  it('点「环境诊断」进入 /diag', async () => {
    getSnapshot.mockRejectedValue(httpError(500));
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: '环境诊断' }));
    expect(await screen.findByText('diag-page')).toBeInTheDocument();
  });

  it('其它错误（500）也给出重试和诊断，而不是只有一行字', async () => {
    getSnapshot.mockRejectedValue(httpError(500));
    renderPage();
    expect(await screen.findByText(/加载画布失败/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /重\s*试/ })).toBeInTheDocument();
  });

  it('owner 失权事件：弹「保存失败」提示，不再被静默丢弃', async () => {
    getSnapshot.mockResolvedValue(okSnapshot);
    renderPage();
    await screen.findByTestId('canvas-editor');
    act(() => emitOwnerLostForTest(new Error('CANVAS_OWNER_LOST')));
    await waitFor(() => expect(messageError).toHaveBeenCalledWith(expect.stringContaining('保存失败')));
  });

  it('快照载入后才记为上一次的项目', async () => {
    getSnapshot.mockResolvedValue(okSnapshot);
    renderPage();
    await screen.findByTestId('canvas-editor');
    expect(loadLastProjectId()).toBe(14);
  });
});
