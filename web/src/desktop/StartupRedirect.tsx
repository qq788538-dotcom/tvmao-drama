import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, Result, Spin } from 'antd';
import { resolveStartupProject } from './resolveStartupProject';

// 根路由 `/`：不渲染任何首页，算出要打开的项目后直接跳进画布（spec F9）。
export function StartupRedirect() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  const retry = useCallback(() => {
    setError(null);
    setAttempt((n) => n + 1);
  }, []);

  useEffect(() => {
    let cancelled = false;
    resolveStartupProject()
      .then((id) => {
        if (!cancelled) navigate(`/canvas/${id}`, { replace: true });
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [navigate, attempt]);

  if (error) {
    return (
      <Result
        status="error"
        title="无法打开项目"
        subTitle={error}
        extra={[
          <Button key="retry" type="primary" onClick={retry}>
            重试
          </Button>,
          <Link key="diag" to="/diag">
            环境诊断
          </Link>,
        ]}
      />
    );
  }
  return (
    <div className="flex h-screen items-center justify-center">
      <Spin tip="正在打开项目…">
        <div className="w-24 h-24" />
      </Spin>
    </div>
  );
}
