package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/library"
)

// 一张混合画布：两张图（一张有内容一张没有）、一个视频、一个只读 preview。
// 只有「类型认得出媒体 + 真的有 content」的节点才该出现在合规状态里。
const cliSeedanceCanvas = `{
  "nodes": [
    {"id":"img-a","type":"input-image","content":"media://k/a.png","status":"succeeded","settings":{}},
    {"id":"img-b","type":"gen-image","status":"idle","settings":{"modelId":"m"}},
    {"id":"vid","type":"gen-video","content":"media://k/v.mp4","status":"succeeded","settings":{"modelId":"m"}},
    {"id":"upl","type":"video-input","content":"media://k/u.mp4","status":"succeeded","settings":{}},
    {"id":"prev","type":"preview","content":"media://k/p.png","status":"succeeded","settings":{}}
  ],
  "connections": []
}`

// fakeSeedanceGate 记下提交，并按 objectKey 给回状态。
type fakeSeedanceGate struct {
	submitted []struct{ Key, AssetType string }
	byKey     map[string]library.SeedanceAsset
	submitErr error
}

func (f *fakeSeedanceGate) Submit(_ context.Context, _ int64, objectKey, assetType string) (*library.SeedanceAsset, error) {
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	f.submitted = append(f.submitted, struct{ Key, AssetType string }{objectKey, assetType})
	rec := library.SeedanceAsset{ObjectKey: objectKey, AssetType: assetType, Status: "pending"}
	return &rec, nil
}

func (f *fakeSeedanceGate) Status(_ int64, keys []string) (map[string]library.SeedanceAsset, error) {
	out := map[string]library.SeedanceAsset{}
	for _, k := range keys {
		if a, ok := f.byKey[k]; ok {
			out[k] = a
		}
	}
	return out, nil
}

// fakeKeyExtractor 认 media:// 前缀，与真实 storage 的行为同形。
type fakeKeyExtractor struct{}

func (fakeKeyExtractor) ExtractKey(url string) (string, bool) {
	const p = "media://"
	if strings.HasPrefix(url, p) && len(url) > len(p) {
		return url[len(p):], true
	}
	return "", false
}

type cliSeedanceEnv struct {
	router http.Handler
	gate   *fakeSeedanceGate
	token  string
	pid    int64
}

func newCLISeedanceEnv(t *testing.T, gate *fakeSeedanceGate) *cliSeedanceEnv {
	t.Helper()
	store := newFakeProjectStore()

	svc := cliapi.NewService(store, func(s string) string { return s }).
		WithSeedance(gate, fakeKeyExtractor{})

	router := NewRouter(Deps{
		Auth: cliTestAuth(),
		CLI:  NewCLIHandler(svc, newMemIdem()),
	})
	tok := testCLIToken
	return &cliSeedanceEnv{
		router: router, gate: gate, token: tok,
		pid: store.add(LocalUserID, "合规画布", cliSeedanceCanvas),
	}
}

func (e *cliSeedanceEnv) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *cliSeedanceEnv) verify(t *testing.T, nodeID string) *httptest.ResponseRecorder {
	return e.do(t, http.MethodPost,
		"/api/v1/cli/projects/"+strconv.FormatInt(e.pid, 10)+"/nodes/"+nodeID+"/compliance")
}

// 提交校验：从节点类型推出 kind、从 content 解析出 object_key。
func TestCLIVerifySubmitsWithKeyAndKindFromNode(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	rec := env.verify(t, "img-a")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(env.gate.submitted) != 1 {
		t.Fatalf("提交次数 = %d", len(env.gate.submitted))
	}
	got := env.gate.submitted[0]
	if got.Key != "k/a.png" {
		t.Fatalf("object_key = %q，应从节点 content 解析", got.Key)
	}
	// AssetType 是上游原值（Image/Video/Audio），由 library.NormalizeAssetType 给。
	if !strings.EqualFold(got.AssetType, "image") {
		t.Fatalf("assetType = %q，input-image 节点应按图片校验", got.AssetType)
	}
}

func TestCLIVerifyVideoNodeUsesVideoAssetType(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	if rec := env.verify(t, "vid"); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := env.gate.submitted[0].AssetType; !strings.EqualFold(got, "video") {
		t.Fatalf("assetType = %q，gen-video 应按视频校验", got)
	}
}

