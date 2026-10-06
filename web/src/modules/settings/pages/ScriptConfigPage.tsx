import { useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Empty, Form, Input, InputNumber, Space, Spin, Table, Tag, Typography } from 'antd';
import { useConfigEditor } from '../useConfigEditor';
import { deepClone, updateAt } from '../draft';
import { ModelSelect } from '../components/ModelSelect';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { DEFAULT_MAX_RETRIES, moveItem, scriptErrors, type ScriptConfig, type ScriptStyle } from './candidates';

export function ScriptConfigPage() {
  const { message } = AntApp.useApp();
  const { data, isLoading, save, restore } = useConfigEditor<ScriptConfig>('canvas_script_config');
  const [draft, setDraft] = useState<ScriptConfig | null>(null);
  useEffect(() => setDraft(data?.value ? deepClone(data.value) : null), [data]);
  const set = (path: (string | number)[], v: unknown) => setDraft((d) => (d ? updateAt(d, path, v) : d));

  if (isLoading) return <Spin />;
  if (!draft) return <Card title="剧本生成分镜"><Empty description="没有内置此配置" /></Card>;
  const cands = draft.modelCandidates ?? [];
  const onSave = () => {
    const errs = scriptErrors(draft);
    if (errs.length) return message.error(errs[0]);
    save.mutate(draft);
  };
  return (
    <Card
      title={<Space>剧本生成分镜{data?.modified && <Tag color="blue">已修改</Tag>}</Space>}
      extra={<Space><RestoreDefaultButton onConfirm={() => restore.mutate()} loading={restore.isPending} disabled={!data?.modified} /><Button type="primary" onClick={onSave} loading={save.isPending}>保存</Button></Space>}
    >
      <Typography.Paragraph type="secondary">脚本节点可选的文本模型（第一个为默认）、本机同时执行的任务数和失败重试次数。风格由内置 Skill 决定，不可修改。</Typography.Paragraph>
      <Form layout="vertical">
        <Form.Item label="候选模型">
          <Space direction="vertical">
            {cands.map((c, i) => (
              <Space key={i}>
                <ModelSelect type="text" value={c.modelId} onChange={(v) => set(['modelCandidates', i, 'modelId'], v)} />
                <Input style={{ width: 160 }} placeholder="展示名" value={c.displayName} onChange={(e) => set(['modelCandidates', i, 'displayName'], e.target.value)} />
                <Button size="small" disabled={i === 0} onClick={() => set(['modelCandidates'], moveItem(cands, i, i - 1))}>上移</Button>
                <Button size="small" danger disabled={cands.length <= 1} onClick={() => set(['modelCandidates'], cands.filter((_, j) => j !== i))}>移除</Button>
              </Space>
            ))}
            <Button size="small" onClick={() => set(['modelCandidates'], [...cands, { modelId: '', displayName: '' }])}>添加候选模型</Button>
          </Space>
        </Form.Item>
        <Space size={24}>
          <Form.Item label="同时执行的任务数">
            <InputNumber min={1} max={20} precision={0} value={draft.concurrency ?? 1} onChange={(v) => set(['concurrency'], v ?? 1)} />
          </Form.Item>
          <Form.Item label="失败自动重跑次数" help={`1 到 10 次，未设置时为 ${DEFAULT_MAX_RETRIES} 次`}>
            <InputNumber min={1} max={10} precision={0} value={draft.maxRetries ?? DEFAULT_MAX_RETRIES} onChange={(v) => set(['maxRetries'], v ?? DEFAULT_MAX_RETRIES)} />
          </Form.Item>
        </Space>
      </Form>
      <Typography.Title level={5}>风格（只读）</Typography.Title>
      <Table<ScriptStyle>
        size="small"
        rowKey={(s) => `${s.name} ${s.version}`}
        pagination={false}
        dataSource={draft.styles ?? []}
        columns={[
          { title: '风格', dataIndex: 'name' },
          { title: '版本', dataIndex: 'version' },
          { title: '状态', render: (_, s) => (s.enabled ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>) },
        ]}
      />
    </Card>
  );
}
