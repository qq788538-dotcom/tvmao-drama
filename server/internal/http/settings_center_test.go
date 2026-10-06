package http

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

func TestScDecodeStrictRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":"x","requestTemplate":{}}`))
	var p ModelPatch
	err := scDecodeStrict(httptest.NewRecorder(), req, &p)
	var ve *SettingsValidationError
	if !errors.As(err, &ve) || ve.Field != "requestTemplate" || !strings.Contains(ve.Message, "不允许修改") {
		t.Fatalf("err=%v", err)
	}
}

func TestScDecodeStrictRejectsTrailingGarbage(t *testing.T) {
	req := httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":"x"} {"name":"y"}`))
	var p ModelPatch
	if err := scDecodeStrict(httptest.NewRecorder(), req, &p); err == nil {
		t.Fatal("want error")
	}
}

func TestScWriteErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
		want string
	}{
		{&SettingsValidationError{Field: "name", Message: "显示名不能为空"}, 400, "显示名不能为空"},
		{ErrSettingsNotFound, 404, "不存在"},
		{errors.New("disk I/O error at /secret/path"), 500, "保存失败"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		scWriteError(rec, c.err)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%v → %d %s", c.err, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "/secret/path") {
			t.Errorf("500 不应回显内部错误: %s", rec.Body.String())
		}
	}
}

func TestModelPatchEmpty(t *testing.T) {
	if !(ModelPatch{}).Empty() {
		t.Fatal("零值应为空")
	}
	n := 1
	if (ModelPatch{Concurrency: &n}).Empty() {
		t.Fatal("有字段时不为空")
	}
}

func TestExtractModelRefs(t *testing.T) {
	refs, err := extractModelRefs("canvas_postprocess_config", []byte(`{"subtitleRemovalModels":[{"modelId":"a"}],"upscaleModels":[{"modelId":"b"}],"upscaleModel":"c","whiteModelModel":""}`))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range refs {
		if r.WantType != "video" {
			t.Errorf("%s 应为 video", r.Path)
		}
		got = append(got, r.Path+"="+r.ModelID)
	}
	want := "subtitleRemovalModels[0].modelId=a,upscaleModels[0].modelId=b,upscaleModel=c"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v", got)
	}
	refs, _ = extractModelRefs("canvas_script_config", []byte(`{"modelCandidates":[{"modelId":"t1"}]}`))
	if len(refs) != 1 || refs[0].WantType != "text" {
		t.Fatalf("script refs=%v", refs)
	}
	refs, _ = extractModelRefs("canvas_multi_angle", []byte(`{"models":["i1","i2"]}`))
	if len(refs) != 2 || refs[1].Path != "models[1]" || refs[1].WantType != "image" {
		t.Fatalf("multi refs=%v", refs)
	}
	if refs, err := extractModelRefs("gen_image_prompt_presets", nil); err != nil || len(refs) != 0 {
		t.Fatalf("空值应无引用: %v %v", refs, err)
	}
	if _, err := extractModelRefs("canvas_multi_angle", []byte(`not json`)); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestSettingsRegistrarsReceiveDepsUnderPrefix(t *testing.T) {
	saved := settingsRouteRegistrars
	t.Cleanup(func() { settingsRouteRegistrars = saved })
	settingsRouteRegistrars = nil
	var got SettingsCenterDeps
	settingsRouteRegistrars = append(settingsRouteRegistrars, func(r chi.Router, d SettingsCenterDeps) {
		got = d
		r.Get("/probe", func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(204) })
	})
	r := chi.NewRouter()
	mountSettingsCenter(r, SettingsCenterDeps{Version: "v"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/settings/probe", nil))
	if rec.Code != 204 || got.Version != "v" {
		t.Fatalf("code=%d deps=%+v", rec.Code, got)
	}
}

// NewRouter 在 SettingsCenter 为零值时仍能工作，并把 Deps.DataDir/Version 透传给设置接口。
func TestNewRouterMountsSettingsCenterBehindAuth(t *testing.T) {
	saved := settingsRouteRegistrars
	t.Cleanup(func() { settingsRouteRegistrars = saved })
	var got SettingsCenterDeps
	settingsRouteRegistrars = []func(chi.Router, SettingsCenterDeps){func(r chi.Router, d SettingsCenterDeps) {
		got = d
		r.Get("/probe", func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(204) })
	}}
	h := NewRouter(Deps{Auth: localauth.Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}}, DataDir: "/data", Version: "1.2.3"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/settings/probe", nil))
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("无 token 应 401，got %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/settings/probe", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 || got.DataDir != "/data" || got.Version != "1.2.3" {
		t.Fatalf("code=%d deps=%+v", rec.Code, got)
	}
}
