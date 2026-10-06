// soundtouchjs 没有官方类型,只声明我们用到的最小面。
declare module 'soundtouchjs' {
  export class SoundTouch {
    tempo: number;
    rate: number;
    pitch: number;
  }
  export class WebAudioBufferSource {
    constructor(buffer: {
      sampleRate: number;
      length: number;
      numberOfChannels: number;
      getChannelData(ch: number): Float32Array;
    });
  }
  export class SimpleFilter {
    /** source 只要求鸭子型:extract(交错双声道 target, 帧数, 读取位置) → 实际帧数。 */
    constructor(
      source: { extract(target: Float32Array, numFrames: number, position: number): number },
      pipe: SoundTouch,
    );
    /** 抽取最多 numFrames 帧交错双声道样本进 target,返回实际帧数,0=结束。 */
    extract(target: Float32Array, numFrames: number): number;
  }
}
