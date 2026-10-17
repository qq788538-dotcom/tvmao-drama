import { App as AntApp } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { CanvasNode } from '@/modules/canvas/editor/types';
import {
  isUnsavedNode,
  replaceHistoryUrl,
  retryErrorMessage,
  retrySaveToLocal,
  useUnsavedLocalBadge,
} from './unsavedLocal';

vi.mock('@/modules/canvas/editor/persistence/canvasMutations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/modules/canvas/editor/persistence/canvasMutations')>()),
  persistNodeById: vi.fn(),
}));

const REMOTE = 'https://upstream.example.com/out/1.png?sig=a&exp=1';
const LOCAL = 'http://127.0.0.1:5123/media/gen/9.png?token=t';

function genNode(patch: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id: 'g1',
    type: 'gen-image',
    x: 0,
    y: 0,
    width: 280,
    height: 280,
    status: 'succeeded',
    content: REMOTE,
    settings: {},
    ...patch,
  };
}

function setNodes(nodes: CanvasNode[]) {
  useCanvasStore.setState({ nodes, connections: [], selectedIds: new Set<string>(), readOnly: false });
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.pushState({}, '', '/canvas/14');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('isUnsavedNode', () => {
  it('快照里带 settings.unsaved 且内容不是本机地址的节点算未保存', () => {
    // 非生成类节点只能靠 unsaved 标记认出来
    expect(isUnsavedNode(genNode({ type: 'input-image', settings: { unsaved: true, genTaskId: 3 } }))).toBe(true);
  });

  // 页面开着重新生成拿到本地地址后，store 里可能残留旧的 unsaved 标记。
  it('unsaved 标记残留但内容已是本机地址（sidecar media 或 media://）时不算', () => {
    expect(isUnsavedNode(genNode({ content: LOCAL, settings: { unsaved: true, genTaskId: 3 } }))).toBe(false);
    expect(isUnsavedNode(genNode({ content: 'media://gen/1.png', settings: { unsaved: true, genTaskId: 3 } }))).toBe(false);
  });

  // 页面开着时生成结果靠轮询回填，store 里没有 unsaved 键：按「生成类节点 + 成功 + 外链」认定。
  it('生成类节点成功但内容是上游外链，也算未保存', () => {
    for (const type of ['gen-image', 'gen-video', 'gen-audio', 'subtitle-erase', 'video-upscale', 'video-whitemodel'] as const) {
      expect(isUnsavedNode(genNode({ type }))).toBe(true);
    }
  });

  it('本机地址、非生成类节点、未完成的节点、空节点都不算', () => {
    expect(isUnsavedNode(genNode({ content: LOCAL }))).toBe(false);
    expect(isUnsavedNode(genNode({ type: 'input-image' }))).toBe(false);
    expect(isUnsavedNode(genNode({ status: 'generating' }))).toBe(false);
    expect(isUnsavedNode(genNode({ content: undefined }))).toBe(false);
    expect(isUnsavedNode(undefined)).toBe(false);
  });
});

describe('replaceHistoryUrl', () => {
  it('字符串条目和对象条目里的旧地址都换成新地址，其余原样', () => {
    expect(
      replaceHistoryUrl([REMOTE, { url: REMOTE, prompt: 'p' }, 'media://old', { url: 'media://x' }], REMOTE, LOCAL),
    ).toEqual([LOCAL, { url: LOCAL, prompt: 'p' }, 'media://old', { url: 'media://x' }]);
  });

  it('不是数组时原样返回', () => {
    expect(replaceHistoryUrl(undefined, REMOTE, LOCAL)).toBeUndefined();
  });
});

describe('retrySaveToLocal', () => {
  it('有 genTaskId：调 repersist，把节点内容、历史、assetId 换成本地并清掉标记，再补一次持久化', async () => {
    setNodes([genNode({ settings: { unsaved: true, genTaskId: 42, history: [{ url: REMOTE, prompt: 'p' }, 'media://old'] } })]);
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [9] } });

    await retrySaveToLocal('g1');

    expect(post).toHaveBeenCalledWith('/tasks/42/repersist');
    const n = useCanvasStore.getState().nodes[0];
    expect(n.content).toBe(LOCAL);
    expect(n.settings).toMatchObject({ assetId: 9, history: [{ url: LOCAL, prompt: 'p' }, 'media://old'] });
    expect(n.settings?.unsaved).toBeUndefined();
    expect(n.settings?.genTaskId).toBeUndefined();
    expect(persistNodeById).toHaveBeenCalledWith('g1');
  });

  it('没有 genTaskId（页面开着时出的结果）：先按节点找回任务号', async () => {
    setNodes([genNode()]);
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { taskId: 77, status: 'succeeded' } });
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [] } });

    await retrySaveToLocal('g1');

    expect(get).toHaveBeenCalledWith('/canvas-tasks/by-node', { params: { projectId: 14, nodeId: 'g1' } });
    expect(post).toHaveBeenCalledWith('/tasks/77/repersist');
    expect(useCanvasStore.getState().nodes[0].content).toBe(LOCAL);
  });

  it('找不到任务号时报错，不调 repersist', async () => {
    setNodes([genNode()]);
    vi.spyOn(api, 'get').mockResolvedValue({ data: {} });
    const post = vi.spyOn(api, 'post');

    await expect(retrySaveToLocal('g1')).rejects.toThrow('找不到这次生成的任务记录，无法重试');
    expect(post).not.toHaveBeenCalled();
  });

  it('等待期间节点已重新生成：只替换历史，不动当前内容和标记', async () => {
    setNodes([genNode({ settings: { genTaskId: 42, history: [REMOTE] } })]);
    vi.spyOn(api, 'post').mockImplementation(async () => {
      setNodes([genNode({ content: 'media://newer', settings: { genTaskId: 50, history: [REMOTE, 'media://newer'] } })]);
      return { data: { outputUrls: [LOCAL], assetIds: [9] } };
    });

    await retrySaveToLocal('g1');

    const n = useCanvasStore.getState().nodes[0];
    expect(n.content).toBe('media://newer');
    expect(n.settings).toMatchObject({ genTaskId: 50, history: [LOCAL, 'media://newer'] });
    expect(n.settings?.assetId).toBeUndefined();
  });

  it('retryErrorMessage 优先取 sidecar 返回的原因', () => {
    expect(retryErrorMessage({ response: { data: { error: '保存到本地失败：timeout' } } })).toBe('保存到本地失败：timeout');
    expect(retryErrorMessage(new Error('找不到这次生成的任务记录，无法重试'))).toBe('找不到这次生成的任务记录，无法重试');
    expect(retryErrorMessage(undefined)).toBe('保存到本地失败');
  });
});

