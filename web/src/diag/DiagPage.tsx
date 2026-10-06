import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { getServerInfo, type ServerInfo } from '@/shared/server';
import {
  connectionChecks,
  formatReport,
  mediaChecks,
  uploadSample,
  webCodecsChecks,
  type CheckResult,
} from './checks';

/**
 * embedded：嵌在设置页「环境诊断」分区里时为 true——不再显示自己的返回链接和大标题，
 * 也不占满 100vh（设置页已有侧栏、返回按钮和滚动容器，否则会重复并出现双滚动条）。
 */
export function DiagPage({ embedded = false }: { embedded?: boolean } = {}) {
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [results, setResults] = useState<CheckResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [copyStatus, setCopyStatus] = useState('');

  useEffect(() => {
    getServerInfo().then(setInfo, (e) => setStartError(String(e)));
  }, []);

  // sidecar 没起来：显示原因和提示，而不是白屏（壳会自动重启 sidecar，多次失败后才会走到这里）。
  if (startError) {
    return (
      <div style={{ padding: 24 }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88' }}>{startError}</pre>
        <p>请退出并重新打开应用。</p>
      </div>
    );
  }
  if (!info) return <div style={{ padding: 24 }}>正在连接本地服务…</div>;

  const runBasic = async () => {
    setBusy(true);
    try {
      setResults([...(await connectionChecks(info)), ...(await webCodecsChecks())]);
    } catch (e) {
      // 单项检查各自捕获异常；走到这里说明检查框架本身出错，也要显示出来而不是静默。
      setResults([{ name: '运行检查', ok: false, detail: String(e), ms: 0 }]);
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
    <div
      style={
        embedded
          ? { fontFamily: 'system-ui, sans-serif' }
          : { padding: 24, fontFamily: 'system-ui, sans-serif', height: '100vh', overflow: 'auto', boxSizing: 'border-box' }
      }
    >
      {!embedded && <Link to="/">← 返回画布</Link>}
      {!embedded && <h2>喵剧AI · 环境诊断</h2>}
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
        <button
          disabled={results.length === 0}
          onClick={() => {
            navigator.clipboard.writeText(report).then(
              () => setCopyStatus('已复制'),
              (e: unknown) => setCopyStatus(`复制失败：${String(e)}，请手动选中下方文本复制`),
            );
          }}
        >
          复制结果
        </button>
        {copyStatus && <span>{copyStatus}</span>}
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
