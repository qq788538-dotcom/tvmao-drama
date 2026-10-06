import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MultiAngleSphere } from './MultiAngleSphere';

describe('MultiAngleSphere', () => {
  it('渲染不炸，线框球壳有 12 根经线', () => {
    render(<MultiAngleSphere rotation={0} tilt={0} onChange={() => {}} />);
    expect(screen.getAllByTestId('meridian')).toHaveLength(12);
  });

  it('点右箭头 rotation +15；359 越界时归一回 0~359', () => {
    const onChange = vi.fn();
    const { unmount } = render(<MultiAngleSphere rotation={0} tilt={0} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: '向右' }));
    expect(onChange).toHaveBeenCalledWith({ rotation: 15, tilt: 0 });
    unmount();

    const onChange2 = vi.fn();
    render(<MultiAngleSphere rotation={350} tilt={0} onChange={onChange2} />);
    fireEvent.click(screen.getByRole('button', { name: '向右' }));
    expect(onChange2).toHaveBeenCalledWith({ rotation: 5, tilt: 0 });
  });

  it('点上箭头 tilt +15（俯拍加），钳制到 90 不越界', () => {
    const onChange = vi.fn();
    const { unmount } = render(<MultiAngleSphere rotation={0} tilt={0} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: '向上' }));
    expect(onChange).toHaveBeenCalledWith({ rotation: 0, tilt: 15 });
    unmount();

    const onChange2 = vi.fn();
    render(<MultiAngleSphere rotation={0} tilt={85} onChange={onChange2} />);
    fireEvent.click(screen.getByRole('button', { name: '向上' }));
    expect(onChange2).toHaveBeenCalledWith({ rotation: 0, tilt: 90 });
  });

  it('相机指示器存在', () => {
    render(<MultiAngleSphere rotation={0} tilt={0} onChange={() => {}} />);
    expect(screen.getByTestId('camera-indicator')).toBeTruthy();
  });
});
