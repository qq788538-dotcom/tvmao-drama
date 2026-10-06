package billing

import (
	"encoding/json"
	"strings"
	"testing"
)

// videoItem 是首个用到 extras 的真实形态：按秒 × 分辨率档位收费的视频模型。
func videoItem() ChargeItem {
	return ChargeItem{
		Unit: "second", Qty: "req:inputs.duration",
		Price: Price{By: []string{"req:inputs.resolution"}, Table: map[string]float64{"720p": 100, "480p": 46}},
	}
}

func videoReq(resolution string, videos any) map[string]any {
	inputs := map[string]any{"duration": "6s", "resolution": resolution}
	if videos != nil {
		inputs["videos"] = videos
	}
	return map[string]any{"inputs": inputs}
}

func extra(when string, factor, add float64) ExtraCharge {
	return ExtraCharge{When: when, Op: OpHasValue, Factor: factor, Add: add}
}

// 不配 extras 时结果必须与「没有这个特性」时完全一致。这是本次改动的验收线：
// 现网模型一个都没配 extras，任何一条对不上就是伤到了存量计费。
func TestExtras_AbsentKeepsLegacyResult(t *testing.T) {
	cases := []struct {
		name string
		cfg  ModelBilling
		req  map[string]any
		resp map[string]any
		want int64
	}{
		{"固定底费", ModelBilling{FlatFee: 50}, nil, nil, 50},
		{"秒×档位 720p", mb(videoItem()), videoReq("720p", nil), nil, 600},
		{"秒×档位 480p", mb(videoItem()), videoReq("480p", nil), nil, 276},
		{
			"token 三段",
			mb(ChargeItem{Unit: "token", Qty: "tokens.output", Price: Price{Fixed: 36}}),
			nil,
			map[string]any{"usage": map[string]any{"completion_tokens": float64(20000)}},
			1,
		},
		{
			"底费 + 计费项",
			ModelBilling{FlatFee: 10, Items: []ChargeItem{videoItem()}},
			videoReq("480p", nil), nil, 286,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.cfg.Extras != nil {
				t.Fatal("用例本身不该配 extras")
			}
			got, err := SettleAmount(c.cfg, c.req, c.resp)
			if err != nil {
				t.Fatalf("SettleAmount err: %v", err)
			}
			if got != c.want {
				t.Fatalf("SettleAmount = %d, want %d", got, c.want)
			}
		})
	}
}

func TestExtras_Settle(t *testing.T) {
	cases := []struct {
		name   string
		extras []ExtraCharge
		req    map[string]any
		want   int64
	}{
		{
			name:   "命中倍率：600 × 1.8",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    videoReq("720p", []any{"https://a/v.mp4"}),
			want:   1080,
		},
		{
			name:   "命中附加：600 + 200",
			extras: []ExtraCharge{extra("req:inputs.videos", 0, 200)},
			req:    videoReq("720p", []any{"https://a/v.mp4"}),
			want:   800,
		},
		{
			// add 加在乘法之外：(600×1.8)+200=1280，而不是 (600+200)×1.8=1440。
			name:   "倍率与附加叠加，add 在乘法外",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 200)},
			req:    videoReq("720p", []any{"https://a/v.mp4"}),
			want:   1280,
		},
		{
			name:   "空数组不命中",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    videoReq("720p", []any{}),
			want:   600,
		},
		{
			name:   "字段缺失不命中",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    videoReq("720p", nil),
			want:   600,
		},
		{
			name:   "字段为 null 不命中",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    videoReq("720p", nil),
			want:   600,
		},
		{
			// 分档价与倍率共存：480p 基础 276，×1.8 = 496.8 → ceil 497。
			// 配置里只写了一个 1.8，两个分辨率档都自动生效——这正是选倍率而非绝对价的理由。
			name:   "低档位同样 ×1.8",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    videoReq("480p", []any{"https://a/v.mp4"}),
			want:   497,
		},
		{
			name: "多规则命中：倍率连乘、附加相加",
			extras: []ExtraCharge{
				extra("req:inputs.videos", 1.5, 100),
				extra("req:inputs.audios", 2, 50),
			},
			req: map[string]any{"inputs": map[string]any{
				"duration": "6s", "resolution": "720p",
				"videos": []any{"v"}, "audios": []any{"a"},
			}},
			want: 1950, // 600 × 1.5 × 2 + 150
		},
		{
			name: "多规则只命中一条",
			extras: []ExtraCharge{
				extra("req:inputs.videos", 1.5, 0),
				extra("req:inputs.audios", 2, 0),
			},
			req:  videoReq("720p", []any{"v"}),
			want: 900,
		},
		{
			name:   "req 为 nil 不命中(agent 结算路径)",
			extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
			req:    nil,
			want:   0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := ModelBilling{Items: []ChargeItem{videoItem()}, Extras: c.extras}
			if c.req == nil {
				cfg.Items = nil // req 为 nil 时按量项取不到值会报错，与 extras 无关
			}
			got, err := SettleAmount(cfg, c.req, nil)
			if err != nil {
				t.Fatalf("SettleAmount err: %v", err)
			}
			if got != c.want {
				t.Fatalf("SettleAmount = %d, want %d", got, c.want)
			}
		})
	}
}

