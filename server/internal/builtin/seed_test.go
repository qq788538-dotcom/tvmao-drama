package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEmbeddedSeed(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Models) != 42 || len(s.Settings) != 6 {
		t.Fatalf("models=%d settings=%d, want 42/6", len(s.Models), len(s.Settings))
	}
	byType := map[string]int{}
	for _, m := range s.Models {
		byType[m.Type]++
	}
	if byType["text"] != 4 || byType["image"] != 11 || byType["video"] != 25 || byType["audio"] != 2 {
		t.Fatalf("按类型计数 %v，want text 4 / image 11 / video 25 / audio 2（seed/README.md）", byType)
	}
	// agent_tools.source_model_id 引用生产 id，必须原样保留。
	if m, ok := s.Model("gpt-image-2"); !ok || m.ID != 9 || m.Type != "image" {
		t.Fatalf("gpt-image-2 = %+v, %v", m, ok)
	}
	for _, key := range []string{"base_domain", "canvas_light_control", "canvas_multi_angle", "canvas_postprocess_config", "canvas_script_config", "gen_image_prompt_presets"} {
		if st, ok := s.Setting(key); !ok || st.Value == "" {
			t.Fatalf("缺少种子配置 %s", key)
		}
	}
	if len(s.Hash) != 64 {
		t.Fatalf("hash=%q", s.Hash)
	}
}

// seeddata/ 必须与仓库根 seed/ 逐字节一致（seed/ 是真源）。
func TestEmbeddedSeedMatchesRepoSeed(t *testing.T) {
	for _, name := range []string{"model_configs.jsonl", "settings.jsonl"} {
		embedded, err := seedFS.ReadFile("seeddata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		repo, err := os.ReadFile(filepath.Join("..", "..", "..", "seed", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, repo) {
			t.Fatalf("server/internal/builtin/seeddata/%s 与 seed/%s 不一致：运行 scripts/sync-seed.sh", name, name)
		}
	}
}

const okModelLine = `{"id":1,"type":"image","name":"A","model_id":"a","base_path":"/v1/a","enabled":1,"hidden":0,"sort_order":0,"request_template":{"model":"a"},"polling_template":null,"ui_schema":null,"billing_config":{"items":null,"flatFee":0,"freezeEstimate":0},"gen_control":null,"image_input_format":"url","image_request_mode":"json","admin_note":"","price_note":"","created_at":"","updated_at":""}`

func TestParseSeedRejectsBadInput(t *testing.T) {
	okSetting := `{"key":"base_domain","value":"https://x.example","updated_at":""}`
	cases := map[string]struct{ models, settings, want string }{
		"重复 id":       {okModelLine + "\n" + strings.Replace(okModelLine, `"model_id":"a"`, `"model_id":"b"`, 1), okSetting, "id 1 重复"},
		"重复 model_id": {okModelLine + "\n" + strings.Replace(okModelLine, `"id":1`, `"id":2`, 1), okSetting, "model_id a 重复"},
		"未知列":         {strings.Replace(okModelLine, `"id":1`, `"id":1,"new_col":1`, 1), okSetting, "unknown field"},
		"非法布尔":        {strings.Replace(okModelLine, `"enabled":1`, `"enabled":2`, 1), okSetting, "布尔值"},
		"请求模板为空":      {strings.Replace(okModelLine, `"request_template":{"model":"a"}`, `"request_template":null`, 1), okSetting, "request_template"},
		"非法加价规则":      {strings.Replace(okModelLine, `"items":null`, `"items":null,"extras":[{"when":"bad","add":1}]`, 1), okSetting, "req:"},
		"密钥类配置":       {okModelLine, `{"key":"upstream_api_key","value":"sk-x","updated_at":""}`, "密钥"},
		"重复配置 key":    {okModelLine, okSetting + "\n" + okSetting, "key base_domain 重复"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSeed([]byte(c.models), []byte(c.settings))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v, want 包含 %q", err, c.want)
			}
		})
	}
}

func TestSeedHashChangesWithContent(t *testing.T) {
	setting := []byte(`{"key":"base_domain","value":"https://x.example","updated_at":""}`)
	a, err := parseSeed([]byte(okModelLine), setting)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseSeed([]byte(strings.Replace(okModelLine, `"name":"A"`, `"name":"B"`, 1)), setting)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash == b.Hash {
		t.Fatal("种子内容变了，哈希必须变")
	}
}

func TestSeedModelPartsMapsNullJSONToNil(t *testing.T) {
	s, err := parseSeed([]byte(okModelLine), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Models[0].parts()
	if p.UISchema != nil || p.GenControl != nil || !p.Enabled || p.Hidden || p.Name != "A" {
		t.Fatalf("parts=%+v", p)
	}
}
