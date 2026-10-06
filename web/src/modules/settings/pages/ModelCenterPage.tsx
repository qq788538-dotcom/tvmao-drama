import { useState } from 'react';
import {
  App as AntApp, Button, Card, Collapse, Descriptions, Drawer, Form, Input, InputNumber, Segmented, Select, Space, Switch, Table, Tag, Tooltip, Typography,
} from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { errorText, listModels, patchModel, restoreModel, type ModelPatch, type ModelView } from '../api';
import { ReadonlyJson } from '../components/ReadonlyJson';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { filterModels, formToPatch, MODEL_FIELD_LABELS, modelToForm, TYPE_OPTIONS, type ModelFormValues } from './modelForm';

export function ModelCenterPage() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const [type, setType] = useState('all');
  const [keyword, setKeyword] = useState('');
  const [editing, setEditing] = useState<ModelView | null>(null);
  const [form] = Form.useForm<ModelFormValues>();
  const { data = [], isLoading } = useQuery({ queryKey: ['settings-models'], queryFn: listModels });

  const replace = (m: ModelView) => qc.setQueryData<ModelView[]>(['settings-models'], (old) => (old ?? []).map((x) => (x.id === m.id ? m : x)));
  const patch = useMutation({
    mutationFn: ({ id, p }: { id: number; p: ModelPatch }) => patchModel(id, p),
    onSuccess: (m) => {
      replace(m);
      message.success('已保存');
      setEditing((cur) => (cur && cur.id === m.id ? null : cur));
    },
    onError: (e) => message.error(errorText(e)),
  });
  const restore = useMutation({
    mutationFn: (id: number) => restoreModel(id),
    onSuccess: (m) => {
      replace(m);
      message.success('已恢复默认');
      setEditing((cur) => (cur && cur.id === m.id ? null : cur));
    },
    onError: (e) => message.error(errorText(e)),
  });

  const openEditor = (m: ModelView) => {
    setEditing(m);
    form.setFieldsValue(modelToForm(m));
  };
  const submit = (v: ModelFormValues) => {
    if (!editing) return;
    const p = formToPatch(editing, v);
    if (Object.keys(p).length === 0) {
      setEditing(null);
      return;
    }
    patch.mutate({ id: editing.id, p });
  };

  const rows = filterModels(data, type, keyword);
  return (
    <Card title="模型中心">
      <Typography.Paragraph type="secondary">
        模型全部内置，可以启用 / 隐藏、改显示名和排序、调整参数默认值、并发与重试；请求模板等技术参数只读。
      </Typography.Paragraph>
      <Space style={{ marginBottom: 12 }} wrap>
        <Segmented options={TYPE_OPTIONS} value={type} onChange={(v) => setType(String(v))} />
        <Input.Search allowClear placeholder="搜索名称或 Model ID" onChange={(e) => setKeyword(e.target.value)} style={{ width: 260 }} />
      </Space>
      <Table<ModelView>
        rowKey="id"
        loading={isLoading}
        dataSource={rows}
        pagination={false}
        size="middle"
        columns={[
          {
            title: '名称',
            render: (_, m) => (
              <Space direction="vertical" size={0}>
                <span>
                  {m.name} {m.modifiedFields.length > 0 && <Tag color="blue">已修改</Tag>}
                </span>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {m.modelId}
                </Typography.Text>
              </Space>
            ),
          },
          { title: '类型', dataIndex: 'type', width: 80 },
          {
            title: '启用',
            width: 80,
            render: (_, m) => {
              const blocked = m.enabled && m.references.length > 0;
              const sw = (
                <Switch
                  size="small"
                  checked={m.enabled}
                  disabled={blocked}
                  loading={patch.isPending && patch.variables?.id === m.id}
                  onChange={(checked) => patch.mutate({ id: m.id, p: { enabled: checked } })}
                />
              );
              return blocked ? <Tooltip title={`正被使用：${m.references.join('、')}`}>{sw}</Tooltip> : sw;
            },
          },
          {
            title: '隐藏',
            width: 80,
            render: (_, m) => <Switch size="small" checked={m.hidden} onChange={(checked) => patch.mutate({ id: m.id, p: { hidden: checked } })} />,
          },
          { title: '排序', dataIndex: 'sortOrder', width: 70 },
          {
            title: '被使用',
            render: (_, m) => m.references.map((r) => <Tag key={r}>{r}</Tag>),
          },
          {
            title: '操作',
            width: 170,
            render: (_, m) => (
              <Space>
                <Button size="small" onClick={() => openEditor(m)}>
                  编辑
                </Button>
                <RestoreDefaultButton onConfirm={() => restore.mutate(m.id)} disabled={m.modifiedFields.length === 0} />
              </Space>
            ),
          },
        ]}
      />

      <Drawer width={640} open={!!editing} onClose={() => setEditing(null)} title={editing ? `编辑：${editing.name}` : ''} destroyOnClose
        extra={<Button type="primary" onClick={() => form.submit()} loading={patch.isPending}>保存</Button>}>
        {editing && (
          <>
            {editing.modifiedFields.length > 0 && (
              <Typography.Paragraph type="secondary">
                已修改：{editing.modifiedFields.map((f) => MODEL_FIELD_LABELS[f] ?? f).join('、')}
              </Typography.Paragraph>
            )}
            <Form form={form} layout="vertical" onFinish={submit}>
              <Form.Item label="显示名称" name="name" rules={[{ required: true, whitespace: true, message: '显示名不能为空' }, { max: 64 }]}>
                <Input />
              </Form.Item>
              <Space size={24}>
                <Form.Item label="启用" name="enabled" valuePropName="checked">
                  <Switch disabled={editing.enabled && editing.references.length > 0} />
                </Form.Item>
                <Form.Item label="隐藏（画布模型列表中不显示）" name="hidden" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item label="排序（小的在前）" name="sortOrder">
                  <InputNumber min={-1000000} max={1000000} />
                </Form.Item>
              </Space>
              {Object.entries(editing.uiSchema ?? {}).map(([key, field]) => (
                <Form.Item key={key} label={`默认${field.label ?? key}`} name={['uiDefaults', key]}>
                  <Select style={{ width: 240 }} options={(field.options ?? []).map((o) => ({ value: o.value, label: o.label ?? String(o.value) }))} />
                </Form.Item>
              ))}
              <Form.Item label="并发上限" name="concurrency" extra="同一模型同时进行的生成任务数上限，0 = 使用默认值。">
                <InputNumber min={0} max={200} />
              </Form.Item>
              <Space size={24}>
                <Form.Item label="最大重试次数" name="canvasMaxRetries" extra="上游失败后自动重试的次数，0 = 不重试。">
                  <InputNumber min={0} max={10} />
                </Form.Item>
                <Form.Item label="重试间隔（秒）" name="canvasRetryIntervalSec" extra="0 = 使用内置间隔。">
                  <InputNumber min={0} max={600} />
                </Form.Item>
              </Space>
            </Form>
            <Collapse
              style={{ marginTop: 8 }}
              items={[
                {
                  key: 'tech',
                  label: '技术参数（只读）',
                  children: (
                    <Space direction="vertical" style={{ width: '100%' }}>
                      <Descriptions column={1} size="small">
                        <Descriptions.Item label="Model ID">{editing.modelId}</Descriptions.Item>
                        <Descriptions.Item label="上游路径">{editing.basePath}</Descriptions.Item>
                        <Descriptions.Item label="图片输入格式">{editing.imageInputFormat}</Descriptions.Item>
                        <Descriptions.Item label="图片请求模式">{editing.imageRequestMode}</Descriptions.Item>
                      </Descriptions>
                      <Typography.Text type="secondary">请求模板</Typography.Text>
                      <ReadonlyJson value={editing.requestTemplate} />
                      <Typography.Text type="secondary">轮询模板</Typography.Text>
                      <ReadonlyJson value={editing.pollingTemplate} />
                      <Typography.Text type="secondary">参数定义（uiSchema）</Typography.Text>
                      <ReadonlyJson value={editing.uiSchema} />
                      <Typography.Text type="secondary">输入数量限制</Typography.Text>
                      <ReadonlyJson value={editing.genControl?.inputLimits ?? null} />
                    </Space>
                  ),
                },
              ]}
            />
          </>
        )}
      </Drawer>
    </Card>
  );
}
