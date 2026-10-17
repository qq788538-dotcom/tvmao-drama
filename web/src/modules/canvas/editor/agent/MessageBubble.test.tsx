import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor, fireEvent, screen } from '@testing-library/react';
import { api } from '@/shared/api';
import { MarkdownBlock, MessageBubble } from './MessageBubble';
import type { AgentMessage } from './agentStore';
import { __resetThumbCacheForTest } from '../thumbs';
import { useCanvasStore } from '../store';

vi.mock('@/shared/api', () => ({
  api: { post: vi.fn() },
}));

const postMock = vi.mocked(api.post);

// 带 query 的签名 URL——marked 会把 href 里的 & 实体化成 &amp;，
// 这条用例同时覆盖实体解码/回编码路径。
const originalUrl = 'https://cdn.test/gen.png?a=1&b=2';
const thumbUrl = 'https://cdn.test/gen.png?a=1&b=2&x-tos-process=w400';

describe('MarkdownBlock 内联图缩略图', () => {
  beforeEach(() => {
    __resetThumbCacheForTest();
    postMock.mockReset();
    postMock.mockResolvedValue({ data: { thumbs: { [originalUrl]: thumbUrl } } });
    useCanvasStore.setState({ lightbox: null });
  });

  it('裸图片 URL 内联成 img：初始无 src（不下原图），换取后回填缩略图', async () => {
    const { container } = render(<MarkdownBlock text={`已生成图片：\n${originalUrl}`} />);

    const img = container.querySelector('img');
    expect(img).not.toBeNull();
    // pending 期间不给 src，否则浏览器会先下原图。
    expect(img!.getAttribute('src')).toBeNull();
    expect(img!.dataset.original).toBe(originalUrl);

    await waitFor(() => {
      expect(container.querySelector('img')!.getAttribute('src')).toBe(thumbUrl);
    });
  });

  it('点击内联图打开 Lightbox 用的是 data-original 原图，不是缩略图', async () => {
    const { container } = render(<MarkdownBlock text={originalUrl} />);
    await waitFor(() => {
      expect(container.querySelector('img')!.getAttribute('src')).toBe(thumbUrl);
    });

    fireEvent.click(container.querySelector('img')!);

    expect(useCanvasStore.getState().lightbox).toEqual({
      url: originalUrl,
      kind: 'image',
      title: '',
    });
  });
});

