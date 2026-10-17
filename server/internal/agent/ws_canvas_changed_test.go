package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
)

// 兼容金测试：canvas_changed 的 type 与既有字段一个都不许变（旧版前端只认
// type=="canvas_changed"，还在按 version/source_mark 判回声）；op 是**新增可选**
// 字段，不带 op 时帧的字节形态与旧格式逐字相同。
func TestHub_CanvasChangedKeepsLegacyShapeAndAddsOptionalOp(t *testing.T) {
	w := &recordingWriter{}
	hub := NewHub()
	hub.conns[1] = []*hubConn{{ws: w, userID: 1, projectID: 10, ctx: context.Background()}}

	hub.NotifyCanvasChanged(1, 10, 7, "abcd1234", nil)
	hub.NotifyCanvasChanged(1, 10, 8, "abcd1234", &canvasstate.CanvasOpSummary{
		Kind:   canvasstate.OpPatchNode,
		NodeID: "n1",
		Patch:  json.RawMessage(`{"settings":{"prompt":"x"}}`),
	})

	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.frames) != 2 {
		t.Fatalf("收到 %d 帧, want 2", len(w.frames))
	}
	legacy := string(w.frames[0])
	if legacy != `{"type":"canvas_changed","project_id":10,"version":7,"source_mark":"abcd1234"}` {
		t.Fatalf("无 op 时帧形态变了（旧版前端会失聪）: %s", legacy)
	}
	withOp := string(w.frames[1])
	if !strings.Contains(withOp, `"type":"canvas_changed"`) || !strings.Contains(withOp, `"version":8`) ||
		!strings.Contains(withOp, `"source_mark":"abcd1234"`) {
		t.Fatalf("带 op 的帧丢了既有字段: %s", withOp)
	}
	if !strings.Contains(withOp, `"op":{"kind":"patchNode","node_id":"n1","patch":{"settings":{"prompt":"x"}}}`) {
		t.Fatalf("op 摘要序列化不对: %s", withOp)
	}
}

// 桌面端没有 presence：注册连接不得向任何人发帧。
func TestHub_RegisterDoesNotBroadcast(t *testing.T) {
	first := &recordingWriter{}
	hub := NewHub()
	hub.Register(context.Background(), 1, 10, first)
	hub.Register(context.Background(), 1, 10, &recordingWriter{})
	first.mu.Lock()
	defer first.mu.Unlock()
	if len(first.frames) != 0 {
		t.Fatalf("注册连接不应广播，收到 %d 帧", len(first.frames))
	}
}
