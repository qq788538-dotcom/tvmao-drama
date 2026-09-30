import React from 'react';
import ReactDOM from 'react-dom/client';
import { DiagPage } from './diag/DiagPage';

// P0 阶段入口只有诊断页；P3 迁移画布后，诊断页移到「设置 → 环境诊断」。
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <DiagPage />
  </React.StrictMode>,
);
