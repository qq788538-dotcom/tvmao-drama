import { Alert, App as AntApp, Button, Card, Descriptions, Space, Tabs, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  cliToolStatus,
  errorText,
  installCliTool,
  uninstallCliTool,
  type CliSkillStatus,
  type CliToolStatus,
} from '../api';

const CODEX_CONFIG = `# ~/.codex/config.toml
sandbox_mode = "workspace-write"

[sandbox_workspace_write]
network_access = true`;

const INSTALLED_HINT = '重新打开终端后输入 miaoju --help 验证；在 Claude Code、Codex 里新开会话即可使用。';

const AGENTS: Record<string, { name: string; dir: string }> = {
  claude: { name: 'Claude Code', dir: '~/.claude' },
  codex: { name: 'Codex', dir: '~/.codex' },
};

function skillText(s: CliSkillStatus): string {
  const a = AGENTS[s.agent] ?? { name: s.agent, dir: s.agent };
  if (s.error) return `${a.name}：${s.installed ? '操作失败' : '安装失败'}：${s.error}`;
  if (!s.available) return `${a.name}：未检测到 ${a.name}（${a.dir} 不存在）`;
  if (s.conflict) return `${a.name}：已存在同名目录，未覆盖：${s.path}`;
  if (s.installed && s.outdated) return `${a.name}：版本不一致，需要重新安装`;
  return `${a.name}：${s.installed ? '已安装' : '未安装'}`;
}

/** 设置 → 命令行工具（CLI spec 7.2）。 */
export function CliToolCard() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['cli-tool-status'], queryFn: cliToolStatus });
  const onDone = (s: CliToolStatus) => qc.setQueryData(['cli-tool-status'], s);
  const install = useMutation({
    mutationFn: installCliTool,
    onSuccess: (s) => {
      onDone(s);
      message.success(INSTALLED_HINT);
    },
    onError: (e) => {
      message.error(errorText(e));
      // 失败前可能已装好一部分（命令、PATH），刷新状态
      void qc.invalidateQueries({ queryKey: ['cli-tool-status'] });
    },
  });
  const uninstall = useMutation({
    mutationFn: uninstallCliTool,
    onSuccess: (s) => {
      onDone(s);
      message.success('已卸载');
    },
    onError: (e) => {
      message.error(errorText(e));
      void qc.invalidateQueries({ queryKey: ['cli-tool-status'] });
    },
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

  const buttonTab = (
    <>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" loading={install.isPending} disabled={busy || !s} onClick={() => install.mutate()}>
          {s?.installed ? '重新安装' : '安装'}
        </Button>
        {s?.installed && (
          <Button disabled={busy} onClick={() => uninstall.mutate()}>
            卸载
          </Button>
        )}
      </Space>
      {install.isSuccess && <Alert type="success" showIcon message={INSTALLED_HINT} style={{ marginBottom: 12 }} />}
      {s && (
        <Descriptions size="small" column={1}>
          <Descriptions.Item label="命令位置">
            <Typography.Text copyable>{s.commandPath}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="AI 助手 skill">
            <Space direction="vertical" size={0}>
              {(s.skills ?? []).map((k) => (
                <Typography.Text
                  key={k.agent}
                  type={k.error ? 'danger' : k.conflict || k.outdated ? 'warning' : undefined}
                >
                  {skillText(k)}
                </Typography.Text>
              ))}
            </Space>
          </Descriptions.Item>
        </Descriptions>
      )}
    </>
  );

  const agentTab = (
    <>
      <Typography.Paragraph type="secondary">
        把下面这段话发给 Claude Code 或 Codex，它会自己完成安装。Codex 会在写入时请求你批准。
      </Typography.Paragraph>
      {s && (
        <Typography.Paragraph copyable={{ text: s.agentPrompt }}>
          <pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{s.agentPrompt}</pre>
        </Typography.Paragraph>
      )}
    </>
  );

  return (
    <Card title={<Space>命令行工具{tag}</Space>}>
      <Typography.Paragraph type="secondary">
        让终端或 AI 助手（Claude Code、Codex）直接操作喵剧画布：建节点、接线、运行生成、做合规校验，改动会自动出现在打开的画布上。使用时喵剧必须保持打开。
      </Typography.Paragraph>
      <Typography.Paragraph type="secondary">
        装好后在终端运行 <Typography.Text code>miaoju --help</Typography.Text>；或者在 Claude Code、Codex
        里新开一个会话，直接说需求，例如“用喵剧在当前项目里建一个图片节点，并接到视频生成节点上”。
      </Typography.Paragraph>
      <Tabs
        defaultActiveKey="button"
        items={[
          { key: 'button', label: '一键安装', children: buttonTab },
          { key: 'agent', label: '让 AI 助手安装', children: agentTab },
        ]}
      />
      <Typography.Title level={5}>Codex 用户须知</Typography.Title>
      <Typography.Paragraph type="secondary">
        Codex 默认禁止沙箱内联网，不加下面这段配置的话，每次调用都要审批一次：
      </Typography.Paragraph>
      <Typography.Paragraph copyable={{ text: CODEX_CONFIG }}>
        <pre style={{ margin: 0 }}>{CODEX_CONFIG}</pre>
      </Typography.Paragraph>
    </Card>
  );
}
