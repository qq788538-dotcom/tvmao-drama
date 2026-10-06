package library

import (
	"errors"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestLibraryForeignKeysRestrictDeletes(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'p')`)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO library_folders (user_id, parent_id, name) VALUES (1, ?, 'c')`, parent)
	child, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, ?, 'k', 'media://k', 1, 'image/png', 'image')`, child); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM library_folders WHERE id = ?`, child); err == nil {
		t.Fatal("有素材的目录不应能直接删（ON DELETE RESTRICT）")
	}
	if _, err := conn.Exec(`DELETE FROM library_folders WHERE id = ?`, parent); err == nil {
		t.Fatal("有子目录的目录不应能直接删")
	}
	if _, err := conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, 99999, 'k', 'u', 1, 'image/png', 'image')`); err == nil {
		t.Fatal("指向不存在目录的素材应被外键挡下")
	}
}

func TestLibraryFoldersUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	res, _ := conn.Exec(`INSERT INTO library_folders (user_id, name, updated_at) VALUES (1, 'a', '2000-01-01 00:00:00')`)
	id, _ := res.LastInsertId()
	if _, err := conn.Exec(`UPDATE library_folders SET name = 'b' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := conn.QueryRow(`SELECT updated_at FROM library_folders WHERE id = ?`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Year() == 2000 {
		t.Fatal("UPDATE 后 updated_at 没有刷新")
	}
}

func TestFolderTreeCRUD(t *testing.T) {
	folders := NewSQLiteFoldersRepository(dbtest.Open(t))
	root, err := folders.Create(1, nil, "角色")
	if err != nil {
		t.Fatal(err)
	}
	child, err := folders.Create(1, &root, "主角")
	if err != nil {
		t.Fatal(err)
	}
	grand, _ := folders.Create(1, &child, "表情")

	list, err := folders.List(1)
	if err != nil || len(list) != 3 || list[0].ID != root || list[0].ParentID != nil {
		t.Fatalf("list=%+v err=%v（根目录 parent_id 为 NULL，应排最前）", list, err)
	}
	got, err := folders.Get(1, child)
	if err != nil || got.ParentID == nil || *got.ParentID != root || got.CreatedAt.IsZero() {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := folders.Get(2, child); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("别的用户读不到: %v", err)
	}
	if _, err := folders.Create(2, &grand, "x"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("不能挂到别人的目录下: %v", err)
	}
	// MySQL 改成同名时 RowsAffected=0 会误报 not found；SQLite 返回匹配行数，不再误报。
	if err := folders.Rename(1, child, "主角"); err != nil {
		t.Fatalf("改成同名不应报错: %v", err)
	}
	if err := folders.Rename(1, 99999, "x"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("err=%v", err)
	}
	if err := folders.Move(1, root, &grand); !errors.Is(err, ErrFolderCycle) {
		t.Fatalf("移到自己的后代下应报环: %v", err)
	}
	if err := folders.Move(1, grand, nil); err != nil {
		t.Fatalf("移回根目录: %v", err)
	}
	ids, err := folders.DescendantIDs(1, root)
	if err != nil || len(ids) != 2 || ids[0] != root || ids[1] != child {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if err := folders.DeleteIfEmpty(1, root); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("有子目录应报非空: %v", err)
	}
	if err := folders.DeleteIfEmpty(1, child); err != nil {
		t.Fatal(err)
	}
	if err := folders.DeleteIfEmpty(1, root); err != nil {
		t.Fatal(err)
	}
	if err := folders.DeleteIfEmpty(1, root); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestItemsCRUDAndFilters(t *testing.T) {
	conn := dbtest.Open(t)
	folders := NewSQLiteFoldersRepository(conn)
	items := NewSQLiteItemsRepository(conn)
	f1, _ := folders.Create(1, nil, "a")
	f2, _ := folders.Create(1, nil, "b")
	w := 640
	id1, err := items.Insert(Item{UserID: 1, FolderID: f1, ObjectKey: "library/1/x.png", URL: "media://library/1/x.png",
		SizeBytes: 10, MimeType: "image/png", Kind: "image", Width: &w, Source: "upload", Name: "Cat Photo"})
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := items.Insert(Item{UserID: 1, FolderID: f2, ObjectKey: "library/1/y.mp4", URL: "media://library/1/y.mp4",
		SizeBytes: 20, MimeType: "video/mp4", Kind: "video", Source: "upload"})

	it, err := items.Get(1, id1)
	if err != nil || it.Width == nil || *it.Width != 640 || it.Height != nil || it.Name != "Cat Photo" || it.CreatedAt.IsZero() {
		t.Fatalf("it=%+v err=%v", it, err)
	}
	if list, total, err := items.List(ItemFilter{UserID: 1}); err != nil || total != 2 || list[0].ID != id2 {
		t.Fatalf("list=%+v total=%d err=%v（按 id 倒序）", list, total, err)
	}
	if list, total, _ := items.List(ItemFilter{UserID: 1, FolderID: f1}); total != 1 || list[0].ID != id1 {
		t.Fatalf("按目录过滤 list=%+v total=%d", list, total)
	}
	if _, total, _ := items.List(ItemFilter{UserID: 1, Search: "cat"}); total != 1 {
		t.Fatalf("按名称搜索应大小写不敏感，total=%d", total)
	}

	conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@miaoju.desktop')`)
	foreign, _ := folders.Create(2, nil, "别人的")
	if err := items.Update(1, id1, nil, &foreign); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("不能移到别人的目录: %v", err)
	}
	name := "renamed"
	if err := items.Update(1, id1, &name, &f2); err != nil {
		t.Fatal(err)
	}
	if n, _ := items.CountForFolder(1, f2); n != 2 {
		t.Fatalf("count=%d", n)
	}
	if got, _ := items.ListByFolderIDs(1, []int64{f1, f2}); len(got) != 2 {
		t.Fatalf("ListByFolderIDs=%d", len(got))
	}
	if got, err := items.ListByFolderIDs(1, nil); got != nil || err != nil {
		t.Fatalf("空列表 got=%v err=%v", got, err)
	}
	if err := items.Delete(1, id2); err != nil {
		t.Fatal(err)
	}
	if err := items.Delete(1, id2); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("err=%v", err)
	}
	if f, _ := folders.Get(1, f2); f.ItemCount != 1 {
		t.Fatalf("itemCount=%d", f.ItemCount)
	}
}

