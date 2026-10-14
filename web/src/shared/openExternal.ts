// 用系统浏览器打开外部链接。WKWebView 里 <a target="_blank"> 不会开新窗口，
// 所以在 Tauri 里走 opener 插件（capabilities/default.json 只放行 qqdao 官网）；
// 纯浏览器调试时回退到 window.open。
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';

export async function openExternal(url: string): Promise<void> {
  let tauri = false;
  try {
    tauri = isTauri();
  } catch {
    tauri = false;
  }
  if (tauri) {
    await openUrl(url);
    return;
  }
  window.open(url, '_blank', 'noopener,noreferrer');
}
