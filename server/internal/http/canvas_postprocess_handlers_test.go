package http

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// ── 手写 fake(仿 drama_postprocess_handlers_test.go 风格) ──────────────────────

// desubFakeSettings 实现 settings.Repository，持一份 canvas_postprocess_config JSON。
type desubFakeSettings struct{ vals map[string]string }

func (s *desubFakeSettings) Get(key string) (string, error) { return s.vals[key], nil }
func (s *desubFakeSettings) Set(key, value string) error {
	if s.vals == nil {
		s.vals = map[string]string{}
	}
	s.vals[key] = value
	return nil
}
func (s *desubFakeSettings) All() (map[string]string, error) { return s.vals, nil }

// newDesubSettings 按给定去字幕绑定列表构造 fake settings（结构简单，Marshal 不会失败，故不需要 *testing.T）。
func newDesubSettings(bindings []canvasgen.CanvasDesubBinding) *desubFakeSettings {
	return newPostprocessSettings(bindings, nil)
}

// newPostprocessSettings 同上但可带转高清模型列表——转高清测试用。
func newPostprocessSettings(bindings []canvasgen.CanvasDesubBinding, upscaleModels []canvasgen.CanvasUpscaleBinding) *desubFakeSettings {
	cfg := canvasgen.CanvasPostprocessConfig{SubtitleRemovalModels: bindings, UpscaleModels: upscaleModels}
	raw, _ := json.Marshal(cfg)
	return &desubFakeSettings{vals: map[string]string{canvasgen.SettingCanvasPostprocess: string(raw)}}
}

// desubFakeProjects 实现 canvasPostprocessProjects：ownedUserID/ownedProjectID 组合通过归属校验，
// GetLatest 直接返回预置快照（模拟 workflow_snapshots.payload）。
type desubFakeProjects struct {
	ownedUserID, ownedProjectID int64
	snapshot                    *projects.Snapshot
}

func (f *desubFakeProjects) GetForUser(userID, projectID int64) (*projects.Project, error) {
	if userID == f.ownedUserID && projectID == f.ownedProjectID {
		return &projects.Project{ID: projectID, UserID: userID}, nil
	}
	return nil, projects.ErrNotFound
}

func (f *desubFakeProjects) GetLatest(projectID int64) (*projects.Snapshot, error) {
	return f.snapshot, nil
}

// desubSnapshot 构造一份只含一个源节点的画布快照 payload——真走一次 json.Marshal/Unmarshal，
// 让 srcGenTaskId 这类数字字段按真实反序列化路径变成 float64（与生产一致）。
func desubSnapshot(t *testing.T, nodeID, content string, settings map[string]any) *projects.Snapshot {
	t.Helper()
	node := map[string]any{"id": nodeID, "content": content}
	if settings != nil {
		node["settings"] = settings
	}
	doc := map[string]any{"nodes": []any{node}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return &projects.Snapshot{Payload: raw, Version: 1}
}

// desubFakeAssets 实现 canvasPostprocessAssets：按 objectKey 查/回填 duration_ms，记录回填调用。
type desubFakeAssets struct {
	found    map[string]int64 // objectKey -> duration_ms（存在即代表 found=true）
	setCalls map[string]int64
}

func (f *desubFakeAssets) GetDurationMsByObjectKey(objectKey string) (int64, bool, error) {
	if ms, ok := f.found[objectKey]; ok {
		return ms, true, nil
	}
	return 0, false, nil
}

func (f *desubFakeAssets) SetDurationMsByObjectKey(objectKey string, durationMs int64) error {
	if f.setCalls == nil {
		f.setCalls = map[string]int64{}
	}
	f.setCalls[objectKey] = durationMs
	return nil
}

// desubFakeGen 实现 canvasPostprocessGeneration：按 taskID 反查 generation task 的 output。
type desubFakeGen struct{ tasks map[int64]*generation.Task }

func (f *desubFakeGen) Get(_ context.Context, _ int64, taskID int64) (*generation.Task, error) {
	if t, ok := f.tasks[taskID]; ok {
		return t, nil
	}
	return nil, generation.ErrNotFound
}

// desubFakeEnqueuer 实现 canvasPostprocessEnqueuer：捕获入队的 EnqueueInput 供断言。
type desubFakeEnqueuer struct {
	taskID   int64
	err      error
	captured *canvasgen.EnqueueInput
}

func (f *desubFakeEnqueuer) Enqueue(_ context.Context, in canvasgen.EnqueueInput) (int64, error) {
	cp := in
	f.captured = &cp
	if f.err != nil {
		return 0, f.err
	}
	return f.taskID, nil
}

// newDesubTestHandler 组装一个 CanvasPostprocessHandler，userID=1 owns projectID=2。
// models lookup 给空的——desub 提交路径不读模型（计费在 generation 侧），只有预估路径要。
func newDesubTestHandler(bindings []canvasgen.CanvasDesubBinding, snapshot *projects.Snapshot, assetsRepo *desubFakeAssets, genRepo *desubFakeGen) (*CanvasPostprocessHandler, *desubFakeEnqueuer) {
	settingsRepo := newDesubSettings(bindings)
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2, snapshot: snapshot}
	if assetsRepo == nil {
		assetsRepo = &desubFakeAssets{}
	}
	if genRepo == nil {
		genRepo = &desubFakeGen{}
	}
	enqueuer := &desubFakeEnqueuer{taskID: 999}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{}}
	h := NewCanvasPostprocessHandler(settingsRepo, enqueuer, projRepo, assetsRepo, genRepo, lookup, postprocessNoopSigner{})
	return h, enqueuer
}

func desubReq(body map[string]any) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks/desub", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(withUserID(context.Background(), 1))
}

// ── 预估端点的 fixture ────────────────────────────────────────────────────────

