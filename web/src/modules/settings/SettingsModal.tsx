import type { ReactNode } from 'react';
import { App as AntApp, ConfigProvider, Menu, Modal } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { QueryClientProvider } from '@tanstack/react-query';
import { queryClient } from '@/shared/queryClient';
import { applySettingsQueryDefaults } from './invalidate';
import { settingsTheme } from './theme';
import { GeneralPage } from './pages/GeneralPage';
import { ModelCenterPage } from './pages/ModelCenterPage';
import { CliToolCard } from './components/CliToolCard';
import { DiagPage } from '@/diag/DiagPage';
import { type SectionKey, useSettingsModal } from './settingsModalStore';

export type { SectionKey };

// 菜单只有一级、没有分组标题。
export const SECTIONS: { key: SectionKey; label: string }[] = [
  { key: 'models', label: '模型中心' },
  { key: 'cli', label: '命令行工具' },
  { key: 'general', label: '配置' },
  { key: 'diag', label: '环境诊断' },
];

const PAGES: Record<SectionKey, () => ReactNode> = {
  general: () => <GeneralPage />,
  models: () => <ModelCenterPage />,
  cli: () => <CliToolCard />,
  diag: () => <DiagPage embedded />,
};

// 用全局 queryClient（final-review I-3）：设置改完能让画布侧缓存失效，sidecar 重启换会话时
// 也会被 adoptSidecarSession 一起清掉（M-3）。设置页自己的查询沿用原来的默认行为。
applySettingsQueryDefaults(queryClient);

/**
 * 设置弹框：盖在当前页面（通常是画布）上，不改路由、不卸载画布。
 * 打开/关闭/选中哪一项见 settingsModalStore。模型中心里的编辑子弹框是普通 antd Modal，
 * 后开的层级更高，叠在本弹框上面。
 */
export function SettingsModal() {
  const open = useSettingsModal((s) => s.open);
  const tab = useSettingsModal((s) => s.tab);
  const setTab = useSettingsModal((s) => s.setTab);
  const close = useSettingsModal((s) => s.closeSettings);
  return (
    <QueryClientProvider client={queryClient}>
      <ConfigProvider locale={zhCN} theme={settingsTheme}>
        <AntApp component={false}>
          <Modal
            open={open}
            onCancel={close}
            footer={null}
            title="设置"
            width="min(1100px, 92vw)"
            centered
            maskClosable={false}
            keyboard={false}
            destroyOnClose
            styles={{ body: { padding: 0 } }}
          >
            <div style={{ display: 'flex', height: '80vh' }}>
              <Menu
                mode="inline"
                theme="dark"
                selectedKeys={[tab]}
                items={SECTIONS.map((s) => ({ key: s.key, label: s.label }))}
                onClick={(e) => setTab(e.key as SectionKey)}
                style={{ width: 140, flex: 'none', borderInlineEnd: 0, paddingTop: 8 }}
              />
              <div style={{ flex: 1, minWidth: 0, overflow: 'auto', padding: 24 }}>{PAGES[tab]()}</div>
            </div>
          </Modal>
        </AntApp>
      </ConfigProvider>
    </QueryClientProvider>
  );
}
