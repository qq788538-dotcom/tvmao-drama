package library

import (
	"errors"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestSeedanceAssetsRepoUpsertResetsFailure(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	aid, v := "a1", 1
	if err := repo.Upsert(SeedanceAsset{ObjectKey: "k", UserID: 1, AssetID: &aid, Version: &v, AssetType: "Image", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetError("k", "failed", "boom"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Error == nil || *got.Error != "boom" || got.FirstFailedAt == nil {
		t.Fatalf("失败态不对: %+v", got)
	}
	aid2 := "a2"
	if err := repo.Upsert(SeedanceAsset{ObjectKey: "k", UserID: 1, AssetID: &aid2, Version: &v, AssetType: "Video", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get("k")
	if *got.AssetID != "a2" || got.AssetType != "Video" || got.Status != "pending" || got.Error != nil || got.FirstFailedAt != nil {
		t.Fatalf("重新提交应清空失败信息: %+v", got)
	}
}

// first_failed_at 驱动 1 小时兜底窗口：必须解析成正确的 UTC 时间，且第二次失败不覆盖。
func TestSeedanceAssetsRepoFirstFailedAt(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteSeedanceAssetsRepository(conn)
	if err := repo.Upsert(SeedanceAsset{ObjectKey: "k", UserID: 1, AssetType: "Image", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetError("k", "failed", "x"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get("k")
	if d := time.Since(*got.FirstFailedAt); d < -time.Minute || d > time.Minute {
		t.Fatalf("first_failed_at 偏差 %v（时区解析错了？）", d)
	}
	if _, err := conn.Exec(`UPDATE seedance_assets SET first_failed_at = '2020-01-01 00:00:00' WHERE object_key = 'k'`); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetError("k", "failed", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get("k")
	if got.FirstFailedAt.Year() != 2020 || got.Error != nil {
		t.Fatalf("第二次失败应保留首次时间、空原因写 NULL: %+v", got)
	}
}

func TestSeedanceAssetsRepoGetManyFiltersUser(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	for _, a := range []SeedanceAsset{
		{ObjectKey: "k1", UserID: 1, AssetType: "Image", Status: "active"},
		{ObjectKey: "k2", UserID: 2, AssetType: "Image", Status: "active"},
	} {
		if err := repo.Upsert(a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.GetMany(1, []string{"k1", "k2", "k3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["k1"].Status != "active" {
		t.Fatalf("got %+v", got)
	}
	if empty, err := repo.GetMany(1, nil); err != nil || len(empty) != 0 {
		t.Fatalf("空输入: %v %v", empty, err)
	}
}

func TestSeedanceAssetsRepoListPending(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	v1, v0 := 1, 0
	for _, a := range []SeedanceAsset{
		{ObjectKey: "pending", UserID: 1, Version: &v1, AssetType: "Image", Status: "pending"},
		{ObjectKey: "active-current", UserID: 1, Version: &v1, AssetType: "Image", Status: "active"},
		{ObjectKey: "active-stale", UserID: 1, Version: &v0, AssetType: "Image", Status: "active"},
		{ObjectKey: "failed", UserID: 1, AssetType: "Image", Status: "failed"},
		{ObjectKey: "permanent", UserID: 1, AssetType: "Image", Status: "failed_permanent"},
	} {
		if err := repo.Upsert(a); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListPending(1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, a := range list {
		got[a.ObjectKey] = true
	}
	if len(got) != 3 || !got["pending"] || !got["active-stale"] || !got["failed"] {
		t.Fatalf("待办 = %v", got)
	}
}

func TestSeedanceAssetsRepoGetNotFound(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	if _, err := repo.Get("nope"); !errors.Is(err, ErrSeedanceAssetNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedanceGroupStoreRoundTrip(t *testing.T) {
	store := NewSQLiteSeedanceGroupStore(dbtest.Open(t))
	gid, ver, err := store.GetSeedanceGroup(1)
	if err != nil || gid != "" || ver != 0 {
		t.Fatalf("初始: %q %d %v", gid, ver, err)
	}
	if err := store.SetSeedanceGroup(1, "g-1", 2); err != nil {
		t.Fatal(err)
	}
	gid, ver, err = store.GetSeedanceGroup(1)
	if err != nil || gid != "g-1" || ver != 2 {
		t.Fatalf("回读: %q %d %v", gid, ver, err)
	}
	if _, _, err := store.GetSeedanceGroup(999); !errors.Is(err, ErrSeedanceGroupUserNotFound) {
		t.Fatalf("不存在的用户: %v", err)
	}
}
