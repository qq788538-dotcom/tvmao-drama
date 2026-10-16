// 音频「截取 / 变速」的媒体处理层,全在浏览器内完成,零服务端零积分。
// 对标 liblib 实测(2026-08-18):它两个功能都是纯前端——截取产物是重写的
// wav,变速是**保音调** time-stretch(频谱质心比值 0.99、同频轴包络相关
// 0.999,运行时探针只见 1 个 AudioContext、零 Worker 零 wasm,即解码后
// 主线程纯 JS 处理)。这里同构:截取走 mediabunny 的 trim + WavOutputFormat
// (与视频剪辑同管道);变速走 WebAudio 解码 + soundtouchjs(WSOLA 类算法,
// 纯 JS 吃 PCM)+ 手写 wav 编码。纯函数,不碰 store。

// soundtouchjs 静态引入(~25KB):本文件由 AudioActionsBar **动态 import**,
// 它和 soundtouchjs 一起落在懒加载 chunk 里,不进首屏。
import { SimpleFilter, SoundTouch } from 'soundtouchjs';

// 变速倍率的合法区间,与 liblib 一致。
export const MIN_SPEED_RATE = 0.1;
export const MAX_SPEED_RATE = 4;

// 截取 [start, end] 秒转出 wav Blob。与 trimVideo 同一套 mediabunny
// Conversion,输出容器换成 WavOutputFormat:只解码音频写 PCM,零编码器依赖。
export async function trimAudio(
  url: string,
  start: number,
  end: number,
  onProgress?: (progress: number) => void,
): Promise<Blob> {
  const { Input, Output, Conversion, UrlSource, ALL_FORMATS, BufferTarget, WavOutputFormat } =
    await import('mediabunny');
  const input = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
  // 无论成败都 dispose(同 trimVideo)。
  try {
    const target = new BufferTarget();
    const output = new Output({ format: new WavOutputFormat(), target });
    const conversion = await Conversion.init({
      input,
      output,
      trim: { start, end },
      showWarnings: false,
    });
    if (onProgress) conversion.onProgress = (p) => onProgress(p);
    await conversion.execute();
    if (!target.buffer) throw new Error('转码未产出数据');
    return new Blob([target.buffer], { type: 'audio/wav' });
  } finally {
    input.dispose();
  }
}

// WebAudioBufferSource 只需要这四样,单测里用普通对象即可,不依赖真 AudioBuffer。
export interface AudioBufferLike {
  sampleRate: number;
  length: number;
  numberOfChannels: number;
  getChannelData(ch: number): Float32Array;
}

// 保音调变速核心:PCM → soundtouch tempo → PCM。独立成纯函数是为了可单测
// (合成正弦波断言 长度≈1/rate 且 过零率(音高)不变)。输出声道数与输入一致
// (soundtouch 内部恒定双声道,单声道输入时两路相同,取一路还原)。
export function stretchBuffer(buffer: AudioBufferLike, rate: number): Float32Array[] {
  const stereo = buffer.numberOfChannels > 1;
  const srcLeft = buffer.getChannelData(0);
  const srcRight = stereo ? buffer.getChannelData(1) : srcLeft;

  // soundtouchjs 的 fillOutputBuffer 在输入缓冲不足 16384 帧时直接 break,
  // 不 flush——直接用它的 WebAudioBufferSource 会把**结尾最多 16383 帧**
  // (~0.37s@44.1k)整段丢掉。这里的 source 在真实样本后垫一段静音把尾巴
  // 顶出管道,再按 length/rate 截断输出,静音垫不会漏进产物。
  const PAD = 16384 * 2;
  const source = {
    extract(target: Float32Array, numFrames: number, position: number): number {
      for (let i = 0; i < numFrames; i++) {
        const p = position + i;
        target[i * 2] = p < buffer.length ? srcLeft[p] : 0;
        target[i * 2 + 1] = p < buffer.length ? srcRight[p] : 0;
      }
      return Math.max(0, Math.min(numFrames, buffer.length + PAD - position));
    },
  };

  const st = new SoundTouch();
  st.tempo = rate;
  const filter = new SimpleFilter(source, st);
  const expected = Math.round(buffer.length / rate);
  const CHUNK = 16384;
  const tmp = new Float32Array(CHUNK * 2);
  const parts: Float32Array[] = [];
  let total = 0;
  while (total < expected) {
    const n = filter.extract(tmp, Math.min(CHUNK, expected - total));
    if (n <= 0) break;
    parts.push(tmp.slice(0, n * 2));
    total += n;
  }
  const left = new Float32Array(total);
  const right = stereo ? new Float32Array(total) : left;
  let off = 0;
  for (const p of parts) {
    const frames = p.length / 2;
    for (let i = 0; i < frames; i++) {
      left[off + i] = p[i * 2];
      if (stereo) right[off + i] = p[i * 2 + 1];
    }
    off += frames;
  }
  return stereo ? [left, right] : [left];
}

// PCM(-1..1 浮点) → 16-bit PCM wav。标准 44 字节头,交错声道。
export function encodeWavPCM16(channels: Float32Array[], sampleRate: number): ArrayBuffer {
  const numCh = channels.length;
  const frames = channels[0]?.length ?? 0;
  const dataSize = frames * numCh * 2;
  const buf = new ArrayBuffer(44 + dataSize);
  const v = new DataView(buf);
  const writeStr = (off: number, s: string) => {
    for (let i = 0; i < s.length; i++) v.setUint8(off + i, s.charCodeAt(i));
  };
  writeStr(0, 'RIFF');
  v.setUint32(4, 36 + dataSize, true);
  writeStr(8, 'WAVE');
  writeStr(12, 'fmt ');
  v.setUint32(16, 16, true);
  v.setUint16(20, 1, true); // PCM
  v.setUint16(22, numCh, true);
  v.setUint32(24, sampleRate, true);
  v.setUint32(28, sampleRate * numCh * 2, true);
  v.setUint16(32, numCh * 2, true);
  v.setUint16(34, 16, true);
  writeStr(36, 'data');
  v.setUint32(40, dataSize, true);
  let off = 44;
  for (let i = 0; i < frames; i++) {
    for (let ch = 0; ch < numCh; ch++) {
      const s = Math.max(-1, Math.min(1, channels[ch][i]));
      v.setInt16(off, s < 0 ? s * 0x8000 : s * 0x7fff, true);
      off += 2;
    }
  }
  return buf;
}

// 整条变速管道:拉源音频 → decodeAudioData → stretch → wav Blob。
// 进度没有真实中间态(soundtouch 是同步紧循环),只报 下载0.3/处理0.9/完成1。
export async function stretchAudio(
  url: string,
  rate: number,
  onProgress?: (progress: number) => void,
): Promise<Blob> {
  onProgress?.(0.05);
  const res = await fetch(url);
  if (!res.ok) throw new Error(`拉取音频失败(${res.status})`);
  const raw = await res.arrayBuffer();
  onProgress?.(0.3);
  const Ctx: typeof AudioContext | undefined =
    window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctx) throw new Error('当前浏览器不支持音频解码');
  const ctx = new Ctx();
  let decoded: AudioBuffer;
  try {
    decoded = await ctx.decodeAudioData(raw);
  } finally {
    void ctx.close();
  }
  onProgress?.(0.5);
  const channels = stretchBuffer(decoded, rate);
  onProgress?.(0.9);
  const wav = encodeWavPCM16(channels, decoded.sampleRate);
  onProgress?.(1);
  return new Blob([wav], { type: 'audio/wav' });
}
