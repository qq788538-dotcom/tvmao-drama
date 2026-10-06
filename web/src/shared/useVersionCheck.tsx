// VersionUpdateGate —— 防「标签页一直开着、跑旧代码」。
// 前端编译期版本(VITE_APP_VERSION，deploy 注入 git 短 hash) 与服务端 /api/v1/version 比对，
// 不一致 → 弹一个「居中」更新弹框，深色主题与产品一致。非强制：可「稍后」关闭，
// 让用户先保存手头操作再自己点「更新」刷新——强制更新会打断操作、导致未保存数据丢失。
// 本会话只触发一次(notified)，关掉后不再反复打扰。
// 触发时机：挂载即查 + 每 5 分钟 + 标签页重新可见时(visibilitychange)。本地开发(dev)不打扰。
import { useEffect, useRef, useState } from 'react';
import { Button, ConfigProvider, Modal, theme } from 'antd';

const APP_VERSION = (import.meta.env.VITE_APP_VERSION as string | undefined) || 'dev';
const POLL_MS = 5 * 60 * 1000;

export function VersionUpdateGate() {
  const [updateReady, setUpdateReady] = useState(false);
  const notified = useRef(false);

  useEffect(() => {
    if (APP_VERSION === 'dev') return; // 本地/未注入版本时不轮询，避免开发噪音
    let stopped = false;
    const check = async () => {
      if (notified.current || stopped || document.hidden) return;
      try {
        const r = await fetch('/api/v1/version', { cache: 'no-store' });
        if (!r.ok) return;
        const data = (await r.json()) as { version?: string };
        if (data.version && data.version !== APP_VERSION) {
          notified.current = true; // 只触发一次
          setUpdateReady(true);
        }
      } catch {
        /* 网络抖动忽略，下次轮询再试 */
      }
    };
    const onVisible = () => {
      if (!document.hidden) check();
    };
    const timer = window.setInterval(check, POLL_MS);
    document.addEventListener('visibilitychange', onVisible);
    check();
    return () => {
      stopped = true;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, []);

  return (
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm, token: { colorPrimary: '#e0218a' } }}>
      <Modal
        open={updateReady}
        centered
        // 非强制：允许「稍后」/X/点遮罩/ESC 关闭，让用户先保存好手头操作再自己点更新，避免数据丢失。
        closable
        maskClosable
        onCancel={() => setUpdateReady(false)}
        footer={null}
        width={400}
        styles={{ content: { background: '#1e2236', borderRadius: 16, padding: 28 } }}
      >
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: 20, fontWeight: 700, color: '#fff', marginBottom: 12 }}>发现新版本</div>
          <div style={{ color: '#a1a1aa', fontSize: 14, lineHeight: 1.7, marginBottom: 26 }}>
            页面已更新。可先保存当前操作，准备好后点「更新」使用最新版本。
          </div>
          <div style={{ display: 'flex', gap: 12 }}>
            <Button
              block
              onClick={() => setUpdateReady(false)}
              style={{ background: 'transparent', borderColor: '#3f3f46', color: '#e4e4e7', height: 44, borderRadius: 10 }}
            >
              稍后
            </Button>
            <Button
              type="primary"
              block
              onClick={() => window.location.reload()}
              style={{
                background: 'linear-gradient(90deg,#7c3aed,#e0218a)',
                border: 'none',
                color: '#fff',
                fontWeight: 600,
                height: 44,
                borderRadius: 10,
              }}
            >
              更新
            </Button>
          </div>
        </div>
      </Modal>
    </ConfigProvider>
  );
}
