package generation

import (
	"context"
	"encoding/json"
	"fmt"
)

// sink：任务结果的去向。
//
// 生成任务的发起方各有各的落点——自由画布要写回 workflow_snapshots 的某个节点，
// 项目管理要写回候选图。以前每个发起方各自维护一张队列表 + 一个 worker 轮询
// generation 的任务状态，于是"持久化 + 接管 + 重试"这套逻辑被重复实现了两遍。
//
// 改成推送式之后，generation 执行完直接把结果交给发起方注册的 handler。关键在于
// generation **只认 kind，不解析 ref 里的其余字段**——所以新增一种去向既不用改
// generation，也不用改表结构（ref 是 JSON）。解耦靠的正是这份"不理解"。
type SinkHandler func(ctx context.Context, ref json.RawMessage, result SinkResult) error

// SinkResult 是交给发起方的执行结果。Err 非空表示任务最终失败
// （此时 OutputURLs/AssetIDs 为空），发起方据此更新自己的展示状态。
type SinkResult struct {
	TaskID int64
	// ModelID 供 handler 做模型相关分支（如 seedance 视频存上游直连地址）。
	ModelID    string
	OutputURLs []string
	// UpstreamURLs 是转存前的上游原始地址（22h 级时效），seedance 去字幕直连用。
	UpstreamURLs []string
	AssetIDs     []int64
	// Text 是文本生成任务的产物（chat 模型正文）。与 OutputURLs 互斥：
	// finalize 仅在没有任何媒体产物时才填它。
	Text string
	Err  string
}

// sinks 在进程启动时由 main.go 一次性注册完，之后只读，因此不加锁。
var sinks = map[string]SinkHandler{}

// RegisterSink 注册一种结果去向。同名重复注册以后者为准（便于测试替换）。
func RegisterSink(kind string, h SinkHandler) {
	if kind == "" || h == nil {
		return
	}
	sinks[kind] = h
}

// resetSinksForTest 只给测试用，避免用例之间互相污染注册表。
func resetSinksForTest() { sinks = map[string]SinkHandler{} }

// dispatchSink 把结果派发给 sink 声明的 handler。
//
// 空 sink 是合法的（老任务、或调用方自己取结果），直接 no-op。
// 但 kind 未注册必须报错——结果没人接收是配置事故，静默丢弃会让
// "钱结算了但结果没写回去"无声发生，那种不一致事后极难排查。
func dispatchSink(ctx context.Context, sink json.RawMessage, result SinkResult) error {
	if len(sink) == 0 || string(sink) == "null" {
		return nil
	}
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(sink, &head); err != nil {
		return fmt.Errorf("sink 解析失败 task=%d: %w", result.TaskID, err)
	}
	if head.Kind == "" {
		return fmt.Errorf("sink 缺少 kind task=%d", result.TaskID)
	}
	h, ok := sinks[head.Kind]
	if !ok {
		return fmt.Errorf("sink kind %q 未注册 task=%d", head.Kind, result.TaskID)
	}
	return h(ctx, sink, result)
}
