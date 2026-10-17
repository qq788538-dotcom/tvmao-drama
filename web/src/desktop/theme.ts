import { theme } from 'antd';
import type { ThemeConfig } from 'antd';

// Ant Design 暗色主题。这些值对齐画布的 design token（tailwind.config.js），
// 让 AntD 的弹窗、下拉、tooltip、表格跟画布共用同一套配色。
export const darkTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: '#1fa2dc',
    colorBgBase: '#000000',
    colorBgContainer: '#1f1f1f',
    colorBgElevated: '#262626',
    colorBgLayout: '#000000',
    colorBorder: 'rgba(255,255,255,0.15)',
    colorBorderSecondary: 'rgba(255,255,255,0.10)',
    colorText: '#f5f5f5',
    colorTextSecondary: '#cfcece',
    colorTextTertiary: '#7a7a7a',
    borderRadius: 12,
    fontFamily: 'Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  },
  components: {
    Card: { colorBgContainer: '#1f1f1f' },
    Modal: { contentBg: '#1f1f1f', headerBg: '#1f1f1f' },
    Layout: {
      bodyBg: '#000000',
      headerBg: '#1c1c1c',
      siderBg: '#1c1c1c',
    },
    Menu: {
      darkItemBg: '#1c1c1c',
      darkSubMenuItemBg: '#1c1c1c',
      darkItemSelectedBg: '#262626',
    },
    Popover: { colorBgElevated: '#262626' },
    Tooltip: { colorBgSpotlight: '#262626' },
  },
};
