package canvasowner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLocalClaimIssuesUniqueThreeSegmentTokens(t *testing.T) {
	l := NewLocal()
	a, err := l.Claim(context.Background(), 42, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := l.Claim(context.Background(), 42, 1, 0)
	if a.OwnerToken == b.OwnerToken {
		t.Fatal("每次 Claim 应发不同的 token（前端靠它区分标签页做回声识别）")
	}
	segs := strings.Split(a.OwnerToken, ":")
	if len(segs) != 3 || segs[0] != "1" || segs[1] != "0" || len(segs[2]) != 32 {
		t.Fatalf("token=%q want <uid>:<actor>:<32位hex>", a.OwnerToken)
	}
	if !a.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiresAt=%v 应在未来", a.ExpiresAt)
	}
	if SourceMark(a.OwnerToken) != segs[2][:8] {
		t.Fatalf("SourceMark=%q", SourceMark(a.OwnerToken))
	}
}

// 单用户单窗口（spec S11、X2）：锁永远放行，包括前端不再 Claim 时发来的空 token。
func TestLocalNeverRejects(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()
	for _, tok := range []string{"", "stale", "7:0:abcdef"} {
		if err := l.Require(ctx, 42, tok); err != nil {
			t.Fatalf("Require(%q)=%v", tok, err)
		}
		if err := l.Heartbeat(ctx, 42, tok); err != nil {
			t.Fatalf("Heartbeat(%q)=%v", tok, err)
		}
	}
	if res, err := l.Probe(ctx, 42); err != nil || res.Held {
		t.Fatalf("Probe=%+v err=%v，want 无人持锁", res, err)
	}
}

func TestClaimResultJSONTags(t *testing.T) {
	payload, err := json.Marshal(ClaimResult{
		OwnerToken: "7:token",
		ExpiresAt:  time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal err=%v", err)
	}
	got := string(payload)
	if !strings.Contains(got, `"ownerToken":"7:token"`) {
		t.Fatalf("ClaimResult JSON missing ownerToken tag: %s", got)
	}
	if !strings.Contains(got, `"expiresAt":`) {
		t.Fatalf("ClaimResult JSON missing expiresAt tag: %s", got)
	}
	if strings.Contains(got, "OwnerToken") || strings.Contains(got, "ExpiresAt") {
		t.Fatalf("ClaimResult JSON used Go field names: %s", got)
	}
}

func TestSourceMarkTakesFirstEightHexOfLastSegment(t *testing.T) {
	if got := SourceMark("7:42:0123456789abcdef"); got != "01234567" {
		t.Fatalf("SourceMark 三段式 got=%q, want 01234567", got)
	}
	if got := SourceMark("7:0123456789abcdef"); got != "01234567" {
		t.Fatalf("SourceMark 两段式 got=%q, want 01234567", got)
	}
	if got := SourceMark(""); got != "" {
		t.Fatalf("SourceMark 空 token got=%q, want 空", got)
	}
	token := "7:42:0123456789abcdef"
	got := SourceMark(token)
	if len(got) != 8 {
		t.Fatalf("SourceMark got=%q(len=%d), want 8 位", got, len(got))
	}
	if got == token || strings.Contains(got, "89abcdef") {
		t.Fatalf("SourceMark got=%q 泄露了 token 内容", got)
	}
}
