package generation

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// 资产事务（asset 行 + 存储用量）失败：先有界重试；仍失败时 http(s) 产物登记孤儿并返回错误
// （保留上游地址可补搬），file:/data: 产物照旧交付、不判失败（P26，方案第 5 节第 6 条）。

// flakyAssets 让 InsertSlotTx 前 failN 次失败。
type flakyAssets struct {
	sqlTestAssets
	failN int32
	calls int32
}

func (f *flakyAssets) InsertSlotTx(tx *sql.Tx, rec assets.AssetRecord, i int) (int64, bool, error) {
	if atomic.AddInt32(&f.calls, 1) <= f.failN {
		return 0, false, errors.New("database is locked")
	}
	return f.sqlTestAssets.InsertSlotTx(tx, rec, i)
}

func zeroAssetTxRetry(t *testing.T) {
	t.Helper()
	old, oldPersist := assetTxRetryDelays, persistRetryDelays
	assetTxRetryDelays = []time.Duration{0, 0}
	persistRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { assetTxRetryDelays, persistRetryDelays = old, oldPersist })
}

func newAssetHarness(t *testing.T, fa *flakyAssets) (*sql.DB, *Service, *[]string) {
	t.Helper()
	conn := dbtest.Open(t)
	fa.sqlTestAssets = sqlTestAssets{db: conn}
	var marked []string
	svc := &Service{
		repo:      NewSQLiteRepository(conn),
		billing:   &billingSpy{},
		storage:   &memStorage{},
		assets:    fa,
		users:     sqlTestUsers{db: conn},
		reclaimer: sqlTestReclaimer{marked: &marked},
		db:        conn,
		tmpDir:    t.TempDir(),
	}
	return conn, svc, &marked
}

func storageUsed(t *testing.T, conn *sql.DB, uid int64) int64 {
	t.Helper()
	var used int64
	if err := conn.QueryRow(`SELECT storage_used_bytes FROM users WHERE id=?`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func TestPersistSlotAssetTxRetriesTransientFailure(t *testing.T) {
	zeroAssetTxRetry(t)
	fa := &flakyAssets{failN: 2}
	conn, svc, marked := newAssetHarness(t, fa)
	urls, ids, _, _, err := svc.persistOutputs(context.Background(), 1, 31, nil, []string{"data:image/png;base64,aGk="}, 0)
	if err != nil || ids[0] == 0 || !strings.HasPrefix(urls[0], "media://") {
		t.Fatalf("重试后应登记成功: urls=%v ids=%v err=%v", urls, ids, err)
	}
	if used := storageUsed(t, conn, 1); used != 100 {
		t.Fatalf("用量应只计一次, got %d", used)
	}
	if len(*marked) != 0 {
		t.Fatalf("成功登记不应标孤儿: %v", *marked)
	}
}

func TestPersistSlotAssetTxExhaustedRemoteKeepsUpstreamURL(t *testing.T) {
	zeroAssetTxRetry(t)
	fa := &flakyAssets{failN: 1 << 20}
	conn, svc, marked := newAssetHarness(t, fa)
	srv, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nxxxx"))
	})
	svc.httpClient = srv.Client()
	raw := srv.URL + "/a.png"
	urls, ids, _, errs, err := svc.persistOutputs(context.Background(), 1, 32, nil, []string{raw}, 0)
	if err != nil {
		t.Fatalf("http 产物登记失败不应让任务失败: %v", err)
	}
	if urls[0] != raw || ids[0] != 0 || len(errs) != 1 || errs[0] == "" {
		t.Fatalf("应保留上游地址并写失败原因: urls=%v ids=%v errs=%v", urls, ids, errs)
	}
	if got := atomic.LoadInt32(&fa.calls); got != int32(1+len(assetTxRetryDelays)) {
		t.Fatalf("资产事务应有界重试 %d 次, got %d", 1+len(assetTxRetryDelays), got)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("资产事务已在内部重试，不应把文件再下载一遍, hits=%d", atomic.LoadInt32(hits))
	}
	if len(*marked) != 1 || (*marked)[0] != "static/gen/task-32-0" {
		t.Fatalf("没登记上的对象应标孤儿, got %v", *marked)
	}
	if used := storageUsed(t, conn, 1); used != 0 {
		t.Fatalf("不应计量, got %d", used)
	}
}

func TestPersistSlotAssetTxExhaustedLocalStillDelivers(t *testing.T) {
	zeroAssetTxRetry(t)
	fa := &flakyAssets{failN: 1 << 20}
	_, svc, marked := newAssetHarness(t, fa)
	urls, ids, mimes, errs, err := svc.persistOutputs(context.Background(), 1, 33, nil, []string{"data:image/png;base64,aGk="}, 0)
	if err != nil {
		t.Fatalf("data: 产物登记失败不应判失败退款: %v", err)
	}
	if !strings.HasPrefix(urls[0], "media://") || ids[0] != 0 || mimes[0] != "image/png" || errs != nil {
		t.Fatalf("应照旧交付本地地址: urls=%v ids=%v mimes=%v errs=%v", urls, ids, mimes, errs)
	}
	if len(*marked) != 0 {
		t.Fatalf("本地产物正被任务引用，不能标孤儿: %v", *marked)
	}
}