// 冻结与结算必须对同一 req 给出同一倍率判定——否则会出现「冻 1 倍、结 1.8 倍」扣穿余额，
// 或「冻 1.8 倍、结 1 倍」多冻用户的钱。
func TestExtras_FreezeMatchesSettle(t *testing.T) {
	cfg := ModelBilling{
		Items:  []ChargeItem{videoItem()},
		Extras: []ExtraCharge{extra("req:inputs.videos", 1.8, 200)},
	}
	for _, req := range []map[string]any{
		videoReq("720p", []any{"https://a/v.mp4"}),
		videoReq("720p", []any{}),
		videoReq("480p", []any{"https://a/v.mp4"}),
		videoReq("480p", nil),
	} {
		freeze := EstimateFreeze(cfg, req)
		settle, err := SettleAmount(cfg, req, nil)
		if err != nil {
			t.Fatalf("SettleAmount err: %v", err)
		}
		if freeze != settle {
			t.Fatalf("freeze=%d settle=%d 不一致, req=%v", freeze, settle, req)
		}
	}
}

// freezeEstimate 也要乘倍率：它兜的是 resp-only 项的上限，而 settle 会把那些项一起乘上
// 倍率。不乘的话 token 型模型配了 extras 就会冻得比结算少，结算时扣穿。
func TestExtras_FreezeEstimateAlsoScaled(t *testing.T) {
	cfg := ModelBilling{
		FreezeEstimate: 200,
		Items:          []ChargeItem{{Unit: "token", Qty: "tokens.output", Price: Price{Fixed: 36}}},
		Extras:         []ExtraCharge{extra("req:inputs.videos", 1.8, 0)},
	}
	req := map[string]any{"inputs": map[string]any{"videos": []any{"v"}}}
	if got := EstimateFreeze(cfg, req); got != 360 {
		t.Fatalf("EstimateFreeze = %d, want 360 (200 × 1.8)", got)
	}
	// 不命中时 freezeEstimate 原样。
	if got := EstimateFreeze(cfg, map[string]any{"inputs": map[string]any{}}); got != 200 {
		t.Fatalf("EstimateFreeze(未命中) = %d, want 200", got)
	}
}

