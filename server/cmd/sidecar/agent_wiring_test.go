package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

const e2eToken = "0123456789abcdef0123456789abcdef"

// fakeLLMUpstream 模拟 new_api 的 OpenAI 兼容流式接口，并记下收到的路径和 Key。
func fakeLLMUpstream(t *testing.T, gotAuth, gotPath *string) *httptest.Server {
	return httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		*gotAuth, *gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta, finish string) {
			fin := "null"
			if finish != "" {
				fin = `"` + finish + `"`
			}
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, fin)
		}
		chunk(`{"role":"assistant","content":"你好"}`, "")
		chunk(`{}`, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

type e2e struct {
	srv       *httptest.Server
	projectID int64
}

func setupE2E(t *testing.T, baseDomain, key string) e2e {
	t.Helper()
	conn := dbtest.Open(t)
	for _, q := range []string{
		`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('text','a','canvas-agent-gpt-5.5','/v1/chat/completions','{}')`,
		`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('text','b','doubao-seed-2-0-lite-260428','/v1/chat/completions','{}')`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if baseDomain != "" {
		_, _ = conn.Exec(`INSERT INTO settings ("key", value) VALUES ('base_domain', ?), ('upstream_api_key', ?)`, baseDomain, key)
	}
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'e2e')`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w, err := wireAgent(ctx, agentWiringDeps{DB: conn})
	if err != nil {
		t.Fatal(err)
	}
	h := apphttp.NewRouter(apphttp.Deps{
		Auth:  localauth.Config{Token: e2eToken, AllowedOrigins: []string{"http://localhost:5173"}},
		Agent: w.Agent, Skills: w.Skills, AdminAgent: w.AdminAgent,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return e2e{srv: srv, projectID: pid}
}

func (e e2e) do(t *testing.T, method, path, body string) map[string]any {
	t.Helper()
	req, _ := nethttp.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		t.Fatalf("%s %s → %d %v", method, path, resp.StatusCode, out)
	}
	return out
}

// sendAndCollect 连 WS、发一条消息、收帧直到 thread_end。
func (e e2e) sendAndCollect(t *testing.T, threadID, text string) []agent.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + fmt.Sprintf("/api/v1/agent/ws?token=%s&project_id=%d", e2eToken, e.projectID)
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: nethttp.Header{"Origin": {"http://localhost:5173"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	buf, _ := json.Marshal(agent.Frame{Type: agent.FrameUserMessage, ThreadID: threadID, ActionID: "act1", Content: text})
	if err := c.Write(ctx, websocket.MessageText, buf); err != nil {
		t.Fatal(err)
	}
	var frames []agent.Frame
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("读帧失败（已收 %d 帧）: %v", len(frames), err)
		}
		var f agent.Frame
		_ = json.Unmarshal(data, &f)
		frames = append(frames, f)
		if f.Type == agent.FrameThreadEnd {
			return frames
		}
	}
}

func TestAgentE2E_ChatRoundTrip(t *testing.T) {
	var gotAuth, gotPath string
	up := fakeLLMUpstream(t, &gotAuth, &gotPath)
	defer up.Close()
	e := setupE2E(t, up.URL, "sk-test")

	skills := e.do(t, "GET", "/api/v1/skills", "")
	if n := len(skills["data"].([]any)); n != 8 {
		t.Fatalf("应列出 8 个已发布的内置 Skill，得到 %d", n)
	}
	th := e.do(t, "POST", fmt.Sprintf("/api/v1/projects/%d/agent/threads", e.projectID), `{"title":"t"}`)
	threadID := th["id"].(string)

	frames := e.sendAndCollect(t, threadID, "你好")
	var text strings.Builder
	for _, f := range frames {
		if f.Type == agent.FrameError {
			t.Fatalf("不应出错: %+v", f.Error)
		}
		if f.Delta != nil {
			text.WriteString(f.Delta.Text)
		}
	}
	if !strings.Contains(text.String(), "你好") {
		t.Fatalf("没收到上游文本，帧: %+v", frames)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer sk-test" {
		t.Fatalf("上游地址或 Key 不对: path=%s auth=%s", gotPath, gotAuth)
	}
	msgs := e.do(t, "GET", fmt.Sprintf("/api/v1/projects/%d/agent/threads/%s/messages", e.projectID, threadID), "")
	if n := len(msgs["data"].([]any)); n != 2 {
		t.Fatalf("应落库 user+assistant 两条，得到 %d", n)
	}
}

// 没配上游就发消息，必须收到 error + action_end，面板不转圈。
func TestAgentE2E_MissingUpstreamConfigEndsAction(t *testing.T) {
	e := setupE2E(t, "", "")
	th := e.do(t, "POST", fmt.Sprintf("/api/v1/projects/%d/agent/threads", e.projectID), `{}`)
	frames := e.sendAndCollect(t, th["id"].(string), "你好")
	var sawErr, sawEnd bool
	for _, f := range frames {
		if f.Type == agent.FrameError && f.Error != nil && f.Error.Code == "no_planner" {
			sawErr = true
		}
		if f.Type == agent.FrameActionEnd {
			sawEnd = true
		}
	}
	if !sawErr || !sawEnd {
		t.Fatalf("缺 no_planner 错误或 action_end: %+v", frames)
	}
}

// 写接口不注册（Agent 模型写死为内置值），只读列表仍在。
func TestAgentE2E_AdminBackboneWritesNotRegistered(t *testing.T) {
	e := setupE2E(t, "http://unused", "k")
	if out := e.do(t, "GET", "/api/v1/admin/agent/backbone-models", ``); out["data"] == nil {
		t.Fatalf("只读列表应返回 data: %v", out)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/v1/admin/agent/backbone-models/1"},
		{"POST", "/api/v1/admin/agent/backbone-models/1/reset"},
	} {
		req, _ := nethttp.NewRequest(c.method, e.srv.URL+c.path, strings.NewReader(`{"model_name":"doubao-seed-2-0-lite-260428"}`))
		req.Header.Set("Authorization", "Bearer "+e2eToken)
		resp, err := nethttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != nethttp.StatusNotFound && resp.StatusCode != nethttp.StatusMethodNotAllowed {
			t.Errorf("%s %s 不应注册，got %d", c.method, c.path, resp.StatusCode)
		}
	}
}

type stubSigner struct{}

func (stubSigner) Sign(s string) string         { return s }
func (stubSigner) Canonicalize(s string) string { return s }

// 接了 Signer/Objects 却漏传 Keys，本机图片会原样发给上游；装配直接报错。
func TestWireAgentRequiresKeysWhenStorageWired(t *testing.T) {
	conn := dbtest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := wireAgent(ctx, agentWiringDeps{DB: conn, Signer: stubSigner{}}); err == nil {
		t.Fatal("Signer 已接、Keys 未接时应报错")
	}
	if _, err := wireAgent(ctx, agentWiringDeps{DB: conn}); err != nil {
		t.Fatalf("存储整体未接入时应照常装配: %v", err)
	}
}
