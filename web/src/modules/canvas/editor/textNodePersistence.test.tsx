import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TextNode } from './nodes/TextNode';
import { useCanvasStore } from './store';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  resetCanvasPersistenceForTest,
  setCanvasOwnerToken,
} from './persistence/canvasMutations';
import { commitTextDraft, flushTextDraft } from './nodes/text/textNodeDraft';
import { patchCanvasNode, upsertCanvasNode } from '@/shared/api';

// 文本节点的持久化回归测试。updateNodeSettings 只改内存（画布没有全量自动保存），
// 每个节点必须自己在恰当时机显式落库。TextNode 曾经漏掉了这一步：打字只进 store，
// 刷新即丢，用户以为"文字保存不了"——上过生产的真实回归。
// Tiptap 化之后落库路径收口在 textNodeDraft.ts（编辑器 onUpdate/onBlur 都走它），
// 所以持久化断言直接打 commitTextDraft/flushTextDraft，不穿透 ProseMirror 模拟打字；
// 组件测试只覆盖四态切换（菜单/编辑/只读渲染/空占位）。

vi.mock('@/shared/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api')>();
  return {
    ...actual,
    api: { post: vi.fn() },
    patchCanvasNode: vi.fn(),
    upsertCanvasNode: vi.fn(),
    currentProjectId: vi.fn().mockReturnValue(42),
  };
});


function renderNode(id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TextNode id={id} />
    </QueryClientProvider>,
  );
}

function seedTextNode(id: string, text?: string) {
  useCanvasStore.setState({
    nodes: [{
      id,
      type: 'text-node',
      x: 0,
      y: 0,
      width: 260,
      height: 200,
      status: 'idle',
      settings: text === undefined ? {} : { text },
    }],
    connections: [],
    selectedIds: new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    lightbox: null,
    readOnly: false,
  });
}

describe('文本节点的持久化', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNode).mockResolvedValue({ version: 1 });
    vi.mocked(upsertCanvasNode).mockResolvedValue({ version: 1 });
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  it('commitTextDraft 本地立即可见，冲刷后落库为 settings.text patch', async () => {
    seedTextNode('t-1');
    commitTextDraft('t-1', '# 第一版分镜脚本');
    // 本地立即可见
    expect(useCanvasStore.getState().nodes[0].settings?.text).toBe('# 第一版分镜脚本');

    // debounce 攒着没关系，冲掉之后必须到服务端
    await act(async () => {
      await flushTextDraft('t-1');
      await flushCanvasMutations();
    });
    // 签名是 patchNode(projectID, ownerToken, nodeID, patch)——nodeID 在第 3 位。
    const calls = vi.mocked(patchCanvasNode).mock.calls.filter(([, , id]) => id === 't-1');
    expect(calls.length).toBeGreaterThan(0);
    expect(calls[calls.length - 1][3]).toMatchObject({ settings: { text: '# 第一版分镜脚本' } });
  });

  it('空节点显示菜单态，点「自己编写内容」挂载 Tiptap 编辑器', async () => {
    seedTextNode('t-2');
    const { getByText } = renderNode('t-2');
    fireEvent.click(getByText('自己编写内容'));
    await waitFor(() => {
      expect(document.querySelector('.tiptap-text-editor')).toBeTruthy();
    });
  });

  it('编辑器失焦触发冲刷——不等 debounce 到点', async () => {
    seedTextNode('t-3');
    const { getByText } = renderNode('t-3');
    fireEvent.click(getByText('自己编写内容'));
    const editorEl = await waitFor(() => {
      const el = document.querySelector('.tiptap-text-editor');
      expect(el).toBeTruthy();
      return el!;
    });

    // 模拟编辑器 onUpdate 的落库调用（打字→onUpdate→commitTextDraft），
    // 再触发失焦：blur 必须立即冲刷，不等 1.5s debounce。
    await act(async () => {
      commitTextDraft('t-3', '写到一半切走');
      fireEvent.blur(editorEl);
      await flushCanvasMutations();
    });

    const calls = vi.mocked(patchCanvasNode).mock.calls.filter(([, , id]) => id === 't-3');
    expect(calls.length).toBeGreaterThan(0);
    expect(calls[calls.length - 1][3]).toMatchObject({ settings: { text: '写到一半切走' } });
  });
});

