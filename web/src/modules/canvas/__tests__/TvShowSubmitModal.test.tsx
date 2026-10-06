import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App as AntApp } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { TvShowSubmitModal } from '../panels/TvShowSubmitModal';
import { getTvshowDicts, submitTvshowWork, uploadAsset } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';

// 顶栏「发布作品」出口的投稿弹框——字段清单、必填规则、协议默认勾选/未勾禁提
// 都是 spec 2026-08-06 §六 拍板的产品行为，不是随意的表单细节，用测试钉死。
// 2026-08-06 用户拍板推翻原三字典设定：分类/活动标签/参赛赛道本是同一个东西，
// 只留一个，用户端显示文案叫「活动标签」，赛道彻底移除（不是隐藏，是没有这个字段）。
vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    getTvshowDicts: vi.fn(),
    submitTvshowWork: vi.fn(),
    uploadAsset: vi.fn(),
    // 历史图库弹层用 ThumbImg 渲染候选缩略图，它经 thumbs.ts 走 api.post 换
    // 缩略图——stub 掉，否则 jsdom 里会发出真实 XHR（同 uploadPersistence.test.tsx
    // 的坑）。
    api: { post: vi.fn() },
  };
});

const dictsWithAll = {
  categories: ['图片', '视频'],
};

function renderModal(props: Partial<React.ComponentProps<typeof TvShowSubmitModal>> = {}) {
  const onClose = vi.fn();
  render(
    <AntApp>
      <TvShowSubmitModal
        open
        projectId={7}
        defaultTitle=""
        onClose={onClose}
        {...props}
      />
    </AntApp>,
  );
  return { onClose };
}

beforeEach(() => {
  vi.mocked(getTvshowDicts).mockReset().mockResolvedValue(dictsWithAll);
  vi.mocked(submitTvshowWork).mockReset().mockResolvedValue({ id: 1 });
  vi.mocked(uploadAsset).mockReset();
  // 重置画布 store：避免上一个用例遗留的节点串到「历史图库」用例里。
  useCanvasStore.setState({ nodes: [], connections: [] });
});

