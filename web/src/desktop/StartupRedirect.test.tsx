import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const resolveStartupProject = vi.fn();
vi.mock('./resolveStartupProject', () => ({
  resolveStartupProject: () => resolveStartupProject(),
}));

import { StartupRedirect } from './StartupRedirect';

function renderAt() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <Routes>
        <Route path="/" element={<StartupRedirect />} />
        <Route path="/canvas/:projectId" element={<div>canvas-page</div>} />
        <Route path="/diag" element={<div>diag-page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('StartupRedirect', () => {
  beforeEach(() => resolveStartupProject.mockReset());

  it('算出项目后直接进画布', async () => {
    resolveStartupProject.mockResolvedValue(8);
    renderAt();
    expect(await screen.findByText('canvas-page')).toBeInTheDocument();
  });

  it('失败时显示原因，可重试，也能进环境诊断', async () => {
    resolveStartupProject.mockRejectedValueOnce(new Error('Request failed with status code 404'));
    resolveStartupProject.mockResolvedValueOnce(3);
    renderAt();

    expect(await screen.findByText('无法打开项目')).toBeInTheDocument();
    expect(screen.getByText('Request failed with status code 404')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '环境诊断' })).toHaveAttribute('href', '/diag');

    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    await waitFor(() => expect(screen.getByText('canvas-page')).toBeInTheDocument());
    expect(resolveStartupProject).toHaveBeenCalledTimes(2);
  });
});
