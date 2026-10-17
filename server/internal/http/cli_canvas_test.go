package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// fakeProjectStore 是 cliapi.ProjectStore 的内存实现。
// 快照按项目存一份 payload + version，MutateSnapshot 语义与 MySQL 版一致：
// 有改动才 version+1。
type fakeProjectStore struct {
	projects  map[int64]*projects.Project
	snapshots map[int64]*projects.Snapshot
	nextID    int64
	// lastListLimit 记下 service 传进来的 limit，供「CLI 必须拉全量」那条断言用。
	lastListLimit int
}

func newFakeProjectStore() *fakeProjectStore {
	return &fakeProjectStore{
		projects:  map[int64]*projects.Project{},
		snapshots: map[int64]*projects.Snapshot{},
	}
}

func (s *fakeProjectStore) add(userID int64, name string, payload string) int64 {
	s.nextID++
	id := s.nextID
	s.projects[id] = &projects.Project{
		ID: id, UserID: userID, Name: name,
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0),
	}
	if payload != "" {
		s.snapshots[id] = &projects.Snapshot{Payload: json.RawMessage(payload), Version: 17}
	}
	return id
}

func (s *fakeProjectStore) ListForUser(userID int64, limit, offset int) ([]projects.Project, int, error) {
	s.lastListLimit = limit
	out := []projects.Project{}
	for _, p := range s.projects {
		if p.UserID == userID {
			out = append(out, *p)
		}
	}
	return out, len(out), nil
}

func (s *fakeProjectStore) GetForUser(userID, projectID int64) (*projects.Project, error) {
	p, ok := s.projects[projectID]
	if !ok || p.UserID != userID {
		return nil, projects.ErrNotFound
	}
	return p, nil
}

func (s *fakeProjectStore) Create(userID int64, name, description string) (int64, error) {
	s.nextID++
	id := s.nextID
	s.projects[id] = &projects.Project{ID: id, UserID: userID, Name: name, Description: description}
	return id, nil
}

func (s *fakeProjectStore) Update(userID, projectID int64, name, description string) error {
	p, ok := s.projects[projectID]
	if !ok || p.UserID != userID {
		return projects.ErrNotFound
	}
	p.Name, p.Description = name, description
	return nil
}

func (s *fakeProjectStore) Delete(userID, projectID int64) error {
	p, ok := s.projects[projectID]
	if !ok || p.UserID != userID {
		return projects.ErrNotFound
	}
	delete(s.projects, projectID)
	return nil
}

func (s *fakeProjectStore) GetLatest(projectID int64) (*projects.Snapshot, error) {
	snap, ok := s.snapshots[projectID]
	if !ok {
		return nil, projects.ErrNotFound
	}
	return snap, nil
}

func (s *fakeProjectStore) MutateSnapshot(projectID int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	snap, ok := s.snapshots[projectID]
	if !ok {
		snap = &projects.Snapshot{Payload: json.RawMessage(`{"nodes":[],"connections":[]}`), Version: 0}
		s.snapshots[projectID] = snap
	}
	next, changed, err := fn(snap.Payload)
	if err != nil {
		return 0, err
	}
	if changed {
		snap.Payload = next
		snap.Version++
	}
	return snap.Version, nil
}

// 一张含 3 类节点 + 2 条连线的画布：可写生成节点、只读 preview、上游输入。
const cliTestCanvasPayload = `{
  "nodes": [
    {"id":"n-1","type":"input-image","x":0,"y":0,"width":260,"height":260,"content":"http://obj/1.png","status":"succeeded"},
    {"id":"n-2","type":"gen-image","x":100,"y":0,"width":280,"height":280,"status":"idle",
     "settings":{"modelId":"doubao-image","prompt":"雨后城市","ratio":"16:9","history":["http://obj/old.png"]}},
    {"id":"n-3","type":"preview","x":200,"y":0,"width":320,"height":260,"content":"http://obj/v.mp4",
     "settings":{"kind":"video","agentMeta":{"actionId":"a1"}}}
  ],
  "connections": [
    {"id":"c-1","from":"n-1","to":"n-2"},
    {"id":"c-2","from":"n-3","to":"n-2"}
  ],
  "view": {"x":0,"y":0,"zoom":1}
}`

