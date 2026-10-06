import { describe, expect, it } from 'vitest';
import {
  buildCategoryGroups,
  colorVisual,
  effectiveCategories,
  filterSkillsByGroup,
  NEUTRAL_VISUAL,
  ORPHAN_GROUP,
  type SkillCategoryRef,
} from './skillCategoryGroups';

const apiCategories = [
  { name: '视频' },
  { name: '社交媒体' },
  { name: '电子商务' },
  { name: '营销' },
];

// 单分类旧行的简写（categories 缺失 = 后端多分类改造前的存量数据形态）。
const sk = (category: string, categories?: string[]): SkillCategoryRef => ({
  category,
  ...(categories !== undefined ? { categories } : {}),
});

describe('buildCategoryGroups', () => {
  it('分组顺序严格按接口顺序，且只列实际有 skill 的分类', () => {
    // skills 里的出现顺序/字典序都与接口顺序不同，结果必须按接口序。
    const { groups } = buildCategoryGroups(
      [sk('营销'), sk('视频'), sk('电子商务'), sk('视频')],
      apiCategories,
    );
    expect(groups).toEqual(['视频', '电子商务', '营销']);
  });

  it('多分类 skill 让它的每个分类分组都出现（一个 skill 归多组）', () => {
    // 单个 skill 带三个分类：两个在字典里 + 一个孤儿——三个分组都要亮起来。
    const { groups, toGroup } = buildCategoryGroups(
      [sk('视频', ['视频', '营销', '自定义流程'])],
      apiCategories,
    );
    expect(groups).toEqual(['视频', '营销', ORPHAN_GROUP]);
    expect(toGroup('营销')).toBe('营销');
    expect(toGroup('自定义流程')).toBe(ORPHAN_GROUP);
  });

  it('无 categories 的旧行回退 [category] 兜底归组', () => {
    // categories 缺失与显式空数组等价，都退回主分类那一组。
    const { groups } = buildCategoryGroups([sk('营销'), sk('视频', [])], apiCategories);
    expect(groups).toEqual(['视频', '营销']);
  });

  it('不在接口分类表里的孤儿分类归到末尾的「其他」', () => {
    const { groups, toGroup } = buildCategoryGroups([sk('自定义流程'), sk('视频')], apiCategories);
    expect(groups).toEqual(['视频', ORPHAN_GROUP]);
    expect(groups[groups.length - 1]).toBe(ORPHAN_GROUP);
    expect(toGroup('自定义流程')).toBe(ORPHAN_GROUP);
    expect(toGroup('视频')).toBe('视频');
  });

  it('字典为空清单时全部 skill 都是孤儿，只剩「其他」一组', () => {
    const { groups, toGroup } = buildCategoryGroups([sk('视频'), sk('营销')], []);
    expect(groups).toEqual([ORPHAN_GROUP]);
    expect(toGroup('视频')).toBe(ORPHAN_GROUP);
  });

  it('拉取失败（apiCategories=null）回退为按 skills 出现的分类去重+字典序分组，多分类同样展开', () => {
    const { groups, toGroup } = buildCategoryGroups(
      [sk('营销'), sk('视频', ['视频', '图像']), sk('营销')],
      null,
    );
    // 等价旧行为：flatMap 展开后 Set 去重再 sort()。
    expect(groups).toEqual(['营销', '视频', '图像'].sort());
    // 回退模式没有「其他」概念，分类原样透传。
    expect(toGroup('随便什么')).toBe('随便什么');
  });
});

describe('effectiveCategories', () => {
  it('categories 非空即真源；空/缺失回退单值 [category]', () => {
    expect(effectiveCategories(sk('视频', ['视频', '营销']))).toEqual(['视频', '营销']);
    expect(effectiveCategories(sk('视频', []))).toEqual(['视频']);
    expect(effectiveCategories(sk('视频'))).toEqual(['视频']);
  });
});

describe('filterSkillsByGroup', () => {
  const { toGroup } = buildCategoryGroups([], apiCategories);
  const multi = sk('视频', ['视频', '营销']);
  const legacy = sk('营销');
  const orphan = sk('自定义流程', ['自定义流程']);

  it('胶囊筛选是「包含即命中」：多分类 skill 在它的每个分组下都可见', () => {
    expect(filterSkillsByGroup([multi, legacy], '视频', toGroup)).toEqual([multi]);
    expect(filterSkillsByGroup([multi, legacy], '营销', toGroup)).toEqual([multi, legacy]);
  });

  it('「全部」视图（group 空串）原样返回——每个 skill 恰好一次，不因多分类重复', () => {
    const all = filterSkillsByGroup([multi, legacy, orphan], '', toGroup);
    expect(all).toEqual([multi, legacy, orphan]);
    expect(all.length).toBe(3);
  });

  it('孤儿分类的 skill 命中「其他」分组', () => {
    expect(filterSkillsByGroup([multi, orphan], ORPHAN_GROUP, toGroup)).toEqual([orphan]);
  });

  it('无 categories 的旧行按 [category] 兜底参与筛选', () => {
    expect(filterSkillsByGroup([legacy], '营销', toGroup)).toEqual([legacy]);
    expect(filterSkillsByGroup([legacy], '视频', toGroup)).toEqual([]);
  });
});

describe('colorVisual', () => {
  it('AntD 预设色名映射到 tailwind bg/fg 类', () => {
    expect(colorVisual('purple')).toEqual({ bg: 'bg-purple-500/15', fg: 'text-purple-400' });
    expect(colorVisual('gold')).toEqual({ bg: 'bg-yellow-500/15', fg: 'text-yellow-400' });
  });

  it('未知色名给中性兜底', () => {
    expect(colorVisual('not-a-color')).toBe(NEUTRAL_VISUAL);
    expect(colorVisual(undefined)).toBe(NEUTRAL_VISUAL);
  });
});
