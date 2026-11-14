import { useCallback, useMemo, useState, type ReactEventHandler } from 'react';
import { App as AntApp } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertCircle, Loader2, ShieldCheck } from 'lucide-react';
import { saveMediaToLibrary, type SeedanceAssetState } from '@/shared/api';
import { loadSeedanceStatus } from '../batchQueries';
import { seedanceStatusKey, verifySeedanceAndRefresh } from './seedanceVerify';
import { useCanvasStore } from '../store';
import { NodeContextMenu, type NodeMenuItem } from './NodeContextMenu';
import { standardNodeMenuItems } from './standardNodeMenuItems';
import { SaveToLibraryModal } from './SaveToLibraryModal';
import { SEEDANCE_REQUIREMENTS, checkSeedanceDuration } from './seedanceRules';
import {
  fileManagerLabel,
  isLocalMediaUrl,
  revealErrorMessage,
  revealInFileManager,
} from '@/desktop/localMedia';
import { useUnsavedLocalBadge } from '@/desktop/unsavedLocal';

type Kind = 'image' | 'video' | 'audio';

const DEFAULT_NAME: Record<Kind, string> = {
  image: '图片素材',
  video: '视频素材',
  audio: '音频素材',
};

const KIND_LABEL: Record<Kind, string> = { image: '图片', video: '视频', audio: '音频' };

interface Options {
  // 节点持有的媒体 URL。整套功能都以它为身份——不用 assetId，因为那个要靠前端在
  // 生成那一刻存进 settings，历史节点/agent 产出的节点/素材库应用回来的节点都没有。
  url: string | undefined;
  kind: Kind;
  // 本节点自身的 id。「拷贝节点」「擦除字幕」都要用——媒体 URL 认不出节点身份
  // (多个节点可持有同一张图);擦除字幕还要据它在源节点右侧建 subtitle-erase 节点并连线。
  nodeId: string;
  // extraItems 排在合规校验之后、标配两项之前，给节点自己的动作留位置
  //（如 gen 节点的「发送到画布」）。
  extraItems?: NodeMenuItem[];
}

// SeedanceRequirementCard 是 ? 悬浮卡片的内容。
function SeedanceRequirementCard({ kind }: { kind: Kind }) {
  const req = SEEDANCE_REQUIREMENTS[kind];
  return (
    <div className="flex flex-col gap-1.5">
      <div className="font-semibold">{req.title}</div>
      {req.items.map((it) => (
        <div key={it.label}>
          <span className="font-semibold">{it.label}：</span>
          <span className="text-muted-foreground">{it.value}</span>
        </div>
      ))}
    </div>
  );
}

/** 挂到节点自己的 <video>/<audio>（VideoPlayer、WaveformPlayer）上，把时长交给菜单。 */
export interface MediaDurationProps {
  onLoadedMetadata: ReactEventHandler<HTMLMediaElement>;
  onError: ReactEventHandler<HTMLMediaElement>;
}

// useMediaDuration 读视频/音频时长（P15）。来源按优先级：
//   1. 节点 settings.videoMeta.duration（VideoInputNode 上传时探测并落库）；只在节点
//      内容就是这个 url 时才信，换片后旧值作废。
//   2. 节点内播放器的 loadedmetadata：调用方把返回的 mediaProps 挂到自己的播放器上。
// 以前每个节点再建一个游离的探测元素，首屏媒体请求翻倍、屏幕外节点也照拉，已去掉。
//
// 返回值三态：undefined = 还没拿到（probing，合规项置灰等它）；null = 播放器加载失败或
// 读不出（Infinity）——放行，交上游判定；number = 时长。
// 不能改成「打开菜单时才探测」：checkSeedanceDuration 对 null 放行，探测完成前那段
// 窗口里超长视频就能提交。
function useMediaDuration(
  url: string | undefined,
  kind: Kind,
  nodeId: string,
): { duration: number | null | undefined; mediaProps: MediaDurationProps } {
  const metaDuration = useCanvasStore((s) => {
    const n = s.nodesById.get(nodeId);
    if (!url || n?.content !== url) return undefined;
    const d = (n.settings?.videoMeta as { duration?: unknown } | undefined)?.duration;
    return typeof d === 'number' && Number.isFinite(d) && d > 0 ? d : undefined;
  });
  const [probed, setProbed] = useState<{ url: string; duration: number | null } | null>(null);
  // 只认 src 就是本 url 的那次加载：GenVideoNode 翻历史版本、WaveformPlayer 播放时
  // 换成 blob 源，都会让播放器的 src 与菜单 url 不一致。
  const mediaProps = useMemo<MediaDurationProps>(
    () => ({
      onLoadedMetadata: (e) => {
        const el = e.currentTarget;
        if (!url || el.getAttribute('src') !== url) return;
        setProbed({ url, duration: Number.isFinite(el.duration) ? el.duration : null });
      },
      onError: (e) => {
        if (!url || e.currentTarget.getAttribute('src') !== url) return;
        setProbed({ url, duration: null });
      },
    }),
    [url],
  );
  if (!url || kind === 'image') return { duration: null, mediaProps };
  if (metaDuration !== undefined) return { duration: metaDuration, mediaProps };
  if (probed && probed.url === url) return { duration: probed.duration, mediaProps };
  return { duration: undefined, mediaProps };
}

