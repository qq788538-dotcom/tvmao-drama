package db_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestOpenEnablesPragmas(t *testing.T) {
	conn := dbtest.Open(t)
	var fk, busy int
	var mode string
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if fk != 1 || mode != "wal" || busy != 10000 {
		t.Fatalf("foreign_keys=%d journal_mode=%s busy_timeout=%d", fk, mode, busy)
	}
}

// macOS 的数据目录是 ~/Library/Application Support/…，路径里带空格。
func TestOpenPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "data.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("数据库文件没有建在预期路径: %v", err)
	}
}

func TestMigrateCreatesSchemaAndIsIdempotent(t *testing.T) {
	conn := dbtest.Open(t)
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = '0001_init.sql'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("0001_init.sql 记录 %d 次，want 1", n)
	}
	for _, table := range []string{"users", "settings", "model_configs", "projects", "workflow_snapshots", "assets"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
}

func TestLocalUserSeeded(t *testing.T) {
	conn := dbtest.Open(t)
	var role string
	var quota int64
	if err := conn.QueryRow(`SELECT role, storage_quota_bytes FROM users WHERE id = 1`).Scan(&role, &quota); err != nil {
		t.Fatalf("本地用户不存在: %v", err)
	}
	if role != "admin" || quota != 1125899906842624 {
		t.Fatalf("role=%s quota=%d", role, quota)
	}
}

// 参考仓依赖外键级联删除快照（projects/repository.go HardDelete 的注释）；SQLite 默认不开外键。
func TestSnapshotCascadesWithProject(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, '{}')`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM projects WHERE id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM workflow_snapshots WHERE project_id = ?`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("删除项目后快照仍在：外键级联没有生效")
	}
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (999999, '{}')`); err == nil {
		t.Fatal("给不存在的项目建快照应被外键挡下")
	}
}

func TestUpdatedAtTriggerBumpsOnUpdate(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO projects (user_id, name, updated_at) VALUES (1, 'p', '2000-01-01 00:00:00')`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	if _, err := conn.Exec(`UPDATE projects SET name = 'q' WHERE id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := conn.QueryRow(`SELECT updated_at FROM projects WHERE id = ?`, pid).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Year() == 2000 {
		t.Fatal("UPDATE 后 updated_at 没有刷新")
	}
	if time.Since(updated) > time.Minute || time.Since(updated) < -time.Minute {
		t.Fatalf("updated_at=%v 与当前时间相差过大（时区不是 UTC？）", updated)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	conn := dbtest.Open(t)
	var created time.Time
	if err := conn.QueryRow(`SELECT created_at FROM users WHERE id = 1`).Scan(&created); err != nil {
		t.Fatalf("DATETIME 列扫不进 time.Time: %v", err)
	}
	if created.Location() != time.UTC {
		t.Fatalf("location=%v want UTC", created.Location())
	}
	// Go 侧传参经 db.Time，必须与 CURRENT_TIMESTAMP 同格式，才能直接做字符串比较。
	future := time.Now().Add(time.Hour)
	var earlier int
	if err := conn.QueryRow(`SELECT created_at < ? FROM users WHERE id = 1`, db.Time(future)).Scan(&earlier); err != nil {
		t.Fatal(err)
	}
	if earlier != 1 {
		t.Fatal("db.Time 的格式与 CURRENT_TIMESTAMP 不一致，比较结果错误")
	}
	if got := db.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*3600))); got != "2026-01-01 19:04:05" {
		t.Fatalf("db.Time 没有转成 UTC：%s", got)
	}
}

// _txlock=immediate：事务一开始就拿写锁，并发的读-改-写排队执行，既不报 SQLITE_BUSY 也不丢更新。
func TestImmediateTransactionsSerializeWriters(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES ('counter', '0')`); err != nil {
		t.Fatal(err)
	}
	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := conn.Begin()
			if err != nil {
				errs <- err
				return
			}
			var v int
			if err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM settings WHERE "key" = 'counter'`).Scan(&v); err != nil {
				tx.Rollback()
				errs <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
			if _, err := tx.Exec(`UPDATE settings SET value = ? WHERE "key" = 'counter'`, v+1); err != nil {
				tx.Rollback()
				errs <- err
				return
			}
			errs <- tx.Commit()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发事务出错: %v", err)
		}
	}
	var final int
	if err := conn.QueryRow(`SELECT CAST(value AS INTEGER) FROM settings WHERE "key" = 'counter'`).Scan(&final); err != nil {
		t.Fatal(err)
	}
	if final != workers {
		t.Fatalf("counter=%d want %d（丢更新）", final, workers)
	}
}

// JSON 参数必须以 string 传入：[]byte 会被存成 BLOB，SQLite 3.45 起 json 函数会把 BLOB 当 JSONB。
func TestJSONStringParamStoredAsText(t *testing.T) {
	conn := dbtest.Open(t)
	raw := `{"model":"x","n":2}`
	if _, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('image', 'n', 'm1', '/p', ?)`, raw); err != nil {
		t.Fatal(err)
	}
	var typ string
	var n int
	if err := conn.QueryRow(`SELECT typeof(request_template), json_extract(request_template, '$.n') FROM model_configs WHERE model_id = 'm1'`).Scan(&typ, &n); err != nil {
		t.Fatal(err)
	}
	if typ != "text" || n != 2 {
		t.Fatalf("typeof=%s n=%d", typ, n)
	}
}

func TestOpenFailsOnUnwritableDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open(filepath.Join(blocker, "sub", "data.db"))
	if err == nil {
		conn.Close()
		t.Fatal("在不可用的目录下打开数据库应当报错")
	}
}

// 用户可以把数据目录改到任意文件夹（spec S2）。路径里的 ? 会被驱动当成参数分隔符，
// 库会被静默建到别处且所有 pragma 失效——必须明确报错，而不是静默成功。
func TestOpenRejectsPathsTheDriverWouldMisparse(t *testing.T) {
	base := t.TempDir()
	for _, p := range []string{
		filepath.Join(base, "a?b", "data.db"),
		"file:" + filepath.Join(base, "data.db"),
	} {
		conn, err := db.Open(p)
		if err == nil {
			conn.Close()
			t.Fatalf("Open(%q) 应当报错", p)
		}
	}
}
