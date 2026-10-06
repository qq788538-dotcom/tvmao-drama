package canvasgen

import (
	"encoding/json"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// 文本生成任务的写回：产物落 settings.text（浏览器 TextNode.tsx 读写的字段），
// 不碰 content（画布语义里那是媒体 URL），不并 genHistory（条目形状 {url,...}
// 与前端逐字同构，文本不进历史是拍板过的第一版边界）。

func TestOnGenerationResult_TextWritesSettingsText(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"text-node","status":"generating","taskId":7,
		"settings":{"text":"旧内容","genPrompt":"根据图片写提示词"}}]}`
	node := runSuccess(t, snap, generation.SinkResult{Text: "# 生成的提示词\n\n一段描述"})

	settings := node["settings"].(map[string]any)
	if settings["text"] != "# 生成的提示词\n\n一段描述" {
		t.Fatalf("settings.text not written: %+v", settings)
	}
	if node["status"] != "succeeded" {
		t.Fatalf("status not updated: %+v", node)
	}
	if _, ok := node["taskId"]; ok {
		t.Fatalf("taskId should be cleared: %+v", node)
	}
	if _, ok := node["content"]; ok {
		t.Fatalf("content must stay untouched for text results: %+v", node)
	}
	if _, ok := settings["history"]; ok {
		t.Fatalf("text results must not create history: %+v", settings)
	}
}

// 同文本重投必须幂等——outbox 投递失败重试是常态路径。
func TestOnGenerationResult_TextIsIdempotent(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"text-node","status":"succeeded",
		"settings":{"text":"# 生成的提示词"}}]}`
	node := runSuccess(t, snap, generation.SinkResult{Text: "# 生成的提示词"})
	settings := node["settings"].(map[string]any)
	if settings["text"] != "# 生成的提示词" || node["status"] != "succeeded" {
		t.Fatalf("idempotent rewrite broke node: %+v", node)
	}
}

// 有媒体产物时 Text 必须让位——生图类 chat 模型（gemini-image）的回复文本只是
// 图片附带说明，走原媒体写回路径，settings.text 不能被污染。
func TestOnGenerationResult_MediaWinsOverText(t *testing.T) {
	snap := `{"nodes":[{"id":"A","type":"gen-image","status":"generating",
		"settings":{}}]}`
	node := runSuccess(t, snap, generation.SinkResult{
		OutputURLs: []string{"media://v1"},
		Text:       "这是生成的图片说明",
	})
	if node["content"] != "media://v1" {
		t.Fatalf("content not written: %+v", node)
	}
	settings := node["settings"].(map[string]any)
	if _, ok := settings["text"]; ok {
		t.Fatalf("settings.text must not be polluted by media caption: %+v", settings)
	}
}

// 文本任务的轮询状态要带 text——前端开着页面时靠它即时展示。
func TestStatusFromTaskCarriesText(t *testing.T) {
	out, _ := json.Marshal(map[string]any{"text": "生成的正文"})
	st := statusFromTask(&generation.Task{Status: "succeeded", Output: out})
	if st.Text != "生成的正文" || st.Status != "succeeded" {
		t.Fatalf("status text missing: %+v", st)
	}
}
