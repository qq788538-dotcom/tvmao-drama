package generation

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// v5 §5.5/§8.2:persistOutputs 重放幂等的真实 DB 验证——
// 重放后 asset 行数不变、storage_used_bytes 不变、对象 key 集不变(key 无时间成分)。

// memStorage 记录上传对象,key 确定性(与生产 StaticKey 同语义)。
type memStorage struct{ puts map[string]int }

func (m *memStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	if m.puts == nil {
		m.puts = map[string]int{}
	}
	m.puts[in.Key]++
	return assets.StoredObject{URL: "media://" + in.Key, Size: 100}, nil
}
func (m *memStorage) DeleteObject(context.Context, string) error { return nil }
func (m *memStorage) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (m *memStorage) NewKey(userID int64, filename string) string { return "dated/" + filename }
func (m *memStorage) StaticKey(userID int64, filename string) string {
	return "static/" + filename
}
func (m *memStorage) ExtractKey(u string) (string, bool) { return u, true }
func (m *memStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	panic("unexpected call")
}

func TestPersistOutputsReplayIdempotent(t *testing.T) {
	conn := dbtest.Open(t)
	uid := int64(1)
	tres, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents) VALUES (?, 'm-p', 'processing', '{}', 1)`, uid)
	tid, _ := tres.LastInsertId()

	st := &memStorage{}
	svc := &Service{
		repo:    NewSQLiteRepository(conn),
		storage: st,
		assets:  sqlTestAssets{db: conn},
		users:   sqlTestUsers{db: conn},
		db:      conn,
	}
	urls := []string{"data:image/png;base64,aGk=", "data:image/png;base64,aG8="}

	out1, ids1, mimes1, _ := svc.persistOutputs(context.Background(), uid, tid, nil, urls, 0)
	if len(out1) != 2 || ids1[0] == 0 || ids1[1] == 0 {
		t.Fatalf("首轮应落 2 个槽位, out=%v ids=%v", out1, ids1)
	}
	if mimes1[0] != "image/png" || mimes1[1] != "image/png" {
		t.Fatalf("mime 应从 data URL 头部取得, got %v", mimes1)
	}
	var used1 int64
	_ = conn.QueryRow(`SELECT storage_used_bytes FROM users WHERE id=?`, uid).Scan(&used1)
	if used1 != 200 {
		t.Fatalf("首轮用量应 200, got %d", used1)
	}

	// 重放:行数/用量/对象集全部不变,asset id 与 URL 原样复用。
	out2, ids2, mimes2, _ := svc.persistOutputs(context.Background(), uid, tid, nil, urls, 0)
	if out2[0] != out1[0] || out2[1] != out1[1] || ids2[0] != ids1[0] || ids2[1] != ids1[1] {
		t.Fatalf("重放应复用槽位, out1=%v out2=%v ids1=%v ids2=%v", out1, out2, ids1, ids2)
	}
	if mimes2[0] != mimes1[0] || mimes2[1] != mimes1[1] {
		t.Fatalf("重放应复用槽位记录的 mime, mimes1=%v mimes2=%v", mimes1, mimes2)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE source_task_id=?`, tid).Scan(&n)
	if n != 2 {
		t.Fatalf("重放后 asset 行数应仍为 2, got %d", n)
	}
	var used2 int64
	_ = conn.QueryRow(`SELECT storage_used_bytes FROM users WHERE id=?`, uid).Scan(&used2)
	if used2 != 200 {
		t.Fatalf("重放不得重复计量, got %d", used2)
	}
	// 槽位命中在下载之前——重放没有产生任何新上传。
	for k, c := range st.puts {
		if c != 1 {
			t.Fatalf("对象 %s 被上传 %d 次, 重放不该重传", k, c)
		}
	}
	if len(st.puts) != 2 {
		t.Fatalf("对象集应恰 2 个(key 无时间成分), got %v", st.puts)
	}
}
