import { icons, Wand2, type LucideIcon } from 'lucide-react';
import { mediaCrossOrigin } from './mediaCrossOrigin';

// 绘图预设条目图标：value.icon 一个字段两种取值——图片 URL 或 lucide 图标名
// （kebab-case，如 clock-3 / grid-3x3，后台「图标库」菜单点击即复制）。
// 图标名方案让运营零图床成本且风格天然统一；URL 留给真要放品牌图的场景。

// isIconUrl 判定 icon 值是不是图片地址。不是 URL 的一律当图标名试解析。
export function isIconUrl(icon: string): boolean {
  return /^(https?:)?\/\//.test(icon) || icon.startsWith('/') || icon.startsWith('data:');
}

// lucideByName 按 kebab 名查 lucide 组件；查不到返回 null（调用方落默认图标）。
// kebab→Pascal：段首字母大写后拼接（clock-3→Clock3、grid-3x3→Grid3x3、
// axis-3d→Axis3d）——与后台图标库的 Pascal→kebab 转换互为逆运算，两侧必须咬合。
export function lucideByName(name: string): LucideIcon | null {
  const pascal = name
    .split('-')
    .filter(Boolean)
    .map((seg) => seg.charAt(0).toUpperCase() + seg.slice(1))
    .join('');
  return (icons as Record<string, LucideIcon>)[pascal] ?? null;
}

// PresetIcon 渲染一个预设图标：URL→<img>；lucide 名→线性图标；解析不了→魔法棒兜底。
export function PresetIcon({
  icon,
  size = 14,
  className = '',
}: {
  icon?: string;
  size?: number;
  className?: string;
}) {
  if (icon) {
    if (isIconUrl(icon)) {
      return (
        <img
          src={icon}
          crossOrigin={mediaCrossOrigin(icon)}
          alt=""
          style={{ width: size, height: size }}
          className={`rounded-sm object-cover ${className}`}
          draggable={false}
        />
      );
    }
    const Named = lucideByName(icon);
    if (Named) return <Named size={size} className={className} />;
  }
  return <Wand2 size={size} className={className} />;
}
