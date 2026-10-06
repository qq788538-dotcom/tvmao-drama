import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { App as AntApp, Button } from 'antd';
import { Copy, X } from 'lucide-react';
import { BrandLogo } from '@/shared/BrandLogo';
import { CanvasEditor } from '@/modules/canvas/editor/CanvasEditor';
import { Lightbox } from '@/modules/canvas/editor/Lightbox';
import { Minimap } from '@/modules/canvas/editor/Minimap';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { cloneWorkflow, getToken, getWorkflowByToken } from '@/shared/api';
import '../../../styles/canvas.css';

// 分享工作流的完整只读画布视图。原样复用 CanvasEditor 和所有节点
// 组件；store 上的 readOnly 模式会把所有会改动状态的操作（拖拽、缩放、
// 删除、addConnection、生成……）变成空操作，这样画布就像一份「只能看
// 不能碰」的快照。平移 / 缩放 / 选择 / lightbox 仍可用于浏览导航。
export function SharedCanvasViewer() {
  const { token } = useParams<{ token: string }>();
  const navigate = useNavigate();
  const { message } = AntApp.useApp();
  const load = useCanvasStore((s) => s.load);
  const setReadOnly = useCanvasStore((s) => s.setReadOnly);
  const [cloning, setCloning] = useState(false);
  const [ready, setReady] = useState(false);

  const query = useQuery({
    queryKey: ['workflow-preview', token],
    queryFn: () => getWorkflowByToken(token ?? ''),
    enabled: !!token,
    retry: false,
  });

  useEffect(() => {
    if (!query.data) return;
    const snapshot = query.data.snapshot;
    load({
      nodes: (snapshot?.nodes as never) ?? [],
      connections: (snapshot?.connections as never) ?? [],
      view: snapshot?.view ?? { x: 0, y: 0, zoom: 1 },
    });
    setReadOnly(true);
    setReady(true);
  }, [query.data, load, setReadOnly]);

  // 卸载时总要清掉 readOnly，免得跳回 /canvas/:id 时编辑器还停在
  // 冻结状态。
  useEffect(
    () => () => {
      setReadOnly(false);
    },
    [setReadOnly],
  );

  const handleClone = async () => {
    if (!token) return;
    if (!getToken()) {
      navigate(`/login?redirect=${encodeURIComponent(`/share/${token}/view`)}`);
      return;
    }
    setCloning(true);
    try {
      const result = await cloneWorkflow(token);
      message.success('已克隆，正在打开...');
      navigate(`/canvas/${result.projectId}`);
    } catch (err: any) {
      const status = err?.response?.status;
      const text = err?.response?.data?.error || err?.message || '克隆失败';
      if (status === 422) message.error('存储空间不足，无法克隆');
      else if (status === 429) message.error(text);
      else if (status === 410) message.error('该模板已不可用');
      else message.error(text);
    } finally {
      setCloning(false);
    }
  };

  if (query.isLoading || (query.data && !ready)) {
    return (
      <FullPageMessage>
        <div style={{ color: '#888' }}>加载中…</div>
      </FullPageMessage>
    );
  }
  if (query.isError || !query.data) {
    return (
      <FullPageMessage>
        <div style={{ fontSize: 18, marginBottom: 8 }}>找不到该模板</div>
        <div style={{ color: '#888', marginBottom: 16 }}>链接可能已失效或被作者下架。</div>
        <Button onClick={() => navigate('/canvas')}>返回项目列表</Button>
      </FullPageMessage>
    );
  }

  const wf = query.data;

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: '#09090b',
        display: 'flex',
        flexDirection: 'column',
        color: '#e4e4e7',
      }}
    >
      <div
        style={{
          height: 56,
          flexShrink: 0,
          display: 'flex',
          alignItems: 'center',
          gap: 16,
          padding: '0 16px',
          background: '#111',
          borderBottom: '1px solid #1f1f1f',
        }}
      >
        <Link to="/" title="回到首页" style={{ display: 'flex', alignItems: 'center', flexShrink: 0 }}>
          <BrandLogo size={28} />
        </Link>
        <div style={{ fontSize: 14, fontWeight: 600, flexShrink: 0 }}>{wf.title}</div>
        <div style={{ flex: 1 }} />
        <div style={{ fontSize: 12, color: '#888' }}>只读模式，如需修改请克隆</div>
        <Button type="primary" icon={<Copy size={14} />} loading={cloning} onClick={handleClone}>
          克隆到我的项目
        </Button>
        <Button
          type="text"
          icon={<X size={16} />}
          onClick={() => navigate(`/share/${token}`)}
          aria-label="关闭"
        />
      </div>
      <div style={{ flex: 1, position: 'relative', overflow: 'hidden' }}>
        <CanvasEditor />
        <Minimap />
      </div>
      <Lightbox />
    </div>
  );
}

function FullPageMessage({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: '#09090b',
        color: '#e4e4e7',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 8,
      }}
    >
      {children}
    </div>
  );
}
