import { useEffect } from 'react';
import { BASE_URL_HINT } from './hints';
import { App as AntApp, Button, Card, Form, Input, Space, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient, type UseMutationResult } from '@tanstack/react-query';
import { errorText, getGeneral, restoreBaseDomain, updateUpstream, type GeneralSettings, type UpstreamUpdate } from '../api';
import { RestoreDefaultButton } from './RestoreDefaultButton';

interface UpstreamForm {
  baseDomain: string;
  apiKey?: string;
}

export function buildUpstreamUpdate(values: UpstreamForm, initial: GeneralSettings): UpstreamUpdate | null {
  const out: UpstreamUpdate = {};
  const bd = values.baseDomain.trim();
  if (bd !== initial.baseDomain) out.baseDomain = bd;
  const key = (values.apiKey ?? '').trim();
  if (key) out.apiKey = key;
  return Object.keys(out).length ? out : null;
}

/** 全局上游服务（地址 + API Key + 恢复默认地址）。放在模型中心顶部；没有单独配置上游的模型都走它。 */
export function UpstreamCard() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<UpstreamForm>();
  const general = useQuery({ queryKey: ['settings-general'], queryFn: getGeneral });

  useEffect(() => {
    if (general.data) form.setFieldsValue({ baseDomain: general.data.baseDomain, apiKey: '' });
  }, [general.data, form]);

  const onSaved = (g: GeneralSettings) => {
    qc.setQueryData(['settings-general'], g);
    form.setFieldsValue({ baseDomain: g.baseDomain, apiKey: '' });
  };
  // 变量里带明文 Key：不在 mutation 缓存里保留。
  const save: UseMutationResult<GeneralSettings, Error, UpstreamUpdate> = useMutation({
    gcTime: 0,
    mutationFn: updateUpstream,
    onSuccess: (g) => {
      onSaved(g);
      message.success('已保存');
    },
    onError: (e) => message.error(errorText(e)),
    onSettled: () => save.reset(),
  });
  const clearKey = useMutation({
    mutationFn: () => updateUpstream({ clearApiKey: true }),
    onSuccess: (g) => {
      onSaved(g);
      message.success('已清除 API Key');
    },
    onError: (e) => message.error(errorText(e)),
  });
  const restore = useMutation({
    mutationFn: restoreBaseDomain,
    onSuccess: (g) => {
      onSaved(g);
      message.success('已恢复默认地址');
    },
    onError: (e) => message.error(errorText(e)),
  });

  const onSubmit = (values: UpstreamForm) => {
    if (!general.data) return;
    const update = buildUpstreamUpdate(values, general.data);
    if (!update) {
      message.info('没有修改');
      return;
    }
    save.mutate(update);
  };

  const g = general.data;
  return (
    <Card
      title={
        <Space>
          上游服务（全局）
          {g?.baseDomainModified && <Tag color="blue">已修改</Tag>}
        </Space>
      }
      extra={<RestoreDefaultButton label="恢复默认地址" onConfirm={() => restore.mutate()} loading={restore.isPending} disabled={!g?.baseDomainModified} />}
    >
      <Typography.Paragraph type="secondary">没有单独配置上游的模型，都通过这个地址调用（newapi 网关），共用同一个 API Key。</Typography.Paragraph>
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <Form.Item label="上游地址" name="baseDomain" extra={BASE_URL_HINT} rules={[{ required: true, message: '请填写上游地址' }]}>
          <Input placeholder="例如 https://your-gateway.example.com" autoComplete="off" />
        </Form.Item>
        <Form.Item
          label="API Key"
          name="apiKey"
          extra={
            g?.apiKeySet ? (
              <Space>
                <span>当前：{g.apiKeyMasked}</span>
                <Button size="small" danger onClick={() => clearKey.mutate()} loading={clearKey.isPending}>
                  清除
                </Button>
              </Space>
            ) : (
              '尚未设置。Key 以明文保存在本机数据目录的 data.db 中，请勿把数据目录发给他人。'
            )
          }
        >
          <Input.Password placeholder={g?.apiKeySet ? '留空表示不修改' : 'sk-...'} autoComplete="new-password" />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={save.isPending}>
          保存
        </Button>
      </Form>
    </Card>
  );
}
