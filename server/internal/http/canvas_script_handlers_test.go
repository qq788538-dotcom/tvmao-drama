package http

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasscript"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/settings"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/skillruntime"
)

type csFakeModels struct{}

func (csFakeModels) GetByModelID(string) (*models.ModelConfig, error) {
	return &models.ModelConfig{}, nil
}

type csFakeKeys struct{}

func (csFakeKeys) Resolve(int64) (string, error) { return "sk", nil }

type csFakeSkills struct{}

func (csFakeSkills) Snapshot(_ context.Context, name string) (skillruntime.Snapshot, error) {
	return skillruntime.Snapshot{Name: name}, nil
}
func (csFakeSkills) Run(context.Context, skillruntime.RunRequest) (skillruntime.RunResult, error) {
	return skillruntime.RunResult{}, nil
}

func newCanvasScriptRouter(t *testing.T) (nethttp.Handler, *canvasscript.Service) {
	t.Helper()
	conn := dbtest.Open(t)
	st := settings.NewSQLiteRepository(conn)
	svc := canvasscript.NewService(conn, st, csFakeModels{}, csFakeKeys{}, csFakeSkills{})
	if err := svc.SaveConfig(canvasscript.Config{
		ModelCandidates: []canvasscript.ModelCandidate{{ModelID: "canvas-gpt-5.5", DisplayName: "gpt-5.5"}},
		Styles:          []canvasscript.StyleEntry{{Name: "真人短剧风格", Version: "v3", SkillName: "real-person-short-drama-v3", Enabled: true}},
	}); err != nil {
		t.Fatal(err)
	}
	h := NewCanvasScriptHandler(svc)
	r := chi.NewRouter()
	r.Use(LocalIdentity)
	r.Get("/api/v1/canvas-script/styles", h.Styles)
	r.Post("/api/v1/canvas-script-tasks", h.Submit)
	r.Get("/api/v1/canvas-script-tasks/{taskID}", h.Status)
	return r, svc
}

func doJSON(t *testing.T, h nethttp.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCanvasScriptStylesHidesSkillName(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	code, out := doJSON(t, h, "GET", "/api/v1/canvas-script/styles", "")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "real-person-short-drama-v3") {
		t.Fatalf("skillName 不应下发: %s", raw)
	}
	cands := out["modelCandidates"].([]any)
	if len(cands) != 1 || cands[0].(map[string]any)["credits"] != float64(0) {
		t.Fatalf("候选应保留且 credits=0: %s", raw)
	}
}

func TestCanvasScriptSubmitAndPollStatus(t *testing.T) {
	h, svc := newCanvasScriptRouter(t)
	code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks",
		`{"projectId":7,"nodeId":"node-1","style":"真人短剧风格 v3","durationSec":10,"script":"第一场","model":"canvas-gpt-5.5"}`)
	if code != 200 || out["status"] != "pending" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	id := int64(out["taskId"].(float64))
	path := "/api/v1/canvas-script-tasks/" + strconv.FormatInt(id, 10)
	if code, out = doJSON(t, h, "GET", path, ""); code != 200 || out["status"] != "pending" || out["nodeId"] != "node-1" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if _, ok := out["result"]; ok {
		t.Fatal("未成功时不应带 result")
	}
	_ = svc // 结果写入由 worker 完成，这里只验证轮询形状
}

func TestCanvasScriptSubmitValidation(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	if code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks", `{"projectId":7,"nodeId":"n","style":"真人短剧风格 v3"}`); code != 400 || out["error"] == nil {
		t.Fatalf("缺 model 应 400: %d %v", code, out)
	}
	if code, _ := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks", `not json`); code != 400 {
		t.Fatalf("坏 body 应 400: %d", code)
	}
	if code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks",
		`{"projectId":7,"nodeId":"n","style":"真人短剧风格 v3","durationSec":99,"script":"x","model":"canvas-gpt-5.5"}`); code != 400 || !strings.Contains(out["error"].(string), "时长") {
		t.Fatalf("业务校验错误应 400 并带原因: %d %v", code, out)
	}
}

func TestCanvasScriptStatusNotFound(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	if code, _ := doJSON(t, h, "GET", "/api/v1/canvas-script-tasks/999", ""); code != 404 {
		t.Fatalf("code=%d", code)
	}
	if code, _ := doJSON(t, h, "GET", "/api/v1/canvas-script-tasks/abc", ""); code != 400 {
		t.Fatalf("code=%d", code)
	}
}
