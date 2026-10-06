import { describe, expect, it } from 'vitest';
import type { ScriptAssets } from './scriptData';
import { makeToken, parseTokens, renameTokenInPrompt, translatePromptForVideo } from './scriptTokens';

const assets: ScriptAssets = {
  characters: [
    { id: 'c1', name: '张三', description: '', linkedNodeId: 'img-zs' },
    { id: 'c2', name: '王大爷', description: '', linkedNodeId: '' }, // 没图
  ],
  scenes: [{ id: 's1', name: '修理铺', description: '', linkedNodeId: 'img-shop' }],
  props: [{ id: 'p1', name: '信封', description: '', linkedNodeId: 'img-letter' }],
};

const alive = (id: string) => id !== 'img-letter'; // 模拟信封的图节点已被删

describe('scriptTokens', () => {
  it('makeToken 产出 skill 原生格式', () => {
    expect(makeToken('character', '张三')).toBe('@[角色-张三]');
    expect(makeToken('prop', '信封')).toBe('@[物品-信封]');
  });

  it('parseTokens 按序解析并解出资产/断链', () => {
    const refs = parseTokens('镜头1：@[角色-张三]在@[场景-修理铺]。@[角色-李四]路过。', assets);
    expect(refs.map((r) => r.name)).toEqual(['张三', '修理铺', '李四']);
    expect(refs[0].asset?.id).toBe('c1');
    expect(refs[2].asset).toBeUndefined(); // 李四不存在 = 断链
  });

  it('renameTokenInPrompt 只替换同类型同名 token', () => {
    const p = '@[角色-张三]与@[场景-张三]同名不同类；@[角色-张三]再次出现。';
    const out = renameTokenInPrompt(p, 'character', '张三', '张三丰');
    expect(out).toBe('@[角色-张三丰]与@[场景-张三]同名不同类；@[角色-张三丰]再次出现。');
  });

  it('translatePromptForVideo：可解析→@[图片:nodeId] 并收集连线；缺图/断链/死节点→纯名字', () => {
    const { text, refNodeIds, missing } = translatePromptForVideo(
      '@[角色-张三]拿着@[物品-信封]走进@[场景-修理铺]，@[角色-王大爷]看着，@[角色-张三]坐下。',
      assets,
      alive,
    );
    // 张三有图两次出现→都替换但只记一次连线；修理铺有图；信封图节点已死、王大爷没图→纯名字
    expect(text).toBe('@[图片:img-zs]拿着信封走进@[图片:img-shop]，王大爷看着，@[图片:img-zs]坐下。');
    expect(refNodeIds).toEqual(['img-zs', 'img-shop']);
    expect(missing).toEqual([
      { kind: 'prop', name: '信封' },
      { kind: 'character', name: '王大爷' },
    ]);
  });
});