function BadgeProbe({ nodeId }: { nodeId: string }) {
  return <div>{useUnsavedLocalBadge(nodeId)}</div>;
}

function renderBadge() {
  render(
    <AntApp>
      <BadgeProbe nodeId="g1" />
    </AntApp>,
  );
}

describe('useUnsavedLocalBadge', () => {
  it('未保存时显示「未保存到本地」和「重试」', () => {
    setNodes([genNode()]);
    renderBadge();
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('本机文件不显示角标', () => {
    setNodes([genNode({ content: LOCAL })]);
    renderBadge();
    expect(screen.queryByText('未保存到本地')).toBeNull();
  });

  // 上次下载失败（unsaved + genTaskId A），不点重试直接重新生成并成功拿到本地地址。
  // 角标必须消失、重试按钮不可用，否则重试会拿任务 A 的图把新结果换掉。
  it('先失败后又重新生成成功：角标消失，没有可点的重试按钮', async () => {
    setNodes([genNode({ settings: { unsaved: true, genTaskId: 42, history: [REMOTE] } })]);
    const post = vi.spyOn(api, 'post');
    renderBadge();
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();

    // 模拟重新生成成功回填：内容换成本地地址，即使 unsaved/genTaskId 没被清也不能再挂角标。
    act(() => {
      useCanvasStore.getState().updateNode('g1', { content: LOCAL });
      useCanvasStore.getState().updateNodeSettings('g1', { history: [REMOTE, LOCAL] });
    });

    await waitFor(() => expect(screen.queryByText('未保存到本地')).toBeNull());
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull();
    expect(post).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().nodes[0].content).toBe(LOCAL);
  });

  it('重试成功：提示「已保存到本地」，角标消失', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [9] } });
    renderBadge();

    await userEvent.click(screen.getByRole('button', { name: '重试' }));

    expect(await screen.findByText('已保存到本地')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('未保存到本地')).toBeNull());
  });

  it('重试失败：提示 sidecar 的原因，角标保留、可以再点', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    vi.spyOn(api, 'post').mockRejectedValue({ response: { data: { error: '保存到本地失败：上游地址已过期' } } });
    renderBadge();

    await userEvent.click(screen.getByRole('button', { name: '重试' }));

    expect(await screen.findByText('保存到本地失败：上游地址已过期')).toBeInTheDocument();
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: '重试' })).not.toBeDisabled());
  });

  it('重试进行中按钮置灰，连点只发一次请求', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let finish: (v: any) => void = () => {};
    const post = vi.spyOn(api, 'post').mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    renderBadge();

    const btn = screen.getByRole('button', { name: '重试' });
    await userEvent.click(btn);
    await waitFor(() => expect(btn).toBeDisabled());
    await userEvent.click(btn);
    expect(post).toHaveBeenCalledTimes(1);

    finish({ data: { outputUrls: [LOCAL], assetIds: [] } });
    await waitFor(() => expect(screen.queryByText('未保存到本地')).toBeNull());
  });
});
