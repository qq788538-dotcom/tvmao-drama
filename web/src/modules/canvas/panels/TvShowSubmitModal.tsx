import { useEffect, useMemo, useRef, useState } from 'react';
import type { ChangeEvent, ComponentProps, ComponentType } from 'react';
import { Alert, App as AntApp, Button, Checkbox, Dropdown, Form, Input, Modal, Select } from 'antd';
import { ImagePlus } from 'lucide-react';
import { getTvshowDicts, submitTvshowWork, uploadAsset } from '@/shared/api';
import type { TvshowDicts } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import type { NodeType } from '@/modules/canvas/editor/types';
import { CoverGalleryModal } from './CoverGalleryModal';
import { mediaCrossOrigin } from '../editor/utils/mediaCrossOrigin';
import type { GalleryImage } from './CoverGalleryModal';

interface Props {
  open: boolean;
  projectId: number;
  defaultTitle?: string;
  onClose: () => void;
  onSubmitted?: () => void;
}

interface FormValues {
  title: string;
  description: string;
  category: string;
  socialLink?: string;
}

const EMPTY_DICTS: TvshowDicts = { categories: [] };

// 「选择封面」触发的 Dropdown 曾经整栏铺满——根因在 rc-dropdown：只要触发元素是
// Dropdown 的直接 children（这里是那个撑满表单宽度的封面点击区），底层就会把
// `minOverlayWidthMatchTrigger` 默认当 true，用 rc-trigger 的 `stretch:'minWidth'`
// 把触发元素的 offsetWidth 同步给浮层（apps/web/node_modules/rc-dropdown/es/Dropdown.js
// 的 getMinOverlayWidthMatchTrigger + apps/web/node_modules/@rc-component/trigger/es/index.js
// 的 syncTargetSize），浮层因此被撑成跟封面框一样宽，看起来像表单里多出的一整块。
// antd 5.29 的 DropdownProps 类型没收 `minOverlayWidthMatchTrigger` 这个透传属性
// （dropdown.js 里就是 omit(props,['rootClassName']) 原样转给 rc-dropdown，跑得通，
// 只是 TS 不认），补一层局部类型把它显式打开，关掉 stretch，浮层退回按内容宽度
// 渲染的正常下拉菜单观感，不用跟内联 style 抢覆盖权。
type DropdownExtraProps = ComponentProps<typeof Dropdown> & { minOverlayWidthMatchTrigger?: boolean };
const CoverMenuDropdown = Dropdown as ComponentType<DropdownExtraProps>;

// 「历史图库」的图片来源判定——显式白名单，不靠猜（同 store.ts 的
// VIDEO_OUTPUT_NODE_TYPES 那条纪律）。input-image / gen-image 这两类节点的
// content 天生就是「一张图片」的 URL，直接收。
//
// 2026-08-07 review 钉死的真实缺陷（Important，已修复）：白名单漏了 preview
// 节点，而它恰恰是用户"整理"图片的主要落点——GenImageNode.tsx 的
// handleSendToCanvas（从生成结果里挑一张"钉"到画布）和 handleUpload（把节点
// 变形成持有上传图的通用容器）都会产出 kind:'image' 的 preview 节点，不是
// 边角场景。preview 是通用媒体容器（可能装图也可能装视频），不能无脑收，
// 但也不能因为「语义模糊」就整类排除——store.ts 的 getConnectedInputs 早就
// 示范了判据：`settings.kind`（'image' | 'video'）是节点自报的产物类型，
// 这里直接复用同一判据，与画布本身对「这是不是一张图」的认定保持一致。
const IMAGE_NODE_TYPES: ReadonlySet<NodeType> = new Set<NodeType>(['input-image', 'gen-image']);

function isImageGalleryCandidate(node: { type: NodeType; content?: string; settings?: Record<string, unknown> }) {
  if (!node.content) return false;
  if (IMAGE_NODE_TYPES.has(node.type)) return true;
  return node.type === 'preview' && node.settings?.kind === 'image';
}

