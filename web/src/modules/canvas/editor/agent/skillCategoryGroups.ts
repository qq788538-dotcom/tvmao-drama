// skill 分类的分组顺序与配色解析——从 SkillPopover 抽出来的纯函数层。
// 分类清单由后台维护（GET /skills/categories，已按 sort_order 排好），前端不再
// 写死「分类名 → 顺序/颜色」；抽成纯函数是为了让「按接口顺序分组 + 孤儿归尾 +
// 拉取失败回退」三条规则可以脱离组件直接测。

// 孤儿分类（skill 的分类不在后台字典里，例如管理员删了分类但没改 skill）
// 统一归到这个兜底分组，排在所有已知分类之后。
export const ORPHAN_GROUP = '其他';

// skill 的分类形状（AgentSkillDTO 的子集）。categories 是多分类真源（后端
// 5fbb661a 起），旧行/旧服务端可能缺失或为空——读方统一走 effectiveCategories
// 拿 [category] 兜底，不要直接读字段。
export interface SkillCategoryRef {
  category: string;
  categories?: string[];
}

// skill 的有效分类数组：categories 非空用它（一个 skill 可属于多个分组）；
// 空/缺失回退单值 [category]（无 categories 的旧行语义 = 只有主分类一组）。
export function effectiveCategories(s: SkillCategoryRef): string[] {
  return s.categories && s.categories.length > 0 ? s.categories : [s.category];
}

export interface CategoryGroups {
  /** 有序分组名清单（只含实际有 skill 的分组；孤儿存在时末尾追加「其他」）。 */
  groups: string[];
  /** skill 的原始分类名 → 它所属的分组名（孤儿映射为「其他」）。 */
  toGroup: (category: string) => string;
}

// 计算分组顺序。skills 按 effectiveCategories 展开——多分类 skill 会让它的每个
// 分类分组都出现。apiCategories 传 null 表示分类接口拉取失败——回退为「按 skills
// 里出现的分类原样分组」（即引入分类字典之前的行为：去重 + 字典序，多分类同样
// 逐个展开），保证弹层在字典接口挂掉时依然可用，只是失去后台排序。
export function buildCategoryGroups(
  skills: SkillCategoryRef[],
  apiCategories: Array<{ name: string }> | null,
): CategoryGroups {
  const present = new Set(skills.flatMap(effectiveCategories));
  if (apiCategories === null) {
    return { groups: Array.from(present).sort(), toGroup: (c) => c };
  }
  const known = new Set(apiCategories.map((c) => c.name));
  const groups = apiCategories.map((c) => c.name).filter((n) => present.has(n));
  const hasOrphan = Array.from(present).some((c) => !known.has(c));
  if (hasOrphan) groups.push(ORPHAN_GROUP);
  return { groups, toGroup: (c) => (known.has(c) ? c : ORPHAN_GROUP) };
}

// 分组胶囊筛选：包含即命中——skill 任一有效分类落在该分组就显示。group 传空串
// 是「全部」视图，原样返回（每个 skill 恰好一行，不因多分类重复）。抽成纯函数
// 与 buildCategoryGroups 同理：语义可脱离组件直接测。
export function filterSkillsByGroup<T extends SkillCategoryRef>(
  skills: T[],
  group: string,
  toGroup: (category: string) => string,
): T[] {
  if (!group) return skills;
  return skills.filter((s) => effectiveCategories(s).some((c) => toGroup(c) === group));
}

export interface ColorVisual {
  bg: string;
  fg: string;
}

// 未知色名 / 孤儿分组的中性兜底。类名与旧 visualFor 的兜底一致。
export const NEUTRAL_VISUAL: ColorVisual = { bg: 'bg-popover', fg: 'text-muted-foreground' };

// AntD 预设色名 → tailwind 配色（图标块底色 + 前景色）。类名必须写成完整字面量,
// tailwind JIT 只收集源码里出现过的字符串。volcano/geekblue 没有同名 tailwind
// 色板，取最近似的 orange-600/indigo。
const ANTD_COLOR_VISUAL: Record<string, ColorVisual> = {
  magenta: { bg: 'bg-pink-500/15', fg: 'text-pink-400' },
  red: { bg: 'bg-red-500/15', fg: 'text-red-400' },
  volcano: { bg: 'bg-orange-600/15', fg: 'text-orange-500' },
  orange: { bg: 'bg-orange-500/15', fg: 'text-orange-400' },
  gold: { bg: 'bg-yellow-500/15', fg: 'text-yellow-400' },
  lime: { bg: 'bg-lime-500/15', fg: 'text-lime-400' },
  green: { bg: 'bg-green-500/15', fg: 'text-green-400' },
  cyan: { bg: 'bg-cyan-500/15', fg: 'text-cyan-400' },
  blue: { bg: 'bg-blue-500/15', fg: 'text-blue-400' },
  geekblue: { bg: 'bg-indigo-500/15', fg: 'text-indigo-400' },
  purple: { bg: 'bg-purple-500/15', fg: 'text-purple-400' },
};

export function colorVisual(color: string | undefined): ColorVisual {
  return (color && ANTD_COLOR_VISUAL[color]) || NEUTRAL_VISUAL;
}

// 分类接口拉取失败时的兜底色（分类名 → AntD 色名）。对齐字典表播种前前端写死的
// 配色（见 migration 0101 与旧 CATEGORY_VISUAL），保证回退渲染与现状一致。
export const LEGACY_CATEGORY_COLOR: Record<string, string> = {
  视频: 'purple',
  社交媒体: 'magenta',
  电子商务: 'orange',
  品牌推广: 'gold',
  营销: 'blue',
  图像: 'blue',
  Video: 'purple',
};