// estimateBillingRaw 是「1 积分/秒」的计费配置——让断言能直接从秒数推出积分：
// 137.2s → ceil(137.2) = 138 积分。用真实的 billing.EstimateFreeze 而非 fake，
// 因为本次要防的恰恰是「预估与结算走了两套算法」。
const estimateBillingRaw = `{"items":[{"unit":"second","qty":"req:inputs.duration","price":{"fixed":1}}]}`

// newEstimateTestHandler 组装带 models lookup 的 handler：白名单里放一个去字幕模型，
// 其计费配置为 1 积分/秒。
func newEstimateTestHandler(snapshot *projects.Snapshot, assetsRepo *desubFakeAssets) (*CanvasPostprocessHandler, *desubFakeAssets) {
	settingsRepo := newDesubSettings([]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}})
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2, snapshot: snapshot}
	if assetsRepo == nil {
		assetsRepo = &desubFakeAssets{}
	}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{
		"desub-std": {ModelID: "desub-std", BillingConfigRaw: json.RawMessage(estimateBillingRaw)},
	}}
	h := NewCanvasPostprocessHandler(settingsRepo, &desubFakeEnqueuer{taskID: 999}, projRepo, assetsRepo, &desubFakeGen{}, lookup, postprocessNoopSigner{})
	return h, assetsRepo
}

func estimateReq(body map[string]any) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks/postprocess-estimate", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(withUserID(context.Background(), 1))
}

