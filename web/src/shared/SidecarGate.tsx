// 所有依赖本地服务的界面都包在 SidecarGate 里：
// 启动中显示等待；失败显示原因和“重试”；就绪后把 info 交给子组件。
// 数据目录可能是失败原因时（用的是自定义目录，或目录配置读不出来），再给一个“改用默认数据目录”的退路，
// 否则外接盘丢失 / 没有写权限时用户会被永远卡在失败页（设置页也在 Gate 里面，进不去）。
// sidecar 重启后端口会变，子树以端口为 key 重新挂载，避免继续用旧地址。
import { Fragment, useEffect, useReducer, useState, type ReactNode } from 'react';
import {
  DataDirSwitchError,
  NotTauriError,
  getDataDir,
  getServerInfo,
  onSidecarStatus,
  restartSidecar,
  switchToDefaultDataDir,
  type ServerInfo,
} from './server';
import { gateReducer, initialGateState, offerDefaultDataDir, type DataDirProbe } from './gateState';

export function SidecarGate({ children }: { children: (info: ServerInfo) => ReactNode }) {
  const [state, dispatch] = useReducer(gateReducer, initialGateState);
  const [dataDir, setDataDir] = useState<DataDirProbe | null>(null);
  const [fallbackError, setFallbackError] = useState<string | null>(null);

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
      (e) => dispatch({ type: 'error', epoch, reason: e instanceof NotTauriError ? e.message : String(e) }),
    );
  }, [state.epoch, state.view.kind]);

  // 每次进入失败页都重新查一次数据目录，决定要不要显示“改用默认数据目录”
  useEffect(() => {
    if (state.view.kind !== 'failed') return;
    let stale = false;
    setDataDir(null);
    getDataDir().then(
      (info) => !stale && setDataDir({ ok: true, info }),
      () => !stale && setDataDir({ ok: false }),
    );
    return () => {
      stale = true;
    };
  }, [state.epoch, state.view.kind]);

  const switchDefault = () => {
    setFallbackError(null);
    // 配置切换失败时 sidecar 没有重启，原因显示在按钮下方；重启失败的原因由失败页本身显示，不重复
    // 不在这里切到“启动中”：配置写好之前 server_info 仍会返回旧的失败原因。重启开始时壳会发 starting 事件。
    switchToDefaultDataDir().catch((e) => {
      if (e instanceof DataDirSwitchError) setFallbackError(e.message);
    });
  };

  const retry = () => {
    setFallbackError(null);
    // 先发起重启（写入缓存），再切到“启动中”让组件等待新的 info
    void restartSidecar().catch(() => undefined);
    dispatch({ type: 'status', status: { status: 'starting' } });
  };

  if (state.view.kind === 'starting') {
    return <div className="bg-background text-foreground" style={{ padding: 24, minHeight: '100vh' }}>正在启动本地服务…</div>;
  }
  if (state.view.kind === 'failed') {
    return (
      <div className="bg-background text-foreground" style={{ padding: 24, minHeight: '100vh' }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88', userSelect: 'text' }}>{state.view.reason}</pre>
        <button onClick={retry}>重试</button>
        {dataDir && offerDefaultDataDir(dataDir) && (
          <div style={{ marginTop: 16 }}>
            <p>
              {dataDir.ok
                ? `当前使用的是自定义数据目录：${dataDir.info.current}。如果它所在的磁盘没有连接、或者没有写入权限，本地服务就无法启动。`
                : '数据目录配置文件读取失败，本地服务不知道数据放在哪里，所以无法启动。'}
              可以改用默认数据目录先用起来：原目录里的数据不会被删除或移动，但默认目录里看不到它们；
              问题解决后可以在设置里重新选回原目录。
            </p>
            <button onClick={switchDefault}>改用默认数据目录</button>
          </div>
        )}
        {fallbackError && (
          <pre style={{ whiteSpace: 'pre-wrap', color: '#f88', userSelect: 'text' }}>{fallbackError}</pre>
        )}
      </div>
    );
  }
  return <Fragment key={state.view.info.port}>{children(state.view.info)}</Fragment>;
}
