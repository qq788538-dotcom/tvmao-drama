package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/cliapi"
)

// cliWriteEnv 是带幂等存储的完整环境。
type cliWriteEnv struct {
	router http.Handler
	store  *fakeProjectStore
	token  string
	userID int64
	pid    int64
}

// memIdem 是 cliapi.IdempotencyStore 的内存实现（与 cliapi 包内的那份同语义，
// 这里重写一份是因为那份是 test-only、不导出）。
type memIdem struct {
	rows map[string]*cliapi.IdempotencyRecord
}

func newMemIdem() *memIdem { return &memIdem{rows: map[string]*cliapi.IdempotencyRecord{}} }

func (m *memIdem) k(userID int64, scope, key string) string {
	return itoa(userID) + "|" + scope + "|" + key
}

func (m *memIdem) Begin(userID int64, scope, key, hash string) (*cliapi.IdempotencyRecord, bool, error) {
	kk := m.k(userID, scope, key)
	if rec, ok := m.rows[kk]; ok {
		return rec, false, nil
	}
	rec := &cliapi.IdempotencyRecord{Status: cliapi.IdempotencyInProgress, RequestHash: hash}
	m.rows[kk] = rec
	return rec, true, nil
}

func (m *memIdem) Finish(userID int64, scope, key string, statusCode int, body []byte) error {
	if rec, ok := m.rows[m.k(userID, scope, key)]; ok {
		rec.Status, rec.StatusCode, rec.Body = cliapi.IdempotencyDone, statusCode, body
	}
	return nil
}

func (m *memIdem) Abort(userID int64, scope, key string) error {
	delete(m.rows, m.k(userID, scope, key))
	return nil
}

func newCLIWriteEnv(t *testing.T, payload string) *cliWriteEnv {
	t.Helper()
	store := newFakeProjectStore()
	sign := func(s string) string { return s + "?sig=1" }

	router := NewRouter(Deps{
		Auth: cliTestAuth(),
		CLI:  NewCLIHandler(cliapi.NewService(store, sign), newMemIdem()),
	})
	tok := testCLIToken
	env := &cliWriteEnv{router: router, store: store, token: tok, userID: LocalUserID}
	env.pid = store.add(LocalUserID, "画布 A", payload)
	return env
}

func (e *cliWriteEnv) req(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *cliWriteEnv) base() string { return "/api/v1/cli/projects/" + itoa(e.pid) }