// decodeEstimate 解出预估响应体。
func decodeEstimate(t *testing.T, rec *httptest.ResponseRecorder) (credits int64, durationMs int64) {
	t.Helper()
	var out struct {
		Credits    int64 `json:"credits"`
		DurationMs int64 `json:"durationMs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal estimate response: %v body=%s", err, rec.Body)
	}
	return out.Credits, out.DurationMs
}

// ── 测试 ─────────────────────────────────────────────────────────────────────

// 1) 未知模型 → 400「未知去字幕模型」。
func TestDesub_UnknownModel(t *testing.T) {
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		desubSnapshot(t, "node-src", "media://video1.mp4", nil),
		nil, nil,
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "unknown",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未知去字幕模型")) {
		t.Errorf("body should contain 未知去字幕模型, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("unknown model should not enqueue")
	}
}

// 2) 普通去字幕 + 源 duration_ms=8000 → enqueue inputs{video:<signed>, duration:8}。
func TestDesub_StandardWithKnownDuration(t *testing.T) {
	assetsRepo := &desubFakeAssets{found: map[string]int64{"video1.mp4": 8000}}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		desubSnapshot(t, "node-src", "media://video1.mp4", nil),
		assetsRepo, nil,
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured == nil {
		t.Fatal("should enqueue")
	}
	if got := enq.captured.Inputs["video"]; got != "https://cdn.example.com/video1.mp4" {
		t.Errorf("inputs.video = %v", got)
	}
	if got := enq.captured.Inputs["duration"]; got != 8.0 {
		t.Errorf("inputs.duration = %v, want 8.0", got)
	}
	if len(assetsRepo.setCalls) != 0 {
		t.Errorf("duration already known, should not backfill: %v", assetsRepo.setCalls)
	}
}

// 3) 普通去字幕 + duration_ms=NULL 但懒回填 probe 成功=6000 → 回填被调用 + enqueue duration:6。
func TestDesub_StandardLazyBackfillSucceeds(t *testing.T) {
	assetsRepo := &desubFakeAssets{}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		desubSnapshot(t, "node-src", "media://video2.mp4", nil),
		assetsRepo, nil,
	)
	h.download = fakeDownload([]byte("fake-bytes"))
	h.probe = func(_ context.Context, _ string) (int64, error) { return 6000, nil }

	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["duration"]; got != 6.0 {
		t.Errorf("inputs.duration = %v, want 6.0", got)
	}
	if ms, ok := assetsRepo.setCalls["video2.mp4"]; !ok || ms != 6000 {
		t.Errorf("should backfill video2.mp4=6000, got %v", assetsRepo.setCalls)
	}
}

// 4) duration_ms=NULL 且懒回填失败(probe 报错) → 400「无法获取视频时长」，不 enqueue。
func TestDesub_StandardLazyBackfillFails(t *testing.T) {
	assetsRepo := &desubFakeAssets{}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		desubSnapshot(t, "node-src", "media://video3.mp4", nil),
		assetsRepo, nil,
	)
	h.download = fakeDownload([]byte("fake-bytes"))
	h.probe = func(_ context.Context, _ string) (int64, error) { return 0, errors.New("ffprobe: no video packets") }

	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("无法获取视频时长")) {
		t.Errorf("body should contain 无法获取视频时长, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("backfill failure should not enqueue")
	}
	if len(assetsRepo.setCalls) != 0 {
		t.Errorf("failed probe should not backfill: %v", assetsRepo.setCalls)
	}
}

// 5) 限时模型 + upstreamExpire 未过期 + 反查到 upstream url → enqueue inputs.video=<上游url>（裸地址）。
func TestDesub_TimeLimitedLiveURL(t *testing.T) {
	assetsRepo := &desubFakeAssets{found: map[string]int64{"video4.mp4": 5000}}
	genRepo := &desubFakeGen{tasks: map[int64]*generation.Task{
		555: {ID: 555, Output: json.RawMessage(`{"upstreamUrls":["https://upstream.example.com/raw.mp4"]}`)},
	}}
	expire := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-live", IsTimeLimited: true}},
		desubSnapshot(t, "node-src", "media://video4.mp4", map[string]any{
			"upstreamExpire": expire,
			"srcGenTaskId":   555, // json 往返后变成 float64，验证 toInt64 兼容
		}),
		assetsRepo, genRepo,
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-live",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["video"]; got != "https://upstream.example.com/raw.mp4" {
		t.Errorf("inputs.video = %v, want raw upstream url", got)
	}
	if got := enq.captured.Inputs["duration"]; got != 5.0 {
		t.Errorf("inputs.duration = %v, want 5.0", got)
	}
}

// 6) 限时模型 + expire 已过期 → 400「暂不支持限时去字幕」。
func TestDesub_TimeLimitedExpired(t *testing.T) {
	genRepo := &desubFakeGen{tasks: map[int64]*generation.Task{
		555: {ID: 555, Output: json.RawMessage(`{"upstreamUrls":["https://upstream.example.com/raw.mp4"]}`)},
	}}
	expired := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-live", IsTimeLimited: true}},
		desubSnapshot(t, "node-src", "media://video5.mp4", map[string]any{
			"upstreamExpire": expired,
			"srcGenTaskId":   555,
		}),
		nil, genRepo,
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-live",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("暂不支持限时去字幕")) {
		t.Errorf("body should contain 暂不支持限时去字幕, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("expired live url should not enqueue")
	}
}

// 6b) 限时模型 + 无 srcGenTaskId(=0) → 同样 400「暂不支持限时去字幕」。
func TestDesub_TimeLimitedMissingSrcTask(t *testing.T) {
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-live", IsTimeLimited: true}},
		desubSnapshot(t, "node-src", "media://video6.mp4", map[string]any{
			"upstreamExpire": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		}),
		nil, &desubFakeGen{},
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-live",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("暂不支持限时去字幕")) {
		t.Errorf("body should contain 暂不支持限时去字幕, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("missing srcGenTaskId should not enqueue")
	}
}

// 源节点在快照里找不到 → 400「源视频未就绪」。
func TestDesub_SourceNodeMissing(t *testing.T) {
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		desubSnapshot(t, "node-other", "media://video1.mp4", nil),
		nil, nil,
	)
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("源视频未就绪")) {
		t.Errorf("body should contain 源视频未就绪, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("missing source node should not enqueue")
	}
}

// 项目不属于该用户 → 404。
func TestDesub_ProjectNotOwned(t *testing.T) {
	settingsRepo := newDesubSettings([]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}})
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{}}
	h := NewCanvasPostprocessHandler(settingsRepo, &desubFakeEnqueuer{}, projRepo, &desubFakeAssets{}, &desubFakeGen{}, lookup, postprocessNoopSigner{})
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 999, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body)
	}
}

// ── POST /canvas-tasks/postprocess-estimate ──────────────────────────────────
//
// 这组测试存在的理由：上一轮实现让前端拿 <video>.duration（容器声明值，可被单独
// 篡改）去算「预计 X 积分」，用户改了 metadata 就看到「预计 1 积分」而实际按真实
// 两分多钟结算。旧 spec A.5 本已规定「预估与结算统一读权威秒数」，只落实了结算那
// 一半。故预估改由后端算，且必须用与提交完全相同的 resolveDurationMs。

// E1) duration_ms 命中缓存 → credits 按权威秒数算，响应带 durationMs。
func TestPostprocessEstimate_KnownDuration(t *testing.T) {
	h, assetsRepo := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://video1.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"video1.mp4": 8000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	credits, durationMs := decodeEstimate(t, rec)
	if credits != 8 { // 8.0s × 1 积分/秒
		t.Errorf("credits = %d, want 8", credits)
	}
	if durationMs != 8000 {
		t.Errorf("durationMs = %d, want 8000", durationMs)
	}
	if len(assetsRepo.setCalls) != 0 {
		t.Errorf("duration already known, should not backfill: %v", assetsRepo.setCalls)
	}
}

// E2) 防薅回归（本次的核心）：请求体注入 duration=1，但 assets 里权威值是 137200ms。
// credits 必须按 137.2 秒算(ceil → 138)，证明请求体那个字段根本没被读。
// 这正是用户改视频容器 metadata 后「预计 1 积分」的复现场景。
func TestPostprocessEstimate_IgnoresClientDuration(t *testing.T) {
	h, _ := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://long.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"long.mp4": 137200}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std",
		"duration": 1, // 恶意/被篡改的申报值
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	credits, durationMs := decodeEstimate(t, rec)
	if credits != 138 {
		t.Errorf("credits = %d, want 138（按权威 137.2s 算，不是客户端申报的 1s）", credits)
	}
	if durationMs != 137200 {
		t.Errorf("durationMs = %d, want 137200", durationMs)
	}
}

// E3) duration_ms 为 NULL → 懒探测并回填，credits 用探测值。
// 与提交路径同一个 resolveDurationMs，故预估与提交绝不会不一致。
func TestPostprocessEstimate_LazyBackfill(t *testing.T) {
	h, assetsRepo := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://fresh.mp4", nil),
		&desubFakeAssets{},
	)
	h.download = fakeDownload([]byte("fake-bytes"))
	h.probe = func(_ context.Context, _ string) (int64, error) { return 6000, nil }

	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	credits, _ := decodeEstimate(t, rec)
	if credits != 6 {
		t.Errorf("credits = %d, want 6", credits)
	}
	if ms, ok := assetsRepo.setCalls["fresh.mp4"]; !ok || ms != 6000 {
		t.Errorf("should backfill fresh.mp4=6000, got %v", assetsRepo.setCalls)
	}
}

// E4) 探测失败 → 400，不返回任何 credits（无依据的预估等于骗人，宁可不显示）。
func TestPostprocessEstimate_ProbeFails(t *testing.T) {
	h, _ := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://broken.mp4", nil),
		&desubFakeAssets{},
	)
	h.download = fakeDownload([]byte("fake-bytes"))
	h.probe = func(_ context.Context, _ string) (int64, error) { return 0, errors.New("ffprobe: no video packets") }

	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("无法获取视频时长")) {
		t.Errorf("body should contain 无法获取视频时长, got %s", rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("credits")) {
		t.Errorf("失败时不应返回 credits, got %s", rec.Body)
	}
}

// E5) 白名单外的 modelId → 400，不落到 EstimateFreeze（防拿任意模型试探价格）。
func TestPostprocessEstimate_UnknownModel(t *testing.T) {
	h, _ := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://video1.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"video1.mp4": 8000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "not-whitelisted",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未知后处理模型")) {
		t.Errorf("body should contain 未知后处理模型, got %s", rec.Body)
	}
}

// E6) 源节点不在快照里 → 400「源视频未就绪」。
func TestPostprocessEstimate_SourceNodeMissing(t *testing.T) {
	h, _ := newEstimateTestHandler(
		desubSnapshot(t, "node-other", "media://video1.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"video1.mp4": 8000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("源视频未就绪")) {
		t.Errorf("body should contain 源视频未就绪, got %s", rec.Body)
	}
}

// E7) 他人项目 → 404（与 Desub 一致，不泄露项目存在性）。
func TestPostprocessEstimate_ProjectNotOwned(t *testing.T) {
	h, _ := newEstimateTestHandler(
		desubSnapshot(t, "node-src", "media://video1.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"video1.mp4": 8000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 999, "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body)
	}
}

// ── POST /canvas-tasks/upscale + 转高清档位 ───────────────────────────────────
//
// 转高清是「按秒 × 档位」计费，两个计费参数都不能信客户端：
//   - duration：请求体刻意不含该字段，服务端 resolveDurationMs 解析(与 desub 同一函数)；
//   - resolution：提交路径由 generation.Submit 的 uiSchema 白名单兜；预估路径不走 Submit，
//     故预估端点自查一遍（否则越界档位会让分级计费匹配不到、算出便宜价）。

// upscaleUISchema 是转高清模型的 uiSchema：两个档位。value 故意用字符串，
// 与 HTTP 请求体里过来的形态一致。
const upscaleUISchema = `{"resolution":{"options":[{"value":"2k","label":"2K 超清"},{"value":"4k","label":"4K 极清"}]}}`

// upscaleProUISchema 是第二个转高清模型的档位：与 upscale-hd 有同值档 4k，也有独有档 1080p。
const upscaleProUISchema = `{"resolution":{"options":[{"value":"1080p","label":"1080P"},{"value":"4k","label":"4K"}]}}`

// upscaleProBillingRaw 是第二个转高清模型的计费：2 积分/秒，与 upscale-hd 区分开。
const upscaleProBillingRaw = `{"items":[{"unit":"second","qty":"req:inputs.duration","price":{"fixed":2}}]}`

// newUpscaleTestHandler 组装带转高清模型列表的 handler：
//   - upscale-hd：uiSchema 出 2k/4k 档位，计费 1 积分/秒，显示名「标准高清」；
//   - upscale-pro：1080p/4k 档位，2 积分/秒，未填显示名；
//   - upscale-bare：模型中心没配 uiSchema 档位（后台配错的情形）。
func newUpscaleTestHandler(snapshot *projects.Snapshot, assetsRepo *desubFakeAssets) (*CanvasPostprocessHandler, *desubFakeEnqueuer, *desubFakeAssets) {
	settingsRepo := newPostprocessSettings(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std", IsTimeLimited: false}},
		[]canvasgen.CanvasUpscaleBinding{
			{ModelID: "upscale-hd", Label: "标准高清"},
			{ModelID: "upscale-bare", Label: "没档位"},
			{ModelID: "upscale-pro"},
		},
	)
	return newUpscaleTestHandlerWithSettings(settingsRepo, snapshot, assetsRepo)
}

func newUpscaleTestHandlerWithSettings(settingsRepo *desubFakeSettings, snapshot *projects.Snapshot, assetsRepo *desubFakeAssets) (*CanvasPostprocessHandler, *desubFakeEnqueuer, *desubFakeAssets) {
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2, snapshot: snapshot}
	if assetsRepo == nil {
		assetsRepo = &desubFakeAssets{}
	}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{
		"upscale-hd": {
			ModelID:          "upscale-hd",
			Hidden:           true, // 转高清模型在模型中心是隐藏的，档位仍要能下发
			UISchema:         json.RawMessage(upscaleUISchema),
			BillingConfigRaw: json.RawMessage(estimateBillingRaw),
		},
		"upscale-pro": {
			ModelID:          "upscale-pro",
			Hidden:           true,
			UISchema:         json.RawMessage(upscaleProUISchema),
			BillingConfigRaw: json.RawMessage(upscaleProBillingRaw),
		},
		"upscale-bare": {ModelID: "upscale-bare", Hidden: true},
	}}
	enqueuer := &desubFakeEnqueuer{taskID: 777}
	h := NewCanvasPostprocessHandler(settingsRepo, enqueuer, projRepo, assetsRepo, &desubFakeGen{}, lookup, postprocessNoopSigner{})
	return h, enqueuer, assetsRepo
}

func upscaleReq(body map[string]any) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks/upscale", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(withUserID(context.Background(), 1))
}

// upscaleConfigOut 是 GetConfig 响应里与转高清相关的部分。
type upscaleConfigOut struct {
	UpscaleModels []struct {
		ModelID     string           `json:"modelId"`
		Label       string           `json:"label"`
		Resolutions []map[string]any `json:"resolutions"`
	} `json:"upscaleModels"`
	UpscaleModel       string           `json:"upscaleModel"`
	UpscaleResolutions []map[string]any `json:"upscaleResolutions"`
	SubtitleRemoval    []map[string]any `json:"subtitleRemoval"`
}

func getUpscaleConfig(t *testing.T, h *CanvasPostprocessHandler) upscaleConfigOut {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/canvas/postprocess-config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	var out upscaleConfigOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// U1) GetConfig 按后台顺序下发每个转高清模型 + 各自从 uiSchema 读出的档位（模型隐藏也要能读到）；
// 没档位的模型不下发，显示名空则回落 modelId。
func TestCanvasPostprocessConfig_UpscaleResolutions(t *testing.T) {
	h, _, _ := newUpscaleTestHandler(desubSnapshot(t, "node-src", "media://v.mp4", nil), nil)
	out := getUpscaleConfig(t, h)
	if len(out.UpscaleModels) != 2 {
		t.Fatalf("upscaleModels = %+v, want upscale-hd + upscale-pro（upscale-bare 无档位应被滤掉）", out.UpscaleModels)
	}
	hd, pro := out.UpscaleModels[0], out.UpscaleModels[1]
	if hd.ModelID != "upscale-hd" || hd.Label != "标准高清" {
		t.Errorf("第一项 = %+v, want upscale-hd/标准高清", hd)
	}
	if len(hd.Resolutions) != 2 || hd.Resolutions[0]["value"] != "2k" {
		t.Errorf("upscale-hd 档位 = %v, want 2k/4k", hd.Resolutions)
	}
	if pro.ModelID != "upscale-pro" || pro.Label != "upscale-pro" {
		t.Errorf("第二项 = %+v, want upscale-pro 且显示名回落 modelId", pro)
	}
	if len(pro.Resolutions) != 2 || pro.Resolutions[0]["value"] != "1080p" {
		t.Errorf("upscale-pro 档位 = %v, want 1080p/4k", pro.Resolutions)
	}
	// 旧字段给发版时还开着的旧页面用：必须是第一个模型。
	if out.UpscaleModel != "upscale-hd" || len(out.UpscaleResolutions) != 2 {
		t.Errorf("旧字段 upscaleModel=%q upscaleResolutions=%v, want 第一个模型", out.UpscaleModel, out.UpscaleResolutions)
	}
	if len(out.SubtitleRemoval) != 1 {
		t.Errorf("去字幕白名单不该受影响, got %v", out.SubtitleRemoval)
	}
}

// U1b) 存量的旧版单模型配置：读时折成一项列表，节点能选、能提交。
func TestCanvasPostprocessConfig_LegacyUpscaleModel(t *testing.T) {
	legacy := &desubFakeSettings{vals: map[string]string{
		canvasgen.SettingCanvasPostprocess: `{"subtitleRemovalModels":[],"upscaleModel":"upscale-hd"}`,
	}}
	h, enq, _ := newUpscaleTestHandlerWithSettings(legacy,
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	out := getUpscaleConfig(t, h)
	if len(out.UpscaleModels) != 1 || out.UpscaleModels[0].ModelID != "upscale-hd" || out.UpscaleModels[0].Label != "upscale-hd" {
		t.Fatalf("upscaleModels = %+v, want 旧字段折成 upscale-hd 一项", out.UpscaleModels)
	}
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "4k",
	}))
	if rec.Code != http.StatusAccepted || enq.captured == nil {
		t.Fatalf("旧配置应能提交, got %d body=%s", rec.Code, rec.Body)
	}

}

// U2) 未配 upscaleModel → 档位空数组（前端据此显示「未配置转高清模型」）。
func TestCanvasPostprocessConfig_UpscaleNotConfigured(t *testing.T) {
	h, _ := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://v.mp4", nil), nil, nil,
	)
	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/canvas/postprocess-config", nil))
	var out struct {
		UpscaleModels      []map[string]any `json:"upscaleModels"`
		UpscaleModel       string           `json:"upscaleModel"`
		UpscaleResolutions []map[string]any `json:"upscaleResolutions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.UpscaleModels == nil || len(out.UpscaleModels) != 0 {
		t.Errorf("upscaleModels 应为空数组(不是 null), got %v", out.UpscaleModels)
	}
	if out.UpscaleModel != "" {
		t.Errorf("upscaleModel 应为空, got %q", out.UpscaleModel)
	}
	if len(out.UpscaleResolutions) != 0 {
		t.Errorf("upscaleResolutions 应为空数组, got %v", out.UpscaleResolutions)
	}
}

