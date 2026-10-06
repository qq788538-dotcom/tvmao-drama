package models

import (
	"encoding/json"
	"testing"
)

func TestGenConcurrency_DefaultsTo200WhenMissingOrZero(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"nil gen_control", "", DefaultGenConcurrency},
		{"empty object", `{}`, DefaultGenConcurrency},
		{"concurrency 0 → default", `{"concurrency":0}`, DefaultGenConcurrency},
		{"explicit value", `{"concurrency":5}`, 5},
		{"corrupt json → default", `{not json`, DefaultGenConcurrency},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := ModelConfig{}
			if c.raw != "" {
				m.GenControlRaw = json.RawMessage(c.raw)
			}
			if got := m.GenConcurrency(); got != c.want {
				t.Fatalf("GenConcurrency()=%d, want %d", got, c.want)
			}
		})
	}
}

func TestGenRetry_PerModuleAndZeroWhenAbsent(t *testing.T) {
	m := ModelConfig{GenControlRaw: json.RawMessage(
		`{"concurrency":5,"modules":{"canvas":{"maxRetries":3,"retryIntervalSec":30},"drama":{"maxRetries":5,"retryIntervalSec":20}}}`,
	)}
	if got := m.GenRetry("canvas"); got.MaxRetries != 3 || got.RetryIntervalSec != 30 {
		t.Fatalf("canvas retry = %+v, want {3,30}", got)
	}
	if got := m.GenRetry("drama"); got.MaxRetries != 5 || got.RetryIntervalSec != 20 {
		t.Fatalf("drama retry = %+v, want {5,20}", got)
	}
	// 未配置的模块 → 零值 = 不重试
	if got := m.GenRetry("unknown"); got.MaxRetries != 0 || got.RetryIntervalSec != 0 {
		t.Fatalf("unknown module retry = %+v, want zero", got)
	}
}

func TestGenRetry_ZeroWhenNoGenControl(t *testing.T) {
	m := ModelConfig{}
	if got := m.GenRetry("canvas"); got.MaxRetries != 0 || got.RetryIntervalSec != 0 {
		t.Fatalf("retry without gen_control = %+v, want zero", got)
	}
}

func TestInputLimits(t *testing.T) {
	t.Run("未配置返回 nil", func(t *testing.T) {
		if (ModelConfig{}).InputLimits() != nil {
			t.Fatal("未配置应返回 nil（调用方按无约束放行）")
		}
	})
	t.Run("解析 min/max 并保留显式 0", func(t *testing.T) {
		m := ModelConfig{GenControlRaw: json.RawMessage(
			`{"inputLimits":{"video":{"min":1,"max":1},"image":{},"audio":{"max":0}}}`)}
		lim := m.InputLimits()
		if v := lim["video"]; v.Min == nil || *v.Min != 1 || v.Max == nil || *v.Max != 1 {
			t.Fatalf("video 解析错: %+v", v)
		}
		if img := lim["image"]; img.Min != nil || img.Max != nil {
			t.Fatalf("image 空对象应两端皆 nil（不限）: %+v", img)
		}
		// max:0 是「禁止连接」，必须与「缺失」区分——这就是用指针的全部理由。
		if a := lim["audio"]; a.Max == nil || *a.Max != 0 || a.Min != nil {
			t.Fatalf("audio max:0 应保留为显式 0: %+v", a)
		}
	})
	t.Run("不认识的键静默忽略", func(t *testing.T) {
		m := ModelConfig{GenControlRaw: json.RawMessage(
			`{"inputLimits":{"video":{"min":1,"maxDurationSec":30},"total":{"max":4}}}`)}
		if v := m.InputLimits()["video"]; v.Min == nil || *v.Min != 1 {
			t.Fatal("已知键应照常解析，未知兄弟键不影响")
		}
	})
}

func TestInputDuration(t *testing.T) {
	t.Run("未配置返回 nil", func(t *testing.T) {
		if (ModelConfig{}).InputDuration() != nil {
			t.Fatal("未配置应返回 nil")
		}
	})
	t.Run("from video 解析", func(t *testing.T) {
		m := ModelConfig{GenControlRaw: json.RawMessage(`{"inputDuration":{"from":"video"}}`)}
		cfg := m.InputDuration()
		if cfg == nil || cfg.From != "video" {
			t.Fatalf("应解析出 from=video: %+v", cfg)
		}
	})
	t.Run("未知 from 视同未配置", func(t *testing.T) {
		m := ModelConfig{GenControlRaw: json.RawMessage(`{"inputDuration":{"from":"audio"}}`)}
		if m.InputDuration() != nil {
			t.Fatal("from=audio 当前应视同未配置（宽松忽略，为将来留形状）")
		}
	})
	t.Run("与 inputLimits 共存互不干扰", func(t *testing.T) {
		m := ModelConfig{GenControlRaw: json.RawMessage(
			`{"inputLimits":{"video":{"min":1,"max":1}},"inputDuration":{"from":"video"}}`)}
		if m.InputDuration() == nil || m.InputLimits() == nil {
			t.Fatal("两个键应各自独立解析")
		}
	})
}
