package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// I-2（短期方案）：合规素材注册在全局上游账号下。目标模型走独立上游（指纹与全局不同）且请求
// 引用了已校验的 asset:// 素材时，提交前直接报错，不调上游。

func seedanceSvc(m *models.ModelConfig) *Service {
	s := planService(m, okSettings(), "sk-global")
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

func TestSeedanceVerifiedAssetRejectedOnOwnUpstream(t *testing.T) {
	m := seedanceModel("https://other.example", "sk-other")
	for name, ref := range map[string]string{
		"命中已校验素材":           "media://library/1/a.png",
		"快照里已是 asset:// 引用": "asset://A1",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := seedanceSvc(m).prepareExecution(context.Background(), 1, m.ModelID, map[string]any{"images": []any{ref}}, "", "", nil)
			if !errors.Is(err, ErrVerifiedAssetUpstreamMismatch) {
				t.Fatalf("应拒绝跨账号引用, got %v", err)
			}
			want := "已校验素材注册在全局上游账号下，当前模型使用独立上游，无法引用。请改用全局上游，或在该账号下重新校验素材。"
			if err.Error() != want {
				t.Fatalf("文案不对: %q", err.Error())
			}
		})
	}
}

func TestSeedanceVerifiedAssetAllowedWhenSameAccount(t *testing.T) {
	for name, m := range map[string]*models.ModelConfig{
		"走全局":         seedanceModel("", ""),
		"独立上游与全局指纹相同": seedanceModel("https://api.example.com/v1/", "sk-global"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := seedanceSvc(m).applyRealPersonRefs(1, m, map[string]any{"images": []any{"media://library/1/a.png"}})
			if err != nil {
				t.Fatal(err)
			}
			if imgs := got["images"].([]any); imgs[0] != "asset://A1" {
				t.Fatalf("应替换成 asset://, got %v", imgs)
			}
		})
	}
}

// 独立上游但没有引用已校验素材：照常放行，原样透传。
func TestSeedanceOwnUpstreamWithoutVerifiedAssetPasses(t *testing.T) {
	m := seedanceModel("https://other.example", "sk-other")
	got, err := seedanceSvc(m).applyRealPersonRefs(1, m, map[string]any{"images": []any{"media://library/1/b.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if imgs := got["images"].([]any); imgs[0] != "media://library/1/b.png" {
		t.Fatalf("未校验的图应原样透传, got %v", imgs)
	}
}