// 没有内容的节点提交了也没意义——上游拿不到东西，报错比静默提交一个空 key 好。
func TestCLIVerifyRejectsNodeWithoutContent(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	rec := env.verify(t, "img-b")
	if rec.Code == http.StatusAccepted {
		t.Fatalf("空内容节点不该提交成功")
	}
	if len(env.gate.submitted) != 0 {
		t.Fatalf("不该调用上游：%+v", env.gate.submitted)
	}
}

// preview 这类节点虽然有 content，但不是 CLI 认得的媒体类型，报错说清楚。
func TestCLIVerifyRejectsUnsupportedNodeType(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	if rec := env.verify(t, "prev"); rec.Code == http.StatusAccepted {
		t.Fatal("preview 节点不该能提交合规校验")
	}
}

func TestCLIVerifyUnknownNodeIs404(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	if rec := env.verify(t, "no-such"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// 状态列表：只列「认得出媒体类型且有内容」的节点；没提交过的是 unverified。
func TestCLIComplianceListCoversOnlyMediaNodes(t *testing.T) {
	gate := &fakeSeedanceGate{byKey: map[string]library.SeedanceAsset{
		"k/a.png": {ObjectKey: "k/a.png", AssetType: "Image", Status: "active"},
	}}
	env := newCLISeedanceEnv(t, gate)
	rec := env.do(t, http.MethodGet, "/api/v1/cli/projects/"+strconv.FormatInt(env.pid, 10)+"/compliance")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list []cliapi.ComplianceState
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	// img-b 没内容、prev 类型不支持，都不该出现。
	if len(list) != 3 {
		t.Fatalf("应只有 img-a、vid、upl 三条：%+v", list)
	}
	byNode := map[string]cliapi.ComplianceState{}
	for _, s := range list {
		byNode[s.NodeID] = s
	}
	if byNode["img-a"].Status != "active" || byNode["img-a"].Kind != "image" {
		t.Fatalf("img-a = %+v", byNode["img-a"])
	}
	// 从没提交过的必须明确是 unverified，不能是空串——调用方要能区分
	// 「没查到」和「查到了但还没结论」。
	if byNode["vid"].Status != "unverified" {
		t.Fatalf("vid = %+v，未提交过应为 unverified", byNode["vid"])
	}
	// 上传的视频输入节点也要能校验，kind 按类型推断为 video。
	if byNode["upl"].Kind != "video" {
		t.Fatalf("upl = %+v，video-input 应按视频校验", byNode["upl"])
	}
}

// 顺序必须稳定：内部是 map 查状态，输出得按快照里的节点顺序，
// 否则同一个项目每次 list 出来的行序都不一样，diff 和肉眼比对全废。
func TestCLIComplianceListOrderIsStable(t *testing.T) {
	env := newCLISeedanceEnv(t, &fakeSeedanceGate{})
	var first []string
	for i := 0; i < 10; i++ {
		rec := env.do(t, http.MethodGet, "/api/v1/cli/projects/"+strconv.FormatInt(env.pid, 10)+"/compliance")
		var list []cliapi.ComplianceState
		_ = json.Unmarshal(rec.Body.Bytes(), &list)
		ids := make([]string, 0, len(list))
		for _, s := range list {
			ids = append(ids, s.NodeID)
		}
		if i == 0 {
			first = ids
			continue
		}
		if strings.Join(ids, ",") != strings.Join(first, ",") {
			t.Fatalf("第 %d 次顺序变了：%v vs %v", i, ids, first)
		}
	}
}

// 没接 seedance 依赖时要明确报「未配置」，不能 nil panic。
func TestCLIComplianceWithoutDepsReportsUnconfigured(t *testing.T) {
	store := newFakeProjectStore()
	pid := store.add(LocalUserID, "无依赖", cliSeedanceCanvas)
	router := NewRouter(Deps{
		Auth: cliTestAuth(),
		CLI: NewCLIHandler(
			cliapi.NewService(store, func(s string) string { return s }), newMemIdem()),
	})
	tok := testCLIToken

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cli/projects/"+strconv.FormatInt(pid, 10)+"/compliance", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req) // 不 panic 就已经是一半的断言
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
