package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// fakeEnqueuer 记录最后一次 Enqueue 的入参，供「CLI 与网页产出一致」的断言用。
type fakeEnqueuer struct {
	calls  []canvasgen.EnqueueInput
	nextID int64
	err    error
	status *canvasgen.Status
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, in canvasgen.EnqueueInput) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.calls = append(f.calls, in)
	f.nextID++
	return f.nextID, nil
}

func (f *fakeEnqueuer) GetStatus(_ context.Context, _ int64, taskID int64) (*canvasgen.Status, error) {
	if f.status == nil {
		return &canvasgen.Status{Status: "processing"}, nil
	}
	return f.status, nil
}

type cliRunEnv struct {
	*cliWriteEnv
	gen *fakeEnqueuer
}

// runCanvas 是一张跑得起来的画布：一个上游图、一个上游文本、一个可运行的
// gen-image、一个没有 modelId 的存量 gen-image、一个 generating 中的节点。
const cliRunCanvas = `{
  "nodes": [
    {"id":"img","type":"input-image","content":"http://obj/a.png","status":"succeeded"},
    {"id":"txt","type":"text-node","settings":{"text":"上游文本"}},
    {"id":"gen","type":"gen-image","status":"idle","settings":{"modelId":"img-model","prompt":"雨后城市","ratio":"1:1","resolution":"1K"}},
    {"id":"legacy","type":"gen-image","status":"succeeded","content":"http://obj/old.png","settings":{"prompt":"老节点"}},
    {"id":"busy","type":"gen-image","status":"generating","settings":{"modelId":"img-model"}},
    {"id":"vid","type":"gen-video","status":"idle","settings":{"modelId":"vid-model","videoPrompt":"镜头推进","ratio":"16:9","resolution":"720P"}},
    {"id":"prev","type":"preview","content":"http://obj/p.png","settings":{"kind":"image"}}
  ],
  "connections": [
    {"id":"e1","from":"img","to":"gen"},
    {"id":"e2","from":"txt","to":"gen"}
  ]
}`

var cliRunModels = []models.ModelConfig{
	{
		ModelID: "img-model", Name: "图片模型", Type: "image", Enabled: true,
		UISchema: json.RawMessage(`{
			"ratio":{"label":"比例","default":"1:1","options":[]},
			"resolution":{"label":"分辨率","default":"1K","options":[]},
			"n":{"label":"数量","default":2,"options":[]}}`),
	},
	{
		ModelID: "vid-model", Name: "视频模型", Type: "video", Enabled: true,
		UISchema: json.RawMessage(`{
			"ratio":{"label":"比例","default":"16:9","options":[]},
			"resolution":{"label":"分辨率","default":"720P","options":[]},
			"duration":{"label":"时长","default":5,"options":[]}}`),
	},
	{ModelID: "off-model", Name: "停用模型", Type: "image", Enabled: false},
}

func newCLIRunEnv(t *testing.T) *cliRunEnv {
	t.Helper()
	store := newFakeProjectStore()
	gen := &fakeEnqueuer{}
	sign := func(s string) string { return s + "?sig=1" }

	svc := cliapi.NewService(store, sign).
		WithGeneration(gen, &cliFakeModelRepo{list: cliRunModels})

	router := NewRouter(Deps{
		Auth: cliTestAuth(),
		CLI:  NewCLIHandler(svc, newMemIdem()),
	})
	tok := testCLIToken

	base := &cliWriteEnv{router: router, store: store, token: tok, userID: LocalUserID}
	base.pid = store.add(LocalUserID, "可运行画布", cliRunCanvas)
	return &cliRunEnv{cliWriteEnv: base, gen: gen}
}

func (e *cliRunEnv) run(t *testing.T, nodeID, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return e.req(t, http.MethodPost, e.base()+"/nodes/"+nodeID+"/runs", body, headers)
}

// ---- 核心验收：CLI 提交的 inputs 必须与网页产出一致 ----

