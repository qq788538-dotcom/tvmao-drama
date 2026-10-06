package http

import (
	"strings"
	"testing"
)

type agentResp struct {
	Data       []AgentModelItem `json:"data"`
	Candidates []struct {
		ModelID string `json:"modelId"`
		Name    string `json:"name"`
	} `json:"candidates"`
}

func TestAgentModelsListOnlyEnabledTextCandidates(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/agent-models", nil)
	a := decodeTo[agentResp](t, body)
	if code != 200 || len(a.Data) != 1 || a.Data[0].ModelName != "canvas-gpt-5.5" {
		t.Fatalf("%d %s", code, body)
	}
	if len(a.Candidates) != 1 || a.Candidates[0].ModelID != "canvas-gpt-5.5" || a.Candidates[0].Name != "GPT-5.5" {
		t.Fatalf("候选应只有已启用的 text 模型（含 hidden）: %+v", a.Candidates)
	}
}

func TestAgentModelSwitchAndRestore(t *testing.T) {
	e := newSCEnv(t)
	// 先启用 old-text，让它成为候选
	m, _ := e.models.GetByID(e.ids["old-text"])
	m.Enabled = true
	if _, err := e.models.Update(m.ID, *m); err != nil {
		t.Fatal(err)
	}
	code, body := e.do("PUT", "/agent-models/1", map[string]any{"modelName": "old-text"})
	a := decodeTo[agentResp](t, body)
	if code != 200 || a.Data[0].ModelName != "old-text" || !a.Data[0].Modified {
		t.Fatalf("%d %s", code, body)
	}
	code, body = e.do("POST", "/agent-models/1/restore-default", nil)
	a = decodeTo[agentResp](t, body)
	if code != 200 || a.Data[0].ModelName != "canvas-gpt-5.5" || a.Data[0].Modified {
		t.Fatalf("%d %s", code, body)
	}
}

func TestAgentModelSwitchRejects(t *testing.T) {
	e := newSCEnv(t)
	for _, body := range []any{
		map[string]any{"modelName": "gpt-image-2"}, // 不是文本模型
		map[string]any{"modelName": "old-text"},    // 已停用
		map[string]any{"modelName": ""},
		map[string]any{"modelName": "canvas-gpt-5.5", "temperature": 0.5}, // A2：其余参数不可改
		map[string]any{"maxTokens": 100},
	} {
		if code, resp := e.do("PUT", "/agent-models/1", body); code != 400 {
			t.Errorf("%v → %d %s", body, code, resp)
		}
	}
	if code, body := e.do("PUT", "/agent-models/42", map[string]any{"modelName": "canvas-gpt-5.5"}); code != 404 || !strings.Contains(body, "不存在") {
		t.Fatalf("%d %s", code, body)
	}
}
