import { describe, expect, it } from 'vitest';
import { importCandidate } from './importCandidate';
import { defaultScriptSettings } from './scriptData';

const candidate = JSON.stringify({
  character: [{ name: '张三', prompt: '青年男子，剑眉星目' }],
  scene: [{ name: '竹林', prompt: '晨雾竹林' }],
  item: [{ name: '长剑', prompt: '青铜长剑' }],
  storyboard: [
    { name: '开场', duration: 8, prompt: '古风。镜头1：@[角色-张三] 立于 @[场景-竹林]', character: ['张三'], scene: ['竹林'], item: [] },
    { name: '拔剑', duration: 5, prompt: '镜头1：@[角色-张三] 拔出 @[物品-长剑]' },
  ],
});

describe('importCandidate', () => {
  it('storyboard→rows 原样(token 不翻译)，顶层三类→资产，行内 character/scene/item 数组丢弃', () => {
    const out = importCandidate(candidate, defaultScriptSettings());
    expect(out.rows).toHaveLength(2);
    expect(out.rows[0]).toMatchObject({ shotNumber: 1, name: '开场', duration: 8 });
    expect(out.rows[0].prompt).toContain('@[角色-张三]');
    expect(out.rows[1]).toMatchObject({ shotNumber: 2, name: '拔剑', duration: 5 });
    expect(out.assets.characters.map((a) => a.name)).toEqual(['张三']);
    expect(out.assets.characters[0].description).toBe('青年男子，剑眉星目');
    expect(out.assets.scenes.map((a) => a.name)).toEqual(['竹林']);
    expect(out.assets.props.map((a) => a.name)).toEqual(['长剑']);
    // 新建资产未配图
    expect(out.assets.characters[0].linkedNodeId).toBe('');
  });

  it('同名资产按 name 匹配：刷新描述但保留已配的 linkedNodeId', () => {
    const cur = defaultScriptSettings();
    cur.assets.characters = [{ id: 'a1', name: '张三', description: '旧描述', linkedNodeId: 'img-1' }];
    const out = importCandidate(candidate, cur);
    expect(out.assets.characters).toHaveLength(1);
    expect(out.assets.characters[0]).toMatchObject({
      id: 'a1',
      description: '青年男子，剑眉星目',
      linkedNodeId: 'img-1',
    });
  });

  it('duration 越界钳到 4-15，缺失回落 4', () => {
    const doc = JSON.stringify({
      storyboard: [
        { name: 'a', duration: 99, prompt: 'p' },
        { name: 'b', duration: 1, prompt: 'p' },
        { name: 'c', prompt: 'p' },
      ],
    });
    const out = importCandidate(doc, defaultScriptSettings());
    expect(out.rows.map((r) => r.duration)).toEqual([15, 4, 4]);
  });

  it('非法 JSON / 空 storyboard 抛错', () => {
    expect(() => importCandidate('not json', defaultScriptSettings())).toThrow('合法 JSON');
    expect(() => importCandidate('{"storyboard":[]}', defaultScriptSettings())).toThrow('storyboard');
  });
});
