import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TextNode } from './nodes/TextNode';
import { useCanvasStore } from './store';
import { isNodeInteracting, resetInteractionRegistry } from './interactionRegistry';
import { setTransientChannel, type OutgoingTransientFrame } from './transientChannel';
import { NODE_EDITING_HEARTBEAT_MS } from './hooks/useNodeEditingBroadcast';

// 文本节点的「正在编辑」广播：进入编辑发一帧、每 3s 续一帧（收端靠 TTL 判活）、
// 退出/卸载发退出帧；同时本地登记在途，让队友的远端 patch 挂起到编辑结束。

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    patchCanvasNode: vi.fn().mockResolvedValue({ version: 1 }),
    upsertCanvasNode: vi.fn().mockResolvedValue({ version: 1 }),
  };
});

function seedTextNode(id: string) {
  useCanvasStore.setState({
    nodes: [{ id, type: 'text-node', x: 0, y: 0, width: 260, height: 200, status: 'idle', settings: {} }],
    connections: [],
    selectedIds: new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
  });
}

function renderNode(id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TextNode id={id} />
    </QueryClientProvider>,
  );
}

describe('文本节点的「正在编辑」瞬态广播', () => {
  let sent: OutgoingTransientFrame[];

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    resetInteractionRegistry();
    sent = [];
    setTransientChannel({ projectId: 14, send: (f) => { sent.push(f); return true; } });
  });

  afterEach(() => {
    setTransientChannel(null);
    resetInteractionRegistry();
    vi.useRealTimers();
  });

  async function enterEditing(id: string) {
    seedTextNode(id);
    const view = renderNode(id);
    fireEvent.click(view.getByText('自己编写内容'));
    await waitFor(() => expect(document.querySelector('.tiptap-text-editor')).toBeTruthy());
    return view;
  }

  it('进入编辑发 editing:true，并登记在途（队友的远端 patch 挂起到编辑结束）', async () => {
    await enterEditing('te-1');
    expect(sent).toEqual([
      { type: 'canvas_transient', project_id: 14, kind: 'node_editing', node_id: 'te-1', editing: true },
    ]);
    expect(isNodeInteracting('te-1')).toBe(true);
  });

  // 续帧间隔必须小于收端 TTL（5s），否则队友那边的角标会一亮一灭。
  it('每 3s 续一帧保活', async () => {
    await enterEditing('te-2');
    await act(async () => { vi.advanceTimersByTime(NODE_EDITING_HEARTBEAT_MS * 2 + 10); });
    expect(sent.filter((f) => f.editing === true)).toHaveLength(3);
  });

  it('失焦退出：发 editing:false、停止续帧、解除在途登记', async () => {
    await enterEditing('te-3');
    const editor = document.querySelector('.tiptap-text-editor')!;
    await act(async () => { fireEvent.blur(editor); });

    expect(sent[sent.length - 1]).toMatchObject({ node_id: 'te-3', editing: false });
    expect(isNodeInteracting('te-3')).toBe(false);

    const afterExit = sent.length;
    await act(async () => { vi.advanceTimersByTime(NODE_EDITING_HEARTBEAT_MS * 2); });
    expect(sent).toHaveLength(afterExit);
  });

  // 卸载路径同样要收尾：blur 不一定发生（节点被删、切故事板、切项目），
  // 漏了退出帧队友的角标要等 5s TTL 才消失，漏了 endInteraction 则该节点上
  // 挂起的远端更新永久卡住。
  it('编辑中卸载：补发 editing:false 并解除在途登记', async () => {
    const view = await enterEditing('te-4');
    await act(async () => { view.unmount(); });

    expect(sent[sent.length - 1]).toMatchObject({ node_id: 'te-4', editing: false });
    expect(isNodeInteracting('te-4')).toBe(false);
  });

  it('个人项目（通道未注册）：进入编辑一帧都不发，但本地在途登记照旧', async () => {
    setTransientChannel(null);
    await enterEditing('te-5');
    expect(sent).toHaveLength(0);
    expect(isNodeInteracting('te-5')).toBe(true);
  });
});
