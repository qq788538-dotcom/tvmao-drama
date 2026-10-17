package http

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// AgentHandler 暴露 agent 的 WS upgrader，以及给右侧聊天面板做引导的
// thread/message REST 端点。桌面端：token 已由 localauth 校验（WS 走 ?token=），
// 用户恒为本地用户 1，没有团队与多人协作。
type AgentHandler struct {
	Hub    *agent.Hub
	Repo   agent.Repository
	Runner AgentRunner
	Signer agentURLSigner
	// BaseCtx 是每个 Run 的父 ctx（sidecar 的应用级 ctx）。退出时取消它，进行中的
	// Run 随之停下。nil = context.Background()。
	BaseCtx context.Context

	// runs 跟踪进行中的 Run goroutine，供关闭流程在关库前等它们收尾（Done）。
	runMu   sync.Mutex
	runs    sync.WaitGroup
	closing bool
}

// startRun 登记一个 Run goroutine；已进入关闭流程返回 false（调用方不应再起 Run）。
func (h *AgentHandler) startRun() bool {
	h.runMu.Lock()
	defer h.runMu.Unlock()
	if h.closing {
		return false
	}
	h.runs.Add(1)
	return true
}

// Done 进入关闭状态（之后拒绝新 Run），返回的通道在已登记的 Run 全部退出后关闭。
// 调用前应已取消 BaseCtx，否则要等 Run 自然结束。
func (h *AgentHandler) Done() <-chan struct{} {
	h.runMu.Lock()
	h.closing = true
	h.runMu.Unlock()
	done := make(chan struct{})
	go func() { h.runs.Wait(); close(done) }()
	return done
}

// agentURLSigner 是出口签名需要的最小能力（*MediaSigner 满足）。
type agentURLSigner interface {
	Sign(s string) string
}

func NewAgentHandler(hub *agent.Hub, repo agent.Repository, runner AgentRunner, signer agentURLSigner) *AgentHandler {
	return &AgentHandler{Hub: hub, Repo: repo, Runner: runner, Signer: signer}
}

// AgentRunner 是 handler 需要的 *agent.Agent 的那一小块能力。把它做成
// interface，让测试可以注入一个 stub，而不用拉起一个真的 LLM client。
// userID 传进来，让 runner 在活跃的 backbone 委托给 newapi 时能解析出
// 调用方自己的上游 sk-key。projectID 一路传下去，让 DynamicTool 的
// GenService.Submit 能把 generation_tasks row 归到正确的项目下。skillID
// 是可选的；runner 会加载匹配的 Skill row 并传给
// agent.Agent.Run，让本次 Run 的工具集 + system prompt addon 相应地被限定。
// 空的 skillID 表示不受限。
type AgentRunner interface {
	Run(ctx context.Context, userID, projectID int64, threadID, actionID, userText string, mentions []agent.Mention, skillID string, sink agent.FrameSink) error
}

// agentRunTimeout 是单次 agent Run 的整体预算（含多回合 + 所有工具的串行轮询）。
// 必须明显大于单个生成工具的 maxWait（5min），否则多图 / 多步请求会在产出最终
// 回复前就超时。
const agentRunTimeout = 60 * time.Minute

