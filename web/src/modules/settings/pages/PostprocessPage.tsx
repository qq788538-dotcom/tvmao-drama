import { useEffect, useState } from 'react';
import { App as AntApp, Button, Card, Empty, Form, Input, Space, Spin, Tag, Typography } from 'antd';
import { useConfigEditor } from '../useConfigEditor';
import { deepClone, updateAt } from '../draft';
import { ModelSelect } from '../components/ModelSelect';
import { RestoreDefaultButton } from '../components/RestoreDefaultButton';
import { postprocessErrors, postprocessPayload, upscaleMode, type PostprocessConfig } from './candidates';

export function PostprocessPage() {
  const { message } = AntApp.useApp();
  const { data, isLoading, save, restore } = useConfigEditor<PostprocessConfig>('canvas_postprocess_config');
  const [draft, setDraft] = useState<PostprocessConfig | null>(null);
  useEffect(() => setDraft(data?.value ? deepClone(data.value) : null), [data]);
  const set = (path: (string | number)[], v: unknown) => setDraft((d) => (d ? updateAt(d, path, v) : d));

  if (isLoading) return <Spin />;
  if (!draft) return <Card title="视频后处理"><Empty description="没有内置此配置" /></Card>;
  const mode = upscaleMode(draft);
  const onSave = () => {
    const errs = postprocessErrors(draft);
    if (errs.length) return message.error(errs[0]);
    save.mutate(postprocessPayload(data?.value ?? draft, draft));
  };
  return (
    <Card
      title={<Space>视频后处理{data?.modified && <Tag color="blue">已修改</Tag>}</Space>}
      extra={<Space><RestoreDefaultButton onConfirm={() => restore.mutate()} loading={restore.isPending} disabled={!data?.modified} /><Button type="primary" onClick={onSave} loading={save.isPending}>保存</Button></Space>}
    >
      <Typography.Paragraph type="secondary">
        配置画布「擦除字幕」「视频转高清」「视频转白模」可用的模型。档位由所选模型的参数定义决定；未配置的功能在画布上显示「未配置」。
      </Typography.Paragraph>
      <Form layout="vertical">
        <Form.Item label="去字幕">
          <Space direction="vertical" style={{ width: '100%' }}>
            {(draft.subtitleRemovalModels ?? []).map((b, i) => (
              <Space key={b.modelId} wrap>
                <Typography.Text code>{b.modelId}</Typography.Text>
                <Input style={{ width: 140 }} placeholder="显示名" value={b.label} onChange={(e) => set(['subtitleRemovalModels', i, 'label'], e.target.value)} />
                <Input style={{ width: 220 }} placeholder="说明" value={b.subtitle ?? ''} onChange={(e) => set(['subtitleRemovalModels', i, 'subtitle'], e.target.value)} />
                {b.isTimeLimited && <Tag>限时（只读）</Tag>}
              </Space>
            ))}
          </Space>
        </Form.Item>
        <Form.Item label="转高清">
          {mode === 'list' && (
            <Space direction="vertical">
              {(draft.upscaleModels ?? []).map((b, i) => (
                <Typography.Text key={i}>
                  {b.label || b.modelId}（{b.modelId}）<Tag style={{ marginLeft: 8 }}>只读</Tag>
                </Typography.Text>
              ))}
            </Space>
          )}
          {mode === 'legacy' && <ModelSelect type="video" value={draft.upscaleModel} onChange={(v) => set(['upscaleModel'], v)} />}
          {mode === 'none' && <Typography.Text type="secondary">未内置转高清模型</Typography.Text>}
        </Form.Item>
        <Form.Item label="转白模">
          <Typography.Text type={draft.whiteModelModel ? undefined : 'secondary'}>
            {draft.whiteModelModel || '未配置'}
          </Typography.Text>
          <Tag style={{ marginLeft: 8 }}>只读</Tag>
        </Form.Item>
      </Form>
    </Card>
  );
}
