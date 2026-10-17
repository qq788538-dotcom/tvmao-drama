package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

type captureGenSvc struct {
	submitTask *generation.SubmitResult
	submitErr  error
	capture    *generation.SubmitInput
}

func (s *captureGenSvc) Submit(_ context.Context, in generation.SubmitInput) (*generation.SubmitResult, error) {
	if s.capture != nil {
		*s.capture = in
	}
	return s.submitTask, s.submitErr
}

func (s *captureGenSvc) Get(context.Context, int64, int64) (*generation.Task, error) {
	return nil, errors.New("not used")
}

type stubGenSvc struct {
	submitTask *generation.SubmitResult
	submitErr  error
	getResults []*generation.Task // Get() 依次返回的序列
	getIdx     int
}

func (s *stubGenSvc) Submit(ctx context.Context, in generation.SubmitInput) (*generation.SubmitResult, error) {
	return s.submitTask, s.submitErr
}

func (s *stubGenSvc) Get(ctx context.Context, userID, taskID int64) (*generation.Task, error) {
	if s.getIdx >= len(s.getResults) {
		return nil, errors.New("no more stub results")
	}
	t := s.getResults[s.getIdx]
	s.getIdx++
	return t, nil
}

func TestDynamicTool_SyncSuccess(t *testing.T) {
	gs := &stubGenSvc{
		submitTask: &generation.SubmitResult{
			TaskID: 1, Status: "succeeded", OutputURLs: []string{"http://x/out.png"},
		},
	}
	tool := &DynamicTool{
		ToolName: "test_image", ModelID: "test-model", Description: "test",
		ParamsSchema: map[string]ParameterSpec{"prompt": {Type: "string", Required: true}},
		GenService:   gs, PollInterval: 10 * time.Millisecond,
	}

	var arts []Artifact
	ctx := WithEmit(WithUserID(WithProjectID(context.Background(), 1), 1), func(a Artifact) { arts = append(arts, a) })
	args, _ := json.Marshal(map[string]any{"prompt": "test"})

	res, err := tool.InvokableRun(ctx, string(args))
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact, got %d", len(arts))
	}
	if arts[0].URL != "http://x/out.png" {
		t.Fatalf("url: %s", arts[0].URL)
	}
	if !contains(res, "out.png") {
		t.Fatalf("result: %s", res)
	}
}

func TestDynamicTool_AsyncPollUntilSuccess(t *testing.T) {
	gs := &stubGenSvc{
		submitTask: &generation.SubmitResult{TaskID: 99, Status: "processing"},
		getResults: []*generation.Task{
			{ID: 99, Status: "processing"},
			{ID: 99, Status: "processing"},
			{ID: 99, Status: "succeeded", Output: json.RawMessage(`{"outputUrls":["http://x/r.mp4"]}`)},
		},
	}
	tool := &DynamicTool{
		ToolName: "test_video", ModelID: "test-vid", Description: "video",
		GenService: gs, PollInterval: 10 * time.Millisecond,
	}

	var arts []Artifact
	ctx := WithEmit(WithUserID(WithProjectID(context.Background(), 1), 1), func(a Artifact) { arts = append(arts, a) })
	args, _ := json.Marshal(map[string]any{"prompt": "x"})

	res, err := tool.InvokableRun(ctx, string(args))
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].URL != "http://x/r.mp4" {
		t.Fatalf("artifact mismatch: %+v", arts)
	}
	if !contains(res, "r.mp4") {
		t.Fatalf("result: %s", res)
	}
}

func TestDynamicTool_AsyncPollTimeout(t *testing.T) {
	gs := &stubGenSvc{
		submitTask: &generation.SubmitResult{TaskID: 99, Status: "processing"},
		getResults: []*generation.Task{},
	}
	tool := &DynamicTool{
		ToolName: "slow", ModelID: "m", GenService: gs,
		PollInterval: 10 * time.Millisecond,
		MaxWait:      30 * time.Millisecond,
	}
	ctx := WithUserID(WithProjectID(context.Background(), 1), 1)
	args, _ := json.Marshal(map[string]any{})

	_, err := tool.InvokableRun(ctx, string(args))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDynamicTool_ArrayParamSchema(t *testing.T) {
	tool := &DynamicTool{
		ToolName: "video_gen", ModelID: "m", Description: "d",
		ParamsSchema: map[string]ParameterSpec{
			"prompt": {Type: "string", Required: true},
			"images": {
				Type:  "array",
				Desc:  "reference image URLs",
				Items: &ParameterSpec{Type: "string"},
			},
			"videos": {Type: "array", Desc: "reference videos"}, // Items 省略 → 默认为 string
		},
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(js)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	// 两个 array 字段都必须声明 items.type=string，这样 LLM 才会输出正确的形态。
	if !contains(out, `"images"`) || !contains(out, `"type":"array"`) {
		t.Fatalf("expected array schema in %s", out)
	}
	if !contains(out, `"items":{"type":"string"`) {
		t.Fatalf("expected items.type=string in %s", out)
	}
}

func TestDynamicTool_ArrayInputsForwarded(t *testing.T) {
	var captured generation.SubmitInput
	gs := &captureGenSvc{
		submitTask: &generation.SubmitResult{TaskID: 1, Status: "succeeded", OutputURLs: []string{"http://x/v.mp4"}},
		capture:    &captured,
	}
	tool := &DynamicTool{
		ToolName: "video_gen", ModelID: "m",
		ParamsSchema: map[string]ParameterSpec{
			"prompt": {Type: "string", Required: true},
			"images": {Type: "array", Items: &ParameterSpec{Type: "string"}},
		},
		DefaultParams: map[string]any{"videos": []any{}, "audios": []any{}},
		GenService:    gs,
	}
	ctx := WithEmit(WithUserID(WithProjectID(context.Background(), 1), 1), func(Artifact) {})
	args, _ := json.Marshal(map[string]any{
		"prompt": "a dog runs",
		"images": []string{"media://a.png", "media://b.png"},
	})
	if _, err := tool.InvokableRun(ctx, string(args)); err != nil {
		t.Fatal(err)
	}
	imgs, ok := captured.Inputs["images"].([]any)
	if !ok || len(imgs) != 2 {
		t.Fatalf("images not forwarded as array, got %T: %v", captured.Inputs["images"], captured.Inputs["images"])
	}
	// default_params 必须垫在 LLM args 下面，这样 videos/audios 会以空数组的形式
	// 出现，供 InterpolateTemplate 的 $map 运算符使用。
	if _, ok := captured.Inputs["videos"].([]any); !ok {
		t.Fatalf("default videos not merged: %v", captured.Inputs["videos"])
	}
}

func TestDynamicTool_FailedTask(t *testing.T) {
	gs := &stubGenSvc{
		submitTask: &generation.SubmitResult{TaskID: 1, Status: "failed"},
	}
	tool := &DynamicTool{ToolName: "t", ModelID: "m", GenService: gs}
	ctx := WithUserID(WithProjectID(context.Background(), 1), 1)
	args, _ := json.Marshal(map[string]any{})

	_, err := tool.InvokableRun(ctx, string(args))
	if err == nil {
		t.Fatal("expected error for failed task")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
