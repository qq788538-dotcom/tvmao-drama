// 把文字里的 sidecar token 换成 ***，用于诊断报告、错误提示这类会被用户复制转发的文本。
// 媒体 URL、WebSocket 地址把 token 放在查询串里（浏览器限制），第三方库（如 mediabunny）
// 报错时会把完整 URL 写进 message；Authorization 头的值也一并处理。
// token 经过 encodeURIComponent，不含 : ) ,，所以遇到这些字符就停（mediabunny 的格式是 "${url}: ${status}"）。
const QUERY_TOKEN = /([?&]token=)[^&\s#"'`:),]+/gi;
const BEARER = /(Bearer\s+)[^\s"'`]+/gi;

export function redactSecrets(text: string): string {
  return text.replace(QUERY_TOKEN, '$1***').replace(BEARER, '$1***');
}
