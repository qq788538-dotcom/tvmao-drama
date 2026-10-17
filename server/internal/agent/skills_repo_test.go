// apps/api/internal/agent/skills_repo_test.go
package agent

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// makeTestUser 插入一行 users（带唯一 email），返回新 id。
func makeTestUser(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (email, password_hash, role) VALUES (?, '', 'user')`,
		"skilltest+"+uuid.NewString()+"@x.com")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// resetSkillsTables 清空 skills，让测试插入有一个干净的起点。
func resetSkillsTables(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM skills`); err != nil {
		t.Fatalf("clear skills: %v", err)
	}
}

func TestSkillsRepoCreateGetRoundTrip(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	resetSkillsTables(t, conn)

	repo := NewSkillsRepo(conn)
	in := Skill{
		ID:                "seedance_2_0_video_" + uuid.NewString()[:8],
		Name:              "Seedance 2.0 视频",
		Description:       "Generate Seedance 2.0 short video.",
		Category:          "video",
		IconURL:           "/icons/seedance.png",
		SkillType:         "force_tool",
		SystemPromptAddon: "Use Seedance 2.0 for any video request.",
		ForceTools:        []string{"generate_video_seedance", "another_tool"},
		ParamDefaults: map[string]any{
			"resolution": "1080p",
			"duration":   float64(10),
			"audio":      true,
		},
		WorkflowSteps: json.RawMessage(`[]`),
		IsOfficial:    true,
		IsPublished:   true,
		SortWeight:    100,
	}
	if err := repo.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(in.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != in.Name || got.Category != in.Category || got.SkillType != in.SkillType {
		t.Fatalf("basic fields mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.ForceTools, in.ForceTools) {
		t.Fatalf("ForceTools mismatch: got %v want %v", got.ForceTools, in.ForceTools)
	}
	if !reflect.DeepEqual(got.ParamDefaults, in.ParamDefaults) {
		t.Fatalf("ParamDefaults mismatch: got %#v want %#v", got.ParamDefaults, in.ParamDefaults)
	}
	if !got.IsOfficial || !got.IsPublished {
		t.Fatalf("flag roundtrip failed: %+v", got)
	}

	// 对不存在的 id 调 Get 应该返回 ErrSkillNotFound。
	if _, err := repo.Get("nope-" + uuid.NewString()); err != ErrSkillNotFound {
		t.Fatalf("expected ErrSkillNotFound, got %v", err)
	}
}

func TestSkillsRepoListPublicByCategory(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	resetSkillsTables(t, conn)

	repo := NewSkillsRepo(conn)
	mk := func(id, category string, published bool) Skill {
		return Skill{
			ID:            id,
			Name:          id,
			Category:      category,
			SkillType:     "force_tool",
			IsOfficial:    true,
			IsPublished:   published,
			ForceTools:    []string{},
			ParamDefaults: map[string]any{},
			WorkflowSteps: json.RawMessage(`[]`),
		}
	}
	suffix := uuid.NewString()[:8]
	mustCreate := func(s Skill) {
		if err := repo.Create(s); err != nil {
			t.Fatalf("Create %s: %v", s.ID, err)
		}
	}
	mustCreate(mk("vid_pub_"+suffix, "video", true))
	mustCreate(mk("vid_draft_"+suffix, "video", false))
	mustCreate(mk("img_pub_"+suffix, "image", true))

	listed, err := repo.ListPublicByCategory("video")
	if err != nil {
		t.Fatalf("ListPublicByCategory: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "vid_pub_"+suffix {
		ids := make([]string, 0, len(listed))
		for _, s := range listed {
			ids = append(ids, s.ID)
		}
		t.Fatalf("want only vid_pub, got %v", ids)
	}

	// 空 category 返回所有已发布的官方 skill（video + image 都算）。
	all, err := repo.ListPublicByCategory("")
	if err != nil {
		t.Fatalf("ListPublicByCategory empty: %v", err)
	}
	if len(all) != 2 {
		ids := make([]string, 0, len(all))
		for _, s := range all {
			ids = append(ids, s.ID)
		}
		t.Fatalf("want 2 published, got %d: %v", len(all), ids)
	}
}

func TestSkillsRepoListForUser(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	resetSkillsTables(t, conn)

	repo := NewSkillsRepo(conn)
	owner := makeTestUser(t, conn)
	stranger := makeTestUser(t, conn)
	suffix := uuid.NewString()[:8]

	publicSkill := Skill{
		ID:            "public_" + suffix,
		Name:          "Public",
		Category:      "video",
		SkillType:     "force_tool",
		IsOfficial:    true,
		IsPublished:   true,
		ForceTools:    []string{},
		ParamDefaults: map[string]any{},
		WorkflowSteps: json.RawMessage(`[]`),
	}
	if err := repo.Create(publicSkill); err != nil {
		t.Fatalf("Create public: %v", err)
	}

	ownerSkill := Skill{
		ID:            "private_" + suffix,
		Name:          "Private",
		Category:      "video",
		SkillType:     "force_tool",
		IsOfficial:    false,
		IsPublished:   false,
		CreatorID:     sql.NullInt64{Int64: owner, Valid: true},
		ForceTools:    []string{},
		ParamDefaults: map[string]any{},
		WorkflowSteps: json.RawMessage(`[]`),
	}
	if err := repo.Create(ownerSkill); err != nil {
		t.Fatalf("Create private: %v", err)
	}

	strangerSkill := Skill{
		ID:            "stranger_" + suffix,
		Name:          "Stranger",
		Category:      "video",
		SkillType:     "force_tool",
		IsOfficial:    false,
		IsPublished:   false,
		CreatorID:     sql.NullInt64{Int64: stranger, Valid: true},
		ForceTools:    []string{},
		ParamDefaults: map[string]any{},
		WorkflowSteps: json.RawMessage(`[]`),
	}
	if err := repo.Create(strangerSkill); err != nil {
		t.Fatalf("Create stranger: %v", err)
	}

	listed, err := repo.ListForUser(owner)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	have := make(map[string]bool, len(listed))
	for _, s := range listed {
		have[s.ID] = true
	}
	if !have[publicSkill.ID] {
		t.Fatalf("want public skill in result, got %v", have)
	}
	if !have[ownerSkill.ID] {
		t.Fatalf("want owner's private skill in result, got %v", have)
	}
	if have[strangerSkill.ID] {
		t.Fatalf("stranger's private skill should not be visible to owner, got %v", have)
	}
}

func TestSkillsRepoIncrementUseCount(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	resetSkillsTables(t, conn)

	repo := NewSkillsRepo(conn)
	s := Skill{
		ID:            "count_" + uuid.NewString()[:8],
		Name:          "Count Me",
		Category:      "video",
		SkillType:     "force_tool",
		IsOfficial:    true,
		IsPublished:   true,
		ForceTools:    []string{},
		ParamDefaults: map[string]any{},
		WorkflowSteps: json.RawMessage(`[]`),
	}
	if err := repo.Create(s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := repo.IncrementUseCount(s.ID); err != nil {
			t.Fatalf("IncrementUseCount #%d: %v", i, err)
		}
	}

	got, err := repo.Get(s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UseCount != 3 {
		t.Fatalf("want use_count=3, got %d", got.UseCount)
	}
}

// 多分类过滤：categories 数组命中也算（json_each）。
func TestListPublicByCategoryMatchesArray(t *testing.T) {
	conn := openTestDB(t)
	resetSkillsTables(t, conn)
	repo := NewSkillsRepo(conn)
	_ = repo.Create(Skill{ID: "a", Name: "a", Categories: []string{"分镜策划", "官方精选"}, SkillType: "force_tool",
		IsOfficial: true, IsPublished: true, WorkflowSteps: json.RawMessage(`[]`)})
	_ = repo.Create(Skill{ID: "b", Name: "b", Categories: []string{"剧本策划"}, SkillType: "force_tool",
		IsOfficial: true, IsPublished: true, WorkflowSteps: json.RawMessage(`[]`)})
	got, err := repo.ListPublicByCategory("官方精选")
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %v err %v", got, err)
	}
}
