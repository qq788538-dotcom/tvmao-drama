import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  App as AntApp,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Spin,
  Tabs,
  Typography,
  Upload,
} from 'antd';
import type { MenuProps } from 'antd';
import { Image as ImageIcon, MoreHorizontal, Plus, UploadCloud } from 'lucide-react';
import { mediaCrossOrigin } from './editor/utils/mediaCrossOrigin';
import {
  createProject,
  deleteProject,
  duplicateProject,
  listProjectImages,
  listProjectsPage,
  Project,
  ProjectImage,
  setProjectCover,
  updateProject,
  uploadAsset,
} from '@/shared/api';

const { Title, Text } = Typography;

/** 每页条数：5 列时正好铺满约 5 行，首屏之外留一屏余量。 */
const PAGE_SIZE = 24;

function formatBytes(n: number): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// ─── 卡片 ────────────────────────────────────────────────────────────────────

/**
 * 封面 16:9 + object-fit: cover（约定，照参考图）。已知代价：9:16 竖屏短剧的
 * 封面会被砍掉上下大半。整齐优先是有意的取舍，不是疏漏。
 */
function ProjectCover({ url }: { url?: string }) {
  // 手动封面指向的图被删掉后会 404，且我们刻意不做级联清理（删图入口有好几处，
  // 每处挂钩才不漏，而这个场景罕见）。onError 让它退回占位图，显示成「没有封面」
  // 而不是一个破图标。
  const [broken, setBroken] = useState(false);
  useEffect(() => setBroken(false), [url]);

  if (!url || broken) {
    return (
      <div className="tn-cover tn-cover--empty">
        <ImageIcon size={40} color="#52525b" />
      </div>
    );
  }
  return (
    <div className="tn-cover">
      <img src={url} crossOrigin={mediaCrossOrigin(url)} alt="" loading="lazy" onError={() => setBroken(true)} />
    </div>
  );
}

function ProjectCard({
  project,
  onOpen,
  onMenu,
}: {
  project: Project;
  onOpen: () => void;
  onMenu: MenuProps['onClick'];
}) {
  const items: MenuProps['items'] = [
    { key: 'open', label: '打开' },
    { key: 'rename', label: '重命名' },
    { key: 'cover', label: '修改封面' },
    { key: 'duplicate', label: '创建副本' },
    { type: 'divider' },
    { key: 'delete', label: '删除项目', danger: true },
  ];

  return (
    <div className="tn-card" onClick={onOpen}>
      <ProjectCover url={project.coverUrl} />
      <div className="tn-card__meta">
        <div className="tn-card__titleRow">
          <span className="tn-card__title" title={project.name}>
            {project.name}
          </span>
          <Dropdown menu={{ items, onClick: onMenu }} trigger={['click']} placement="bottomRight">
            <button
              type="button"
              className="tn-card__more"
              aria-label="更多操作"
              onClick={(e) => e.stopPropagation()}
            >
              <MoreHorizontal size={18} />
            </button>
          </Dropdown>
        </div>
        <div className="tn-card__sub">
          <span>{formatDate(project.updatedAt)}</span>
          <span>{formatBytes(project.storageBytes ?? 0)}</span>
        </div>
      </div>
    </div>
  );
}

// ─── 封面选择弹窗 ────────────────────────────────────────────────────────────

function CoverModal({
  project,
  onClose,
  onPicked,
}: {
  project: Project | null;
  onClose: () => void;
  onPicked: (assetId: number) => void;
}) {
  const { message } = AntApp.useApp();
  const [images, setImages] = useState<ProjectImage[]>([]);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    if (!project) return;
    // 关掉 A 的弹窗马上打开 B 时，A 的慢响应不能落进 B 的弹窗——否则点选的是
    // A 项目的图，被设成 B 的封面。上一个项目的图也先清掉，不在 B 里闪现。
    let cancelled = false;
    setImages([]);
    setLoading(true);
    listProjectImages(project.id)
      .then((rows) => {
        if (!cancelled) setImages(rows);
      })
      .catch((err: any) => {
        if (!cancelled) message.error(err?.response?.data?.error || '读取项目图片失败');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [project, message]);

  async function handleUpload(file: File) {
    if (!project) return;
    setUploading(true);
    try {
      const asset = await uploadAsset(file, project.id);
      onPicked(asset.id);
    } catch (err: any) {
      message.error(err?.response?.data?.error || '上传失败');
    } finally {
      setUploading(false);
    }
  }

  return (
    <Modal title="修改封面" open={!!project} onCancel={onClose} footer={null} width={720}>
      <Tabs
        items={[
          {
            key: 'pick',
            label: '从项目中选择',
            children: loading ? (
              <div style={{ textAlign: 'center', padding: 48 }}>
                <Spin />
              </div>
            ) : images.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="这个项目还没有图片，可以直接上传一张"
              />
            ) : (
              <div className="tn-coverPick">
                {images.map((img) => (
                  <button
                    key={img.id}
                    type="button"
                    className="tn-coverPick__item"
                    onClick={() => onPicked(img.id)}
                  >
                    <img src={img.url} crossOrigin={mediaCrossOrigin(img.url)} alt="" loading="lazy" />
                  </button>
                ))}
              </div>
            ),
          },
          {
            key: 'upload',
            label: '上传图片',
            children: (
              <Upload.Dragger
                accept="image/*"
                showUploadList={false}
                disabled={uploading}
                beforeUpload={(file) => {
                  void handleUpload(file);
                  return false; // 自己发请求，不走 AntD 内置上传
                }}
              >
                <p style={{ marginBottom: 8 }}>
                  <UploadCloud size={32} color="#71717a" />
                </p>
                <p>{uploading ? '上传中…' : '点击或拖拽图片到这里'}</p>
              </Upload.Dragger>
            ),
          },
        ]}
      />
    </Modal>
  );
}

