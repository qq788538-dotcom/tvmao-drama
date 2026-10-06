// apps/api/internal/http/skills_handlers_test.go
package http

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/agent"
)

// stubSkillsRepo 是 agent.SkillsRepo 的内存测试替身。HTTP handler 只需要
// Get/Create/Delete/List/ListForUser，所以其余方法返回最简占位值。
type stubSkillsRepo struct {
	items map[string]agent.Skill
}

func newStubSkillsRepo() *stubSkillsRepo {
	return &stubSkillsRepo{items: map[string]agent.Skill{}}
}

func (s *stubSkillsRepo) Get(id string) (*agent.Skill, error) {
	v, ok := s.items[id]
	if !ok {
		return nil, agent.ErrSkillNotFound
	}
	return &v, nil
}

func (s *stubSkillsRepo) Create(sk agent.Skill) error {
	if _, exists := s.items[sk.ID]; exists {
		return errExists
	}
	s.items[sk.ID] = sk
	return nil
}

func (s *stubSkillsRepo) Update(sk agent.Skill) error {
	if _, ok := s.items[sk.ID]; !ok {
		return agent.ErrSkillNotFound
	}
	s.items[sk.ID] = sk
	return nil
}

func (s *stubSkillsRepo) Delete(id string) error {
	delete(s.items, id)
	return nil
}

