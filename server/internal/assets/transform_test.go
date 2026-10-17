package assets

import (
	"strings"
	"testing"
)

// 画布快照经 CanonicalizeJSON 落库：只该改字符串叶子，数字必须原样保留。
// 解成 float64 会把超过 2^53 的整数（CLI / 上游返回的 id、seed）悄悄改掉。
func TestTransformJSONRawKeepsNumbersVerbatim(t *testing.T) {
	raw := []byte(`{"seed":9007199254740993,"id":12345678901234567890,"x":1.50,"s":"a"}`)
	out := string(TransformJSONRaw(raw, strings.ToUpper))
	for _, want := range []string{`"seed":9007199254740993`, `"id":12345678901234567890`, `"x":1.50`, `"s":"A"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("out=%s，缺少 %s", out, want)
		}
	}
}
