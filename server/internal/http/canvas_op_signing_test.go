package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// signingStorage 的 ExtractKey 只认 media:// 前缀，SignedURL 出一个一眼可辨的
// https 地址——这样断言「帧里已经没有 media://、换成了签名 URL」才有意义。
type signingStorage struct{ fakeAssetStorage }

func (signingStorage) ExtractKey(urlOrKey string) (string, bool) {
	if key, ok := strings.CutPrefix(urlOrKey, "media://"); ok {
		return key, true
	}
	return "", false
}

func (signingStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://signed.example/" + key, nil
}

// op 摘要里的媒体引用必须在出帧前被签名。
//
// 为什么是必修：快照里存的是 canonical `media://<key>`，一期同伴走 GET 快照，
// 那条路上有 SignJSON（唯一签名出口）；二期本地合并绕开了它。前端把 node.content
// 直接当 <img src>，浏览器不认 media:// → A 生成一张图，B 的画布上这张图整块裂开，
// 刷新才好，且没有任何报错。
func TestCanvasMutationSignsMediaInBroadcastOp(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"gen-image"}],"connections":[]}`),
		mutateVersion: 6,
	}
	notifier := &recordingNotifier{}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, NewMediaSigner(signingStorage{}))
	handler.SetChangeNotifier(notifier)

	// upsertNode：整节点里带媒体。
	handler.UpsertNode(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPut,
		"/api/v1/projects/42/canvas/nodes",
		[]byte(`{"ownerToken":"team:aa","node":{"id":"n2","type":"gen-image","content":"media://k2"}}`), nil, 9))
	op := requireOp(t, notifier)
	if strings.Contains(string(op.Node), "media://") {
		t.Fatalf("op.node 里还留着 media://（同伴的图会整块裂开）: %s", op.Node)
	}
	if !strings.Contains(string(op.Node), "https://signed.example/k2") {
		t.Fatalf("op.node 没换成签名 URL: %s", op.Node)
	}

	// patchNode：patch 里带媒体（生成结果落节点走的就是这条）。
	notifier.calls = nil
	handler.PatchNode(httptest.NewRecorder(), teamCanvasRequest(t, http.MethodPatch,
		"/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"team:aa","patch":{"content":"media://k1"}}`),
		map[string]string{"nodeID": "n1"}, 9))
	op = requireOp(t, notifier)
	if strings.Contains(string(op.Patch), "media://") || !strings.Contains(string(op.Patch), "https://signed.example/k1") {
		t.Fatalf("op.patch 没被签名: %s", op.Patch)
	}

	// 落库的字节必须仍是 canonical：签名 URL 有时效，写进快照就是一堆定时死链。
	if !strings.Contains(string(repo.mutatedPayload), "media://k1") {
		t.Fatalf("落库的快照被签名 URL 污染了: %s", repo.mutatedPayload)
	}
}

// no-op 写（服务端状态本来就等于目标）不涨 version，绝不能发帧：同 version 两条
// 内容不同的帧会让接收端的「version==local+1」缺口检测静默失效——某条连接被写超时
// 跳过一帧后，下一条 no-op 帧正好满足等式，两端从此分叉且无人察觉。
func TestCanvasMutationNoOpDoesNotBroadcast(t *testing.T) {
	const snapshot = `{"nodes":[{"id":"n1","type":"text","label":"same"}],"connections":[]}`
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(snapshot),
		mutateVersion: 9,
	}
	notifier := &recordingNotifier{}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	handler.SetChangeNotifier(notifier)
	rec := httptest.NewRecorder()

	// patch 的内容与现状逐字相同 → canvasstate 判定 changed=false。
	handler.PatchNode(rec, teamCanvasRequest(t, http.MethodPatch,
		"/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"team:aa","patch":{"label":"same"}}`),
		map[string]string{"nodeID": "n1"}, 9))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（no-op 仍是成功）: %s", rec.Code, rec.Body.String())
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("no-op 广播了 %d 次, want 0", len(notifier.calls))
	}
}

// 个人路径同样不发 no-op 帧（一期这里会白发一帧让所有观看端重拉快照）。
func TestCanvasMutationNoOpDoesNotBroadcastForPersonal(t *testing.T) {
	repo := &fakeProjectRepository{
		project:       &projects.Project{ID: 42, UserID: 7},
		mutatePayload: json.RawMessage(`{"nodes":[{"id":"n1","type":"text","label":"same"}],"connections":[]}`),
		mutateVersion: 9,
	}
	notifier := &recordingNotifier{}
	handler := NewCanvasMutationHandler(repo, &fakeCanvasOwner{}, nil)
	handler.SetChangeNotifier(notifier)

	handler.PatchNode(httptest.NewRecorder(), newProjectCanvasRequest(t, http.MethodPatch,
		"/api/v1/projects/42/canvas/nodes/n1",
		[]byte(`{"ownerToken":"7:0:abcdef0123456789","patch":{"label":"same"}}`),
		map[string]string{"nodeID": "n1"}))

	if len(notifier.calls) != 0 {
		t.Fatalf("个人路径 no-op 广播了 %d 次, want 0", len(notifier.calls))
	}
}