// U3) 转高清成功：inputs 带 video(签名地址) + duration(权威秒数) + resolution。
func TestUpscale_Success(t *testing.T) {
	h, enq, assetsRepo := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "4k",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured == nil {
		t.Fatal("should enqueue")
	}
	if got := enq.captured.Inputs["video"]; got != "https://cdn.example.com/hd.mp4" {
		t.Errorf("inputs.video = %v", got)
	}
	if got := enq.captured.Inputs["duration"]; got != 12.0 {
		t.Errorf("inputs.duration = %v, want 12.0", got)
	}
	if got := enq.captured.Inputs["resolution"]; got != "4k" {
		t.Errorf("inputs.resolution = %v, want 4k", got)
	}
	if enq.captured.NodeID != "node-up" {
		t.Errorf("sink 应指向 node-up, got %q", enq.captured.NodeID)
	}
	if len(assetsRepo.setCalls) != 0 {
		t.Errorf("duration 已知不该回填: %v", assetsRepo.setCalls)
	}
}

// U4) 防薅（核心）：请求体注入 duration=1，权威值是 137200ms →
// 入队 inputs.duration 必须是 137.2，证明注入值被丢弃。
func TestUpscale_IgnoresClientDuration(t *testing.T) {
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://long.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"long.mp4": 137200}},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "2k",
		"duration": 1, // 恶意申报
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["duration"]; got != 137.2 {
		t.Errorf("inputs.duration = %v, want 137.2（权威值，不是客户端申报的 1）", got)
	}
}