// WS：token 已由外层 localauth 中间件校验，这里直接升级连接，
// 在 Hub 注册，然后循环读取进来的 frame（user_message / abort / ack），
// 直到客户端断开。
func (h *AgentHandler) WS(w http.ResponseWriter, r *http.Request) {
	uid := agentUserID(r)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusInternalError, "shutting down")
	// coder/websocket 默认单条消息只读 32KB，超了就 ErrMessageTooBig + 关连接。
	// 用户把整篇小说粘进聊天（洗稿等场景）轻松超 32KB，会导致下面 c.Read 直接
	// 报错退出循环、Runner.Run 根本没跑、前端一直转圈。放宽到 8MB（中文小说
	// 百万字 ~3MB），既覆盖正常用法又给极端 payload 留个上界、不至于无限吃内存。
	c.SetReadLimit(8 << 20)

	ctx := r.Context()
	connectionProjectID, _ := strconv.ParseInt(r.URL.Query().Get("project_id"), 10, 64)
	if connectionProjectID < 0 {
		connectionProjectID = 0
	}
	h.Hub.Register(ctx, uid, connectionProjectID, c)
	defer h.Hub.Unregister(uid, c)
	sink := agent.UserSink{Hub: h.Hub, UserID: uid, ProjectID: connectionProjectID}

	// 进行中 Run 的 cancel 注册在 Hub 上（不是这里的局部变量）：WS 被代理掐断后
	// 客户端会重连出一条新连接，abort 帧多半从新连接进来，而 Run 跑在
	// context.Background() 上仍然活着。注册表跟着连接走的话，那种情况下点「停止」
	// 就只能眼睁睁看它把 60min 预算烧完。
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			// 正常关闭 / 客户端离开 / ctx 取消都静默；其余（如超 ReadLimit 被
			// StatusMessageTooBig 关掉）打一行——否则这类故障在服务端完全不可见，
			// 排查只能靠猜。
			if s := websocket.CloseStatus(err); s != websocket.StatusNormalClosure &&
				s != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
				log.Printf("agent ws: read 终止 user=%d: %v", uid, err)
			}
			return
		}
		var f agent.Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		switch f.Type {
		case agent.FrameUserMessage:
			actionID := f.ActionID
			if actionID == "" {
				actionID = uuid.NewString()
			}
			// 正文长度兜底：前端把 >100KB 的附件/文字节点分流
			// 到 read_attachment 通道、输入框超长禁止提交，这里挡「绕过前端直连
			// WS 塞大文本」。上限给足余量（几个 100KB 内联附件 + 正文），但远低
			// 于 8MB ReadLimit——超过它的正文没有任何合法来路。
			if len(f.Content) > maxAgentUserTextBytes {
				agent.SendFatal(sink, f.ThreadID, actionID, "message_too_long", "消息正文过长，请将长文本保存为 .txt/.md 作为附件上传")
				continue
			}
			th, terr := h.Repo.GetThread(f.ThreadID)
			if terr != nil {
				// 完整三件套（error + action_end + thread_end），而不是裸 error 帧：
				// 前端按 thread_id 路由帧，裸帧缺字段被整帧丢弃；且这条路径连
				// action_start 都还没发，没有 action_end 的话 sending 永远卡住。
				agent.SendFatal(sink, f.ThreadID, actionID, "thread_not_found", terr.Error())
				continue
			}
			// 归属校验。少了这一条，任何登录用户拿到别人的 thread UUID 发一条
			// user_message，buildHistory 就会把那个 thread 的全部历史读出来喂
			// planner，而 sink 按**发起者**的 uid 广播——别人的对话内容会流回
			// 攻击者自己的面板，同时还往受害者 thread 里追加了消息。REST 侧的
			// ListMessages/Update/Delete 都做了这个检查，WS 这条路曾经漏掉。
			// 报错文案跟 thread 不存在时保持一致，不泄露「这个 ID 确实存在」。
			if th.UserID != uid {
				agent.SendFatal(sink, f.ThreadID, actionID, "thread_not_found", "thread not found")
				continue
			}
			threadID := f.ThreadID
			projectID := th.ProjectID
			runSink := agent.UserSink{Hub: h.Hub, UserID: uid, ProjectID: projectID}
			content := f.Content
			mentions := f.Mentions
			skillID := f.SkillID
			// 整个 Run 的预算。多图 / 多步生成是串行的（每个工具各自 poll 最多
			// 5min），单张稍慢就可能把旧的 5min 预算耗光、在拿到最终回复前就被
			// ctx 取消，导致前端永久转圈。放宽到 60min，给多图 / 多轮留足余量。
			base := h.BaseCtx
			if base == nil {
				base = context.Background()
			}
			if !h.startRun() {
				agent.SendFatal(sink, threadID, actionID, "shutting_down", "服务正在关闭")
				continue
			}
			rctx, cancel := context.WithTimeout(base, agentRunTimeout)
			h.Hub.RegisterRun(uid, threadID, actionID, cancel)
			go func() {
				defer func() {
					defer h.runs.Done()
					cancel()
					h.Hub.UnregisterRun(uid, threadID, actionID)
				}()
				_ = h.Runner.Run(rctx, uid, projectID, threadID, actionID, content, mentions, skillID, runSink)
			}()
		case agent.FrameAbort:
			// cancel 对应 Run 的 ctx——会一路传到 LLM 流式 Recv 和工具轮询，让它们
			// 立刻收手。按 (uid,thread,action) 匹配，所以拿别人的 threadID 发 abort
			// 掐不掉别人的任务。
			h.Hub.CancelRun(uid, f.ThreadID, f.ActionID)
		case agent.FrameAck:
			// noop——不需要 ack 记账。
		default:
			// 其余帧（含旧前端可能发来的 canvas_transient）一律丢弃：桌面端没有多人协作。
		}
	}
}

