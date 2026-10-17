import { useEffect } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';
import { CanvasProjectsPage } from '@/modules/canvas/CanvasProjectsPage';
import { CanvasEditorPage } from '@/modules/canvas/CanvasEditorPage';
import { DiagPage } from '@/diag/DiagPage';
import { StartupRedirect } from '@/desktop/StartupRedirect';
import { ProjectsModal } from '@/modules/canvas/ProjectsModal';
import { SettingsModal } from '@/modules/settings/SettingsModal';
import { openSettings } from '@/modules/settings/settingsModalStore';

// 桌面端路由：
//   /                  启动入口，直接跳进画布（上一次 → 最近修改 → 新建）
//   /canvas            所有项目（CanvasProjectsPage）
//   /canvas/:projectId 画布编辑器
//   /settings          兼容路由：打开设置弹框并回启动入口（设置本身是弹框，不是页面）
//   /diag              环境诊断（隐藏入口；同样在「设置 → 环境诊断」里）
// 路径不能改：画布模块里有十余处直接从 location.pathname 解析 /canvas/:id。
export function App() {
  return (
    <>
      <Routes>
        <Route path="/" element={<StartupRedirect />} />
        <Route path="/canvas" element={<CanvasProjectsPage />} />
        <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
        <Route path="/settings" element={<SettingsRoute />} />
        <Route path="/diag" element={<DiagPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <ProjectsModal />
      <SettingsModal />
    </>
  );
}

/** 兼容旧的 /settings 链接：打开设置弹框，并回启动入口（会进上一次的画布），不出现空白或 404。 */
function SettingsRoute() {
  useEffect(() => openSettings(), []);
  return <Navigate to="/" replace />;
}