// spawnPostprocessNode 在源视频节点正右方新建一个后处理节点并连线。
// 「智能去字幕」「高清」「白模」共用（入口在 VideoActionsBar 工具条）
// ——两者落位规则完全相同，且这段坐标计算要避开「偏上 + 压住节点」，复制两份必然漂移。
//
// addNode 把坐标当**中心点**（内部减半尺寸落位），三种产物节点默认同宽 320：
// 右侧留 80 间距后再加半宽 160，y 取源节点垂直中心 —— 落在正右方、垂直居中对齐。
// 返回新节点 id(源节点不存在/readOnly 时返回 '')——「剪辑」需要拿到 id 继续
// 写入产物 content。
export function spawnPostprocessNode(
  type: 'subtitle-erase' | 'video-upscale' | 'video-whitemodel' | 'preview',
  sourceNodeId: string,
): string {
  const store = useCanvasStore.getState();
  const src = store.nodes.find((n) => n.id === sourceNodeId);
  if (!src) return '';
  const srcW = src.width ?? 320;
  const srcH = src.height ?? 260;
  const pos = { x: src.x + srcW + 80 + 160, y: src.y + srcH / 2 };
  const newId = store.addNode(type, pos);
  if (newId) store.addConnection(sourceNodeId, newId, 'video');
  return newId;
}

