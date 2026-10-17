import { beforeEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useCanvasStore } from '../../store';
import { AssetsPanel } from './AssetsPanel';

// AddAssetDialog（新增弹窗）内部有 useQuery，AssetsPanel 的渲染需要包一层 Provider。
function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queryClient.setQueryData(['runtime-models'], []);
  return render(
    <QueryClientProvider client={queryClient}>
      <AssetsPanel nodeId="s1" onClose={() => {}} />
    </QueryClientProvider>,
  );
}

describe('AssetsPanel（skill 对齐版）', () => {
  beforeEach(() => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: {
          rows: [{ id: 'r1', shotNumber: 1, name: '', duration: 5, colorMark: '', prompt: '镜头1：@[角色-林芽]出场。' }],
          assets: { characters: [{ id: 'c1', name: '林芽', description: '设定词', linkedNodeId: '' }], scenes: [], props: [] },
        } }],
      connections: [], readOnly: false,
    });
  });

  it('渲染三分区（物品替代道具）与未设定占位', () => {
    renderPanel();
    for (const t of ['角色', '场景', '物品']) expect(screen.getByText(t)).toBeInTheDocument();
    expect(screen.queryByText('道具')).toBeNull();
    expect(screen.getByText('生成或上传图')).toBeInTheDocument();
  });

  it('点新增卡打开三 Tab 配图弹窗', () => {
    renderPanel();
    fireEvent.click(screen.getByTestId('asset-add-characters'));
    expect(screen.getByTestId('add-asset-dialog')).toBeInTheDocument();
    for (const t of ['AI生成', '从当前画布选择', '本地上传']) {
      expect(screen.getByText(t)).toBeInTheDocument();
    }
  });

  it('编辑改名撞已有名字被拒绝', () => {
    useCanvasStore.setState({
      nodes: [{ id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
        settings: {
          rows: [],
          assets: { characters: [
            { id: 'c1', name: '林芽', description: '', linkedNodeId: '' },
            { id: 'c2', name: '王大爷', description: '', linkedNodeId: '' },
          ], scenes: [], props: [] },
        } }],
      connections: [], readOnly: false,
    });
    renderPanel();
    fireEvent.click(screen.getAllByText('生成或上传图')[1]); // 打开王大爷的编辑抽屉
    const input = screen.getByTestId('asset-name-input');
    fireEvent.change(input, { target: { value: '林芽' } });
    fireEvent.blur(input);
    expect(screen.getByText('同类型下已有同名资产')).toBeInTheDocument();
    // 校验失败不写回
    const chars = (useCanvasStore.getState().nodes[0].settings! as { assets: { characters: { name: string }[] } }).assets.characters;
    expect(chars.map((a) => a.name)).toEqual(['林芽', '王大爷']);
  });

  it('改名全表联动：行 prompt 里的 token 一起换（抽屉失焦自动保存）', () => {
    renderPanel();
    fireEvent.click(screen.getByText('生成或上传图'));
    expect(screen.getByTestId('asset-edit-drawer')).toBeInTheDocument();
    const input = screen.getByTestId('asset-name-input');
    fireEvent.change(input, { target: { value: '林小芽' } });
    fireEvent.blur(input);
    const s = useCanvasStore.getState().nodes[0].settings! as {
      assets: { characters: { name: string }[] };
      rows: { prompt: string }[];
    };
    expect(s.assets.characters[0].name).toBe('林小芽');
    expect(s.rows[0].prompt).toBe('镜头1：@[角色-林小芽]出场。');
  });

  it('绑定的生图节点在生成中/失败时卡片透出状态', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
          settings: { rows: [], assets: { characters: [
            { id: 'c1', name: 'A', description: '', linkedNodeId: 'g1' },
            { id: 'c2', name: 'B', description: '', linkedNodeId: 'g2' },
          ], scenes: [], props: [] } } },
        { id: 'g1', type: 'gen-image', x: 0, y: 0, width: 200, height: 200, status: 'generating' },
        { id: 'g2', type: 'gen-image', x: 0, y: 0, width: 200, height: 200, status: 'failed', error: '积分不足' },
      ],
      connections: [], readOnly: false,
    });
    renderPanel();
    expect(screen.getByTestId('asset-generating-c1')).toHaveTextContent('AI 生成中…');
    expect(screen.getByText('生成失败：积分不足')).toBeInTheDocument();
  });

  it('AI 生角色：弹「选择图片」定位 AI生成 tab 并预填描述词，不直接建节点', () => {
    renderPanel();
    fireEvent.click(screen.getByTestId('asset-menu-c1'));
    fireEvent.click(screen.getByText('AI 生角色'));
    // 不再静默建 gen-image——生成参数(模型/画质/比例)得让用户在弹框里确认
    expect(useCanvasStore.getState().nodes.some((n) => n.type === 'gen-image')).toBe(false);
    expect(screen.getByTestId('add-asset-dialog')).toBeInTheDocument();
    const prompt = screen.getByTestId('asset-gen-prompt') as HTMLTextAreaElement;
    expect(prompt.value).toBe('设定词');
  });

  it('清除图片摘掉旧连线；共享引用的节点不摘', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
          settings: {
            rows: [],
            assets: { characters: [
              { id: 'c1', name: 'A', description: '', linkedNodeId: 'img1' },
              { id: 'c2', name: 'B', description: '', linkedNodeId: 'img1' }, // 共享同一图节点
              { id: 'c3', name: 'C', description: '', linkedNodeId: 'img2' },
            ], scenes: [], props: [] },
          } },
        { id: 'img1', type: 'input-image', x: 0, y: 0, width: 200, height: 200, content: 'https://x/1.png' },
        { id: 'img2', type: 'input-image', x: 0, y: 300, width: 200, height: 200, content: 'https://x/2.png' },
      ],
      connections: [
        { id: 'e1', from: 'img1', to: 's1' },
        { id: 'e2', from: 'img2', to: 's1' },
      ],
      readOnly: false,
    });
    renderPanel();
    // C 独占 img2：清除后连线应被摘掉
    fireEvent.click(screen.getByTestId('asset-menu-c3'));
    fireEvent.click(screen.getByText('清除图片'));
    let conns = useCanvasStore.getState().connections;
    expect(conns.some((c) => c.id === 'e2')).toBe(false);
    // A 与 B 共享 img1：清除 A 后连线必须保留（B 还在用）
    fireEvent.click(screen.getByTestId('asset-menu-c1'));
    fireEvent.click(screen.getAllByText('清除图片')[0]);
    conns = useCanvasStore.getState().connections;
    expect(conns.some((c) => c.id === 'e1')).toBe(true);
  });

  it('替换图片：走选择弹窗（标题带资产名/当前图置灰），换绑后摘旧连线', () => {
    useCanvasStore.setState({
      nodes: [
        { id: 's1', type: 'script', x: 0, y: 0, width: 300, height: 220,
          settings: {
            rows: [],
            assets: { characters: [{ id: 'c1', name: '角色 2', description: '', linkedNodeId: 'imgOld' }], scenes: [], props: [] },
          } },
        { id: 'imgOld', type: 'input-image', x: 0, y: 0, width: 200, height: 200, content: 'https://x/old.png' },
        { id: 'imgNew', type: 'input-image', x: 0, y: 300, width: 200, height: 200, content: 'https://x/new.png' },
      ],
      connections: [{ id: 'e1', from: 'imgOld', to: 's1' }],
      readOnly: false,
    });
    renderPanel();
    fireEvent.click(screen.getByTestId('asset-menu-c1'));
    fireEvent.click(screen.getByText('替换图片'));
    expect(screen.getByText('选择图片（角色 2）')).toBeInTheDocument();
    // 当前图置灰不可选
    expect(screen.getByTestId('pick-node-imgOld')).toBeDisabled();
    expect(screen.getByText('当前')).toBeInTheDocument();
    // 选新图 → 换绑 + 旧连线摘除 + 新连线建立
    fireEvent.click(screen.getByTestId('pick-node-imgNew'));
    const st = useCanvasStore.getState();
    const chars = (st.nodes.find((n) => n.id === 's1')!.settings! as { assets: { characters: { linkedNodeId: string }[] } }).assets.characters;
    expect(chars[0].linkedNodeId).toBe('imgNew');
    expect(st.connections.some((c) => c.from === 'imgOld' && c.to === 's1')).toBe(false);
    expect(st.connections.some((c) => c.from === 'imgNew' && c.to === 's1')).toBe(true);
  });

  it('删除资产只删条目，行 prompt 的 token 保留（变断链由红点提示）', () => {
    renderPanel();
    fireEvent.click(screen.getByTestId('asset-menu-c1'));
    fireEvent.click(screen.getByText('删除'));
    const s = useCanvasStore.getState().nodes[0].settings! as {
      assets: { characters: unknown[] };
      rows: { prompt: string }[];
    };
    expect(s.assets.characters).toHaveLength(0);
    expect(s.rows[0].prompt).toBe('镜头1：@[角色-林芽]出场。');
  });
});
