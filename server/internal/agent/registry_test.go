package agent

import "testing"

// stubRegistry 是最小化的 ToolRegistry，给那些需要传一个非 nil registry、但不关心
// 它内容的测试用。
type stubRegistry struct{ byName map[string]Tool }

func (s *stubRegistry) Snapshot() []Tool {
	out := make([]Tool, 0, len(s.byName))
	for _, t := range s.byName {
		out = append(out, t)
	}
	return out
}
func (s *stubRegistry) ByName(name string) (Tool, bool) {
	t, ok := s.byName[name]
	return t, ok
}
func (s *stubRegistry) DisplayName(string) string { return "" }

func TestToolRegistryInterfaceContract(t *testing.T) {
	var _ ToolRegistry = (*stubRegistry)(nil) // 编译期断言

	r := &stubRegistry{byName: map[string]Tool{}}
	if len(r.Snapshot()) != 0 {
		t.Fatalf("empty registry should snapshot to empty slice")
	}
	if _, ok := r.ByName("missing"); ok {
		t.Fatal("ByName for missing key must return false")
	}
}