// ListThreads 返回当前用户在 URL 指定项目下的所有 thread。
func (h *AgentHandler) ListThreads(w http.ResponseWriter, r *http.Request) {
	uid := agentUserID(r)
	projectID := projectIDFromContext(r.Context())
	threads, err := h.Repo.ListThreadsByProject(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 过滤出调用方自己的 thread（项目归属已由上游的 ProjectFromURL
	// 强制校验过，但多用户项目是可能的）。
	out := make([]agent.Thread, 0, len(threads))
	for _, t := range threads {
		if t.UserID == uid {
			out = append(out, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// CreateThread 在服务端初始化一个新的 thread row。客户端也可以在本地生成
// 一个 UUID，在第一条 user_message 里内联带上——那时服务端做 upsert——
// 但这个端点是给显式的「+ 新对话」按钮兜底用的。
func (h *AgentHandler) CreateThread(w http.ResponseWriter, r *http.Request) {
	uid := agentUserID(r)
	projectID := projectIDFromContext(r.Context())
	var body struct {
		Title string `json:"title"`
	}
	_ = decodeJSON(r, &body)
	if body.Title == "" {
		body.Title = "新对话"
	}

	id := uuid.NewString()
	if err := h.Repo.CreateThread(agent.Thread{
		ID: id, ProjectID: projectID, UserID: uid, Title: body.Title,
	}); err != nil {
		if errors.Is(err, agent.ErrProjectNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "title": body.Title})
}

// UpdateThread 重命名调用方拥有的 thread。
func (h *AgentHandler) UpdateThread(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")
	uid := agentUserID(r)
	t, err := h.Repo.GetThread(threadID)
	if errors.Is(err, agent.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if t.UserID != uid {
		writeError(w, http.StatusForbidden, "not your thread")
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "title required")
		return
	}
	if err := h.Repo.UpdateThreadTitle(threadID, body.Title); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": threadID, "title": body.Title})
}

// DeleteThread 删除调用方拥有的 thread（并通过 FK 级联删除其消息）。
func (h *AgentHandler) DeleteThread(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")
	uid := agentUserID(r)
	t, err := h.Repo.GetThread(threadID)
	if errors.Is(err, agent.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if t.UserID != uid {
		writeError(w, http.StatusForbidden, "not your thread")
		return
	}
	if err := h.Repo.DeleteThread(threadID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": threadID})
}

// ListMessages 返回调用方拥有的某个 thread 的消息。缺省（不带 limit）返回全部
// 活跃消息——历史行为，逐字节保留；带 `?limit=N` 走开窗分页：返回排序上最新的
// N 条（或严格早于 `?before=<消息id>` 游标的 N 条），并在响应里多一个 has_more。
// 分页只服务前端懒加载；agent 运行时的历史回放不走 HTTP，不受影响。
func (h *AgentHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")
	uid := agentUserID(r)

	t, err := h.Repo.GetThread(threadID)
	if errors.Is(err, agent.ErrThreadNotFound) {
		writeError(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if t.UserID != uid {
		writeError(w, http.StatusForbidden, "not your thread")
		return
	}

	// limit 非法值（非数字/负数/0）一律当缺省处理走全量，而不是报 400——
	// 全量是这个端点自诞生就有的安全语义，宽松解析让老客户端永远不会被拒。
	// 上限 200 是硬防线而非产品参数（前端页大小是 30）：repo 按 limit+1 预分配
	// 切片，不封顶的话登录用户一句 ?limit=2000000000 就是一次 ~350GB 的
	// make——Go 的 OOM 是 fatal 不是可恢复 panic，单二进制整个进程直接没。
	const maxPageLimit = 200
	limit := 0
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
			if limit > maxPageLimit {
				limit = maxPageLimit
			}
		}
	}

	var (
		msgs    []agent.Message
		hasMore bool
	)
	if limit > 0 {
		msgs, hasMore, err = h.Repo.ListMessagesPageBefore(threadID, r.URL.Query().Get("before"), limit)
	} else {
		msgs, err = h.Repo.ListMessages(threadID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range msgs {
		msgs[i].ContentBlocks = signContentBlockURLs(msgs[i].ContentBlocks, h.Signer)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": msgs, "has_more": hasMore})
}

// urlInTextRe 匹配自由文本里任何 http(s) 或 media:// 的 URL 子串。
// 我们在出口处把它们全部重新签名：
//   - media://——canonical 的 DB 形式，换成一个新鲜的签名 https URL
//   - https://…（带 bucket host）——过期的签名 URL，重新签名
//     （MediaSigner.Sign 内部会调 storage.ExtractKey，它知道怎么把
//     这两种形式都剥回成 key）
//   - https://…（外部 host）——Sign 原样返回，无害
var urlInTextRe = regexp.MustCompile(`(?:https?|media)://[^\s"'<>)]+`)

// signTextMediaURLs 把文本里每个 media:// 或过期签名的 URL 子串改写成一个
// 新鲜的签名 URL。对非 bucket 的 URL 是 no-op（Sign 原样放行）。
func signTextMediaURLs(s string, signer agentURLSigner) string {
	if signer == nil || s == "" {
		return s
	}
	return urlInTextRe.ReplaceAllStringFunc(s, func(u string) string {
		return signer.Sign(u)
	})
}

// signContentBlockURLs 遍历持久化的 content_blocks 载荷，把任何 `media://key`
// （或过期签名）的 URL 刷新成新鲜的预签名 URL。用在出口边界，让前端即便在
// DB row 已经存了好几天的情况下也总能看到一个有效的 URL。结构化的 `url`
// 字段和自由文本 text/content 子串都会被改写。
func signContentBlockURLs(raw json.RawMessage, signer agentURLSigner) json.RawMessage {
	if signer == nil || len(raw) == 0 {
		return raw
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return raw
	}
	for _, b := range blocks {
		if u, ok := b["url"].(string); ok && u != "" {
			b["url"] = signer.Sign(u)
		}
		// LLM 回显的 URL 藏在 text/content 字符串里——做子串替换。
		if t, ok := b["text"].(string); ok && t != "" {
			b["text"] = signTextMediaURLs(t, signer)
		}
		if c, ok := b["content"].(string); ok && c != "" {
			b["content"] = signTextMediaURLs(c, signer)
		}
		if arts, ok := b["artifacts"].([]any); ok {
			for _, a := range arts {
				if am, ok := a.(map[string]any); ok {
					if u, ok := am["url"].(string); ok && u != "" {
						am["url"] = signer.Sign(u)
					}
				}
			}
		}
	}
	out, err := json.Marshal(blocks)
	if err != nil {
		return raw
	}
	return out
} // maxAgentUserTextBytes 是单条 user_message 正文的字节上限（4MB）。
// 与 SetReadLimit(8MB) 的关系：ReadLimit 管整帧（含 mentions 等），这里专管
// 正文——正常路径的正文由前端保证 ≤ 若干个 100KB 内联段，4MB 全是余量。
const maxAgentUserTextBytes = 4 << 20
