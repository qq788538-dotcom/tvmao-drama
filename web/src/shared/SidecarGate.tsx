// 所有依赖本地服务的界面都包在 SidecarGate 里：
// 启动中显示等待；失败显示原因和“重试”；就绪后把 info 交给子组件。
// sidecar 重启后端口会变，子树以端口为 key 重新挂载，避免继续用旧地址。
import { Fragment, useEffect, useReducer, type ReactNode } from 'react';
import { getServerInfo, onSidecarStatus, restartSidecar, type ServerInfo } from './server';
import { gateReducer, initialGateState } from './gateState';

export function SidecarGate({ children }: { children: (info: ServerInfo) => ReactNode }) {
  const [state, dispatch] = useReducer(gateReducer, initialGateState);

  useEffect(() => {
    let unlisten: (() => void) | undefined;
    let disposed = false;
    void onSidecarStatus((status) => dispatch({ type: 'status', status })).then((u) => {
      if (disposed) u();
      else unlisten = u;
    });
    return () => {
      disposed = true;
      unlisten?.();
    };
  }, []);

  useEffect(() => {
    if (state.view.kind !== 'starting') return;
    const epoch = state.epoch;
    getServerInfo().then(
      (info) => dispatch({ type: 'info', epoch, info }),
      (e) => dispatch({ type: 'error', epoch, reason: String(e) }),
    );
  }, [state.epoch, state.view.kind]);

  const retry = () => {
    // 先发起重启（写入缓存），再切到“启动中”让组件等待新的 info
    void restartSidecar().catch(() => undefined);
    dispatch({ type: 'status', status: { status: 'starting' } });
  };

  if (state.view.kind === 'starting') {
    return <div style={{ padding: 24 }}>正在启动本地服务…</div>;
  }
  if (state.view.kind === 'failed') {
    return (
      <div style={{ padding: 24 }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88', userSelect: 'text' }}>{state.view.reason}</pre>
        <button onClick={retry}>重试</button>
      </div>
    );
  }
  return <Fragment key={state.view.info.port}>{children(state.view.info)}</Fragment>;
}