describe('tool_event 的 text_info 定位 chip', () => {
  const bubbleMsg = (artifacts: object[]): AgentMessage => ({
    id: 'm1',
    thread_id: 't1',
    action_id: 'a1',
    role: 'assistant',
    blocks: [{ type: 'tool_event', name: 'create_text_node', artifacts } as never],
  });

  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      view: { x: 0, y: 0, zoom: 1 },
      selectedIds: new Set<string>(),
      undoStack: [],
      redoStack: [],
      readOnly: false,
      lightbox: null,
    });
  });

  it('gen_*_node_info 渲染成定位 chip（铺节点不生成的产物，同样无 url）', () => {
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'gen_video_node_info', artifact_id: 'gv_1', name: '镜头1·雨夜追逐', prompt: 'x' },
          { artifact_type: 'gen_image_node_info', artifact_id: 'gi_1', prompt: 'y' },
        ])}
      />,
    );
    expect(screen.getByText('镜头1·雨夜追逐')).toBeTruthy();
    // 无 title 时按类型给可读兜底名。
    expect(screen.getByText('AI 绘图节点')).toBeTruthy();
  });

  it('text_info 渲染成带名称的 chip（没有 url 也不被过滤掉）', () => {
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'text_info', artifact_id: 'txt_1', name: '短片剧本《暴雨单》', text: '正文' },
        ])}
      />,
    );
    expect(screen.getByText('短片剧本《暴雨单》')).toBeTruthy();
  });

  it('点击 chip 按 agentMeta.artifactId 反查节点并把视口平移到它', () => {
    // 定位语义现在由 gen_*_node_info 这类 chip 承担——text_info 已改成点击
    // 弹预览（同用户气泡的内联段卡片，见下面两个用例）。
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-script', type: 'gen-image',
          x: 500, y: 600, width: 320, height: 360,
          settings: { agentMeta: { artifactId: 'gi_1' } },
          status: 'idle',
        },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'gen_image_node_info', artifact_id: 'gi_1', name: '镜头1', prompt: 'x' },
        ])}
      />,
    );
    fireEvent.click(screen.getByText('镜头1'));
    const view = useCanvasStore.getState().view;
    // centerNode 公式：view.x = (innerWidth-56)/2 - 节点中心x * zoom。
    expect(view.x).toBe((window.innerWidth - 56) / 2 - 660);
    expect(view.y).toBe((window.innerHeight - 56) / 2 - 780);
  });

  it('text_info chip 点击：画布上找得到节点 → 开节点展开编辑弹层（同内联段卡片）', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-script', type: 'text-node',
          x: 0, y: 0, width: 320, height: 360,
          settings: { text: '节点当前正文', agentMeta: { artifactId: 'txt_1' } },
          status: 'succeeded',
        },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'text_info', artifact_id: 'txt_1', name: '剧本', text: '发送时快照' },
        ])}
      />,
    );
    fireEvent.click(screen.getByText('剧本'));
    // TextExpandDialog 展示的是节点当前内容，不是 artifact 快照。
    expect(screen.getByText('节点当前正文')).toBeTruthy();
  });

  it('text_info chip 点击：节点已删 → 用 artifact.text 快照开 ChatTextSectionDialog', () => {
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'text_info', artifact_id: 'txt_gone', name: '剧本', text: '快照正文' },
        ])}
      />,
    );
    fireEvent.click(screen.getByText('剧本'));
    expect(screen.getByText('快照正文')).toBeTruthy();
  });

  it('chip 显示节点名而不是 id——一屏 n-4AUPZAsL 认不出改的是哪一段', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-4AUPZAsL', type: 'gen-video',
          x: 0, y: 0, width: 400, height: 500,
          settings: { name: '第2段·恶行快闪' },
          status: 'idle',
        },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'node_update_info', artifact_id: 'op_n', name: 'n-4AUPZAsL', payload: { node_id: 'n-4AUPZAsL', settings: { modelId: 'm' } } },
        ])}
      />,
    );
    expect(screen.getByText('已修改节点 第2段·恶行快闪')).toBeTruthy();
  });

  it('节点没设名称时退回 id', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 'n-noname', type: 'gen-video', x: 0, y: 0, width: 400, height: 500, settings: {}, status: 'idle' },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'node_update_info', artifact_id: 'op_n2', name: 'n-noname', payload: { node_id: 'n-noname', settings: {} } },
        ])}
      />,
    );
    expect(screen.getByText('已修改节点 n-noname')).toBeTruthy();
  });

  it('名字只是空白串时也退回 id', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 'n-blank', type: 'gen-video', x: 0, y: 0, width: 400, height: 500, settings: { name: '   ' }, status: 'idle' },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'node_update_info', artifact_id: 'op_n3', name: 'n-blank', payload: { node_id: 'n-blank', settings: {} } },
        ])}
      />,
    );
    expect(screen.getByText('已修改节点 n-blank')).toBeTruthy();
  });

  it('连线 chip 两端也显示名称', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 'n-a', type: 'input-image', x: 0, y: 0, width: 260, height: 260, settings: { name: '参考图' }, status: 'idle' },
        { id: 'n-b', type: 'gen-video', x: 0, y: 0, width: 400, height: 500, settings: { name: '第1段' }, status: 'idle' },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'connection_create_info', artifact_id: 'op_c', name: 'n-a → n-b', payload: { from: 'n-a', to: 'n-b' } },
        ])}
      />,
    );
    expect(screen.getByText('连线 参考图 → 第1段')).toBeTruthy();
  });

  it('node_create_info / node_update_info 渲染可点定位 chip；update 直接按 payload.node_id 定位', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-target', type: 'gen-image',
          x: 500, y: 600, width: 320, height: 360,
          settings: {},
          status: 'idle',
        },
      ],
    });
    render(
      <MessageBubble
        msg={bubbleMsg([
          // 后端 create 的 name 填的是节点类型，payload 是内嵌 JSON 对象（非字符串）。
          { artifact_type: 'node_create_info', artifact_id: 'op_1', name: 'gen-image', payload: { type: 'gen-image', settings: {} } },
          { artifact_type: 'node_update_info', artifact_id: 'op_2', name: 'n-target', payload: { node_id: 'n-target', settings: { prompt: 'x' } } },
        ])}
      />,
    );
    expect(screen.getByText('已创建 gen-image 节点')).toBeTruthy();

    fireEvent.click(screen.getByText('已修改节点 n-target'));
    const view = useCanvasStore.getState().view;
    // centerNode 公式同上：按 payload.node_id 找到节点并把视口平移到它。
    expect(view.x).toBe((window.innerWidth - 56) / 2 - 660);
    expect(view.y).toBe((window.innerHeight - 56) / 2 - 780);
  });

  it('node_delete_info / connection_* 渲染成静态说明 chip，不可点击', () => {
    render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'node_delete_info', artifact_id: 'op_3', name: 'n-dead', payload: { node_id: 'n-dead' } },
          { artifact_type: 'connection_create_info', artifact_id: 'op_4', payload: { from: 'n-a', to: 'n-b' } },
          { artifact_type: 'connection_delete_info', artifact_id: 'op_5', payload: { from: 'n-a', to: 'n-b' } },
        ])}
      />,
    );
    expect(screen.getByText('已删除节点 n-dead')).toBeTruthy();
    const wire = screen.getByText('连线 n-a → n-b');
    // 删除/断线的对象已不在画布上，没有可定位目标——不是按钮。
    expect(wire.closest('button')).toBeNull();
    expect(screen.getByText('断开 n-a → n-b')).toBeTruthy();
  });

  it('只含画布操作 artifact（无 url）的消息不被 hasVisible 的 url 过滤吞掉', () => {
    const { container } = render(
      <MessageBubble
        msg={bubbleMsg([
          { artifact_type: 'node_delete_info', artifact_id: 'op_6', name: 'n-x', payload: { node_id: 'n-x' } },
        ])}
      />,
    );
    expect(container.firstChild).not.toBeNull();
    expect(screen.getByText('已删除节点 n-x')).toBeTruthy();
  });

  it('用户消息里 video/audio block 不再当 <img> 渲染', () => {
    const msg: AgentMessage = {
      id: 'm2', thread_id: 't1', action_id: 'a2', role: 'user',
      blocks: [
        { type: 'text', text: '看看这两个' },
        { type: 'video', url: 'https://cdn.test/v.mp4', name: '片段' },
        { type: 'audio', url: 'https://cdn.test/a.mp3', name: '配音' },
      ],
    };
    const { container } = render(<MessageBubble msg={msg} />);
    expect(container.querySelector('video')).not.toBeNull();
    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('配音')).toBeTruthy();
  });
});

