// 跟 apps/api/internal/agent/envelope.go 逐字段一一对应。保持两边对齐：
// 这里改了就要在 Go 那边同步改，反过来也一样。
export type FrameType =
  | 'thread_start' | 'action_start' | 'step_start'
  | 'content_block_start' | 'content_block_delta' | 'content_block_stop'
  | 'step_end' | 'action_end' | 'thread_end' | 'error'
  | 'user_message' | 'abort' | 'ack'
  // canvas_changed 不属于 agent 会话流：它是画布快照被改动后的广播（Hub 按
  // (userID, projectID) 分发），团队观看端据此重拉快照。之所以复用 agent WS
  // 而不是另开一条连接——同一条 socket 已经按项目建好了，多路复用零成本。
  | 'canvas_changed'
  // 瞬态层（二期并发编辑）。canvas_transient 是**双向**的：客户端发出去，
  // 服务端原样转播给同项目的其余连接（不回发送者）；canvas_presence 只由
  // 服务端下发。两者都不落库、断线即失，权威状态永远只看 canvas_changed。
  | 'canvas_transient'
  | 'canvas_presence';

export interface SkillDraft {
  id: string;
  name: string;
  description?: string;
  category?: string;
  system_prompt_addon?: string;
  force_tools?: string[];
  param_defaults?: Record<string, unknown>;
}

export interface Artifact {
  // text_info（create_text_node 工具产出的剧本/文案）与 audio_info（音频类
  // DynamicTool 产物）是 2026-08 后端新增（commit 018a35bf），协议 additive。
  artifact_type:
    | 'image_info'
    | 'video_info'
    | 'audio_info'
    | 'text_info'
    // create_gen_node 工具：铺 gen-image / gen-video 节点预填提示词，不生成不扣费。
    | 'gen_image_node_info'
    | 'gen_video_node_info'
    // 画布操作五件套（后端 tools_canvas_ops.go，2026-08-13）：增/改/删节点、
    // 连/断连线。均无 url，结构化参数随 payload 携带，前端 canvasOps 执行。
    | 'node_create_info'
    | 'node_update_info'
    | 'node_delete_info'
    | 'connection_create_info'
    | 'connection_delete_info'
    | 'skill_definition';
  artifact_id: string;
  url?: string;          // 可选——skill_definition / text_info 没有对象存储 URL
  name?: string;
  width?: number;
  height?: number;
  prompt?: string;
  source?: string;
  skill?: SkillDraft;    // 当 artifact_type === 'skill_definition' 时填充
  /** artifact_type === 'text_info' 时的正文全文——文本产物没有存储 URL，
   *  内容直接随帧携带，前端据此在画布上建文字节点。 */
  text?: string;
  /** 画布操作类 artifact（node_*_info / connection_*_info）的结构化载荷。
   *  后端是 json.RawMessage，帧序列化后是**内嵌 JSON 对象**（不是字符串化的
   *  JSON）；形态按 artifact_type 各不相同，统一在 canvasOps 的
   *  parseCanvasOpPayload 里解——所以这里定成 unknown 而不是具体形态联合。 */
  payload?: unknown;
}

export interface ContentBlock {
  type: 'text' | 'tool_use' | 'tool_result' | 'tool_event' | 'image' | 'video' | 'audio' | 'skill' | 'file' | 'text_ref';
  text?: string;
  id?: string;           // skill block：skill id
  name?: string;         // skill block：skill 显示名称；tool_use block：工具内部名
  /** skill block：true = 这个技能是关键词自动挂上的，不是用户在 SkillPopover 里点的。
   *  存量消息没有该字段（服务端 omitempty），渲染时按 false 处理。 */
  auto?: boolean;
  /** tool_use block：面向用户的工具显示名（后台 display_name / 内置工具中文名），
   *  发帧时刻的快照。存量旧消息没有这个字段——渲染时回退 name。 */
  display_name?: string;
  url?: string;          // 媒体 block（image / video / audio）
  input?: unknown;
  tool_use_id?: string;
  content?: string;
  artifacts?: Artifact[];
  /** file / text_ref block（大文本附件引用）：全文字数，历史气泡 chip 展示用。 */
  char_count?: number;
}

export interface BlockDelta {
  type: 'text_delta' | 'input_json_delta';
  text?: string;
  partial_json?: string;
}

