import { Navigate, Route, Routes } from 'react-router-dom';
import { CanvasProjectsPage } from '@/modules/canvas/CanvasProjectsPage';
import { CanvasEditorPage } from '@/modules/canvas/CanvasEditorPage';
import { DiagPage } from '@/diag/DiagPage';
import { StartupRedirect } from '@/desktop/StartupRedirect';

// 桌面端路由（spec F9）：
//   /                  启动入口，直接跳进画布（上一次 → 最近修改 → 新建）
//   /canvas            所有项目（原样复用参考仓的 CanvasProjectsPage）
//   /canvas/:projectId 画布编辑器
//   /diag              环境诊断（P0 诊断页，隐藏入口；P4 移入「设置 → 环境诊断」）
// 路径与参考仓保持一致：画布模块里有十余处直接从 location.pathname 解析 /canvas/:id。
export function App() {
  return (
    <Routes>
      <Route path="/" element={<StartupRedirect />} />
      <Route path="/canvas" element={<CanvasProjectsPage />} />
      <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
      <Route path="/diag" element={<DiagPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