describe('tool_use 的工具名显示', () => {
  const toolUseMsg = (block: object): AgentMessage => ({
    id: 'm2',
    thread_id: 't1',
    action_id: 'a1',
    role: 'assistant',
    blocks: [block as never],
  });

  it('优先显示 display_name（后台配置的面向用户名称）', () => {
    render(
      <MessageBubble
        msg={toolUseMsg({ type: 'tool_use', id: 'c1', name: 'generate_image', display_name: '生成图片' })}
      />,
    );
    expect(screen.getByText('调用 生成图片')).toBeTruthy();
    expect(screen.queryByText('调用 generate_image')).toBeNull();
  });

  it('没有 display_name 的旧消息回退到内部名', () => {
    render(<MessageBubble msg={toolUseMsg({ type: 'tool_use', id: 'c2', name: 'generate_image' })} />);
    expect(screen.getByText('调用 generate_image')).toBeTruthy();
  });
});

describe('skill chip 的自动触发标记', () => {
  function bubble(blocks: AgentMessage['blocks']) {
    return render(
      <MessageBubble msg={{ id: 'm1', role: 'user', blocks } as AgentMessage} />,
    );
  }

  // 用户没点过任何东西，气泡上却凭空出现一个 chip——不标出来他只会困惑
  // "这标签哪来的"。
  it('自动挂载的技能 chip 标出"自动"', () => {
    const { container } = bubble([{ type: 'skill', id: 'writer', name: '编剧大师', auto: true }]);
    expect(container.textContent).toContain('编剧大师');
    expect(container.textContent).toContain('自动');
  });

  it('手动选的技能 chip 不带"自动"', () => {
    const { container } = bubble([{ type: 'skill', id: 'writer', name: '编剧大师' }]);
    expect(container.textContent).toContain('编剧大师');
    expect(container.textContent).not.toContain('自动');
  });
});

