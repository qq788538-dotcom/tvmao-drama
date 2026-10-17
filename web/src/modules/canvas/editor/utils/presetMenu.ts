import type { GenImagePresetItem } from '@/shared/api';

// 绘图提示词预设菜单的分组与分列。抽成纯函数：分列按高度估算换列，规则值得
// 单测钉住（组不许从中间劈开、列数封顶后兜底进最后一列）。

export interface PresetGroup {
  category: string;
  items: GenImagePresetItem[];
}

// 预设条目按 category 分组：分组顺序 = category 在 presets 数组中首次出现的
// 先后（后台调组顺序 = 移动条目位置，不另设分类元数据）；组内按 sort 升序
// （缺省 0，Array#sort 稳定，同 sort 保持配置顺序）；category 为空归「其他」
// 组固定放最后。
export function groupPresets(presets: GenImagePresetItem[]): PresetGroup[] {
  const groups: PresetGroup[] = [];
  const byCategory = new Map<string, GenImagePresetItem[]>();
  let others: GenImagePresetItem[] | null = null;
  for (const p of presets) {
    const category = (p.value.category ?? '').trim();
    if (!category) {
      (others ??= []).push(p);
      continue;
    }
    let items = byCategory.get(category);
    if (!items) {
      items = [];
      byCategory.set(category, items);
      groups.push({ category, items });
    }
    items.push(p);
  }
  if (others) groups.push({ category: '其他', items: others });
  for (const g of groups) g.items.sort((a, b) => (a.value.sort ?? 0) - (b.value.sort ?? 0));
  return groups;
}

// 单列内容宽度（px）。弹层总宽 = 列数 × 列宽 + 列间距 + 面板内边距，
// 定位（开在按钮上方、左右夹取）要在渲染前算，所以只能用常量估算、不能量 DOM。
export const PRESET_COL_W = 288;

// 高度估算（px）——与 GenImageNode 弹层里组标题/条目的实际样式对应：
// 标题 pt-2 pb-1 + 11px 文字 ≈ 28；条目 py-2(16) + 32px 图标底座 ≈ 48；
// 副标题让文字块超过底座高度，再加 ≈ 8。
// 估算允许有小误差：换列只是排版决策，差十几像素顶多某列略高，不影响正确性。
const HEADER_H = 28;
const ITEM_H = 48;
const ITEM_DESC_EXTRA = 8;

// 单列最大内容高度：与弹层 maxHeight(400) 减去面板上下内边距对应。
export const PRESET_COL_MAX_H = 388;

// splitPresetColumns 把分组流式排进至多 maxCols 列：一列放不下（按高度估算）
// 就从下一个组起换列——**组永远不从中间劈开**（组标题始终跟着
// 自己的条目）。列数封顶后剩余组全部进最后一列，由弹层的 overflow-auto 兜底
// 滚动，不让面板顶出屏幕。单个组自己就超高时独占一列，同样交给兜底滚动。
export function splitPresetColumns(
  groups: PresetGroup[],
  maxCols = 3,
  maxH = PRESET_COL_MAX_H,
): PresetGroup[][] {
  const heightOf = (g: PresetGroup) =>
    HEADER_H + g.items.reduce((h, p) => h + ITEM_H + (p.value.description ? ITEM_DESC_EXTRA : 0), 0);
  const cols: PresetGroup[][] = [];
  let cur: PresetGroup[] = [];
  let curH = 0;
  for (const g of groups) {
    const h = heightOf(g);
    if (cur.length > 0 && curH + h > maxH && cols.length < maxCols - 1) {
      cols.push(cur);
      cur = [];
      curH = 0;
    }
    cur.push(g);
    curH += h;
  }
  if (cur.length > 0) cols.push(cur);
  return cols;
}