// U5) 防薅：链式继承——源是「去字幕产物」这类 duration_ms 已继承权威值的 asset，
// 转高清仍按权威秒数计费。钉住 spec 决策 #4「可对去字幕产物再转高清」的安全性。
func TestUpscale_ChainedFromDesubOutput(t *testing.T) {
	// desub 产物 asset：其 duration_ms 由 persistOutputs 从后处理 inputs.duration
	// (服务端权威塞的) 继承而来，故这里就是权威值。
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-desub-out", "media://desubbed.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"desubbed.mp4": 45500}},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-desub-out",
		"modelId": "upscale-hd", "resolution": "2k",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["duration"]; got != 45.5 {
		t.Errorf("链式源 inputs.duration = %v, want 45.5", got)
	}
}

// U6) 未知转高清模型 → 400，不入队。
func TestUpscale_UnknownModel(t *testing.T) {
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "some-other-model", "resolution": "4k",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未知转高清模型")) {
		t.Errorf("body should contain 未知转高清模型, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("unknown model should not enqueue")
	}
}

// U7) 后台未配置转高清模型时，即使 modelId 传空串也不能放行（空串 == 空串的陷阱）。
func TestUpscale_RejectsWhenUnconfigured(t *testing.T) {
	settingsRepo := newPostprocessSettings([]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}}, nil)
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2,
		snapshot: desubSnapshot(t, "node-src", "media://hd.mp4", nil)}
	enq := &desubFakeEnqueuer{taskID: 777}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{}}
	h := NewCanvasPostprocessHandler(settingsRepo, enq, projRepo,
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}}, &desubFakeGen{}, lookup, postprocessNoopSigner{})

	for _, modelID := range []string{"", "upscale-hd"} {
		rec := httptest.NewRecorder()
		h.Upscale(rec, upscaleReq(map[string]any{
			"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src", "modelId": modelID,
		}))
		// modelId 为空时先被必填校验挡掉(400)，非空时被「未知转高清模型」挡掉(400)。
		if rec.Code != http.StatusBadRequest {
			t.Errorf("modelId=%q: want 400, got %d body=%s", modelID, rec.Code, rec.Body)
		}
		if enq.captured != nil {
			t.Errorf("modelId=%q: 未配置时不该入队", modelID)
		}
	}
}

