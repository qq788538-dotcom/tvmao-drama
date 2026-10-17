// apps/api/internal/agent/skill_categories_repo_test.go
package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func resetCategories(t *testing.T, repo SkillCategoriesRepo) []SkillCategory {
	t.Helper()
	rows, err := repo.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return rows
}

func TestSkillCategoriesRepo_CRUDAndOrder(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSkillCategoriesRepo(conn)
	skills := NewSkillsRepo(conn)
	resetSkillsTables(t, conn)

	base := len(resetCategories(t, repo))

	idA, err := repo.Create("测试分类A", "cyan")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	idB, err := repo.Create("测试分类B", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM skill_categories WHERE id IN (?,?)`, idA, idB)
	})

	rows := resetCategories(t, repo)
	if len(rows) != base+2 {
		t.Fatalf("expected %d categories, got %d", base+2, len(rows))
	}
	// 新建的排在最后（MAX+1），B 在 A 后。
	if rows[len(rows)-1].ID != idB || rows[len(rows)-2].ID != idA {
		t.Fatalf("new categories should append at tail: %+v", rows)
	}
	if rows[len(rows)-1].Color != "blue" {
		t.Fatalf("empty color should default to blue, got %q", rows[len(rows)-1].Color)
	}

	// 整表重排：把 B 提到 A 前。
	ids := make([]int64, 0, len(rows))
	for _, c := range rows[:len(rows)-2] {
		ids = append(ids, c.ID)
	}
	ids = append(ids, idB, idA)
	if err := repo.SetOrder(ids); err != nil {
		t.Fatalf("set order: %v", err)
	}
	rows = resetCategories(t, repo)
	if rows[len(rows)-2].ID != idB || rows[len(rows)-1].ID != idA {
		t.Fatalf("order not applied: %+v", rows)
	}

	// 改名级联：skills.category 必须跟着改，否则整批孤儿。
	uid := makeTestUser(t, conn)
	_ = uid
	sk := Skill{ID: "cat_test_skill", Name: "x", Category: "测试分类A", SkillType: "force_tool", IsOfficial: true}
	if err := skills.Create(sk); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if err := repo.Update(idA, "测试分类A改", "red"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := skills.Get("cat_test_skill")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if got.Category != "测试分类A改" {
		t.Fatalf("rename must cascade to skills.category, got %q", got.Category)
	}

	// 被使用中的分类拒绝删除。
	if err := repo.Delete(idA); !errors.Is(err, ErrCategoryInUse) {
		t.Fatalf("expected ErrCategoryInUse, got %v", err)
	}
	// 释放后可删。
	if err := skills.Delete("cat_test_skill"); err != nil {
		t.Fatalf("delete skill: %v", err)
	}
	if err := repo.Delete(idA); err != nil {
		t.Fatalf("delete category: %v", err)
	}
}

func TestSkillsRepo_MultiCategoryRoundTripAndFilter(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSkillsRepo(conn)
	resetSkillsTables(t, conn)

	sk := Skill{ID: "multi_cat_skill", Name: "多分类", SkillType: "force_tool",
		Categories: []string{"视频", "社交媒体"}, IsOfficial: true, IsPublished: true}
	if err := repo.Create(sk); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.Get("multi_cat_skill")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// 主分类 = 数组首元素；数组完整回读。
	if got.Category != "视频" || len(got.Categories) != 2 || got.Categories[1] != "社交媒体" {
		t.Fatalf("roundtrip mismatch: category=%q categories=%v", got.Category, got.Categories)
	}
	// 非主分类也要能按分类过滤命中（JSON_CONTAINS 路径）。
	rows, err := repo.ListPublicByCategory("社交媒体")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var hit bool
	for _, r := range rows {
		if r.ID == "multi_cat_skill" {
			hit = true
		}
	}
	if !hit {
		t.Fatal("skill must be discoverable under its secondary category")
	}
}

// 改名要级联 skills.categories 数组里的同名元素（在 Go 里改）。
func TestSkillCategoriesRenameCascadesArray(t *testing.T) {
	conn := openTestDB(t)
	cats := NewSkillCategoriesRepo(conn)
	skills := NewSkillsRepo(conn)
	id, err := cats.Create("剧本策划", "orange")
	if err != nil {
		t.Fatal(err)
	}
	if err := skills.Create(Skill{ID: "s1", Name: "n", Categories: []string{"官方精选", "剧本策划"}, SkillType: "force_tool",
		IsOfficial: true, IsPublished: true, WorkflowSteps: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	if err := cats.Update(id, "剧本", "orange"); err != nil {
		t.Fatal(err)
	}
	got, _ := skills.Get("s1")
	if !reflect.DeepEqual(got.Categories, []string{"官方精选", "剧本"}) || got.Category != "官方精选" {
		t.Fatalf("级联结果不对: category=%q categories=%v", got.Category, got.Categories)
	}
	if err := cats.Delete(id); !errors.Is(err, ErrCategoryInUse) {
		t.Fatalf("仍被引用的分类删除应失败，得到 %v", err)
	}
}