// ─── 页面 ────────────────────────────────────────────────────────────────────

export interface CanvasProjectsPageProps {
  /** 弹框模式：打开项目（含新建、创建副本后跳转）交给调用方；不传则走路由跳转。 */
  onOpenProject?: (id: number) => void;
  /** 项目被删除后回调（弹框用它处理「删的是当前项目」）。 */
  onProjectDeleted?: (id: number) => void;
  /** 嵌在弹框里：不显示自己的大标题（弹框已有标题）。 */
  embedded?: boolean;
}

export function CanvasProjectsPage({ onOpenProject, onProjectDeleted, embedded = false }: CanvasProjectsPageProps = {}) {
  const routerNavigate = useNavigate();
  // 页面里所有「进入某个项目」都走这一个出口。
  const navigate = useCallback(
    (to: string) => {
      const id = Number(to.split('/').pop());
      if (onOpenProject && Number.isFinite(id)) onOpenProject(id);
      else routerNavigate(to);
    },
    [onOpenProject, routerNavigate],
  );
  const { message, modal } = AntApp.useApp();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<Project | null>(null);
  const [coverFor, setCoverFor] = useState<Project | null>(null);
  const [duplicating, setDuplicating] = useState(false);
  const [editForm] = Form.useForm<{ name: string; description?: string }>();

  // 分页与首页「个人最近项目」区分开缓存：那边拉全量、key 是 ['projects']，
  // 混用会让两边互相污染。
  const projectsQuery = useInfiniteQuery({
    queryKey: ['projects', 'paged'],
    // 列表按 updated_at 倒序，打开项目会刷新 updated_at（最近打开排第一）；每次打开列表都重拉，
    // 不吃全局 30s staleTime 的旧顺序。
    refetchOnMount: 'always',
    initialPageParam: 0,
    queryFn: ({ pageParam }) => listProjectsPage(PAGE_SIZE, pageParam as number),
    getNextPageParam: (lastPage, allPages) => {
      const loaded = allPages.reduce((n, p) => n + p.items.length, 0);
      return loaded < lastPage.total ? loaded : undefined;
    },
  });

  const projects = useMemo(
    () => projectsQuery.data?.pages.flatMap((p) => p.items) ?? [],
    [projectsQuery.data],
  );

  function invalidate() {
    queryClient.invalidateQueries({ queryKey: ['projects'] });
  }

  // 滚动到底自动加载下一页。哨兵放在网格末尾，进入视口即翻页。
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  const { fetchNextPage, hasNextPage, isFetchingNextPage } = projectsQuery;
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasNextPage) return;
    const io = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting && !isFetchingNextPage) void fetchNextPage();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [fetchNextPage, hasNextPage, isFetchingNextPage]);

  // 「开始创作」：不弹窗，直接建一个未命名项目并进画布。名字回头用菜单里的
  // 「重命名」改——少一步表单，创建成本降到点一下。
  const createMutation = useMutation({
    mutationFn: () => createProject('未命名', ''),
    onSuccess: (project) => {
      invalidate();
      navigate(`/canvas/${project.id}`);
    },
    onError: (err: any) => message.error(err?.response?.data?.error || '创建失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteProject(id),
    onSuccess: (_data, id) => {
      message.success('项目已删除');
      invalidate();
      onProjectDeleted?.(id);
    },
    onError: (err: any) => message.error(err?.response?.data?.error || '删除失败'),
  });

  const updateMutation = useMutation({
    mutationFn: (values: { name: string; description?: string }) =>
      updateProject(editing!.id, { name: values.name, description: values.description || '' }),
    onSuccess: () => {
      message.success('已保存');
      invalidate();
      setEditing(null);
      editForm.resetFields();
    },
    onError: (err: any) => message.error(err?.response?.data?.error || '保存失败'),
  });

  const coverMutation = useMutation({
    mutationFn: (v: { projectId: number; assetId: number }) =>
      setProjectCover(v.projectId, v.assetId),
    onSuccess: () => {
      message.success('封面已更新');
      invalidate();
      setCoverFor(null);
    },
    onError: (err: any) => message.error(err?.response?.data?.error || '设置封面失败'),
  });

  /**
   * 创建副本。**必须有 loading 遮罩**：没有前置确认框，而复制大项目要
   * 几秒才跳走，没有反馈的话用户会以为没点中而连点——连点就是复制出好几份、真实
   * 吃掉几倍存储配额。模态遮罩挡住的是整页点击，比只禁用某个按钮更彻底。
   */
  const duplicateMutation = useMutation({
    mutationFn: (id: number) => duplicateProject(id),
    onMutate: () => setDuplicating(true),
    onSuccess: (newId) => {
      setDuplicating(false);
      invalidate();
      navigate(`/canvas/${newId}`);
    },
    onError: (err: any) => {
      // 失败必须关掉遮罩，否则用户被永久卡住只能刷新页面。
      setDuplicating(false);
      message.error(err?.response?.data?.error || '创建副本失败');
    },
  });

  function confirmDelete(project: Project) {
    modal.confirm({
      title: '删除项目？',
      content: (
        <div>
          <div style={{ marginBottom: 8 }}>
            「<strong>{project.name}</strong>」将被永久删除，画布上的所有节点、生成结果、上传素材一并清理。
          </div>
          <div style={{ color: '#f5222d', fontSize: 12 }}>此操作不可撤销。</div>
        </div>
      ),
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      centered: true,
      onOk: () => deleteMutation.mutateAsync(project.id),
    });
  }

  const handleMenu = useCallback(
    (project: Project): MenuProps['onClick'] =>
      ({ key, domEvent }) => {
        domEvent.stopPropagation();
        switch (key) {
          case 'open':
            navigate(`/canvas/${project.id}`);
            break;
          case 'rename':
            setEditing(project);
            editForm.setFieldsValue({ name: project.name, description: project.description });
            break;
          case 'cover':
            setCoverFor(project);
            break;
          case 'duplicate':
            duplicateMutation.mutate(project.id);
            break;
          case 'delete':
            confirmDelete(project);
            break;
        }
      },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [navigate, editForm, duplicateMutation],
  );

  return (
    <div style={{ minHeight: '100%', color: '#e4e4e7' }}>
      <style>{CARD_CSS}</style>
      {/* 容器不再夹 maxWidth:1200——2000px 屏上那会让两边各空掉 400px。 */}
      <div style={{ padding: embedded ? '16px 24px 24px' : '32px 40px' }}>
        {!embedded && (
          <div
            style={{
              display: 'flex',
              width: '100%',
              justifyContent: 'space-between',
              alignItems: 'center',
              marginBottom: 24,
            }}
          >
            <Title level={3} style={{ margin: 0, color: '#e4e4e7' }}>
              项目列表
            </Title>
          </div>
        )}

        {projectsQuery.isLoading ? (
          <div style={{ textAlign: 'center', padding: 48 }}>
            <Spin />
          </div>
        ) : (
          <>
            <div className="tn-grid">
              <button
                type="button"
                className="tn-card tn-card--create"
                onClick={() => createMutation.mutate()}
                disabled={createMutation.isPending}
              >
                <div className="tn-cover tn-cover--empty">
                  <Plus size={28} color="#a1a1aa" />
                  <span style={{ marginTop: 8, color: '#a1a1aa' }}>开始创作</span>
                </div>
                <div className="tn-card__meta">
                  <div className="tn-card__sub">
                    <span>创建新的视频项目</span>
                  </div>
                </div>
              </button>

              {projects.map((project) => (
                <ProjectCard
                  key={project.id}
                  project={project}
                  onOpen={() => navigate(`/canvas/${project.id}`)}
                  onMenu={handleMenu(project)}
                />
              ))}
            </div>

            <div ref={sentinelRef} style={{ height: 1 }} />
            {isFetchingNextPage && (
              <div style={{ textAlign: 'center', padding: 24 }}>
                <Spin />
              </div>
            )}
          </>
        )}
      </div>

      <Modal
        title="重命名"
        open={!!editing}
        onCancel={() => {
          setEditing(null);
          editForm.resetFields();
        }}
        onOk={() => editForm.submit()}
        confirmLoading={updateMutation.isPending}
        destroyOnClose
      >
        {/* 弹窗叫「重命名」但含描述字段：新卡片不再展示描述，若这里也砍掉，
            description 就成了没有任何入口能看能改的孤儿字段（库里、API、CLI 都还在），
            而存量项目的描述写得相当详细。 */}
        <Form form={editForm} layout="vertical" onFinish={updateMutation.mutate}>
          <Form.Item label="项目名称" name="name" rules={[{ required: true, message: '请输入名称' }]}>
            <Input />
          </Form.Item>
          <Form.Item label="描述" name="description">
            <Input.TextArea rows={3} />
          </Form.Item>
        </Form>
      </Modal>

      <CoverModal
        project={coverFor}
        onClose={() => setCoverFor(null)}
        onPicked={(assetId) =>
          coverFor && coverMutation.mutate({ projectId: coverFor.id, assetId })
        }
      />

      {/* destroyOnClose：关闭后把内容从 DOM 里摘掉。不加的话遮罩虽然不可见，
          「正在复制项目…」这段文字仍留在 DOM 里，读屏软件照样能读到。 */}
      <Modal
        open={duplicating}
        closable={false}
        maskClosable={false}
        keyboard={false}
        footer={null}
        centered
        width={320}
        destroyOnClose
      >
        <div style={{ textAlign: 'center', padding: '16px 0' }}>
          <Spin />
          <div style={{ marginTop: 16 }}>正在复制项目…</div>
          <Text type="secondary" style={{ fontSize: 12 }}>
            素材较多时需要几秒，请勿关闭页面
          </Text>
        </div>
      </Modal>

      {!projectsQuery.isLoading && projects.length === 0 && (
        <div style={{ padding: '0 40px 40px' }}>
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={<span style={{ color: '#71717a' }}>还没有项目，点「开始创作」建一个吧</span>}
          />
        </div>
      )}
    </div>
  );
}

