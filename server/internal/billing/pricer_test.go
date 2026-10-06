package billing

import (
	"strings"
	"testing"
)

func mb(items ...ChargeItem) ModelBilling { return ModelBilling{Items: items} }

func TestSettleAmount_Unified(t *testing.T) {
	cases := []struct {
		name string
		cfg  ModelBilling
		req  map[string]any
		resp map[string]any
		want int64
	}{
		{
			name: "per_call 固定费",
			cfg:  ModelBilling{FlatFee: 50},
			want: 50,
		},
		{
			name: "视频 秒×分辨率矩阵 720p",
			cfg:  mb(ChargeItem{Unit: "second", Qty: "req:inputs.duration", Price: Price{By: []string{"req:inputs.resolution"}, Table: map[string]float64{"720p": 100, "480p": 46}}}),
			req:  map[string]any{"inputs": map[string]any{"duration": "6s", "resolution": "720p"}},
			want: 600,
		},
		{
			name: "图片 张数×2K/4K",
			cfg:  mb(ChargeItem{Unit: "image", Qty: "req:inputs.n", Price: Price{By: []string{"req:inputs.size"}, Table: map[string]float64{"2k": 12, "4k": 40}}}),
			req:  map[string]any{"inputs": map[string]any{"n": float64(2), "size": "4k"}},
			want: 80,
		},
		{
			name: "复合档位 2k|hd",
			cfg:  mb(ChargeItem{Unit: "image", Qty: "flat", Price: Price{By: []string{"req:inputs.size", "req:inputs.quality"}, Table: map[string]float64{"2k|hd": 20, "2k|sd": 8}}}),
			req:  map[string]any{"inputs": map[string]any{"size": "2k", "quality": "hd"}},
			want: 20,
		},
		{
			name: "token 三段(prompt含cached)",
			cfg: mb(
				ChargeItem{Unit: "token", Qty: "tokens.inputNonCached", Price: Price{Fixed: 0.5}},
				ChargeItem{Unit: "token", Qty: "tokens.cached", Price: Price{Fixed: 0.05}},
				ChargeItem{Unit: "token", Qty: "tokens.output", Price: Price{Fixed: 0.5}},
			),
			resp: map[string]any{"usage": map[string]any{
				"prompt_tokens": float64(14743), "completion_tokens": float64(13039),
				"prompt_tokens_details": map[string]any{"cached_tokens": float64(4864)},
			}},
			want: 1,
		},
		{
			name: "flatFee + 计费项相加",
			cfg:  ModelBilling{FlatFee: 5, Items: []ChargeItem{{Unit: "second", Qty: "req:inputs.duration", Price: Price{Fixed: 10}}}},
			req:  map[string]any{"inputs": map[string]any{"duration": "3s"}},
			want: 35,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SettleAmount(c.cfg, c.req, c.resp)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %d want %d", got, c.want)
			}
		})
	}
}

func TestSettleAmount_Errors(t *testing.T) {
	cfg := mb(ChargeItem{Unit: "image", Qty: "flat", Price: Price{By: []string{"req:inputs.size"}, Table: map[string]float64{"2k": 1}}})
	if _, err := SettleAmount(cfg, map[string]any{"inputs": map[string]any{"size": "8k"}}, nil); err == nil {
		t.Fatal("缺档位键应报错")
	}
	cfg2 := mb(ChargeItem{Unit: "second", Qty: "req:inputs.duration", Price: Price{Fixed: 1}})
	if _, err := SettleAmount(cfg2, map[string]any{"inputs": map[string]any{}}, nil); err == nil {
		t.Fatal("量取不到应报错")
	}
}

