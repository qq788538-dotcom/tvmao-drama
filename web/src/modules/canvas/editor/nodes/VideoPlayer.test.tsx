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
});
