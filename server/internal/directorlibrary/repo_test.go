package directorlibrary

import (
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func TestCategoryCRUD(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	id, err := repo.CreateCategory(Category{Name: "家具", SortOrder: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.GetCategory(id)
	if err != nil || c.Name != "家具" || !c.Enabled || c.ModelCount != 0 || c.CreatedAt.IsZero() {
		t.Fatalf("get = %+v %v", c, err)
	}
	if err := repo.UpdateCategory(Category{ID: id, Name: "道具", SortOrder: 1, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	c, _ = repo.GetCategory(id)
	if c.Name != "道具" || c.Enabled || c.SortOrder != 1 {
		t.Fatalf("update = %+v", c)
	}
	if _, err := repo.GetCategory(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestModelThumbNullRoundTrip(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	cat, _ := repo.CreateCategory(Category{Name: "c", Enabled: true})
	id, err := repo.CreateModel(Model{CategoryID: cat, Name: "m", FileName: "m.glb", ObjectKey: "lib/m.glb", SizeBytes: 1, MimeType: "model/gltf-binary", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := conn.QueryRow(`SELECT thumb_object_key IS NULL FROM director_library_models WHERE id = ?`, id).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("空缩略图应落 NULL: %v %v", isNull, err)
	}
	m, _ := repo.GetModel(id)
	if m.ThumbObjectKey != "" || !m.Enabled {
		t.Fatalf("got %+v", m)
	}
	m.ThumbObjectKey = "lib/m.png"
	if err := repo.UpdateModel(conn, *m); err != nil {
		t.Fatal(err)
	}
	m, _ = repo.GetModel(id)
	if m.ThumbObjectKey != "lib/m.png" {
		t.Fatalf("got %+v", m)
	}
}

func TestListEnabledJoinedKeepsEmptyAndFiltersDisabled(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	a, _ := repo.CreateCategory(Category{Name: "A", SortOrder: 1, Enabled: true})
	b, _ := repo.CreateCategory(Category{Name: "B", SortOrder: 2, Enabled: true})
	c, _ := repo.CreateCategory(Category{Name: "C", SortOrder: 0, Enabled: false})
	mk := func(cat int64, name string, sort int, enabled bool) {
		if _, err := repo.CreateModel(Model{CategoryID: cat, Name: name, FileName: name + ".glb", ObjectKey: "lib/" + name, SizeBytes: 1, MimeType: "x", SortOrder: sort, Enabled: enabled}); err != nil {
			t.Fatal(err)
		}
	}
	mk(a, "A1", 2, true)
	mk(a, "A2", 1, true)
	mk(a, "A3", 0, false)
	mk(c, "C1", 0, true)
	out, err := repo.ListEnabledJoined()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].ID != a || out[1].ID != b {
		t.Fatalf("分类 = %+v", out)
	}
	if len(out[0].Models) != 2 || out[0].Models[0].Name != "A2" || out[0].Models[1].Name != "A1" {
		t.Fatalf("A 的模型 = %+v", out[0].Models)
	}
	if out[1].Models == nil || len(out[1].Models) != 0 {
		t.Fatalf("空分类应保留且 models 为空数组: %+v", out[1].Models)
	}
}

func TestDeleteNonEmptyCategoryBlocked(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	cat, _ := repo.CreateCategory(Category{Name: "c", Enabled: true})
	if _, err := repo.CreateModel(Model{CategoryID: cat, Name: "m", FileName: "m.glb", ObjectKey: "lib/m", SizeBytes: 1, MimeType: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteCategory(cat); err == nil {
		t.Fatal("外键 RESTRICT 应拒绝删除非空分类")
	}
	var notEmpty *ErrCategoryNotEmpty
	if err := (&Service{Repo: repo}).DeleteCategory(cat); !errors.As(err, &notEmpty) || notEmpty.Models != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestCountProjectsReferencingAllUsers(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@local')`); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(conn)
	for i, p := range []struct {
		user    int64
		payload string
		deleted bool
	}{
		{1, `"media://lib/a_b.glb"`, false},
		{2, `"media://lib/a_b.glb"`, false},
		{2, `"media://lib/a_b.glb"`, true},
		{1, `"media://lib/aXb.glb"`, false},
		{1, `"media://LIB/A_B.GLB"`, false},
		{1, `"media://lib/a_b.glb2"`, false},
	} {
		res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (?, 'p')`, p.user)
		if err != nil {
			t.Fatal(i, err)
		}
		id, _ := res.LastInsertId()
		if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, id, `{"x":`+p.payload+`}`); err != nil {
			t.Fatal(i, err)
		}
		if p.deleted {
			conn.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
		}
	}
	n, err := repo.CountProjectsReferencingAllUsers("lib/a_b.glb")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
