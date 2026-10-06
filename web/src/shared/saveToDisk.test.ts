import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
let tauri = true;
vi.mock('@tauri-apps/api/core', () => ({
  invoke: (...args: unknown[]) => invoke(...args),
  isTauri: () => tauri,
}));
const saveAs = vi.fn();
vi.mock('file-saver', () => ({ saveAs: (...args: unknown[]) => saveAs(...args) }));

import {
  FILE_NAME_HEADER,
  MAX_SAVE_BYTES,
  saveBytesToDisk,
  dataUrlToBytes,
  extFromUrl,
  isLocalMediaUrl,
  saveBlobToDisk,
  saveManyMediaToDisk,
  saveMediaToDisk,
  saveWithFeedback,
} from './saveToDisk';

const LOCAL = 'http://127.0.0.1:54321/media/gen/1/out?token=t';

beforeEach(() => {
  invoke.mockReset();
  saveAs.mockReset();
  tauri = true;
  vi.unstubAllGlobals();
});

describe('isLocalMediaUrl', () => {
  it('只认 sidecar 媒体 URL 和 media://', () => {
    expect(isLocalMediaUrl(LOCAL)).toBe(true);
    expect(isLocalMediaUrl('media://gen/1/out')).toBe(true);
    expect(isLocalMediaUrl('http://127.0.0.1:54321/api/v1/x')).toBe(false);
    expect(isLocalMediaUrl('http://localhost:5173/media/a.png')).toBe(false);
    expect(isLocalMediaUrl('https://cdn.example.com/media/a.png')).toBe(false);
    expect(isLocalMediaUrl('data:image/png;base64,AA==')).toBe(false);
  });
});

describe('saveMediaToDisk (Tauri)', () => {
  it('本地媒体走 save_media，只传 url 和建议文件名，不传目标路径', async () => {
    invoke.mockResolvedValue('/Users/me/Desktop/a.png');
    await expect(saveMediaToDisk(LOCAL, 'a.png')).resolves.toBe('saved');
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(invoke).toHaveBeenCalledWith('save_media', { url: LOCAL, suggestedName: 'a.png' });
  });

  it('用户取消对话框返回 cancelled', async () => {
    invoke.mockResolvedValue(null);
    await expect(saveMediaToDisk(LOCAL, 'a.png')).resolves.toBe('cancelled');
  });

  it('Rust 报错原样抛出', async () => {
    invoke.mockRejectedValue('文件不存在，可能已被删除');
    await expect(saveMediaToDisk(LOCAL, 'a.png')).rejects.toBe('文件不存在，可能已被删除');
  });

  it('data URL 在前端解码后走 save_bytes（raw body + 文件名头）', async () => {
    invoke.mockResolvedValue('/x/shot.png');
    await expect(saveMediaToDisk('data:image/png;base64,AQID', '截图 1.png')).resolves.toBe('saved');
    const [cmd, body, opts] = invoke.mock.calls[0];
    expect(cmd).toBe('save_bytes');
    expect(Array.from(body as Uint8Array)).toEqual([1, 2, 3]);
    expect(opts).toEqual({ headers: { [FILE_NAME_HEADER]: encodeURIComponent('截图 1.png') } });
  });

  it('非本地 http 先 fetch 再 save_bytes；fetch 失败给出可读错误', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Uint8Array([9, 9])));
    vi.stubGlobal('fetch', fetchMock);
    invoke.mockResolvedValue('/x/a.png');
    await saveMediaToDisk('blob:http://tauri.localhost/abc', 'a.png');
    expect(fetchMock).toHaveBeenCalledWith('blob:http://tauri.localhost/abc');
    expect(invoke.mock.calls[0][0]).toBe('save_bytes');

    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(saveMediaToDisk('https://cdn.example.com/a.png', 'a.png')).rejects.toThrow('不在本地');
  });

  it('空 URL 直接报错', async () => {
    await expect(saveMediaToDisk('', 'a.png')).rejects.toThrow();
    expect(invoke).not.toHaveBeenCalled();
  });
});

describe('saveMediaToDisk (浏览器回退)', () => {
  it('非 Tauri 时用 <a download>，不调 invoke', async () => {
    tauri = false;
    const clicks: Array<{ href: string; download: string }> = [];
    const orig = HTMLAnchorElement.prototype.click;
    HTMLAnchorElement.prototype.click = function (this: HTMLAnchorElement) {
      clicks.push({ href: this.href, download: this.download });
    };
    try {
      await expect(saveMediaToDisk(LOCAL, 'v.mp4')).resolves.toBe('fallback');
    } finally {
      HTMLAnchorElement.prototype.click = orig;
    }
    expect(invoke).not.toHaveBeenCalled();
    expect(clicks).toEqual([{ href: LOCAL, download: 'v.mp4' }]);
    expect(document.querySelectorAll('a').length).toBe(0);
  });

  it('Blob 非 Tauri 时交给 file-saver', async () => {
    tauri = false;
    const blob = new Blob(['zip']);
    await expect(saveBlobToDisk(blob, 'h.zip')).resolves.toBe('fallback');
    expect(saveAs).toHaveBeenCalledWith(blob, 'h.zip');
  });
});

