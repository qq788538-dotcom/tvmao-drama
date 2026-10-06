import type { CSSProperties } from 'react';

/**
 * 品牌 logo。全站只此一处引用图片路径——换 logo 只需替换 public/logo.png。
 *
 * 图本身自带圆形轮廓和粉紫渐变底（透明背景 PNG），所以调用方**不要**再包一层
 * 渐变方块：那样会变成「方块里套个圆」，两层渐变叠在一起。改这个组件前的旧写法
 * 就是 5 处各自手搓一个渐变 div 塞 lucide 图标，配色还两两不一致（顶栏粉紫、
 * 画布页蓝）。
 */
export function BrandLogo({ size = 40, style }: { size?: number; style?: CSSProperties }) {
  return (
    <img
      src="/logo.png"
      alt="喵剧AI"
      width={size}
      height={size}
      style={{ display: 'block', flexShrink: 0, ...style }}
    />
  );
}
