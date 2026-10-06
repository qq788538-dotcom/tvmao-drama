import { describe, expect, it } from 'vitest';
import { groupByZone, zoneOf } from './zones';
import type { CanvasNode } from '../editor/types';

function node(id: string, type: CanvasNode['type'], over: Partial<CanvasNode> = {}): CanvasNode {
  return { id, type, x: 0, y: 0, width: 200, height: 200, settings: {}, status: 'idle', ...over };
}

/** 只关心类型映射的用例用它，省得每条都写一遍节点字面量。 */
function zoneOfType(type: string) {
  return zoneOf(node('n', type as CanvasNode['type']));
}

describe('zoneOf', () => {
  it('把音频类节点归到 audio', () => {
    expect(zoneOfType('input-audio')).toBe('audio');
    expect(zoneOfType('gen-audio')).toBe('audio');
  });

  it('只有 text-node 归到 text —— script 按用户要求不进故事板', () => {
    expect(zoneOfType('text-node')).toBe('text');
    expect(zoneOfType('script')).toBeNull();
  });

  it('把图片类节点归到 image', () => {
    expect(zoneOfType('input-image')).toBe('image');
    expect(zoneOfType('gen-image')).toBe('image');
  });

  it('吃视频吐视频的后处理节点也归到 video', () => {
    expect(zoneOfType('video-input')).toBe('video');
    expect(zoneOfType('gen-video')).toBe('video');
    expect(zoneOfType('subtitle-erase')).toBe('video');
    expect(zoneOfType('video-upscale')).toBe('video');
    expect(zoneOfType('video-whitemodel')).toBe('video');
  });

  it('工作台与纯工具节点不进故事板', () => {
    expect(zoneOfType('director-desk')).toBeNull();
    expect(zoneOfType('image-compare')).toBeNull();
    expect(zoneOfType('local-save')).toBeNull();
  });

  // 节点类型清单还在增长。把它当封闭枚举的话，每加一种类型都得回来改故事板，
  // 忘了改就是运行时崩。未知类型静默不进，是唯一不会拖住新类型上线的做法。
  it('未知类型返回 null 而不是抛错', () => {
    expect(zoneOfType('some-future-node')).toBeNull();
  });
});

// preview 是通用「媒体容器」，一种类型对应三个分区，光看 type 判断不了——
// 这正是 zoneOf 吃整个 node 而不是 node.type 的原因。
describe('zoneOf — preview 按 settings.kind 分三栏', () => {
  it('kind=audio 进音频栏', () => {
    expect(zoneOf(node('p', 'preview', { settings: { kind: 'audio' } }))).toBe('audio');
  });

  it('kind=video 进视频栏', () => {
    expect(zoneOf(node('p', 'preview', { settings: { kind: 'video' } }))).toBe('video');
  });

  it('kind=image 进图片栏', () => {
    expect(zoneOf(node('p', 'preview', { settings: { kind: 'image' } }))).toBe('image');
  });

  // 兜底规则必须与 PreviewNode 自己的判断一致，否则同一个节点会在画布上
  // 渲染成视频播放器、在故事板里却被分进图片栏。
  it('kind 缺省时按 URL 扩展名兜底认视频', () => {
    expect(zoneOf(node('p', 'preview', { content: 'https://x/a.mp4' }))).toBe('video');
    expect(zoneOf(node('p', 'preview', { content: 'https://x/a.webm?sig=1' }))).toBe('video');
    expect(zoneOf(node('p', 'preview', { content: 'https://x/a.mov' }))).toBe('video');
  });

  it('kind 缺省且扩展名认不出时当图片', () => {
    expect(zoneOf(node('p', 'preview', { content: 'https://x/a.png' }))).toBe('image');
    expect(zoneOf(node('p', 'preview', { content: 'https://x/noext' }))).toBe('image');
    expect(zoneOf(node('p', 'preview'))).toBe('image');
  });
});

describe('groupByZone', () => {
  it('按分区分组，并保持传入顺序', () => {
    const nodes = [
      node('img-1', 'gen-image'),
      node('vid-1', 'gen-video'),
      node('img-2', 'input-image'),
      node('txt-1', 'text-node'),
    ];
    const grouped = groupByZone(nodes);
    expect(grouped.image.map((n) => n.id)).toEqual(['img-1', 'img-2']);
    expect(grouped.video.map((n) => n.id)).toEqual(['vid-1']);
    expect(grouped.text.map((n) => n.id)).toEqual(['txt-1']);
    expect(grouped.audio).toEqual([]);
  });

  it('丢掉不进故事板的节点', () => {
    const grouped = groupByZone([node('d-1', 'director-desk'), node('s-1', 'script')]);
    expect(grouped.audio).toEqual([]);
    expect(grouped.text).toEqual([]);
    expect(grouped.image).toEqual([]);
    expect(grouped.video).toEqual([]);
  });

  // 四个 key 必须恒在。少一个 key 会让消费方的 grouped[zone].map 直接崩。
  it('即使没有任何节点，四个分区的 key 也都在', () => {
    const grouped = groupByZone([]);
    expect(Object.keys(grouped).sort()).toEqual(['audio', 'image', 'text', 'video']);
  });

  it('同为 preview 的三个节点按各自 kind 落到三个不同分区', () => {
    const grouped = groupByZone([
      node('p-a', 'preview', { settings: { kind: 'audio' } }),
      node('p-v', 'preview', { settings: { kind: 'video' } }),
      node('p-i', 'preview', { settings: { kind: 'image' } }),
    ]);
    expect(grouped.audio.map((n) => n.id)).toEqual(['p-a']);
    expect(grouped.video.map((n) => n.id)).toEqual(['p-v']);
    expect(grouped.image.map((n) => n.id)).toEqual(['p-i']);
  });
});
