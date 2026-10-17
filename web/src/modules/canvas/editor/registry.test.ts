import { describe, expect, it } from 'vitest';
import { nodeComponents, nodeMenuItems } from './registry';

// 「保存到本地」在右键菜单里，「图片对比」「预览」隐藏。
describe('添加节点菜单', () => {
  it('「保存到本地」在菜单里，归「输出」组', () => {
    expect(nodeMenuItems.find((m) => m.type === 'local-save')).toMatchObject({
      label: '保存到本地',
      group: 'output',
      hint: '下载图片或视频',
    });
  });

  it('「图片对比」「预览」不在菜单里，但仍注册（存量项目里的这两种节点照常渲染）', () => {
    const types = nodeMenuItems.map((m) => m.type);
    expect(types).not.toContain('image-compare');
    expect(types).not.toContain('preview');
    expect(nodeComponents['image-compare']).toBeTruthy();
    expect(nodeComponents.preview).toBeTruthy();
  });
});