// cliTestEnv 组装一套完整路由 + 一个已登录 CLI 用户。
type cliTestEnv struct {
	router http.Handler
	store  *fakeProjectStore
	token  string
	userID int64
}

func newCLITestEnv(t *testing.T) *cliTestEnv {
	t.Helper()
	store := newFakeProjectStore()

	// 签名器用可辨识的后缀，测试能断言「确实签过」。
	sign := func(s string) string { return s + "?sig=1" }

	router := NewRouter(Deps{
		Auth: cliTestAuth(),
		CLI:  NewCLIHandler(cliapi.NewService(store, sign), nil),
	})
	tok := testCLIToken
	return &cliTestEnv{router: router, store: store, token: tok, userID: LocalUserID}
}

func (e *cliTestEnv) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func TestCLICanvasGetNormalizesEverything(t *testing.T) {
	env := newCLITestEnv(t)
	pid := env.store.add(env.userID, "画布 A", cliTestCanvasPayload)

	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects/"+itoa(pid)+"/canvas")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var c cliapi.Canvas
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}

	if c.Version != 17 {
		t.Fatalf("version = %d, want 17", c.Version)
	}
	if len(c.Nodes) != 3 || len(c.Edges) != 2 {
		t.Fatalf("nodes=%d edges=%d", len(c.Nodes), len(c.Edges))
	}

	byID := map[string]cliapi.Node{}
	for _, n := range c.Nodes {
		byID[n.ID] = n
	}
	if got := byID["n-2"]; got.Type != "image-generator" || !got.Writable {
		t.Fatalf("gen-image 应映射成可写的 image-generator: %+v", got)
	}
	// preview 是节点数第一名，但 CLI 只能读它、拿它当上游，不能创建/修改。
	if got := byID["n-3"]; got.Type != "preview" || got.Writable {
		t.Fatalf("preview 必须只读: %+v", got)
	}
	if got := byID["n-3"]; got.Media == nil || got.Media.Kind != "video" {
		t.Fatalf("preview 的 kind 必须暴露出来——它是判断媒体类型的唯一依据: %+v", got.Media)
	}
	// 媒体 URL 必须签名：CLI 永远拿不到裸对象存储地址。
	if got := byID["n-1"]; got.Content != "http://obj/1.png?sig=1" {
		t.Fatalf("content 未签名: %s", got.Content)
	}
	if got := byID["n-2"]; len(got.History) != 1 || got.History[0].URL != "http://obj/old.png?sig=1" {
		t.Fatalf("history 未归一化/未签名: %+v", got.History)
	}
	// 连线顺序即输入顺序，不得重排。
	if c.Edges[0].ID != "c-1" || c.Edges[1].ID != "c-2" {
		t.Fatalf("edge 顺序被改: %+v", c.Edges)
	}
}

