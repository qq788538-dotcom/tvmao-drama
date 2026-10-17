package agent

import (
	"encoding/json"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasstate"
)

type FrameType string

const (
	// 服务端 → 客户端
	FrameThreadStart       FrameType = "thread_start"
	FrameActionStart       FrameType = "action_start"
	FrameStepStart         FrameType = "step_start"
	FrameContentBlockStart FrameType = "content_block_start"
	FrameContentBlockDelta FrameType = "content_block_delta"
	FrameContentBlockStop  FrameType = "content_block_stop"
	FrameStepEnd           FrameType = "step_end"
	FrameActionEnd         FrameType = "action_end"
	FrameThreadEnd         FrameType = "thread_end"
	FrameError             FrameType = "error"
	// FrameCanvasChanged 是「这个项目的画布快照变了」的权威通知帧（version + 源指纹
	// + 后来新增的 op 变更摘要）。它复用 agent 这条既有 socket，正是为了不为画布协作
	// 再开一条连接。
	//
	// 新版为什么**不**换成新的 canvas_op 帧名：旧版前端只认 type=="canvas_changed"，
	// 改名等于让所有还没刷新的页面瞬间失聪（协作靠的就是这条帧）。改为在同一帧里
	// 加一个可选的 op 字段——旧版前端忽略未知字段、继续整拉快照；新版前端见 op 就
	// 本地合并，没 op（后端未升级/摘要序列化失败）才回退整拉。加字段零破坏，是这里
	// 唯一能同时满足「兼容旧页面」和「带上变更数据」的形态。
	FrameCanvasChanged FrameType = "canvas_changed"

	// 客户端 → 服务端
	FrameUserMessage FrameType = "user_message"
	FrameAbort       FrameType = "abort"
	FrameAck         FrameType = "ack"
)

