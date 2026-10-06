import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Root } from 'react-dom/client';
import { getSidecarSession, resetSidecarSessionForTest } from '@/shared/sidecarSession';

// App 本身（画布页、项目列表）不在本文件的测试范围，用占位替代。
vi.mock('@/App', () => ({ App: () => <div>app-routes</div> }));

import { startApp } from './startApp';

describe('startApp', () => {
  let root: Root | null = null;
  afterEach(() => {
    act(() => root?.unmount());
    root = null;
    document.body.innerHTML = '';
    resetSidecarSessionForTest();
  });

  function container() {
    const el = document.createElement('div');
    document.body.appendChild(el);
    return el;
  }

  it('拿到端口和 token 后写入会话，再渲染应用路由', async () => {
    let resolve!: (v: { port: number; token: string }) => void;
    const pending = new Promise<{ port: number; token: string }>((r) => { resolve = r; });

    let started!: Promise<Root>;
    act(() => {
      started = startApp(container(), () => pending);
    });
    // 会话建立之前只显示「正在连接」，不渲染任何页面（axios 此时还不知道地址）。
    expect(await screen.findByText('正在连接本地服务…')).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();

    await act(async () => {
      resolve({ port: 50001, token: 't' });
      root = await started;
    });
    await waitFor(() => expect(screen.getByText('app-routes')).toBeInTheDocument());
    expect(getSidecarSession()).toEqual({ port: 50001, token: 't' });
  });

  it('sidecar 启动失败：显示原因，不渲染应用、不写会话', async () => {
    await act(async () => {
      root = await startApp(container(), () => Promise.reject(new Error('sidecar exited: code 1')));
    });
    expect(screen.getByText('本地服务未启动')).toBeInTheDocument();
    expect(screen.getByText(/sidecar exited: code 1/)).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();
    expect(getSidecarSession()).toBeNull();
  });
});
