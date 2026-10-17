import { useThumbUrl } from './thumbs';
import { mediaCrossOrigin } from './utils/mediaCrossOrigin';

/**
 * 展示用 <img>：src 自动换成缩略图签名 URL（换取中/失败回退原图），并默认
 * 懒加载。给 gallery/chip 这类 map 循环用——hook 不能进循环，组件可以。
 * 注意：需要原图的场景（Lightbox、下载 <a href>）不要用它。
 */
export function ThumbImg({ src, loading, decoding, ...rest }: React.ImgHTMLAttributes<HTMLImageElement>) {
  const thumbSrc = useThumbUrl(src);
  // pending 期间不给 src——渲染占位空 img，避免先下原图再下缩略图。
  // crossOrigin 语义见 mediaCrossOrigin 注释：所有平台签名媒体统一 CORS 模式
  // 加载，防「无 ACAO 响应进缓存 → 读像素点位（宫格切分/标注/旋转）命中毒缓存
  // 被 CORS 拦截」。thumbs 失败回退原图时这条同样护住原图 URL。
  return (
    <img
      src={thumbSrc}
      crossOrigin={mediaCrossOrigin(thumbSrc)}
      loading={loading ?? 'lazy'}
      decoding={decoding ?? 'async'}
      {...rest}
    />
  );
}
