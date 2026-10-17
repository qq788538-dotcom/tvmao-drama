package http

import (
	"bytes"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

type upstreamViewResp struct {
	Configured   bool   `json:"configured"`
	BaseURL      string `json:"baseUrl"`
	APIKeyMasked string `json:"apiKeyMasked"`
}

type modelUpstreamResp struct {
	ID             int64            `json:"id"`
	ModelID        string           `json:"modelId"`
	Name           string           `json:"name"`
	ModifiedFields []string         `json:"modifiedFields"`
	Upstream       upstreamViewResp `json:"upstream"`
}

const secretKey = "sk-secret-abcdefgh1234"

// captureLog 收集测试期间的标准日志输出，用于断言 Key 明文不进日志。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func (e *scEnv) upstreamOf(modelID string) (string, string) {
	e.t.Helper()
	m, err := e.models.GetByModelID(modelID)
	if err != nil {
		e.t.Fatal(err)
	}
	return m.UpstreamBaseURL, m.UpstreamAPIKey
}

func TestModelsListUpstreamDefaultsToUnconfigured(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/models", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, m := range decodeTo[struct{ Data []modelUpstreamResp }](t, body).Data {
		if m.Upstream != (upstreamViewResp{}) {
			t.Fatalf("%s 未配置时应为 {false,\"\",\"\"}: %+v", m.ModelID, m.Upstream)
		}
	}
	if !strings.Contains(body, `"upstream":{"configured":false,"baseUrl":"","apiKeyMasked":""}`) {
		t.Fatalf("未配置时 upstream 字段形状不对: %s", body)
	}
}

func TestPatchModelUpstreamSetMaskAndList(t *testing.T) {
	e := newSCEnv(t)
	logs := captureLog(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": " https://own.example/api/ ", "apiKey": " " + secretKey + " "}})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	v := decodeTo[modelUpstreamResp](t, body)
	if !v.Upstream.Configured || v.Upstream.BaseURL != "https://own.example/api" || v.Upstream.APIKeyMasked != "sk-••••••••1234" {
		t.Fatalf("回显不对: %+v", v.Upstream)
	}
	if base, key := e.upstreamOf("gpt-image-2"); base != "https://own.example/api" || key != secretKey {
		t.Fatalf("落库不对: %q %q", base, key)
	}
	// 独立上游不属于 user_overrides 机制，不应进 modifiedFields，也不应经编辑服务。
	if len(v.ModifiedFields) != 0 || e.modelEdit.lastPatch != nil {
		t.Fatalf("只改 upstream 不应走内置字段编辑: modified=%v patch=%+v", v.ModifiedFields, e.modelEdit.lastPatch)
	}
	code, list := e.do("GET", "/models", nil)
	if code != 200 || !strings.Contains(list, `"upstream":{"configured":true,"baseUrl":"https://own.example/api","apiKeyMasked":"sk-••••••••1234"}`) {
		t.Fatalf("列表应带掩码后的独立上游: %s", list)
	}
	for _, s := range []string{body, list, logs.String()} {
		if strings.Contains(s, secretKey) || strings.Contains(s, "abcdefgh") {
			t.Fatalf("Key 明文泄露: %s", s)
		}
	}
}

func TestPatchModelUpstreamKeepsKeyWhenOnlyBaseChanges(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	if code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, up := range []map[string]any{
		{"baseUrl": "https://b.example"},
		{"baseUrl": "https://c.example", "apiKey": ""},
	} {
		code, body := e.do("PATCH", path, map[string]any{"upstream": up})
		if code != 200 {
			t.Fatalf("%v → %d %s", up, code, body)
		}
		if base, key := e.upstreamOf("gpt-image-2"); base != up["baseUrl"] || key != secretKey {
			t.Fatalf("%v：只改地址应保留原 Key, got %q %q", up, base, key)
		}
	}
	// 换 Key。
	if code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "https://c.example", "apiKey": "sk-new-key-0000000"}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if _, key := e.upstreamOf("gpt-image-2"); key != "sk-new-key-0000000" {
		t.Fatalf("Key 未更新: %q", key)
	}
}

func TestPatchModelUpstreamRequiresBoth(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	const pairMsg = "单独配置上游时，地址和 API Key 都必须填写"
	for _, up := range []map[string]any{
		{"baseUrl": "https://a.example"},               // 原来没 Key
		{"baseUrl": "https://a.example", "apiKey": ""}, // 原来没 Key
		{"baseUrl": "", "apiKey": secretKey},
		{"apiKey": secretKey},
	} {
		code, body := e.do("PATCH", path, map[string]any{"upstream": up})
		if code != 400 || !strings.Contains(body, pairMsg) {
			t.Errorf("%v → %d %s", up, code, body)
		}
	}
	// 已有 Key 时清空地址也不行。
	if code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "  "}}); code != 400 || !strings.Contains(body, pairMsg) {
		t.Fatalf("%d %s", code, body)
	}
	if base, key := e.upstreamOf("gpt-image-2"); base != "https://a.example" || key != secretKey {
		t.Fatalf("被拒的请求不应改库: %q %q", base, key)
	}
}