func TestCLINodeListFiltersBySemanticType(t *testing.T) {
	env := newCLITestEnv(t)
	pid := env.store.add(env.userID, "画布 A", cliTestCanvasPayload)

	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects/"+itoa(pid)+"/nodes?type=image-generator")
	var got struct {
		Data []cliapi.Node `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].ID != "n-2" {
		t.Fatalf("filter 结果 = %+v", got.Data)
	}

	// 内部类型名不能当过滤值用——契约只认一套命名。
	rec = env.do(t, http.MethodGet, "/api/v1/cli/projects/"+itoa(pid)+"/nodes?type=gen-image")
	got.Data = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Data) != 0 {
		t.Fatalf("内部类型不该匹配: %+v", got.Data)
	}
}

func TestCLINodeGetAndResolve(t *testing.T) {
	env := newCLITestEnv(t)
	pid := env.store.add(env.userID, "画布 A", cliTestCanvasPayload)
	base := "/api/v1/cli/projects/" + itoa(pid)

	if rec := env.do(t, http.MethodGet, base+"/nodes/n-2"); rec.Code != http.StatusOK {
		t.Fatalf("get node status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec := env.do(t, http.MethodGet, base+"/nodes/n-nope")
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeNodeNotFound {
		t.Fatalf("缺失节点 = %d %s", rec.Code, rec.Body.String())
	}

	rec = env.do(t, http.MethodGet, base+"/nodes/resolve?ref=n-2")
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		NodeID string `json:"nodeId"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.NodeID != "n-2" {
		t.Fatalf("nodeId = %q", res.NodeID)
	}
	// 第一阶段只解析 ID：不引入节点 name，因为快照结构不能改。
	rec = env.do(t, http.MethodGet, base+"/nodes/resolve?ref=城市夜景")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("按名称解析应 404（第一阶段无名称），got %d", rec.Code)
	}
}

// 「不存在」与「别人的」必须返回同一个 PROJECT_NOT_FOUND，否则项目 id 可枚举。
func TestCLICrossUserProjectIsIndistinguishableFromMissing(t *testing.T) {
	env := newCLITestEnv(t)
	othersID := env.store.add(env.userID+999, "别人的画布", cliTestCanvasPayload)

	for _, path := range []string{
		"/api/v1/cli/projects/" + itoa(othersID),
		"/api/v1/cli/projects/" + itoa(othersID) + "/canvas",
		"/api/v1/cli/projects/" + itoa(othersID) + "/nodes",
		"/api/v1/cli/projects/" + itoa(othersID) + "/edges",
		"/api/v1/cli/projects/99999",
		"/api/v1/cli/projects/99999/canvas",
	} {
		rec := env.do(t, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, rec.Code)
			continue
		}
		if got := decodeCLIError(t, rec).Code; got != cliapi.CodeProjectNotFound {
			t.Errorf("%s code = %s", path, got)
		}
	}
}

// 新项目还没保存过画布：回空画布而不是 404 或 500。
func TestCLICanvasOfProjectWithoutSnapshot(t *testing.T) {
	env := newCLITestEnv(t)
	pid := env.store.add(env.userID, "空画布", "")

	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects/"+itoa(pid)+"/canvas")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	// nodes/edges 必须是 []，不能是 null——Agent 直接 .length 会炸。
	body := rec.Body.String()
	if !containsAll(body, `"nodes":[]`, `"edges":[]`) {
		t.Fatalf("空画布应回空数组而非 null: %s", body)
	}
}

func TestCLIProjectListOnlyOwn(t *testing.T) {
	env := newCLITestEnv(t)
	env.store.add(env.userID, "我的", "")
	env.store.add(env.userID+999, "别人的", "")

	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects")
	var got struct {
		Data []cliapi.Project `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Data) != 1 || got.Data[0].Name != "我的" {
		t.Fatalf("projects = %+v", got.Data)
	}
}

// itoa 与 generation_routes_test.go 的同名辅助一致，复用那一份。

// containsAll 断言 body 里同时出现全部片段。
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// CLI 的项目列表必须**拉全量**。网页列表页有 limit/offset 分页（无限
// 滚动），而 ListForUser 这个方法是网页 / CLI / 首页三方共用的——分页参数一旦漏进
// CLI 这条路，`tvmao project list` 就会悄悄少东西，而且少得毫无提示。这条断言就是
// 钉住「service 层恒传 limit=0」，别让以后有人图省事把默认页大小塞进来。
func TestCLIListProjectsPullsAllWithoutPagination(t *testing.T) {
	env := newCLITestEnv(t)
	for i := 0; i < 3; i++ {
		env.store.Create(env.userID, "p", "")
	}

	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if env.store.lastListLimit != 0 {
		t.Fatalf("CLI 传了 limit=%d —— 必须传 0 拉全量，否则项目多了会悄悄少东西",
			env.store.lastListLimit)
	}
}