describe('操作 chip 的定格名兜底（有名显示名，没名才显示 id）', () => {
  const opMsg = (artifacts: object[]): AgentMessage => ({
    id: 'm2',
    thread_id: 't1',
    action_id: 'a1',
    role: 'assistant',
    blocks: [{ type: 'tool_event', name: 'delete_node', artifacts } as never],
  });

  it('删除 chip：节点已不在画布，用 payload 定格名而不是 id', () => {
    useCanvasStore.setState({ nodes: [] });
    render(
      <MessageBubble
        msg={opMsg([
          {
            artifact_type: 'node_delete_info', artifact_id: 'd1',
            name: 'n-FyTFDnNY',
            payload: { node_id: 'n-FyTFDnNY', node_name: '第一章剧本' },
          },
        ])}
      />,
    );
    expect(screen.getByText('已删除节点 第一章剧本')).toBeTruthy();
  });

  it('删除 chip：存量老消息没有定格名，回退 id', () => {
    useCanvasStore.setState({ nodes: [] });
    render(
      <MessageBubble
        msg={opMsg([
          { artifact_type: 'node_delete_info', artifact_id: 'd2', name: 'n-old', payload: { node_id: 'n-old' } },
        ])}
      />,
    );
    expect(screen.getByText('已删除节点 n-old')).toBeTruthy();
  });

  it('连线 chip：payload 定格 from_name/to_name，没名的一端回退 id', () => {
    useCanvasStore.setState({ nodes: [] });
    render(
      <MessageBubble
        msg={opMsg([
          {
            artifact_type: 'connection_create_info', artifact_id: 'c1',
            name: 'n-a → n-b',
            payload: { from: 'n-a', to: 'n-b', from_name: '第一章剧本', to_name: '' },
          },
        ])}
      />,
    );
    expect(screen.getByText('连线 第一章剧本 → n-b')).toBeTruthy();
  });

  it('画布现名优先于定格名——节点还活着且改过名时显示最新名', () => {
    useCanvasStore.setState({
      nodes: [
        {
          id: 'n-live', type: 'text-node', x: 0, y: 0, width: 100, height: 100,
          settings: { name: '改名后的新名字' }, status: 'succeeded',
        },
      ],
    });
    render(
      <MessageBubble
        msg={opMsg([
          {
            artifact_type: 'node_update_info', artifact_id: 'u1',
            name: 'n-live',
            payload: { node_id: 'n-live', node_name: '执行时的旧名字' },
          },
        ])}
      />,
    );
    expect(screen.getByText('已修改节点 改名后的新名字')).toBeTruthy();
  });
});
