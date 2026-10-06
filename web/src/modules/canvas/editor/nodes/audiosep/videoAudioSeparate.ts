// 「音视频分离」核心:把视频拆成 音轨 wav + 无声视频 mp4,全在浏览器内完成。
// 对标 liblib 实测(docs/2026-08-17-liblib-audio-separation-analysis.md 第一节):
// 它的产物就是 解码 PCM 重写的 wav + stream copy 的无声 mp4,零服务端零积分。
//
// 两个 Conversion 都不重编码视频:
//   - 无声视频:audio.discard → 视频轨 mp4→mp4 stream copy,秒级;
//   - 音轨 wav:video.discard + WavOutputFormat → 只解码音频写 PCM,
//     零编码器依赖(wav 也正好在 Seedance 音频合规的格式清单里)。
// 顺序执行而不并行:两个任务都要拉源文件,并行会把同一份数据在内存里存两份。
export interface SeparatedMedia {
  audio: Blob; // wav
  video: Blob; // 无声 mp4
}

// 源视频没有音轨时抛此错误文案——调用方直接 toast,不产出空 wav。
export const NO_AUDIO_TRACK_ERROR = '该视频没有音轨,无法分离';

export async function separateAudioVideo(
  url: string,
  onProgress?: (progress: number) => void,
): Promise<SeparatedMedia> {
  const {
    Input,
    Output,
    Conversion,
    UrlSource,
    ALL_FORMATS,
    BufferTarget,
    Mp4OutputFormat,
    WavOutputFormat,
  } = await import('mediabunny');

  // 先探测音轨:mediabunny 在「输出容器装不下某轨」时只是静默 discard,
  // 不探测的话无音轨视频会安静产出一个 0 时长 wav。
  const probe = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
  if (!(await probe.getPrimaryAudioTrack())) {
    throw new Error(NO_AUDIO_TRACK_ERROR);
  }

  const convert = async (
    format: InstanceType<typeof Mp4OutputFormat> | InstanceType<typeof WavOutputFormat>,
    options: { video?: { discard: true }; audio?: { discard: true } },
    // 两段转码合并成一条 0~1 进度:无声视频是 stream copy 快路径,权重给小头。
    progressBase: number,
    progressSpan: number,
  ): Promise<Blob> => {
    const input = new Input({ formats: ALL_FORMATS, source: new UrlSource(url) });
    const target = new BufferTarget();
    const output = new Output({ format, target });
    const conversion = await Conversion.init({
      input,
      output,
      ...options,
      showWarnings: false,
    });
    if (onProgress) conversion.onProgress = (p) => onProgress(progressBase + p * progressSpan);
    await conversion.execute();
    if (!target.buffer) throw new Error('转码未产出数据');
    return new Blob([target.buffer], {
      type: format instanceof WavOutputFormat ? 'audio/wav' : 'video/mp4',
    });
  };

  const video = await convert(
    // fastStart:'in-memory' 把 moov 放文件头,产物上传后可边下边播(同 trim/crop)。
    new Mp4OutputFormat({ fastStart: 'in-memory' }),
    { audio: { discard: true } },
    0,
    0.3,
  );
  const audio = await convert(new WavOutputFormat(), { video: { discard: true } }, 0.3, 0.7);
  return { audio, video };
}
