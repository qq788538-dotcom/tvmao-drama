package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestAgentSmokeRunsWithStubLLM 是不用真实 Anthropic key 的情况下，最接近端到端
// agent_loop 的测试。跑通完整的 Run 路径，包括帧顺序和 repo 持久化。
func TestAgentSmokeRunsWithStubLLM(t *testing.T) {
	llm := &stubChatModel{chunks: [][]*schema.Message{
		{{Role: schema.Assistant, Content: "你好"}},
	}}
	sink := &stubFrameSink{}
	repo := newInMemRepo()
	repo.CreateThread(Thread{ID: "thr_smoke", ProjectID: 1, UserID: 1})

	a := &Agent{Planner: llm, Tools: newStubRegistryWith(&fakeTool{name: "fake_image"}), Repo: repo, Sink: sink}
	if err := a.Run(context.Background(), "thr_smoke", "act_smoke", "hi", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	types := frameTypes(sink.Frames())
	expectInOrder := []string{"thread_start", "action_start", "content_block_start", "content_block_delta", "content_block_stop", "action_end", "thread_end"}
	if !containsSubseq(types, expectInOrder) {
		t.Fatalf("expected order %v, got %v", expectInOrder, types)
	}
}

func containsSubseq(haystack, needle []string) bool {
	i := 0
	for _, h := range haystack {
		if i < len(needle) && h == needle[i] {
			i++
		}
	}
	return i == len(needle)
}
