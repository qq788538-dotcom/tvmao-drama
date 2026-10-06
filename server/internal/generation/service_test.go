package generation

import (
	"encoding/json"
	"testing"
)

func TestDurationMsFromInputs(t *testing.T) {
	cases := []struct {
		name   string
		inputs map[string]any
		want   int64
	}{
		{"整数秒", map[string]any{"duration": 5}, 5000},
		{"浮点秒", map[string]any{"duration": 5.5}, 5500},
		{"字符串带s", map[string]any{"duration": "6s"}, 6000},
		{"json.Number", map[string]any{"duration": json.Number("10")}, 10000},
		{"无duration字段", map[string]any{"resolution": "720p"}, 0},
		{"非正值", map[string]any{"duration": 0}, 0},
	}
	for _, c := range cases {
		if got := durationMsFromInputs(c.inputs); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