// canvas 直接读快照，验证「落盘的到底是什么」而不是只信响应。
func (e *cliWriteEnv) canvas(t *testing.T) cliapi.Canvas {
	t.Helper()
	snap, err := e.store.GetLatest(e.pid)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cliapi.ParseCanvas(snap.Payload, snap.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ---- 节点写入 ----

func TestCLINodeCreatePersistsModelID(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	rec := env.req(t, http.MethodPost, env.base()+"/nodes", `{
		"type":"image-generator","position":{"x":50,"y":60},
		"params":{"prompt":"雨后城市","modelId":"doubao-image","ratio":"16:9"}}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var n cliapi.Node
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	if n.ID == "" || n.Type != "image-generator" {
		t.Fatalf("node = %+v", n)
	}

	for _, got := range env.canvas(t).Nodes {
		if got.ID != n.ID {
			continue
		}
		if got.InternalType != "gen-image" {
			t.Fatalf("落盘 type = %s", got.InternalType)
		}
		// modelId 必须真的落盘——CLI 存在的意义之一就是不再制造无 modelId 的节点。
		if got.Params["modelId"] != "doubao-image" {
			t.Fatalf("modelId 未落盘: %+v", got.Params)
		}
		if got.Status != "idle" {
			t.Fatalf("新建节点 status = %s", got.Status)
		}
		return
	}
	t.Fatal("新节点没进快照")
}

func TestCLINodeCreateRejectsReadonlyType(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPost, env.base()+"/nodes", `{"type":"preview"}`, nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeNodeReadonlyType {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

func TestCLINodeCreateRequiresModel(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPost, env.base()+"/nodes", `{"type":"image-generator","params":{"prompt":"x"}}`, nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeModelRequired {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	// 失败不得留下半个节点。
	if len(env.canvas(t).Nodes) != 3 {
		t.Fatal("失败的创建不该改动快照")
	}
}

// merge patch：只改传来的键，别的字段原样保留。
func TestCLINodePatchMergesInsteadOfReplacing(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/n-2", `{"params":{"prompt":"改过的词"}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	for _, n := range env.canvas(t).Nodes {
		if n.ID != "n-2" {
			continue
		}
		if n.Params["prompt"] != "改过的词" {
			t.Fatalf("prompt 未改: %+v", n.Params)
		}
		// 没传的键必须还在——覆盖式写入会把用户在网页刚设的比例抹掉。
		if n.Params["ratio"] != "16:9" || n.Params["modelId"] != "doubao-image" {
			t.Fatalf("merge patch 抹掉了其它参数: %+v", n.Params)
		}
		return
	}
	t.Fatal("节点消失了")
}

func TestCLINodePatchRejectsReadonlyNode(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/n-3", `{"params":{"prompt":"x"}}`, nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeNodeReadonlyType {
		t.Fatalf("preview 节点应拒改: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCLINodePatchMissingNode(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/n-nope", `{"params":{"prompt":"x"}}`, nil)
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeNodeNotFound {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// 删节点必须连带清掉它的边——分两次写会在中间态留下悬空边，网页读到会报错。
func TestCLINodeDeleteCascadesEdges(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	rec := env.req(t, http.MethodDelete, env.base()+"/nodes/n-1", "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	c := env.canvas(t)
	for _, n := range c.Nodes {
		if n.ID == "n-1" {
			t.Fatal("节点没删掉")
		}
	}
	for _, e := range c.Edges {
		if e.FromNodeID == "n-1" || e.ToNodeID == "n-1" {
			t.Fatalf("留下了悬空边: %+v", e)
		}
	}
	// 与被删节点无关的边不受影响。
	if len(c.Edges) != 1 || c.Edges[0].ID != "c-2" {
		t.Fatalf("edges = %+v", c.Edges)
	}
}

func TestCLINodeDeleteRejectsReadonly(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodDelete, env.base()+"/nodes/n-3", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// 幂等：CLI 网络失败重试同一条创建命令，不能建出第二个节点。
func TestCLINodeCreateIdempotentReplay(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	body := `{"type":"image-generator","params":{"modelId":"m1","prompt":"x"}}`
	hdr := map[string]string{"Idempotency-Key": "cmd-42"}

	r1 := env.req(t, http.MethodPost, env.base()+"/nodes", body, hdr)
	r2 := env.req(t, http.MethodPost, env.base()+"/nodes", body, hdr)

	if r1.Code != http.StatusCreated || r2.Code != http.StatusCreated {
		t.Fatalf("codes = %d %d", r1.Code, r2.Code)
	}
	if r1.Body.String() != r2.Body.String() {
		t.Fatalf("重放响应必须逐字相同:\n%s\n%s", r1.Body.String(), r2.Body.String())
	}
	if got := len(env.canvas(t).Nodes); got != 4 {
		t.Fatalf("节点数 = %d, want 4（只该新增一个）", got)
	}
}

func TestCLINodeCreateIdempotencyKeyReuseConflicts(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	hdr := map[string]string{"Idempotency-Key": "cmd-42"}

	env.req(t, http.MethodPost, env.base()+"/nodes", `{"type":"image-generator","params":{"modelId":"m1"}}`, hdr)
	rec := env.req(t, http.MethodPost, env.base()+"/nodes", `{"type":"image-input"}`, hdr)

	if rec.Code != http.StatusConflict || decodeCLIError(t, rec).Code != cliapi.CodeIdempotencyKeyReused {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- 连线写入 ----

func TestCLIEdgeCreateIsIdempotentOnSamePair(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	body := `{"fromNodeId":"n-1","toNodeId":"n-3"}`

	r1 := env.req(t, http.MethodPost, env.base()+"/edges", body, nil)
	r2 := env.req(t, http.MethodPost, env.base()+"/edges", body, nil)
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("codes = %d %d body=%s", r1.Code, r2.Code, r2.Body.String())
	}
	var e1, e2 cliapi.Edge
	_ = json.Unmarshal(r1.Body.Bytes(), &e1)
	_ = json.Unmarshal(r2.Body.Bytes(), &e2)
	if e1.ID != e2.ID {
		t.Fatalf("第二次应返回原连线: %s vs %s", e1.ID, e2.ID)
	}
	if got := len(env.canvas(t).Edges); got != 3 {
		t.Fatalf("edges = %d, want 3（不得攒出重复边）", got)
	}
}

// 输入顺序由 connections 数组位置承载：按建边顺序读出来必须一致。
func TestCLIEdgeOrderFollowsInsertionOrder(t *testing.T) {
	env := newCLIWriteEnv(t, `{"nodes":[
		{"id":"a","type":"input-image","content":"http://o/a.png"},
		{"id":"b","type":"input-audio","content":"http://o/b.mp3"},
		{"id":"c","type":"input-image","content":"http://o/c.png"},
		{"id":"g","type":"gen-video"}
	],"connections":[]}`)

	for _, from := range []string{"a", "b", "c"} {
		rec := env.req(t, http.MethodPost, env.base()+"/edges",
			`{"fromNodeId":"`+from+`","toNodeId":"g"}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("建边 %s 失败: %d %s", from, rec.Code, rec.Body.String())
		}
	}
	edges := env.canvas(t).Edges
	want := []string{"a", "b", "c"}
	if len(edges) != 3 {
		t.Fatalf("edges = %+v", edges)
	}
	for i, w := range want {
		if edges[i].FromNodeID != w {
			t.Fatalf("顺序错位: edges = %+v, want from 顺序 %v", edges, want)
		}
	}
}

func TestCLIEdgeCreateValidatesEndpoints(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	rec := env.req(t, http.MethodPost, env.base()+"/edges", `{"fromNodeId":"n-nope","toNodeId":"n-2"}`, nil)
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeNodeNotFound {
		t.Fatalf("缺失上游 = %d %s", rec.Code, rec.Body.String())
	}
	rec = env.req(t, http.MethodPost, env.base()+"/edges", `{"fromNodeId":"n-2","toNodeId":"n-2"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("自环 = %d %s", rec.Code, rec.Body.String())
	}
	rec = env.req(t, http.MethodPost, env.base()+"/edges", `{"fromNodeId":"","toNodeId":"n-2"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空端点 = %d", rec.Code)
	}
}

func TestCLIEdgeDelete(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	if rec := env.req(t, http.MethodDelete, env.base()+"/edges/c-1", "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if got := len(env.canvas(t).Edges); got != 1 {
		t.Fatalf("edges = %d", got)
	}
	rec := env.req(t, http.MethodDelete, env.base()+"/edges/c-nope", "", nil)
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeEdgeNotFound {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- 批量几何 ----

// 12 个节点摆位置曾经是 12 条命令 × (canvas get + resolve + patch)，中间夹着
// 12 轮 agent 思考。批量端点的验收标准：一次请求、一次 version+1、整批原子。
func TestCLIPatchNodePositionsBatchIsAtomicSingleVersionBump(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload) // version = 17

	// n-3 是只读 preview：坐标不属于「内容/参数」，网页上任何节点都能拖，
	// 「agent 能做的 = UI 能做的」——批量摆位不看可写类型。
	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/positions", `{"nodes":[
		{"id":"n-1","x":10,"y":500},
		{"id":"n-2","x":300,"y":500,"width":400,"height":300},
		{"id":"n-3","y":3400}
	]}`, map[string]string{"If-Match": `"snapshot-17"`})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Version int64         `json:"version"`
		Nodes   []cliapi.Node `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Version != 18 {
		t.Fatalf("三个节点一批只应 +1：version = %d", res.Version)
	}
	if len(res.Nodes) != 3 || res.Nodes[1].ID != "n-2" || res.Nodes[1].Size.Width != 400 {
		t.Fatalf("响应应按请求顺序回新几何: %+v", res.Nodes)
	}

	c := env.canvas(t)
	if c.Version != 18 {
		t.Fatalf("落盘 version = %d", c.Version)
	}
	byID := map[string]cliapi.Node{}
	for _, n := range c.Nodes {
		byID[n.ID] = n
	}
	if byID["n-1"].Position != (cliapi.Position{X: 10, Y: 500}) {
		t.Fatalf("n-1 = %+v", byID["n-1"].Position)
	}
	if byID["n-2"].Position != (cliapi.Position{X: 300, Y: 500}) || byID["n-2"].Size != (cliapi.Size{Width: 400, Height: 300}) {
		t.Fatalf("n-2 = %+v %+v", byID["n-2"].Position, byID["n-2"].Size)
	}
	// 只传 y 的节点 x 不动。
	if byID["n-3"].Position != (cliapi.Position{X: 200, Y: 3400}) {
		t.Fatalf("n-3 = %+v", byID["n-3"].Position)
	}
	// 几何补丁绝不碰参数。
	if byID["n-2"].Params["prompt"] != "雨后城市" {
		t.Fatalf("prompt 被动了: %#v", byID["n-2"].Params["prompt"])
	}
}

// 任一 id 不存在整批不落地——半批成功会让 agent 拿到一张它不知道是什么样的画布。
func TestCLIPatchNodePositionsUnknownIDRejectsWholeBatch(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/positions",
		`{"nodes":[{"id":"n-1","x":999},{"id":"n-nope","x":1}]}`, nil)
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeNodeNotFound {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(decodeCLIError(t, rec).Message, "n-nope") {
		t.Fatalf("错误要点名是哪个 id 不存在: %s", rec.Body.String())
	}
	c := env.canvas(t)
	if c.Version != 17 || c.Nodes[0].Position.X != 0 {
		t.Fatalf("不该有任何落盘: version=%d n-1.x=%v", c.Version, c.Nodes[0].Position.X)
	}
}

func TestCLIPatchNodePositionsValidation(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	cases := map[string]string{
		"空数组":      `{"nodes":[]}`,
		"缺 id":     `{"nodes":[{"x":1}]}`,
		"没有任何几何字段": `{"nodes":[{"id":"n-1"}]}`,
		"同一 id 重复": `{"nodes":[{"id":"n-1","x":1},{"id":"n-1","x":2}]}`,
	}
	for name, body := range cases {
		rec := env.req(t, http.MethodPatch, env.base()+"/nodes/positions", body, nil)
		if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeInvalidArgument {
			t.Errorf("%s: = %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if env.canvas(t).Version != 17 {
		t.Fatal("坏请求不该动快照")
	}
}

func TestCLIPatchNodePositionsHonoursIfMatch(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPatch, env.base()+"/nodes/positions",
		`{"nodes":[{"id":"n-1","x":1}]}`, map[string]string{"If-Match": `"snapshot-16"`})
	if rec.Code != http.StatusConflict || decodeCLIError(t, rec).Code != cliapi.CodeCanvasVersionConflict {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- If-Match 并发保护 ----

func TestCLIIfMatchVersionConflict(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload) // 初始 version = 17

	ok := env.req(t, http.MethodPost, env.base()+"/nodes",
		`{"type":"image-input"}`, map[string]string{"If-Match": `"snapshot-17"`})
	if ok.Code != http.StatusCreated {
		t.Fatalf("版本匹配应放行: %d %s", ok.Code, ok.Body.String())
	}
	// 上一次写入后 version 变成 18，再用 17 就该冲突。
	stale := env.req(t, http.MethodPost, env.base()+"/nodes",
		`{"type":"image-input"}`, map[string]string{"If-Match": `"snapshot-17"`})
	if stale.Code != http.StatusConflict || decodeCLIError(t, stale).Code != cliapi.CodeCanvasVersionConflict {
		t.Fatalf("= %d %s", stale.Code, stale.Body.String())
	}
}

func TestCLIIfMatchMalformed(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	rec := env.req(t, http.MethodPost, env.base()+"/nodes",
		`{"type":"image-input"}`, map[string]string{"If-Match": `"W/abc"`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- 项目写入 ----

func TestCLIProjectCreateAndPatch(t *testing.T) {
	env := newCLIWriteEnv(t, "")

	rec := env.req(t, http.MethodPost, "/api/v1/cli/projects", `{"name":"新项目","description":"说明"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var p cliapi.Project
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.ID == 0 || p.Name != "新项目" {
		t.Fatalf("project = %+v", p)
	}

	// PATCH 只传 description：name 必须保留。底层 Update 是全量覆盖，
	// service 得先读后写补上这个语义差。
	rec = env.req(t, http.MethodPatch, "/api/v1/cli/projects/"+itoa(p.ID), `{"description":"新说明"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Name != "新项目" || p.Description != "新说明" {
		t.Fatalf("部分更新把 name 弄丢了: %+v", p)
	}
}

func TestCLIProjectCreateRequiresName(t *testing.T) {
	env := newCLIWriteEnv(t, "")
	rec := env.req(t, http.MethodPost, "/api/v1/cli/projects", `{"name":"   "}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

func TestCLIProjectDeleteThenGone(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)

	if rec := env.req(t, http.MethodDelete, env.base(), "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("= %d", rec.Code)
	}
	if rec := env.req(t, http.MethodGet, env.base(), "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("删后应 404, got %d", rec.Code)
	}
}

func TestCLIWriteCrossUserDenied(t *testing.T) {
	env := newCLIWriteEnv(t, cliTestCanvasPayload)
	other := "/api/v1/cli/projects/" + itoa(env.store.add(env.userID+999, "别人的", cliTestCanvasPayload))

	cases := []struct{ method, path, body string }{
		{http.MethodPost, other + "/nodes", `{"type":"image-input"}`},
		{http.MethodPatch, other + "/nodes/n-2", `{"params":{"prompt":"x"}}`},
		{http.MethodDelete, other + "/nodes/n-2", ""},
		{http.MethodPost, other + "/edges", `{"fromNodeId":"n-1","toNodeId":"n-2"}`},
		{http.MethodDelete, other + "/edges/c-1", ""},
		{http.MethodPatch, other, `{"name":"改名"}`},
		{http.MethodDelete, other, ""},
	}
	for _, c := range cases {
		rec := env.req(t, c.method, c.path, c.body, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}
