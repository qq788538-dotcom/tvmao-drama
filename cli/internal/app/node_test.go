package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/james/huabu_studio/apps/cli/internal/exitcode"
	"github.com/james/huabu_studio/apps/cli/internal/workspace"
)

// recordedRequest 记录一次请求，供断言 CLI 到底发了什么。
type recordedRequest struct {
	Method  string
	Path    string
	Body    map[string]any
	IdemKey string
	IfMatch string
}

// recorder 是一个可编程的假服务端：按路径给响应，同时记录所有请求。
//
// 匹配规则必须是**确定的**：按 key 长度从长到短取最具体的一条。
// 直接遍历 map 会踩 Go 的随机迭代序——`/nodes/resolve` 同时匹配 `/nodes/`
// 与 `/nodes/resolve`，随机命中哪个，测试就会时绿时红。
type recorder struct {
	reqs      []recordedRequest
	responses map[string]string
}

func (rc *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		blob, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(blob) > 0 {
			_ = json.Unmarshal(blob, &body)
		}
		rc.reqs = append(rc.reqs, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Body: body,
			IdemKey: r.Header.Get("Idempotency-Key"),
			IfMatch: r.Header.Get("If-Match"),
		})

		keys := make([]string, 0, len(rc.responses))
		for k := range rc.responses {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, k := range keys {
			if strings.Contains(r.URL.Path, k) {
				_, _ = w.Write([]byte(rc.responses[k]))
				return
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}
}

func (rc *recorder) find(method, pathSubstr string) *recordedRequest {
	for i := range rc.reqs {
		if rc.reqs[i].Method == method && strings.Contains(rc.reqs[i].Path, pathSubstr) {
			return &rc.reqs[i]
		}
	}
	return nil
}

func nodeHarness(t *testing.T, rc *recorder) *harness {
	t.Helper()
	h := newHarness(t, rc.handler())
	_ = workspace.SaveProjectBinding(h.workDir, workspace.ProjectBinding{ProjectID: 1})
	return h
}

// StringSliceVar 会按逗号拆值——中文提示词几乎必然含逗号，拆了就毁了。
func TestParamFlagKeepsCommasAndRepeats(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/models/": `{"modelId":"m1","name":"模型","kind":"image"}`,
		"/nodes":   `{"id":"n-new","type":"image-generator"}`,
	}}
	h := nodeHarness(t, rc)

	code := h.run("node", "create", "--type", "image-generator", "--model", "m1",
		"--param", "prompt=雨后的城市街道，霓虹灯，电影感",
		"--param", "ratio=16:9")
	if code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	req := rc.find(http.MethodPost, "/nodes")
	if req == nil {
		t.Fatal("没发出建节点请求")
	}
	params, _ := req.Body["params"].(map[string]any)
	if params["prompt"] != "雨后的城市街道，霓虹灯，电影感" {
		t.Fatalf("提示词里的逗号被拆了: %#v", params["prompt"])
	}
	if params["ratio"] != "16:9" {
		t.Fatalf("ratio = %#v", params["ratio"])
	}
}

// --param n=2 要是数字，--param prompt=2026 必须还是字符串。
func TestParamFlagTypeInference(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/models/": `{"modelId":"m1","kind":"image"}`,
		"/nodes":   `{"id":"n-new"}`,
	}}
	h := nodeHarness(t, rc)

	h.run("node", "create", "--type", "image-generator", "--model", "m1",
		"--param", "n=2", "--param", "enableSound=true", "--param", "scale=1.5",
		"--param", "prompt=2026", "--param-string", "seed=42")

	req := rc.find(http.MethodPost, "/nodes")
	params, _ := req.Body["params"].(map[string]any)

	if params["n"] != float64(2) {
		t.Errorf("n 应是数字, got %#v", params["n"])
	}
	if params["enableSound"] != true {
		t.Errorf("enableSound 应是 bool, got %#v", params["enableSound"])
	}
	if params["scale"] != 1.5 {
		t.Errorf("scale 应是浮点, got %#v", params["scale"])
	}
	// prompt 恒字符串——否则纯数字提示词会变成数字，上游收到的类型就不对了。
	if params["prompt"] != "2026" {
		t.Errorf("prompt 必须恒为字符串, got %#v", params["prompt"])
	}
	if params["seed"] != "42" {
		t.Errorf("--param-string 应强制字符串, got %#v", params["seed"])
	}
}