// U8) 源视频未落对象存储 → 400（转高清无限时分支，拿不到签名地址就是没就绪）。
func TestUpscale_SourceNotStored(t *testing.T) {
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "", nil), // content 空 → ObjectKey 认不出
		&desubFakeAssets{},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "4k",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("源视频未就绪")) {
		t.Errorf("body should contain 源视频未就绪, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("source not stored should not enqueue")
	}
}

// U9) 权威时长拿不到 → 400，不入队（不估算兜底）。
func TestUpscale_DurationUnavailable(t *testing.T) {
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://nodur.mp4", nil),
		&desubFakeAssets{},
	)
	h.download = fakeDownload([]byte("x"))
	h.probe = func(_ context.Context, _ string) (int64, error) { return 0, errors.New("ffprobe failed") }

	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "4k",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("无法获取视频时长")) {
		t.Errorf("body should contain 无法获取视频时长, got %s", rec.Body)
	}
	if enq.captured != nil {
		t.Error("no authoritative duration should not enqueue")
	}
}

// U10) 转高清预估：resolution 参与计费 inputs，credits 按权威秒数算。
func TestPostprocessEstimate_UpscaleWithResolution(t *testing.T) {
	h, _, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "4k",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	credits, durationMs := decodeEstimate(t, rec)
	if credits != 12 { // 12.0s × 1 积分/秒
		t.Errorf("credits = %d, want 12", credits)
	}
	if durationMs != 12000 {
		t.Errorf("durationMs = %d, want 12000", durationMs)
	}
}

// U11) 防薅（核心）：预估传一个 uiSchema 里不存在的档位 → 400，且不返回任何 credits。
// 预估不走 generation.Submit，那条 uiSchema 白名单校验不会执行；若放行，分级计费匹配不到
// 任何级会算出便宜价，预估与提交就又是两套口径了。
func TestPostprocessEstimate_RejectsUnknownResolution(t *testing.T) {
	h, _, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "720p", // 不在 2k/4k 里
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未知档位")) {
		t.Errorf("body should contain 未知档位, got %s", rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("credits")) {
		t.Errorf("越界档位不应返回 credits（否则等于给出一个便宜的假价）, got %s", rec.Body)
	}
}

// ── POST /canvas-tasks/whitemodel + 转白模档位 ─────────────────────────────────
//
// 转白模与转高清共用 submitTiered，只差后台配的模型。这里钉住的是「共用」带来的两条新风险：
//   - 端点之间不能串：转白模端点不认转高清模型，反之亦然；
//   - 预估按被问价模型**自己的**档位表校验，不能拿转高清那张表去放行转白模的越界档位。

// whiteModelUISchema 故意和 upscaleUISchema 的档位不重合(720p/1080p vs 2k/4k)，
// 这样「拿错档位表」会直接暴露成测试失败。
const whiteModelUISchema = `{"resolution":{"options":[{"value":"720p","label":"720p"},{"value":"1080p","label":"1080p"}]}}`

// newWhiteModelTestHandler 组装同时配了转高清(upscale-hd)与转白模(white-clay)的 handler。
func newWhiteModelTestHandler(t *testing.T, whiteModel string) (*CanvasPostprocessHandler, *desubFakeEnqueuer) {
	t.Helper()
	cfg := canvasgen.CanvasPostprocessConfig{UpscaleModel: "upscale-hd", WhiteModelModel: whiteModel}
	raw, _ := json.Marshal(cfg)
	settingsRepo := &desubFakeSettings{vals: map[string]string{canvasgen.SettingCanvasPostprocess: string(raw)}}
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2,
		snapshot: desubSnapshot(t, "node-src", "media://clay.mp4", nil)}
	lookup := &postprocessFakeModelLookup{m: map[string]*models.ModelConfig{
		"upscale-hd": {
			ModelID: "upscale-hd", Hidden: true,
			UISchema: json.RawMessage(upscaleUISchema), BillingConfigRaw: json.RawMessage(estimateBillingRaw),
		},
		"white-clay": {
			ModelID: "white-clay", Hidden: true,
			UISchema: json.RawMessage(whiteModelUISchema), BillingConfigRaw: json.RawMessage(estimateBillingRaw),
		},
	}}
	enq := &desubFakeEnqueuer{taskID: 888}
	h := NewCanvasPostprocessHandler(settingsRepo, enq, projRepo,
		&desubFakeAssets{found: map[string]int64{"clay.mp4": 9000}}, &desubFakeGen{}, lookup, postprocessNoopSigner{})
	return h, enq
}

