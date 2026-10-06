package canvasscript

import (
	"encoding/json"
	"testing"
)

func TestVisibleTo(t *testing.T) {
	cases := []struct {
		name     string
		entry    StyleEntry
		userTags []string
		want     bool
	}{
		{"空标签全员可见", StyleEntry{}, nil, true},
		{"命中任一标签放行", StyleEntry{UserTags: []string{"vip", "beta"}}, []string{"beta"}, true},
		{"未命中拒绝", StyleEntry{UserTags: []string{"vip"}}, []string{"normal"}, false},
		{"用户无标签且风格限定则拒绝", StyleEntry{UserTags: []string{"vip"}}, nil, false},
	}
	for _, c := range cases {
		if got := c.entry.VisibleTo(c.userTags); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	var zero Config
	if zero.concurrency() != 1 || zero.maxAttempts() != 2 {
		t.Errorf("默认并发 1、共 2 次: conc=%d attempts=%d", zero.concurrency(), zero.maxAttempts())
	}
	if c := (Config{Concurrency: 3, MaxRetries: 2}); c.concurrency() != 3 || c.maxAttempts() != 3 {
		t.Errorf("显式配置未生效: conc=%d attempts=%d", c.concurrency(), c.maxAttempts())
	}
	if bad := (Config{Concurrency: -1, MaxRetries: 99}); bad.concurrency() != 1 || bad.maxAttempts() != 2 {
		t.Errorf("越界值应回默认: conc=%d attempts=%d", bad.concurrency(), bad.maxAttempts())
	}
}

func TestValidateCandidate(t *testing.T) {
	if err := validateCandidate(`{"storyboard":[{"name":"镜1"}]}`); err != nil {
		t.Errorf("合法产物不应报错: %v", err)
	}
	for _, bad := range []string{`not json`, `{"storyboard":[]}`, `{"character":[]}`} {
		if err := validateCandidate(bad); err == nil {
			t.Errorf("%s 应报错", bad)
		}
	}
}

func TestStyleKey(t *testing.T) {
	if k := (StyleEntry{Name: "真人短剧"}).Key(); k != "真人短剧" {
		t.Errorf("无版本 Key=%q", k)
	}
	if k := (StyleEntry{Name: "真人短剧", Version: "v2"}).Key(); k != "真人短剧 v2" {
		t.Errorf("带版本 Key=%q", k)
	}
}

func TestPassthroughModelFields(t *testing.T) {
	got := passthroughModelFields(json.RawMessage(`{"model":"x","messages":"{{messages}}","stream":true,"max_tokens":8000,"prompt":"{{prompt}}","reasoning_effort":"high"}`))
	if len(got) != 2 || got["max_tokens"] != float64(8000) || got["reasoning_effort"] != "high" {
		t.Fatalf("只应透传 max_tokens 与 reasoning_effort: %#v", got)
	}
	if passthroughModelFields(json.RawMessage(`not json`)) != nil || passthroughModelFields(nil) != nil {
		t.Fatal("坏模板或空模板应返回 nil")
	}
}