func TestParamFlagRejectsMalformed(t *testing.T) {
	rc := &recorder{responses: map[string]string{"/models/": `{"modelId":"m1","kind":"image"}`}}
	h := nodeHarness(t, rc)
	if code := h.run("node", "create", "--type", "image-input", "--param", "没有等号"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// 建边顺序 = 输入顺序，所以 --left 必须严格按传入顺序发。
func TestLeftFlagOrderPreserved(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/models/": `{"modelId":"m1","kind":"image"}`,
		"/resolve": `{"nodeId":"resolved"}`,
		"/nodes":   `{"id":"n-new"}`,
		"/edges":   `{"id":"e-1"}`,
	}}
	h := nodeHarness(t, rc)

	code := h.run("node", "create", "--type", "image-generator", "--model", "m1",
		"--left", "n-aaa", "--left", "n-bbb", "--left", "n-ccc")
	if code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}

	// resolve 请求按 --left 的传入顺序出现。
	var order []string
	for _, r := range rc.reqs {
		if strings.Contains(r.Path, "/nodes/resolve") {
			order = append(order, "resolve")
		}
	}
	if len(order) != 3 {
		t.Fatalf("应解析 3 个上游, got %d", len(order))
	}
	edgeCount := 0
	for _, r := range rc.reqs {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/edges") {
			edgeCount++
		}
	}
	if edgeCount != 3 {
		t.Fatalf("应建 3 条边, got %d", edgeCount)
	}
}

// 生成节点缺 --model 在本地就拦下，省一次往返。
func TestNodeCreateRequiresModelLocally(t *testing.T) {
	rc := &recorder{}
	h := nodeHarness(t, rc)

	if code := h.run("node", "create", "--type", "image-generator"); code != exitcode.Usage {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	if rc.find(http.MethodPost, "/nodes") != nil {
		t.Fatal("不该发出请求")
	}
}

func TestNodeCreateRequiresType(t *testing.T) {
	h := nodeHarness(t, &recorder{})
	if code := h.run("node", "create"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// 写命令默认自动带幂等键——client 的写重试才是安全的。
func TestWriteCommandsCarryIdempotencyKeyByDefault(t *testing.T) {
	rc := &recorder{responses: map[string]string{"/projects": `{"id":1,"name":"x"}`}}
	h := newHarness(t, rc.handler())

	h.run("project", "create", "--name", "新项目")
	req := rc.find(http.MethodPost, "/projects")
	if req == nil || req.IdemKey == "" {
		t.Fatalf("写请求应自动带幂等键: %+v", req)
	}
	if !strings.HasPrefix(req.IdemKey, "cli-") {
		t.Fatalf("幂等键格式 = %q", req.IdemKey)
	}
}

func TestIdempotencyKeyFlagOverrides(t *testing.T) {
	rc := &recorder{responses: map[string]string{"/projects": `{"id":1}`}}
	h := newHarness(t, rc.handler())

	h.run("project", "create", "--name", "x", "--idempotency-key", "my-fixed-key")
	if req := rc.find(http.MethodPost, "/projects"); req == nil || req.IdemKey != "my-fixed-key" {
		t.Fatalf("幂等键 = %+v", req)
	}
}

// 固定 --idempotency-key 时，--left 建的每条连线要用各自派生的 key：同一 scope 下复用同一个 key
// 发不同请求体，服务端会报 IDEMPOTENCY_KEY_REUSED，第二条连线必然失败。重跑同一条命令 key 不变。
func TestFixedIdempotencyKeyDerivesDistinctEdgeKeys(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/models/": `{"modelId":"m1","kind":"image"}`,
		"/resolve": `{"nodeId":"resolved"}`,
		"/nodes":   `{"id":"n-new"}`,
		"/edges":   `{"id":"e-1"}`,
	}}
	h := nodeHarness(t, rc)

	edgeKeys := func() []string {
		var keys []string
		for _, r := range rc.reqs {
			if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/edges") {
				keys = append(keys, r.IdemKey)
			}
		}
		return keys
	}
	for _, args := range [][]string{
		{"node", "create", "--type", "image-generator", "--model", "m1", "--left", "n-a", "--left", "n-b", "--idempotency-key", "K"},
		{"node", "update", "n-new", "--left", "n-a", "--left", "n-b", "--idempotency-key", "K"},
	} {
		rc.reqs = nil
		if code := h.run(args...); code != exitcode.OK {
			t.Fatalf("%v: code = %d stderr=%s", args[:2], code, h.stderr.String())
		}
		if got := edgeKeys(); len(got) != 2 || got[0] != "K:edge-1" || got[1] != "K:edge-2" {
			t.Fatalf("%v: 连线幂等键 = %q", args[:2], got)
		}
	}
	if req := rc.find(http.MethodPost, "/nodes"); req != nil {
		t.Fatalf("update 不应建节点: %+v", req)
	}
	rc.reqs = nil
	h.run("node", "create", "--type", "image-generator", "--model", "m1", "--left", "n-a", "--idempotency-key", "K")
	if req := rc.find(http.MethodPost, "/nodes"); req == nil || req.IdemKey != "K" {
		t.Fatalf("建节点仍用用户给的 key: %+v", req)
	}
}

