// 音频「截取/变速」的落地侧:wav Blob → 上传 → 画布落 preview 音频节点并
// 连回源。与 executeAudioSeparate 同一分层:AudioActionsBar 只管模式条与
// toast,这里收口全部副作用,单测打桩 uploadAsset 断言节点形态。
//
// 产物用 preview 节点而不是 input-audio,理由同 audiosep:input-audio 没有
// 输入桩,连不了「源 → 产物」溯源线;音频态靠 settings.kind='audio' 自报
// 类型(签名 URL 无扩展名,下游分类只有 kind 兜得住)。
import { uploadAsset, currentProjectId } from '@/shared/api';
import { DEFAULT_SIZES, useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';

const GAP = 80;

export async function deliverAudioEditProduct(
  nodeId: string,
  blob: Blob,
  fileLabel: string,
): Promise<void> {
  const asset = await uploadAsset(
    new File([blob], `${fileLabel}.wav`, { type: 'audio/wav' }),
    currentProjectId(),
  );

  const store = useCanvasStore.getState();
  const src = store.nodes.find((n) => n.id === nodeId);
  if (!src) throw new Error('源节点不存在');
  const srcW = src.width ?? 320;
  const size = DEFAULT_SIZES['preview'];
  // 反复截取/变速的产物竖向排队:数这个源已有的音频产物下游,
  // 整节点高度往下错开,不然第二次操作的产物会精确压在第一次上。
  const count = store.connections.filter((c) => {
    if (c.from !== nodeId) return false;
    const t = store.nodes.find((n) => n.id === c.to);
    return t?.type === 'preview' && t.settings?.kind === 'audio';
  }).length;

  const newId = store.addNode('preview', { x: src.x, y: src.y });
  if (!newId) throw new Error('创建产物节点失败');
  store.addConnection(nodeId, newId, 'audio');
  store.updateNode(newId, {
    content: asset.url,
    status: 'succeeded',
    x: src.x + srcW + GAP,
    y: src.y + count * (size.height + 40),
  });
  store.updateNodeSettings(newId, { kind: 'audio', assetId: asset.id });
  persistNodeById(newId);
}
