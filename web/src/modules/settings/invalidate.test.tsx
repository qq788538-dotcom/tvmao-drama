// @vitest-environment jsdom
import './testing/domSetup';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, renderHook, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClientProvider, useQueryClient } from '@tanstack/react-query';
import { queryClient } from '@/shared/queryClient';

vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  getConfig: vi.fn(async (key: string) => ({ key, value: { a: 1 }, modified: false })),
  putConfig: vi.fn(async (key: string, value: unknown) => ({ key, value, modified: true })),
  restoreConfig: vi.fn(async (key: string) => ({ key, value: { a: 1 }, modified: false })),
  listModels: vi.fn(async () => []),
  getAgentModels: vi.fn(async () => ({ data: [] })),
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
import { useConfigEditor } from './useConfigEditor';
import { SettingsRoot } from './SettingsRoot';

const CANVAS_KEYS = [
  ['runtime-models'],
  ['gen-image-presets'],
  ['multi-angle-config'],
  ['light-control-config'],
  ['canvas-postprocess-config'],
  ['canvas-script-styles'],
];

function seedCanvasCaches() {
  for (const key of CANVAS_KEYS) queryClient.setQueryData(key, { cached: true });
  queryClient.setQueryData(['settings-agent-models'], { data: [] });
}

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <AntApp>{children}</AntApp>
    </QueryClientProvider>
  );
}

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
    const d = queryClient.getQueryDefaults(['settings-config', 'canvas_multi_angle']);
    expect(d).toMatchObject({ retry: false, refetchOnWindowFocus: false });
  });

  it('失效清单覆盖画布侧全部依赖设置的 key', () => {
    const keys = SETTINGS_DEPENDENT_QUERY_KEYS.map((k) => k.join('/'));
    for (const k of CANVAS_KEYS) expect(keys).toContain(k.join('/'));
  });

  it.each(['save', 'restore'] as const)('画布配置 %s 成功后，画布侧缓存全部失效', async (action) => {
    seedCanvasCaches();
    const { result } = renderHook(() => useConfigEditor<{ a: number }>('canvas_multi_angle'), { wrapper });
    await waitFor(() => expect(result.current.data).toBeTruthy());
    for (const key of CANVAS_KEYS) expect(queryClient.getQueryState(key)?.isInvalidated).toBe(false);

    await act(async () => {
      if (action === 'save') await result.current.save.mutateAsync({ a: 2 });
      else await result.current.restore.mutateAsync();
    });

    for (const key of CANVAS_KEYS) expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(['settings-agent-models'])?.isInvalidated).toBe(true);
  });
});
