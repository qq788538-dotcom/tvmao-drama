// 音频工具条的行为约定钉死在这里:按钮清单 = 截取 / 变速 / 下载(不含
// 「智能切分」「自定义切分」);只在单选中本节点时
// 显示;截取/变速点击进入对应编辑会话(store.audioEdit),模式条顶掉工具条;
// 截取在时长未回填(duration=null)前禁用「生成」——提交管道在 duration 之前
// 无从校验选区。
import { App as AntApp } from 'antd';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AudioActionsBar } from './AudioActionsBar';
import { useCanvasStore } from '../store';

// jsdom 没有 VideoEncoder,截取入口的 WebCodecs 探测必须打桩放行。
vi.mock('./trim/videoTrim', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./trim/videoTrim')>();
  return { ...actual, isTrimSupported: () => true };
});

const audioURL = 'https://example.test/voice.mp3';

function setStore(selected = true) {
  useCanvasStore.setState({
    nodes: [
      {
        id: 'audio-1',
        type: 'input-audio' as const,
        x: 100,
        y: 200,
        width: 300,
        height: 200,
        status: 'succeeded' as const,
        content: audioURL,
        settings: {},
      },
    ],
    connections: [],
    selectedIds: selected ? new Set(['audio-1']) : new Set<string>(),
    view: { x: 0, y: 0, zoom: 1 },
    readOnly: false,
    audioEdit: null,
  });
}

function renderBar() {
  return render(
    <AntApp>
      <AudioActionsBar nodeId="audio-1" visible url={audioURL} />
    </AntApp>,
  );
}

describe('AudioActionsBar', () => {
  beforeEach(() => {
    setStore();
  });

  it('按钮清单:截取 / 变速 / 下载,没有切分类按钮', () => {
    renderBar();
    expect(screen.getByRole('button', { name: /截取/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /变速/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: '下载' })).toBeTruthy();
    expect(screen.queryByText(/智能切分/)).toBeNull();
    expect(screen.queryByText(/自定义切分/)).toBeNull();
  });

  it('未选中节点时整条不渲染', () => {
    setStore(false);
    renderBar();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('点击截取进入 trim 会话:模式条顶掉工具条,时长回填前禁用生成', async () => {
    renderBar();
    await userEvent.click(screen.getByRole('button', { name: /截取/ }));

    const edit = useCanvasStore.getState().audioEdit;
    expect(edit).toMatchObject({ nodeId: 'audio-1', mode: 'trim', duration: null });

    // 模式条形态:取消 + 时间段 + 生成(禁用);常规按钮消失。
    expect(screen.getByRole('button', { name: '取消' })).toBeTruthy();
    expect(screen.getByRole('button', { name: '生成' })).toHaveProperty('disabled', true);
    expect(screen.queryByRole('button', { name: '下载' })).toBeNull();

    // WaveformPlayer 回填时长 + 选区后,生成解禁。
    useCanvasStore.setState({
      audioEdit: { nodeId: 'audio-1', mode: 'trim', start: 0, end: 45, rate: 1, duration: 136 },
    });
    expect(await screen.findByRole('button', { name: '生成' })).toHaveProperty('disabled', false);
    expect(screen.getByText('00:00 - 00:45')).toBeTruthy();
  });

  it('点击变速进入 speed 会话:滑杆 + 倍率输入,默认 1x', async () => {
    renderBar();
    await userEvent.click(screen.getByRole('button', { name: /变速/ }));

    expect(useCanvasStore.getState().audioEdit).toMatchObject({ nodeId: 'audio-1', mode: 'speed', rate: 1 });
    expect(screen.getByRole('slider')).toBeTruthy();
    expect(screen.getByRole('button', { name: '生成变速音频' })).toBeTruthy();
  });

  it('模式条点取消回到工具条,会话清空', async () => {
    renderBar();
    await userEvent.click(screen.getByRole('button', { name: /变速/ }));
    await userEvent.click(screen.getByRole('button', { name: '取消' }));

    expect(useCanvasStore.getState().audioEdit).toBeNull();
    expect(screen.getByRole('button', { name: /截取/ })).toBeTruthy();
  });
});
