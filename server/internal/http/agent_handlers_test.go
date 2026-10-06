package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// capturingRunner 记下 Run 收到的 userText，让测试断言「消息到底有没有触达 runner」。
type capturingRunner struct{ called chan string }

func (r *capturingRunner) Run(_ context.Context, _, _ int64, _, _, userText string, _ []agent.Mention, _ string, _ agent.FrameSink) error {
	r.called <- userText
	return nil
}

// stubThreadRepo 让 GetThread 永远成功；WS handler 只需要这一个方法，其余满足接口即可。
type stubThreadRepo struct{}

func (stubThreadRepo) CreateThread(agent.Thread) error         { return nil }
func (stubThreadRepo) LoadedSkillIDs(string) ([]string, error) { return nil, nil }
func (stubThreadRepo) GetThread(id string) (*agent.Thread, error) {
	return &agent.Thread{ID: id, ProjectID: 11, UserID: 1}, nil
}
func (stubThreadRepo) ListThreadsByProject(int64) ([]agent.Thread, error) { return nil, nil }
func (stubThreadRepo) UpdateThreadStatus(string, string) error            { return nil }
func (stubThreadRepo) UpdateThreadTitle(string, string) error             { return nil }
func (stubThreadRepo) DeleteThread(string) error                          { return nil }
func (stubThreadRepo) UpdateThreadSummary(string, string) error           { return nil }
func (stubThreadRepo) AppendMessage(agent.Message) error                  { return nil }
func (stubThreadRepo) ListMessages(string) ([]agent.Message, error)       { return nil, nil }
func (stubThreadRepo) ListMessagesPageBefore(string, string, int) ([]agent.Message, bool, error) {
	return nil, false, nil
}
func (stubThreadRepo) MarkMessagesSummarized([]string) error { return nil }

// blockingRunner 的 Run 阻塞到 ctx 被取消，用来验证 abort 帧能真正 cancel
// 正在进行的 Run（而不只是设个没人读的标志）。
type blockingRunner struct {
	started  chan struct{}
	canceled chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, _, _ int64, _, _, _ string, _ []agent.Mention, _ string, _ agent.FrameSink) error {
	close(r.started)
	<-ctx.Done()
	close(r.canceled)
	return ctx.Err()
}

// 点「停止」时前端发 abort 帧；后端必须 cancel 对应 (thread,action) 的 Run ctx，
// 让正在等 LLM / 轮询的 goroutine 立刻收手——否则按钮点了也停不下来。
func TestAgentWS_AbortCancelsRun(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), canceled: make(chan struct{})}
	h := &AgentHandler{Hub: agent.NewHub(), Repo: stubThreadRepo{}, Runner: runner}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	send := func(f agent.Frame) {
		buf, _ := json.Marshal(f)
		if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	send(agent.Frame{Type: agent.FrameUserMessage, ThreadID: "thr_1", ActionID: "act_1", Content: "跑起来"})

	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner 没启动")
	}

	send(agent.Frame{Type: agent.FrameAbort, ThreadID: "thr_1", ActionID: "act_1"})

	select {
	case <-runner.canceled:
	case <-ctx.Done():
		t.Fatal("abort 帧没能 cancel 正在进行的 Run ctx")
	}
}

// sidecar 退出（BaseCtx 取消）时，进行中的 Run 必须跟着停下，不能挂在 Background 上。
func TestAgentWS_BaseCtxCancelStopsRun(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), canceled: make(chan struct{})}
	base, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	h := &AgentHandler{Hub: agent.NewHub(), Repo: stubThreadRepo{}, Runner: runner, BaseCtx: base}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	buf, _ := json.Marshal(agent.Frame{Type: agent.FrameUserMessage, ThreadID: "thr_1", ActionID: "act_1", Content: "跑起来"})
	if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner 没启动")
	}
	baseCancel()
	select {
	case <-runner.canceled:
	case <-ctx.Done():
		t.Fatal("BaseCtx 取消后 Run 没停")
	}
}

// otherUserThreadRepo 的 thread 归 user 2 所有，用来验证 user 1 打不进去。
type otherUserThreadRepo struct{ stubThreadRepo }

func (otherUserThreadRepo) GetThread(id string) (*agent.Thread, error) {
	return &agent.Thread{ID: id, ProjectID: 11, UserID: 2}, nil
}

// 越权防线：thread 不属于调用者时，user_message 绝不能触达 Runner。否则
// buildHistory 会把别人 thread 的全部历史喂给 planner，帧再按发起者的 uid 广播
// 回攻击者自己的面板——等于把别人的对话念出来，还往受害者 thread 里写了消息。
func TestAgentWS_RejectsOtherUsersThread(t *testing.T) {
	runner := &capturingRunner{called: make(chan string, 1)}
	h := &AgentHandler{
		Hub:    agent.NewHub(),
		Repo:   otherUserThreadRepo{},
		Runner: runner,
	}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()

	// uid=1 的凭据，去捅 uid=2 的 thread。
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	buf, _ := json.Marshal(agent.Frame{
		Type: agent.FrameUserMessage, ThreadID: "thr_victim", ActionID: "act_1", Content: "偷看",
	})
	if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 必须收到 error 帧，且 Runner 一次都没被调用。
	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	_, data, err := c.Read(readCtx)
	if err != nil {
		t.Fatalf("期望一个 error 帧，却读失败: %v", err)
	}
	var got agent.Frame
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != agent.FrameError {
		t.Fatalf("期望 error 帧，得到 %+v", got)
	}

	select {
	case text := <-runner.called:
		t.Fatalf("Runner 不该被调用，却收到了 %q", text)
	case <-time.After(200 * time.Millisecond):
	}
}

