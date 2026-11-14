import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { VideoPlayer } from './VideoPlayer';

function setup(props: Parameters<typeof VideoPlayer>[0] = {}) {
  const outerMouseDown = vi.fn();
  const view = render(
    <div onMouseDown={outerMouseDown}>
      <VideoPlayer src="https://example.com/a.mp4" {...props} />
    </div>,
  );
  const video = view.container.querySelector('video') as HTMLVideoElement;
  const play = vi.fn().mockResolvedValue(undefined);
  const pause = vi.fn();
  Object.defineProperty(video, 'play', { value: play, configurable: true });
  Object.defineProperty(video, 'pause', { value: pause, configurable: true });
  return { ...view, video, play, pause, outerMouseDown };
}

describe('VideoPlayer', () => {
  it('不用原生 controls，渲染自绘控制条', () => {
    const { video, container } = setup();
    expect(video).not.toHaveAttribute('controls');
    expect(container.querySelector('[data-video-controls]')).not.toBeNull();
    expect(screen.getByText('0:00')).toBeInTheDocument();
  });

  it('点播放键调用 video.play', () => {
    const { play } = setup();
    fireEvent.click(screen.getByRole('button', { name: '播放' }));
    expect(play).toHaveBeenCalled();
  });

  it('控制条上按下不冒泡（不会拖动节点）；画面上按下照常冒泡', () => {
    const { video, outerMouseDown } = setup();
    fireEvent.mouseDown(screen.getByRole('button', { name: '播放' }));
    expect(outerMouseDown).not.toHaveBeenCalled();
    fireEvent.mouseDown(video);
    expect(outerMouseDown).toHaveBeenCalledTimes(1);
  });

  it('点画面切换播放；按住拖动后松手不切换', () => {
    const { video, play } = setup();
    fireEvent.mouseDown(video, { clientX: 10, clientY: 10 });
    fireEvent.click(video, { clientX: 11, clientY: 10 });
    expect(play).toHaveBeenCalledTimes(1);
    fireEvent.mouseDown(video, { clientX: 10, clientY: 10 });
    fireEvent.click(video, { clientX: 80, clientY: 60 });
    expect(play).toHaveBeenCalledTimes(1);
  });

  it('静音键切换 muted', () => {
    const { video } = setup();
    fireEvent.click(screen.getByRole('button', { name: '静音' }));
    expect(video.muted).toBe(true);
  });

  it('悬停喇叭展开音量滑杆，点击位置设置音量', () => {
    const { video } = setup();
    expect(screen.queryByRole('slider', { name: '音量' })).toBeNull();
    fireEvent.mouseEnter(screen.getByRole('button', { name: '静音' }).parentElement!);
    const slider = screen.getByRole('slider', { name: '音量' });
    slider.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 100, height: 16, right: 100, bottom: 16, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect;
    slider.setPointerCapture = () => {};
    slider.releasePointerCapture = () => {};
    // jsdom 没有 PointerEvent，fireEvent.pointerDown 不带坐标；直接派发带 clientX 的同名事件。
    act(() => {
      slider.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, clientX: 30 }));
    });
    expect(video.volume).toBeCloseTo(0.3);
    expect(video.muted).toBe(false);
  });

  it('更多菜单可改播放速度', () => {
    const { video } = setup();
    fireEvent.click(screen.getByRole('button', { name: '更多' }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: '2x' }));
    expect(video.playbackRate).toBe(2);
  });

  it('透传 onContextMenu 给 video', () => {
    const onContextMenu = vi.fn();
    const { video } = setup({ onContextMenu });
    fireEvent.contextMenu(video);
    expect(onContextMenu).toHaveBeenCalled();
  });

  it('默认 preload=metadata，调用方显式传的优先', () => {
    const { video, unmount } = setup();
    expect(video.getAttribute('preload')).toBe('metadata');
    unmount();
    const { video: v2 } = setup({ preload: 'none' });
    expect(v2.getAttribute('preload')).toBe('none');
  });
});

// P15/P25：屏幕外的视频不挂媒体源。src 必须走 React（不活跃时渲染为 undefined），
// 不能在 DOM 上 removeAttribute——否则回到视口时 React 认为值没变不会写回，黑屏。
describe('VideoPlayer active', () => {
  const SRC = 'https://example.com/a.mp4';

  function stubMedia(video: HTMLVideoElement, opts: { paused?: boolean } = {}) {
    const load = vi.fn();
    Object.defineProperty(video, 'load', { value: load, configurable: true });
    Object.defineProperty(video, 'currentSrc', { value: SRC, configurable: true });
    Object.defineProperty(video, 'paused', { value: opts.paused ?? true, configurable: true });
    return load;
  }

  it('active=false 时不渲染 src；切回 true 时 React 把 src 写回', () => {
    const view = render(<VideoPlayer src={SRC} active={false} />);
    const video = view.container.querySelector('video')!;
    expect(video.hasAttribute('src')).toBe(false);
    view.rerender(<VideoPlayer src={SRC} active />);
    expect(video.getAttribute('src')).toBe(SRC);
  });

  it('从活跃变为不活跃：去掉 src 后 load() 释放资源', () => {
    const view = render(<VideoPlayer src={SRC} active />);
    const video = view.container.querySelector('video')!;
    const load = stubMedia(video);
    view.rerender(<VideoPlayer src={SRC} active={false} />);
    expect(video.hasAttribute('src')).toBe(false);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it('播放中离开视口不卸载', () => {
    const view = render(<VideoPlayer src={SRC} active />);
    const video = view.container.querySelector('video')!;
    const load = stubMedia(video, { paused: false });
    act(() => {
      video.dispatchEvent(new Event('play'));
    });
    view.rerender(<VideoPlayer src={SRC} active={false} />);
    expect(video.getAttribute('src')).toBe(SRC);
    expect(load).not.toHaveBeenCalled();
  });

  it('调用方传了 poster 时直接用它', () => {
    const view = render(<VideoPlayer src={SRC} active={false} poster="https://example.com/p.jpg" />);
    expect(view.container.querySelector('video')!.getAttribute('poster')).toBe('https://example.com/p.jpg');
  });

  it('没有 active prop 时视为活跃（灯箱、素材详情）', () => {
    const view = render(<VideoPlayer src={SRC} />);
    expect(view.container.querySelector('video')!.getAttribute('src')).toBe(SRC);
  });
});