func (s *stubSkillsRepo) List() ([]agent.Skill, error) {
	out := make([]agent.Skill, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func (s *stubSkillsRepo) ListPublicByCategory(category string) ([]agent.Skill, error) {
	out := make([]agent.Skill, 0)
	for _, v := range s.items {
		if !v.IsOfficial || !v.IsPublished {
			continue
		}
		if category != "" && v.Category != category {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *stubSkillsRepo) ListForUser(userID int64) ([]agent.Skill, error) {
	out := make([]agent.Skill, 0)
	for _, v := range s.items {
		if v.IsOfficial && v.IsPublished {
			out = append(out, v)
			continue
		}
		if v.CreatorID.Valid && v.CreatorID.Int64 == userID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *stubSkillsRepo) ListFeatured() ([]agent.Skill, error) {
	out := make([]agent.Skill, 0)
	for _, v := range s.items {
		if v.IsOfficial && v.IsPublished && v.IsFeatured {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *stubSkillsRepo) IncrementUseCount(id string) error {
	v, ok := s.items[id]
	if !ok {
		return agent.ErrSkillNotFound
	}
	v.UseCount++
	s.items[id] = v
	return nil
}

type stringError string

func (e stringError) Error() string { return string(e) }

const errExists = stringError("skill already exists")

func TestSkillsList_FiltersByCategoryAndQuery(t *testing.T) {
	repo := newStubSkillsRepo()
	// 预置两个公开官方 + 一个私有（其他用户）+ 一个私有（调用方）。
	repo.items["video_a"] = agent.Skill{
		ID: "video_a", Name: "Seedance", Description: "video tool",
		Category: "video", IsOfficial: true, IsPublished: true,
	}
	repo.items["image_a"] = agent.Skill{
		ID: "image_a", Name: "PixelBrush", Description: "image tool",
		Category: "image", IsOfficial: true, IsPublished: true,
	}
	repo.items["mine"] = agent.Skill{
		ID: "mine", Name: "MyCustom", Description: "personal",
		Category: "video", IsOfficial: false, IsPublished: false,
		CreatorID: sql.NullInt64{Int64: 1, Valid: true},
	}
	repo.items["other_priv"] = agent.Skill{
		ID: "other_priv", Name: "OtherUser", Description: "stranger",
		Category: "video", IsOfficial: false, IsPublished: false,
		CreatorID: sql.NullInt64{Int64: 99, Valid: true},
	}

	h := NewSkillsHandler(repo, nil, fakeIconSigner{})
	r := chi.NewRouter()
	r.Get("/skills", h.List)

	// category=video → 公开的 video + 自己私有的 video
	req := httptest.NewRequest(http.MethodGet, "/skills?category=video", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []agent.Skill `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, s := range resp.Data {
		gotIDs[s.ID] = true
	}
	if !gotIDs["video_a"] || !gotIDs["mine"] {
		t.Fatalf("want video_a + mine, got %v", gotIDs)
	}
	if gotIDs["image_a"] {
		t.Fatalf("image_a should be filtered out by category=video, got %v", gotIDs)
	}
	if gotIDs["other_priv"] {
		t.Fatalf("other user's private skill should not leak, got %v", gotIDs)
	}

	// q=pixelbrush（对 name+description 做大小写不敏感的子串匹配）
	req2 := httptest.NewRequest(http.MethodGet, "/skills?q=pixelbrush", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list q: %d", rec2.Code)
	}
	var resp2 struct {
		Data []agent.Skill `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode q: %v", err)
	}
	if len(resp2.Data) != 1 || resp2.Data[0].ID != "image_a" {
		t.Fatalf("want only image_a from q=pixelbrush, got %+v", resp2.Data)
	}
}

func TestSkillsList_SignsMediaIconURL(t *testing.T) {
	repo := newStubSkillsRepo()
	repo.items["a"] = agent.Skill{ID: "a", Name: "A", IsOfficial: true, IsPublished: true, IconURL: "media://icons/a.png"}
	repo.items["b"] = agent.Skill{ID: "b", Name: "B", IsOfficial: true, IsPublished: true, IconURL: "https://cdn.example/b.svg"}
	h := NewSkillsHandler(repo, nil, fakeIconSigner{})
	req := httptest.NewRequest(http.MethodGet, "/skills", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	var resp struct {
		Data []agent.Skill `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]string{}
	for _, s := range resp.Data {
		got[s.ID] = s.IconURL
	}
	if got["a"] != "https://signed.example/icons/a.png" || got["b"] != "https://cdn.example/b.svg" {
		t.Fatalf("icon_url 签名结果不对: %v", got)
	}
}

// 首推端点是公开的：只出已发布+官方+首推的 skill，图标走缩略签名，提示词剥掉。
func TestSkillsFeatured_FiltersThumbsAndStripsPrompt(t *testing.T) {
	repo := newStubSkillsRepo()
	repo.items["a"] = agent.Skill{ID: "a", Name: "A", IsOfficial: true, IsPublished: true, IsFeatured: true,
		IconURL: "media://icons/a.png", SystemPromptAddon: "secret", SortWeight: 1}
	repo.items["b"] = agent.Skill{ID: "b", Name: "B", IsOfficial: true, IsPublished: true, IsFeatured: false}
	repo.items["c"] = agent.Skill{ID: "c", Name: "C", IsOfficial: true, IsPublished: false, IsFeatured: true}
	h := NewSkillsHandler(repo, nil, fakeIconSigner{})
	rec := httptest.NewRecorder()
	h.Featured(rec, httptest.NewRequest(http.MethodGet, "/skills/featured", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("featured: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []agent.Skill `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "a" {
		t.Fatalf("只应出 a，得到 %+v", resp.Data)
	}
	if resp.Data[0].IconURL != "https://thumb.example/icons/a.png?w=64" {
		t.Fatalf("icon 应为缩略签名 URL，得到 %q", resp.Data[0].IconURL)
	}
	if resp.Data[0].SystemPromptAddon != "" {
		t.Fatalf("公开端点不得下发 system_prompt_addon")
	}
}

// fakeIconSigner 模拟 MediaSigner 的两种出口：原图签名与缩略签名；外链原样。
type fakeIconSigner struct{}

func (fakeIconSigner) Sign(s string) string {
	if strings.HasPrefix(s, "media://") {
		return "https://signed.example/" + strings.TrimPrefix(s, "media://")
	}
	return s
}

func (fakeIconSigner) SignThumb(s string, w int) string {
	if strings.HasPrefix(s, "media://") {
		return "https://thumb.example/" + strings.TrimPrefix(s, "media://") + "?w=" + strconv.Itoa(w)
	}
	return s
}

// 本地用户看得到全部已发布的官方 Skill，且提示词照常下发（桌面端没有匿名访客）。
func TestSkillsList_LocalUserKeepsPrompt(t *testing.T) {
	repo := newStubSkillsRepo()
	repo.items["a"] = agent.Skill{ID: "a", Name: "A", IsOfficial: true, IsPublished: true, SystemPromptAddon: "p"}
	h := NewSkillsHandler(repo, nil, fakeIconSigner{})
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/skills", nil))
	if !strings.Contains(rec.Body.String(), `"system_prompt_addon":"p"`) {
		t.Fatalf("本地用户应拿到提示词: %s", rec.Body.String())
	}
}
