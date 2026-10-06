package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestHub_SendDeliversFrame(t *testing.T) {
	hub := NewHub()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		// 注册并保持连接打开，直到测试关掉它。
		hub.Register(r.Context(), 1, 0, c)
		<-r.Context().Done()
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// 在我们发送前，给服务端 handler 一点时间完成注册。
	time.Sleep(100 * time.Millisecond)

	hub.SendToUser(1, Frame{Type: FrameThreadStart, ThreadID: "thr_x"})

	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Type != FrameThreadStart || f.ThreadID != "thr_x" {
		t.Fatalf("got %+v", f)
	}
}

func TestHub_SendToProjectOnlyDeliversMatchingProject(t *testing.T) {
	hub := NewHub()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		projectID := int64(10)
		if strings.Contains(r.URL.RawQuery, "project=20") {
			projectID = 20
		}
		hub.Register(r.Context(), 1, projectID, c)
		<-r.Context().Done()
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c10, _, err := websocket.Dial(ctx, wsURL+"?project=10", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c10.Close(websocket.StatusNormalClosure, "")
	c20, _, err := websocket.Dial(ctx, wsURL+"?project=20", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c20.Close(websocket.StatusNormalClosure, "")

	time.Sleep(100 * time.Millisecond)

	hub.SendToProject(1, 10, Frame{Type: FrameThreadStart, ThreadID: "thr_project_10"})

	_, data, err := c10.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.ThreadID != "thr_project_10" || f.ProjectID != 10 {
		t.Fatalf("got %+v", f)
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer shortCancel()
	if _, _, err := c20.Read(shortCtx); err == nil {
		t.Fatal("project 20 connection should not receive project 10 frame")
	}
}

// 团队观看的正确性钉子：同一个（影子）userID 下、同一项目的**两条**连接，一次
// NotifyCanvasChanged 必须都收到——团队成员共用影子账号，全员送达靠的就是这条；
// 另一个项目的连接必须收不到（帧不许串项目）。
func TestHub_NotifyCanvasChangedReachesAllProjectConnections(t *testing.T) {
	hub := NewHub()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		projectID := int64(10)
		if strings.Contains(r.URL.RawQuery, "project=20") {
			projectID = 20
		}
		hub.Register(r.Context(), 1, projectID, c)
		<-r.Context().Done()
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dial := func(query string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.Dial(ctx, wsURL+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
		return c
	}
	editor := dial("?project=10")
	viewer := dial("?project=10")
	other := dial("?project=20")

	time.Sleep(100 * time.Millisecond)

	hub.NotifyCanvasChanged(1, 10, 42, "abcd1234", nil)

	for name, c := range map[string]*websocket.Conn{"编辑者": editor, "观看者": viewer} {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("%s 没收到帧: %v", name, err)
		}
		var f Frame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("%s 帧解析失败: %v", name, err)
		}
		if f.Type != FrameCanvasChanged || f.ProjectID != 10 || f.SourceMark != "abcd1234" {
			t.Fatalf("%s 收到 %+v", name, f)
		}
		// version 必须真出现在 JSON 里（哪怕是 0）——观看端的过滤规则读它。
		if f.Version == nil || *f.Version != 42 {
			t.Fatalf("%s 的 version=%v, want 42", name, f.Version)
		}
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer shortCancel()
	if _, _, err := other.Read(shortCtx); err == nil {
		t.Fatal("项目 20 的连接不该收到项目 10 的 canvas_changed")
	}
}

// blockingWriter 模拟「TCP 还活着但对端不读」的连接：写一直挂到 ctx 到期。
// 真 socket 要灌满内核缓冲区才会阻塞，单测里构造不出来，所以按接口注入。
type blockingWriter struct {
	started chan struct{}
	err     error
	once    sync.Once
}

func (b *blockingWriter) Write(ctx context.Context, _ websocket.MessageType, _ []byte) error {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	b.err = ctx.Err()
	return ctx.Err()
}

type recordingWriter struct {
	mu     sync.Mutex
	frames [][]byte
}

func (r *recordingWriter) Write(_ context.Context, _ websocket.MessageType, p []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, append([]byte(nil), p...))
	return nil
}

func (r *recordingWriter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

// 广播的调用方是同步的保存 handler / 回写 worker：一个不读的观看端绝不能把编辑者
// 的请求无限期挂住，也不能挡住后面那条正常连接收帧。
func TestHub_NotifyCanvasChangedSkipsStuckConnection(t *testing.T) {
	prev := canvasNotifyWriteTimeout
	canvasNotifyWriteTimeout = 80 * time.Millisecond
	defer func() { canvasNotifyWriteTimeout = prev }()

	stuck := &blockingWriter{started: make(chan struct{})}
	healthy := &recordingWriter{}
	hub := NewHub()
	ctx := context.Background()
	hub.conns[1] = []*hubConn{
		{ws: stuck, userID: 1, projectID: 10, ctx: ctx},
		{ws: healthy, userID: 1, projectID: 10, ctx: ctx},
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		hub.NotifyCanvasChanged(1, 10, 3, "mark1234", nil)
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		// 卡住的连接最多吃掉一个超时；绝不能永久挂住。
		if elapsed > 2*time.Second {
			t.Fatalf("广播耗时 %s，超时没生效", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("广播被卡住的连接永久挂住了")
	}

	if healthy.count() != 1 {
		t.Fatalf("正常连接收到 %d 帧, want 1（不该被卡住的那条挡掉）", healthy.count())
	}
	if !errors.Is(stuck.err, context.DeadlineExceeded) {
		t.Fatalf("卡住的连接写错误 = %v, want DeadlineExceeded", stuck.err)
	}
}

// agent 自己的帧路径不受影响：SendToProject 仍用连接自身的 ctx，不套写超时。
func TestHub_AgentFramesKeepConnectionContext(t *testing.T) {
	healthy := &recordingWriter{}
	hub := NewHub()
	hub.conns[1] = []*hubConn{{ws: healthy, userID: 1, projectID: 10, ctx: context.Background()}}

	hub.SendToProject(1, 10, Frame{Type: FrameThreadStart, ThreadID: "thr_x"})
	if healthy.count() != 1 {
		t.Fatalf("agent 帧收到 %d, want 1", healthy.count())
	}
}

// version=0（快照行刚建/回写 no-op）不许从 JSON 里消失；反过来 agent 自己的帧
// 一个 version 字段都不该多出来。
func TestHub_CanvasChangedKeepsZeroVersionInJSON(t *testing.T) {
	healthy := &recordingWriter{}
	hub := NewHub()
	hub.conns[1] = []*hubConn{{ws: healthy, userID: 1, projectID: 10, ctx: context.Background()}}

	hub.NotifyCanvasChanged(1, 10, 0, "server", nil)
	hub.SendToProject(1, 10, Frame{Type: FrameThreadStart, ThreadID: "thr_x"})

	healthy.mu.Lock()
	defer healthy.mu.Unlock()
	if len(healthy.frames) != 2 {
		t.Fatalf("收到 %d 帧, want 2", len(healthy.frames))
	}
	if !strings.Contains(string(healthy.frames[0]), `"version":0`) {
		t.Fatalf("canvas_changed 丢了 version=0: %s", healthy.frames[0])
	}
	if strings.Contains(string(healthy.frames[1]), `"version"`) {
		t.Fatalf("agent 帧不该带 version: %s", healthy.frames[1])
	}
}

func TestHub_NotifyCanvasChangedIgnoresInvalidKeys(t *testing.T) {
	hub := NewHub()
	// 只要不 panic 且不退化成 SendToUser 广播即可：没有连接注册，纯粹钉住入参守卫。
	hub.NotifyCanvasChanged(0, 10, 1, "mark", nil)
	hub.NotifyCanvasChanged(1, 0, 1, "mark", nil)
}

func TestHub_CancelRunInvokesRegisteredCancel(t *testing.T) {
	hub := NewHub()
	canceled := false
	hub.RegisterRun(1, "thr_1", "act_1", func() { canceled = true })

	if !hub.CancelRun(1, "thr_1", "act_1") {
		t.Fatal("CancelRun 应当找到已注册的 Run")
	}
	if !canceled {
		t.Fatal("cancel 函数没被调用")
	}

	// UnregisterRun 之后同一个 key 必须落空——这是这张表唯一的出口，
	// 漏掉它就是进程存活期间只增不减的内存泄漏。
	hub.UnregisterRun(1, "thr_1", "act_1")
	if hub.CancelRun(1, "thr_1", "act_1") {
		t.Fatal("UnregisterRun 之后不该还能找到")
	}
}

// abort 帧不查 DB，key 里必须带 userID：否则任何登录用户拿到别人的 threadID
// 就能掐断别人正在跑的生成。
func TestHub_CancelRunIgnoresOtherUsersKey(t *testing.T) {
	hub := NewHub()
	canceled := false
	hub.RegisterRun(1, "thr_1", "act_1", func() { canceled = true })

	if hub.CancelRun(2, "thr_1", "act_1") {
		t.Fatal("用户 2 不该能 cancel 用户 1 的 Run")
	}
	if canceled {
		t.Fatal("用户 1 的 Run 被别人 cancel 了")
	}
}

// 同 key 重复 RegisterRun（正常前端每次发送都造新 actionID，只有自造客户端 /
// 帧重放会撞 key）必须先掐掉旧 Run：表里同一个 key 只能存一个 cancel，旧 Run
// 一旦被覆盖就永远不可 abort，会带着 60min 预算失控跑完。
func TestHub_DuplicateRegisterCancelsOldRun(t *testing.T) {
	hub := NewHub()
	oldCanceled, newCanceled := false, false
	hub.RegisterRun(1, "thr_1", "act_1", func() { oldCanceled = true })
	hub.RegisterRun(1, "thr_1", "act_1", func() { newCanceled = true })

	if !oldCanceled {
		t.Fatal("同 key 二次注册必须先 cancel 掉旧 Run")
	}
	if newCanceled {
		t.Fatal("新 Run 不该在注册时就被 cancel")
	}
	// 覆盖后 abort 落到的是新 Run 的 cancel。
	if !hub.CancelRun(1, "thr_1", "act_1") {
		t.Fatal("覆盖注册后 CancelRun 应当仍能找到条目")
	}
	if !newCanceled {
		t.Fatal("CancelRun 应当调用的是新 Run 的 cancel")
	}
}

// 被覆盖的旧 Run 结束时会走自己的 defer UnregisterRun——同一个 key。这次
// Unregister 不能把新 Run 的条目删掉，否则新 Run 也变成不可 abort。
func TestHub_StaleUnregisterDoesNotEvictNewRun(t *testing.T) {
	hub := NewHub()
	newCanceled := false
	hub.RegisterRun(1, "thr_1", "act_1", func() {})
	hub.RegisterRun(1, "thr_1", "act_1", func() { newCanceled = true })

	// 模拟旧 Run 结束时的 defer UnregisterRun。
	hub.UnregisterRun(1, "thr_1", "act_1")

	if !hub.CancelRun(1, "thr_1", "act_1") {
		t.Fatal("旧 Run 的 Unregister 误删了新 Run 的条目")
	}
	if !newCanceled {
		t.Fatal("新 Run 的 cancel 没被调用")
	}

	// 新 Run 自己的 Unregister 才是最后一次配对调用，之后条目必须真正消失
	// ——否则 map 泄漏。
	hub.UnregisterRun(1, "thr_1", "act_1")
	if hub.CancelRun(1, "thr_1", "act_1") {
		t.Fatal("配对的 Unregister 都到齐后条目应当被删除")
	}
}

func TestUserSinkSends(t *testing.T) {
	// 冒烟测试：没有注册任何连接时，UserSink 调用 SendToUser 不应 panic。
	hub := NewHub()
	sink := UserSink{Hub: hub, UserID: 42}
	sink.Send(Frame{Type: FrameThreadStart, ThreadID: "thr_y"})
}
