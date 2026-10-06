import JSZip from "jszip";
import { dataUrlToBytes, saveBlobToDisk, saveMediaToDisk, type SaveResult } from "@/shared/saveToDisk";

export interface ScreenshotMeta {
  mode: "director" | "camera";
  cameraId: string | null;
  fov: number;
  position: [number, number, number];
  target: [number, number, number];
}

export interface ScreenshotResult {
  label: string;
  dataUrl: string;
  meta: ScreenshotMeta;
}

export function buildScreenshotMeta(input: ScreenshotMeta) {
  return input;
}

export function buildCaptureFileName(result: ScreenshotResult, index = 0) {
  const labelSlug = result.label.replace(/\s+/g, "-");
  const cameraSuffix = result.meta.cameraId ? `-${result.meta.cameraId}` : "";
  return `storyai-director-desk-${result.meta.mode}${cameraSuffix}-${labelSlug}-${index + 1}.png`;
}

/** 单张截图：统一走 saveMediaToDisk（Tauri 里弹系统保存对话框；锚点 download 会被 WebView 取消）。 */
export function downloadDataUrl(dataUrl: string, fileName: string): Promise<SaveResult> {
  return saveMediaToDisk(dataUrl, fileName);
}

/**
 * 多张截图：一张时直接保存；多张时打成一个 ZIP 只弹一次保存对话框（四/十二方位不必连弹十几个对话框）。
 * 返回保存的张数，用户取消返回 0。
 */
export async function downloadCaptureResults(results: ScreenshotResult[]): Promise<number> {
  if (results.length === 0) return 0;
  if (results.length === 1) {
    const r = await downloadDataUrl(results[0].dataUrl, buildCaptureFileName(results[0], 0));
    return r === "cancelled" ? 0 : 1;
  }
  const zip = new JSZip();
  results.forEach((result, index) => {
    zip.file(buildCaptureFileName(result, index), dataUrlToBytes(result.dataUrl));
  });
  const blob = await zip.generateAsync({ type: "blob" });
  const r = await saveBlobToDisk(blob, `storyai-director-desk-captures-${Date.now()}.zip`);
  return r === "cancelled" ? 0 : results.length;
}
