package generation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// sink 注册表让 generation 能把结果交回给发起方，而自己不认识任何下游模块。
// 解耦的关键在于：generation 只读 kind，其余字段原样透传——所以新增一种结果去向
// 不需要改 generation，也不需要改表结构。

func TestSinkDispatchRoutesByKind(t *testing.T) {
	resetSinksForTest()
	var canvasGot, dramaGot json.RawMessage
	RegisterSink("canvas_node", func(_ context.Context, ref json.RawMessage, _ SinkResult) error {
		canvasGot = ref
		return nil
	})
	RegisterSink("drama_candidate", func(_ context.Context, ref json.RawMessage, _ SinkResult) error {
		dramaGot = ref
		return nil
	})

	sink := json.RawMessage(`{"kind":"canvas_node","projectId":12,"nodeId":"abc"}`)
	if err := dispatchSink(context.Background(), sink, SinkResult{TaskID: 7}); err != nil {
		t.Fatal(err)
	}
	if dramaGot != nil {
		t.Fatal("不该派发给别的 kind")
	}
	// 整个 JSON 原样交给 handler——generation 不解析 projectId/nodeId 这些字段。
	if string(canvasGot) != string(sink) {
		t.Fatalf("ref 应原样透传\n got = %s\nwant = %s", canvasGot, sink)
	}
}

// 没有 sink 的任务（老任务、调用方自取结果）不该被当成错误。
func TestSinkDispatchEmptyIsNoop(t *testing.T) {
	resetSinksForTest()
	if err := dispatchSink(context.Background(), nil, SinkResult{}); err != nil {
		t.Fatalf("空 sink 应 no-op, got %v", err)
	}
	if err := dispatchSink(context.Background(), json.RawMessage(`null`), SinkResult{}); err != nil {
		t.Fatalf("null sink 应 no-op, got %v", err)
	}
}

// 未注册的 kind 必须报错而不是静默丢弃：结果没人接收是配置事故,
// 静默会让"钱结算了但结果没写回去"无声发生。
func TestSinkDispatchUnknownKindErrors(t *testing.T) {
	resetSinksForTest()
	err := dispatchSink(context.Background(), json.RawMessage(`{"kind":"nobody"}`), SinkResult{TaskID: 9})
	if err == nil {
		t.Fatal("未注册的 kind 必须报错")
	}
	if !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("错误里要带上 kind 便于定位, got %v", err)
	}
}

// sink JSON 损坏时同样要报错,不能 panic 掉整个 worker。
func TestSinkDispatchMalformedErrors(t *testing.T) {
	resetSinksForTest()
	if err := dispatchSink(context.Background(), json.RawMessage(`{broken`), SinkResult{}); err == nil {
		t.Fatal("损坏的 sink JSON 应报错")
	}
}
