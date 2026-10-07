package cliapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

func TestToModelNeverLeaksUpstreamSecrets(t *testing.T) {
	m := models.ModelConfig{
		ModelID: "doubao-image", Name: "豆包绘图", Type: "image", Enabled: true,
		BasePath:        "/v1/images/generations",
		AdminNote:       "内部备注：这个模型贵",
		RequestTemplate: json.RawMessage(`{"model":"doubao","key":"sk-secret"}`),
		PollingTemplate: json.RawMessage(`{"url":"https://upstream/poll"}`),
		UISchema:        json.RawMessage(`{"ratio":{"label":"比例","default":"16:9","options":[{"value":"16:9","label":"宽屏"}]}}`),
	}
	blob, err := json.Marshal(ToModel(m))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{
		"basePath", "requestTemplate", "pollingTemplate", "adminNote",
		"/v1/images/generations", "sk-secret", "内部备注", "upstream",
	} {
		if strings.Contains(string(blob), leak) {
			t.Fatalf("公共模型 DTO 泄漏 %q: %s", leak, blob)
		}
	}
}

func TestToModelUISchemaBecomesJSONSchema(t *testing.T) {
	m := models.ModelConfig{
		ModelID: "m1", Name: "模型一", Type: "video", Enabled: true,
		UISchema: json.RawMessage(`{
			"ratio":      {"label":"比例","default":"16:9","options":[{"value":"16:9","label":"宽屏"},{"value":"1:1","label":"方"}]},
			"duration":   {"label":"时长","default":5,"options":[{"value":5,"label":"5秒"},{"value":10,"label":"10秒"}]},
			"style":      {"label":"风格","options":[]}
		}`),
	}
	got := ToModel(m)

	if got.Kind != "video" {
		t.Fatalf("kind = %s", got.Kind)
	}
	props := got.InputSchema.Properties

	ratio := props["ratio"]
	if ratio.Type != "string" || len(ratio.Enum) != 2 || ratio.Enum[0] != "16:9" || ratio.Default != "16:9" {
		t.Fatalf("ratio = %+v", ratio)
	}
	// 数字选项要保持数字类型——CLI 按 schema 决定 --param duration=5 是数字还是字符串。
	duration := props["duration"]
	if duration.Type != "number" || len(duration.Enum) != 2 {
		t.Fatalf("duration = %+v", duration)
	}
	// 无 options 无 default 的字段退化成自由字符串，而不是消失。
	if props["style"].Type != "string" {
		t.Fatalf("style = %+v", props["style"])
	}
	// prompt 不在 uiSchema 里（它是画布固有输入），必须由服务端注入并列入 required，
	// 否则 Agent 读 schema 会以为不用传提示词。
	if props["prompt"].Type != "string" {
		t.Fatalf("prompt 必须被注入: %+v", props)
	}
	found := false
	for _, r := range got.InputSchema.Required {
		if r == "prompt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("prompt 必须在 required 里: %+v", got.InputSchema.Required)
	}
	if got.InputSchema.Type != "object" {
		t.Fatalf("schema type = %s", got.InputSchema.Type)
	}
}

func TestToModelEmptySchemaStillHasPrompt(t *testing.T) {
	got := ToModel(models.ModelConfig{ModelID: "m", Type: "image", Enabled: true})
	if got.InputSchema.Properties["prompt"].Type != "string" {
		t.Fatalf("空 uiSchema 也必须有 prompt: %+v", got.InputSchema)
	}
}

func TestToModelAvailabilityFollowsEnabled(t *testing.T) {
	if !ToModel(models.ModelConfig{ModelID: "m", Enabled: true}).Available {
		t.Fatal("enabled 模型应 available")
	}
	off := ToModel(models.ModelConfig{ModelID: "m", Enabled: false})
	if off.Available {
		t.Fatal("disabled 模型不应 available")
	}
	// 不可用必须给原因——否则用户只能提交后才发现。
	if off.UnavailableReason == "" {
		t.Fatal("不可用模型必须给出原因")
	}
}

func TestResolveModelRef(t *testing.T) {
	list := []models.ModelConfig{
		{ModelID: "a", Name: "同名", Enabled: true},
		{ModelID: "b", Name: "同名", Enabled: true},
		{ModelID: "c", Name: "唯一", Enabled: true},
	}

	if m, err := ResolveModelRef(list, "c"); err != nil || m.ModelID != "c" {
		t.Fatalf("ID 精确匹配失败: %v %v", m, err)
	}
	if m, err := ResolveModelRef(list, "唯一"); err != nil || m.ModelID != "c" {
		t.Fatalf("名称精确匹配失败: %v %v", m, err)
	}

	var ce *Error
	_, err := ResolveModelRef(list, "同名")
	if !errors.As(err, &ce) || ce.Code != CodeModelRefAmbiguous {
		t.Fatalf("歧义应报 MODEL_REF_AMBIGUOUS, got %v", err)
	}
	// 歧义必须带候选，否则用户不知道该改成什么。
	if ce.Details["candidates"] == nil {
		t.Fatalf("歧义错误必须带 candidates: %+v", ce.Details)
	}

	_, err = ResolveModelRef(list, "不存在")
	if !errors.As(err, &ce) || ce.Code != CodeModelNotFound {
		t.Fatalf("got %v", err)
	}
	_, err = ResolveModelRef(list, "")
	if !errors.As(err, &ce) || ce.Code != CodeInvalidArgument {
		t.Fatalf("空 ref 应报 INVALID_ARGUMENT, got %v", err)
	}
}

// ID 与名称撞车时 ID 优先——稳定 ID 才是权威值。
func TestResolveModelRefIDBeatsName(t *testing.T) {
	list := []models.ModelConfig{
		{ModelID: "x", Name: "别人的名字", Enabled: true},
		{ModelID: "y", Name: "x", Enabled: true},
	}
	m, err := ResolveModelRef(list, "x")
	if err != nil || m.ModelID != "x" {
		t.Fatalf("ID 应优先于名称: %+v %v", m, err)
	}
}

func TestFilterModels(t *testing.T) {
	list := []models.ModelConfig{
		{ModelID: "img-1", Name: "豆包绘图", Type: "image"},
		{ModelID: "vid-wan", Name: "Wan 视频", Type: "video"},
		{ModelID: "vid-seed", Name: "Seedance 2.0 VIP", Type: "video"},
	}
	if got := FilterModels(list, "video", ""); len(got) != 2 {
		t.Fatalf("type 过滤 = %d", len(got))
	}
	// 搜索对 modelId 与 name 都生效，且大小写不敏感——用户记得住哪个是哪个。
	if got := FilterModels(list, "video", "wan"); len(got) != 1 || got[0].ModelID != "vid-wan" {
		t.Fatalf("按 name 搜 = %+v", got)
	}
	if got := FilterModels(list, "", "SEED"); len(got) != 1 || got[0].ModelID != "vid-seed" {
		t.Fatalf("大小写不敏感搜索 = %+v", got)
	}
	if got := FilterModels(list, "", ""); len(got) != 3 {
		t.Fatalf("空过滤 = %d", len(got))
	}
}
