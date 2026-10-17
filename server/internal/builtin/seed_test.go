package builtin

import (
	"bytes"
	"encoding/json"
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
	if len(s.Models) != 27 || len(s.Settings) != 6 {
		t.Fatalf("models=%d settings=%d, want 27/6", len(s.Models), len(s.Settings))
	}
	byType := map[string]int{}
	for _, m := range s.Models {
		byType[m.Type]++
	}
	if byType["text"] != 2 || byType["image"] != 7 || byType["video"] != 16 || byType["audio"] != 2 {
		t.Fatalf("按类型计数 %v，want text 2 / image 7 / video 16 / audio 2（seed/README.md）", byType)
	}
	// agent_tools.source_model_id 引用生产 id，必须原样保留。
	if m, ok := s.Model("gpt-image-2"); !ok || m.ID != 9 || m.Type != "image" {
		t.Fatalf("gpt-image-2 = %+v, %v", m, ok)
	}
	// base_domain 默认留空，由用户在设置页填写。
	if st, ok := s.Setting("base_domain"); !ok || st.Value != "" {
		t.Fatalf("base_domain 种子应存在且为空：%+v %v", st, ok)
	}
	for _, key := range []string{"canvas_light_control", "canvas_multi_angle", "canvas_postprocess_config", "canvas_script_config", "gen_image_prompt_presets"} {
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

// 图片内嵌：除 edits 模式（multipart 读本地字节）和
// 模板按裸 base64 编写的模型外，不得再有 url。
func TestSeedImagesAreInline(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Models {
		if m.ModelID == "seedance-asset" {
			continue // 不经过 prepareImageInputs
		}
		if m.ImageInputFormat != "url" {
			continue
		}
		if m.ImageRequestMode == "openai_image_edits" {
			continue
		}
		t.Errorf("模型 %s 仍是 image_input_format=url，应为 data_uri", m.ModelID)
	}
}

// 合规校验走隐藏的内置模型（seedance-asset 设计 §2）。
func TestSeedHasHiddenSeedanceAssetModel(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Models {
		if m.ModelID != "seedance-asset" {
			continue
		}
		if !bool(m.Hidden) || !bool(m.Enabled) || m.BasePath != "/v1/images/generations" {
			t.Fatalf("seedance-asset 配置不对: %+v", m)
		}
		var tpl map[string]any
		if err := json.Unmarshal(m.RequestTemplate, &tpl); err != nil {
			t.Fatal(err)
		}
		md, _ := tpl["metadata"].(map[string]any)
		if tpl["model"] != "seedance-asset" || tpl["image"] != "{{image}}" || md["asset_type"] != "{{asset_type}}" {
			t.Fatalf("模板不对: %s", m.RequestTemplate)
		}
		return
	}
	t.Fatal("种子里没有 seedance-asset")
}

// 豆包 5.0-pro（1k/2k）上游最多接受 5 张参考图：经 gen_control.inputLimits 限制，
// 后端 generation 与前端图片节点共用这一配置。
func TestSeedSeedream5ProLimitsImagesToFive(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"doubao-seedream-5-0-pro-260628-1k": false, "doubao-seedream-5-0-pro-260628-2k": false}
	for _, m := range s.Models {
		if _, ok := want[m.ModelID]; !ok {
			continue
		}
		var gc struct {
			InputLimits map[string]struct {
				Max *int `json:"max"`
			} `json:"inputLimits"`
		}
		if err := json.Unmarshal(m.GenControl, &gc); err != nil {
			t.Fatal(err)
		}
		if lim, ok := gc.InputLimits["image"]; !ok || lim.Max == nil || *lim.Max != 5 {
			t.Errorf("%s 的 inputLimits.image.max 应为 5: %s", m.ModelID, m.GenControl)
		}
		want[m.ModelID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("种子里没有 %s", id)
		}
	}
}