export interface Mention {
  artifact_id: string;
  url?: string;
  name?: string;
  /** 被 @ 的媒体类型。后端 buildUserMessage 据此分流：只有 image（含空 =
   *  旧客户端）挂 image part，视频/音频转成文本引用让 planner 调分析工具——
   *  不带 kind 的话视频/音频会被当图片挂给多模态上游，直连通道是断的。 */
  kind?: 'image' | 'video' | 'audio' | 'file' | 'text';
  /** kind='file'/'text'（大文本附件/画布文字节点，正文不内联）的全文字数。
   *  后端引用行靠它告诉 planner「这有多大」。 */
  char_count?: number;
}

// NodeSummary 是 `canvas_summary` 里的单条记录——一个画布节点的缩略快照，
// LLM 可以按 name/id 引用它。跟 apps/api/internal/agent/envelope.go 里的
// agent.NodeSummary 一一对应。
export interface NodeSummary {
  id: string;
  type: string;
  name?: string;
  url?: string;
  /** 对于 `type` 含糊的节点（preview 可能是 image 也可能是 video），
   *  用这个来消歧。仅前端用——不属于 agent.NodeSummary 的传输契约，
   *  所以加它不需要改后端。'text' = 文字节点：没有 URL，@ 它时正文
   *  内联进消息文本（复用 .txt/.md chip 的内联机制），不进 mentions[]。 */
  kind?: 'image' | 'video' | 'audio' | 'text';
  /** kind === 'text' 时携带节点正文（settings.text），发送时内联用。 */
  text?: string;
}

export interface Frame {
  type: FrameType;
  project_id?: number;
  thread_id?: string;
  action_id?: string;
  message_id?: string;
  index?: number;
  block?: ContentBlock;
  delta?: BlockDelta;
  stop_reason?: string;
  error?: { code: string; message: string };
  content?: string;
  mentions?: Mention[];
  skill_id?: string;
  /** canvas_changed 专用：变更后的快照版本。观看端据此丢掉过期/回声帧。
   *  后端是 `*int64` + omitempty，所以 version=0 也会真的下发（不会消失）。 */
  version?: number;
  /** canvas_changed 专用：发起方 ownerToken 末段的前 8 位指纹，后端回写恒为
   *  "server"。不下发完整 token——帧会推给项目下所有成员连接，带全 token
   *  等于广播编辑权凭据。前端算法必须同规则：`token.split(':').pop().slice(0,8)`。 */
  source_mark?: string;
  /** canvas_changed 专用（二期并发编辑）：本次写入的变更摘要。带它就能在本地
   *  合并出同一结果，不必整拉快照（整拉会清掉选中/undo/正在输入的面板）。
   *  **可选**——一期客户端忽略它继续整拉；摘要序列化失败时后端也不带，
   *  接收端一律退回整拉兜底。 */
  op?: CanvasOpFramePayload;
  /** canvas_transient 专用：瞬态种类（'drag' | 'node_editing'）。后端**不做
   *  白名单**（种类还会增长），前端同样按「不认识就忽略」处理，不当闭合枚举。 */
  kind?: string;
  /** canvas_transient 专用：发帧的真人成员 id。**服务端覆写**，客户端自报的
   *  会被丢弃——名册与角标都靠它认人，可伪造就等于可冒名。 */
  actor_user_id?: number;
  /** canvas_transient(kind=drag)：拖拽途中的绝对坐标批。 */
  positions?: TransientPositionFramePayload[];
  /** canvas_transient(kind=node_editing)：哪个节点、进入还是退出编辑。 */
  node_id?: string;
  editing?: boolean;
  /** canvas_presence：项目下的在线成员**全量**名册（有人进出就整份重发）。 */
  members?: PresenceMemberFramePayload[];
}

/** 线格式，**不做校验**——运行时校验在 transientChannel 的
 *  parseTransientDragPositions / presenceStore 的 parsePresenceMembers。 */
export interface TransientPositionFramePayload {
  id: string;
  x: number;
  y: number;
}

export interface PresenceMemberFramePayload {
  actor_user_id: number;
  email?: string;
}

/**
 * `canvas_changed` 帧里的 op 载荷，与后端 `canvasstate.CanvasOpSummary` 一一对应
 * （snake_case 线格式）。**这里只描述线上形状，不做校验**：字段可信度由
 * `parseRemoteCanvasOp`（editor/remoteOpFrame.ts）在运行时把关，翻译成 store 的
 * `RemoteCanvasOp` 后才进画布——kind 是会增长的枚举，认不出来就退回整拉，
 * 不能让一个新 kind 把老客户端搞崩。
 */
export interface CanvasOpFramePayload {
  kind?: string;
  node?: unknown;
  node_id?: string;
  patch?: unknown;
  node_ids?: unknown;
  positions?: unknown;
  connection?: unknown;
  connection_id?: string;
}
