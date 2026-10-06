package projects

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

const uid int64 = 1

func newRepo(t *testing.T) (*SQLiteRepository, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	return NewSQLiteRepository(conn), conn
}

func mustCreate(t *testing.T, repo *SQLiteRepository, name string) int64 {
	t.Helper()
	id, err := repo.Create(uid, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func snapshotRow(t *testing.T, conn *sql.DB, pid int64) (payload string, version, lastClient int64, typ string) {
	t.Helper()
	err := conn.QueryRow(`SELECT payload, version, last_client_version, typeof(payload) FROM workflow_snapshots WHERE project_id = ?`, pid).
		Scan(&payload, &version, &lastClient, &typ)
	if err != nil {
		t.Fatalf("读快照: %v", err)
	}
	return
}

func TestCreateGetUpdate(t *testing.T) {
	repo, _ := newRepo(t)
	id := mustCreate(t, repo, "first")
	p, err := repo.GetForUser(uid, id)
	if err != nil || p.Name != "first" || p.UserID != uid {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("时间列没有扫出来")
	}
	if err := repo.Update(uid, id, "renamed", "desc"); err != nil {
		t.Fatal(err)
	}
	p, _ = repo.GetForUser(uid, id)
	if p.Name != "renamed" || p.Description != "desc" {
		t.Fatalf("p=%+v", p)
	}
	if err := repo.Update(uid, 99999, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("更新不存在的项目 err=%v want ErrNotFound", err)
	}
}

func TestDeleteIsSoftAndHidesProject(t *testing.T) {
	repo, _ := newRepo(t)
	id := mustCreate(t, repo, "p")
	if err := repo.Delete(uid, id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetForUser(uid, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("软删后仍能读到: %v", err)
	}
	if err := repo.Delete(uid, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除 err=%v want ErrNotFound", err)
	}
	pending, err := repo.ListPendingDeletion(10)
	if err != nil || len(pending) != 1 || pending[0].ID != id || pending[0].UserID != uid {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}

func TestHardDeleteCascadesSnapshot(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	if _, err := repo.SaveAuthoritative(id, json.RawMessage(`{"nodes":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.HardDelete(id); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM workflow_snapshots WHERE project_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("硬删项目后快照仍在")
	}
}

func TestListForUserPaginatesAndCounts(t *testing.T) {
	repo, conn := newRepo(t)
	for i := 0; i < 5; i++ {
		id := mustCreate(t, repo, fmt.Sprintf("p%d", i))
		// updated_at 显式错开，避免同一秒内排序不确定
		conn.Exec(`UPDATE projects SET updated_at = ? WHERE id = ?`, fmt.Sprintf("2026-01-0%d 00:00:00", i+1), id)
	}
	page, total, err := repo.ListForUser(uid, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 2 || page[0].Name != "p3" || page[1].Name != "p2" {
		t.Fatalf("total=%d page=%v", total, page)
	}
	all, total, _ := repo.ListForUser(uid, 0, 0)
	if total != 5 || len(all) != 5 || all[0].Name != "p4" {
		t.Fatalf("全量 total=%d len=%d first=%v", total, len(all), all)
	}
}

func TestListForUserStorageBytesAndAutoCover(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	conn.Exec(`INSERT INTO assets (user_id, project_id, object_key, source_kind, url, size_bytes, mime_type) VALUES
		(1, ?, 'k1', 'upload', 'media://k1', 100, 'image/png'),
		(1, ?, 'k2', 'generation', 'media://gen-first', 200, 'image/png'),
		(1, ?, 'k3', 'generation', 'media://gen-second', 300, 'image/png'),
		(1, ?, 'k4', 'generation', 'media://video', 400, 'video/mp4')`, id, id, id, id)
	list, _, err := repo.ListForUser(uid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].StorageBytes != 1000 {
		t.Fatalf("storageBytes=%d want 1000", list[0].StorageBytes)
	}
	if list[0].CoverURL != "media://gen-first" {
		t.Fatalf("自动封面=%q want 最早一张生成图 media://gen-first", list[0].CoverURL)
	}
}

func TestListForUserCoverFallsBackWhenCleared(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	conn.Exec(`INSERT INTO assets (user_id, project_id, object_key, source_kind, url, size_bytes, mime_type)
		VALUES (1, ?, 'k', 'generation', 'media://auto', 1, 'image/png')`, id)
	if err := repo.SetCover(uid, id, "media://manual"); err != nil {
		t.Fatal(err)
	}
	list, _, _ := repo.ListForUser(uid, 0, 0)
	if list[0].CoverURL != "media://manual" {
		t.Fatalf("手动封面=%q", list[0].CoverURL)
	}
	// 清除封面写的是空串；读侧 NULLIF 必须把它当成未指定，回落到自动取图。
	if err := repo.SetCover(uid, id, ""); err != nil {
		t.Fatal(err)
	}
	list, _, _ = repo.ListForUser(uid, 0, 0)
	if list[0].CoverURL != "media://auto" {
		t.Fatalf("清除后封面=%q want media://auto", list[0].CoverURL)
	}
	if err := repo.SetCover(uid, 99999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestSaveAuthoritativeCreatesThenBumpsVersion(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	v, err := repo.SaveAuthoritative(id, json.RawMessage(`{"nodes":["a"]}`))
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v want 1", v, err)
	}
	v, err = repo.SaveAuthoritative(id, json.RawMessage(`{"nodes":["b"]}`))
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v want 2", v, err)
	}
	payload, version, lastClient, typ := snapshotRow(t, conn, id)
	if payload != `{"nodes":["b"]}` || version != 2 || lastClient != 0 || typ != "text" {
		t.Fatalf("payload=%s version=%d lastClient=%d typeof=%s", payload, version, lastClient, typ)
	}
}

func TestMutateClientSnapshotInsertsAndSetsLastClientVersion(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	v, err := repo.MutateClientSnapshot(id, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		if len(payload) != 0 {
			t.Fatalf("新项目回调收到 payload=%s，want nil", payload)
		}
		return json.RawMessage(`{"nodes":[1]}`), true, nil
	})
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	_, version, lastClient, typ := snapshotRow(t, conn, id)
	if version != 1 || lastClient != 1 || typ != "text" {
		t.Fatalf("version=%d lastClient=%d typeof=%s", version, lastClient, typ)
	}
}

func TestMutateClientSnapshotUpdatesAndSkipsUnchanged(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	repo.SaveAuthoritative(id, json.RawMessage(`{"n":0}`)) // version 1, lastClient 0
	v, err := repo.MutateClientSnapshot(id, func(json.RawMessage) (json.RawMessage, bool, error) {
		return json.RawMessage(`{"n":1}`), true, nil
	})
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v want 2", v, err)
	}
	_, version, lastClient, _ := snapshotRow(t, conn, id)
	if version != 2 || lastClient != 2 {
		t.Fatalf("version=%d lastClient=%d", version, lastClient)
	}
	v, err = repo.MutateClientSnapshot(id, func(p json.RawMessage) (json.RawMessage, bool, error) {
		return p, false, nil
	})
	if err != nil || v != 2 {
		t.Fatalf("无改动 v=%d err=%v want 2", v, err)
	}
}

func TestMutateSnapshotCreatesRowWhenMissing(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	v, err := repo.MutateSnapshot(id, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		return json.RawMessage(`{"nodes":["x"]}`), true, nil
	})
	if err != nil || v != 1 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	_, version, lastClient, _ := snapshotRow(t, conn, id)
	if version != 1 || lastClient != 0 {
		t.Fatalf("后端权威写建行：version=%d lastClient=%d want 1, 0", version, lastClient)
	}
}

func TestMutateSnapshotMissingRowNoChangeCreatesNothing(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	v, err := repo.MutateSnapshot(id, func(json.RawMessage) (json.RawMessage, bool, error) {
		return nil, false, nil
	})
	if err != nil || v != 0 {
		t.Fatalf("v=%d err=%v want 0", v, err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM workflow_snapshots WHERE project_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("无改动时不应建行")
	}
}

func TestMutateSnapshotExistingRowBumpsVersionOnly(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	repo.MutateClientSnapshot(id, func(json.RawMessage) (json.RawMessage, bool, error) {
		return json.RawMessage(`{"n":1}`), true, nil
	}) // version 1, lastClient 1
	v, err := repo.MutateSnapshot(id, func(json.RawMessage) (json.RawMessage, bool, error) {
		return json.RawMessage(`{"n":2}`), true, nil
	})
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	_, version, lastClient, _ := snapshotRow(t, conn, id)
	if version != 2 || lastClient != 1 {
		t.Fatalf("后端回写不推进客户端版本：version=%d lastClient=%d want 2, 1", version, lastClient)
	}
}

func TestMutateSnapshotPropagatesCallbackErrorAndRollsBack(t *testing.T) {
	repo, conn := newRepo(t)
	id := mustCreate(t, repo, "p")
	repo.SaveAuthoritative(id, json.RawMessage(`{"n":1}`))
	boom := errors.New("boom")
	if _, err := repo.MutateSnapshot(id, func(json.RawMessage) (json.RawMessage, bool, error) {
		return nil, false, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
	payload, version, _, _ := snapshotRow(t, conn, id)
	if payload != `{"n":1}` || version != 1 {
		t.Fatalf("回调出错后快照被改动：payload=%s version=%d", payload, version)
	}
}

// 后台回写（MutateSnapshot）与用户编辑（MutateClientSnapshot）并发写同一个快照：
// 不得报 SQLITE_BUSY，也不得丢更新。
func TestMutateClientSnapshotConcurrentWritersNoLostUpdate(t *testing.T) {
	repo, _ := newRepo(t)
	id := mustCreate(t, repo, "p")
	repo.SaveAuthoritative(id, json.RawMessage(`{"n":0}`))
	increment := func(payload json.RawMessage) (json.RawMessage, bool, error) {
		var doc struct{ N int }
		if err := json.Unmarshal(payload, &doc); err != nil {
			return nil, false, err
		}
		return json.RawMessage(fmt.Sprintf(`{"n":%d}`, doc.N+1)), true, nil
	}
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = repo.MutateClientSnapshot(id, increment)
			} else {
				_, err = repo.MutateSnapshot(id, increment)
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发写出错: %v", err)
		}
	}
	snap, err := repo.GetLatest(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.Payload) != fmt.Sprintf(`{"n":%d}`, workers) || snap.Version != workers+1 {
		t.Fatalf("payload=%s version=%d want n=%d version=%d", snap.Payload, snap.Version, workers, workers+1)
	}
}

func TestGetLatestMissingReturnsNil(t *testing.T) {
	repo, _ := newRepo(t)
	id := mustCreate(t, repo, "p")
	snap, err := repo.GetLatest(id)
	if err != nil || snap != nil {
		t.Fatalf("snap=%v err=%v want nil, nil", snap, err)
	}
	repo.SaveAuthoritative(id, json.RawMessage(`{}`))
	snap, err = repo.GetLatest(id)
	if err != nil || snap == nil || snap.SyncedAt.IsZero() {
		t.Fatalf("snap=%v err=%v", snap, err)
	}
}

func TestSnapshotProjectsContaining(t *testing.T) {
	repo, _ := newRepo(t)
	a := mustCreate(t, repo, "a")
	b := mustCreate(t, repo, "b")
	repo.SaveAuthoritative(a, json.RawMessage(`{"content":"https://up.example/x.mp4"}`))
	repo.SaveAuthoritative(b, json.RawMessage(`{"content":"media://y"}`))
	ids, err := repo.SnapshotProjectsContaining("up.example/x.mp4")
	if err != nil || len(ids) != 1 || ids[0] != a {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if ids, _ := repo.SnapshotProjectsContaining(""); ids != nil {
		t.Fatalf("空子串应返回 nil，got %v", ids)
	}
}

// 子串搜索必须精确：区分大小写，且不把 URL 里的 _ 当 LIKE 通配符。
func TestSnapshotProjectsContainingExact(t *testing.T) {
	repo, _ := newRepo(t)
	a := mustCreate(t, repo, "a")
	if _, err := repo.SaveAuthoritative(a, json.RawMessage(`{"content":"https://up.example/A_b.mp4"}`)); err != nil {
		t.Fatal(err)
	}
	for _, miss := range []string{"https://up.example/a_b.mp4", "https://up.example/AXb.mp4"} {
		if ids, err := repo.SnapshotProjectsContaining(miss); err != nil || len(ids) != 0 {
			t.Fatalf("%q 不应命中: ids=%v err=%v", miss, ids, err)
		}
	}
	if ids, err := repo.SnapshotProjectsContaining("https://up.example/A_b.mp4"); err != nil || len(ids) != 1 || ids[0] != a {
		t.Fatalf("精确子串应命中: ids=%v err=%v", ids, err)
	}
}

// updated_at 秒级精度，同秒创建的项目靠 id 兜底排序，翻页才不重不漏。
func TestListForUserStablePaginationOnTies(t *testing.T) {
	repo, conn := newRepo(t)
	for i := 0; i < 5; i++ {
		mustCreate(t, repo, fmt.Sprintf("p%d", i))
	}
	if _, err := conn.Exec(`UPDATE projects SET updated_at = '2026-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, off := range []int{0, 2, 4} {
		page, _, err := repo.ListForUser(uid, 2, off)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page {
			got = append(got, p.ID)
		}
	}
	if len(got) != 5 {
		t.Fatalf("got=%v，want 5 个", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] >= got[i-1] {
			t.Fatalf("应按 id 降序且不重复: %v", got)
		}
	}
}
