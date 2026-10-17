export interface ResolutionResult {
  str: string;
  w: number;
  h: number;
}

export function calculateResolution(ratio: string, baseResolution: string): ResolutionResult {
  let baseW = 1024;
  let baseH = 1024;

  const explicitMatch = String(baseResolution).trim().match(/^(\d+)[xX](\d+)$/);
  if (explicitMatch) {
    const w = parseInt(explicitMatch[1], 10);
    const h = parseInt(explicitMatch[2], 10);
    if (Number.isFinite(w) && Number.isFinite(h)) {
      const safeW = Math.max(16, Math.round(w / 16) * 16);
      const safeH = Math.max(16, Math.round(h / 16) * 16);
      return { str: `${safeW}x${safeH}`, w: safeW, h: safeH };
    }
  }

  if (baseResolution === '1080P') {
    baseW = 1920;
    baseH = 1080;
  } else if (baseResolution === '720P') {
    baseW = 1280;
    baseH = 720;
  } else if (baseResolution === '480P') {
    baseW = 854;
    baseH = 480;
  } else if (baseResolution === '2K') {
    baseW = 2048;
    baseH = 2048;
  } else if (baseResolution === '4K') {
    // 图片 4K = 4096px 边长（和 1K/2K 一致），不是视频的 3840×2160。
    baseW = 4096;
    baseH = 4096;
  } else if (typeof baseResolution === 'string') {
    const kMatch = baseResolution.toUpperCase().match(/^(\d+)K$/);
    if (kMatch) {
      const size = parseInt(kMatch[1], 10) * 1024;
      baseW = size;
      baseH = size;
    }
  }

  if (ratio === 'Auto') {
    return { str: `${baseW}x${baseH}`, w: baseW, h: baseH };
  }

  const [rW, rH] = ratio.split(':').map(Number);
  if (!rW || !rH) return { str: '1024x1024', w: 1024, h: 1024 };

  let targetW: number;
  let targetH: number;

  if (Math.abs(rW - rH) < 0.1) {
    targetW = baseW;
    targetH = baseH;
  } else if (rW > rH) {
    targetW = baseResolution === 'Auto' || baseResolution === '1K' ? 1280 : baseW;
    targetH = Math.round(targetW * (rH / rW));
  } else {
    targetH = baseResolution === 'Auto' || baseResolution === '1K' ? 1280 : baseW;
    targetW = Math.round(targetH * (rW / rH));
  }

  targetW = Math.round(targetW / 16) * 16;
  targetH = Math.round(targetH / 16) * 16;

  return { str: `${targetW}x${targetH}`, w: targetW, h: targetH };
}

// gen-image 节点在 canvas 上的显示框，由真实输出的像素尺寸推导而来。
// 较短的一边钉在 1:1 基准（280px）；较长的一边在此基础上等比放大。
export function nodeBoxFromDims(w: number, h: number): { width: number; height: number } {
  const base = 280;
  let width: number;
  let height: number;
  if (w >= h) {
    height = base;
    width = Math.round((base * w) / h);
  } else {
    width = base;
    height = Math.round((base * h) / w);
  }
  return { width, height };
}
