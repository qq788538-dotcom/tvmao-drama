import { describe, it, expect } from 'vitest';
import { layoutDropPoints, nodeTypeForFile, partitionDroppedFiles } from './fileDrop';

function fakeFile(name: string, type: string): File {
  return new File(['x'], name, { type });
}

describe('nodeTypeForFile', () => {
  it('按 MIME 前缀分到三种输入节点', () => {
    expect(nodeTypeForFile(fakeFile('a.png', 'image/png'))).toBe('input-image');
    expect(nodeTypeForFile(fakeFile('a.mp3', 'audio/mpeg'))).toBe('input-audio');
    expect(nodeTypeForFile(fakeFile('a.mp4', 'video/mp4'))).toBe('video-input');
  });

  it('空 MIME 时按扩展名兜底——macOS 拖 .mov/.heic 常常不给 MIME', () => {
    expect(nodeTypeForFile(fakeFile('clip.mov', ''))).toBe('video-input');
    expect(nodeTypeForFile(fakeFile('photo.HEIC', ''))).toBe('input-image');
    expect(nodeTypeForFile(fakeFile('voice.m4a', ''))).toBe('input-audio');
  });

  it('未知类型返回 null', () => {
    expect(nodeTypeForFile(fakeFile('pack.zip', 'application/zip'))).toBeNull();
    expect(nodeTypeForFile(fakeFile('notes.txt', 'text/plain'))).toBeNull();
    // 文件夹在浏览器里就是「空 MIME + 无扩展名」
    expect(nodeTypeForFile(fakeFile('我的素材', ''))).toBeNull();
    // 纯点开头的隐藏文件不该被当成扩展名 "gitignore"
    expect(nodeTypeForFile(fakeFile('.gitignore', ''))).toBeNull();
  });
});

describe('partitionDroppedFiles', () => {
  it('混合批次里合法文件照常通过,只有非法的进 rejected', () => {
    const { accepted, rejected } = partitionDroppedFiles([
      fakeFile('a.png', 'image/png'),
      fakeFile('pack.zip', 'application/zip'),
      fakeFile('b.mp4', 'video/mp4'),
    ]);
    expect(accepted.map((a) => a.nodeType)).toEqual(['input-image', 'video-input']);
    expect(rejected).toEqual([{ name: 'pack.zip', reason: 'application/zip' }]);
  });

  it('空 MIME 的非法文件给「未知类型」的拒绝原因', () => {
    const { rejected } = partitionDroppedFiles([fakeFile('素材文件夹', '')]);
    expect(rejected).toEqual([{ name: '素材文件夹', reason: '未知类型' }]);
  });

  it('整批非法时 accepted 为空', () => {
    const { accepted, rejected } = partitionDroppedFiles([
      fakeFile('a.zip', 'application/zip'),
      fakeFile('b.pdf', 'application/pdf'),
    ]);
    expect(accepted).toHaveLength(0);
    expect(rejected).toHaveLength(2);
  });
});

describe('layoutDropPoints', () => {
  const img = { width: 260, height: 260 };
  const video = { width: 580, height: 460 };

  it('单个文件落点就是锚点加半个节点——addNode 按中心放置', () => {
    expect(layoutDropPoints([img], { x: 100, y: 50 })).toEqual([{ x: 230, y: 180 }]);
  });

  it('横向步长取各节点自己的宽度,宽窄混排也不重叠', () => {
    const [p1, p2] = layoutDropPoints([img, video], { x: 0, y: 0 });
    // 第一个节点右边缘 = 260,第二个左边缘 = 中心 - 290 = 284,间隔正好 GAP=24
    expect(p1.x + img.width / 2).toBe(260);
    expect(p2.x - video.width / 2).toBe(284);
  });

  it('第 5 个换行:x 回到锚点,y 下移一整行高 + 间距', () => {
    const points = layoutDropPoints([img, img, img, img, img], { x: 0, y: 0 });
    expect(points[4].x).toBe(points[0].x);
    expect(points[4].y).toBe(points[0].y + img.height + 24);
  });

  it('行高按该行最高的节点算,下一行不会压上来', () => {
    const points = layoutDropPoints([img, img, img, video, img], { x: 0, y: 0 });
    // 首行最高是 video(460),第二行顶边 = 460 + 24
    expect(points[4].y).toBe(484 + img.height / 2);
  });

  it('空数组返回空', () => {
    expect(layoutDropPoints([], { x: 0, y: 0 })).toEqual([]);
  });
});
