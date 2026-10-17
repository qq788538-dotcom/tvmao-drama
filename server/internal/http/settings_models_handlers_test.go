package http

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type modelViewResp struct {
	ID              int64          `json:"id"`
	ModelID         string         `json:"modelId"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	RequestTemplate map[string]any `json:"requestTemplate"`
	ModifiedFields  []string       `json:"modifiedFields"`
	References      []string       `json:"references"`
}

func TestModelsListHidesBillingAndShowsReferences(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/models", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, banned := range []string{"billingConfig", "priceNote", "adminNote", "10积分", "内部备注"} {
		if strings.Contains(body, banned) {
			t.Errorf("响应不应包含 %q", banned)
		}
	}
	list := decodeTo[struct{ Data []modelViewResp }](t, body).Data
	if len(list) != 4 {
		t.Fatalf("应返回含停用模型在内的全部 4 个，got %d", len(list))
	}
	refs := map[string][]string{}
	byModel := map[string]modelViewResp{}
	for _, m := range list {
		refs[m.ModelID] = m.References
		byModel[m.ModelID] = m
	}
	if got := strings.Join(refs["gpt-image-2"], ","); got != "绘图预设,多角度" {
		t.Errorf("gpt-image-2 引用=%q", got)
	}
	if got := strings.Join(refs["canvas-gpt-5.5"], ","); got != "剧本生成分镜,Agent · 主Agent" {
		t.Errorf("canvas-gpt-5.5 引用=%q", got)
	}
	if byModel["gpt-image-2"].RequestTemplate["model"] != "gpt-image-2" {
		t.Errorf("技术字段应原样只读返回: %+v", byModel["gpt-image-2"])
	}
}

func TestPatchModelForwardsAllowedFields(t *testing.T) {
	e := newSCEnv(t)
	id := e.ids["old-text"]
	code, body := e.do("PATCH", fmt.Sprintf("/models/%d", id), map[string]any{"name": "  新名字 ", "sortOrder": 5, "concurrency": 2, "canvasMaxRetries": 3, "canvasRetryIntervalSec": 10})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	v := decodeTo[modelViewResp](t, body)
	if v.Name != "新名字" || strings.Join(v.ModifiedFields, ",") != "name,sortOrder" {
		t.Fatalf("%+v", v)
	}
	p := e.modelEdit.lastPatch
	if *p.Concurrency != 2 || *p.CanvasMaxRetries != 3 || *p.CanvasRetryIntervalSec != 10 {
		t.Fatalf("patch 未完整转发: %+v", p)
	}
}

func TestPatchModelRejectsTechnicalFieldsAndBadValues(t *testing.T) {
	e := newSCEnv(t)
	path := fmt.Sprintf("/models/%d", e.ids["gpt-image-2"])
	for _, body := range []any{
		map[string]any{"requestTemplate": map[string]any{}},
		map[string]any{"basePath": "/x"},
		map[string]any{"billingConfig": map[string]any{}},
		map[string]any{},
		map[string]any{"name": "   "},
		map[string]any{"name": strings.Repeat("长", 65)},
		map[string]any{"concurrency": -1},
		map[string]any{"concurrency": 201},
		map[string]any{"canvasMaxRetries": 11},
		map[string]any{"canvasRetryIntervalSec": 601},
		map[string]any{"uiSchemaDefaults": map[string]any{"ratio": "4:3"}},
		map[string]any{"uiSchemaDefaults": map[string]any{"nope": "1:1"}},
		map[string]any{"uiSchemaDefaults": map[string]any{"n": "1"}},
	} {
		if code, resp := e.do("PATCH", path, body); code != 400 {
			t.Errorf("%v → %d %s", body, code, resp)
		}
	}
	if e.modelEdit.lastPatch != nil {
		t.Fatal("被拒的请求不应到达编辑服务")
	}
}

func TestPatchModelAcceptsValidUISchemaDefault(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("PATCH", fmt.Sprintf("/models/%d", e.ids["gpt-image-2"]), map[string]any{"uiSchemaDefaults": map[string]any{"ratio": "16:9", "n": 2}})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if string(e.modelEdit.lastPatch.UISchemaDefaults["n"]) != "2" {
		t.Fatalf("%s", e.modelEdit.lastPatch.UISchemaDefaults["n"])
	}
}

func TestPatchRejectsDisablingReferencedModel(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("PATCH", fmt.Sprintf("/models/%d", e.ids["canvas-gpt-5.5"]), map[string]any{"enabled": false})
	if code != 400 || !strings.Contains(body, "剧本生成分镜") || !strings.Contains(body, "Agent · 主Agent") {
		t.Fatalf("%d %s", code, body)
	}
	// 隐藏不影响引用方，允许。
	if code, body := e.do("PATCH", fmt.Sprintf("/models/%d", e.ids["canvas-gpt-5.5"]), map[string]any{"hidden": false}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
}

func TestPatchModelNotFoundAndServiceError(t *testing.T) {
	e := newSCEnv(t)
	if code, _ := e.do("PATCH", "/models/99999", map[string]any{"name": "x"}); code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
	if code, _ := e.do("PATCH", "/models/abc", map[string]any{"name": "x"}); code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
	e.modelEdit.err = &SettingsValidationError{Field: "name", Message: "策略拒绝"}
	if code, body := e.do("PATCH", fmt.Sprintf("/models/%d", e.ids["old-text"]), map[string]any{"name": "x"}); code != 400 || !strings.Contains(body, "策略拒绝") {
		t.Fatalf("%d %s", code, body)
	}
	e.modelEdit.err = errors.New("boom")
	if code, _ := e.do("PATCH", fmt.Sprintf("/models/%d", e.ids["old-text"]), map[string]any{"name": "x"}); code != 500 {
		t.Fatalf("want 500, got %d", code)
	}
}

func TestRestoreModelDefault(t *testing.T) {
	e := newSCEnv(t)
	id := e.ids["old-text"]
	e.do("PATCH", fmt.Sprintf("/models/%d", id), map[string]any{"name": "改过"})
	code, body := e.do("POST", fmt.Sprintf("/models/%d/restore-default", id), nil)
	v := decodeTo[modelViewResp](t, body)
	if code != 200 || v.Name != "旧文本模型" || len(v.ModifiedFields) != 0 {
		t.Fatalf("%d %s", code, body)
	}
}
