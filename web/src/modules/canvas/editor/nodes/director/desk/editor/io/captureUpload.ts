import { getDirectorHostAdapter } from "./hostAdapter";

// 快门 → 镜头列表的**唯一**上传收口。曾经的教训：上传挂钩只加在 CameraPanel
// 的快门上，漏了 ViewportToolbar 的快门（单张 + 4/12 宫格连拍都从那里入列），
// dataURL 直接进了场景状态并随画布快照落库（线上实拍）。所有「拍到 dataURL →
// 进镜头列表」的路径必须先过这里；持久化侧另有兜底消毒闸（directorStore 的
// 写库副本剥 data:），双层防线。
//
// 批量语义：并发上传、任何一张失败整体 reject——部分成功会让「第 3 张失败」
// 悄悄丢图，宁可让用户整轮重拍；已传成功的对象由孤儿 GC 管道回收，不泄漏。
export async function storeCaptureDataUrls(
  dataUrls: string[],
  fileNameForIndex: (index: number) => string,
): Promise<string[]> {
  const adapter = getDirectorHostAdapter();
  if (!adapter) return dataUrls;

  return Promise.all(
    dataUrls.map((dataUrl, index) => adapter.storeCapture(dataUrl, fileNameForIndex(index))),
  );
}