func whiteModelReq(body map[string]any) *http.Request {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/canvas-tasks/whitemodel", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(withUserID(context.Background(), 1))
}

// W1) GetConfig 下发 whiteModelModel + 它自己的档位，且不影响转高清那组。
func TestCanvasPostprocessConfig_WhiteModelResolutions(t *testing.T) {
	h, _ := newWhiteModelTestHandler(t, "white-clay")
	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/canvas/postprocess-config", nil))
	var out struct {
		UpscaleResolutions    []map[string]any `json:"upscaleResolutions"`
		WhiteModelModel       string           `json:"whiteModelModel"`
		WhiteModelResolutions []map[string]any `json:"whiteModelResolutions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WhiteModelModel != "white-clay" {
		t.Errorf("whiteModelModel = %q, want white-clay", out.WhiteModelModel)
	}
	if len(out.WhiteModelResolutions) != 2 || out.WhiteModelResolutions[0]["value"] != "720p" {
		t.Errorf("whiteModelResolutions = %v, want 720p/1080p", out.WhiteModelResolutions)
	}
	if len(out.UpscaleResolutions) != 2 || out.UpscaleResolutions[0]["value"] != "2k" {
		t.Errorf("转高清档位不该受影响, got %v", out.UpscaleResolutions)
	}
}

// W2) 未配转白模 → 字段为空、档位空数组（前端显示「未配置白模模型」）。
func TestCanvasPostprocessConfig_WhiteModelNotConfigured(t *testing.T) {
	h, _ := newWhiteModelTestHandler(t, "")
	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/canvas/postprocess-config", nil))
	var out struct {
		WhiteModelModel       string           `json:"whiteModelModel"`
		WhiteModelResolutions []map[string]any `json:"whiteModelResolutions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.WhiteModelModel != "" || out.WhiteModelResolutions == nil || len(out.WhiteModelResolutions) != 0 {
		t.Errorf("未配置时应为空串 + 空数组, got %q %v", out.WhiteModelModel, out.WhiteModelResolutions)
	}
}

// W3) 转白模成功：inputs 与转高清同构——video(签名地址) + duration(权威秒数) + resolution。
func TestWhiteModel_Success(t *testing.T) {
	h, enq := newWhiteModelTestHandler(t, "white-clay")
	rec := httptest.NewRecorder()
	h.WhiteModel(rec, whiteModelReq(map[string]any{
		"projectId": 2, "nodeId": "node-clay", "sourceNodeId": "node-src",
		"modelId": "white-clay", "resolution": "1080p",
		"duration": 1, // 恶意申报，必须被丢弃
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured == nil {
		t.Fatal("should enqueue")
	}
	if enq.captured.ModelID != "white-clay" || enq.captured.NodeID != "node-clay" {
		t.Errorf("model/node = %q/%q", enq.captured.ModelID, enq.captured.NodeID)
	}
	if got := enq.captured.Inputs["video"]; got != "https://cdn.example.com/clay.mp4" {
		t.Errorf("inputs.video = %v", got)
	}
	if got := enq.captured.Inputs["duration"]; got != 9.0 {
		t.Errorf("inputs.duration = %v, want 9.0（权威值）", got)
	}
	if got := enq.captured.Inputs["resolution"]; got != "1080p" {
		t.Errorf("inputs.resolution = %v, want 1080p", got)
	}
}

// W4) 端点不能串：转白模端点拿转高清模型 → 400「未知转白模模型」；转高清端点拿转白模模型同理。
func TestTieredEndpoints_DoNotAcceptEachOthersModel(t *testing.T) {
	h, enq := newWhiteModelTestHandler(t, "white-clay")

	rec := httptest.NewRecorder()
	h.WhiteModel(rec, whiteModelReq(map[string]any{
		"projectId": 2, "nodeId": "node-clay", "sourceNodeId": "node-src", "modelId": "upscale-hd",
	}))
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("未知转白模模型")) {
		t.Errorf("whitemodel 端点收 upscale 模型: want 400 未知转白模模型, got %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src", "modelId": "white-clay",
	}))
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("未知转高清模型")) {
		t.Errorf("upscale 端点收 whitemodel 模型: want 400 未知转高清模型, got %d %s", rec.Code, rec.Body)
	}
	if enq.captured != nil {
		t.Error("串端点不该入队")
	}
}

// W5) 未配置转白模时空 modelId / 任意 modelId 都不放行。
func TestWhiteModel_RejectsWhenUnconfigured(t *testing.T) {
	h, enq := newWhiteModelTestHandler(t, "")
	for _, modelID := range []string{"", "white-clay"} {
		rec := httptest.NewRecorder()
		h.WhiteModel(rec, whiteModelReq(map[string]any{
			"projectId": 2, "nodeId": "node-clay", "sourceNodeId": "node-src", "modelId": modelID,
		}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("modelId=%q: want 400, got %d body=%s", modelID, rec.Code, rec.Body)
		}
	}
	if enq.captured != nil {
		t.Error("未配置时不该入队")
	}
}

// W6) 预估按被问价模型自己的档位表校验：720p 对转白模合法、对转高清越界。
func TestPostprocessEstimate_WhiteModelUsesOwnResolutionTable(t *testing.T) {
	h, _ := newWhiteModelTestHandler(t, "white-clay")

	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src", "modelId": "white-clay", "resolution": "720p",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("转白模 720p: want 200, got %d body=%s", rec.Code, rec.Body)
	}
	if credits, _ := decodeEstimate(t, rec); credits != 9 { // 9.0s × 1 积分/秒
		t.Errorf("credits = %d, want 9", credits)
	}

	for _, c := range []struct{ model, res string }{
		{"white-clay", "4k"},   // 转高清的档位，不在转白模表里
		{"upscale-hd", "720p"}, // 转白模的档位，不在转高清表里
	} {
		rec = httptest.NewRecorder()
		h.Estimate(rec, estimateReq(map[string]any{
			"projectId": 2, "sourceNodeId": "node-src", "modelId": c.model, "resolution": c.res,
		}))
		if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("未知档位")) {
			t.Errorf("%s + %s: want 400 未知档位, got %d %s", c.model, c.res, rec.Code, rec.Body)
		}
	}
}

