import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { App as AntApp, Button, Card, Skeleton, Tag, Typography } from 'antd';
import { Sparkles } from 'lucide-react';
import { cloneWorkflow, getToken, getWorkflowByToken } from '@/shared/api';
import { BrandLogo } from '@/shared/BrandLogo';
import { mediaCrossOrigin } from '@/modules/canvas/editor/utils/mediaCrossOrigin';

// SharePage 在 `/share/:token` 渲染已发布工作流的公开预览。这里我们
// 刻意不下发完整快照——只给够用来决定是否克隆的元数据。克隆需要登录；
// 未登录访客会被弹到 /login，并带上回跳参数。
export function SharePage() {
  const { token } = useParams<{ token: string }>();
  const navigate = useNavigate();
  const { message } = AntApp.useApp();
  const [cloning, setCloning] = useState(false);

  const query = useQuery({
    queryKey: ['workflow-preview', token],
    queryFn: () => getWorkflowByToken(token ?? ''),
    enabled: !!token,
    retry: false,
  });

  const handleClone = async () => {
    if (!token) return;
    if (!getToken()) {
      navigate(`/login?redirect=${encodeURIComponent(`/share/${token}`)}`);
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

  if (query.isLoading) {
    return (
      <PageShell>
        <Skeleton active paragraph={{ rows: 6 }} />
      </PageShell>
    );
  }
  if (query.isError || !query.data) {
    return (
      <PageShell>
        <Typography.Title level={4}>找不到该模板</Typography.Title>
        <Typography.Paragraph type="secondary">
          链接可能已失效或被作者下架。
        </Typography.Paragraph>
        <Button onClick={() => navigate('/canvas')}>返回项目列表</Button>
      </PageShell>
    );
  }

  const wf = query.data;
  const tags = (wf.tags || '').split(',').map((s) => s.trim()).filter(Boolean);

  return (
    <PageShell>
      <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap' }}>
        <div style={{ flex: '1 1 320px', minWidth: 280 }}>
          {wf.thumbnailUrl ? (
            <img
              src={wf.thumbnailUrl}
              crossOrigin={mediaCrossOrigin(wf.thumbnailUrl)}
              alt={wf.title}
              style={{
                width: '100%',
                borderRadius: 16,
                border: '1px solid #2a2a2a',
                background: '#1a1a1a',
                aspectRatio: '1 / 1',
                objectFit: 'cover',
              }}
            />
          ) : (
            <div
              style={{
                width: '100%',
                aspectRatio: '1 / 1',
                borderRadius: 16,
                border: '1px solid #2a2a2a',
                background: 'linear-gradient(135deg, #1f1f1f, #292929)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: '#666',
              }}
            >
              <Sparkles size={48} />
            </div>
          )}
        </div>
        <div style={{ flex: '2 1 360px', minWidth: 320 }}>
          <Typography.Title level={3} style={{ marginTop: 0 }}>
            {wf.title}
          </Typography.Title>
          {wf.description ? (
            <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>
              {wf.description}
            </Typography.Paragraph>
          ) : (
            <Typography.Paragraph type="secondary">作者没有填写简介。</Typography.Paragraph>
          )}
          {tags.length > 0 && (
            <div style={{ marginBottom: 16 }}>
              {tags.map((t) => (
                <Tag key={t} color="blue">{t}</Tag>
              ))}
            </div>
          )}
          <div style={{ marginBottom: 16, color: '#888', fontSize: 13, display: 'flex', flexWrap: 'wrap', gap: 16 }}>
            {wf.ownerName && <span>作者：{wf.ownerName}</span>}
            {wf.publishedAt && (
              <span>发布于：{new Date(wf.publishedAt).toLocaleDateString('zh-CN')}</span>
            )}
            <span>节点数：{wf.nodeCount}</span>
            <span>克隆次数：{wf.cloneCount}</span>
            {wf.byteSize > 0 && (
              <span>资源大小：{(wf.byteSize / 1024 / 1024).toFixed(1)} MB</span>
            )}
          </div>
          {(wf.models ?? []).length > 0 && (
            <div style={{ marginBottom: 16 }}>
              <Typography.Text strong>使用的模型：</Typography.Text>
              <div style={{ marginTop: 6 }}>
                {(wf.models ?? []).map((m) => (
                  <Tag key={m}>{m}</Tag>
                ))}
              </div>
            </div>
          )}
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Button
              type="primary"
              size="large"
              loading={cloning}
              onClick={handleClone}
            >
              克隆到我的项目
            </Button>
            <Button
              size="large"
              onClick={() => navigate(`/share/${token}/view`)}
            >
              在画布中查看
            </Button>
          </div>
        </div>
      </div>

    </PageShell>
  );
}

function PageShell({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        minHeight: '100vh',
        background: '#000',
        padding: '40px 24px',
        color: '#f5f5f5',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 24 }}>
        <Link to="/" title="回到首页" style={{ display: 'flex', alignItems: 'center', gap: 10, textDecoration: 'none', color: 'inherit' }}>
          <BrandLogo size={30} />
          <div style={{ fontSize: 15, fontWeight: 600 }}>喵剧AI</div>
        </Link>
        <div style={{ flex: 1 }} />
      </div>
      <Card
        style={{
          maxWidth: 960,
          margin: '0 auto',
          background: '#161616',
          border: '1px solid #262626',
        }}
        styles={{ body: { padding: 24 } }}
      >
        {children}
      </Card>
    </div>
  );
}
