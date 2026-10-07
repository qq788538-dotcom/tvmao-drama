package http

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// fakeSettingEdit 模拟 P2a 的编辑策略服务：直接写 settings，记录改过的 key。
type fakeSettingEdit struct {
	repo     settings.Repository
	defaults map[string]string
	modified map[string]bool
	reject   map[string]error
}

func (f *fakeSettingEdit) UpdateSetting(_ context.Context, key, value string) error {
	if err := f.reject[key]; err != nil {
		return err
	}
	f.modified[key] = true
	return f.repo.Set(key, value)
}

func (f *fakeSettingEdit) RestoreSettingDefault(_ context.Context, key string) error {
	d, ok := f.defaults[key]
	if !ok {
		return ErrSettingsNotFound
	}
	delete(f.modified, key)
	return f.repo.Set(key, d)
}

func (f *fakeSettingEdit) ModifiedSettings(context.Context) ([]string, error) {
	out := []string{}
	for k := range f.modified {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// fakeModelEdit 只应用 name/enabled/hidden/sortOrder 四个字段，足够验证 handler 的转发与回显。
type fakeModelEdit struct {
	repo      models.Repository
	defaults  map[int64]models.ModelConfig
	modified  map[int64][]string
	lastPatch *ModelPatch
	err       error
}

func (f *fakeModelEdit) UpdateModel(_ context.Context, id int64, p ModelPatch) error {
	if f.err != nil {
		return f.err
	}
	m, err := f.repo.GetByID(id)
	if err != nil {
		return err
	}
	cp := p
	f.lastPatch = &cp
	if p.Name != nil {
		m.Name = *p.Name
		f.modified[id] = append(f.modified[id], "name")
	}
	if p.Enabled != nil {
		m.Enabled = *p.Enabled
		f.modified[id] = append(f.modified[id], "enabled")
	}
	if p.Hidden != nil {
		m.Hidden = *p.Hidden
		f.modified[id] = append(f.modified[id], "hidden")
	}
	if p.SortOrder != nil {
		m.SortOrder = *p.SortOrder
		f.modified[id] = append(f.modified[id], "sortOrder")
	}
	_, err = f.repo.Update(id, *m)
	return err
}

func (f *fakeModelEdit) RestoreModelDefault(_ context.Context, id int64) error {
	d, ok := f.defaults[id]
	if !ok {
		return ErrSettingsNotFound
	}
	delete(f.modified, id)
	_, err := f.repo.Update(id, d)
	return err
}

func (f *fakeModelEdit) ModifiedModelFields(context.Context) (map[int64][]string, error) {
	return f.modified, nil
}

type fakeAgent struct {
	items []AgentModelItem
}

func (f *fakeAgent) ListAgentModels(context.Context) ([]AgentModelItem, error) {
	return append([]AgentModelItem(nil), f.items...), nil
}

type scEnv struct {
	t           *testing.T
	handler     nethttp.Handler
	settings    *settings.SQLiteRepository
	models      *models.SQLiteRepository
	settingEdit *fakeSettingEdit
	modelEdit   *fakeModelEdit
	agent       *fakeAgent
	dataDir     string
	ids         map[string]int64
	objectStore ObjectStoreProber
}

const (
	scMultiAngleSeed = `{"models":["gpt-image-2"],"template":"改变相机视角：{angle}","presets":[{"key":"fisheye","name":"鱼眼","rotation":0,"tilt":0,"zoom":"medium","prompt":"鱼眼镜头"}]}`
	scScriptSeed     = `{"modelCandidates":[{"modelId":"canvas-gpt-5.5","displayName":"gpt-5.5"}],"styles":[{"name":"真人短剧风格","version":"v3","skillName":"real-person-short-drama-v3","enabled":true,"userTags":[]}],"concurrency":1,"maxRetries":1}`
	scPresetsSeed    = `{"models":["gpt-image-2"],"presets":[{"key":"portrait","value":{"name":"人像","category":"人物","prompt":"高质感人像","icon":"user","futureField":{"x":1}}}]}`
	scPostSeed       = `{"subtitleRemovalModels":[{"modelId":"task-doubao-video-enhance","label":"标准去字幕","subtitle":"速度快","isTimeLimited":false}],"upscaleModel":"task-doubao-video-enhance"}`
)

// newSCEnv 建一个真实 SQLite 库，预置 4 个模型、5 项 settings 和 1 条 Agent 模型，
// 用 chi 直接挂设置接口（不经 localauth，鉴权由 router 测试覆盖）。
func newSCEnv(t *testing.T) *scEnv {
	t.Helper()
	conn := dbtest.Open(t)
	e := &scEnv{t: t, settings: settings.NewSQLiteRepository(conn), models: models.NewSQLiteRepository(conn), dataDir: t.TempDir(), ids: map[string]int64{}}
	e.settingEdit = &fakeSettingEdit{repo: e.settings, defaults: map[string]string{}, modified: map[string]bool{}, reject: map[string]error{}}
	e.modelEdit = &fakeModelEdit{repo: e.models, defaults: map[int64]models.ModelConfig{}, modified: map[int64][]string{}}

	seed := []models.ModelConfig{
		{Type: "image", Name: "GPT Image 2", ModelID: "gpt-image-2", BasePath: "/v1/images/generations", Enabled: true, SortOrder: 1,
			RequestTemplate:  json.RawMessage(`{"model":"gpt-image-2"}`),
			UISchema:         json.RawMessage(`{"ratio":{"label":"比例","default":"1:1","options":[{"label":"1:1","value":"1:1"},{"label":"16:9","value":"16:9"}]},"n":{"label":"张数","default":1,"options":[{"label":"1","value":1},{"label":"2","value":2}]}}`),
			GenControlRaw:    json.RawMessage(`{"concurrency":3,"modules":{"canvas":{"maxRetries":1,"retryIntervalSec":5}}}`),
			BillingConfigRaw: json.RawMessage(`{"flatFee":10}`), PriceNote: "10积分/张", AdminNote: "内部备注"},
		{Type: "video", Name: "转高清", ModelID: "task-doubao-video-enhance", BasePath: "/v1/video/generations", Enabled: true, Hidden: true, RequestTemplate: json.RawMessage(`{}`)},
		{Type: "text", Name: "GPT-5.5", ModelID: "canvas-gpt-5.5", BasePath: "/v1/chat/completions", Enabled: true, Hidden: true, RequestTemplate: json.RawMessage(`{}`)},
		{Type: "text", Name: "旧文本模型", ModelID: "old-text", BasePath: "/v1/chat/completions", Enabled: false, RequestTemplate: json.RawMessage(`{}`)},
	}
	for _, m := range seed {
		created, err := e.models.Create(m)
		if err != nil {
			t.Fatalf("seed model %s: %v", m.ModelID, err)
		}
		e.ids[m.ModelID] = created.ID
		e.modelEdit.defaults[created.ID] = created
	}
	for k, v := range map[string]string{
		"base_domain":               "http://115.191.35.118:3000",
		"canvas_multi_angle":        scMultiAngleSeed,
		"canvas_script_config":      scScriptSeed,
		"gen_image_prompt_presets":  scPresetsSeed,
		"canvas_postprocess_config": scPostSeed,
	} {
		if err := e.settings.Set(k, v); err != nil {
			t.Fatal(err)
		}
		e.settingEdit.defaults[k] = v
	}
	e.agent = &fakeAgent{
		items: []AgentModelItem{{ID: 1, Role: "planner", Alias: "主Agent", ModelName: "canvas-gpt-5.5"}},
	}
	e.rebuild()
	return e
}

// rebuild 在改了 env 依赖后重新挂路由。
func (e *scEnv) rebuild() {
	r := chi.NewRouter()
	mountSettingsCenter(r, e.deps())
	e.handler = r
}

func (e *scEnv) deps() SettingsCenterDeps {
	return SettingsCenterDeps{
		Settings: e.settings, Models: e.models, ModelUpstream: e.models,
		ModelEdit: e.modelEdit, SettingEdit: e.settingEdit, Agent: e.agent,
		DataDir: e.dataDir, Version: "9.9.9", ObjectStore: e.objectStore,
	}
}

// do 发一个请求，返回状态码和响应体文本。body 为 string 时原样发送（用于构造非法 JSON）。
func (e *scEnv) do(method, path string, body any) (int, string) {
	e.t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1/settings"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func decodeTo[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}
