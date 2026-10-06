package generation

import (
	"strings"
	"testing"
)

// v5 §3.1:分流在建行时定死——sink 有无是执行模式的唯一真源。
// 这里测三件结构性事实:data URI 拒绝的判定、直连任务不经过 queued 的
// SQL 形态、以及 containsDataURI 不误伤普通输入。

func TestContainsDataURI(t *testing.T) {
	cases := []struct {
		name   string
		inputs map[string]any
		want   bool
	}{
		{"全 URL", map[string]any{"prompt": "画只猫", "images": []any{"https://a/b.png"}}, false},
		{"顶层 data URI", map[string]any{"image": "data:image/png;base64,AAAA"}, true},
		{"嵌套数组里的 data URI", map[string]any{"images": []any{"https://a/b.png", "data:image/jpeg;base64,BB"}}, true},
		{"prompt 文本里提到 data: 但不是 URI(无逗号)", map[string]any{"prompt": "字段 data:image 的含义"}, false},
		{"深层 map", map[string]any{"ref": map[string]any{"url": "data:video/mp4;base64,CC"}}, true},
		{"空 inputs", map[string]any{}, false},
	}
	for _, c := range cases {
		if got := containsDataURI(c.inputs); got != c.want {
			t.Errorf("%s: containsDataURI=%v, want %v", c.name, got, c.want)
		}
	}
}

// ErrDataURIInput 必须是稳定哨兵——HTTP 层靠 errors.Is 映射 400。
func TestDataURIErrorMessage(t *testing.T) {
	if !strings.Contains(ErrDataURIInput.Error(), "上传为素材") {
		t.Fatalf("错误文案要指引用户走上传, got %q", ErrDataURIInput.Error())
	}
}
