// 本机媒体（sidecar 数据目录里的文件）的小工具：判断一个媒体地址是不是本机文件，
// 以及节点右键「在访达中显示 / 在资源管理器中显示」（spec S3）。
//
// 真实路径只在 sidecar 里换算：前端把节点上的媒体地址原样交给 POST /media/reveal，
// sidecar 解析出 object key、找到文件、调系统文件管理器选中它（P1b Task 8 的设计决定：
// 壳不加权限，WebView 拿不到打开任意路径的能力）。
import { api } from '@/shared/api';
import { isMacLike } from '@/modules/canvas/editor/nodes/standardNodeMenuItems';

// sidecar 出口的本地媒体地址：http://127.0.0.1:<端口>/media/<key>?token=…（P1b MediaSigner）。
// 端口每次启动都不同，sidecar 认得任意一次启动发出的这种地址，所以这里不比对端口。
const LOCAL_MEDIA_URL = /^http:\/\/127\.0\.0\.1:\d+\/media\//;

export function isLocalMediaUrl(url: string | undefined | null): boolean {
  if (!url) return false;
  return url.startsWith('media://') || LOCAL_MEDIA_URL.test(url);
}

/** 上游外链。生成结果下载到本机失败（重试 3 次）时，节点里留下的就是它（spec S5）。 */
export function isRemoteMediaUrl(url: string | undefined | null): boolean {
  if (!url) return false;
  return /^https?:\/\//i.test(url) && !isLocalMediaUrl(url);
}

/** 菜单文案按平台切换：macOS 叫访达，Windows 叫资源管理器。每次现算，测试才能切平台。 */
export function fileManagerLabel(): string {
  return isMacLike() ? '在访达中显示' : '在资源管理器中显示';
}

export async function revealInFileManager(url: string): Promise<void> {
  await api.post('/media/reveal', { url });
}

export function revealErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string } | undefined;
  return e?.response?.data?.error || e?.message || '无法打开所在文件夹';
}
