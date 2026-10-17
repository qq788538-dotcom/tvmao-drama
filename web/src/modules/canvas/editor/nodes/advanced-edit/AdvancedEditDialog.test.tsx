import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AdvancedEditDialog } from './AdvancedEditDialog';

// 落一笔定位点并「添加至输入框」——确认交互引入后，产生一条记录的最短路径。
// 各用例共用，避免每处重复五行。
function annotateOnePin(container: HTMLElement) {
  fireEvent.click(screen.getByRole('button', { name: '定位点' }));
  const overlay = container.querySelector('canvas')!;
  fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
  fireEvent.click(screen.getByRole('button', { name: '添加至输入框' }));
}

describe('AdvancedEditDialog', () => {
  it('渲染工具栏与保存按钮，初始无记录时保存禁用', () => {
    render(<AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByText('高级编辑')).toBeInTheDocument();
    for (const name of ['矩形', '画笔', '箭头', '文字', '橡皮', '定位点', '撤销', '重做']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument();
    }
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled();
  });

  it('点关闭触发 onClose', async () => {
    const onClose = vi.fn();
    render(<AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={onClose} />);
    screen.getByRole('button', { name: '关闭' }).click();
    expect(onClose).toHaveBeenCalled();
  });

  // 工具门控：默认无工具选中，标注层对指针透明——
  // 视频可直接点击播放/拖进度条；选中工具才拦截；再点同一工具取消选中。
  it('默认无工具选中：overlay 不拦截指针；选中工具拦截；再点取消选中', () => {
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />,
    );
    const overlay = container.querySelector('canvas')!;
    expect(overlay.style.pointerEvents).toBe('none');
    fireEvent.click(screen.getByRole('button', { name: '画笔' }));
    expect(overlay.style.pointerEvents).toBe('auto');
    fireEvent.click(screen.getByRole('button', { name: '画笔' }));
    expect(overlay.style.pointerEvents).toBe('none');
  });

  // 点工具即暂停播放：选工具 = 要对当前帧标注了，
  // 画面继续跑的话落笔对不上用户看中的帧。撤销/重做同理。
  it('播放中点工具/撤销/重做都会暂停视频', () => {
    const pause = vi
      .spyOn(HTMLMediaElement.prototype, 'pause')
      .mockImplementation(() => {});
    try {
      render(<AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />);
      fireEvent.click(screen.getByRole('button', { name: '画笔' }));
      expect(pause).toHaveBeenCalledTimes(1);
      // 撤销/重做 disabled 态点击不触发 onClick，先落一笔让撤销可用
      fireEvent.click(screen.getByRole('button', { name: '定位点' }));
      const overlay = document.querySelector('canvas')!;
      fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
      pause.mockClear(); // 落笔本身也有既有的自动暂停，清零只看撤销/重做
      fireEvent.click(screen.getByRole('button', { name: '撤销' }));
      expect(pause).toHaveBeenCalledTimes(1);
      fireEvent.click(screen.getByRole('button', { name: '重做' }));
      expect(pause).toHaveBeenCalledTimes(2);
    } finally {
      pause.mockRestore();
    }
  });

  // 确认交互：落笔进待确认缓冲，出现「取消/添加至输入框」；取消整批丢弃且
  // 退出工具态；确认才生成记录。未确认期间保存被禁（防止把没确认的标注漏掉）。
  it('落笔出确认操作区；取消丢弃且退出工具态；确认生成记录', () => {
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />,
    );
    const overlay = container.querySelector('canvas')!;
    fireEvent.click(screen.getByRole('button', { name: '定位点' }));
    fireEvent.pointerDown(overlay, { clientX: 10, clientY: 10 });
    expect(screen.getByRole('button', { name: '添加至输入框' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.queryByRole('button', { name: '添加至输入框' })).toBeNull();
    expect(overlay.style.pointerEvents).toBe('none'); // 取消同时退出工具态
    expect(screen.queryByPlaceholderText('说明这处标注（可选，进提示词）')).toBeNull();

    annotateOnePin(container);
    expect(screen.getByPlaceholderText('说明这处标注（可选，进提示词）')).toBeInTheDocument();
    expect(overlay.style.pointerEvents).toBe('none'); // 确认后也退出工具态
    expect(screen.getByRole('button', { name: '保存' })).not.toBeDisabled();
  });

  // 同秒多次「添加至输入框」各自成条：jsdom 里 currentTime
  // 恒为 0，天然构造出「同一秒确认两次」的场景。
  it('同一秒两次确认生成两条独立记录，说明编辑互不串', () => {
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />,
    );
    annotateOnePin(container);
    annotateOnePin(container);
    const notes = screen.getAllByPlaceholderText('说明这处标注（可选，进提示词）');
    expect(notes).toHaveLength(2);
    fireEvent.change(notes[0], { target: { value: '张三' } });
    expect((notes[0] as HTMLInputElement).value).toBe('张三');
    expect((notes[1] as HTMLInputElement).value).toBe(''); // 同秒的另一条不被串改
  });

  it('调色板只在选中工具时出现，共六色', () => {
    render(<AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryAllByRole('button', { name: /^颜色 / })).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: '画笔' }));
    expect(screen.getAllByRole('button', { name: /^颜色 / })).toHaveLength(6);
  });

  // crossOrigin=anonymous 下对象存储缺 CORS 头时浏览器拒载整个视频——
  // loadeddata 不触发、探测不跑，必须靠 error 事件给出可见提示并锁死工具。
  it('视频加载失败：第一次自动重试不打扰，第二次才显示错误横幅并禁用工具', () => {
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={vi.fn()} onClose={vi.fn()} />,
    );
    const video = container.querySelector('video')!;
    fireEvent.error(video);
    // 首次失败走静默重试（缓存投毒/网络抖动一次 load 通常就好），不出横幅。
    expect(screen.queryByText(/视频加载失败/)).toBeNull();
    fireEvent.error(video);
    expect(screen.getByText(/视频加载失败/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '画笔' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled();
  });

  it('captureFrame：目标时间已是当前播放位置（no-op seek）时立即抓帧返回，不永久挂起', async () => {
    // jsdom 视频没有真实解码/seek，currentTime 恒为 0——正好复现「落笔不播放，
    // 直接标注第一帧」这个最容易触发 bug 的场景：目标时间与当前播放位置相同，
    // 浏览器规范定义的 no-op seek 不会触发 seeked 事件，修复前会永久 pending。
    const toBlobSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'toBlob')
      .mockImplementation(function toBlobStub(this: HTMLCanvasElement, cb: BlobCallback) {
        cb(new Blob(['x']));
      });
    try {
      const onSave = vi.fn().mockResolvedValue({ ok: 1 });
      const { container } = render(
        <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={onSave} onClose={vi.fn()} />,
      );
      annotateOnePin(container);
      const saveBtn = screen.getByRole('button', { name: '保存' });
      expect(saveBtn).not.toBeDisabled();
      fireEvent.click(saveBtn);

      await waitFor(() => expect(onSave).toHaveBeenCalled());
      const [entries, captureFrame] = onSave.mock.calls[0];
      expect(entries).toHaveLength(1);

      // 修复前：这里会永久 pending（死等一个不会来的 seeked 事件）。
      const blob = await captureFrame(entries[0]);
      expect(blob).toBeInstanceOf(Blob);
    } finally {
      toBlobSpy.mockRestore();
    }
  });

  // onSave 契约改为返回 SaveResult 之后，弹框自己要读 failedAt/ok 决定要不要
  // 关框——这里覆盖「部分失败」分支：不关框、错误文案带上第几条失败、已成功的
  // 条目从记录条剔除（这里只有 1 条且未成功，所以剔除 0 条、记录条原样保留，
  // 用户能就地重试）。
  it('保存部分失败：不关框、显示「前 N 条已保存」文案、记录条只保留未成功部分', async () => {
    const onClose = vi.fn();
    const onSave = vi.fn().mockResolvedValue({ ok: 0, failedAt: 0, error: '上传失败' });
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={onSave} onClose={onClose} />,
    );
    annotateOnePin(container);
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(await screen.findByText('前 0 条已保存，第 1 条失败：上传失败')).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    // 唯一一条尚未成功，仍留在记录条里，不是被静默清空。
    expect(screen.getByPlaceholderText('说明这处标注（可选，进提示词）')).toBeInTheDocument();
  });

  // 全部成功才关框——这是「不再由调用方 setAdvEditOpen(false)，改由弹框自己
  // 读 SaveResult 决定」这条契约变更里容易漏掉的另一半。
  it('保存全部成功：调用 onClose 关框', async () => {
    const onClose = vi.fn();
    const onSave = vi.fn().mockResolvedValue({ ok: 1 });
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={onSave} onClose={onClose} />,
    );
    annotateOnePin(container);
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  // C2 坐标修复回归：overlay 为让出底部原生 controls 高度比 video 的显示盒矮
  // 一截（同 left/top/width，只是矮 40px 左右）。toVideoCoords 必须用 video
  // 自身的 rect 换算——如果误用 overlay（变矮）的 rect，同一个屏幕坐标会被
  // 换算成更靠下的原始像素坐标，越靠画面底部偏差越大。
  //
  // 用一组还原报告里算例的尺寸：1920×1080 视频渲染成 600×337.5，overlay 矮到
  // 297.5（差 40px）。点在屏幕 y=250 处：
  //   - 按 video 盒换算（正确）：250/337.5*1080 ≈ 800
  //   - 按 overlay 盒换算（修复前的 bug）：250/297.5*1080 ≈ 908
  // 两者相差百余像素，用宽松区间卡住回归方向，不依赖精确浮点相等。
  it('overlay 让出原生 controls 后变矮，标注坐标仍按 video 自身显示盒换算', async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: 1 });
    const { container } = render(
      <AdvancedEditDialog videoUrl="https://x/v.mp4" onSave={onSave} onClose={vi.fn()} />,
    );
    const video = container.querySelector('video')!;
    const canvas = container.querySelector('canvas')!;

    Object.defineProperty(video, 'videoWidth', { value: 1920, configurable: true });
    Object.defineProperty(video, 'videoHeight', { value: 1080, configurable: true });
    const rect = (w: number, h: number) => ({
      left: 0, top: 0, width: w, height: h, right: w, bottom: h, x: 0, y: 0, toJSON() {},
    }) as DOMRect;
    video.getBoundingClientRect = () => rect(600, 337.5); // video 真实渲染盒
    canvas.getBoundingClientRect = () => rect(600, 297.5); // overlay 让出 controls 后矮 40px

    fireEvent.click(screen.getByRole('button', { name: '定位点' }));
    // fireEvent.pointerDown 在这套 jsdom 版本里不携带 clientX/clientY——jsdom
    // 没有原生 PointerEvent 构造函数，@testing-library/dom 的 createEvent 会
    // 退化成裸 Event（见其源码 `window[EventType] || window.Event`），裸 Event
    // 的构造函数会静默丢掉 init 里的 clientX/clientY。要让这两个字段真的落到
    // 事件对象上，只能手工构造事件、用 defineProperty 挂上去，再走两参数的
    // fireEvent(el, event) 直接派发（跳过 createEvent 这一层）。
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true });
    Object.defineProperty(evt, 'clientX', { value: 10, configurable: true });
    Object.defineProperty(evt, 'clientY', { value: 250, configurable: true });
    fireEvent(canvas, evt);
    fireEvent.click(screen.getByRole('button', { name: '添加至输入框' }));
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const [entries] = onSave.mock.calls[0];
    const y = entries[0].ops[0].points[0].y;
    expect(y).toBeGreaterThan(750);
    expect(y).toBeLessThan(850);
  });
});
