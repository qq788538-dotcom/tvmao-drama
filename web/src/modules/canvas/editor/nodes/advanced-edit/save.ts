import { formatTime, type FrameAnnotation } from './engine';

// 保存编排与 UI 解耦成纯函数：抓帧/上传/建节点/连线/提示词全部注入——
// 这条链的顺序和失败停点是本功能最容易回归的部分，必须能脱离 DOM 单测。
export interface SaveDeps {
  // 传记录本身而不是时间：同一秒可有多条独立记录，按时间抓帧会全部合成出
  // 第一条的涂鸦。seek+合成在 UI 侧闭包（要摸 video 元素）。
  captureFrame(entry: FrameAnnotation): Promise<Blob>;
  // 建节点 + 上传二合一，返回新节点 id。合并成一步是因为上传只留 uploadFileIntoNode
  // 一个入口（见 utils/uploadIntoNode.ts 的 WHY 注释）——它要求先有 nodeId 才能把
  // content/assetId/mimeType/status 一次性落库，拆成 upload+createImageNode 两步
  // 会诱使调用方绕开这个入口自己拼 URL 写节点。
  materialize(blob: Blob, filename: string, index: number): Promise<string>;
  connect(nodeId: string): void;
  appendPrompt(line: string): void;
}

export interface SaveResult {
  ok: number;        // 成功条数
  failedAt?: number; // 失败条目下标（其后的条目未执行）
  error?: string;
}

export async function saveAnnotations(
  entries: FrameAnnotation[], deps: SaveDeps,
): Promise<SaveResult> {
  // 说明文字可选（用户拍板 2026-08-12）：画面标注本身已经携带意图，留空不拦。
  let ok = 0;
  for (let i = 0; i < entries.length; i++) {
    const e = entries[i];
    try {
      const blob = await deps.captureFrame(e);
      const nodeId = await deps.materialize(blob, `frame-${formatTime(e.timeSec).replace(':', '-')}.png`, i);
      deps.connect(nodeId);
      // 追加行形态对齐 jimeng：「@图片 mm:ss 视频帧标注 [说明]」——「视频帧标注」
      // 是固定标记词，让上游把这张参考图理解为帧级编辑指令而不是普通参考；说明
      // 留空时行尾干净不留悬空空格。提交时 serializeMentionPrompt 展开成
      // 「图片N mm:ss 视频帧标注 …」，编号与 images 数组下标天然对齐。
      const note = e.note.trim();
      deps.appendPrompt(`@[图片:${nodeId}] ${formatTime(e.timeSec)} 视频帧标注${note ? ` ${note}` : ''}`);
      ok += 1;
    } catch (err) {
      // 已成功的条目不回滚：节点已上画布是用户可见资产，撤销反而丢工作量。
      return { ok, failedAt: i, error: err instanceof Error ? err.message : String(err) };
    }
  }
  return { ok };
}
