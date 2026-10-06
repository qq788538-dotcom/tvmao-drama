import { useCallback, useEffect, useState } from 'react';
import { App as AntApp } from 'antd';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertCircle, Loader2, ShieldCheck } from 'lucide-react';
import {
  saveMediaToLibrary,
  verifySeedance,
  type SeedanceAssetState,
} from '@/shared/api';
import { loadSeedanceStatus } from '../batchQueries';
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

// useMediaDuration 读视频/音频时长。为什么不从节点 settings 拿：只有 VideoInputNode
// 存了 videoMeta.duration，GenVideoNode / PreviewNode / SubtitleEraseNode / AudioInputNode
// 都没有，而合规校验对这些节点一样开放。所以在这里统一探测一次。
// 拿不到（加载失败、浏览器给 Infinity）返回 null——调用方据此放行，交上游判定。
function useMediaDuration(url: string | undefined, kind: Kind): number | null {
  const [duration, setDuration] = useState<number | null>(null);
  useEffect(() => {
    if (!url || kind === 'image') {
      setDuration(null);
      return;
    }
    const el = document.createElement(kind === 'video' ? 'video' : 'audio');
    el.preload = 'metadata';
    const onMeta = () => setDuration(Number.isFinite(el.duration) ? el.duration : null);
    const onError = () => setDuration(null);
    el.addEventListener('loadedmetadata', onMeta);
    el.addEventListener('error', onError);
    el.src = url;
    return () => {
      el.removeEventListener('loadedmetadata', onMeta);
      el.removeEventListener('error', onError);
      // 断掉正在进行的请求，免得关掉节点后还在后台下载。
      el.src = '';
    };
  }, [url, kind]);
  return duration;
}

// spawnPostprocessNode 在源视频节点正右方新建一个后处理节点并连线。
// 「智能去字幕」「高清」「白模」共用（入口在 VideoActionsBar 工具条，2026-08-14 从
// 本文件的右键菜单迁走）——两者落位规则完全相同，且这段坐标计算曾因
// 「偏上 + 压住节点」修过一次（2ca59f9），复制两份必然漂移。
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
  const duration = useMediaDuration(url, kind);
  const durationCheck = checkSeedanceDuration(kind, duration);

  const statusQuery = useQuery({
    queryKey: ['seedance-status', url],
    // 走合批器：大画布上每个图片节点各查一次会把 /seedance/status 打成几百条请求；
    // 这里塌成一次批量查询，data 直接是本节点的状态。见 batchQueries.ts。
    // 未校验过的图片合批器给 undefined，但 react-query 不接受 queryFn 返回 undefined
    // （会判为错误并按 retry 重打），所以在这个边界统一转成 null。
    queryFn: () => loadSeedanceStatus(url!).then((s) => s ?? null),
    enabled: !!url,
    // pending 期间快轮询，到终态就停——不给上游和自己白白加压。同一窗口的多个节点
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
      await verifySeedance(url, kind);
      message.success('已提交 Seedance 合规校验');
      queryClient.invalidateQueries({ queryKey: ['seedance-status', url] });
    } catch (e: any) {
      message.error(e?.response?.data?.error || e.message || '提交校验失败');
    }
  }, [url, kind, message, queryClient]);

  // 桌面端（spec S3）：本机文件在系统文件管理器里定位出来。内容还是上游外链（生成结果
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
    // 合规校验固定放首位（用户拍板 2026-08-13）：它是 seedance 生成的前置动作，
    // 排最前让「先校验再生成」的路径最短。
    {
      key: 'seedance',
      label: 'Seedance2.0 合规校验',
      onPick: () => {
        setMenuPos(null);
        void verify();
      },
      // 时长越界的视频/音频提交必被上游拒（还要等一轮轮询才知道），所以直接不给点。
      // 已通过校验的同样置灰：自己校验过的图再提交只是服务端幂等空转；公共角色库
      // 的图（永远 active）则会被归属校验拒成 403「media not yours」——两种情况
      // 都是「点了也白点」，明示出来。版本轮换后的重新校验由后台 worker 自动做。
      disabled: !canAct || !durationCheck.ok || state?.status === 'active',
      hint: !canAct
        ? `还没有${KIND_LABEL[kind]}内容`
        : state?.status === 'active'
          ? '已通过校验，无需重复提交'
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

  // 状态角标。没校验过的不显示任何东西——绝大多数素材都属于这一类，全都挂标记
  // 只会变成噪音。
  const badge = !state ? null : state.status === 'active' ? (
    <span
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-emerald-600/90 text-white text-[11px] leading-none font-medium"
      title="已通过 Seedance 2.0 合规校验"
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
    // failed 是瞬时态（还在 1 小时自动重试窗口内），跟 pending 一样显示校验中——
    // 网络抖一下就给素材扣个「不合规」是冤枉。
    <span
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-black/60 text-white text-[11px] leading-none"
      title="正在进行 Seedance 合规校验"
    >
      <Loader2 size={11} className="animate-spin" />
      校验中
    </span>
  );

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

  return { onContextMenu, overlay, badge, state };
}
