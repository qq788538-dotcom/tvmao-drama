package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/cliapi"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

type cliFakeModelRepo struct{ list []models.ModelConfig }

func (r *cliFakeModelRepo) List(bool) ([]models.ModelConfig, error) { return r.list, nil }
func (r *cliFakeModelRepo) GetByModelID(id string) (*models.ModelConfig, error) {
	for i := range r.list {
		if r.list[i].ModelID == id {
			return &r.list[i], nil
		}
	}
	return nil, models.ErrNotFound
}

func cliModelTestEnv(t *testing.T, list []models.ModelConfig) (http.Handler, string) {
	t.Helper()
	router := NewRouter(Deps{
		Auth:     cliTestAuth(),
		CLIModel: NewCLIModelHandler(&cliFakeModelRepo{list: list}),
	})
	tok := testCLIToken
	return router, tok
}

var cliTestModels = []models.ModelConfig{
	{
		ModelID: "doubao-image", Name: "豆包绘图", Type: "image", Enabled: true,
		BasePath: "/v1/images", AdminNote: "内部备注",
		RequestTemplate:  json.RawMessage(`{"key":"sk-secret"}`),
		UISchema:         json.RawMessage(`{"ratio":{"label":"比例","default":"1:1","options":[{"value":"1:1","label":"方"},{"value":"16:9","label":"宽"}]}}`),
		BillingConfigRaw: json.RawMessage(`{"flatFee":7}`),
	},
	{ModelID: "wan-video", Name: "Wan 视频", Type: "video", Enabled: true},
	{ModelID: "seed-video", Name: "Seedance 2.0 VIP", Type: "video", Enabled: true},
}

func doCLI(t *testing.T, router http.Handler, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCLIModelListFiltersAndSearches(t *testing.T) {
	router, tok := cliModelTestEnv(t, cliTestModels)

	var got struct {
		Data []cliapi.Model `json:"data"`
	}
	rec := doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models?type=video", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Data) != 2 {
		t.Fatalf("video 模型 = %d", len(got.Data))
	}

	rec = doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models?type=video&q=seed", "")
	got.Data = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Data) != 1 || got.Data[0].ModelID != "seed-video" {
		t.Fatalf("搜索结果 = %+v", got.Data)
	}
}

// 公共模型响应绝不能带上游细节——这是「CLI 不接触模型供应商配置」的落点。
func TestCLIModelListDoesNotLeakUpstream(t *testing.T) {
	router, tok := cliModelTestEnv(t, cliTestModels)
	body := doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models", "").Body.String()
	for _, leak := range []string{"sk-secret", "/v1/images", "内部备注", "requestTemplate", "basePath"} {
		if strings.Contains(body, leak) {
			t.Fatalf("泄漏 %q: %s", leak, body)
		}
	}
}

func TestCLIModelGetByIDAndName(t *testing.T) {
	router, tok := cliModelTestEnv(t, cliTestModels)

	for _, ref := range []string{"seed-video", "Seedance%202.0%20VIP"} {
		rec := doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models/"+ref, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("ref %q status = %d body=%s", ref, rec.Code, rec.Body.String())
		}
		var m cliapi.Model
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		if m.ModelID != "seed-video" {
			t.Fatalf("ref %q → %s", ref, m.ModelID)
		}
	}

	rec := doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models/nope", "")
	if rec.Code != http.StatusNotFound || decodeCLIError(t, rec).Code != cliapi.CodeModelNotFound {
		t.Fatalf("缺失模型 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCLIModelRefAmbiguousReturnsCandidates(t *testing.T) {
	router, tok := cliModelTestEnv(t, []models.ModelConfig{
		{ModelID: "a", Name: "同名", Type: "image", Enabled: true},
		{ModelID: "b", Name: "同名", Type: "image", Enabled: true},
	})
	rec := doCLI(t, router, tok, http.MethodGet, "/api/v1/cli/models/%E5%90%8C%E5%90%8D", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeCLIError(t, rec)
	if body.Code != cliapi.CodeModelRefAmbiguous || body.Details["candidates"] == nil {
		t.Fatalf("歧义响应 = %+v", body)
	}
}

func TestCLIModelRoutesRequireCLIToken(t *testing.T) {
	router, _ := cliModelTestEnv(t, cliTestModels)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/cli/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭据应 401，got %d", rec.Code)
	}
}
