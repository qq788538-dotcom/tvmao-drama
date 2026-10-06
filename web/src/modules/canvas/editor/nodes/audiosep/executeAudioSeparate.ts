// 「音视频分离」的落地侧:拆轨 → 双上传 → 画布落 音频 + 无声视频 两个
// preview 节点并连回源。与 executeGridSplit 同一分层思路:VideoActionsBar
// 只管菜单与 toast,这里收口全部副作用,单测直接打桩 separateAudioVideo /
// uploadAsset 断言节点形态。
//
// 产物用 preview 节点而不是 input-audio:input-audio 没有输入桩,连不了
// 「源 → 产物」这条溯源线;preview 是剪辑/裁剪产物的既有惯例,音频态靠
// settings.kind='audio' 自报类型(签名 URL 无扩展名,下游分类只有 kind 兜得住)。
import { uploadAsset, currentProjectId } from '@/shared/api';
import { DEFAULT_SIZES, useCanvasStore } from '../../store';
import { persistNodeById } from '../../persistence/canvasMutations';
import { separateAudioVideo } from './videoAudioSeparate';

// 布局对标 liblib:源 → _音轨 → _无声 同排从左到右,80 间距,顶边对齐
// (同 trim 产物的 y: src.y)。
const GAP = 80;

export async function executeAudioSeparate(
  nodeId: string,
  url: string,
  onProgress?: (progress: number) => void,
): Promise<void> {
  const { audio, video } = await separateAudioVideo(url, onProgress);
  const projectId = currentProjectId();
  // 两个上传互不依赖,并行省一半等待。
  const [audioAsset, videoAsset] = await Promise.all([
    uploadAsset(new File([audio], 'audio-track.wav', { type: 'audio/wav' }), projectId),
    uploadAsset(new File([video], 'muted-video.mp4', { type: 'video/mp4' }), projectId),
  ]);

  const store = useCanvasStore.getState();
  const src = store.nodes.find((n) => n.id === nodeId);
  if (!src) throw new Error('源节点不存在');
  const srcW = src.width ?? 320;
  const srcH = src.height ?? 260;
  const audioSize = DEFAULT_SIZES['preview'];

  // addNode 以中心点落位,先随手放,下面 updateNode 直接写最终 x/y/尺寸
  // (同 trim 产物的做法)。音频节点用 preview 默认尺寸;无声视频画面没变,
  // 抄源节点尺寸才不会视觉跳变。
  const audioId = store.addNode('preview', { x: src.x, y: src.y });
  if (audioId) {
    store.addConnection(nodeId, audioId, 'audio');
    store.updateNode(audioId, {
      content: audioAsset.url,
      status: 'succeeded',
      x: src.x + srcW + GAP,
      y: src.y,
    });
    store.updateNodeSettings(audioId, { kind: 'audio' });
    persistNodeById(audioId);
  }

  const videoId = store.addNode('preview', { x: src.x, y: src.y });
  if (videoId) {
    store.addConnection(nodeId, videoId, 'video');
    store.updateNode(videoId, {
      content: videoAsset.url,
      status: 'succeeded',
      width: srcW,
      height: srcH,
      x: src.x + srcW + GAP + audioSize.width + GAP,
      y: src.y,
    });
    store.updateNodeSettings(videoId, { kind: 'video' });
    persistNodeById(videoId);
  }
}
