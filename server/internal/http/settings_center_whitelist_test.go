package http

// 用真实库 + 真实内置种子 + 真实 builtin.Service 挂设置接口的测试环境。
// 画布配置的五项已写死，其用例见 settings_hardcoded_test.go。

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/agent"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/agent/agentseed"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/builtin"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
)

type wlEnv struct {
	t        *testing.T
	h        nethttp.Handler
	settings settings.Repository
	policy   *builtin.Service
}

func newWhitelistEnv(t *testing.T) *wlEnv {
	t.Helper()
	conn := dbtest.Open(t)
	if err := builtin.Apply(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := agentseed.Import(conn); err != nil {
		t.Fatal(err)
	}
	policy, err := builtin.NewService(conn)
	if err != nil {
		t.Fatal(err)
	}
	sr, mr := settings.NewSQLiteRepository(conn), models.NewSQLiteRepository(conn)
	r := chi.NewRouter()
	mountSettingsCenter(r, SettingsCenterDeps{
		Settings:    sr,
		Models:      mr,
		ModelEdit:   NewPolicyModelEdit(policy, mr),
		SettingEdit: NewPolicySettingEdit(policy, sr),
		Agent:       NewBackboneAgentModels(agent.NewBackboneRepo(conn)),
		DataDir:     t.TempDir(),
		Version:     "test",
	})
	return &wlEnv{t: t, h: r, settings: sr, policy: policy}
}

func (e *wlEnv) do(method, path, body string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1/settings"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func (e *wlEnv) raw(key string) string {
	e.t.Helper()
	v, err := e.settings.Get(key)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *wlEnv) modified(key string) bool {
	e.t.Helper()
	states, err := e.policy.ListSettings(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, st := range states {
		if st.Key == key {
			return len(st.Overrides) > 0
		}
	}
	return false
}

// 模型补丁只认白名单字段，请求模板等一律 400（handler 严格解码），库不变。
func TestSettingsModelPatchRejectsTemplateFields(t *testing.T) {
	e := newWhitelistEnv(t)
	code, body := e.do("GET", "/models", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var list struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(body), &list)
	if len(list.Data) == 0 {
		t.Fatal("没有模型")
	}
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]any{"name": "x", "requestTemplate": map[string]any{"evil": true}})
	if code, out := e.do("PATCH", "/models/"+jsonInt(list.Data[0].ID), buf.String()); code != 400 {
		t.Fatalf("应 400：%d %s", code, out)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }
