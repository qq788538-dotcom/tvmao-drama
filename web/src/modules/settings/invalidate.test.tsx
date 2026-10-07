// @vitest-environment jsdom
import './testing/domSetup';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { useQueryClient } from '@tanstack/react-query';
import { queryClient } from '@/shared/queryClient';

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  listModels: vi.fn(async () => []),
}));

let probedClient: unknown = null;
vi.mock('./pages/GeneralPage', () => ({
  GeneralPage: () => {
    probedClient = useQueryClient();
    return <div>通用页内容</div>;
  },
}));
vi.mock('@/diag/DiagPage', () => ({ DiagPage: () => <div>诊断页内容</div> }));

import { SETTINGS_DEPENDENT_QUERY_KEYS } from './invalidate';
import { SettingsRoot } from './SettingsRoot';

const CANVAS_KEYS = [
  ['runtime-models'],
  ['gen-image-presets'],
  ['multi-angle-config'],
  ['light-control-config'],
  ['canvas-postprocess-config'],
  ['canvas-script-styles'],
];

afterEach(() => {
  queryClient.clear();
});

describe('设置页与画布共用全局 queryClient（final-review I-3）', () => {
  it('SettingsRoot 里的页面拿到的就是全局 queryClient', () => {
    render(<SettingsRoot />);
    expect(screen.getByText('通用页内容')).toBeTruthy();
    expect(probedClient).toBe(queryClient);
  });

  it('设置页查询保持不重试、不随窗口聚焦刷新', () => {
    render(<SettingsRoot />);
    const d = queryClient.getQueryDefaults(['settings-models']);
    expect(d).toMatchObject({ retry: false, refetchOnWindowFocus: false });
  });

  it('失效清单覆盖画布侧全部依赖设置的 key', () => {
    const keys = SETTINGS_DEPENDENT_QUERY_KEYS.map((k) => k.join('/'));
    for (const k of CANVAS_KEYS) expect(keys).toContain(k.join('/'));
  });
});
