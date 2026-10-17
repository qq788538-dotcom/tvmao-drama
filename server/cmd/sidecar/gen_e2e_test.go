package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// 真实装配（磁盘存储 + 资产仓库 + 签名器）下走一遍画布生成：经 HTTP 提交 → 产物落盘 →
// 节点回写。落库的节点 content 必须是 media://<key>（不得是带端口/token 的本地 URL），
// 经接口读快照时才签成本次启动的本地 URL。
func TestE2EGenerationPersistsMediaKeyOnCanvas(t *testing.T) {
	e := newE2E(t)
	up := newFakeUpstream(t)
	st := settings.NewSQLiteRepository(e.conn)
	_ = st.Set("base_domain", up.srv.URL)
	_ = st.Set("upstream_api_key", "sk-test")
	if _, err := models.NewSQLiteRepository(e.conn).Create(models.ModelConfig{Type: "image", Name: "img", ModelID: "img-test",
		BasePath: "/v1/images/generations", Enabled: true,
		RequestTemplate: json.RawMessage(`{"model":"img-test","prompt":"{{prompt}}"}`)}); err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(e.conn)
	pid, err := pr.Create(1, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{"id": "n1", "type": "gen-image", "status": "idle", "settings": map[string]any{}}}, "connections": []any{}})
	if _, err := pr.SaveAuthoritative(pid, snap); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.app.gen.Gen.StartBackground(ctx)

	resp, b := e.callJSON(t, http.MethodGet, "/api/v1/models", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "img-test") {
		t.Fatalf("models: %d %s", resp.StatusCode, b)
	}
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/canvas-tasks",
		fmt.Sprintf(`{"projectId":%d,"nodeId":"n1","modelId":"img-test","inputs":{"prompt":"一只猫"}}`, pid))
	if resp.StatusCode/100 != 2 {
		t.Fatalf("submit: %d %s", resp.StatusCode, b)
	}

	node := func() map[string]any {
		raw, err := pr.GetLatestSnapshot(pid)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Nodes []map[string]any `json:"nodes"`
		}
		_ = json.Unmarshal(raw, &doc)
		for _, n := range doc.Nodes {
			if n["id"] == "n1" {
				return n
			}
		}
		return nil
	}
	waitFor(t, "节点 n1 回写为 succeeded", func() bool {
		n := node()
		return n != nil && n["status"] == "succeeded"
	})

	content, _ := node()["content"].(string)
	key, ok := e.storage.ExtractKey(content)
	if !strings.HasPrefix(content, "media://") || !ok || content != "media://"+key {
		t.Fatalf("落库的节点 content 应为 media://<key>, got %q", content)
	}
	raw, _ := pr.GetLatestSnapshot(pid)
	if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), e2eMediaToken) {
		t.Fatalf("快照落库不得含本地 URL 或媒体 token: %s", raw)
	}
	p, err := e.storage.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("产物应已写入本地存储: %v", err)
	}

	resp, b = e.callJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/snapshot", pid), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), e.base+"/media/") {
		t.Fatalf("读快照应签成本地 URL: %d %s", resp.StatusCode, b)
	}
}
