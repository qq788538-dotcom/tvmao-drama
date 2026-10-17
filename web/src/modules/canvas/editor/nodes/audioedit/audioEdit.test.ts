// 变速核心(stretchBuffer)与 wav 编码的行为钉死在这里。截取本体走 mediabunny
// (WebCodecs),jsdom 不测(同 trim/crop/audiosep 先例)。
//
// 变速的验收标准是两条:时长 ≈ 1/rate,音高(过零率/秒)不变。
import { describe, expect, it } from 'vitest';
import { encodeWavPCM16, stretchBuffer, type AudioBufferLike } from './audioEdit';

function sineBuffer(freq: number, seconds: number, sampleRate = 44100, channels = 1): AudioBufferLike {
  const length = Math.floor(seconds * sampleRate);
  const chans = Array.from({ length: channels }, () => {
    const d = new Float32Array(length);
    for (let i = 0; i < length; i++) d[i] = Math.sin((2 * Math.PI * freq * i) / sampleRate) * 0.8;
    return d;
  });
  return {
    sampleRate,
    length,
    numberOfChannels: channels,
    getChannelData: (ch) => chans[ch],
  };
}

// 过零次数——数字信号里最朴素的音高代理:同一频率的正弦,每秒过零次数恒定。
function zeroCrossings(data: Float32Array): number {
  let n = 0;
  for (let i = 1; i < data.length; i++) {
    if ((data[i - 1] < 0 && data[i] >= 0) || (data[i - 1] >= 0 && data[i] < 0)) n++;
  }
  return n;
}

describe('stretchBuffer', () => {
  it('2x 变速:时长≈一半,音高(每秒过零率)不变', () => {
    const sr = 44100;
    const src = sineBuffer(220, 2, sr);
    const [out] = stretchBuffer(src, 2);

    const outSeconds = out.length / sr;
    expect(outSeconds).toBeGreaterThan(0.85);
    expect(outSeconds).toBeLessThan(1.15);

    // 220Hz 正弦每秒过零 440 次。WSOLA 拼接处会有少量偏差,给 ±10%。
    const zcPerSec = zeroCrossings(out) / outSeconds;
    expect(zcPerSec).toBeGreaterThan(440 * 0.9);
    expect(zcPerSec).toBeLessThan(440 * 1.1);
  });

  it('0.5x 变速:时长≈两倍', () => {
    const sr = 44100;
    const src = sineBuffer(220, 1, sr);
    const [out] = stretchBuffer(src, 0.5);
    expect(out.length / sr).toBeGreaterThan(1.7);
    expect(out.length / sr).toBeLessThan(2.3);
  });

  it('声道数跟随输入:单声道出 1 路,立体声出 2 路', () => {
    expect(stretchBuffer(sineBuffer(220, 0.5), 2)).toHaveLength(1);
    expect(stretchBuffer(sineBuffer(220, 0.5, 44100, 2), 2)).toHaveLength(2);
  });
});

describe('encodeWavPCM16', () => {
  it('标准 44 字节头 + 16bit 交错 PCM,样本可回读', () => {
    const left = new Float32Array([0, 0.5, -0.5, 1, -1]);
    const buf = encodeWavPCM16([left], 48000);
    const v = new DataView(buf);

    expect(String.fromCharCode(v.getUint8(0), v.getUint8(1), v.getUint8(2), v.getUint8(3))).toBe('RIFF');
    expect(v.getUint16(22, true)).toBe(1); // 声道数
    expect(v.getUint32(24, true)).toBe(48000); // 采样率
    expect(v.getUint32(40, true)).toBe(left.length * 2); // data 大小
    expect(buf.byteLength).toBe(44 + left.length * 2);

    expect(v.getInt16(44, true)).toBe(0);
    // DataView.setInt16 对小数截断而非四舍五入:0.5*0x7fff=16383.5 → 16383。
    expect(v.getInt16(46, true)).toBe(16383);
    expect(v.getInt16(48, true)).toBe(-16384);
    expect(v.getInt16(50, true)).toBe(0x7fff); // 正向满幅
    expect(v.getInt16(52, true)).toBe(-0x8000); // 负向满幅
  });

  it('立体声按 LRLR 交错', () => {
    const buf = encodeWavPCM16([new Float32Array([1, 1]), new Float32Array([-1, -1])], 44100);
    const v = new DataView(buf);
    expect(v.getUint16(22, true)).toBe(2);
    expect(v.getInt16(44, true)).toBe(0x7fff);
    expect(v.getInt16(46, true)).toBe(-0x8000);
    expect(v.getInt16(48, true)).toBe(0x7fff);
  });
});
