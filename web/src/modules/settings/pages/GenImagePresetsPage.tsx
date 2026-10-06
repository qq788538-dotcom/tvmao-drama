import { useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Collapse, Empty, Form, Input, Space, Spin, Tag, Typography } from 'antd';
import { useConfigEditor } from '../useConfigEditor';
import { deepClone, updateAt } from '../draft';
import { ModelSelect } from '../components/ModelSelect';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { groupByCategory, presetErrors, type GenImagePresetsConfig } from './presets';

export function GenImagePresetsPage() {
  const { message } = AntApp.useApp();
  const { data, isLoading, save, restore } = useConfigEditor<GenImagePresetsConfig>('gen_image_prompt_presets');
  const [draft, setDraft] = useState<GenImagePresetsConfig | null>(null);
  useEffect(() => setDraft(data?.value ? deepClone(data.value) : null), [data]);
  const set = (path: (string | number)[], v: unknown) => setDraft((d) => (d ? updateAt(d, path, v) : d));

  if (isLoading) return <Spin />;
  if (!draft) return <Card title="绘图预设"><Empty description="没有内置此配置" /></Card>;
  const onSave = () => {
    const errs = presetErrors(draft);
    if (errs.length) return message.error(errs[0]);
    save.mutate(draft);
  };
  return (
    <Card
      title={<Space>绘图预设{data?.modified && <Tag color="blue">已修改</Tag>}</Space>}
      extra={<Space><RestoreDefaultButton onConfirm={() => restore.mutate()} loading={restore.isPending} disabled={!data?.modified} /><Button type="primary" onClick={onSave} loading={save.isPending}>保存</Button></Space>}
    >
      <Typography.Paragraph type="secondary">
        绘图节点选中下列模型时显示预设菜单。预设提示词在本机拼接后发送给模型，可以直接修改。
      </Typography.Paragraph>
      <Form layout="vertical">
        <Form.Item label="适用模型">
          <ModelSelect type="image" multiple value={draft.models} onChange={(v) => set(['models'], v)} style={{ width: '100%' }} />
        </Form.Item>
      </Form>
      <Collapse
        items={groupByCategory(draft).map((g) => ({
          key: g.category,
          label: `${g.category}（${g.items.length}）`,
          children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
              {g.items.map(({ index, preset }) => (
                <div key={preset.key}>
                  <Typography.Text strong>{preset.value.name ?? preset.key}</Typography.Text>
                  {preset.value.placeholder && (
                    <Typography.Text type="secondary" style={{ marginLeft: 8 }}>输入提示：{preset.value.placeholder}</Typography.Text>
                  )}
                  <Input.TextArea
                    style={{ marginTop: 6 }}
                    autoSize={{ minRows: 2, maxRows: 10 }}
                    value={preset.value.prompt ?? ''}
                    onChange={(e) => set(['presets', index, 'value', 'prompt'], e.target.value)}
                  />
                  {(preset.value.params ?? []).map((param, pi) => (
                    <div key={param.key} style={{ marginTop: 8, paddingLeft: 12 }}>
                      <Typography.Text type="secondary">参数「{param.name}」各档位附加词（可为空）</Typography.Text>
                      {param.options.map((opt, oi) => (
                        <Input
                          key={opt.key}
                          addonBefore={opt.name}
                          style={{ marginTop: 4 }}
                          value={opt.prompt ?? ''}
                          onChange={(e) => set(['presets', index, 'value', 'params', pi, 'options', oi, 'prompt'], e.target.value)}
                        />
                      ))}
                    </div>
                  ))}
                </div>
              ))}
            </Space>
          ),
        }))}
      />
    </Card>
  );
}
