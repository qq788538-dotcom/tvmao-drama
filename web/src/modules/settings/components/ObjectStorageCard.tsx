import { useEffect, useState } from 'react';
import { Alert, App as AntApp, Button, Card, Form, Input, Modal, Select, Space, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  clearObjectStorage,
  errorText,
  getObjectStorage,
  saveObjectStorage,
  testObjectStorage,
  type ObjectStorageInput,
  type ObjectStorageProvider,
  type ProbeResult,
} from '../api';

const EXAMPLES: Record<ObjectStorageProvider, { endpoint: string; region: string }> = {
  tos: { endpoint: '例：tos-cn-beijing.volces.com', region: '例：cn-beijing' },
  oss: { endpoint: '例：oss-cn-hangzhou.aliyuncs.com', region: '例：cn-hangzhou' },
};

const STEP_LABEL: Record<string, string> = {
  config: '配置', upload: '上传', presign: '签名', download: '公网下载', delete: '删除',
};

/** 视频、音频参考素材上传到用户自己的对象存储（对象存储设计 §3.4）。 */
export function ObjectStorageCard() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['settings-object-storage'], queryFn: getObjectStorage });
  const [form] = Form.useForm<ObjectStorageInput>();
  const provider = (Form.useWatch('provider', form) ?? 'tos') as ObjectStorageProvider;
  const [probe, setProbe] = useState<ProbeResult | null>(null);

  useEffect(() => {
    const v = q.data;
    if (!v) return;
    form.setFieldsValue({
      provider: (v.provider || 'tos') as ObjectStorageProvider,
      endpoint: v.endpoint, region: v.region, bucket: v.bucket,
      prefix: v.prefix || 'miaoju-inputs', accessKey: v.accessKey, secretKey: '',
    });
  }, [q.data, form]);

  const values = (): ObjectStorageInput => {
    const f = form.getFieldsValue();
    return { ...f, prefix: f.prefix ?? '', secretKey: f.secretKey ?? '' };
  };

  const save = useMutation({
    mutationFn: () => saveObjectStorage(values()),
    onSuccess: (v) => {
      qc.setQueryData(['settings-object-storage'], v);
      message.success('对象存储配置已保存');
    },
    onError: (e) => message.error(errorText(e)),
  });
  const test = useMutation({
    mutationFn: () => testObjectStorage(values()),
    onMutate: () => setProbe(null),
    onSuccess: setProbe,
    onError: (e) => setProbe({ ok: false, message: errorText(e) }),
  });
  const clear = useMutation({
    mutationFn: clearObjectStorage,
    onSuccess: (v) => {
      qc.setQueryData(['settings-object-storage'], v);
      form.resetFields();
      message.success('已清除对象存储配置');
    },
    onError: (e) => message.error(errorText(e)),
  });

  const confirmClear = () =>
    Modal.confirm({
      title: '清除对象存储配置',
      content: '清除后，带本机视频或音频参考素材的生成将无法提交。已上传到桶里的文件不会被删除。',
      okText: '清除',
      okButtonProps: { danger: true },
      onOk: () => clear.mutateAsync(),
    });

  const v = q.data;
  return (
    <Card
      title="对象存储"
      extra={v?.configured ? <Tag color="green">已配置</Tag> : <Tag>未配置</Tag>}
    >
      <Typography.Paragraph type="secondary">
        视频、音频参考素材会上传到你自己的对象存储，并用 24 小时有效的私有链接交给模型。建议给桶设置生命周期规则（如 7 天自动删除）以控制费用。
      </Typography.Paragraph>
      <Form form={form} layout="vertical" initialValues={{ provider: 'tos', prefix: 'miaoju-inputs' }}>
        <Form.Item label="服务商" name="provider">
          <Select
            options={[
              { value: 'tos', label: '火山引擎 TOS' },
              { value: 'oss', label: '阿里云 OSS' },
            ]}
          />
        </Form.Item>
        <Form.Item label="Endpoint" name="endpoint">
          <Input placeholder={EXAMPLES[provider].endpoint} />
        </Form.Item>
        <Form.Item label="Region" name="region">
          <Input placeholder={EXAMPLES[provider].region} />
        </Form.Item>
        <Form.Item label="Bucket" name="bucket">
          <Input />
        </Form.Item>
        <Form.Item label="路径前缀" name="prefix">
          <Input placeholder="miaoju-inputs" />
        </Form.Item>
        <Form.Item label="AccessKey" name="accessKey">
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item label="SecretKey" name="secretKey">
          <Input.Password
            autoComplete="new-password"
            placeholder={v?.secretKeySet ? `已保存 ${v.secretKeyMasked}，留空表示不修改` : ''}
          />
        </Form.Item>
      </Form>
      {probe && (
        <Alert
          style={{ marginBottom: 12 }}
          showIcon
          type={probe.ok ? 'success' : 'error'}
          message={probe.ok ? probe.message : `${probe.step ? `【${STEP_LABEL[probe.step] ?? probe.step}】` : ''}${probe.message}`}
        />
      )}
      <Space>
        <Button onClick={() => test.mutate()} loading={test.isPending}>测试连接</Button>
        <Button type="primary" onClick={() => save.mutate()} loading={save.isPending}>保存</Button>
        {v?.configured && <Button danger onClick={confirmClear}>清除配置</Button>}
      </Space>
    </Card>
  );
}
