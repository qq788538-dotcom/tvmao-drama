package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
)

// Hub 把帧分发到按 (user, project) 维度的 WS 连接，并跟踪进行中 Run 的 cancel
// 函数（key 为 (user,thread,action)）。Phase 0 是单进程的；多实例横向扩展
// （Redis pubsub）以后再做。
//
// 为什么 cancel 注册表挂在 Hub 而不是 WS handler 的局部变量上：线上 WS 会被反向
// 代理 idle-timeout 掐断，客户端自动重连后是**一条新连接**。如果注册表随连接走，
// 重连后点「停止」就找不到任何 cancel——而后端 Run 跑在 context.Background() 上，
// 会带着 60min 预算继续烧钱把工具跑完。挂在 Hub 上（进程级）才能跨连接停下它。
type Hub struct {
	mu    sync.RWMutex
	conns map[int64][]*hubConn
	runs  map[string]*runEntry
}

// runEntry 是 runs 表的值：cancel 之外还要一个 refs 计数。为什么需要它：
// UnregisterRun 的签名里没有「我是哪次注册」的身份（调用方是 agent_handlers.go，
// 不想为此改 HTTP 层签名），所以当同 key 被重复注册、旧 Run 结束时走自己的
// defer UnregisterRun，光凭 key 无法区分「删的是不是自己那次注册」。
// Register/Unregister 严格配对（handler 里 defer 保证），因此用「同 key 尚未
// Unregister 的注册次数」就能解决：refs 归零才真正删条目，被覆盖的旧 Run 那次
// Unregister 只是减一，不会把新 Run 的 cancel 误删掉。
//
// 已知取舍：若新 Run 先结束、旧 Run 的 defer 后到，中间短窗内条目里存的是已
// 结束的新 cancel——此时 CancelRun 会调一个已 cancel 的 CancelFunc（幂等 no-op）
// 并返回 true。窗口只存在于两个 defer 之间，且不泄漏（最后一次 Unregister
// 仍会删掉条目），不值得为它引入更重的世代结构。
type runEntry struct {
	cancel context.CancelFunc
	refs   int
}

// frameWriter 是 *websocket.Conn 在 Hub 里被用到的唯一方法，也是 Register/
// Unregister 用来指代一条连接的句柄类型。抽成接口是为了让
// 「写阻塞」这类行为能在单测里被真实构造出来
// （真 socket 要灌满内核缓冲区才会阻塞）。
type frameWriter interface {
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
}

type hubConn struct {
	ws        frameWriter
	userID    int64
	projectID int64
	ctx       context.Context
}

func NewHub() *Hub {
	return &Hub{conns: map[int64][]*hubConn{}, runs: map[string]*runEntry{}}
}

// Register 把一个已经 accept 的 ws 连接挂到 hub 上。让 handler 保活（比如 park 在
// r.Context().Done() 上）是调用方的责任——hub 不驱动 read loop。
// 桌面端没有多人协作（spec X2），注册不广播任何帧。
func (h *Hub) Register(ctx context.Context, userID int64, projectID int64, ws frameWriter) {
	c := &hubConn{ws: ws, userID: userID, projectID: projectID, ctx: ctx}
	h.mu.Lock()
	h.conns[userID] = append(h.conns[userID], c)
	h.mu.Unlock()
}

