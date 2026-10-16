import type { MouseEvent } from 'react';
import { openExternal } from '@/shared/openExternal';

/**
 * markdown 渲染容器（agent 回复、文本节点只读态）里点到 <a> 时拦下默认导航。
 *
 * 桌面壳没有导航拦截：放任 <a href> 默认行为，整个应用主窗口会被带到外部站点
 * （WKWebView/WebView2 都没有返回入口，画布状态全丢，外站页面还跑在应用窗口
 * 里）。http(s) 链接改走系统浏览器（openExternal；Tauri 里受 opener 白名单约束，
 * 不在白名单的链接只是打不开，不会再把应用顶掉）。返回是否点中了链接。
 */
export function interceptMarkdownLink(e: MouseEvent<HTMLElement>): boolean {
  const target = e.target as Element | null;
  const a = target?.closest?.('a[href]') as HTMLAnchorElement | null;
  if (!a || !e.currentTarget.contains(a)) return false;
  e.preventDefault();
  const href = a.href;
  if (/^https?:\/\//i.test(href)) {
    void openExternal(href).catch((err) => console.warn('open external link failed', err));
  }
  return true;
}