type Frame struct {
	Type       FrameType     `json:"type"`
	ProjectID  int64         `json:"project_id,omitempty"`
	ThreadID   string        `json:"thread_id,omitempty"`
	ActionID   string        `json:"action_id,omitempty"`
	MessageID  string        `json:"message_id,omitempty"`
	Index      int           `json:"index,omitempty"`
	Block      *ContentBlock `json:"block,omitempty"`
	Delta      *BlockDelta   `json:"delta,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"`
	Error      *ErrorPayload `json:"error,omitempty"`

	// canvas_changed 专用：Version 是变更后的快照版本（观看端据此忽略过期帧），
	// SourceMark 是发起方 ownerToken 的 8 位指纹 / 后端回写的 "server"
	// （观看端据此跳过自己触发的回声）。字段名沿用本结构体的 snake_case 惯例。
	//
	// Version 是指针而不是 int64：值类型 + omitempty 会让 version=0（快照行刚建、
	// 或回写 no-op）从 JSON 里整个消失，观看端的比较规则读到 undefined 而不是 0；
	// 去掉 omitempty 又会给每一条高频 delta 帧都挂上一个无意义的 "version":0。
	// 指针两头都占：canvas_changed 恒带（含 0），其它帧一个字节不多。
	Version    *int64 `json:"version,omitempty"`
	SourceMark string `json:"source_mark,omitempty"`

	// Op 是 canvas_changed 帧的**变更摘要**（可选）：收到它的编辑端直接
	// 在本地重放这一笔变更，不必整拉快照——整拉会清掉选中/拖拽/文本光标等本地交互态，
	// 多人同时编辑时等于互相打断。合并语义逐条写在 canvasstate.CanvasOpSummary 上，
	// 前端 applyRemoteOp 必须与之同构。
	//
	// 类型直接复用 canvasstate 的结构体而不是在这里再抄一份：抄一份就意味着两处
	// json tag 各自演化，某天加字段只改一边，帧里就会静默少一个字段。canvasstate
	// 只依赖标准库，不会引入循环依赖。
	Op *canvasstate.CanvasOpSummary `json:"op,omitempty"`

	// 入站的 user_message
	Content  string    `json:"content,omitempty"`
	Mentions []Mention `json:"mentions,omitempty"`
	SkillID  string    `json:"skill_id,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// DisplayName 是 tool_use 块的面向用户名称（agent_tools.display_name /
	// 内置工具的中文名），发帧时刻的快照——后台改名不回溯已存消息。
	// 内部 Name 是 planner 的函数标识符，不该直接暴露在对话 UI 上。
	DisplayName string          `json:"display_name,omitempty"`
	URL         string          `json:"url,omitempty"` // image block（用户附件 / canvas 引用）
	Input       json.RawMessage `json:"input,omitempty"`
	ToolUseID   string          `json:"tool_use_id,omitempty"`
	Content     string          `json:"content,omitempty"`
	Artifacts   []Artifact      `json:"artifacts,omitempty"`
	// Auto 只用在 skill block 上：true = 这个技能是关键词自动挂的，不是用户在
	// SkillPopover 里点的。前端据此把 chip 标成"自动"，否则用户会困惑气泡上
	// 凭空多出来的标签是哪来的。omitempty 保证存量消息的 JSON 逐字不变。
	Auto bool `json:"auto,omitempty"`
	// CharCount 只用在 file / text_ref block（大文本附件引用）上：历史回放时
	// 重新渲染引用行需要字数。omitempty 保证存量消息 JSON 逐字不变。
	CharCount int64 `json:"char_count,omitempty"`
}

type BlockDelta struct {
	Type        string `json:"type"` // 'text_delta' | 'input_json_delta'
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type Artifact struct {
	ArtifactType string      `json:"artifact_type"`
	ArtifactID   string      `json:"artifact_id"`
	URL          string      `json:"url"`
	Name         string      `json:"name,omitempty"`
	Width        int         `json:"width,omitempty"`
	Height       int         `json:"height,omitempty"`
	Prompt       string      `json:"prompt,omitempty"`
	Source       string      `json:"source,omitempty"`
	Skill        *SkillDraft `json:"skill,omitempty"`
	// Text 是 artifact_type 为 "text_info" 时的正文载荷（create_text_node 工具
	// 产出的剧本/文案全文）。文本产物没有对象存储 URL，内容直接随帧携带并落库
	//（tool_event block 里），前端据此在画布上建文字节点。
	Text string `json:"text,omitempty"`
	// Payload 是画布操作类 artifact（node_create/update/delete、connection_*）
	// 的结构化载荷（节点 id、settings patch、连线端点等）。操作语义各不相同，
	// 与其给每种操作开一批顶层字段，不如让各工具自带 JSON——前端按
	// artifact_type 解对应形态。
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SkillDraft 是 artifact_type 为 "skill_definition" 的 Artifact 携带的载荷——
// 当 LLM 处于 meta-skill 流程、要提议一个新的用户 skill 时，由
// output_skill_definition meta 工具发出。前端会弹一个确认 modal 让用户保存它。
type SkillDraft struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	Category          string         `json:"category,omitempty"`
	SystemPromptAddon string         `json:"system_prompt_addon,omitempty"`
	ForceTools        []string       `json:"force_tools,omitempty"`
	ParamDefaults     map[string]any `json:"param_defaults,omitempty"`
}

type Mention struct {
	ArtifactID string `json:"artifact_id"`
	URL        string `json:"url,omitempty"`
	Name       string `json:"name,omitempty"`
	// Kind 标记被 @ 的媒体类型（image / video / audio；空 = 旧客户端，按 image
	// 处理）。buildUserMessage 据此分流：只有图片能作为 image part 直接喂给
	// 多模态上游；视频/音频挂成 image_url 会被上游拒绝或误读（曾经全部一律
	// 当图片挂，@ 视频的直连通道是断的），改为转成文本引用让 planner 自己调
	// analyze_video 这类工具查看。
	Kind string `json:"kind,omitempty"`
	// CharCount 是 kind='file'/'text'（大文本附件/画布文字节点，正文不内联）
	// 的全文字数。引用行靠它告诉 planner「这有多大」，planner 据此决定分几段读。
	CharCount int64 `json:"char_count,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
