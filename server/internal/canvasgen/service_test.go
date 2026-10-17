package canvasgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

// captureMutator 是本文件专用的 fake snapshotMutator：固定操作单节点 "n1"，
// 把 mutate 后的节点 map 直接回调给测试，省去测试自己再解一遍 JSON。
type captureMutator struct {
	onNode func(node map[string]any)
}

func (c *captureMutator) MutateSnapshot(_ int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	payload := json.RawMessage(`{"nodes":[{"id":"n1","settings":{}}]}`)
	next, _, err := fn(payload)
	if err != nil {
		return 0, err
	}
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(next, &doc); err != nil {
		return 0, err
	}
	for _, n := range doc.Nodes {
		if n["id"] == "n1" {
			c.onNode(n)
		}
	}
	return 7, nil
}

// newTestServiceWithMutator 组装一个只挂 captureMutator 的 Service，并把时钟钉死，
// 让 upstreamExpire 断言可以精确核对而不是只判断「有没有写」。
func newTestServiceWithMutator(onNode func(node map[string]any)) *Service {
	svc := NewService(nil, &captureMutator{onNode: onNode})
	svc.now = func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) }
	return svc
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// seedance 视频生成成功回写时,节点 settings 要额外存 upstreamExpire(now+22h)+
// srcGenTaskId,供「限时去字幕」反查上游地址;裸 upstream url 绝不能进节点。
func TestOnGenerationResult_SeedanceStoresUpstreamMeta(t *testing.T) {
	cases := []struct {
		name       string
		modelID    string
		upstream   []string
		wantExpire bool // 是否应写 upstreamExpire+srcGenTaskId
	}{
		{"seedance 带上游地址", "doubao-seedance-1-0-pro", []string{"https://up/x.mp4"}, true},
		{"seedance 无上游地址", "doubao-seedance-1-0-pro", nil, false},
		{"非 seedance", "some-other-model", []string{"https://up/x.mp4"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var captured map[string]any
			svc := newTestServiceWithMutator(func(node map[string]any) { captured = node })
			err := svc.OnGenerationResult(context.Background(),
				mustJSON(canvasSinkRef{Kind: "canvas_node", ProjectID: 1, NodeID: "n1"}),
				generation.SinkResult{TaskID: 42, ModelID: c.modelID, OutputURLs: []string{"media://k.mp4"}, UpstreamURLs: c.upstream})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			settings, _ := captured["settings"].(map[string]any)
			_, hasExp := settings["upstreamExpire"]
			_, hasTask := settings["srcGenTaskId"]
			if hasExp != c.wantExpire || hasTask != c.wantExpire {
				t.Errorf("hasExpire=%v hasTask=%v want=%v", hasExp, hasTask, c.wantExpire)
			}
			// 无论如何都不能把裸 upstream url 写进节点
			for k, v := range settings {
				if s, ok := v.(string); ok && strings.HasPrefix(s, "https://up/") {
					t.Errorf("裸上游地址泄漏进节点 settings[%q]=%q", k, s)
				}
			}
			if c.wantExpire {
				wantExpire := time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC).Format(time.RFC3339)
				if got := settings["upstreamExpire"]; got != wantExpire {
					t.Errorf("upstreamExpire = %v, want %v", got, wantExpire)
				}
				var gotTaskID int64
				switch v := settings["srcGenTaskId"].(type) {
				case int64:
					gotTaskID = v
				case float64:
					gotTaskID = int64(v)
				default:
					t.Fatalf("srcGenTaskId 类型异常: %T", v)
				}
				if gotTaskID != 42 {
					t.Errorf("srcGenTaskId = %v, want 42", gotTaskID)
				}
			}
		})
	}
}
