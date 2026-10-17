/** 菜单「粘贴」的剪贴板读取。范围只收图片：截图/复制的图片是
 *  主场景，文本/视频/音频剪贴板极少见，不为它们加分支。
 *
 *  ⌘V 按键那条路**不走这里**——keydown 触发的 paste 事件自带 clipboardData，
 *  无需权限；navigator.clipboard.read() 只服务「菜单点击拿不到 paste 事件」
 *  这一种场景。 */

export type ClipboardImagesResult =
  | { status: 'ok'; files: File[] }
  | { status: 'empty' }
  /** 浏览器不支持 clipboard.read（Firefox 默认关）或用户拒了权限弹窗。 */
  | { status: 'denied' };

export async function readClipboardImageFiles(): Promise<ClipboardImagesResult> {
  if (typeof navigator === 'undefined' || !navigator.clipboard?.read) {
    return { status: 'denied' };
  }
  let items: ClipboardItems;
  try {
    items = await navigator.clipboard.read();
  } catch {
    return { status: 'denied' };
  }
  const files: File[] = [];
  for (const item of items) {
    const type = item.types.find((t) => t.startsWith('image/'));
    if (!type) continue;
    try {
      const blob = await item.getType(type);
      // image/svg+xml 这类带 + 的 subtype 只取主段当扩展名。
      const ext = type.split('/')[1]?.split('+')[0] || 'png';
      files.push(new File([blob], `clipboard-${Date.now()}-${files.length}.${ext}`, { type }));
    } catch {
      // 单条读失败（如格式协商失败）不拖累同批其他图。
    }
  }
  return files.length > 0 ? { status: 'ok', files } : { status: 'empty' };
}
