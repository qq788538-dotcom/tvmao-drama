import { useState } from 'react';
import { App as AntApp, Form, Input, Modal, Upload } from 'antd';
import type { UploadProps } from 'antd';
import { Inbox } from 'lucide-react';
import { publishWorkflow, uploadAsset } from '@/shared/api';
import { mediaCrossOrigin } from '../editor/utils/mediaCrossOrigin';

interface Props {
  open: boolean;
  projectId: number;
  defaultTitle?: string;
  onClose: () => void;
  onPublished: (token: string, thumbnailUrl: string) => void;
}

interface FormValues {
  title: string;
  description?: string;
  tags?: string;
}

// 精简后的「分享链接」弹框——顶栏浮层的「分享链接」项打开它。原先这里还有
// 可见性单选（unlisted/public）和「隐藏提示词」勾选框，产品拍板砍掉：
// 公开市场从未上线过（选项一直 disabled），隐藏提示词没人用过；分享链接
// 场景本来就是「给指定的人一个可访问入口」，语义上恒等于 unlisted，没有
// 让用户选的必要。提交体固定 visibility:'unlisted' + redactPrompts:false，
// 后端 /workflows/publish 默认路径本就是这两个值，前端固定传不改变任何
// 已有行为。
export function PublishWorkflowModal({ open, projectId, defaultTitle, onClose, onPublished }: Props) {
  const { message } = AntApp.useApp();
  const [form] = Form.useForm<FormValues>();
  const [submitting, setSubmitting] = useState(false);
  const [coverUrl, setCoverUrl] = useState<string>('');

  // 自定义封面上传复用现有的 /assets 端点。我们把返回的 URL 存进本地 state
  // 并在发布时传过去；如果用户什么都不上传，后端会从项目快照里自动挑一张。
  const handleUpload: UploadProps['customRequest'] = async (opt) => {
    const file = opt.file as File;
    try {
      const asset = await uploadAsset(file, projectId);
      setCoverUrl(asset.url);
      opt.onSuccess?.(asset, new XMLHttpRequest());
    } catch (err: any) {
      message.error(err?.response?.data?.error || err?.message || '上传失败');
      opt.onError?.(err);
    }
  };

  const handleOK = async () => {
    let values: FormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSubmitting(true);
    try {
      const result = await publishWorkflow({
        projectId,
        title: values.title,
        description: values.description,
        tags: values.tags,
        visibility: 'unlisted',
        redactPrompts: false,
        thumbnailUrl: coverUrl || undefined,
      });
      message.success('链接已生成');
      onPublished(result.shareToken, result.thumbnailUrl);
      onClose();
      form.resetFields();
      setCoverUrl('');
    } catch (err: any) {
      message.error(err?.response?.data?.error || err?.message || '发布失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      onCancel={() => { if (!submitting) onClose(); }}
      onOk={handleOK}
      okText="生成链接"
      cancelText="取消"
      okButtonProps={{ loading: submitting }}
      destroyOnClose
      closeIcon={<span style={{ color: '#fff' }}>×</span>}
      // `content.padding: 0` 清空了 antd Modal 默认的外层 padding（非 wireframe
      // 主题下 header/body/footer 自身 padding 都是 0，左右下留白全靠
      // `.ant-modal-content` 那圈外层 padding 撑出来）。body 另设了
      // `20px 24px 24px` 补回自己的留白，但 footer 没补——底部按钮区因此贴着
      // 弹框右/下边缘，只剩 antd 默认的 `footerMarginTop`(12px) 跟表单区分隔，
      // 左右和下方完全没有内边距。补 footer 的左右/下内边距，跟 body 的
      // 左右 24、下 24 对齐，上留 0（间距已由 footerMarginTop 提供，避免叠加）。
      styles={{
        content: { padding: 0, borderRadius: 16, overflow: 'hidden' },
        body: { padding: '20px 24px 24px' },
        footer: { padding: '0 24px 24px' },
      }}
      title={null}
    >
      {/* 顶部深色渐变图头，向 libtv 投稿弹框的视觉语言靠拢——圆角 modal +
          深色 banner + 大间距表单，跟 Task 6 的投稿弹框视觉统一。 */}
      <div
        style={{
          padding: '24px 24px 20px',
          background: 'linear-gradient(135deg, #1f1f1f 0%, #2a2a2a 55%, #1a1a1a 100%)',
          color: '#fff',
        }}
      >
        <div style={{ fontSize: 18, fontWeight: 600 }}>分享画布</div>
        <div style={{ fontSize: 13, opacity: 0.65, marginTop: 6 }}>
          拥有此链接的人可以查看并复制你的画布
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        style={{ marginTop: 20 }}
        initialValues={{ title: defaultTitle ?? '' }}
      >
        <Form.Item label="自定义封面" extra="不上传时自动从画布里选一张图">
          <Upload.Dragger
            customRequest={handleUpload}
            showUploadList={false}
            accept="image/*"
            multiple={false}
            style={{ padding: 12 }}
          >
            {coverUrl ? (
              <img
                src={coverUrl}
                crossOrigin={mediaCrossOrigin(coverUrl)}
                alt="cover"
                style={{ maxWidth: '100%', maxHeight: 180, objectFit: 'contain' }}
              />
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
                <Inbox size={28} />
                <span>点击或拖拽上传一张封面图</span>
              </div>
            )}
          </Upload.Dragger>
        </Form.Item>
        <Form.Item
          name="title"
          label="标题"
          rules={[
            { required: true, message: '请填写标题' },
            { max: 120, message: '标题最长 120 字' },
          ]}
        >
          <Input placeholder="例：通用 AI 图像生成模板" />
        </Form.Item>
        <Form.Item name="description" label="简介">
          <Input.TextArea rows={3} placeholder="简单描述这个画布能做什么" />
        </Form.Item>
        <Form.Item name="tags" label="标签" extra="多个标签用逗号分隔">
          <Input placeholder="人物,封面,卡通" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
