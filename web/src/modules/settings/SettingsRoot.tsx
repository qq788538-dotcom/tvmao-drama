import { useState, type ReactNode } from 'react';
import { App as AntApp, Button, ConfigProvider, Layout, Menu } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { QueryClientProvider } from '@tanstack/react-query';
import { queryClient } from '@/shared/queryClient';
import { applySettingsQueryDefaults } from './invalidate';
import { settingsTheme } from './theme';
import { GeneralPage } from './pages/GeneralPage';
import { ModelCenterPage } from './pages/ModelCenterPage';
import { DiagPage } from '@/diag/DiagPage';

export type SectionKey = 'general' | 'models' | 'diag';

export const SECTIONS: { key: SectionKey; label: string; group: string }[] = [
  { key: 'general', label: '通用', group: '基础' },
  { key: 'models', label: '模型中心', group: '模型' },
  { key: 'diag', label: '环境诊断', group: '其他' },
];

const PAGES: Record<SectionKey, () => ReactNode> = {
  general: () => <GeneralPage />,
  models: () => <ModelCenterPage />,
  diag: () => <DiagPage embedded />,
};

// 用全局 queryClient（final-review I-3）：设置改完能让画布侧缓存失效，sidecar 重启换会话时
// 也会被 adoptSidecarSession 一起清掉（M-3）。设置页自己的查询沿用原来的默认行为。
applySettingsQueryDefaults(queryClient);

function menuItems() {
  const groups = [...new Set(SECTIONS.map((s) => s.group))];
  return groups.map((g) => ({
    type: 'group' as const,
    key: g,
    label: g,
    children: SECTIONS.filter((s) => s.group === g).map((s) => ({ key: s.key, label: s.label })),
  }));
}

/** 设置页入口。P3a 合并后挂在 /settings 路由，onBack 传“返回画布”。 */
export function SettingsRoot({ initial = 'general', onBack }: { initial?: SectionKey; onBack?: () => void }) {
  const [section, setSection] = useState<SectionKey>(initial);
  return (
    <QueryClientProvider client={queryClient}>
      <ConfigProvider locale={zhCN} theme={settingsTheme}>
        <AntApp>
          <Layout style={{ minHeight: '100vh' }}>
            <Layout.Sider width={200} theme="dark">
              <div style={{ padding: '16px 16px 8px', fontWeight: 600, fontSize: 16 }}>
                {onBack && (
                  <Button type="text" size="small" onClick={onBack} style={{ marginRight: 4 }}>
                    ←
                  </Button>
                )}
                设置
              </div>
              <Menu theme="dark" mode="inline" selectedKeys={[section]} items={menuItems()} onClick={(e) => setSection(e.key as SectionKey)} />
            </Layout.Sider>
            <Layout.Content style={{ padding: 24, overflow: 'auto' }}>{PAGES[section]()}</Layout.Content>
          </Layout>
        </AntApp>
      </ConfigProvider>
    </QueryClientProvider>
  );
}