// inputs 有两种来源：网页经 HTTP JSON 反序列化(数组是 []any)，CLI 经
// canvasinputs.BuildVideoInputs 在 Go 侧直接构造(数组是 []string)。两边判定必须一致，
// 否则同一个节点网页收 1.8 倍、CLI 收 1 倍。
func TestExtras_HasValueAcrossTypes(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"[]any 非空", []any{"x"}, true},
		{"[]any 空", []any{}, false},
		{"[]string 非空(CLI 路径)", []string{"x"}, true},
		{"[]string 空(CLI 路径)", []string{}, false},
		{"nil", nil, false},
		{"字符串非空", "x", true},
		{"字符串空", "", false},
		{"数字非零", float64(1), true},
		{"数字零", float64(0), false},
		{"int 非零", 3, true},
		{"bool true", true, true},
		{"bool false", false, false},
		{"map 非空", map[string]any{"a": 1}, true},
		{"map 空", map[string]any{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasValue(c.v); got != c.want {
				t.Fatalf("hasValue(%#v) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

func TestExtras_MatchRejectsNonReqSource(t *testing.T) {
	// resp: 前缀在保存时就被 ValidateExtras 拦下；万一存量数据里有，运行期也不能命中——
	// 冻结阶段没有 resp，命中了就会冻结与结算不一致。
	e := ExtraCharge{When: "resp:usage.total_tokens", Op: OpHasValue, Factor: 1.8}
	if matchExtra(e, map[string]any{"usage": map[string]any{"total_tokens": float64(10)}}) {
		t.Fatal("resp: 前缀不应命中")
	}
	// 未知 op 同理：兜底为不加价，而不是 panic 或报错。
	if matchExtra(ExtraCharge{When: "req:inputs.videos", Op: "regex", Factor: 1.8},
		map[string]any{"inputs": map[string]any{"videos": []any{"v"}}}) {
		t.Fatal("未知 op 不应命中")
	}
}

func TestExtras_JSONRoundTrip(t *testing.T) {
	raw := `{"flatFee":0,"freezeEstimate":0,"items":[],"extras":[{"when":"req:inputs.videos","op":"hasValue","factor":1.8,"note":"带参考视频"}]}`
	var got ModelBilling
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Extras) != 1 || got.Extras[0].When != "req:inputs.videos" || got.Extras[0].Factor != 1.8 {
		t.Fatalf("解析结果不对: %+v", got.Extras)
	}
	if got.Extras[0].Add != 0 {
		t.Fatalf("未填的 add 应为 0, got %v", got.Extras[0].Add)
	}
	// 老配置(无 extras)解析后必须是 nil，才能命中短路。
	var legacy ModelBilling
	if err := json.Unmarshal([]byte(`{"flatFee":50,"items":[]}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.Extras != nil {
		t.Fatalf("老配置的 Extras 应为 nil, got %+v", legacy.Extras)
	}
	// 序列化回去不能凭空写出 extras 字段(否则每次保存都会污染存量配置)。
	out, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "extras") {
		t.Fatalf("老配置序列化不应出现 extras: %s", out)
	}
}

func TestValidateExtras(t *testing.T) {
	cases := []struct {
		name    string
		extras  []ExtraCharge
		wantErr string // 空 = 应通过
	}{
		{"合法：倍率", []ExtraCharge{extra("req:inputs.videos", 1.8, 0)}, ""},
		{"合法：附加", []ExtraCharge{extra("req:inputs.videos", 0, 200)}, ""},
		{"合法：两者", []ExtraCharge{extra("req:inputs.videos", 1.8, 200)}, ""},
		{"合法：op 缺省", []ExtraCharge{{When: "req:inputs.videos", Factor: 1.8}}, ""},
		{"空规则集", nil, ""},
		{"缺 req: 前缀", []ExtraCharge{{When: "inputs.videos", Factor: 1.8}}, "必须以 req: 开头"},
		{"resp: 前缀", []ExtraCharge{{When: "resp:usage.x", Factor: 1.8}}, "必须以 req: 开头"},
		{"路径为空", []ExtraCharge{{When: "req:", Factor: 1.8}}, "参数路径不能为空"},
		{"未知 op", []ExtraCharge{{When: "req:inputs.videos", Op: "regex", Factor: 1.8}}, "不支持的条件类型"},
		{"倍率与附加都为 0", []ExtraCharge{{When: "req:inputs.videos"}}, "至少填一个"},
		{"负倍率", []ExtraCharge{{When: "req:inputs.videos", Factor: -1}}, "不能为负"},
		{"倍率过大", []ExtraCharge{{When: "req:inputs.videos", Factor: 1000}}, "不得超过 100"},
		{"附加过大", []ExtraCharge{{When: "req:inputs.videos", Add: 2_000_000}}, "不得超过 1000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateExtras(c.extras)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过, got err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应报错 %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("错误信息 %q 不含 %q", err.Error(), c.wantErr)
			}
		})
	}
}