func (h *Hub) Unregister(userID int64, ws frameWriter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.conns[userID]
	for i, c := range list {
		if c.ws == ws {
			h.conns[userID] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

func (h *Hub) SendToUser(userID int64, f Frame) {
	h.mu.RLock()
	conns := append([]*hubConn(nil), h.conns[userID]...)
	h.mu.RUnlock()
	buf, err := json.Marshal(f)
	if err != nil {
		return
	}
	for _, c := range conns {
		_ = writeFrameWithTimeout(c.ctx, c.ws, buf, agentFrameWriteTimeout)
	}
}

func (h *Hub) SendToProject(userID, projectID int64, f Frame) {
	if projectID <= 0 {
		h.SendToUser(userID, f)
		return
	}
	if f.ProjectID == 0 {
		f.ProjectID = projectID
	}
	h.mu.RLock()
	all := append([]*hubConn(nil), h.conns[userID]...)
	h.mu.RUnlock()
	buf, err := json.Marshal(f)
	if err != nil {
		return
	}
	for _, c := range all {
		if c.projectID == projectID {
			_ = writeFrameWithTimeout(c.ctx, c.ws, buf, agentFrameWriteTimeout)
		}
	}
}

// canvasNotifyWriteTimeout 是 canvas_changed 每条连接的写超时。
//
// 为什么只有这条帧路径需要它：广播的调用方是**同步的画布保存 HTTP handler 和生成
// 回写 worker**。一个「TCP 还活着但不读」的观看端（切后台被冻结、进程卡死）会让
// c.ws.Write 一直阻塞在内核缓冲区上，于是编辑者每次保存都要陪着一起卡——观看功能
// 反过来拖垮了编辑。
var canvasNotifyWriteTimeout = 2 * time.Second

// agentFrameWriteTimeout 是 Agent 帧每条连接的写超时。Agent 帧走在 Run 的 goroutine
// 里，放宽到 30s（正常写在毫秒级）；只防 WebView 被冻结但 TCP 还活着时 Run 永久卡住。
// 超时后 coder/websocket 会把连接置为失败态，读循环退出、defer 摘表。
var agentFrameWriteTimeout = 30 * time.Second

// NotifyCanvasChanged 把一次画布变更广播给该项目下的所有连接。
//
// 桌面端只有本地用户，键是 (1, projectID)。
//
// 自己铺一遍分发循环而不复用 SendToProject，是为了给每条连接的写加超时（见
// canvasNotifyWriteTimeout）；顺带挡掉 projectID<=0——SendToProject 那条退化成
// SendToUser 的分支会把帧撒到该用户的全部页面，画布变更绝不该那样。
//
// op 是二期加的变更摘要，可以为 nil（个人路径、或调用方拿不到摘要时）：nil 时帧的
// 字节形态与一期**逐字相同**，一期前端照常整拉。
func (h *Hub) NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary) {
	if userID <= 0 || projectID <= 0 {
		return
	}
	buf, err := json.Marshal(Frame{
		Type:       FrameCanvasChanged,
		ProjectID:  projectID,
		Version:    &version,
		SourceMark: sourceMark,
		Op:         op,
	})
	if err != nil {
		return
	}
	h.mu.RLock()
	all := append([]*hubConn(nil), h.conns[userID]...)
	h.mu.RUnlock()
	for _, c := range all {
		if c.projectID != projectID {
			continue
		}
		// 超时的连接只跳过，不在这里摘表：Hub 没有清死连接的机制，注销一直是
		// WS handler 的 defer Unregister 负责（连接的生命周期归它管）。而且
		// coder/websocket 在写超时后会把这条连接置为失败态，读循环随即退出、
		// defer 自然摘表——重复一套清理反而会和它抢。
		_ = writeFrameWithTimeout(c.ctx, c.ws, buf, canvasNotifyWriteTimeout)
	}
}

// writeFrameWithTimeout 给一次写套上超时上限：调用方是同步请求路径，绝不能被
// 单个不读的客户端无限期挂住。
func writeFrameWithTimeout(ctx context.Context, ws frameWriter, buf []byte, timeout time.Duration) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, buf)
}

// UserSink 是一个按 (user, project) 维度的 FrameSink——agent loop 跟 WS 层
// 对话的主要方式。
type UserSink struct {
	Hub       *Hub
	UserID    int64
	ProjectID int64
}

func (s UserSink) Send(f Frame) { s.Hub.SendToProject(s.UserID, s.ProjectID, f) }

// RegisterRun 记下一个进行中 Run 的 cancel，让后续（可能来自另一条连接的）abort
// 帧能停掉它。调用方必须在 Run 结束时配对调用 UnregisterRun，否则 map 会泄漏。
//
// 同 key 重复注册时先 cancel 掉旧 Run 再覆盖。正常前端每次发送都生成新
// actionID，撞 key 只可能来自自造客户端 / 帧重放——本身就是异常。此时表里
// 只能存一个 cancel，旧 Run 一旦被覆盖就永远失去 abort 手段，会带着 60min
// 预算失控烧完；两个同 key Run 并存没有任何正当场景，掐掉旧的比让它失控更安全。
func (h *Hub) RegisterRun(userID int64, threadID, actionID string, cancel context.CancelFunc) {
	key := runKey(userID, threadID, actionID)
	var oldCancel context.CancelFunc
	h.mu.Lock()
	if e := h.runs[key]; e != nil {
		oldCancel = e.cancel
		e.cancel = cancel
		e.refs++
	} else {
		h.runs[key] = &runEntry{cancel: cancel, refs: 1}
	}
	h.mu.Unlock()
	// 锁外调用，与 CancelRun 保持同一纪律：不在持锁状态下执行外部回调。
	if oldCancel != nil {
		oldCancel()
	}
}

// UnregisterRun 归还一次注册。Run 无论成功、失败还是被 cancel，都要走到这里
// ——这是这张表唯一的出口。refs 归零才真正删条目：被同 key 新注册覆盖过的
// 旧 Run，其结束时的这次调用只减计数，不会误删新 Run 的 cancel（WHY 见
// runEntry 的注释）。
func (h *Hub) UnregisterRun(userID int64, threadID, actionID string) {
	key := runKey(userID, threadID, actionID)
	h.mu.Lock()
	if e := h.runs[key]; e != nil {
		e.refs--
		if e.refs <= 0 {
			delete(h.runs, key)
		}
	}
	h.mu.Unlock()
}

// CancelRun 取消该用户名下匹配的进行中 Run，返回是否真的找到了一个。
// 找不到时返回 false——调用方据此可以判断这次 abort 是否落空（Run 已自然结束，
// 或者 key 根本不属于这个用户）。
//
// key 带 userID 是必需的鉴权维度：abort 帧不查 DB，如果只按 (thread,action) 匹配，
// 任何登录用户拿到别人的 threadID 就能掐断别人正在跑的生成。
func (h *Hub) CancelRun(userID int64, threadID, actionID string) bool {
	h.mu.Lock()
	var cancel context.CancelFunc
	if e := h.runs[runKey(userID, threadID, actionID)]; e != nil {
		cancel = e.cancel
	}
	h.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func runKey(userID int64, threadID, actionID string) string {
	return strconv.FormatInt(userID, 10) + "|" + threadID + "|" + actionID
}
