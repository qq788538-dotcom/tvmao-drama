// @vitest-environment jsdom
import './testing/domSetup';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';

vi.mock('@/diag/DiagPage', () => ({ DiagPage: ({ embedded }: { embedded?: boolean }) => <div>诊断页内容{embedded ? '-embedded' : ''}</div> }));
vi.mock('./pages/GeneralPage', () => ({ GeneralPage: () => <div>通用页内容</div> }));
vi.mock('./pages/ModelCenterPage', () => ({ ModelCenterPage: () => <div>模型中心内容</div> }));
vi.mock('./components/CliToolCard', () => ({ CliToolCard: () => <div>命令行工具内容</div> }));

import { SettingsModal, SECTIONS } from './SettingsModal';
import { openSettings, useSettingsModal } from './settingsModalStore';

function Probe() {
  return <div data-testid="path">{useLocation().pathname}</div>;
}
function setup() {
  return render(
    <MemoryRouter initialEntries={['/canvas/1']}>
      <div>canvas-behind</div>
      <Probe />
      <SettingsModal />
    </MemoryRouter>,
  );
}

describe('SettingsModal', () => {
  beforeEach(() => useSettingsModal.setState({ open: false, tab: 'general' }));

  it('未打开时不渲染内容；openSettings 后打开，URL 不变', () => {
    setup();
    expect(screen.queryByText('通用页内容')).toBeNull();
    act(() => openSettings());
    expect(screen.getByText('通用页内容')).toBeTruthy();
    expect(screen.getByTestId('path').textContent).toBe('/canvas/1');
  });

  it('默认打开第一项"模型中心"', () => {
    expect(useSettingsModal.getInitialState().tab).toBe('models');
  });

  it('菜单正好 3 项、没有分组标题', () => {
    setup();
    act(() => openSettings());
    expect(SECTIONS.map((s) => s.label)).toEqual(['模型中心', '命令行工具', '对象存储', '配置', '环境诊断']);
    const items = [...document.querySelectorAll('.ant-menu-item')].map((e) => e.textContent);
    expect(items).toEqual(['模型中心', '命令行工具', '对象存储', '配置', '环境诊断']);
    expect(document.querySelector('.ant-menu-item-group-title')).toBeNull();
    expect(document.querySelector('.ant-menu-submenu')).toBeNull();
  });

  it('切换菜单显示对应内容；诊断页用 embedded', () => {
    setup();
    act(() => openSettings());
    fireEvent.click(screen.getByText('模型中心'));
    expect(screen.getByText('模型中心内容')).toBeTruthy();
    fireEvent.click(screen.getByText('命令行工具'));
    expect(screen.getByText('命令行工具内容')).toBeTruthy();
    fireEvent.click(screen.getByText('环境诊断'));
    expect(screen.getByText('诊断页内容-embedded')).toBeTruthy();
  });

  it('openSettings(tab) 直接打开指定一项', () => {
    setup();
    act(() => openSettings('models'));
    expect(screen.getByText('模型中心内容')).toBeTruthy();
  });

  it('关闭后弹框消失，背后的页面还在', async () => {
    setup();
    act(() => openSettings());
    fireEvent.click(document.querySelector('.ant-modal-close')!);
    await waitFor(() => expect(useSettingsModal.getState().open).toBe(false));
    expect(screen.getByText('canvas-behind')).toBeTruthy();
    expect(screen.getByTestId('path').textContent).toBe('/canvas/1');
  });

  it('点弹框外和按 Esc 都不会关闭，只能点 X', () => {
    setup();
    act(() => openSettings());
    fireEvent.click(document.querySelector('.ant-modal-wrap')!);
    fireEvent.keyDown(document.querySelector('.ant-modal-wrap')!, { key: 'Escape', code: 'Escape', keyCode: 27 });
    expect(useSettingsModal.getState().open).toBe(true);
    expect(screen.getByText('通用页内容')).toBeTruthy();
  });

  it('不出现任何导入导出或新建入口', () => {
    setup();
    act(() => openSettings());
    for (const banned of ['导入', '导出', '新建', '新增', 'Skill']) expect(screen.queryByText(new RegExp(banned))).toBeNull();
  });
});
