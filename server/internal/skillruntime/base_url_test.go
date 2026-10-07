package skillruntime

import "testing"

// M-1：剧本生成路径在 ResolveUpstream 交出的根地址后补一层 /v1；已带 /v1 的不重复补。
func TestNormalizeOpenAIBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://own.example":                     "https://own.example/v1",
		"https://own.example/":                    "https://own.example/v1",
		"https://own.example/v1":                  "https://own.example/v1",
		"https://own.example/v1/chat/completions": "https://own.example/v1",
	} {
		if got := normalizeOpenAIBaseURL(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
