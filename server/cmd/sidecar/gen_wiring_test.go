package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasstate"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
)

// memGenStorage：内存版本地存储（生产是磁盘实现）。
type memGenStorage struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (m *memGenStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	r, _ := in.Content()
	b, err := io.ReadAll(r)
	if err != nil {
		return assets.StoredObject{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objs == nil {
		m.objs = map[string][]byte{}
	}
	m.objs[in.Key] = b
	return assets.StoredObject{URL: "media://" + in.Key, Size: int64(len(b))}, nil
}
func (m *memGenStorage) DeleteObject(context.Context, string) error { return nil }
func (m *memGenStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://127.0.0.1:1/media/" + key, nil
}
func (m *memGenStorage) NewKey(_ int64, name string) string { return "u1/2026/10/" + name }
func (m *memGenStorage) StaticKey(_ int64, name string) string {
	return "u1/" + strings.ReplaceAll(name, "/", "_")
}
func (m *memGenStorage) ExtractKey(u string) (string, bool) {
	if strings.HasPrefix(u, "media://") {
		return strings.TrimPrefix(u, "media://"), true
	}
	return "", false
}
func (m *memGenStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(strings.NewReader(string(m.objs[key]))), nil
}

type countingNotifier struct{ n atomic.Int32 }

func (c *countingNotifier) NotifyCanvasChanged(int64, int64, int64, string, *canvasstate.CanvasOpSummary) {
	c.n.Add(1)
}

// fakeUpstream：同步出图模型 POST /v1/images/generations；异步视频模型 GET /v1/tasks/up-1。
type fakeUpstream struct {
	srv        *httptest.Server
	imagePosts atomic.Int32
	videoPosts atomic.Int32
	videoPolls atomic.Int32
	authMu     sync.Mutex
	auths      []string // 打到本上游的生成/轮询请求带的 Authorization（下载产物不计）
}

func (f *fakeUpstream) recordAuth(r *http.Request) {
	f.authMu.Lock()
	defer f.authMu.Unlock()
	f.auths = append(f.auths, r.Header.Get("Authorization"))
}

func (f *fakeUpstream) authHeaders() []string {
	f.authMu.Lock()
	defer f.authMu.Unlock()
	return append([]string(nil), f.auths...)
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/generations":
			f.recordAuth(r)
			f.imagePosts.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"url":"`+f.srv.URL+`/out.png"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video/generations":
			f.recordAuth(r)
			f.videoPosts.Add(1)
			_, _ = io.WriteString(w, `{"task_id":"up-new"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/up-1":
			f.recordAuth(r)
			f.videoPolls.Add(1)
			_, _ = io.WriteString(w, `{"status":"completed","video_url":"`+f.srv.URL+`/out.mp4"}`)
		case r.URL.Path == "/out.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nIMG"))
		case r.URL.Path == "/out.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4DATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type harness struct {
	w        *genWiring
	projects *projects.SQLiteRepository
	pid      int64
	up       *fakeUpstream
	notifier *countingNotifier
	exec     func(q string, args ...any)
	query    func(q string, args ...any) string
}

func newHarness(t *testing.T, nodeIDs ...string) *harness {
	t.Helper()
	conn := dbtest.Open(t)
	up := newFakeUpstream(t)
	st := settings.NewSQLiteRepository(conn)
	_ = st.Set("base_domain", up.srv.URL)
	_ = st.Set("upstream_api_key", "sk-test")
	mr := models.NewSQLiteRepository(conn)
	if _, err := mr.Create(models.ModelConfig{Type: "image", Name: "img", ModelID: "img-test", BasePath: "/v1/images/generations",
		Enabled: true, RequestTemplate: json.RawMessage(`{"model":"img-test","prompt":"{{prompt}}"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.Create(models.ModelConfig{Type: "video", Name: "vid", ModelID: "vid-test", BasePath: "/v1/video/generations",
		Enabled: true, RequestTemplate: json.RawMessage(`{"model":"vid-test","prompt":"{{prompt}}"}`),
		PollingTemplate: json.RawMessage(`{"path":"/v1/tasks/{{task_id}}","interval":1,"statusField":"status","completedValue":"completed","urlField":"video_url"}`)}); err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	pid, err := pr.Create(1, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []any
	for _, id := range nodeIDs {
		nodes = append(nodes, map[string]any{"id": id, "type": "gen-image", "status": "generating", "settings": map[string]any{}})
	}
	snap, _ := json.Marshal(map[string]any{"nodes": nodes, "connections": []any{}})
	if _, err := pr.SaveAuthoritative(pid, snap); err != nil {
		t.Fatal(err)
	}
	notifier := &countingNotifier{}
	w, err := wireGeneration(conn, t.TempDir(), genExternal{Storage: &memGenStorage{}, Notifier: notifier})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		w: w, projects: pr, pid: pid, up: up, notifier: notifier,
		exec: func(q string, args ...any) {
			if _, err := conn.Exec(q, args...); err != nil {
				t.Fatal(err)
			}
		},
		query: func(q string, args ...any) string {
			var s string
			_ = conn.QueryRow(q, args...).Scan(&s)
			return s
		},
	}
}

func (h *harness) node(t *testing.T, id string) map[string]any {
	t.Helper()
	raw, err := h.projects.GetLatestSnapshot(h.pid)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	_ = json.Unmarshal(raw, &doc)
	for _, n := range doc.Nodes {
		if n["id"] == id {
			return n
		}
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

func TestCanvasGenerationEndToEnd(t *testing.T) {
	h := newHarness(t, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.w.Gen.StartBackground(ctx)

	taskID, err := h.w.CanvasGen.Enqueue(ctx, canvasgen.EnqueueInput{UserID: 1, ProjectID: h.pid, NodeID: "n1", ModelID: "img-test",
		Inputs: map[string]any{"prompt": "一只猫"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "节点 n1 回写为 succeeded", func() bool {
		n := h.node(t, "n1")
		return n != nil && n["status"] == "succeeded"
	})
	n := h.node(t, "n1")
	if c, _ := n["content"].(string); !strings.HasPrefix(c, "media://") {
		t.Fatalf("产物应已落本地, content=%v", n["content"])
	}
	if h.query(`SELECT status FROM generation_tasks WHERE id=?`, taskID) != "succeeded" {
		t.Fatal("任务应为 succeeded")
	}
	waitFor(t, "outbox 投递完成", func() bool {
		return h.query(`SELECT state FROM generation_sink_outbox WHERE task_id=?`, taskID) == "done"
	})
	if h.notifier.n.Load() == 0 {
		t.Fatal("回写后应发 canvas_changed")
	}
}

// 上个进程留下两条心跳仍「新鲜」的在途任务——
// n2 已领取但没调过上游（应重新执行恰好一次），n3 已拿到上游任务号（应续轮询、绝不重新提交）。
func TestRestartResumesInFlightTasks(t *testing.T) {
	h := newHarness(t, "n2", "n3")
	sink := func(node string) string {
		return `{"kind":"canvas_node","projectId":` + itoa(h.pid) + `,"nodeId":"` + node + `","userId":1}`
	}
	h.exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, sink, owner_instance, heartbeat_at, lease_version)
		VALUES (1, ?, 'img-test', 'running', '{"modelId":"img-test","inputs":{"prompt":"x"},"note":""}', ?, 'old-proc', CURRENT_TIMESTAMP, 1)`,
		h.pid, sink("n2"))
	h.exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (1, ?, 'vid-test', 'processing', '{"modelId":"vid-test","inputs":{"prompt":"y"},"note":""}', ?, 'up-1', CURRENT_TIMESTAMP, 'old-proc', CURRENT_TIMESTAMP, 1)`,
		h.pid, sink("n3"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	h.w.Gen.StartBackground(ctx)

	waitFor(t, "n2、n3 都回写成功", func() bool {
		a, b := h.node(t, "n2"), h.node(t, "n3")
		return a != nil && b != nil && a["status"] == "succeeded" && b["status"] == "succeeded"
	})
	if time.Since(start) > 20*time.Second {
		t.Fatalf("续跑用了 %s：上个进程的心跳没有被立即判过期", time.Since(start))
	}
	if got := h.up.imagePosts.Load(); got != 1 {
		t.Fatalf("未调过上游的任务应重新执行恰好一次, POST 次数=%d", got)
	}
	if got := h.up.videoPosts.Load(); got != 0 {
		t.Fatalf("已拿到上游任务号的任务绝不能重新提交, POST 次数=%d", got)
	}
	if h.up.videoPolls.Load() == 0 {
		t.Fatal("已拿到上游任务号的任务应续轮询")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
