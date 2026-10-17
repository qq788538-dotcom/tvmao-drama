import { theme, type ThemeConfig } from 'antd';

// 与画布的暗色主题一致，让设置页与画布同一套配色。
// 全局 ConfigProvider 用的是同一份 token，这里嵌套一层不会产生差异。
export const settingsTheme: ThemeConfig = {
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
    Layout: { bodyBg: '#000000', headerBg: '#1c1c1c', siderBg: '#1c1c1c' },
    Menu: { darkItemBg: '#1c1c1c', darkSubMenuItemBg: '#1c1c1c', darkItemSelectedBg: '#262626' },
    Popover: { colorBgElevated: '#262626' },
    Tooltip: { colorBgSpotlight: '#262626' },
  },
};
