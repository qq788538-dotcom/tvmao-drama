package http

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/aicharacters"
)

type fakeAICharactersRepo struct {
	lastParams aicharacters.ListParams
	items      []aicharacters.Character
}

func (f *fakeAICharactersRepo) List(p aicharacters.ListParams) ([]aicharacters.Character, error) {
	f.lastParams = p
	end := p.Offset + p.Limit
	if end > len(f.items) {
		end = len(f.items)
	}
	if p.Offset >= len(f.items) {
		return nil, nil
	}
	return f.items[p.Offset:end], nil
}

func (f *fakeAICharactersRepo) PublicAssetIDs([]string) (map[string]string, error) {
	return nil, nil
}

func (f *fakeAICharactersRepo) Get(assetID string) (*aicharacters.Character, error) {
	for i := range f.items {
		if f.items[i].AssetID == assetID {
			c := f.items[i]
			return &c, nil
		}
	}
	return nil, aicharacters.ErrNotFound
}

func manyCharacters(n int) []aicharacters.Character {
	out := make([]aicharacters.Character, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, aicharacters.Character{
			AssetID:  fmt.Sprintf("asset-%04d", i),
			Age:      20 + i%50,
			Gender:   "女",
			AgeGroup: "青年",
			ImageURL: fmt.Sprintf("media://builtin/characters/asset-%04d.jpg", i),
		})
	}
	return out
}

func TestAICharacterListPagination(t *testing.T) {
	repo := &fakeAICharactersRepo{items: manyCharacters(60)}
	h := NewAICharacterHandler(repo, aiCharFakeSigner{})

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/canvas/ai-characters", nil))

	var body struct {
		Items   []aiCharacterDTO `json:"items"`
		HasMore bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 50 || !body.HasMore {
		t.Fatalf("60 条应回 50 条 + hasMore，实际 %d 条 hasMore=%v", len(body.Items), body.HasMore)
	}
	// 出图必须是签名 URL——canonical 的 media:// 直接回给前端是渲染不出来的。
	if !strings.HasPrefix(body.Items[0].ImageURL, "https://") {
		t.Fatalf("imageUrl 应为签名 URL，实际 %q", body.Items[0].ImageURL)
	}

	rec = httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/canvas/ai-characters?offset=50", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 10 || body.HasMore {
		t.Fatalf("尾页应回 10 条且 hasMore=false，实际 %d 条 hasMore=%v", len(body.Items), body.HasMore)
	}
}

func TestAICharacterListFilters(t *testing.T) {
	repo := &fakeAICharactersRepo{}
	h := NewAICharacterHandler(repo, aiCharFakeSigner{})

	h.List(httptest.NewRecorder(), httptest.NewRequest(
		"GET", "/canvas/ai-characters?search=%E6%BC%94%E5%91%98&gender=%E5%A5%B3&ageGroup=%E9%9D%92%E5%B9%B4", nil))
	p := repo.lastParams
	if p.Search != "演员" || p.Gender != "女" || p.AgeGroup != "青年" {
		t.Fatalf("过滤参数没透传: %+v", p)
	}

	// 非法过滤值当不过滤——枚举将来扩充时老后端不至于 400。
	h.List(httptest.NewRecorder(), httptest.NewRequest(
		"GET", "/canvas/ai-characters?gender=bogus&ageGroup=bogus&offset=-3", nil))
	p = repo.lastParams
	if p.Gender != "" || p.AgeGroup != "" || p.Offset != 0 {
		t.Fatalf("非法值应被忽略: %+v", p)
	}
}

// aiCharFakeSigner 复刻 MediaSigner 对 media:// 的签名语义。
type aiCharFakeSigner struct{}

func (aiCharFakeSigner) Sign(s string) string {
	return "https://signed/" + strings.TrimPrefix(s, "media://")
}

func (aiCharFakeSigner) SignThumb(s string, w int) string {
	return fmt.Sprintf("https://signed/%s?w=%d", strings.TrimPrefix(s, "media://"), w)
}

// 形象图内置后恒为已配置：前端不再出现「图片未配置」提示。
func TestAICharacterListReportsImageConfigured(t *testing.T) {
	repo := &fakeAICharactersRepo{items: manyCharacters(1)}
	h := NewAICharacterHandler(repo, aiCharFakeSigner{})
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/canvas/ai-characters", nil))
	var body struct {
		ImageConfigured bool `json:"imageConfigured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.ImageConfigured {
		t.Fatal("imageConfigured 应恒为 true")
	}
}
