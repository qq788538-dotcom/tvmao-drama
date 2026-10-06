package canvasgen

import "testing"

func TestLoadCanvasPostprocessConfig(t *testing.T) {
	cases := []struct {
		name    string
		stored  string
		wantN   int
		wantErr bool
	}{
		{"空配置", "", 0, false},
		{"一条普通+一条限时", `{"subtitleRemovalModels":[{"modelId":"desub-std","label":"标准去字幕","subtitle":"通用","isTimeLimited":false},{"modelId":"desub-sd","label":"限时去字幕","subtitle":"seedance","isTimeLimited":true}]}`, 2, false},
		{"坏JSON", `{not json`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := LoadCanvasPostprocessConfig(func(string) (string, error) { return c.stored, nil })
			if (err != nil) != c.wantErr { t.Fatalf("err=%v wantErr=%v", err, c.wantErr) }
			if err == nil && len(cfg.SubtitleRemovalModels) != c.wantN {
				t.Errorf("got %d bindings want %d", len(cfg.SubtitleRemovalModels), c.wantN)
			}
		})
	}
}

func TestDesubModel(t *testing.T) {
	cfg := CanvasPostprocessConfig{SubtitleRemovalModels: []CanvasDesubBinding{
		{ModelID: "a", IsTimeLimited: false}, {ModelID: "b", IsTimeLimited: true},
	}}
	if _, ok := cfg.DesubModel("a"); !ok { t.Error("a 应命中") }
	if dm, ok := cfg.DesubModel("b"); !ok || !dm.IsTimeLimited { t.Error("b 应命中且限时") }
	if _, ok := cfg.DesubModel("x"); ok { t.Error("x 不应命中") }
}

// 旧版单模型 upscaleModel 读时折成一项列表——上线当下存量配置不失效。
func TestLoadCanvasPostprocessConfig_LegacyUpscaleModel(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		want   []string
	}{
		{"只有旧字段", `{"upscaleModel":"upscale-hd"}`, []string{"upscale-hd"}},
		{"新列表优先于残留旧字段", `{"upscaleModel":"old","upscaleModels":[{"modelId":"a","label":"甲"},{"modelId":"b"}]}`, []string{"a", "b"}},
		{"旧字段为空", `{"upscaleModel":""}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := LoadCanvasPostprocessConfig(func(string) (string, error) { return c.stored, nil })
			if err != nil { t.Fatal(err) }
			var got []string
			for _, u := range cfg.UpscaleModels { got = append(got, u.ModelID) }
			if len(got) != len(c.want) { t.Fatalf("got %v want %v", got, c.want) }
			for i := range got {
				if got[i] != c.want[i] { t.Fatalf("got %v want %v", got, c.want) }
			}
			if cfg.UpscaleModel != "" { t.Errorf("旧字段读后应清空, got %q", cfg.UpscaleModel) }
		})
	}
}

func TestUpscaleBinding(t *testing.T) {
	cfg := CanvasPostprocessConfig{UpscaleModels: []CanvasUpscaleBinding{{ModelID: "a", Label: "甲"}, {ModelID: "b"}}}
	if u, ok := cfg.UpscaleBinding("a"); !ok || u.Label != "甲" { t.Error("a 应命中且带显示名") }
	if _, ok := cfg.UpscaleBinding("b"); !ok { t.Error("b 应命中") }
	if _, ok := cfg.UpscaleBinding("x"); ok { t.Error("x 不应命中") }
	if _, ok := (CanvasPostprocessConfig{}).UpscaleBinding(""); ok { t.Error("空 modelID 不应命中") }
}