// 顶栏「发布与分享」浮层的「发布作品」出口——对照 libtv 投稿弹框视觉
// （docs/superpowers/specs/2026-08-06-canvas-publish-share-refs/libtv-submit-work-modal.png），
// 布局/文案/字段来自 spec 2026-08-06 §六 拍板，不是随意设计。
//
// 2026-08-06 用户拍板：原「分类」「活动标签」「参赛赛道」三个字典下拉本是同一个
// 东西，只留一个——表单 name 仍是 category（API 字段名不动，是硬边界），用户端
// 显示文案改叫「活动标签」，参赛赛道彻底删除（不是隐藏）。
//
// 「活动标签」是运营配置项，后台没配任何选项时（dicts.categories 为空）整个
// 字段不渲染——而不是渲染一个空的、点开什么都没有的 Select，那样用户会误以为
// 是加载失败（原「字典为空则整个字段不渲染」的逻辑保留，只是原来这条逻辑只覆盖
// activityTag/raceTrack 两个可选字段，现在合并字段仍是必填，套用同一逻辑）。
//
// 2026-08-07 review 发现的真实缺陷（Important，已修复）：字段不渲染 = 该
// Form.Item 连同它的 required rule 一起没挂载，`form.validateFields()` 只校验
// 已挂载的项，于是空字典时点「发布并投稿」会直接带着 category: '' 提交给
// 后端（400 category required，纯英文，用户在 UI 上根本找不到叫 category 的
// 字段，无从自救）。字典初始为空是新环境的必经状态（migration 不灌种子数据，
// 靠管理员手动配置），不是边角情况，必须堵死：categories 为空时除了隐藏
// Select，还要 1) 用 Alert 明确提示「找管理员配置」 2) 禁用提交按钮——不能
// 指望用户点了才从服务端错误里猜出原因。
export function TvShowSubmitModal({ open, projectId, defaultTitle, onClose, onSubmitted }: Props) {
  const { message } = AntApp.useApp();
  const [form] = Form.useForm<FormValues>();
  const [dicts, setDicts] = useState<TvshowDicts>(EMPTY_DICTS);
  const [coverUrl, setCoverUrl] = useState('');
  // 历史图库选图层的开关；封面来源二选一（本地上传 / 历史图库）都写同一个
  // coverUrl，提交时统一走 thumbnailUrl 字段，后端零改动。
  const [galleryOpen, setGalleryOpen] = useState(false);
  // 封面菜单锚在**鼠标点击处**，而不是靠 placement 相对触发盒猜位置。
  // 前两轮分别试过 `topRight`（浮层被顶到标题区）和 `bottom`（贴封面框下沿、
  // 压住 extra 文案），都对不准——根因是触发盒是整宽的封面框，无论哪个
  // placement 都只能相对这个大盒子的边角定位，而对标交互是「点哪儿弹哪儿」。
  // 改法：记录点击点相对封面框的坐标，在该点放一个 0×0 的锚元素给 Dropdown
  // 用，浮层自然从指针处展开，与盒子多宽无关。
  const [menuAnchor, setMenuAnchor] = useState<{ x: number; y: number } | null>(null);
  // 「点击发布即代表同意《创作许可服务协议》」——产品拍板默认勾选（多数投稿场景
  // 不需要用户额外动作），但未勾选必须硬拦提交按钮，不能只是视觉提示。
  const [agreed, setAgreed] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  // 后台没配任何活动标签选项——不止字段不渲染，还得拦提交按钮 + 给出可操作的
  // 提示，否则用户点了发布只会拿到后端一句英文 400，且 UI 上根本没有能填的字段。
  const noCategoryOptions = dicts.categories.length === 0;

  const fileInputRef = useRef<HTMLInputElement>(null);

  // 「历史图库」的候选集直接读画布 store——不新建后端接口，画布里已生成/
  // 已上传的图片节点全部信息都在这。弹框打开时画布节点早已加载完毕，直接
  // 订阅即可。store 的 nodes 初始值恒为 []（见 store.ts），不会是 undefined。
  const canvasNodes = useCanvasStore((s) => s.nodes);
  const galleryImages = useMemo<GalleryImage[]>(
    () =>
      canvasNodes
        .filter(isImageGalleryCandidate)
        .map((n) => ({ nodeId: n.id, url: n.content as string })),
    [canvasNodes],
  );
  const hasGalleryImages = galleryImages.length > 0;

  // 字典只在弹框打开时拉一次；这份数据是后台运营配置，几乎不变，用
  // react-query 引入缓存 key 管理是过度设计，普通 useEffect 就够。
  useEffect(() => {
    if (!open) return;
    getTvshowDicts()
      .then(setDicts)
      .catch(() => setDicts(EMPTY_DICTS));
  }, [open]);

  // 每次打开弹框都重置成初始态：预填画布名 + 协议默认勾选 + 清空上一次
  // 残留的封面/字段（destroyOnClose 会卸载 Form 实例，但 coverUrl/agreed
  // 是本组件自己的 state，不会跟着自动清）。
  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({ title: defaultTitle ?? '' });
    setAgreed(true);
    setCoverUrl('');
    setGalleryOpen(false);
  }, [open, defaultTitle, form]);

  // 本地上传：菜单项点击后触发隐藏 file input，选完文件走既有 /assets
  // 上传逻辑（原来挂在 Upload.Dragger 的 customRequest 上，现在改由 input
  // 的 onChange 驱动，逻辑不变）。
  const handleFileInputChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ''; // 允许连续选同一个文件也能触发 onChange
    if (!file) return;
    try {
      const asset = await uploadAsset(file, projectId);
      setCoverUrl(asset.url);
    } catch (err: any) {
      message.error(err?.response?.data?.error || err?.message || '上传失败');
    }
  };

  const coverMenuItems = [
    {
      key: 'upload',
      label: '本地上传',
      onClick: () => fileInputRef.current?.click(),
    },
    {
      key: 'gallery',
      disabled: !hasGalleryImages,
      label: hasGalleryImages ? (
        '历史图库'
      ) : (
        <div>
          <div>历史图库</div>
          <div style={{ fontSize: 12, color: 'rgba(0,0,0,0.45)' }}>当前画布还没有图片</div>
        </div>
      ),
      onClick: () => setGalleryOpen(true),
    },
  ];

  const handleSubmit = async () => {
    let values: FormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSubmitting(true);
    try {
      await submitTvshowWork({
        projectId,
        title: values.title,
        description: values.description,
        category: values.category,
        socialLink: values.socialLink || undefined,
        thumbnailUrl: coverUrl || undefined,
      });
      message.success('已提交，等待审核');
      onSubmitted?.();
      onClose();
    } catch (err: any) {
      // 待审核超限时后端返回 422 + 中文文案「待审核投稿已达上限，请等待审核完成」，
      // 直接透传即可，不需要前端另翻译一遍。
      message.error(err?.response?.data?.error || err?.message || '提交失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
      <Modal
        open={open}
        onCancel={() => {
          if (!submitting) onClose();
        }}
        footer={null}
        destroyOnClose
        closeIcon={<span style={{ color: '#fff' }}>×</span>}
        // 距顶 50px（用户 2026-08-07 拍板）。AntD 默认 top:100 且高度随内容
        // 无限增长——表单一长就顶出视口底部，得滚整个页面才看得到下半截。
        // 这里把弹框钉在距顶 50px，并给 body 限高 + 内部滚动：溢出在弹框里
        // 滚，页面不再被撑开。100px = 上下各 50px 的留白预算。
        style={{ top: 50 }}
        styles={{
          content: { padding: 0, borderRadius: 16, overflow: 'hidden' },
          body: { padding: 0, maxHeight: 'calc(100vh - 100px)', overflowY: 'auto' },
        }}
        title={null}
        width={520}
      >
        {/* 顶部深色背景图头，跟 PublishWorkflowModal 同一套视觉语言 */}
        <div
          style={{
            padding: '24px 24px 20px',
            background: 'linear-gradient(135deg, #1f1f1f 0%, #2a2a2a 55%, #1a1a1a 100%)',
            color: '#fff',
          }}
        >
          <div style={{ fontSize: 18, fontWeight: 600 }}>发布作品到喵剧AI</div>
          <div style={{ fontSize: 13, opacity: 0.65, marginTop: 6 }}>
            您的作品将展示到 TV Show，方便大家交流使用
          </div>
        </div>

        <div style={{ padding: '20px 24px 24px' }}>
          {noCategoryOptions && (
            <Alert
              type="warning"
              showIcon
              message="管理员尚未配置活动标签，暂时无法投稿"
              style={{ marginBottom: 16 }}
            />
          )}
          <Form form={form} layout="vertical" requiredMark>
            {/* 封面在最上方——投稿场景封面是决定作品在 TV Show 列表里能不能被
                点开的关键因素，比标题/描述更值得放在第一眼看到的位置。对标
                libtv：点「选择封面」弹小菜单，本地上传 / 历史图库二选一
                （不再是拖拽上传区）。 */}
            <Form.Item label="封面" extra="建议上传横版图片">
              <div
                style={{
                  position: 'relative',
                  border: '1px dashed rgba(0,0,0,0.15)',
                  borderRadius: 8,
                  padding: coverUrl ? 8 : 24,
                  cursor: 'pointer',
                  textAlign: 'center',
                }}
                onClick={(e) => {
                  const box = e.currentTarget.getBoundingClientRect();
                  setMenuAnchor({ x: e.clientX - box.left, y: e.clientY - box.top });
                }}
              >
                {/* 0×0 锚元素：定位到点击点，Dropdown 以它为参照展开，于是浮层
                    从指针处弹出、宽度只由菜单内容决定，与封面框多宽无关。 */}
                <CoverMenuDropdown
                  menu={{ items: coverMenuItems }}
                  trigger={['click']}
                  open={!!menuAnchor}
                  onOpenChange={(next: boolean) => {
                    if (!next) setMenuAnchor(null);
                  }}
                  placement="bottomLeft"
                  minOverlayWidthMatchTrigger={false}
                >
                  <span
                    aria-hidden
                    style={{
                      position: 'absolute',
                      left: menuAnchor?.x ?? 0,
                      top: menuAnchor?.y ?? 0,
                      width: 0,
                      height: 0,
                    }}
                  />
                </CoverMenuDropdown>
                <div style={{ textAlign: 'center' }}>
                  {coverUrl ? (
                    <>
                      <img
                        src={coverUrl}
                        crossOrigin={mediaCrossOrigin(coverUrl)}
                        alt="封面"
                        style={{ maxWidth: '100%', maxHeight: 160, objectFit: 'contain' }}
                      />
                      <div style={{ marginTop: 8 }}>
                        <Button type="link" size="small" style={{ padding: 0 }}>
                          重新选择
                        </Button>
                      </div>
                    </>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
                      <ImagePlus size={24} />
                      <span>选择封面</span>
                    </div>
                  )}
                </div>
              </div>
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                style={{ display: 'none' }}
                onChange={handleFileInputChange}
              />
            </Form.Item>

            <Form.Item label="当前画布">
              <Input value={defaultTitle} disabled />
            </Form.Item>

            <Form.Item
              name="title"
              label="作品名称"
              rules={[
                { required: true, message: '请输入作品名称' },
                { max: 120, message: '作品名称最长 120 字' },
              ]}
            >
              <Input placeholder="请输入作品名称" />
            </Form.Item>

            <Form.Item
              name="description"
              label="作品描述"
              rules={[{ required: true, message: '请输入作品描述' }]}
            >
              <Input.TextArea rows={3} placeholder="请输入作品描述" />
            </Form.Item>

            {!noCategoryOptions && (
              <Form.Item name="category" label="活动标签" rules={[{ required: true, message: '请选择活动标签' }]}>
                <Select
                  aria-label="活动标签"
                  placeholder="请选择活动标签"
                  options={dicts.categories.map((c) => ({ label: c, value: c }))}
                />
              </Form.Item>
            )}

            <Form.Item name="socialLink" label="社媒链接">
              <Input placeholder="请添加您在社媒发布该作品的链接" />
            </Form.Item>
          </Form>

          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 8, gap: 16 }}>
            <Checkbox checked={agreed} onChange={(e) => setAgreed(e.target.checked)}>
              点击发布即代表同意《
              <a href="/creator-license" target="_blank" rel="noreferrer">
                创作许可服务协议
              </a>
              》
            </Checkbox>
            <Button
              type="primary"
              onClick={handleSubmit}
              disabled={!agreed || noCategoryOptions}
              loading={submitting}
            >
              发布并投稿
            </Button>
          </div>
        </div>
      </Modal>
      <CoverGalleryModal
        open={galleryOpen}
        images={galleryImages}
        onClose={() => setGalleryOpen(false)}
        onSelect={(url) => setCoverUrl(url)}
      />
    </>
  );
}
