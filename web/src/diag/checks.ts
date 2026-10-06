// 诊断检查：验证「壳 → sidecar → WebView」整条链路，以及参考仓画布依赖的
// WebCodecs / mediabunny 能力在当前系统 WebView 里是否可用。
// 每个检查互不依赖、各自捕获异常，一项失败不影响其余项。
import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  type ServerInfo,
} from '@/shared/server';

export interface CheckResult {
  name: string;
  ok: boolean;
  detail: string;
  ms: number;
}

async function run(name: string, fn: () => Promise<string>): Promise<CheckResult> {
  const start = performance.now();
  try {
    const detail = await fn();
    return { name, ok: true, detail, ms: Math.round(performance.now() - start) };
  } catch (e) {
    const detail = e instanceof Error ? e.message : String(e);
    return { name, ok: false, detail, ms: Math.round(performance.now() - start) };
  }
}

export async function connectionChecks(info: ServerInfo): Promise<CheckResult[]> {
  const out: CheckResult[] = [];

  out.push(
    await run('HTTP ping（带 token）', async () => {
      const resp = await fetch(buildHttpUrl(info, '/api/v1/ping'), { headers: authHeaders(info) });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const body = (await resp.json()) as { ok: boolean; version: string };
      return `version=${body.version}`;
    }),
  );

  out.push(
    await run('HTTP ping（不带 token 应被拒绝）', async () => {
      const resp = await fetch(buildHttpUrl(info, '/api/v1/ping'));
      if (resp.status !== 401) throw new Error(`期望 401，实际 ${resp.status}`);
      return '401';
    }),
  );

  out.push(
    await run('WebSocket 回显', () => {
      return new Promise<string>((resolve, reject) => {
        const ws = new WebSocket(buildWsUrl(info, '/api/v1/diag/ws'));
        const timer = setTimeout(() => {
          ws.close();
          reject(new Error('5 秒内未收到回显'));
        }, 5000);
        ws.onopen = () => ws.send('你好');
        ws.onmessage = (ev) => {
          clearTimeout(timer);
          ws.close();
          if (ev.data === '你好') resolve('回显一致');
          else reject(new Error(`回显不一致：${String(ev.data)}`));
        };
        ws.onerror = () => {
          clearTimeout(timer);
          reject(new Error('连接失败'));
        };
      });
    }),
  );

  return out;
}

type CodecCtor = { isConfigSupported(config: unknown): Promise<{ supported?: boolean }> };

async function supported(ctor: CodecCtor | undefined, config: unknown): Promise<string> {
  if (!ctor) throw new Error('API 不存在');
  const result = await ctor.isConfigSupported(config);
  if (!result.supported) throw new Error('不支持');
  return '支持';
}

export async function webCodecsChecks(): Promise<CheckResult[]> {
  const g = globalThis as Record<string, unknown>;
  const out: CheckResult[] = [];

  for (const api of ['VideoEncoder', 'VideoDecoder', 'AudioEncoder', 'AudioDecoder']) {
    out.push(
      await run(`API ${api}`, async () => {
        if (typeof g[api] === 'undefined') throw new Error('不存在');
        return '存在';
      }),
    );
  }

  const VE = g.VideoEncoder as CodecCtor | undefined;
  const VD = g.VideoDecoder as CodecCtor | undefined;
  const AE = g.AudioEncoder as CodecCtor | undefined;
  const AD = g.AudioDecoder as CodecCtor | undefined;

  out.push(
    await run('编码 H.264 1080p', () =>
      supported(VE, { codec: 'avc1.640028', width: 1920, height: 1080, bitrate: 8e6, framerate: 30 }),
    ),
  );
  out.push(await run('解码 H.264', () => supported(VD, { codec: 'avc1.640028' })));
  out.push(
    await run('编码 AAC', () =>
      supported(AE, { codec: 'mp4a.40.2', sampleRate: 48000, numberOfChannels: 2, bitrate: 128000 }),
    ),
  );
  out.push(
    await run('解码 AAC', () =>
      supported(AD, { codec: 'mp4a.40.2', sampleRate: 48000, numberOfChannels: 2 }),
    ),
  );
  return out;
}