func TestEstimateFreeze_Unified(t *testing.T) {
	cfg := mb(ChargeItem{Unit: "second", Qty: "req:inputs.duration", Price: Price{By: []string{"req:inputs.resolution"}, Table: map[string]float64{"720p": 100}}})
	got := EstimateFreeze(cfg, map[string]any{"inputs": map[string]any{"duration": "6s", "resolution": "720p"}})
	if got != 600 {
		t.Errorf("video freeze got %d want 600", got)
	}
	cfgT := ModelBilling{FreezeEstimate: 200, Items: []ChargeItem{{Unit: "token", Qty: "tokens.output", Price: Price{Fixed: 0.5}}}}
	if got := EstimateFreeze(cfgT, nil); got != 200 {
		t.Errorf("token freeze got %d want 200", got)
	}
	if got := EstimateFreeze(ModelBilling{FlatFee: 50}, nil); got != 50 {
		t.Errorf("flat freeze got %d want 50", got)
	}
}

// 小数秒不足一秒按一秒：先 ceil 秒数再乘单价（ceil(q)*p），不是把小数秒折进
// 积分尾数后再 ceil（ceil(q*p)）。音频/按实际时长结算的模型上游返回 4.58 这类值。
func TestSettleAmount_FractionalSecondsCeil(t *testing.T) {
	cfg := mb(ChargeItem{Unit: "second", Qty: "req:inputs.duration", Price: Price{Fixed: 10}})
	got, err := SettleAmount(cfg, map[string]any{"inputs": map[string]any{"duration": "0.5s"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 { // ceil(0.5)=1 × 10 = 10
		t.Errorf("got %d want 10", got)
	}

	got, err = SettleAmount(cfg, map[string]any{"inputs": map[string]any{"duration": "4.58s"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 50 { // ceil(4.58)=5 × 10 = 50；旧算法 ceil(4.58×10)=46
		t.Errorf("got %d want 50", got)
	}
}

func TestPriceJSON_RoundTrip(t *testing.T) {
	// 裸数字 → Fixed
	var p Price
	if err := p.UnmarshalJSON([]byte(`36`)); err != nil || p.Fixed != 36 {
		t.Fatalf("bare number parse: %+v err=%v", p, err)
	}
	// 对象 → by/table
	var p2 Price
	if err := p2.UnmarshalJSON([]byte(`{"by":["req:inputs.size"],"table":{"2k":12}}`)); err != nil || len(p2.By) != 1 || p2.Table["2k"] != 12 {
		t.Fatalf("object parse: %+v err=%v", p2, err)
	}
}

func TestSettleAmount_RangeTiers(t *testing.T) {
	mx := func(f float64) *float64 { return &f }
	// 整段按档：unit=image+qty=flat → amount = 1×rate = rate（避开 token 除法/ceil 噪音，直接验档位选择）
	cfg := mb(ChargeItem{Unit: "image", Qty: "flat", Price: Price{
		ByRange: "req:inputs.ctx",
		Tiers: []PriceTier{
			{MaxTokens: mx(64000), Price: 5},
			{MaxTokens: mx(128000), Price: 8},
			{Price: 12}, // 兜底 >128K
		},
	}})
	mk := func(ctx float64) map[string]any { return map[string]any{"inputs": map[string]any{"ctx": ctx}} }
	cases := map[float64]int64{50000: 5, 64000: 5, 64001: 8, 128000: 8, 200000: 12}
	for ctx, want := range cases {
		got, err := SettleAmount(cfg, mk(ctx), nil)
		if err != nil {
			t.Fatalf("ctx=%v err=%v", ctx, err)
		}
		if got != want {
			t.Errorf("ctx=%v got %d want %d", ctx, got, want)
		}
	}
	// 无兜底档且超出 → 报错
	cfgNoCatch := mb(ChargeItem{Unit: "image", Qty: "flat", Price: Price{ByRange: "req:inputs.ctx", Tiers: []PriceTier{{MaxTokens: mx(64000), Price: 5}}}})
	if _, err := SettleAmount(cfgNoCatch, mk(99999), nil); err == nil {
		t.Fatal("超出所有档且无兜底应报错")
	}
	// 无兜底档但落在档内 → 正常返回(验 maxTokens 含上限 <= 语义)
	if v, err := SettleAmount(cfgNoCatch, mk(64000), nil); err != nil || v != 5 {
		t.Errorf("cfgNoCatch ctx=64000 应=5, got %d err=%v", v, err)
	}
	// byRange 用 req 字段 + qty=flat → 可精确预冻结
	if EstimateFreeze(cfg, mk(200000)) != 12 {
		t.Errorf("byRange(req) 应精确冻结=12")
	}
}

func TestResolvePrice_TiersWithoutByRange(t *testing.T) {
	cfg := mb(ChargeItem{Unit: "image", Qty: "flat", Price: Price{Tiers: []PriceTier{{Price: 5}}}})
	if _, err := SettleAmount(cfg, nil, nil); err == nil {
		t.Fatal("tiers 无 byRange 应报错")
	}
}

func TestSettleAmount_CharsInput(t *testing.T) {
	msgs := func(contents ...any) map[string]any {
		arr := make([]any, len(contents))
		for i, c := range contents {
			arr[i] = map[string]any{"role": "user", "content": c}
		}
		return map[string]any{"messages": arr}
	}
	cases := []struct {
		name string
		cfg  ModelBilling
		req  map[string]any
		want int64
	}{
		{
			name: "线性:200字符×0.1",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", Price: Price{Fixed: 0.1}}),
			req:  msgs(strings.Repeat("a", 200)),
			want: 20,
		},
		{
			name: "按rune计中文:100汉字×0.1",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", Price: Price{Fixed: 0.1}}),
			req:  msgs(strings.Repeat("中", 100)),
			want: 10,
		},
		{
			name: "多条message求和:60+40字符×0.1",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", Price: Price{Fixed: 0.1}}),
			req:  msgs(strings.Repeat("a", 60), strings.Repeat("b", 40)),
			want: 10,
		},
		{
			name: "多模态content只计text部分",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", Price: Price{Fixed: 0.1}}),
			req: msgs([]any{
				map[string]any{"type": "text", "text": strings.Repeat("a", 100)},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/x.png"}},
			}),
			want: 10,
		},
		{
			name: "按段:150字符每100字一段不足一段按一段×10",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", BlockSize: 100, Price: Price{Fixed: 10}}),
			req:  msgs(strings.Repeat("a", 150)),
			want: 20,
		},
		{
			name: "按段:整段边界100字符=1段",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", BlockSize: 100, Price: Price{Fixed: 10}}),
			req:  msgs(strings.Repeat("a", 100)),
			want: 10,
		},
		{
			name: "按段:段大小500可配",
			cfg:  mb(ChargeItem{Unit: "char", Qty: "chars.input", BlockSize: 500, Price: Price{Fixed: 10}}),
			req:  msgs(strings.Repeat("a", 501)),
			want: 20,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SettleAmount(c.cfg, c.req, nil)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %d want %d", got, c.want)
			}
		})
	}
}

func TestSettleAmount_CharsInput_Errors(t *testing.T) {
	cfg := mb(ChargeItem{Unit: "char", Qty: "chars.input", Price: Price{Fixed: 0.1}})
	if _, err := SettleAmount(cfg, nil, nil); err == nil {
		t.Fatal("req 为 nil 应报错(不静默扣0)")
	}
	if _, err := SettleAmount(cfg, map[string]any{"inputs": map[string]any{}}, nil); err == nil {
		t.Fatal("req.messages 缺失应报错")
	}
}

func TestEstimateFreeze_CharsInput(t *testing.T) {
	// chars.input 只依赖 req → 发起时可精确预冻结,冻结=结算,不需要 freezeEstimate
	cfg := mb(ChargeItem{Unit: "char", Qty: "chars.input", BlockSize: 100, Price: Price{Fixed: 10}})
	req := map[string]any{"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("a", 150)}}}
	if got := EstimateFreeze(cfg, req); got != 20 {
		t.Errorf("chars freeze got %d want 20", got)
	}
}
