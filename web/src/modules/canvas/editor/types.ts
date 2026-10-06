export type NodeType =
  | 'input-image'
  | 'input-audio'
  | 'text-node'
  | 'gen-image'
  | 'gen-video'
  | 'gen-audio'
  | 'video-input'
  | 'image-compare'
  | 'local-save'
  | 'preview'
  | 'subtitle-erase'
  | 'video-upscale'
  | 'video-whitemodel'
  | 'script'
  | 'director-desk';

export type NodeStatus = 'idle' | 'uploading' | 'generating' | 'succeeded' | 'failed';

export interface CanvasNode {
  id: string;
  type: NodeType;
  x: number;
  y: number;
  width: number;
  height: number;
  content?: string;
  settings?: Record<string, unknown>;
  status?: NodeStatus;
  progress?: number;
  error?: string;
  // 异步生成进行中时会持久化 taskId，这样页面刷新后我们能恢复 polling。
  taskId?: number;
}

export interface Connection {
  id: string;
  from: string;
  to: string;
  inputType?: string;
}

export interface ViewState {
  x: number;
  y: number;
  zoom: number;
}

export interface CanvasSnapshot {
  nodes: CanvasNode[];
  connections: Connection[];
  view: ViewState;
}

// 上游连线媒体的带身份引用：@ 提及 chip 用 nodeId 绑定身份(连线增删时序号会漂移,
// id 不会)；kind 决定「图片/视频/音频」各自独立的下标序列。
export interface ConnectedMediaRef {
  nodeId: string;
  kind: 'image' | 'video' | 'audio';
  url: string;
  /** 上游节点的自定义名（settings.name，去空白）。提示词里 `@张三` 这类文字引用
   *  按它匹配成 chip；没改过名的节点为空，不参与匹配（默认名「图片」没有引用价值）。 */
  name?: string;
}

export interface ConnectedInputs {
  images: string[];
  texts: string[];
  videos: string[];
  audios: string[];
  // 与 images/videos/audios 同一次遍历产出——顺序严格一致,序列化「图片N」的
  // 下标才能与提交给后端的数组下标对上。
  refs: ConnectedMediaRef[];
}