func TestCLIRunInputsMatchWebSubmission(t *testing.T) {
	env := newCLIRunEnv(t)

	rec := env.run(t, "gen", "", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(env.gen.calls) != 1 {
		t.Fatalf("Enqueue 调用次数 = %d", len(env.gen.calls))
	}
	got := env.gen.calls[0]
	if got.ModelID != "img-model" || got.NodeID != "gen" || got.ProjectID != env.pid {
		t.Fatalf("enqueue 目标错: %+v", got)
	}

	// 期望值 = 前端 buildImageGenerationInputs 的产出：
	// prompt 由 composePrompt 合成（上游 text-node 的词在前、节点自身的词在后），
	// dynamic 来自 uiSchema 合并（n 取 schema default 2），
	// size/width/height 由 calculateResolution(1:1,1K) 得出，
	// images 来自上游连线（已签名），image 是第一张。
	want := map[string]any{
		"prompt": "上游文本\n雨后城市", "ratio": "1:1", "resolution": "1K", "n": float64(2),
		"size": "1024x1024", "width": float64(1024), "height": float64(1024),
		"images": []any{"http://obj/a.png?sig=1"},
		"image":  "http://obj/a.png?sig=1",
	}
	assertInputsEqual(t, want, got.Inputs)
}

// 节点自身的词为空时，合成结果里就只剩上游 text-node 那句 —— 与网页
// fallbackPrompt 同源（composePrompt 会把空的一侧丢掉，不留下多余的换行）。
func TestCLIRunFallsBackToUpstreamText(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "gen", `{"overrides":{"prompt":""}}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := env.gen.calls[0].Inputs["prompt"]; got != "上游文本" {
		t.Fatalf("prompt = %v, want 上游文本", got)
	}
}

func TestCLIRunVideoInputsIncludeAllMediaArrays(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "vid", "", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	in := env.gen.calls[0].Inputs
	for _, k := range []string{"images", "videos", "audios"} {
		if in[k] == nil {
			t.Fatalf("视频 inputs 缺 %s: %+v", k, in)
		}
	}
	if in["size"] != "1280x720" {
		t.Fatalf("size = %v", in["size"])
	}
}

// 视频节点的提示词在画布里存 settings.videoPrompt（GenVideoNode.tsx 读写的
// 就是它）。CLI 曾经只认 settings.prompt，于是网页建的视频节点从 CLI 跑生成
// 时提示词是空的——空 prompt 会静默回退到上游文本，用户拿到一段完全不相干的
// 视频还不知道为什么。上游收到的必须是节点上那句词。
func TestCLIRunVideoUsesWebVideoPromptKey(t *testing.T) {
	env := newCLIRunEnv(t)
	if rec := env.run(t, "vid", "", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := env.gen.calls[0].Inputs["prompt"]; got != "镜头推进" {
		t.Fatalf("上游收到的 prompt = %v，want 节点上的 videoPrompt", got)
	}
	// 写回之后再读，提示词不能在往返里丢——CLI 对外暴露的键始终是 prompt。
	for _, n := range env.canvas(t).Nodes {
		if n.ID != "vid" {
			continue
		}
		if n.Params["prompt"] != "镜头推进" {
			t.Fatalf("写回后 params.prompt = %v", n.Params["prompt"])
		}
		if _, ok := n.Params["videoPrompt"]; ok {
			t.Fatalf("画布内部键名漏给了调用方：%v", n.Params)
		}
		return
	}
	t.Fatal("节点消失")
}

// 覆盖时也走同一条翻译：--overrides 传的是 prompt，落盘要变成 videoPrompt。
func TestCLIRunVideoOverridePromptPersistsUnderWebKey(t *testing.T) {
	env := newCLIRunEnv(t)
	if rec := env.run(t, "vid", `{"overrides":{"prompt":"换一句"}}`, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := env.gen.calls[0].Inputs["prompt"]; got != "换一句" {
		t.Fatalf("覆盖后上游收到 %v", got)
	}
	for _, n := range env.canvas(t).Nodes {
		if n.ID == "vid" && n.Params["prompt"] != "换一句" {
			t.Fatalf("覆盖值没写回：%+v", n.Params)
		}
	}
}

// ---- 模型规则 ----

// 生产 52% 的成功 gen-image 节点没有 modelId。绝不自动挑一个。
func TestCLIRunRequiresModelOnLegacyNode(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "legacy", "", nil)

	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeModelRequired {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if len(env.gen.calls) != 0 {
		t.Fatal("不得在没有 modelId 时提交任务")
	}
}

// 存量节点可以靠 overrides 补上模型，一次性把它跑起来。
func TestCLIRunOverrideSuppliesModel(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "legacy", `{"overrides":{"modelId":"img-model"}}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if env.gen.calls[0].ModelID != "img-model" {
		t.Fatalf("modelId = %s", env.gen.calls[0].ModelID)
	}
}

func TestCLIRunRejectsDisabledModel(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "gen", `{"overrides":{"modelId":"off-model"}}`, nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeModelDisabled {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if len(env.gen.calls) != 0 {
		t.Fatal("停用模型不得静默换一个跑起来")
	}
}

func TestCLIRunRejectsMissingModel(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "gen", `{"overrides":{"modelId":"不存在"}}`, nil)
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeModelNotFound {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// 视频节点配图片模型必须报错——静默跑起来会让用户拿到不知从哪来的产物。
func TestCLIRunModelKindMustMatchNodeType(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "vid", `{"overrides":{"modelId":"img-model"}}`, nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeInvalidModelParams {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- 状态与重复运行 ----

// 没有实时同步，用户看不到 CLI 的改动就会重复触发，每次都是真实冻结。
func TestCLIRunRejectsAlreadyGenerating(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "busy", "", nil)

	if rec.Code != http.StatusConflict || decodeCLIError(t, rec).Code != cliapi.CodeNodeAlreadyRunning {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	if len(env.gen.calls) != 0 {
		t.Fatal("generating 中的节点不得再次提交")
	}
}

func TestCLIRunRejectsReadonlyAndNonGeneratorNodes(t *testing.T) {
	env := newCLIRunEnv(t)

	rec := env.run(t, "prev", "", nil)
	if decodeCLIError(t, rec).Code != cliapi.CodeNodeReadonlyType {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body.String())
	}
	rec = env.run(t, "img", "", nil)
	if decodeCLIError(t, rec).Code != cliapi.CodeInvalidNodeType {
		t.Fatalf("input-image = %d %s", rec.Code, rec.Body.String())
	}
	rec = env.run(t, "nope", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺失节点 = %d", rec.Code)
	}
}

// 提交成功后节点必须落 generating + taskId，且合并后的参数（含 modelId）写回节点。
func TestCLIRunPersistsGeneratingAndEffectiveParams(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.run(t, "legacy", `{"overrides":{"modelId":"img-model","ratio":"16:9"}}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	var res cliapi.RunResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)

	for _, n := range env.canvas(t).Nodes {
		if n.ID != "legacy" {
			continue
		}
		if n.Status != "generating" {
			t.Fatalf("status = %s", n.Status)
		}
		if n.TaskID != res.TaskID || n.TaskID == 0 {
			t.Fatalf("taskId 未落盘: node=%d resp=%d", n.TaskID, res.TaskID)
		}
		// 合并结果写回节点：下次不带 overrides 也是新值，且不再是无 modelId 节点。
		if n.Params["modelId"] != "img-model" || n.Params["ratio"] != "16:9" {
			t.Fatalf("生效参数未写回: %+v", n.Params)
		}
		return
	}
	t.Fatal("节点消失")
}

func TestCLIRunInputLimitInvalidArgument(t *testing.T) {
	env := newCLIRunEnv(t)
	env.gen.err = &generation.InputLimitError{Msg: "该模型需要连接至少 1 个视频"}

	rec := env.run(t, "gen", "", nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeInvalidArgument {
		t.Fatalf("数量违约应 INVALID_ARGUMENT（不可重试——重试改变不了连线）, got %d %s",
			rec.Code, rec.Body.String())
	}
}

func TestCLIRunInputDurationInvalidArgument(t *testing.T) {
	env := newCLIRunEnv(t)
	env.gen.err = &generation.InputDurationError{Msg: "无法读取输入视频时长，请确认视频有效后重试"}

	rec := env.run(t, "gen", "", nil)
	if rec.Code != http.StatusBadRequest || decodeCLIError(t, rec).Code != cliapi.CodeInvalidArgument {
		t.Fatalf("时长不可读应 INVALID_ARGUMENT（不可重试——重试改变不了视频本身）, got %d %s",
			rec.Code, rec.Body.String())
	}
}

// ---- 幂等 ----

func TestCLIRunIdempotentReplayDoesNotDoubleCharge(t *testing.T) {
	env := newCLIRunEnv(t)
	hdr := map[string]string{"Idempotency-Key": "run-1"}

	r1 := env.run(t, "gen", "", hdr)
	r2 := env.run(t, "gen", "", hdr)

	if len(env.gen.calls) != 1 {
		t.Fatalf("重放必须不再入队，calls = %d", len(env.gen.calls))
	}
	if r1.Body.String() != r2.Body.String() {
		t.Fatalf("重放响应不一致:\n%s\n%s", r1.Body.String(), r2.Body.String())
	}
}

// 同一个 key 用在两个节点上必须冲突，否则第二个节点会静默拿到第一个的 taskId。
func TestCLIRunIdempotencyKeyIsNodeScoped(t *testing.T) {
	env := newCLIRunEnv(t)
	hdr := map[string]string{"Idempotency-Key": "run-1"}

	env.run(t, "gen", "", hdr)
	rec := env.run(t, "vid", "", hdr)
	if rec.Code != http.StatusConflict || decodeCLIError(t, rec).Code != cliapi.CodeIdempotencyKeyReused {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
}

// ---- 任务状态 ----

func TestCLIGetRunStatus(t *testing.T) {
	env := newCLIRunEnv(t)
	env.gen.status = &canvasgen.Status{
		Status: "succeeded", OutputURLs: []string{"http://obj/out.png"}, AssetIDs: []int64{7},
	}
	rec := env.req(t, http.MethodGet, "/api/v1/cli/runs/42", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d %s", rec.Code, rec.Body.String())
	}
	var st cliapi.RunStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Status != "succeeded" || st.TaskID != 42 {
		t.Fatalf("status = %+v", st)
	}
	// 结果 URL 必须签名。
	if len(st.OutputURLs) != 1 || st.OutputURLs[0] != "http://obj/out.png?sig=1" {
		t.Fatalf("outputUrls 未签名: %+v", st.OutputURLs)
	}
	if len(st.AssetIDs) != 1 || st.AssetIDs[0] != 7 {
		t.Fatalf("assetIds = %+v", st.AssetIDs)
	}
}

func TestCLIGetRunInvalidID(t *testing.T) {
	env := newCLIRunEnv(t)
	if rec := env.req(t, http.MethodGet, "/api/v1/cli/runs/abc", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("= %d", rec.Code)
	}
}

// assertInputsEqual 比 JSON，规避 int / float64 噪音。
func assertInputsEqual(t *testing.T, want, got map[string]any) {
	t.Helper()
	w, _ := json.Marshal(want)
	// got 来自 Go 内存（width 是 int），先过一遍 JSON round-trip 再比。
	gb, _ := json.Marshal(got)
	var norm map[string]any
	_ = json.Unmarshal(gb, &norm)
	g, _ := json.Marshal(norm)
	if string(w) != string(g) {
		t.Fatalf("inputs 与网页产出不一致:\n want %s\n got  %s", w, g)
	}
}

// ---- node get 的运行前审计信息 ----

// Agent 可以先搭完整张图再由用户确认是否花钱——前提是运行前能看清
// 每个节点会拿到什么输入、什么顺序。
func TestCLINodeGetExposesDerivedInputsAndOutputs(t *testing.T) {
	env := newCLIRunEnv(t)

	rec := env.req(t, http.MethodGet, env.base()+"/nodes/gen", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got cliapi.NodeDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DerivedInputs == nil {
		t.Fatal("生成节点必须给出派生输入摘要")
	}
	// 上游是一张图 + 一个文本节点，分类与顺序要与运行时逐字一致。
	if len(got.DerivedInputs.Images) != 1 || got.DerivedInputs.Images[0] != "http://obj/a.png?sig=1" {
		t.Fatalf("images = %+v", got.DerivedInputs.Images)
	}
	if len(got.DerivedInputs.Texts) != 1 || got.DerivedInputs.Texts[0] != "上游文本" {
		t.Fatalf("texts = %+v", got.DerivedInputs.Texts)
	}
	if got.DerivedInputs.Videos == nil || got.DerivedInputs.Audios == nil {
		t.Fatalf("空的媒体列表也要是 []，不能是 null: %+v", got.DerivedInputs)
	}
	if got.Outputs == nil {
		t.Fatal("outputs 必须是数组，未生成时为空数组")
	}
}

// 派生输入只对生成节点有意义；输入节点与只读节点不该带这块。
func TestCLINodeGetOmitsDerivedInputsForNonGenerators(t *testing.T) {
	env := newCLIRunEnv(t)
	for _, id := range []string{"img", "prev", "txt"} {
		rec := env.req(t, http.MethodGet, env.base()+"/nodes/"+id, "", nil)
		var got cliapi.NodeDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.DerivedInputs != nil {
			t.Errorf("节点 %s 不该带 derivedInputs: %+v", id, got.DerivedInputs)
		}
	}
}

// 已生成过的节点要能看到全部产物，供运行前对比。
func TestCLINodeGetListsOutputsFromHistory(t *testing.T) {
	env := newCLIRunEnv(t)
	rec := env.req(t, http.MethodGet, env.base()+"/nodes/legacy", "", nil)
	var got cliapi.NodeDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Outputs) != 1 || got.Outputs[0] != "http://obj/old.png?sig=1" {
		t.Fatalf("outputs = %+v", got.Outputs)
	}
}
