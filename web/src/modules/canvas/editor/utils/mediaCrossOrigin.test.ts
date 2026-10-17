import { describe, expect, it } from 'vitest';
import { mediaCrossOrigin } from './mediaCrossOrigin';

describe('mediaCrossOrigin', () => {
  it('本机 sidecar 媒体按 CORS 模式加载（否则读像素会污染 canvas）', () => {
    expect(mediaCrossOrigin('http://127.0.0.1:51234/media/u1/2026/10/a.png?token=t')).toBe('anonymous');
  });

  it('对象存储签名 URL 照旧按 CORS 模式加载', () => {
    expect(mediaCrossOrigin('https://b.tos.example/a.png?X-Tos-Signature=1')).toBe('anonymous');
    expect(mediaCrossOrigin('https://s3.example/a.png?X-Amz-Signature=1')).toBe('anonymous');
  });

  it('上游裸 URL、data:/blob:、空值不加 crossOrigin', () => {
    expect(mediaCrossOrigin('https://cdn.example/a.png')).toBeUndefined();
    expect(mediaCrossOrigin('data:image/png;base64,AAAA')).toBeUndefined();
    expect(mediaCrossOrigin('blob:tauri://localhost/123')).toBeUndefined();
    expect(mediaCrossOrigin('http://127.0.0.1.evil.example/a.png')).toBeUndefined();
    expect(mediaCrossOrigin(null)).toBeUndefined();
  });
});
