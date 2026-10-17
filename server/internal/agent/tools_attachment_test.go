package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

type stubAssetFinder struct{ rec *assets.AssetRecord }

func (s *stubAssetFinder) FindByID(id int64) (*assets.AssetRecord, error) {
	if s.rec != nil && s.rec.ID == id {
		return s.rec, nil
	}
	return nil, nil
}

type stubObjectReader struct{ content string }

func (s *stubObjectReader) GetObject(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.content)), nil
}

type stubSnapshotReader struct{ raw string }

func (s *stubSnapshotReader) GetLatestSnapshot(_ int64) (json.RawMessage, error) {
	return json.RawMessage(s.raw), nil
}

func attachCtx(userID, projectID int64) context.Context {
	ctx := WithUserID(context.Background(), userID)
	return WithProjectID(ctx, projectID)
}

func runTool(t *testing.T, tool *ReadAttachmentTool, ctx context.Context, args string) map[string]any {
	t.Helper()
	out, err := tool.InvokableRun(ctx, args)
	if err != nil {
		t.Fatalf("InvokableRun(%s) error: %v", args, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("result not JSON: %v\n%s", err, out)
	}
	return m
}

// 分段读取的核心契约：切片正确 + 导航元信息（total/next_offset/eof）齐全——
// 没有导航模型只能瞎猜 offset。
func TestReadAttachment_AssetPaging(t *testing.T) {
	content := strings.Repeat("甲", 100) + strings.Repeat("乙", 100)
	tool := &ReadAttachmentTool{
		Assets:  &stubAssetFinder{rec: &assets.AssetRecord{ID: 7, UserID: 42, ObjectKey: "k"}},
		Objects: &stubObjectReader{content: content},
	}
	ctx := attachCtx(42, 1)

	m := runTool(t, tool, ctx, `{"artifact_id":"asset_7","offset":0,"limit":100}`)
	if m["total_chars"].(float64) != 200 || m["next_offset"].(float64) != 100 {
		t.Fatalf("navigation wrong: %v", m)
	}
	if m["content"] != strings.Repeat("甲", 100) {
		t.Fatalf("slice wrong: %q", m["content"])
	}

	m = runTool(t, tool, ctx, `{"artifact_id":"asset_7","offset":100,"limit":100}`)
	if m["content"] != strings.Repeat("乙", 100) || m["eof"] != true {
		t.Fatalf("tail slice wrong: %v", m)
	}
}

// IDOR 防线：别人的 asset 必须报「不存在」，不泄露存在性。
func TestReadAttachment_OwnershipRejected(t *testing.T) {
	tool := &ReadAttachmentTool{
		Assets:  &stubAssetFinder{rec: &assets.AssetRecord{ID: 7, UserID: 42, ObjectKey: "k"}},
		Objects: &stubObjectReader{content: "秘密"},
	}
	_, err := tool.InvokableRun(attachCtx(99, 1), `{"artifact_id":"asset_7"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

// query 返回命中 offset + 上下文，offset 必须与原文 rune 位置对齐（后续精读靠它）。
func TestReadAttachment_QueryOffsets(t *testing.T) {
	content := strings.Repeat("前", 300) + "关键词" + strings.Repeat("后", 300)
	tool := &ReadAttachmentTool{
		Assets:  &stubAssetFinder{rec: &assets.AssetRecord{ID: 7, UserID: 42, ObjectKey: "k"}},
		Objects: &stubObjectReader{content: content},
	}
	m := runTool(t, tool, attachCtx(42, 1), `{"artifact_id":"asset_7","query":"关键词"}`)
	matches := m["query_matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %v", m)
	}
	hit := matches[0].(map[string]any)
	if hit["offset"].(float64) != 300 {
		t.Fatalf("offset misaligned: %v", hit)
	}
	if !strings.Contains(hit["context"].(string), "关键词") {
		t.Fatalf("context missing keyword: %v", hit)
	}
}

// 画布文字节点来源：按节点 id 从快照读 settings.text；非 text 节点报错。
func TestReadAttachment_CanvasTextNode(t *testing.T) {
	snap := `{"nodes":[{"id":"n-1","type":"text","settings":{"text":"节点正文"}},{"id":"n-2","type":"gen-image","settings":{}}]}`
	tool := &ReadAttachmentTool{Canvas: &stubSnapshotReader{raw: snap}}
	ctx := attachCtx(42, 5)

	m := runTool(t, tool, ctx, `{"artifact_id":"n-1"}`)
	if m["content"] != "节点正文" {
		t.Fatalf("node text wrong: %v", m)
	}
	if _, err := tool.InvokableRun(ctx, `{"artifact_id":"n-2"}`); err == nil {
		t.Fatal("non-text node must error")
	}
	if _, err := tool.InvokableRun(ctx, `{"artifact_id":"n-404"}`); err == nil {
		t.Fatal("missing node must error")
	}
}

// limit 超 cap 被压回；offset 越界报错而不是静默空串。
func TestReadAttachment_LimitCapAndOffsetBounds(t *testing.T) {
	content := strings.Repeat("字", 25_000)
	tool := &ReadAttachmentTool{
		Assets:  &stubAssetFinder{rec: &assets.AssetRecord{ID: 7, UserID: 42, ObjectKey: "k"}},
		Objects: &stubObjectReader{content: content},
	}
	ctx := attachCtx(42, 1)
	m := runTool(t, tool, ctx, `{"artifact_id":"asset_7","limit":999999}`)
	if len([]rune(m["content"].(string))) != maxAttachmentReadRunes {
		t.Fatalf("limit cap not applied")
	}
	if _, err := tool.InvokableRun(ctx, `{"artifact_id":"asset_7","offset":25001}`); err == nil {
		t.Fatal("out-of-range offset must error")
	}
}
