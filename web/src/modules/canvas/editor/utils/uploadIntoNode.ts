import { currentProjectId, uploadAsset, type UploadedAsset } from '@/shared/api';
import { useCanvasStore } from '../store';
import { persistNodeById } from '../persistence/canvasMutations';

/**
 * 把一个本地文件上传成 asset，写进指定节点，并**持久化**。成功返回 asset，
 * 失败返回 null（错误已经写进节点的 status/error，调用方不用再处理）。
 *
 * 存在的理由是最后那步 persist：store 的 updateNode/updateNodeSettings 只改
 * 内存（见 store.ts 里 CanvasOp 的注释——更新类操作不广播 op），内容要落库必须
 * 显式 persistNodeById。上传入口有 5 个（三种输入节点自己的「选择/更换」、右键
 * 菜单「上传」、拖文件进画布），指望每个调用点自己记得的结果就是漏了 4 个：
 * addNode 只 upsert 了空壳节点，上传完的 URL 从没写回去，刷新页面图就没了。
 *
 * 所以上传这件事只留这一个入口，新的上传口子一律走这里。
 */
export async function uploadFileIntoNode(
  nodeId: string,
  file: File,
  extraSettings?: Record<string, unknown>,
): Promise<UploadedAsset | null> {
  const { updateNode, updateNodeSettings } = useCanvasStore.getState();
  updateNode(nodeId, { status: 'uploading', error: undefined });
  try {
    const asset = await uploadAsset(file, currentProjectId());
    updateNode(nodeId, { content: asset.url, status: 'succeeded' });
    updateNodeSettings(nodeId, {
      assetId: asset.id,
      mimeType: asset.mimeType,
      ...extraSettings,
    });
    persistNodeById(nodeId);
    return asset;
  } catch (err) {
    updateNode(nodeId, { status: 'failed', error: uploadErrorMessage(err) });
    // 失败态同样要落库：不然刷新后节点显示成「空白待上传」，用户以为没点过。
    persistNodeById(nodeId);
    return null;
  }
}

function uploadErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string };
  return e?.response?.data?.error || e?.message || '上传失败';
}