// 线上 WS 会被反向代理 idle-timeout 掐断、客户端自动重连出一条新连接，而 Run
// 跑在 context.Background() 上仍然活着。此时点「停止」，abort 帧从**新**连接进来
// 也必须能停掉它——注册表跟着连接走的话，用户只能眼看它把 60min 预算烧完。
func TestAgentWS_AbortFromReconnectedConnectionCancelsRun(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}), canceled: make(chan struct{})}
	h := &AgentHandler{Hub: agent.NewHub(), Repo: stubThreadRepo{}, Runner: runner}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	send := func(c *websocket.Conn, f agent.Frame) {
		buf, _ := json.Marshal(f)
		if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// 第一条连接发起 Run，然后模拟被代理掐断。
	c1, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	send(c1, agent.Frame{Type: agent.FrameUserMessage, ThreadID: "thr_1", ActionID: "act_1", Content: "跑起来"})
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatal("runner 没启动")
	}
	_ = c1.Close(websocket.StatusGoingAway, "proxy idle timeout")

	// 客户端重连——一条全新的连接，服务端也是一个全新的 handler goroutine。
	c2, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close(websocket.StatusNormalClosure, "")
	send(c2, agent.Frame{Type: agent.FrameAbort, ThreadID: "thr_1", ActionID: "act_1"})

	select {
	case <-runner.canceled:
	case <-ctx.Done():
		t.Fatal("重连后的 abort 帧没能 cancel 掉仍在进行的 Run")
	}
}

// 一篇小说远超 coder/websocket 默认的 32KB 读上限。回归保护：发大消息时，
// WS handler 必须把它读全并触达 Runner，而不是因为超限关连接、让对话一直转圈。
func TestAgentWS_LargeMessageReachesRunner(t *testing.T) {
	runner := &capturingRunner{called: make(chan string, 1)}
	h := &AgentHandler{
		Hub:    agent.NewHub(),
		Repo:   stubThreadRepo{},
		Runner: runner,
	}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// 约 195KB 的 UTF-8 中文正文——模拟用户粘进来的一段小说。
	novel := strings.Repeat("这是一段被洗稿的小说正文。", 5000)
	buf, _ := json.Marshal(agent.Frame{Type: agent.FrameUserMessage, ThreadID: "thr_1", Content: novel})
	if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case got := <-runner.called:
		if len(got) != len(novel) {
			t.Fatalf("runner 收到 %d 字节，期望 %d", len(got), len(novel))
		}
	case <-ctx.Done():
		t.Fatal("runner 从未被调用——大消息被丢弃（read limit 超限？）")
	}
}

// 桌面端没有多人协作（X2）：canvas_transient 帧被静默丢弃，同项目的其他连接收不到任何东西。
func TestAgentWS_IgnoresCanvasTransient(t *testing.T) {
	hub := agent.NewHub()
	h := &AgentHandler{Hub: hub, Repo: stubThreadRepo{}, Runner: &capturingRunner{called: make(chan string, 1)}}
	srv := httptest.NewServer(http.HandlerFunc(h.WS))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "?project_id=11"
	a, _, err := websocket.Dial(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(websocket.StatusNormalClosure, "")
	b, _, err := websocket.Dial(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(websocket.StatusNormalClosure, "")
	if err := a.Write(ctx, websocket.MessageText, []byte(`{"type":"canvas_transient","project_id":11,"kind":"drag"}`)); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer rcancel()
	if _, data, err := b.Read(rctx); err == nil {
		t.Fatalf("不应转播瞬态帧，收到 %s", data)
	}
}

func TestAgentListThreads_LocalUserWithoutContext(t *testing.T) {
	repo := &listRepo{threads: []agent.Thread{{ID: "t1", ProjectID: 5, UserID: 1}}}
	h := &AgentHandler{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(withProjectID(req.Context(), 5))
	rec := httptest.NewRecorder()
	h.ListThreads(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"t1"`) {
		t.Fatalf("没有身份中间件时也应以本地用户 1 列出线程: %d %s", rec.Code, rec.Body.String())
	}
}

type listRepo struct {
	stubThreadRepo
	threads []agent.Thread
}

func (r *listRepo) ListThreadsByProject(int64) ([]agent.Thread, error) { return r.threads, nil }

// 关闭：Done 之后拒绝新 Run，并等已登记的 Run 退出后才关闭通道（关库前不再有 Agent 写入）。
func TestAgentHandlerDoneWaitsForRunsAndRejectsNew(t *testing.T) {
	h := &AgentHandler{}
	if !h.startRun() {
		t.Fatal("关闭前应能登记 Run")
	}
	done := h.Done()
	if h.startRun() {
		t.Fatal("Done 之后不应再接受新 Run")
	}
	select {
	case <-done:
		t.Fatal("Run 未退出时 Done 不应关闭")
	case <-time.After(50 * time.Millisecond):
	}
	h.runs.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run 退出后 Done 应关闭")
	}
}