// 画布上所有媒体节点共用的右键菜单：合规校验 + 保存到素材库 + 状态角标。
// 之前每个节点各写各的，其中 useSaveToLibrary 那份是裸 position:fixed，没 portal
// 到 body，被画布祖先的 transform: scale/translate 一带就飘到别处去了。
export function useMediaNodeMenu({ url, kind, nodeId, extraItems }: Options) {
  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();
  const deleteNodes = useCanvasStore((s) => s.deleteNodes);
  const [menuPos, setMenuPos] = useState<{ x: number; y: number } | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const canAct = !!url;
  const { duration, mediaProps } = useMediaDuration(url, kind, nodeId);
  // 时长还没拿到：合规项先置灰，等播放器读到元数据（节点在视口附近才会加载）。
  const probing = canAct && duration === undefined;
  const durationCheck = checkSeedanceDuration(kind, duration ?? null);
  const unsavedBadge = useUnsavedLocalBadge(nodeId);

  const statusQuery = useQuery({
    queryKey: seedanceStatusKey(url ?? ''),
    // 走合批器：大画布上每个图片节点各查一次会把 /seedance/status 打成几百条请求；
    // 这里塌成一次批量查询，data 直接是本节点的状态。见 batchQueries.ts。
    // 未校验过的图片合批器给 undefined，但 react-query 不接受 queryFn 返回 undefined
    // （会判为错误并按 retry 重打），所以在这个边界统一转成 null。
    queryFn: () => loadSeedanceStatus(url!).then((s) => s ?? null),
    enabled: !!url,
    // pending（同步校验进行中）期间快轮询，到终态就停。同一窗口的多个节点
    // 轮询也会被合批器合并成一条请求。
    refetchInterval: (query) => {
      const st = query.state.data?.status;
      return st === 'pending' || st === 'failed' ? 5000 : false;
    },
  });
  const state: SeedanceAssetState | undefined = statusQuery.data ?? undefined;

  const onContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setMenuPos({ x: e.clientX, y: e.clientY });
  };

  const verify = useCallback(async () => {
    if (!url) return;
    try {
      // 同步校验，返回即出结果；成败都会刷新角标（见 verifySeedanceAndRefresh）。
      await verifySeedanceAndRefresh(queryClient, url, kind);
      message.success('Seedance 合规校验已通过');
    } catch (e: any) {
      message.error(e?.response?.data?.error || e.message || '提交校验失败');
    }
  }, [url, kind, message, queryClient]);

  // 桌面端：本机文件在系统文件管理器里定位出来。内容还是上游外链（生成结果
  // 没保存到本地）时没有本机文件可定位，直接置灰。
  const canReveal = isLocalMediaUrl(url);
  const reveal = async () => {
    if (!url) return;
    try {
      await revealInFileManager(url);
    } catch (e) {
      message.error(revealErrorMessage(e));
    }
  };

  const handleSave = async (folderId: number, name: string) => {
    if (!url) return;
    setSaving(true);
    try {
      await saveMediaToLibrary(url, folderId, name);
      message.success('已保存到素材库');
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      setModalOpen(false);
    } catch (e: any) {
      message.error(e?.response?.data?.error || e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const items: NodeMenuItem[] = [
    // 合规校验固定放首位：它是 seedance 生成的前置动作，
    // 排最前让「先校验再生成」的路径最短。
    {
      key: 'seedance',
      label: 'Seedance 合规校验',
      onPick: () => {
        setMenuPos(null);
        void verify();
      },
      // 时长越界的视频/音频提交必被上游拒（还要等一轮轮询才知道），所以直接不给点。
      // 已通过校验的同样置灰：自己校验过的图再提交只是服务端幂等空转；公共角色库
      // 的图（永远 active）则会被归属校验拒成 403「media not yours」——两种情况
      // 都是「点了也白点」，明示出来。版本轮换后的重新校验由后台 worker 自动做。
      // 时长未知（probing）同样不给点：读不到时长就放行的话，超长视频能趁这段窗口提交。
      disabled: !canAct || probing || !durationCheck.ok || state?.status === 'active',
      hint: !canAct
        ? `还没有${KIND_LABEL[kind]}内容`
        : state?.status === 'active'
          ? '已通过校验，无需重复提交'
          : probing
            ? `正在读取${KIND_LABEL[kind]}时长，读到后才能提交`
            : (durationCheck.reason ?? '提交给 Seedance 做素材合规校验'),
      tip: <SeedanceRequirementCard kind={kind} />,
    },
    ...(extraItems ?? []),
    // 复制节点/创建副本是所有节点右键菜单的标配（文案/快捷键/提示统一在
    // standardNodeMenuItems，别在这里另写一份）。
    ...standardNodeMenuItems(nodeId, () => setMenuPos(null)),
    {
      key: 'save',
      label: '保存到素材库',
      onPick: () => {
        setMenuPos(null);
        setModalOpen(true);
      },
      disabled: !canAct,
      hint: canAct ? undefined : '还没有内容可保存',
    },
    {
      key: 'reveal',
      label: fileManagerLabel(),
      onPick: () => {
        setMenuPos(null);
        void reveal();
      },
      disabled: !canReveal,
      hint: !url ? '还没有内容' : canReveal ? undefined : '还没有保存到本地，无法在文件夹中显示',
    },
    // 删除固定放最后一项——节点右上角的悬浮 X 因误点风险已移除，右键菜单是
    // 唯一的鼠标删除入口（键盘 Delete/Backspace 仍在）。
    {
      key: 'delete',
      label: '删除',
      danger: true,
      onPick: () => {
        setMenuPos(null);
        deleteNodes([nodeId]);
      },
      hint: '删除本节点，可撤销（Ctrl/Cmd+Z）',
    },
  ];

  // 合规状态角标。没校验过的不显示任何东西——绝大多数素材都属于这一类，全都挂标记
  // 只会变成噪音。
  const seedanceBadge = !state ? null : state.status === 'active' ? (
    <span
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-emerald-600/90 text-white text-[11px] leading-none font-medium"
      title="已通过 Seedance 合规校验"
    >
      <ShieldCheck size={11} />
      已校验
    </span>
  ) : state.status === 'failed_permanent' ? (
    <span
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-red-500/90 text-white text-[11px] leading-none font-medium"
      title={state.error || '素材未通过 Seedance 合规校验'}
    >
      <AlertCircle size={11} />
      不合规
    </span>
  ) : (
    // pending：同步校验进行中（后端在调用 seedance-asset 期间写的临时行）。
    // failed 是旧流程遗留的瞬时态，新流程不再写入，沿用校验中显示。
    <span
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-black/60 text-white text-[11px] leading-none"
      title="正在进行 Seedance 合规校验"
    >
      <Loader2 size={11} className="animate-spin" />
      校验中
    </span>
  );

  // 「未保存到本地」和合规角标互不相干，同时出现时并排显示，未保存排前面。
  const badge = unsavedBadge || seedanceBadge ? (
    <>
      {unsavedBadge}
      {seedanceBadge}
    </>
  ) : null;

  const overlay = (
    <>
      {menuPos && (
        <NodeContextMenu
          x={menuPos.x}
          y={menuPos.y}
          items={items}
          onClose={() => setMenuPos(null)}
        />
      )}
      <SaveToLibraryModal
        open={modalOpen}
        kind={kind}
        previewUrl={url}
        defaultName={DEFAULT_NAME[kind]}
        saving={saving}
        onCancel={() => setModalOpen(false)}
        onConfirm={handleSave}
      />
    </>
  );

  return { onContextMenu, overlay, badge, state, mediaProps };
}
