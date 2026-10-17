package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// 合规素材注册在内置模型 seedance-asset 解析出的上游账号下（seedance-asset 设计 §2，
// 取代 X5「恒走全局」）。生成模型解析出的上游与它不同、且请求引用了已校验的 asset://
// 素材时，提交前直接报错，不调上游。

// mapModels 按 model_id 区分生成模型与 seedance-asset。
type mapModels map[string]*models.ModelConfig

func (m mapModels) GetByModelID(id string) (*models.ModelConfig, error) {
	if c, ok := m[id]; ok {
		return c, nil
	}
	return nil, models.ErrNotFound
}

func seedanceSvc(m, asset *models.ModelConfig) *Service {
	s := planService(m, okSettings(), "sk-global")
	lookup := mapModels{m.ModelID: m}
	if asset != nil {
		lookup[asset.ModelID] = asset
	}
	s.models = lookup
	s.storage = fakeKeyStorage{}
	s.realPerson = fakeRPResolver{resolve: func(_ int64, key string) (string, error) {
		if key == "library/1/a.png" {
			return "asset://A1", nil
		}
		return "", nil
	}}
	return s
}

func seedanceModel(base, key string) *models.ModelConfig {
	return &models.ModelConfig{ModelID: "doubao-seedance-2-0", Enabled: true, RequestTemplate: json.RawMessage(`{"images":"{{images}}"}`),
		UpstreamBaseURL: base, UpstreamAPIKey: key}
}

func assetModel(base, key string) *models.ModelConfig {
	return &models.ModelConfig{ModelID: "seedance-asset", Enabled: true, Hidden: true,
		UpstreamBaseURL: base, UpstreamAPIKey: key}
}

const wantMismatchText = "已校验素材注册在合规校验模型 seedance-asset 所用的上游账号下，当前模型使用的上游与之不同，无法引用。请让两者使用同一上游，或改用与合规校验相同的上游后重新生成。"

func TestSeedanceVerifiedAssetRejectedOnDifferentUpstream(t *testing.T) {
	cases := map[string]struct{ gen, asset *models.ModelConfig }{
		"生成模型独立上游、合规走全局":   {seedanceModel("https://other.example", "sk-other"), assetModel("", "")},
		"合规模型独立上游、生成模型走全局": {seedanceModel("", ""), assetModel("https://u1.example", "sk-u1")},
		"两者各自不同的独立上游":      {seedanceModel("https://other.example", "sk-other"), assetModel("https://u1.example", "sk-u1")},
	}
	for name, c := range cases {
		for refName, ref := range map[string]string{
			"命中已校验素材":           "media://library/1/a.png",
			"快照里已是 asset:// 引用": "asset://A1",
		} {
			t.Run(name+"/"+refName, func(t *testing.T) {
				_, err := seedanceSvc(c.gen, c.asset).prepareExecution(context.Background(), 1, c.gen.ModelID, map[string]any{"images": []any{ref}}, "", "", nil)
				if !errors.Is(err, ErrVerifiedAssetUpstreamMismatch) {
					t.Fatalf("应拒绝跨账号引用, got %v", err)
				}
				if err.Error() != wantMismatchText {
					t.Fatalf("文案不对: %q", err.Error())
				}
			})
		}
	}
}

func TestSeedanceVerifiedAssetAllowedWhenSameAccount(t *testing.T) {
	cases := map[string]struct{ gen, asset *models.ModelConfig }{
		"两者都走全局":          {seedanceModel("", ""), assetModel("", "")},
		"合规模型不存在时按全局":     {seedanceModel("", ""), nil},
		"两者同一非全局上游":       {seedanceModel("https://u1.example", "sk-u1"), assetModel("https://u1.example/v1/", "sk-u1")},
		"生成模型独立上游与全局指纹相同": {seedanceModel("https://api.example.com/v1/", "sk-global"), assetModel("", "")},
		"合规模型独立上游与全局指纹相同": {seedanceModel("", ""), assetModel("https://api.example.com", "sk-global")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := seedanceSvc(c.gen, c.asset).applyRealPersonRefs(1, c.gen, map[string]any{"images": []any{"media://library/1/a.png"}})
			if err != nil {
				t.Fatal(err)
			}
			if imgs := got["images"].([]any); imgs[0] != "asset://A1" {
				t.Fatalf("应替换成 asset://, got %v", imgs)
			}
		})
	}
}

// 上游不同但没有引用已校验素材：照常放行，原样透传。
func TestSeedanceOwnUpstreamWithoutVerifiedAssetPasses(t *testing.T) {
	m := seedanceModel("https://other.example", "sk-other")
	got, err := seedanceSvc(m, assetModel("", "")).applyRealPersonRefs(1, m, map[string]any{"images": []any{"media://library/1/b.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if imgs := got["images"].([]any); imgs[0] != "media://library/1/b.png" {
		t.Fatalf("未校验的图应原样透传, got %v", imgs)
	}
}
