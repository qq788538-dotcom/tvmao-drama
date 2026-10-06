import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { App } from '@/App';
import { queryClient } from '@/shared/queryClient';
import { getServerInfo, type ServerInfo } from '@/shared/server';
import { setSidecarSession } from '@/shared/sidecarSession';
import { darkTheme } from './theme';

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <ConfigProvider locale={zhCN} theme={darkTheme}>
          <AntApp>{children}</AntApp>
        </ConfigProvider>
      </QueryClientProvider>
    </React.StrictMode>
  );
}

export function SidecarConnecting() {
  return <div style={{ padding: 24, color: '#cfcece' }}>正在连接本地服务…</div>;
}

// sidecar 没起来：显示原因，而不是白屏（壳不会自动重启 sidecar，P8 才做重启）。
export function SidecarFailed({ error }: { error: string }) {
  return (
    <div style={{ padding: 24, color: '#f5f5f5' }}>
      <h2>本地服务未启动</h2>
      <pre style={{ whiteSpace: 'pre-wrap', color: '#f88' }}>{error}</pre>
      <p>请退出并重新打开应用。</p>
    </div>
  );
}

/**
 * 启动流程：先拿到本次 sidecar 的端口和 token 并写进会话，之后才渲染任何页面——
 * axios 拦截器和 AgentClient 都是同步读会话的。
 */
export async function startApp(
  container: HTMLElement,
  loadServerInfo: () => Promise<ServerInfo> = getServerInfo,
): Promise<ReactDOM.Root> {
  const root = ReactDOM.createRoot(container);
  root.render(<Shell><SidecarConnecting /></Shell>);
  let info: ServerInfo;
  try {
    info = await loadServerInfo();
  } catch (err) {
    root.render(<Shell><SidecarFailed error={String(err)} /></Shell>);
    return root;
  }
  setSidecarSession(info);
  root.render(
    <Shell>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </Shell>,
  );
  return root;
}
