// 平台签名媒体统一以 CORS 模式加载，根治「缓存投毒」类偶发加载失败：
// TOS/S3 只在请求带 Origin 头时才返回 Access-Control-Allow-Origin。若某处
// <video> 不带 crossOrigin 先播了某个 URL，浏览器会把「无 ACAO 的响应」写进
// 磁盘缓存；之后高级编辑等需要 crossOrigin（抓帧读像素的硬前提）的场景请求
// 同一 URL，直接命中这份无头缓存 → CORS 拦截。表现为「大概率能开、偶尔报错、
// 关闭重开又好了」（重试触发重新验证换来带头的响应）。全部播放点统一带
// crossOrigin 后，缓存里的响应恒有 ACAO，问题不存在。
//
// 图片侧一模一样：宫格切分/标注/旋转烘焙都要读原图像素，任何一个不带
// crossOrigin 的 <img>（Lightbox、故事板原图、agent 气泡……）先展示过同一
// URL 就会投毒（症状同款：偶发 CORS 报错，开着 F12 的 Disable cache 又好了）。
// 所以画布模块**所有**渲染平台媒体的 <img> 和 new Image() 探测一律带上——
// 本函数对非签名 URL 返回 undefined，闭眼挂不会误伤。
//
// 只对平台签名 URL 生效（带 TOS/S3 签名参数的才是我们的对象存储，CORS 已
// 配置）：极少数历史节点可能落了上游裸 URL（persistOutputs 存储失败的回退
// 路径），那些主机未必配 CORS，加 crossOrigin 反而会让本来能播的直接拒载。
export function mediaCrossOrigin(url: string | null | undefined): 'anonymous' | undefined {
  if (!url) return undefined;
  return url.includes('X-Tos-') || url.includes('X-Amz-') ? 'anonymous' : undefined;
}