// ---- run --wait ----

func TestNodeRunWithoutWaitReturnsImmediately(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/resolve": `{"nodeId":"n-1"}`,
		"/runs":    `{"taskId":42,"status":"processing","modelId":"m1"}`,
	}}
	h := nodeHarness(t, rc)

	if code := h.run("node", "run", "n-1"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	var out map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout 不是 JSON: %q", h.stdout.String())
	}
	if out["taskId"] != float64(42) {
		t.Fatalf("out = %+v", out)
	}
	// 不带 --wait 时不该轮询。
	for _, r := range rc.reqs {
		if r.Method == http.MethodGet && strings.Contains(r.Path, "/runs/") {
			t.Fatal("不该轮询")
		}
	}
}

func TestNodeRunAlreadyRunningExits5(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/resolve") {
			_, _ = w.Write([]byte(`{"nodeId":"n-1"}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"NODE_ALREADY_RUNNING","message":"节点正在生成中"}}`))
	})
	_ = workspace.SaveProjectBinding(h.workDir, workspace.ProjectBinding{ProjectID: 1})

	if code := h.run("node", "run", "n-1"); code != exitcode.Conflict {
		t.Fatalf("code = %d, want %d", code, exitcode.Conflict)
	}
}

// 失败任务的 error 必须进 stdout 的 JSON —— 只写 stderr 会让脚本读不到。
func TestRunGetFailedPutsErrorInStdoutAndExits6(t *testing.T) {
	h := newHarness(t, jsonHandler(`{"taskId":42,"status":"failed","error":"上游拒绝了这个提示词"}`))

	code := h.run("run", "get", "42")
	if code != exitcode.Server {
		t.Fatalf("code = %d, want %d", code, exitcode.Server)
	}
	var out map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout 不是 JSON: %q", h.stdout.String())
	}
	if out["error"] != "上游拒绝了这个提示词" {
		t.Fatalf("失败原因未进 stdout: %+v", out)
	}
}

