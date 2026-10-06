// @vitest-environment jsdom
import './testing/domSetup';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('@/diag/DiagPage', () => ({ DiagPage: () => <div>诊断页内容</div> }));
vi.mock('./pages/GeneralPage', () => ({ GeneralPage: () => <div>通用页内容</div> }));

import { SettingsRoot, SECTIONS } from './SettingsRoot';

describe('SettingsRoot', () => {
  it('列出全部分区，默认显示通用页，可切到环境诊断', () => {
    render(<SettingsRoot />);
    for (const s of SECTIONS) expect(screen.getAllByText(s.label).length).toBeGreaterThan(0);
    expect(screen.getByText('通用页内容')).toBeTruthy();
    fireEvent.click(screen.getByText('环境诊断'));
    expect(screen.getByText('诊断页内容')).toBeTruthy();
  });
  it('不出现任何导入导出或新建入口（spec M7、M8、A4）', () => {
    render(<SettingsRoot />);
    for (const banned of ['导入', '导出', '新建', '新增', 'Skill']) expect(screen.queryByText(new RegExp(banned))).toBeNull();
  });
});