// U12) 多模型：选第二个模型提交，入队的就是它（白名单按列表校验，不是只认第一个）。
func TestUpscale_SecondModel(t *testing.T) {
	h, enq, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Upscale(rec, upscaleReq(map[string]any{
		"projectId": 2, "nodeId": "node-up", "sourceNodeId": "node-src",
		"modelId": "upscale-pro", "resolution": "1080p",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured == nil || enq.captured.ModelID != "upscale-pro" {
		t.Fatalf("应按 upscale-pro 入队, got %+v", enq.captured)
	}
}

// U13) 预估的档位白名单按「所选模型」校验：1080p 只属于 upscale-pro，拿去问 upscale-hd 必须拒，
// 问 upscale-pro 则按它自己的单价算。否则换模型后可能用 A 模型的档位套 B 模型的价。
func TestPostprocessEstimate_ResolutionCheckedPerModel(t *testing.T) {
	h, _, _ := newUpscaleTestHandler(
		desubSnapshot(t, "node-src", "media://hd.mp4", nil),
		&desubFakeAssets{found: map[string]int64{"hd.mp4": 12000}},
	)
	rec := httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src",
		"modelId": "upscale-hd", "resolution": "1080p",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upscale-hd 没有 1080p，want 400, got %d body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.Estimate(rec, estimateReq(map[string]any{
		"projectId": 2, "sourceNodeId": "node-src",
		"modelId": "upscale-pro", "resolution": "1080p",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body)
	}
	if credits, _ := decodeEstimate(t, rec); credits != 24 { // 12.0s × 2 积分/秒
		t.Errorf("credits = %d, want 24（upscale-pro 的单价）", credits)
	}
}

// postprocessFakeModelLookup 满足 postprocessModelLookup：按 modelId 返回预置模型(含隐藏)。
// 参考仓叫 httpFakeModelLookup、定义在不移植的 drama_postprocess_handlers_test.go。
type postprocessFakeModelLookup struct {
	m map[string]*models.ModelConfig
}

func (f *postprocessFakeModelLookup) GetByModelID(id string) (*models.ModelConfig, error) {
	if mc, ok := f.m[id]; ok {
		return mc, nil
	}
	return nil, errors.New("model not found")
}

// postprocessNoopSigner 复刻参考仓 newNoopSigner()（noopStorage）的语义：
// 只认 media://，签成 https://cdn.example.com/<key>；认不出原样返回。
type postprocessNoopSigner struct{}

func (postprocessNoopSigner) ObjectKey(s string) string {
	const p = "media://"
	if strings.HasPrefix(s, p) {
		return s[len(p):]
	}
	return ""
}

func (postprocessNoopSigner) Sign(s string) string {
	const p = "media://"
	if strings.HasPrefix(s, p) {
		return "https://cdn.example.com/" + s[len(p):]
	}
	return s
}

// postprocessMP4 构造一个只有 ftyp + moov/mvhd 的最小 mp4，声明时长 durationMs 毫秒。
func postprocessMP4(durationMs uint32) []byte {
	mvhd := make([]byte, 8+100)
	binary.BigEndian.PutUint32(mvhd[0:4], uint32(len(mvhd)))
	copy(mvhd[4:8], "mvhd")
	binary.BigEndian.PutUint32(mvhd[8+12:8+16], 1000)
	binary.BigEndian.PutUint32(mvhd[8+16:8+20], durationMs)
	moov := make([]byte, 8, 8+len(mvhd))
	binary.BigEndian.PutUint32(moov[0:4], uint32(8+len(mvhd)))
	copy(moov[4:8], "moov")
	moov = append(moov, mvhd...)
	ftyp := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}
	return append(ftyp, moov...)
}

// 桌面端没有 ffprobe：默认探测器必须靠纯 Go 读出 mp4 时长并照常提交、回填。
func TestDesub_DefaultProbeReadsMP4WithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assetsRepo := &desubFakeAssets{}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://video9.mp4", nil),
		assetsRepo, nil,
	)
	h.download = fakeDownload(postprocessMP4(7300))
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["duration"]; got != 7.3 {
		t.Errorf("inputs.duration = %v, want 7.3", got)
	}
	if ms := assetsRepo.setCalls["video9.mp4"]; ms != 7300 {
		t.Errorf("应回填 7300ms, got %v", assetsRepo.setCalls)
	}
}

func TestDesub_DefaultProbeRejectsNonMP4(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://clip.webm", nil),
		&desubFakeAssets{}, nil,
	)
	h.download = fakeDownload([]byte{0x1a, 0x45, 0xdf, 0xa3})
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("无法获取视频时长")) {
		t.Fatalf("want 400 无法获取视频时长, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured != nil {
		t.Error("不应 enqueue")
	}
}

// fakeDownload 返回一个固定内容的下载流（替代真实 HTTP 下载）。
func fakeDownload(data []byte) func(context.Context, string) (io.ReadCloser, error) {
	return func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}