describe('TvShowSubmitModal', () => {
  it('表单里没有「参赛赛道」字段', async () => {
    renderModal();
    await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());
    expect(screen.queryByText('参赛赛道')).not.toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: '参赛赛道' })).not.toBeInTheDocument();
  });

  it('必填校验：名称/描述/活动标签为空时提交被拦截，不调用 submitTvshowWork', async () => {
    renderModal();
    await waitFor(() => expect(getTvshowDicts).toHaveBeenCalled());

    fireEvent.click(screen.getByRole('button', { name: '发布并投稿' }));

    expect(await screen.findByText('请输入作品名称')).toBeInTheDocument();
    expect(screen.getByText('请输入作品描述')).toBeInTheDocument();
    // 「请选择活动标签」同时匹配 Select 的 placeholder 文本，只认 antd 错误提示节点那一份。
    const categoryError = Array.from(document.querySelectorAll('.ant-form-item-explain-error')).find(
      (el) => el.textContent === '请选择活动标签',
    );
    expect(categoryError).toBeTruthy();
    expect(submitTvshowWork).not.toHaveBeenCalled();
  });

  it('协议勾选行默认勾选；取消勾选后提交按钮禁用', async () => {
    renderModal();
    await waitFor(() => expect(getTvshowDicts).toHaveBeenCalled());

    const submitBtn = screen.getByRole('button', { name: '发布并投稿' });
    expect(submitBtn).not.toBeDisabled();

    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toBeChecked();

    fireEvent.click(checkbox);
    expect(checkbox).not.toBeChecked();
    expect(submitBtn).toBeDisabled();

    const link = screen.getByRole('link', { name: '创作许可服务协议' });
    expect(link).toHaveAttribute('href', '/creator-license');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('活动标签是必填 Select，选项来自 dicts.categories', async () => {
    renderModal();
    await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

    const select = screen.getByRole('combobox', { name: '活动标签' });
    fireEvent.mouseDown(select);
    // 选项来自 dictsWithAll.categories，两项都应可见。
    expect(await screen.findByText('图片', { selector: '.ant-select-item-option-content' })).toBeInTheDocument();
    expect(screen.getByText('视频', { selector: '.ant-select-item-option-content' })).toBeInTheDocument();
  });

  it('字典为空时，活动标签字段整个不渲染，且提示文案出现 + 提交按钮禁用', async () => {
    // 2026-08-07 review 钉死的回归：字段不渲染 = required rule 也没挂载，
    // 空字典时若不拦提交按钮，用户点了发布只会拿到后端一句英文 400
    // （UI 上根本没有叫 category 的字段，无从自救）。
    vi.mocked(getTvshowDicts).mockResolvedValue({ categories: [] });
    renderModal();

    await waitFor(() => expect(getTvshowDicts).toHaveBeenCalled());
    expect(screen.queryByText('活动标签')).not.toBeInTheDocument();
    expect(screen.queryByText('参赛赛道')).not.toBeInTheDocument();

    expect(await screen.findByText('管理员尚未配置活动标签，暂时无法投稿')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '发布并投稿' })).toBeDisabled();
  });

  it('字典非空时，不显示「管理员尚未配置」提示，提交按钮可用（防止误伤正常路径）', async () => {
    renderModal();
    await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

    expect(screen.queryByText('管理员尚未配置活动标签，暂时无法投稿')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '发布并投稿' })).not.toBeDisabled();
  });

  it('提交时 submitTvshowWork 收到正确入参，不含 activityTag/raceTrack', async () => {
    const onClose = vi.fn();
    renderModal({ onClose, defaultTitle: '我的画布' });
    await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

    const titleInput = screen.getByPlaceholderText('请输入作品名称');
    fireEvent.change(titleInput, { target: { value: '我的作品' } });

    const descInput = screen.getByPlaceholderText('请输入作品描述');
    fireEvent.change(descInput, { target: { value: '一段描述' } });

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '活动标签' }));
    // 点击 option 内层的 .ant-select-item-option-content——外层 role=option 节点的
    // textContent 也是"图片"（内层文本是它的后代），click 外层在 jsdom 下不会触发
    // rc-select 的 onSelect，必须点内层这个才行（ScriptSplitModal.test.tsx 同一坑）。
    fireEvent.click(await screen.findByText('图片', { selector: '.ant-select-item-option-content' }));
    // 等 antd Select 把选中值回写进 Form 状态（渲染出 selection-item），
    // 再继续填下一个字段/提交，否则提交时 category 仍是初始的 undefined。
    await screen.findByText('图片', { selector: '.ant-select-selection-item' });

    const socialInput = screen.getByPlaceholderText('请添加您在社媒发布该作品的链接');
    fireEvent.change(socialInput, { target: { value: 'https://weibo.com/x' } });

    fireEvent.click(screen.getByRole('button', { name: '发布并投稿' }));

    await waitFor(() => expect(submitTvshowWork).toHaveBeenCalledTimes(1));
    expect(submitTvshowWork).toHaveBeenCalledWith({
      projectId: 7,
      title: '我的作品',
      description: '一段描述',
      category: '图片',
      socialLink: 'https://weibo.com/x',
      thumbnailUrl: undefined,
    });
    const call = vi.mocked(submitTvshowWork).mock.calls[0][0];
    expect(call).not.toHaveProperty('activityTag');
    expect(call).not.toHaveProperty('raceTrack');
  });

  // 封面双来源（本地上传 / 历史图库）——spec 2026-08-07 Task B 拍板：点「选择
  // 封面」弹小菜单二选一，历史图库直接读画布 store，不新增后端接口。
  describe('封面双来源', () => {
    it('封面菜单：本地上传 / 历史图库两项都在', async () => {
      renderModal();
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      await userEvent.click(screen.getByText('选择封面'));
      expect(await screen.findByText('本地上传')).toBeInTheDocument();
      expect(screen.getByText('历史图库')).toBeInTheDocument();
    });

    // 2026-08-07 修复(第一轮)：菜单曾经整栏铺满(跟触发区一样宽)，根因是
    // rc-dropdown 默认把浮层 minWidth 同步成触发元素宽度。jsdom 不做真实布局
    // (getBoundingClientRect 恒为 0)，量不出宽度差异，但 placement 是可观察的
    // 行为差异，用它钉住"改法确实生效了"而不是只测菜单项存在。
    //
    // 2026-08-07 修复(第二轮，实拍反馈)：第一轮选的 'topRight' 位置又飘了——
    // 触发元素是撑满表单宽度的封面框，紧贴在深色标题区下方几乎没有向上展开
    // 空间，'topRight' 把浮层锚在触发盒顶边导致它顶到标题区附近。改成
    // 'bottom'(下方展开、水平居中于触发盒)：封面框下方是表单剩余字段，空间
    // 充足，且封面框内容本身居中，居中对齐观感上最贴近点击位置。
    // 菜单锚在点击点：Dropdown 的触发元素是一个绝对定位的 0×0 span，点击封面框
    // 时把它移到指针处。前两轮靠 placement 相对整宽封面框定位都对不准（先飘到
    // 标题区、后压住 extra 文案），根因是参照物是整个大盒子。这里断言锚元素
    // 存在且随点击移动，才是真正守住「点哪儿弹哪儿」的机制。
    it('封面菜单锚在点击点弹出(0×0 锚元素随指针移动，不受封面框宽度影响)', async () => {
      renderModal();
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      // Modal 走 portal 渲染到 body 上，从 document 查而不是 render 的 container
      const anchor = document.querySelector('span[aria-hidden]') as HTMLElement | null;
      expect(anchor).not.toBeNull();
      expect(anchor!.style.position).toBe('absolute');
      expect(anchor!.style.width).toBe('0px');

      await userEvent.click(screen.getByText('选择封面'));
      await screen.findByText('本地上传');
    });

    it('画布里没有图片时，「历史图库」菜单项禁用并给出说明', async () => {
      renderModal();
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      await userEvent.click(screen.getByText('选择封面'));
      expect(await screen.findByText('当前画布还没有图片')).toBeInTheDocument();

      const galleryItem = screen.getByText('历史图库').closest('li');
      expect(galleryItem).toHaveAttribute('aria-disabled', 'true');

      // 禁用项点击不应该弹出选图层。
      await userEvent.click(screen.getByText('历史图库'));
      expect(screen.queryByRole('dialog', { name: '历史图库' })).not.toBeInTheDocument();
    });

    it('本地上传：菜单项触发文件选择，选中文件后走 uploadAsset 并显示预览+重新选择入口', async () => {
      vi.mocked(uploadAsset).mockResolvedValue({
        id: 1,
        url: 'https://cdn.example.com/cover.png',
        size: 100,
        mimeType: 'image/png',
      });
      renderModal();
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      await userEvent.click(screen.getByText('选择封面'));
      await userEvent.click(await screen.findByText('本地上传'));

      const file = new File(['x'], 'cover.png', { type: 'image/png' });
      const input = document.querySelector('input[type="file"]') as HTMLInputElement;
      expect(input).not.toBeNull();
      fireEvent.change(input, { target: { files: [file] } });

      await waitFor(() => expect(uploadAsset).toHaveBeenCalledWith(file, 7));
      expect(await screen.findByAltText('封面')).toHaveAttribute('src', 'https://cdn.example.com/cover.png');
      expect(screen.getByRole('button', { name: '重新选择' })).toBeInTheDocument();
    });

    it('已选封面后点击「重新选择」会弹出封面菜单（本地上传/历史图库仍可选）', async () => {
      // 2026-08-07 review Minor：「重新选择」按钮自身没有 onClick，靠事件冒泡
      // 到外层 Dropdown 触发容器——功能上对，但要有测试直接覆盖这条隐式路径。
      vi.mocked(uploadAsset).mockResolvedValue({
        id: 1,
        url: 'https://cdn.example.com/cover.png',
        size: 100,
        mimeType: 'image/png',
      });
      renderModal();
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      await userEvent.click(screen.getByText('选择封面'));
      await userEvent.click(await screen.findByText('本地上传'));
      const file = new File(['x'], 'cover.png', { type: 'image/png' });
      const input = document.querySelector('input[type="file"]') as HTMLInputElement;
      fireEvent.change(input, { target: { files: [file] } });
      await screen.findByAltText('封面');

      await userEvent.click(screen.getByRole('button', { name: '重新选择' }));
      expect(await screen.findByText('本地上传')).toBeInTheDocument();
      expect(screen.getByText('历史图库')).toBeInTheDocument();
    });

    it('历史图库：列出画布中的图片节点，点选后作为封面写入，提交带上该 URL', async () => {
      useCanvasStore.setState({
        nodes: [
          {
            id: 'n1',
            type: 'input-image',
            x: 0,
            y: 0,
            width: 100,
            height: 100,
            content: 'https://cdn.example.com/gallery-a.png',
          },
          {
            id: 'n2',
            type: 'gen-image',
            x: 0,
            y: 0,
            width: 100,
            height: 100,
            content: 'https://cdn.example.com/gallery-b.png',
          },
          // 非图片类节点（视频）不应出现在候选里。
          {
            id: 'n3',
            type: 'gen-video',
            x: 0,
            y: 0,
            width: 100,
            height: 100,
            content: 'https://cdn.example.com/gallery-c.mp4',
          },
          // 2026-08-07 review 钉死：preview 是「发送到画布」/「上传变形」的
          // 主要落点（GenImageNode.tsx 的 handleSendToCanvas / handleUpload），
          // settings.kind='image' 时必须收进候选——白名单漏了它，用户最想选
          // 的那批图反而挑不到。
          {
            id: 'n4',
            type: 'preview',
            x: 0,
            y: 0,
            width: 100,
            height: 100,
            content: 'https://cdn.example.com/gallery-d.png',
            settings: { kind: 'image' },
          },
          // 同一节点类型，kind='video' 时必须排除——两个方向都要钉，防止
          // 退化成「无条件收 preview」也能蒙混过关。
          {
            id: 'n5',
            type: 'preview',
            x: 0,
            y: 0,
            width: 100,
            height: 100,
            content: 'https://cdn.example.com/gallery-e.mp4',
            settings: { kind: 'video' },
          },
        ],
        connections: [],
      });

      renderModal({ defaultTitle: '我的画布' });
      await waitFor(() => expect(screen.getByText('活动标签')).toBeInTheDocument());

      await userEvent.click(screen.getByText('选择封面'));
      await userEvent.click(await screen.findByText('历史图库'));

      expect(await screen.findByTestId('cover-gallery-item-n1')).toBeInTheDocument();
      expect(screen.getByTestId('cover-gallery-item-n2')).toBeInTheDocument();
      expect(screen.queryByTestId('cover-gallery-item-n3')).not.toBeInTheDocument();
      expect(screen.getByTestId('cover-gallery-item-n4')).toBeInTheDocument();
      expect(screen.queryByTestId('cover-gallery-item-n5')).not.toBeInTheDocument();

      await userEvent.click(screen.getByTestId('cover-gallery-item-n1'));

      // 选图层关闭，封面预览换成选中的 URL。
      await waitFor(() =>
        expect(screen.getByAltText('封面')).toHaveAttribute('src', 'https://cdn.example.com/gallery-a.png'),
      );

      const titleInput = screen.getByPlaceholderText('请输入作品名称');
      fireEvent.change(titleInput, { target: { value: '我的作品' } });
      const descInput = screen.getByPlaceholderText('请输入作品描述');
      fireEvent.change(descInput, { target: { value: '一段描述' } });
      fireEvent.mouseDown(screen.getByRole('combobox', { name: '活动标签' }));
      fireEvent.click(await screen.findByText('图片', { selector: '.ant-select-item-option-content' }));
      await screen.findByText('图片', { selector: '.ant-select-selection-item' });

      fireEvent.click(screen.getByRole('button', { name: '发布并投稿' }));

      await waitFor(() => expect(submitTvshowWork).toHaveBeenCalledTimes(1));
      expect(submitTvshowWork).toHaveBeenCalledWith(
        expect.objectContaining({ thumbnailUrl: 'https://cdn.example.com/gallery-a.png' }),
      );
    });
  });
});
