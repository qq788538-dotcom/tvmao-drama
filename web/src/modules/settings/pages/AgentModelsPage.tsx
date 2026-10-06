import { App as AntApp, Card, Select, Space, Table, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { errorText, getAgentModels, restoreAgentModel, switchAgentModel, type AgentModelItem, type AgentModelsView } from '../api';
import { invalidateAfterSettingsChange } from '../invalidate';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { candidateOptions, roleLabel } from './agentRoles';

export function AgentModelsPage() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ['settings-agent-models'], queryFn: getAgentModels });
  const onDone = (v: AgentModelsView, msg: string) => {
    qc.setQueryData(['settings-agent-models'], v);
    invalidateAfterSettingsChange(qc); // 模型页“被使用”标记、画布侧依赖模型的缓存
    message.success(msg);
  };
  const sw = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) => switchAgentModel(id, name),
    onSuccess: (v) => onDone(v, '已切换'),
    onError: (e) => message.error(errorText(e)),
  });
  const restore = useMutation({
    mutationFn: (id: number) => restoreAgentModel(id),
    onSuccess: (v) => onDone(v, '已恢复默认'),
    onError: (e) => message.error(errorText(e)),
  });

  return (
    <Card title="Agent 模型">
      <Typography.Paragraph type="secondary">
        Agent 对话使用的大模型。只能在模型中心里已启用的文本模型之间切换，其余参数为内置值、不可修改。
      </Typography.Paragraph>
      <Table<AgentModelItem>
        rowKey="id"
        loading={isLoading}
        pagination={false}
        dataSource={data?.data ?? []}
        columns={[
          {
            title: '用途',
            render: (_, it) => (
              <Space>
                {roleLabel(it.role)}
                {it.modified && <Tag color="blue">已修改</Tag>}
              </Space>
            ),
          },
          { title: '名称', dataIndex: 'alias' },
          {
            title: '模型',
            render: (_, it) => (
              <Select
                style={{ minWidth: 280 }}
                value={it.modelName}
                options={data ? candidateOptions(data, it.modelName) : []}
                loading={sw.isPending && sw.variables?.id === it.id}
                onChange={(name) => sw.mutate({ id: it.id, name })}
              />
            ),
          },
          {
            title: '操作',
            width: 110,
            render: (_, it) => <RestoreDefaultButton onConfirm={() => restore.mutate(it.id)} disabled={!it.modified} />,
          },
        ]}
      />
    </Card>
  );
}