describe('saveBlobToDisk (Tauri)', () => {
  it('ZIP 字节经 save_bytes 交给 Rust', async () => {
    invoke.mockResolvedValue('/x/h.zip');
    await expect(saveBlobToDisk(new Blob([new Uint8Array([80, 75])]), 'history-1.zip')).resolves.toBe('saved');
    const [cmd, body, opts] = invoke.mock.calls[0];
    expect(cmd).toBe('save_bytes');
    expect(Array.from(body as Uint8Array)).toEqual([80, 75]);
    expect(opts.headers[FILE_NAME_HEADER]).toBe('history-1.zip');
    expect(saveAs).not.toHaveBeenCalled();
  });
});

describe('saveBytesToDisk 大小上限', () => {
  it('超过上限直接报错，不调 invoke（与 Rust MAX_SAVE_BYTES 一致）', async () => {
    expect(MAX_SAVE_BYTES).toBe(2 * 1024 * 1024 * 1024);
    const huge = { byteLength: MAX_SAVE_BYTES + 1 } as unknown as Uint8Array;
    await expect(saveBytesToDisk(huge, 'a.zip')).rejects.toThrow('文件过大');
    expect(invoke).not.toHaveBeenCalled();
  });
});

describe('saveManyMediaToDisk', () => {
  it('全是本地媒体时只调一次 save_media_batch（一个文件夹对话框）', async () => {
    invoke.mockResolvedValue(2);
    const items = [
      { url: LOCAL, name: 'output-1.png' },
      { url: 'media://gen/2/out', name: 'output-2.mp4' },
    ];
    await expect(saveManyMediaToDisk(items)).resolves.toBe(2);
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(invoke).toHaveBeenCalledWith('save_media_batch', { items });
  });

  it('单个文件走 save_media；取消返回 null', async () => {
    invoke.mockResolvedValue(null);
    await expect(saveManyMediaToDisk([{ url: LOCAL, name: 'a.png' }])).resolves.toBeNull();
    expect(invoke.mock.calls[0][0]).toBe('save_media');
  });

  it('混有非本地媒体时逐个保存，中途取消就停', async () => {
    invoke.mockResolvedValueOnce('/x/a.png').mockResolvedValueOnce(null);
    const n = await saveManyMediaToDisk([
      { url: LOCAL, name: 'a.png' },
      { url: 'data:text/plain,hi', name: 'b.txt' },
      { url: LOCAL, name: 'c.png' },
    ]);
    expect(n).toBe(1);
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('空列表不弹任何对话框', async () => {
    await expect(saveManyMediaToDisk([])).resolves.toBe(0);
    expect(invoke).not.toHaveBeenCalled();
  });
});

describe('saveWithFeedback', () => {
  const fb = () => ({ success: vi.fn(), error: vi.fn() });

  it('保存成功提示，取消不提示，失败报原因', async () => {
    const a = fb();
    await saveWithFeedback(async () => 'saved', a);
    expect(a.success).toHaveBeenCalledWith('已保存');

    const b = fb();
    await saveWithFeedback(async () => 'cancelled', b);
    await saveWithFeedback(async () => null, b);
    expect(b.success).not.toHaveBeenCalled();
    expect(b.error).not.toHaveBeenCalled();

    const c = fb();
    vi.spyOn(console, 'error').mockImplementation(() => {});
    await saveWithFeedback(async () => {
      throw '不能保存到喵剧的数据目录里，请换一个位置';
    }, c);
    expect(c.error).toHaveBeenCalledWith('保存失败：不能保存到喵剧的数据目录里，请换一个位置');

    const d = fb();
    await saveWithFeedback(async () => 3, d);
    expect(d.success).toHaveBeenCalledWith('已保存 3 个文件');
  });
});

describe('工具函数', () => {
  it('dataUrlToBytes 支持 base64 和百分号编码', () => {
    expect(Array.from(dataUrlToBytes('data:image/png;base64,AQID'))).toEqual([1, 2, 3]);
    expect(new TextDecoder().decode(dataUrlToBytes('data:application/json,%7B%22a%22%3A1%7D'))).toBe('{"a":1}');
    expect(() => dataUrlToBytes('nope')).toThrow();
  });

  it('extFromUrl 忽略 query，无扩展名用兜底', () => {
    expect(extFromUrl('http://127.0.0.1:1/media/u/a.WEBP?token=x', 'png')).toBe('webp');
    expect(extFromUrl('http://127.0.0.1:1/media/gen/1/out?token=x.mp4', 'png')).toBe('png');
  });
});
