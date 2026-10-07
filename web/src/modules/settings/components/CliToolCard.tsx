import { Alert, App as AntApp, Button, Card, Descriptions, Space, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { cliToolStatus, errorText, installCliTool, uninstallCliTool, type CliToolStatus } from '../api';

const CODEX_CONFIG = `# ~/.codex/config.toml
sandbox_mode = "workspace-write"

[sandbox_workspace_write]
network_access = true`;

/** 设置 → 配置 → 命令行工具（CLI spec 7.2）。 */
export function CliToolCard() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['cli-tool-status'], queryFn: cliToolStatus });
  const onDone = (s: CliToolStatus) => qc.setQueryData(['cli-tool-status'], s);
  const install = useMutation({
    mutationFn: installCliTool,
    onSuccess: (s) => {
      onDone(s);
      message.success('已安装，重新打开终端后输入 miaoju --help');
    },
    onError: (e) => message.error(errorText(e)),
  });
  const uninstall = useMutation({
    mutationFn: uninstallCliTool,
    onSuccess: (s) => {
      onDone(s);
      message.success('已卸载');
    },
    onError: (e) => message.error(errorText(e)),
  });
  const s = q.data;
  const busy = install.isPending || uninstall.isPending;
  const tag = !s ? null : !s.installed ? (
    <Tag>未安装</Tag>
  ) : s.upToDate ? (
    <Tag color="green">已安装</Tag>
  ) : (
    <Tag color="orange">版本不一致</Tag>
  );

  return (
    <Card
      title={<Space>命令行工具{tag}</Space>}
      extra={
        <Space>
          {s?.installed && (
            <Button disabled={busy} onClick={() => uninstall.mutate()}>
              卸载
            </Button>
          )}
          <Button type="primary" loading={install.isPending} disabled={busy || !s} onClick={() => install.mutate()}>
            {s?.installed ? '重新安装' : '安装'}
          </Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary">
        安装后可在终端里用 <Typography.Text code>miaoju</Typography.Text> 命令操作当前画布，也可以交给 AI 助手（Claude Code、Codex 等）使用。使用时喵剧必须保持打开。
      </Typography.Paragraph>
      {s && (
        <Descriptions size="small" column={1}>
          <Descriptions.Item label="命令位置">
            <Typography.Text copyable>{s.commandPath}</Typography.Text>
          </Descriptions.Item>
        </Descriptions>
      )}
      {install.isSuccess && (
        <Alert type="success" showIcon message="重新打开终端后输入 miaoju --help 验证" style={{ marginBottom: 12 }} />
      )}
      {s && (
        <>
          <Typography.Title level={5}>复制给 AI 助手</Typography.Title>
          <Typography.Paragraph copyable>{`请阅读 ${s.skillEntry}，学会使用喵剧 CLI`}</Typography.Paragraph>
        </>
      )}
      <Typography.Title level={5}>Codex 用户</Typography.Title>
      <Typography.Paragraph type="secondary">
        Codex 默认禁止沙箱内联网，不加下面这段配置的话，每次调用都要审批一次：
      </Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: CODEX_CONFIG }}>
        <pre style={{ margin: 0 }}>{CODEX_CONFIG}</pre>
      </Typography.Paragraph>
    </Card>
  );
}
