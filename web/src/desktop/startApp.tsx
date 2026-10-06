import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { App } from '@/App';
import { queryClient } from '@/shared/queryClient';
import { getServerInfo, type ServerInfo } from '@/shared/server';
import { SidecarGate } from '@/shared/SidecarGate';
import { getSidecarSession, setSidecarSession, setSidecarSessionLoader } from '@/shared/sidecarSession';
import { useAgentStore } from '@/modules/canvas/editor/agent/agentStore';
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

/**
 * 采用 SidecarGate 交来的会话：写进同步会话（AgentClient 构造、页面里的 sidecarToken()
 * 读它），接上「每个请求现取」的来源，并在会话变了（sidecar 重启，端口 / token 换了）时
 * 清掉 react-query 缓存——旧快照里的媒体 URL 带着旧 token，留着会整批 401。
 * 返回会话代号：会话变了就 +1，作为页面树的 key，让整棵树重新挂载、重新拉数据。
 * 在渲染期间调用，所以必须幂等（StrictMode 会渲染两次）：同一份会话重复调用不产生副作用。
 */
let sessionEpoch = 0;
export function adoptSidecarSession(info: ServerInfo): number {
  const prev = getSidecarSession();
  if (!prev || prev.port !== info.port || prev.token !== info.token) {
    if (prev) {
      queryClient.clear();
      // Agent 对话的消息缓存是模块级 zustand store，不随页面树重挂而清空；里面的原图链接由
      // 旧会话签发，留着的话 Lightbox 看原图会失败（final-review M-6）。清掉后重挂时重新拉。
      useAgentStore.setState({ messages: {}, pageState: {} });
    }
    setSidecarSession(info);
    sessionEpoch += 1;
  }
  setSidecarSessionLoader(getServerInfo);
  return sessionEpoch;
}

function SessionScope({ info }: { info: ServerInfo }) {
  const epoch = adoptSidecarSession(info);
  return (
    <BrowserRouter key={epoch}>
      <App />
    </BrowserRouter>
  );
}

/**
 * 启动流程：SidecarGate 拿到本次 sidecar 的端口和 token 之后才渲染任何页面——
 * axios 拦截器和 AgentClient 都要用到会话。P8 的壳会在 sidecar 崩溃后自动重启它并广播
 * 状态事件：Gate 先切回「正在启动」（卸掉整棵页面树），就绪后用新会话重新挂载。
 * 地址栏里的路由不变，所以用户会回到原来的画布，快照和媒体 URL 用新 token 重新拉。
 */
export function startApp(container: HTMLElement): ReactDOM.Root {
  const root = ReactDOM.createRoot(container);
  root.render(
    <Shell>
      <SidecarGate>{(info) => <SessionScope info={info} />}</SidecarGate>
    </Shell>,
  );
  return root;
}
