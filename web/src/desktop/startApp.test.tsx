import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useEffect } from 'react';
import type { Root } from 'react-dom/client';
import type { ServerInfo, SidecarStatus } from '@/shared/server';
import {
  currentSidecarSession,
  getSidecarSession,
  resetSidecarSessionForTest,
} from '@/shared/sidecarSession';
import { queryClient } from '@/shared/queryClient';
import { useAgentStore } from '@/modules/canvas/editor/agent/agentStore';

// App 本身（画布页、项目列表）不在本文件的测试范围，用占位替代；记录挂载次数，
// 用来确认 sidecar 换会话后整棵页面树重新挂载（旧页面里的地址、token、媒体 URL 全部作废）。
const appMounts = vi.fn();
vi.mock('@/App', () => ({
  App: () => {
    useEffect(() => appMounts(), []);
    return <div>app-routes</div>;
  },
}));

// 壳的 IPC：getServerInfo 和状态事件由测试控制。
const getServerInfo = vi.fn<() => Promise<ServerInfo>>();
let statusCb: ((s: SidecarStatus) => void) | null = null;
const getSidecarStatus = vi.fn<() => Promise<SidecarStatus>>(async () => ({ status: 'starting' }));
vi.mock('@/shared/server', async () => {
  const actual = await vi.importActual<typeof import('@/shared/server')>('@/shared/server');
  return {
    ...actual,
    getServerInfo: () => getServerInfo(),
    onSidecarStatus: (cb: (s: SidecarStatus) => void) => {
      statusCb = cb;
      // StrictMode 下 Gate 会订阅两次：只撤掉自己那一次。
      return Promise.resolve(() => { if (statusCb === cb) statusCb = null; });
    },
    restartSidecar: vi.fn(() => new Promise(() => undefined)),
    getDataDir: vi.fn(async () => ({ current: '/d', default: '/d', custom: false })),
    getSidecarStatus: () => getSidecarStatus(),
  };
});

import { startApp } from './startApp';

describe('startApp', () => {
  let root: Root | null = null;
  beforeEach(() => {
    getServerInfo.mockReset();
    getSidecarStatus.mockReset();
    getSidecarStatus.mockResolvedValue({ status: 'starting' });
    appMounts.mockReset();
    statusCb = null;
    queryClient.clear();
  });
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
    let resolve!: (v: ServerInfo) => void;
    getServerInfo.mockReturnValue(new Promise<ServerInfo>((r) => { resolve = r; }));

    act(() => {
      root = startApp(container());
    });
    // 会话建立之前只显示「正在启动」，不渲染任何页面（axios 此时还不知道地址）。
    expect(await screen.findByText('正在启动本地服务…')).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();

    await act(async () => resolve({ port: 50001, token: 't' }));
    await waitFor(() => expect(screen.getByText('app-routes')).toBeInTheDocument());
    expect(getSidecarSession()).toEqual({ port: 50001, token: 't' });
    // REST 拦截器的会话来源已接上 getServerInfo（每个请求现取）。
    expect(await currentSidecarSession()).toEqual({ port: 50001, token: 't' });
  });

  it('启动前正在迁移数据目录：挂载时主动查询到 migrating，显示“请勿关闭”，迁移完照常进入应用', async () => {
    let resolve!: (v: ServerInfo) => void;
    getServerInfo.mockReturnValue(new Promise<ServerInfo>((r) => { resolve = r; }));
    getSidecarStatus.mockResolvedValue({ status: 'migrating' }); // StrictMode 下 Gate 挂两次
    act(() => {
      root = startApp(container());
    });
    expect(await screen.findByText(/正在迁移数据目录，请勿关闭应用/)).toBeInTheDocument();
    await act(async () => resolve({ port: 50003, token: 'm' }));
    await waitFor(() => expect(screen.getByText('app-routes')).toBeInTheDocument());
  });

  it('sidecar 启动失败：显示原因和重试，不渲染应用、不写会话', async () => {
    getServerInfo.mockRejectedValue('sidecar exited: code 1');
    act(() => {
      root = startApp(container());
    });
    expect(await screen.findByText('本地服务未启动')).toBeInTheDocument();
    expect(screen.getByText(/sidecar exited: code 1/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();
    expect(getSidecarSession()).toBeNull();
  });

  it('sidecar 自动重启（端口、token 都变）：换新会话、清掉 query 缓存、页面重新挂载', async () => {
    getServerInfo.mockResolvedValue({ port: 50001, token: 'old' });
    act(() => {
      root = startApp(container());
    });
    await screen.findByText('app-routes');
    const mountsBefore = appMounts.mock.calls.length; // StrictMode 下开发模式会挂两次，只比相对值
    // 旧会话下拉到的快照：里面的媒体 URL 带着旧 token，重启后会 401。
    queryClient.setQueryData(['snapshot', 14], { payload: { nodes: [{ src: 'http://127.0.0.1:50001/media/a?token=old' }] } });
    // 旧会话下翻页加载过的 Agent 消息（原图链接同样带旧 token）。
    useAgentStore.setState({
      messages: { t1: [{ id: 'm1', thread_id: 't1', role: 'assistant', content: 'x', createdAt: '2026-01-01T00:00:00Z' } as never] },
      pageState: { t1: { hasMore: true, loadingOlder: false } },
    });

    await waitFor(() => expect(statusCb).not.toBeNull());
    // 壳的 server_info 在 sidecar 启动中会一直等到就绪。
    let resolveNew!: (v: ServerInfo) => void;
    getServerInfo.mockReturnValue(new Promise<ServerInfo>((r) => { resolveNew = r; }));
    act(() => statusCb!({ status: 'starting' }));
    expect(await screen.findByText('正在启动本地服务…')).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();

    getServerInfo.mockResolvedValue({ port: 50002, token: 'new' });
    await act(async () => {
      resolveNew({ port: 50002, token: 'new' });
      statusCb!({ status: 'ready' });
    });
    await screen.findByText('app-routes');

    expect(getSidecarSession()).toEqual({ port: 50002, token: 'new' });
    expect(queryClient.getQueryData(['snapshot', 14])).toBeUndefined();
    expect(useAgentStore.getState().messages).toEqual({});
    expect(useAgentStore.getState().pageState).toEqual({});
    expect(appMounts.mock.calls.length).toBeGreaterThan(mountsBefore);
  });

  it('端口没变、只换了 token：同样换会话并重新挂载', async () => {
    getServerInfo.mockResolvedValue({ port: 50001, token: 'old' });
    act(() => {
      root = startApp(container());
    });
    await screen.findByText('app-routes');
    await waitFor(() => expect(statusCb).not.toBeNull());
    const mountsBefore = appMounts.mock.calls.length;

    getServerInfo.mockResolvedValue({ port: 50001, token: 'new' });
    await act(async () => statusCb!({ status: 'ready' }));
    await waitFor(() => expect(getSidecarSession()).toEqual({ port: 50001, token: 'new' }));
    await waitFor(() => expect(appMounts.mock.calls.length).toBeGreaterThan(mountsBefore));
  });
});