/**
 * 卡片样式内联在页面里：这一套只服务这个页面，抽成全局 CSS 反而要跨文件找。
 * 列数用 auto-fill + minmax 而不是写死断点——容器现在是满宽的，minmax 能让列数
 * 随视口自然增减，卡片宽度始终落在 260~380px 这个区间（参考图约 330px）。
 */
const CARD_CSS = `
.tn-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 20px 16px;
}
.tn-card {
  background: transparent;
  border: none;
  padding: 0;
  text-align: left;
  cursor: pointer;
  display: block;
  width: 100%;
  /* 顶部对齐、不被拉伸到行高。grid item 默认 align-self:stretch，而同一行里
     卡片高度并不相同（项目卡信息区两行，「开始创作」卡只有一行小字），被拉伸后
     矮的那张会多出一截空白。对 <div> 卡片只是底部留白看不出来，对 <button>
     卡片就会露馅——button 的内容在自身盒子里是**垂直居中**的（<div> 没有这个
     默认行为），多出来的空白被平分到上下，整张卡连带封面一起往下沉约 20px，
     和邻居卡的封面框对不齐。 */
  align-self: start;
}
/* 顺带把 button 的布局显式写死，不再依赖它那个默认居中行为。 */
.tn-card--create {
  color: inherit;
  display: flex;
  flex-direction: column;
}
.tn-card--create:disabled { opacity: .6; cursor: default; }
.tn-cover {
  position: relative;
  width: 100%;
  aspect-ratio: 16 / 9;
  border-radius: 10px;
  overflow: hidden;
  background: #1c1c1f;
  border: 1px solid #27272a;
}
.tn-cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
.tn-cover--empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.tn-card:hover .tn-cover { border-color: #3f3f46; }
.tn-card__meta { padding: 10px 2px 0; }
.tn-card__titleRow { display: flex; align-items: center; gap: 8px; }
.tn-card__title {
  flex: 1;
  min-width: 0;
  font-size: 14px;
  color: #e4e4e7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tn-card__more {
  flex: none;
  background: transparent;
  border: none;
  color: #a1a1aa;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 6px;
  line-height: 0;
}
.tn-card__more:hover { background: #27272a; color: #e4e4e7; }
.tn-card__sub {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
  font-size: 12px;
  color: #71717a;
}
/* 触屏没有 hover 态，不区分的话菜单在手机上永远点不出来——所以只在支持 hover
   的设备上做「悬停才显形」，其余一律常驻。 */
@media (hover: hover) {
  .tn-card__more { opacity: 0; transition: opacity .15s; }
  .tn-card:hover .tn-card__more { opacity: 1; }
}
.tn-coverPick {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 12px;
  max-height: 60vh;
  overflow-y: auto;
}
.tn-coverPick__item {
  padding: 0;
  border: 1px solid #27272a;
  border-radius: 8px;
  overflow: hidden;
  background: #18181b;
  cursor: pointer;
  aspect-ratio: 16 / 9;
}
.tn-coverPick__item:hover { border-color: #3b82f6; }
.tn-coverPick__item img { width: 100%; height: 100%; object-fit: cover; display: block; }
`;