func TestPatchModelUpstreamValidation(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	for _, body := range []any{
		map[string]any{"upstream": map[string]any{"baseUrl": "ftp://a.example", "apiKey": secretKey}},
		map[string]any{"upstream": map[string]any{"baseUrl": "a.example", "apiKey": secretKey}},
		map[string]any{"upstream": map[string]any{"baseUrl": "https://u:p@a.example", "apiKey": secretKey}},
		map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": "sk with space"}},
		map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey, "extra": 1}},
		map[string]any{"upstream": "https://a.example"},
		map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey}, "basePath": "/x"},
	} {
		code, resp := e.do("PATCH", path, body)
		if code != 400 {
			t.Errorf("%v → %d %s", body, code, resp)
		}
		if strings.Contains(resp, secretKey) {
			t.Errorf("错误响应不得回显 Key: %s", resp)
		}
	}
	if base, key := e.upstreamOf("gpt-image-2"); base != "" || key != "" {
		t.Fatalf("被拒的请求不应改库: %q %q", base, key)
	}
}

func TestPatchModelUpstreamNullClears(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	if code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	code, body := e.do("PATCH", path, `{"upstream":null}`)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if v := decodeTo[modelUpstreamResp](t, body); v.Upstream != (upstreamViewResp{}) {
		t.Fatalf("清空后应为未配置: %+v", v.Upstream)
	}
	if base, key := e.upstreamOf("gpt-image-2"); base != "" || key != "" {
		t.Fatalf("清空失败: %q %q", base, key)
	}
}

// 不带 upstream 字段：独立上游不变；与内置字段一起提交时两边都生效。
func TestPatchModelWithoutUpstreamLeavesItAndCombined(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["old-text"])
	if code, body := e.do("PATCH", path, map[string]any{"name": "改名", "upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": secretKey}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if e.modelEdit.lastPatch == nil || *e.modelEdit.lastPatch.Name != "改名" {
		t.Fatal("内置字段应照常转发给编辑服务")
	}
	code, body := e.do("PATCH", path, map[string]any{"sortOrder": 9})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	v := decodeTo[modelUpstreamResp](t, body)
	if v.Name != "改名" || !v.Upstream.Configured {
		t.Fatalf("%+v", v)
	}
	if base, key := e.upstreamOf("old-text"); base != "https://a.example" || key != secretKey {
		t.Fatalf("不带 upstream 不应改动: %q %q", base, key)
	}
	// 内置字段校验失败时，upstream 也不写。
	if code, _ := e.do("PATCH", path, map[string]any{"name": " ", "upstream": nil}); code != 400 {
		t.Fatalf("应 400, got %d", code)
	}
	if _, key := e.upstreamOf("old-text"); key != secretKey {
		t.Fatal("校验失败的请求不应清空上游")
	}
}

func TestMaskAPIKeyShortKeyFullyMasked(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": "https://a.example", "apiKey": "short-key"}})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if v := decodeTo[modelUpstreamResp](t, body); v.Upstream.APIKeyMasked != "••••••••" || strings.Contains(body, "short-key") {
		t.Fatalf("不足 12 位应全掩: %s", body)
	}
}

// 独立上游地址保存时去掉末尾的 / 和 /v1。
func TestPatchModelUpstreamStripsTrailingV1(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	for in, want := range map[string]string{
		"https://own.example/v1":      "https://own.example",
		"https://own.example/v1/":     "https://own.example",
		"https://own.example/api/v1/": "https://own.example/api",
	} {
		code, body := e.do("PATCH", path, map[string]any{"upstream": map[string]any{"baseUrl": in, "apiKey": secretKey}})
		if code != 200 {
			t.Fatalf("%s → %d %s", in, code, body)
		}
		if base, _ := e.upstreamOf("gpt-image-2"); base != want {
			t.Fatalf("%s 落库为 %q, want %q", in, base, want)
		}
	}
}

// SetUpstream 的 ErrUpstreamUnpaired 是校验错误，应返回 400 而不是 500。
func TestScWriteErrorUpstreamUnpairedIs400(t *testing.T) {
	rec := httptest.NewRecorder()
	scWriteError(rec, fmt.Errorf("wrap: %w", models.ErrUpstreamUnpaired))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"upstream"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
