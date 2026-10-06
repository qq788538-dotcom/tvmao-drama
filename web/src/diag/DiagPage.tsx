import { useState } from 'react';
import { SidecarGate } from '@/shared/SidecarGate';
import type { ServerInfo } from '@/shared/server';
import {
  connectionChecks,
  formatReport,
  mediaChecks,
  uploadSample,
  webCodecsChecks,
  type CheckResult,
} from './checks';

export function DiagPage() {
  return <SidecarGate>{(info) => <DiagBody info={info} />}</SidecarGate>;
}

function DiagBody({ info }: { info: ServerInfo }) {
  const [results, setResults] = useState<CheckResult[]>([]);
  const [busy, setBusy] = useState(false);

  const runBasic = async () => {
    setBusy(true);
    try {
      setResults([...(await connectionChecks(info)), ...(await webCodecsChecks())]);
    } finally {
      setBusy(false);
    }
  };

  const runMedia = async (file: File) => {
    setBusy(true);
    try {
      const name = await uploadSample(info, file);
      const media = await mediaChecks(info, name);
      setResults((prev) => [...prev, ...media]);
    } catch (e) {
      setResults((prev) => [...prev, { name: '上传测试视频', ok: false, detail: String(e), ms: 0 }]);
    } finally {
      setBusy(false);
    }
  };

  const report = formatReport(results);
  const failed = results.filter((r) => !r.ok).length;

  return (
    <div style={{ padding: 24, fontFamily: 'system-ui, sans-serif' }}>
      <h2>喵剧AI · 环境诊断</h2>
      <p>本地服务端口：{info.port}</p>
      <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
        <button disabled={busy} onClick={runBasic}>
          1. 运行连接与编解码检查
        </button>
        <label>
          2. 选择一个带声音的 mp4 测试视频：
          <input
            type="file"
            accept=".mp4,.mov,.webm"
            disabled={busy}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void runMedia(file);
            }}
          />
        </label>
        <button disabled={results.length === 0} onClick={() => void navigator.clipboard.writeText(report)}>
          复制结果
        </button>
      </div>
      {results.length > 0 && (
        <p style={{ color: failed ? '#f88' : '#8f8' }}>
          {failed ? `${failed} 项失败` : `全部 ${results.length} 项通过`}
        </p>
      )}
      <pre style={{ whiteSpace: 'pre-wrap', userSelect: 'text' }}>{report}</pre>
    </div>
  );
}
