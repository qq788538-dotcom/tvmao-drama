package assets

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func newRepo(t *testing.T) (*SQLiteRepository, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	return NewSQLiteRepository(conn), conn
}

func TestInsertAndFindByUserProjectHash(t *testing.T) {
	repo, _ := newRepo(t)
	id, err := repo.Insert(AssetRecord{UserID: 1, ProjectID: 5, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 3, MimeType: "image/png", ContentHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByUserProjectHash(1, 5, "h1")
	if err != nil || got.ID != id || got.ProjectID != 5 || got.URL != "media://u1/a.png" || got.CreatedAt.IsZero() {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByUserProjectHash(1, 6, "h1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨项目不去重，err=%v", err)
	}
	// projectID=0 匹配没挂项目的行
	if _, err := repo.Insert(AssetRecord{UserID: 1, ObjectKey: "u1/b.png", SourceKind: "upload",
		URL: "media://u1/b.png", SizeBytes: 1, MimeType: "image/png", ContentHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.FindByUserProjectHash(1, 0, "h2"); err != nil || got.ProjectID != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByUserProjectHash(1, 5, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("空 hash 必须视为未命中")
	}
}

func TestListAndDeleteForProject(t *testing.T) {
	repo, _ := newRepo(t)
	for i, k := range []string{"k1", "k2"} {
		if _, err := repo.Insert(AssetRecord{UserID: 1, ProjectID: 9, ObjectKey: k, SourceKind: "upload",
			URL: "media://" + k, SizeBytes: int64(10 * (i + 1)), MimeType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListForProject(9)
	if err != nil || len(list) != 2 || list[0].CreatedAt.IsZero() {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	freed, err := repo.DeleteForProject(9)
	if err != nil || freed != 30 {
		t.Fatalf("freed=%d err=%v", freed, err)
	}
	if list, _ := repo.ListForProject(9); len(list) != 0 {
		t.Fatal("没删干净")
	}
}

func TestListByObjectKeyAndClearLibraryRef(t *testing.T) {
	repo, conn := newRepo(t)
	repo.Insert(AssetRecord{UserID: 1, ProjectID: 2, ObjectKey: "shared", SourceKind: "upload", URL: "media://shared", SizeBytes: 5, MimeType: "image/png", LibraryItemID: 77})
	repo.Insert(AssetRecord{UserID: 1, ProjectID: 3, ObjectKey: "shared", SourceKind: "canvas_ref", URL: "media://shared", SizeBytes: 0, MimeType: "image/png"})
	rows, err := repo.ListByObjectKey("shared")
	if err != nil || len(rows) != 2 || rows[0].LibraryItemID != 77 || rows[1].ProjectID != 3 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := repo.ClearLibraryRef(77); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE library_item_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("library_item_id 未清空")
	}
}

// 生成产物按 (source_task_id, output_index) 槽位落库：重放复用已有行，不重复插入、不重复记账。
func TestInsertSlotTxIsIdempotentPerSlot(t *testing.T) {
	repo, conn := newRepo(t)
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rec := AssetRecord{UserID: 1, ProjectID: 4, SourceTaskID: 9, ObjectKey: "u1/gen_task-9-0", SourceKind: "generation",
		URL: "media://u1/gen_task-9-0", SizeBytes: 100, MimeType: "image/png"}
	id1, inserted, err := repo.InsertSlotTx(tx, rec, 0)
	if err != nil || !inserted || id1 == 0 {
		t.Fatalf("首次 id=%d inserted=%v err=%v", id1, inserted, err)
	}
	id2, inserted, err := repo.InsertSlotTx(tx, rec, 0)
	if err != nil || inserted || id2 != id1 {
		t.Fatalf("重放 id=%d inserted=%v err=%v，want 复用 %d", id2, inserted, err, id1)
	}
	id3, inserted, err := repo.InsertSlotTx(tx, rec, 1)
	if err != nil || !inserted || id3 == id1 {
		t.Fatalf("新槽位 id=%d inserted=%v err=%v", id3, inserted, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByTaskSlot(9, 0)
	if err != nil || got.ID != id1 || got.SourceTaskID != 9 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByTaskSlot(9, 5); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskAssetsListAndDelete(t *testing.T) {
	repo, _ := newRepo(t)
	repo.Insert(AssetRecord{UserID: 1, SourceTaskID: 3, ObjectKey: "t1", SourceKind: "generation", URL: "media://t1", SizeBytes: 4, MimeType: "image/png"})
	repo.Insert(AssetRecord{UserID: 1, SourceTaskID: 3, ObjectKey: "t2", SourceKind: "generation", URL: "media://t2", SizeBytes: 6, MimeType: "image/png"})
	if list, err := repo.ListForTask(3); err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if freed, err := repo.DeleteForTask(3); err != nil || freed != 10 {
		t.Fatalf("freed=%d err=%v", freed, err)
	}
}

func TestDurationMsByObjectKey(t *testing.T) {
	repo, _ := newRepo(t)
	if _, found, err := repo.GetDurationMsByObjectKey("none"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	repo.Insert(AssetRecord{UserID: 1, ObjectKey: "v", SourceKind: "upload", URL: "media://v", SizeBytes: 1, MimeType: "video/mp4"})
	if d, found, err := repo.GetDurationMsByObjectKey("v"); !found || d != 0 || err != nil {
		t.Fatalf("NULL 时长 d=%d found=%v err=%v", d, found, err)
	}
	if err := repo.SetDurationMsByObjectKey("v", 4200); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := repo.GetDurationMsByObjectKey("v"); d != 4200 {
		t.Fatalf("d=%d", d)
	}
}

func TestFindByIDAndListForUser(t *testing.T) {
	repo, _ := newRepo(t)
	id, _ := repo.Insert(AssetRecord{UserID: 1, ObjectKey: "a", SourceKind: "upload", URL: "media://a", SizeBytes: 1, MimeType: "image/png"})
	got, err := repo.FindByID(id)
	if err != nil || got == nil || got.ObjectKey != "a" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got, err := repo.FindByID(99999); got != nil || err != nil {
		t.Fatalf("不存在应返回 (nil,nil)，got=%+v err=%v", got, err)
	}
	if list, err := repo.ListForUser(1, 0); err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}

// 求和与删除必须原子：分两步时，两步之间插入的行会被删掉却不计入 freed，
// 配额扣减就少扣了。并发插入期间反复删除，freed 总和必须等于插入总量。
func TestDeleteForProjectFreedMatchesDeletedRows(t *testing.T) {
	repo, _ := newRepo(t)
	const total = 300
	done := make(chan error, 1)
	go func() {
		for i := 0; i < total; i++ {
			if _, err := repo.Insert(AssetRecord{UserID: 1, ProjectID: 9, ObjectKey: "k", SourceKind: "upload",
				URL: "media://k", SizeBytes: 1, MimeType: "image/png"}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var freed int64
	for running := true; running; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			running = false
		default:
		}
		n, err := repo.DeleteForProject(9)
		if err != nil {
			t.Fatal(err)
		}
		freed += n
	}
	if freed != total {
		t.Fatalf("freed=%d, want %d", freed, total)
	}
}
