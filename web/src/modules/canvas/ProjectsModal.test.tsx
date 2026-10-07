// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor, fireEvent } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { MemoryRouter, useLocation } from 'react-router-dom';

const flush = vi.hoisted(() => vi.fn(async () => undefined));
const resolveStartup = vi.hoisted(() => vi.fn(async () => 99));
vi.mock('@/modules/canvas/editor/persistence/canvasMutations', () => ({ flushCanvasMutations: flush }));
vi.mock('@/desktop/resolveStartupProject', () => ({ resolveStartupProject: resolveStartup }));
vi.mock('@/modules/canvas/CanvasProjectsPage', () => ({
  CanvasProjectsPage: (p: { onOpenProject: (id: number) => void; onProjectDeleted: (id: number) => void; embedded?: boolean }) => (
    <div>
      projects-list{p.embedded ? '-embedded' : ''}
      <button onClick={() => p.onOpenProject(5)}>open-current</button>
      <button onClick={() => p.onOpenProject(6)}>open-other</button>
      <button onClick={() => p.onProjectDeleted(5)}>delete-current</button>
      <button onClick={() => p.onProjectDeleted(6)}>delete-other</button>
    </div>
  ),
}));

import { ProjectsModal } from './ProjectsModal';
import { openProjects, useProjectsModal } from './projectsModalStore';
import { saveLastProjectId, loadLastProjectId } from '@/desktop/lastProject';

function Probe() {
  return <div data-testid="path">{useLocation().pathname}</div>;
}
function setup() {
  return render(
    <AntApp>
      <MemoryRouter initialEntries={['/canvas/5']}>
        <Probe />
        <ProjectsModal />
      </MemoryRouter>
    </AntApp>,
  );
}
const path = () => screen.getByTestId('path').textContent;

describe('ProjectsModal', () => {
  beforeEach(() => {
    useProjectsModal.setState({ open: false });
    flush.mockClear();
    resolveStartup.mockClear();
    localStorage.clear();
  });
  afterEach(() => localStorage.clear());

  it('openProjects 打开弹框，URL 不变', () => {
    setup();
    expect(screen.queryByText(/projects-list/)).toBeNull();
    act(() => openProjects());
    expect(screen.getByText('projects-list-embedded')).toBeTruthy();
    expect(path()).toBe('/canvas/5');
  });

  it('点当前项目：只关弹框，不跳转不 flush', async () => {
    setup();
    act(() => openProjects());
    fireEvent.click(screen.getByText('open-current'));
    await waitFor(() => expect(useProjectsModal.getState().open).toBe(false));
    expect(path()).toBe('/canvas/5');
    expect(flush).not.toHaveBeenCalled();
  });

  it('点别的项目：先 flush，再跳转并关弹框', async () => {
    setup();
    act(() => openProjects());
    fireEvent.click(screen.getByText('open-other'));
    await waitFor(() => expect(path()).toBe('/canvas/6'));
    expect(flush).toHaveBeenCalledTimes(1);
    expect(useProjectsModal.getState().open).toBe(false);
  });

  it('删除当前项目：按启动规则回落到下一个项目并关弹框', async () => {
    saveLastProjectId(5);
    setup();
    act(() => openProjects());
    fireEvent.click(screen.getByText('delete-current'));
    await waitFor(() => expect(path()).toBe('/canvas/99'));
    expect(resolveStartup).toHaveBeenCalledTimes(1);
    expect(loadLastProjectId()).toBeNull();
    expect(useProjectsModal.getState().open).toBe(false);
  });

  it('删除别的项目：留在当前画布，弹框不关', async () => {
    setup();
    act(() => openProjects());
    fireEvent.click(screen.getByText('delete-other'));
    expect(resolveStartup).not.toHaveBeenCalled();
    expect(path()).toBe('/canvas/5');
    expect(useProjectsModal.getState().open).toBe(true);
  });
});
