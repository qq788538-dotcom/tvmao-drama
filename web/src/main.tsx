import React from 'react';
import ReactDOM from 'react-dom/client';
import 'antd/dist/reset.css';
import { SettingsRoot } from './modules/settings/SettingsRoot';

// P4 分支临时入口：直接进设置页（环境诊断在 设置 → 环境诊断）。
// P3a 合并后以 P3a 的入口为准，设置页挂在 /settings（见 P4 计划 Task 15）。
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <SettingsRoot />
  </React.StrictMode>,
);