/** 上传一个测试视频到 sidecar，返回可读回的文件名。 */
export async function uploadSample(info: ServerInfo, file: File): Promise<string> {
  const form = new FormData();
  form.append('file', file);
  const resp = await fetch(buildHttpUrl(info, '/api/v1/diag/upload'), {
    method: 'POST',
    headers: authHeaders(info),
    body: form,
  });
  if (!resp.ok) throw new Error(`上传失败：HTTP ${resp.status} ${await resp.text()}`);
  return ((await resp.json()) as { name: string }).name;
}

/** 用参考仓画布同样的 mediabunny 调用方式，对 sidecar 返回的媒体跑一遍。 */
export async function mediaChecks(info: ServerInfo, name: string): Promise<CheckResult[]> {
  const mb = await import('mediabunny');
  const url = buildMediaUrl(info, `/api/v1/diag/media/${name}`);
  const out: CheckResult[] = [];

  const convert = async (options: Record<string, unknown>, wav = false): Promise<string> => {
    const input = new mb.Input({ formats: mb.ALL_FORMATS, source: new mb.UrlSource(url) });
    const target = new mb.BufferTarget();
    const format = wav ? new mb.WavOutputFormat() : new mb.Mp4OutputFormat({ fastStart: 'in-memory' });
    const output = new mb.Output({ format, target });
    const conversion = await mb.Conversion.init({ input, output, ...options, showWarnings: false });
    if (!conversion.isValid) throw new Error('转换配置无效（有轨道被丢弃）');
    await conversion.execute();
    if (!target.buffer) throw new Error('转码未产出数据');
    return `输出 ${target.buffer.byteLength} 字节`;
  };

  out.push(
    await run('Range 请求返回 206', async () => {
      const resp = await fetch(url, { headers: { Range: 'bytes=0-99' } });
      if (resp.status !== 206) throw new Error(`期望 206，实际 ${resp.status}`);
      return resp.headers.get('Content-Range') ?? '206';
    }),
  );

  let duration = 0;
  out.push(
    await run('抽缩略图条（10 帧）', async () => {
      const input = new mb.Input({ formats: mb.ALL_FORMATS, source: new mb.UrlSource(url) });
      const track = await input.getPrimaryVideoTrack();
      if (!track) throw new Error('没有视频轨');
      duration = await input.computeDuration();
      const sink = new mb.CanvasSink(track, { width: 96, height: 56, fit: 'cover' });
      const stamps = Array.from({ length: 10 }, (_, i) => ((i + 0.5) / 10) * duration);
      let frames = 0;
      for await (const wrapped of sink.canvasesAtTimestamps(stamps)) if (wrapped) frames++;
      if (frames < 10) throw new Error(`只解出 ${frames}/10 帧`);
      return `时长 ${duration.toFixed(2)} 秒`;
    }),
  );

  const end = Math.max(1, Math.min(duration * 0.6, 5));
  out.push(await run('剪辑（截取前段）', () => convert({ trim: { start: 0.3, end } })));
  out.push(
    await run('强制重编码 H.264', () =>
      convert({ video: { forceTranscode: true, codec: 'avc' }, trim: { start: 0, end: Math.min(end, 2) } }),
    ),
  );
  out.push(await run('音频分离：无声 mp4', () => convert({ audio: { discard: true } })));
  out.push(await run('音频分离：WAV', () => convert({ video: { discard: true } }, true)));
  return out;
}

export function formatReport(results: CheckResult[]): string {
  const lines = [`UA: ${navigator.userAgent}`];
  for (const r of results) lines.push(`${r.ok ? 'PASS' : 'FAIL'} ${r.name} — ${r.detail} (${r.ms}ms)`);
  return lines.join('\n');
}
