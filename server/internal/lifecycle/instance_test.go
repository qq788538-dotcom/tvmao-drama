package lifecycle

import (
	"strings"
	"testing"
)

func TestInstanceID_StableAndNonEmpty(t *testing.T) {
	a := InstanceID()
	b := InstanceID()
	if a == "" {
		t.Fatal("InstanceID 不应为空")
	}
	if a != b {
		t.Errorf("同进程内 InstanceID 应稳定: %q != %q", a, b)
	}
	if !strings.Contains(a, "-") {
		t.Errorf("InstanceID 应含分隔符, got %q", a)
	}
}
