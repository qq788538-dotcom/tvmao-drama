import { useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Empty, Form, Input, Space, Spin, Table, Tag, Typography } from 'antd';
import { useConfigEditor } from '../useConfigEditor';
import { deepClone, updateAt } from '../draft';
import { ModelSelect } from '../components/ModelSelect';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { describeAngle, templateError, type MultiAngleConfig, type MultiAnglePreset } from './promptTemplates';

export function MultiAnglePage() {
  const { message } = AntApp.useApp();
  const { data, isLoading, save, restore } = useConfigEditor<MultiAngleConfig>('canvas_multi_angle');
  const [draft, setDraft] = useState<MultiAngleConfig | null>(null);
  useEffect(() => setDraft(data?.value ? deepClone(data.value) : null), [data]);
  const set = (path: (string | number)[], v: unknown) => setDraft((d) => (d ? updateAt(d, path, v) : d));

  if (isLoading) return <Spin />;
  if (!draft) return <Card title="多角度"><Empty description="没有内置此配置" /></Card>;
  const tplErr = templateError(draft.template ?? '', '{angle}');
  const onSave = () => {
    if (tplErr) return message.error(tplErr);
    save.mutate(draft);
  };
  return (
    <Card
      title={<Space>多角度{data?.modified && <Tag color="blue">已修改</Tag>}</Space>}
      extra={<Space><RestoreDefaultButton onConfirm={() => restore.mutate()} loading={restore.isPending} disabled={!data?.modified} /><Button type="primary" onClick={onSave} loading={save.isPending}>保存</Button></Space>}
    >
      <Form layout="vertical">
        <Form.Item label="适用模型（第一个为默认）">
          <ModelSelect type="image" multiple value={draft.models} onChange={(v) => set(['models'], v)} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item label="拼接模板" validateStatus={tplErr ? 'error' : undefined} help={tplErr ?? '{angle} 会被替换成“水平 / 俯仰 / 景别”的描述；留空使用内置模板。'}>
          <Input.TextArea autoSize={{ minRows: 2, maxRows: 8 }} value={draft.template ?? ''} onChange={(e) => set(['template'], e.target.value)} />
        </Form.Item>
      </Form>
      <Typography.Title level={5}>预设</Typography.Title>
      <Table<MultiAnglePreset>
        size="small"
        rowKey="key"
        pagination={false}
        dataSource={draft.presets ?? []}
        columns={[
          { title: '名称', dataIndex: 'name', width: 120 },
          { title: '角度（只读）', width: 240, render: (_, p) => describeAngle(p) },
          {
            title: '附加提示词',
            render: (_, p, i) => (
              <Input.TextArea autoSize={{ minRows: 1, maxRows: 6 }} value={p.prompt ?? ''} placeholder="可为空" onChange={(e) => set(['presets', i, 'prompt'], e.target.value)} />
            ),
          },
        ]}
      />
    </Card>
  );
}
