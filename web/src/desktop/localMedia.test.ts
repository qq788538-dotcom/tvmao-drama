import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/shared/api';
import {
  fileManagerLabel,
  isLocalMediaUrl,
  isRemoteMediaUrl,
  revealErrorMessage,
  revealInFileManager,
} from './localMedia';

function setPlatform(value: string) {
  Object.defineProperty(window.navigator, 'platform', { value, configurable: true });
}

afterEach(() => {
  setPlatform('');
  vi.restoreAllMocks();
});

describe('本机媒体地址判定', () => {
  it('sidecar 本地媒体地址和 media:// 都算本机文件，端口不限', () => {
    expect(isLocalMediaUrl('http://127.0.0.1:5123/media/u/1/a.png?token=t')).toBe(true);
    expect(isLocalMediaUrl('http://127.0.0.1:61000/media/k')).toBe(true);
    expect(isLocalMediaUrl('media://u/1/a.png')).toBe(true);
  });

  it('外链、接口地址、blob、空值都不算', () => {
    expect(isLocalMediaUrl('https://cdn.example.com/a.png')).toBe(false);
    expect(isLocalMediaUrl('http://127.0.0.1:5123/api/v1/projects')).toBe(false);
    expect(isLocalMediaUrl('blob:http://localhost/1')).toBe(false);
    expect(isLocalMediaUrl(undefined)).toBe(false);
    expect(isLocalMediaUrl('')).toBe(false);
  });

  it('isRemoteMediaUrl 只认 http(s) 外链', () => {
    expect(isRemoteMediaUrl('https://cdn.example.com/a.png')).toBe(true);
    expect(isRemoteMediaUrl('HTTP://cdn.example.com/a.png')).toBe(true);
    expect(isRemoteMediaUrl('http://127.0.0.1:5123/media/k')).toBe(false);
    expect(isRemoteMediaUrl('media://k')).toBe(false);
    expect(isRemoteMediaUrl('data:image/png;base64,AAAA')).toBe(false);
    expect(isRemoteMediaUrl(undefined)).toBe(false);
  });
});

describe('fileManagerLabel', () => {
  it('macOS 叫「在访达中显示」', () => {
    setPlatform('MacIntel');
    expect(fileManagerLabel()).toBe('在访达中显示');
  });

  it('Windows 叫「在资源管理器中显示」', () => {
    setPlatform('Win32');
    expect(fileManagerLabel()).toBe('在资源管理器中显示');
  });
});

describe('revealInFileManager', () => {
  it('把媒体地址原样 POST 给 /media/reveal', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ status: 204, data: '' });
    await revealInFileManager('http://127.0.0.1:5123/media/k?token=t');
    expect(post).toHaveBeenCalledWith('/media/reveal', { url: 'http://127.0.0.1:5123/media/k?token=t' });
  });

  it('revealErrorMessage 优先取 sidecar 返回的中文原因', () => {
    expect(revealErrorMessage({ response: { data: { error: '文件不存在，可能已被删除' } } })).toBe(
      '文件不存在，可能已被删除',
    );
    expect(revealErrorMessage(new Error('Network Error'))).toBe('Network Error');
    expect(revealErrorMessage(undefined)).toBe('无法打开所在文件夹');
  });
});
