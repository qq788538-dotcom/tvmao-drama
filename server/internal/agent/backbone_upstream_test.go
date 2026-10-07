package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// chatUpstream 是一个最小的 OpenAI 兼容上游：记录收到的路径与 Authorization。
type chatUpstream struct {
	srv   *httptest.Server
	mu    sync.Mutex
	auths []string
	paths []string
}

func newChatUpstream(t *testing.T) *chatUpstream {
	t.Helper()
	u := &chatUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		u.mu.Lock()
		u.auths = append(u.auths, r.Header.Get("Authorization"))
		u.paths = append(u.paths, r.URL.Path)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *chatUpstream) calls() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.auths...), append([]string(nil), u.paths...)
}

func generateOnce(t *testing.T, deps PlannerDeps) {
	t.Helper()
	cm, err := BuildPlannerForUser(context.Background(), 1, deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
}

// Agent backbone：按 model_name 找到 model_configs 里同 model_id 的行，配了独立上游就用它的地址和 Key。
func TestBackboneUsesModelOwnUpstream(t *testing.T) {
	global, own := newChatUpstream(t), newChatUpstream(t)
	deps := PlannerDeps{
		Backbones:   &stubBackboneRepo{active: &BackboneModel{ModelName: "gpt-x", MaxTokens: 100}},
		Settings:    stubSettings{base: global.srv.URL},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{cfg: &models.ModelConfig{ModelID: "gpt-x", UpstreamBaseURL: own.srv.URL + "/", UpstreamAPIKey: "sk-own"}},
	}
	generateOnce(t, deps)
	auths, paths := own.calls()
	if len(auths) != 1 || auths[0] != "Bearer sk-own" || paths[0] != "/v1/chat/completions" {
		t.Fatalf("独立上游: auths=%v paths=%v", auths, paths)
	}
	if a, _ := global.calls(); len(a) != 0 {
		t.Fatalf("不应打到全局上游: %v", a)
	}
}

func TestBackboneFallsBackToGlobalUpstream(t *testing.T) {
	for name, m := range map[string]stubModels{
		"模型未配置独立上游":          {cfg: &models.ModelConfig{ModelID: "gpt-x"}},
		"model_configs 没有该行": {err: models.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			global := newChatUpstream(t)
			generateOnce(t, PlannerDeps{
				Backbones:   &stubBackboneRepo{active: &BackboneModel{ModelName: "gpt-x", MaxTokens: 100}},
				Settings:    stubSettings{base: global.srv.URL},
				UpstreamKey: stubKeyResolver{key: "sk-global"},
				Models:      m,
			})
			if auths, _ := global.calls(); len(auths) != 1 || auths[0] != "Bearer sk-global" {
				t.Fatalf("应走全局: %v", auths)
			}
		})
	}
}

// 全局没配地址/Key，但 backbone 的模型配了独立上游：照样可用。
func TestBackboneModelOwnUpstreamWithoutGlobal(t *testing.T) {
	own := newChatUpstream(t)
	generateOnce(t, PlannerDeps{
		Backbones:   &stubBackboneRepo{active: &BackboneModel{ModelName: "gpt-x", MaxTokens: 100}},
		Settings:    stubSettings{},
		UpstreamKey: stubKeyResolver{},
		Models:      stubModels{cfg: &models.ModelConfig{ModelID: "gpt-x", UpstreamBaseURL: own.srv.URL, UpstreamAPIKey: "sk-own"}},
	})
	if auths, _ := own.calls(); len(auths) != 1 {
		t.Fatalf("应打到独立上游: %v", auths)
	}
}

// M-1：独立上游地址存成了 .../v1（旧值），Agent 仍只拼一层 /v1。
func TestBackboneOwnUpstreamWithTrailingV1(t *testing.T) {
	own := newChatUpstream(t)
	generateOnce(t, PlannerDeps{
		Backbones:   &stubBackboneRepo{active: &BackboneModel{ModelName: "gpt-x", MaxTokens: 100}},
		Settings:    stubSettings{},
		UpstreamKey: stubKeyResolver{},
		Models:      stubModels{cfg: &models.ModelConfig{ModelID: "gpt-x", UpstreamBaseURL: own.srv.URL + "/v1/", UpstreamAPIKey: "sk-own"}},
	})
	if _, paths := own.calls(); len(paths) != 1 || paths[0] != "/v1/chat/completions" {
		t.Fatalf("paths=%v", paths)
	}
}

// M-5：查模型行遇到 ErrNotFound 以外的错误时直接失败，不静默回落全局账号。
func TestBackboneModelLookupErrorDoesNotFallBackToGlobal(t *testing.T) {
	global := newChatUpstream(t)
	_, err := BuildPlannerForUser(context.Background(), 1, PlannerDeps{
		Backbones:   &stubBackboneRepo{active: &BackboneModel{ModelName: "gpt-x", MaxTokens: 100}},
		Settings:    stubSettings{base: global.srv.URL},
		UpstreamKey: stubKeyResolver{key: "sk-global"},
		Models:      stubModels{err: errors.New("database is locked")},
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if auths, _ := global.calls(); len(auths) != 0 {
		t.Fatalf("不应打到全局: %v", auths)
	}
}