func TestOrphanMarkRefreshesAndListReady(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	if err := orphans.Mark(conn, "k1", 1, 100); err != nil {
		t.Fatal(err)
	}
	conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00' WHERE object_key = 'k1'`)
	ready, err := orphans.ListReady(DefaultGracePeriod)
	if err != nil || len(ready) != 1 || ready[0].MarkedAt.Year() != 2000 || ready[0].SizeBytes != 100 {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	// 再次 Mark 刷新宽限计时：主键冲突走 ON CONFLICT DO UPDATE。
	if err := orphans.Mark(conn, "k1", 1, 100); err != nil {
		t.Fatal(err)
	}
	if ready, _ := orphans.ListReady(DefaultGracePeriod); len(ready) != 0 {
		t.Fatalf("重新标记后应重新计时，ready=%+v", ready)
	}
	if err := orphans.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects`).Scan(&n)
	if n != 0 {
		t.Fatal("没删掉")
	}
}

func TestTryReclaimOnlyMarksUnreferencedKeys(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	folders := NewSQLiteFoldersRepository(conn)
	items := NewSQLiteItemsRepository(conn)
	f, _ := folders.Create(1, nil, "f")
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'in-assets', 'media://in-assets', 1, 'image/png')`)
	items.Insert(Item{UserID: 1, FolderID: f, ObjectKey: "in-library", URL: "media://in-library", SizeBytes: 1, MimeType: "image/png", Kind: "image", Source: "upload"})

	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"in-assets", "in-library", "gone"} {
		if err := TryReclaim(ReclaimDeps{Querier: tx, Execer: tx, Orphans: orphans}, k, 1, 5); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ready, _ := orphans.ListReady(-time.Hour) // 截止时间在未来：刚标记的也算
	if len(ready) != 1 || ready[0].ObjectKey != "gone" {
		t.Fatalf("只应标记无引用的 key，got %+v", ready)
	}
	if ref, err := IsStillReferenced(conn, "in-library"); err != nil || !ref {
		t.Fatalf("ref=%v err=%v", ref, err)
	}
	if ref, _ := IsStillReferenced(conn, "gone"); ref {
		t.Fatal("gone 不应仍被引用")
	}
}