describe('文本节点的四态展示', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetCanvasPersistenceForTest();
    configureCanvasPersistence({ projectID: 42 });
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNode).mockResolvedValue({ version: 1 });
  });

  afterEach(() => {
    resetCanvasPersistenceForTest();
  });

  it('有内容节点渲染 markdown 只读态（# 转 h1）', () => {
    seedTextNode('t-4', '# 大标题\n\n正文一段');
    const { container } = renderNode('t-4');
    const view = container.querySelector('[data-testid="text-node-view"]')!;
    expect(view).toBeTruthy();
    expect(view.querySelector('h1')?.textContent).toBe('大标题');
    expect(view.textContent).toContain('正文一段');
  });

  it('只读画布（分享页）不出现「自己编写内容」入口', () => {
    seedTextNode('t-5');
    useCanvasStore.setState({ readOnly: true });
    const { queryByText } = renderNode('t-5');
    expect(queryByText('自己编写内容')).toBeNull();
  });

  // 回归：曾在菜单/只读/空占位容器上整片 onMouseDown stopPropagation，把
  // NodeShell 的「mousedown → 选中+拖拽」吞掉——节点拖不动、Delete 删不了、
  // 生成面板因 isSelected 永远起不来而点不到。内容区只允许记录按下坐标，
  // 不许拦截冒泡。
  it('菜单态 mousedown 冒泡到 NodeShell——节点可被选中', () => {
    seedTextNode('t-6');
    const { container } = renderNode('t-6');
    const menu = container.querySelector('[data-testid="text-node-menu"]')!;
    fireEvent.mouseDown(menu, { button: 0, clientX: 10, clientY: 10 });
    expect(useCanvasStore.getState().selectedIds.has('t-6')).toBe(true);
  });

  it('只读渲染态 mousedown 同样可选中节点', () => {
    seedTextNode('t-7', '# 标题');
    const { container } = renderNode('t-7');
    const view = container.querySelector('[data-testid="text-node-view"]')!;
    fireEvent.mouseDown(view, { button: 0, clientX: 10, clientY: 10 });
    expect(useCanvasStore.getState().selectedIds.has('t-7')).toBe(true);
  });

  // 回归：tiptap-markdown 的 transformPastedText 默认关闭,粘贴 markdown 原文
  // 会被当纯文本转义(# 被存成 \#),满屏符号不渲染。
  it('粘贴 markdown 原文按 markdown 解析（# 变标题而不是字面符号）', async () => {
    seedTextNode('t-9');
    const { getByText } = renderNode('t-9');
    fireEvent.click(getByText('自己编写内容'));
    const editorEl = (await waitFor(() => {
      const el = document.querySelector('.tiptap-text-editor');
      expect(el).toBeTruthy();
      return el;
    }))!;

    // 模拟真实剪贴板:从 VSCode/网页复制会同时带 text/html——ProseMirror 默认
    // 优先吃 HTML,md 原文按字面进来。handlePaste 必须无视 HTML 通道取纯文本。
    fireEvent.paste(editorEl, {
      clipboardData: {
        types: ['text/html', 'text/plain'],
        getData: (type: string) => {
          if (type === 'text/plain') return '# 粘贴的标题\n\n**加粗**';
          if (type === 'text/html') return '<meta charset="utf-8"><p># 粘贴的标题</p><p>**加粗**</p>';
          return '';
        },
      },
    });
    await waitFor(() => {
      expect(editorEl.querySelector('h1')?.textContent).toBe('粘贴的标题');
      expect(editorEl.querySelector('strong')?.textContent).toBe('加粗');
    });
  });

  // 文本节点右键菜单（对标 LibTV，用户拍板 2026-08-13）：标配两项 + 复制文本 +
  // 粘贴 + 删除；不带媒体节点那套「保存到素材库/合规校验」。
  it('右键菜单是文本专属五项，媒体项不出现', () => {
    seedTextNode('t-menu', '一段内容');
    const { container, getByText, queryByText } = renderNode('t-menu');
    fireEvent.contextMenu(container.querySelector('[data-testid="text-node-view"]')!);
    expect(getByText('复制节点')).toBeTruthy();
    expect(getByText('创建副本')).toBeTruthy();
    expect(getByText('复制文本')).toBeTruthy();
    expect(getByText('粘贴')).toBeTruthy();
    expect(getByText('删除')).toBeTruthy();
    expect(queryByText('保存到素材库')).toBeNull();
    expect(queryByText('Seedance2.0 合规校验')).toBeNull();
    expect(queryByText('拷贝节点')).toBeNull();
  });

  it('菜单「创建副本」原地复制出第二个文本节点', () => {
    seedTextNode('t-dup', '# 提示词');
    const { container, getByText } = renderNode('t-dup');
    fireEvent.contextMenu(container.querySelector('[data-testid="text-node-view"]')!);
    fireEvent.click(getByText('创建副本'));
    const nodes = useCanvasStore.getState().nodes.filter((n) => n.type === 'text-node');
    expect(nodes.length).toBe(2);
    expect(nodes[1].settings?.text).toBe('# 提示词');
  });

  it('按下后拖动超过阈值，click 不进入编辑（拖节点不误触编辑态）', async () => {
    seedTextNode('t-8', '# 标题');
    const { container } = renderNode('t-8');
    const view = container.querySelector('[data-testid="text-node-view"]')!;
    fireEvent.mouseDown(view, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.click(view, { clientX: 60, clientY: 60 });
    expect(document.querySelector('.tiptap-text-editor')).toBeNull();
    // 原地点击（无位移）才进编辑
    fireEvent.mouseDown(view, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.click(view, { clientX: 11, clientY: 10 });
    await waitFor(() => {
      expect(document.querySelector('.tiptap-text-editor')).toBeTruthy();
    });
  });
});