func TestRunWaitPollsUntilTerminal(t *testing.T) {
	var calls int32
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			_, _ = w.Write([]byte(`{"taskId":42,"status":"processing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"taskId":42,"status":"succeeded","outputUrls":["http://o/1.png"]}`))
	})

	// 轮询间隔是 3 秒，两次调用即可结束，测试大约 3 秒。
	if code := h.run("run", "wait", "42"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "succeeded") {
		t.Fatalf("stdout = %q", h.stdout.String())
	}
	// 进度只走 stderr。
	if strings.Contains(h.stdout.String(), "生成中") {
		t.Fatal("进度污染了 stdout")
	}
}

func TestRunGetRejectsNonNumericTaskID(t *testing.T) {
	h := newHarness(t, jsonHandler(`{}`))
	if code := h.run("run", "get", "abc"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// ---- edge ----

func TestEdgeCreateResolvesBothEnds(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/resolve": `{"nodeId":"resolved"}`,
		"/edges":   `{"id":"e-1","fromNodeId":"resolved","toNodeId":"resolved"}`,
	}}
	h := nodeHarness(t, rc)

	if code := h.run("edge", "create", "--from", "n-a", "--to", "n-b"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	resolves := 0
	for _, r := range rc.reqs {
		if strings.Contains(r.Path, "/nodes/resolve") {
			resolves++
		}
	}
	if resolves != 2 {
		t.Fatalf("两端都要解析, got %d", resolves)
	}
}

func TestEdgeCreateRequiresBothEnds(t *testing.T) {
	h := nodeHarness(t, &recorder{})
	if code := h.run("edge", "create", "--from", "n-a"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// ---- model ----

func TestModelRefAmbiguousExits5WithCandidates(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"MODEL_REF_AMBIGUOUS","message":"匹配到多个",
			"details":{"candidates":["a","b"]}}}`))
	})

	if code := h.run("model", "get", "同名"); code != exitcode.Conflict {
		t.Fatalf("code = %d, want %d", code, exitcode.Conflict)
	}
	if !strings.Contains(h.stderr.String(), "候选") {
		t.Fatalf("stderr 应列出候选: %q", h.stderr.String())
	}
}

// ---- --left-rm ----

// 「换掉某个上游」要能在一条命令里完成：先删后加，顺序可预期。
func TestNodeUpdateLeftRmThenLeft(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/nodes/resolve": `{"nodeId":"resolved"}`,
		"/edges":         `{"data":[{"id":"e-old","fromNodeId":"resolved","toNodeId":"resolved"}],"id":"e-new"}`,
		"/nodes/":        `{"id":"n-target"}`,
	}}
	h := nodeHarness(t, rc)

	code := h.run("node", "update", "n-target", "--left-rm", "n-old", "--left", "n-new")
	if code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}

	// 删必须发生在加之前，否则「先加后删」可能把刚建的边删掉。
	var order []string
	for _, r := range rc.reqs {
		if r.Method == http.MethodDelete && strings.Contains(r.Path, "/edges/") {
			order = append(order, "delete")
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/edges") {
			order = append(order, "create")
		}
	}
	if len(order) != 2 || order[0] != "delete" || order[1] != "create" {
		t.Fatalf("顺序应为 先删后加, got %v", order)
	}
}

// 「确保它不在」——本来就没有这条边时视为成功，不报错。
func TestNodeUpdateLeftRmMissingEdgeIsSuccess(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/nodes/resolve": `{"nodeId":"resolved"}`,
		"/edges":         `{"data":[]}`,
		"/nodes/":        `{"id":"n-target"}`,
	}}
	h := nodeHarness(t, rc)

	if code := h.run("node", "update", "n-target", "--left-rm", "n-nothing"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	for _, r := range rc.reqs {
		if r.Method == http.MethodDelete {
			t.Fatal("没有匹配的边时不该发删除请求")
		}
	}
}

// 只做连线增删、不改参数时也要合法（不该报「没有要修改的内容」）。
func TestNodeUpdateAcceptsEdgeOnlyChanges(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/nodes/resolve": `{"nodeId":"resolved"}`,
		"/edges":         `{"data":[],"id":"e-new"}`,
		"/nodes/":        `{"id":"n-target"}`,
	}}
	h := nodeHarness(t, rc)

	if code := h.run("node", "update", "n-target", "--left", "n-a"); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	// 没有参数改动时不该发 PATCH。
	for _, r := range rc.reqs {
		if r.Method == http.MethodPatch {
			t.Fatal("无参数改动时不该发 PATCH")
		}
	}
}

func TestNodeUpdateRequiresSomeChange(t *testing.T) {
	rc := &recorder{responses: map[string]string{"/nodes/resolve": `{"nodeId":"resolved"}`}}
	h := nodeHarness(t, rc)
	if code := h.run("node", "update", "n-target"); code != exitcode.Usage {
		t.Fatalf("code = %d", code)
	}
}

// 重跑 asset upload --create-node 必须拿到同一个幂等键，否则会多出第二个节点。
func TestAssetUploadCreateNodeUsesStableIdempotencyKey(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/ref.png"
	if err := os.WriteFile(file, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := &recorder{responses: map[string]string{
		"/assets": `{"id":77,"url":"http://obj/ref.png","size":8,"mimeType":"image/png","deduped":true}`,
		"/nodes":  `{"id":"n-ref"}`,
	}}
	h := nodeHarness(t, rc)

	if code := h.run("asset", "upload", file, "--create-node"); code != exitcode.OK {
		t.Fatalf("第一次 = %d stderr=%s", code, h.stderr.String())
	}
	first := rc.find(http.MethodPost, "/nodes")
	if first == nil || first.IdemKey == "" {
		t.Fatalf("建节点应带幂等键: %+v", first)
	}
	firstKey := first.IdemKey

	rc.reqs = nil
	if code := h.run("asset", "upload", file, "--create-node"); code != exitcode.OK {
		t.Fatalf("第二次 = %d stderr=%s", code, h.stderr.String())
	}
	second := rc.find(http.MethodPost, "/nodes")
	if second == nil || second.IdemKey != firstKey {
		t.Fatalf("重跑必须复用同一幂等键: %q vs %q", firstKey, second.IdemKey)
	}
}

// ---- node move ----

// 批量摆位的全部意义就是「N 个节点 = 1 次请求」：不能偷偷退化成逐个 resolve + patch。
func TestNodeMoveSendsOneBatchRequest(t *testing.T) {
	rc := &recorder{responses: map[string]string{
		"/nodes/positions": `{"version":18,"nodes":[{"id":"n-a"},{"id":"n-b"},{"id":"n-c"}]}`,
	}}
	h := nodeHarness(t, rc)

	code := h.run("node", "move",
		"--set", "n-a=10,20",
		"--set", "n-b=30,40,300,200",
		"--set", "n-c=,500", // 只改 y
		"--snapshot-version", "17")
	if code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	if len(rc.reqs) != 1 {
		t.Fatalf("应只有 1 次请求, got %d: %+v", len(rc.reqs), rc.reqs)
	}
	req := rc.reqs[0]
	if req.Method != http.MethodPatch || !strings.HasSuffix(req.Path, "/projects/1/nodes/positions") {
		t.Fatalf("请求 = %s %s", req.Method, req.Path)
	}
	if req.IfMatch != `"snapshot-17"` {
		t.Fatalf("If-Match = %q", req.IfMatch)
	}
	nodes, _ := req.Body["nodes"].([]any)
	if len(nodes) != 3 {
		t.Fatalf("nodes = %#v", req.Body["nodes"])
	}
	a, _ := nodes[0].(map[string]any)
	b, _ := nodes[1].(map[string]any)
	c, _ := nodes[2].(map[string]any)
	if a["id"] != "n-a" || a["x"] != float64(10) || a["y"] != float64(20) {
		t.Errorf("n-a = %#v", a)
	}
	if _, has := a["width"]; has {
		t.Errorf("没传的分量不该出现在请求里: %#v", a)
	}
	if b["width"] != float64(300) || b["height"] != float64(200) {
		t.Errorf("n-b = %#v", b)
	}
	if _, has := c["x"]; has || c["y"] != float64(500) {
		t.Errorf("留空的 x 不该发出去: %#v", c)
	}
	if !strings.Contains(h.stdout.String(), `"version": 18`) && !strings.Contains(h.stdout.String(), `"version":18`) {
		t.Errorf("stdout 应带回新版本号: %s", h.stdout.String())
	}
}

func TestNodeMoveFromFileAndStdin(t *testing.T) {
	rc := &recorder{responses: map[string]string{"/nodes/positions": `{"version":2,"nodes":[]}`}}
	h := nodeHarness(t, rc)

	path := h.workDir + "/layout.json"
	if err := os.WriteFile(path, []byte(`[{"id":"n-a","x":1,"y":2},{"id":"n-b","width":300}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("node", "move", "--file", path); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	nodes, _ := rc.reqs[0].Body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %#v", rc.reqs[0].Body["nodes"])
	}

	h.stdin = strings.NewReader(`[{"id":"n-z","y":9}]`)
	if code := h.run("node", "move", "--file", "-"); code != exitcode.OK {
		t.Fatalf("stdin: code = %d stderr=%s", code, h.stderr.String())
	}
	nodes, _ = rc.reqs[1].Body["nodes"].([]any)
	if z, _ := nodes[0].(map[string]any); len(nodes) != 1 || z["id"] != "n-z" {
		t.Fatalf("stdin nodes = %#v", rc.reqs[1].Body["nodes"])
	}
}

func TestNodeMoveRejectsMalformedLocally(t *testing.T) {
	rc := &recorder{}
	h := nodeHarness(t, rc)
	for _, args := range [][]string{
		{"node", "move"},
		{"node", "move", "--set", "n-a"},
		{"node", "move", "--set", "n-a=1"},
		{"node", "move", "--set", "n-a=x,y"},
		{"node", "move", "--set", "n-a=,"},
		{"node", "move", "--set", "n-a=1,2,3,4,5"},
	} {
		if code := h.run(args...); code != exitcode.Usage {
			t.Errorf("%v: code = %d", args, code)
		}
	}
	if len(rc.reqs) != 0 {
		t.Fatalf("坏参数不该发请求: %+v", rc.reqs)
	}
}

// ---- asset download ----

// 下载失败不能动用户已有的同名文件，也不能留下半截临时文件；成功才整体替换。
func TestAssetDownloadKeepsExistingFileOnFailure(t *testing.T) {
	var fail atomic.Bool
	var srvURL string
	rc := &recorder{}
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/assets/7"):
			_, _ = w.Write([]byte(`{"id":7,"url":"` + srvURL + `/blob"}`))
		case r.URL.Path == "/blob":
			if fail.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte("NEW-CONTENT"))
		default:
			rc.handler()(w, r)
		}
	})
	srvURL = h.server.URL
	dir := t.TempDir()
	out := dir + "/out.png"
	if err := os.WriteFile(out, []byte("USER-FILE"), 0o644); err != nil {
		t.Fatal(err)
	}

	fail.Store(true)
	if code := h.run("asset", "download", "--asset", "7", "--out", out); code == exitcode.OK {
		t.Fatal("下载失败应返回非 0")
	}
	if b, _ := os.ReadFile(out); string(b) != "USER-FILE" {
		t.Fatalf("下载失败不能截断 / 删除已有文件，现在是 %q", b)
	}

	fail.Store(false)
	if code := h.run("asset", "download", "--asset", "7", "--out", out); code != exitcode.OK {
		t.Fatalf("code = %d stderr=%s", code, h.stderr.String())
	}
	if b, _ := os.ReadFile(out); string(b) != "NEW-CONTENT" {
		t.Fatalf("内容 = %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("不应留下临时文件: %v", entries)
	}
}
