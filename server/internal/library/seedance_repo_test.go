package library

import (
	"errors"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
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

func TestSeedanceAssetsRepoGetNotFound(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	if _, err := repo.Get("nope"); !errors.Is(err, ErrSeedanceAssetNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// 失败后重新提交成功：Upsert 成 active 时旧 error / first_failed_at 必须被清掉。
func TestSeedanceAssetsRepoUpsertActiveClearsOldFailure(t *testing.T) {
	repo := NewSQLiteSeedanceAssetsRepository(dbtest.Open(t))
	if err := repo.Upsert(SeedanceAsset{ObjectKey: "k", UserID: 1, AssetType: "Image", Status: "failed_permanent"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetError("k", "failed_permanent", "素材不合规"); err != nil {
		t.Fatal(err)
	}
	aid, v := "a1", 1
	if err := repo.Upsert(SeedanceAsset{ObjectKey: "k", UserID: 1, AssetID: &aid, Version: &v, AssetType: "Image", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get("k")
	if got.Status != "active" || got.Error != nil || got.FirstFailedAt != nil {
		t.Fatalf("active 行不应残留旧失败信息: %+v", got)
	}
}
