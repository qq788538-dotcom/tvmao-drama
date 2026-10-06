// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { App as AntApp } from 'antd';

const save = { mutate: vi.fn(), isPending: false };
const restore = { mutate: vi.fn(), isPending: false };
let data: unknown;
const setValue = (v: unknown) => { data = { value: v, modified: false }; };
vi.mock('../useConfigEditor', () => ({
  useConfigEditor: () => ({ data, isLoading: false, save, restore }),
}));
vi.mock('../components/ModelSelect', () => ({ ModelSelect: () => <div>model-select</div> }));

import { ScriptConfigPage } from './ScriptConfigPage';
import { PostprocessPage } from './PostprocessPage';

const banned = ['添加', '移除', '上移', '下移', '删除'];

describe('脚本与后处理页', () => {
  it('脚本页：无增删排序按钮，保存只提交允许字段', () => {
    setValue({ modelCandidates: [{ modelId: 'a', displayName: 'A' }, { modelId: 'b', displayName: 'B' }], styles: [] });
    render(<AntApp><ScriptConfigPage /></AntApp>);
    for (const t of banned) expect(screen.queryByText(new RegExp(t))).toBeNull();
    expect(screen.queryByText('model-select')).toBeNull();
    expect(screen.getByText('a')).toBeTruthy();
    fireEvent.change(screen.getAllByPlaceholderText('展示名')[0], { target: { value: 'A2' } });
    fireEvent.click(screen.getByText(/保\s*存/));
    expect(save.mutate).toHaveBeenCalledWith(expect.objectContaining({ modelCandidates: [{ modelId: 'a', displayName: 'A2' }, { modelId: 'b', displayName: 'B' }] }));
  });
  it('后处理页：去字幕模型 ID 只读，保存只提交允许字段', () => {
    save.mutate.mockClear();
    setValue({ subtitleRemovalModels: [{ modelId: 'v1', label: 'L', subtitle: 's' }] });
    render(<AntApp><PostprocessPage /></AntApp>);
    for (const t of banned) expect(screen.queryByText(new RegExp(t))).toBeNull();
    expect(screen.getByText('v1')).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText('显示名'), { target: { value: 'L2' } });
    fireEvent.click(screen.getByText(/保\s*存/));
    expect(save.mutate).toHaveBeenCalledWith({ subtitleRemovalModels: [{ modelId: 'v1', label: 'L2', subtitle: 's' }] });
  });
});
