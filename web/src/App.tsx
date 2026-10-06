import { Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import { CanvasProjectsPage } from '@/modules/canvas/CanvasProjectsPage';
import { CanvasEditorPage } from '@/modules/canvas/CanvasEditorPage';
import { DiagPage } from '@/diag/DiagPage';
import { StartupRedirect } from '@/desktop/StartupRedirect';
import { SettingsRoot } from '@/modules/settings/SettingsRoot';

// 桌面端路由（spec F9）：
//   /                  启动入口，直接跳进画布（上一次 → 最近修改 → 新建）
//   /canvas            所有项目（原样复用参考仓的 CanvasProjectsPage）
//   /canvas/:projectId 画布编辑器
//   /settings          设置（P4；画布左上角「设置」进入，← 返回上一页）
//   /diag              环境诊断（P0 诊断页，隐藏入口；同样在「设置 → 环境诊断」里）
// 路径与参考仓保持一致：画布模块里有十余处直接从 location.pathname 解析 /canvas/:id。
export function App() {
  return (
    <Routes>
      <Route path="/" element={<StartupRedirect />} />
      <Route path="/canvas" element={<CanvasProjectsPage />} />
      <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
      <Route path="/settings" element={<SettingsRoute />} />
      <Route path="/diag" element={<DiagPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

/** 设置页：← 回到进入前的页面；直接打开 /settings（没有上一页）时回启动入口。 */
function SettingsRoute() {
  const navigate = useNavigate();
  const onBack = () => {
    const idx = (window.history.state as { idx?: number } | null)?.idx ?? 0;
    if (idx > 0) navigate(-1);
    else navigate('/', { replace: true });
  };
  return <SettingsRoot onBack={onBack} />;
}
