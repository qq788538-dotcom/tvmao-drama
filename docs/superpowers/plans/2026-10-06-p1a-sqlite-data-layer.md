# P1a SQLite 数据层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Go sidecar 拥有一个可用的 SQLite 数据层：连接与迁移、P1a 所需 6 张表的基线结构，以及从参考仓移植过来、在 SQLite 上通过测试的 settings / models / projects 仓库和 canvasstate 画布状态包。

**Architecture:** 新增 `server/internal/db`（modernc 纯 Go SQLite 驱动，WAL + `BEGIN IMMEDIATE` + `busy_timeout`，嵌入式迁移）和测试辅助 `db/dbtest`。其余包从参考仓复制后做最小的 SQL 方言改写，包名、类型名、方法签名保持与参考仓一致（仅把 `MySQLRepository` 改名为 `SQLiteRepository`），以便后续计划继续搬运依赖它们的代码。本计划不改 HTTP 层，交付物以 Go 测试验证。

**Tech Stack:** Go 1.25（module `github.com/james/huabu_studio/apps/api`）、`modernc.org/sqlite` v1.60.1、`database/sql`。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（D2、D4、D5、M1–M4、S6）；事实依据见 `docs/superpowers/plans/p1-research.md` 第 2 节。

**参考仓：** `/Users/james/github/huabu_studio`，commit `fea92ab9ad58ac1aa6752855999c221fcb460d89`，下文简写 `REF=/Users/james/github/huabu_studio/apps/api/internal`。

**P1 后续子计划（各自成文，本计划不含）：** P1b 存储与媒体（本地 `assets.Storage`、MediaSigner、资产/素材库/孤儿回收、项目与画布 HTTP 接口、本地身份中间件）· P1c 生成链路（upstream、generation、billing 空实现、canvasgen 回写与通知）· P1d 其余接口与收尾。输入媒体公网 URL 方案（spec S12）待定，P1a 不涉及。

## Global Constraints

- Go module 路径保持 `github.com/james/huabu_studio/apps/api`；包路径与参考仓 `apps/api/internal/*` 一致（`db`、`settings`、`models`、`projects`、`canvasstate`、`billing`）。
- 仓库类型统一改名：`MySQLRepository` → `SQLiteRepository`，`NewMySQLRepository` → `NewSQLiteRepository`。其余类型名、方法名、方法签名、导出常量、JSON tag 与参考仓逐字一致。
- SQLite 驱动：`modernc.org/sqlite` v1.60.1（纯 Go，`CGO_ENABLED=0` 可交叉编译）。驱动名 `"sqlite"`。
- 连接参数：`foreign_keys(1)`、`journal_mode(WAL)`、`busy_timeout(10000)`、`synchronous(NORMAL)`，事务锁模式 `_txlock=immediate`（所有 `db.Begin()` 一开始就拿写锁，替代 MySQL 的 `SELECT … FOR UPDATE`）。
- **时间**：时间列一律声明为 `DATETIME`，库里存 UTC 文本 `YYYY-MM-DD HH:MM:SS`（即 `CURRENT_TIMESTAMP` 的格式）。SQL 里取当前时间用 `CURRENT_TIMESTAMP`；从 Go 传时间参数必须经 `db.Time(t)` 转成同一格式。不得直接把 `time.Time` 当参数传入。
- **JSON**：JSON 列声明为 `TEXT`。**写入 JSON 参数一律传 `string`，不得传 `[]byte`**——modernc 会把 `[]byte` 存成 BLOB，而 SQLite 3.45 起 json 函数把 BLOB 当作 JSONB 解析，后续计划里 `json_extract` 等查询会出错或读到乱码。
- **布尔**：存 `INTEGER` 0/1。
- MySQL 的 `ON UPDATE CURRENT_TIMESTAMP` 用 `AFTER UPDATE` 触发器实现。
- 本地用户固定为 `users.id = 1`、`role = 'admin'`，存储配额 1 PiB（`1125899906842624` 字节），由迁移预置。
- 迁移文件放 `server/internal/db/migrations/NNNN_*.sql`，按文件名字典序执行，每个文件在一个事务里执行；已执行过的迁移文件不得修改，后续计划新增 `0002_*.sql` 起。
- 代码注释用中文，风格与参考仓一致；从参考仓复制的注释若提到 MySQL 行为且已不成立，就地改写成 SQLite 的说法。
- 不改动 `server/internal/http/**`、`server/internal/localauth/**`、`server/cmd/**`、`src-tauri/**`、`web/**`。

## Review Focus

1. **macOS 数据目录路径带空格**（`~/Library/Application Support/com.miaoju.desktop/`）→ `db.Open` 必须能在含空格的路径上建库。（Task 1 `TestOpenPathWithSpaces`）
2. **后台回写与用户编辑同时写同一个画布快照** → 不得出现 `SQLITE_BUSY` 报错，也不得丢更新。（Task 6 `TestMutateClientSnapshotConcurrentWritersNoLostUpdate`）
3. **JSON 被存成 BLOB** → 后续 json 函数查询失效。（Task 1 `TestJSONStringParamStoredAsText`、Task 4 和 Task 6 中对 `typeof(...)` 的断言）
4. **删除项目后残留孤儿快照** → 依赖外键级联，SQLite 默认不开外键。（Task 1 `TestSnapshotCascadesWithProject`）
5. **清除封面后封面没有回落到自动取图** → `NULLIF(cover_url, '')` 语义必须保留。（Task 6 `TestListForUserCoverFallsBackWhenCleared`）

## 并发执行安排

- 第一批（互相独立，可并发）：**Task 1**（db）、**Task 2**（billing 纯函数）、**Task 5**（canvasstate）。
- 第二批（依赖 Task 1；Task 4 另依赖 Task 2；三者分属不同包，可并发）：**Task 3**（settings）、**Task 4**（models）、**Task 6**（projects）。
- 并发的实现者若在同一工作区，只能 `git add` 自己任务列出的路径；修改 `server/go.mod` / `server/go.sum` 的只有 Task 1（加 modernc 依赖），其余任务不得改动这两个文件。

---

## 文件结构

```
server/internal/
  db/
    sqlite.go                 Open、Migrate、Time
    sqlite_test.go
    migrations/0001_init.sql  P1a 基线：users、settings、model_configs、projects、workflow_snapshots、assets
    dbtest/dbtest.go          测试辅助：临时目录建库并迁移
  billing/
    pricer.go                 从参考仓原样复制（纯函数：ModelBilling、EstimateFreeze、SettleAmount、ValidateExtras…）
    pricer_test.go            原样复制
    extras_test.go            原样复制
  settings/
    repository.go             移植
    repository_test.go        新写（SQLite）
  models/
    repository.go             移植
    repository_test.go        移植（改用 dbtest）
    gen_control_test.go       原样复制
    billing_config_test.go    原样复制
  canvasstate/
    state.go、op_summary.go   原样复制
    state_test.go、op_summary_test.go  原样复制
  projects/
    repository.go             移植
    repository_test.go        新写（SQLite，替换参考仓按 SQL 文本匹配的 fake driver 测试）
```

---

### Task 1: SQLite 连接、迁移与基线表结构

**Files:**
- Create: `server/internal/db/sqlite.go`
- Create: `server/internal/db/migrations/0001_init.sql`
- Create: `server/internal/db/dbtest/dbtest.go`
- Test: `server/internal/db/sqlite_test.go`
- Modify: `server/go.mod`、`server/go.sum`

**Interfaces:**
- Consumes: 无
- Produces（后续所有数据层任务依赖）：
  - `db.Open(path string) (*sql.DB, error)`
  - `db.Migrate(conn *sql.DB) error`
  - `db.TimeLayout = "2006-01-02 15:04:05"`，`db.Time(t time.Time) string`
  - `dbtest.Open(t testing.TB) *sql.DB`：在 `t.TempDir()` 下名为 `test data.db` 的文件建库并迁移，测试结束自动关闭；库里已有 `users.id = 1` 的本地用户。

- [ ] **Step 1: 加依赖**

```bash
cd server && go get modernc.org/sqlite@v1.60.1
```

- [ ] **Step 2: 写失败的测试**

`server/internal/db/sqlite_test.go`：

```go
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
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/db/...`
Expected: 编译失败，`no required module provides package …/internal/db/dbtest` 或 `undefined: db.Open`

- [ ] **Step 4: 写基线迁移**

`server/internal/db/migrations/0001_init.sql`：

```sql
-- 桌面端 SQLite 基线（P1a）：本地用户、设置、模型中心、项目、画布快照、资产。
-- 列与参考仓 MySQL 最终结构一致（由迁移 0001…0099 推导，见 docs/superpowers/plans/p1-research.md）。
-- 约定：时间列 DATETIME，存 UTC 文本 'YYYY-MM-DD HH:MM:SS'；JSON 列 TEXT；布尔 INTEGER 0/1；
-- MySQL 的 ON UPDATE CURRENT_TIMESTAMP 用 AFTER UPDATE 触发器实现。

CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  role TEXT NOT NULL DEFAULT 'user',
  storage_quota_bytes INTEGER NOT NULL DEFAULT 10737418240,
  storage_used_bytes INTEGER NOT NULL DEFAULT 0,
  balance_credits INTEGER NOT NULL DEFAULT 0,
  frozen_credits INTEGER NOT NULL DEFAULT 0,
  upstream_api_key TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 桌面端只有一个本地用户：id 固定为 1，管理员（可用后台配置页），配额 1 PiB 视同不限。
INSERT INTO users (id, email, role, storage_quota_bytes)
VALUES (1, 'local@miaoju.desktop', 'admin', 1125899906842624);

CREATE TABLE settings (
  "key" TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE model_configs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  name TEXT NOT NULL,
  model_id TEXT NOT NULL,
  base_path TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  hidden INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  request_template TEXT NOT NULL,
  polling_template TEXT NULL,
  ui_schema TEXT NULL,
  billing_config TEXT NULL,
  gen_control TEXT NULL,
  image_input_format TEXT NOT NULL DEFAULT 'url',
  image_request_mode TEXT NOT NULL DEFAULT 'json',
  admin_note TEXT NOT NULL DEFAULT '',
  price_note TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT uniq_model_id UNIQUE (model_id)
);

CREATE TABLE projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id),
  name TEXT NOT NULL,
  description TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  deleted_at DATETIME NULL,
  -- 手动指定的封面，canonical 形态 media://key；NULL 或空串 = 未指定，回退到最早一张生成图。
  cover_url TEXT NULL
);
CREATE INDEX idx_projects_user ON projects (user_id);
CREATE INDEX idx_projects_deleted_at ON projects (deleted_at);

CREATE TABLE workflow_snapshots (
  project_id INTEGER PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
  payload TEXT NOT NULL,
  -- 乐观锁版本号，每次成功保存自增。
  version INTEGER NOT NULL DEFAULT 0,
  -- 最后一次客户端保存后的版本号；后端权威回写不更新它。
  last_client_version INTEGER NOT NULL DEFAULT 0,
  synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE assets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NULL,
  project_id INTEGER NULL,
  source_task_id INTEGER NULL,
  object_key TEXT NOT NULL,
  source_kind TEXT NOT NULL DEFAULT 'upload',
  url TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  width INTEGER NULL,
  height INTEGER NULL,
  duration_ms INTEGER NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  library_item_id INTEGER NULL,
  content_hash TEXT NULL,
  -- 生成产物在该任务输出中的序号（0 起）；与 source_task_id 组成唯一键，重放不产生重复资产。
  output_index INTEGER NULL,
  CONSTRAINT uk_assets_task_output UNIQUE (source_task_id, output_index)
);
CREATE INDEX idx_assets_user ON assets (user_id);
CREATE INDEX idx_assets_project ON assets (project_id);
CREATE INDEX idx_assets_object_key ON assets (object_key);
CREATE INDEX idx_assets_library_item_id ON assets (library_item_id);
CREATE INDEX idx_assets_user_hash ON assets (user_id, content_hash);

-- ON UPDATE CURRENT_TIMESTAMP 的等价实现。WHEN 条件让显式写入时间列的 UPDATE 保持原值。
CREATE TRIGGER trg_users_updated_at AFTER UPDATE ON users
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_settings_updated_at AFTER UPDATE ON settings
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE settings SET updated_at = CURRENT_TIMESTAMP WHERE "key" = NEW."key";
END;

CREATE TRIGGER trg_model_configs_updated_at AFTER UPDATE ON model_configs
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE model_configs SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_projects_updated_at AFTER UPDATE ON projects
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE projects SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER trg_workflow_snapshots_synced_at AFTER UPDATE ON workflow_snapshots
FOR EACH ROW WHEN NEW.synced_at = OLD.synced_at
BEGIN
  UPDATE workflow_snapshots SET synced_at = CURRENT_TIMESTAMP WHERE project_id = NEW.project_id;
END;
```

- [ ] **Step 5: 写 db 包**

`server/internal/db/sqlite.go`：

```go
// Package db 负责打开桌面端的 SQLite 数据库并执行嵌入式迁移。
//
// 与参考仓（MySQL）的差异集中在这里：
//   - 事务锁模式 _txlock=immediate：Begin 即拿写锁，替代 SELECT … FOR UPDATE；
//     后台 worker 与 HTTP 写请求争锁时排队等待（busy_timeout），而不是在锁升级处报 SQLITE_BUSY。
//   - WAL：读不阻塞写。
//   - 外键默认关闭，必须显式打开；workflow_snapshots 的级联删除依赖它。
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// TimeLayout 是库里时间列的统一文本格式，与 SQLite 的 CURRENT_TIMESTAMP 一致（UTC，秒级）。
const TimeLayout = "2006-01-02 15:04:05"

// Time 把 Go 时间转成库里的统一格式。SQL 参数里传时间一律经它，才能与列值直接比较。
func Time(t time.Time) string { return t.UTC().Format(TimeLayout) }

// Open 打开（不存在则创建）path 处的数据库。path 是普通文件路径，可以含空格。
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	conn, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// WAL 下多个读连接可并发；写由 immediate 事务 + busy_timeout 串行化。
	conn.SetMaxOpenConns(8)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	return conn, nil
}

// Migrate 按文件名顺序执行尚未执行过的嵌入迁移；每个文件一个事务，失败整体回滚。
func Migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("创建 schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	paths, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		name := strings.TrimPrefix(p, "migrations/")
		if applied[name] {
			continue
		}
		raw, err := migrationsFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := applyMigration(conn, name, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(conn *sql.DB, name, script string) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(script); err != nil {
		tx.Rollback()
		return fmt.Errorf("执行迁移 %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		tx.Rollback()
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	return tx.Commit()
}
```

`server/internal/db/dbtest/dbtest.go`：

```go
// Package dbtest 给各包的仓库测试提供一个真实的、已迁移的 SQLite 库。
package dbtest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

// Open 在临时目录建一个新库并执行全部迁移，测试结束自动关闭。
// 文件名带空格，顺带覆盖 macOS「Application Support」这类路径。
func Open(t testing.TB) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test data.db"))
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return conn
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `cd server && go mod tidy && go test ./internal/db/... -v`
Expected: 10 个测试全部 PASS。

若 `TestOpenEnablesPragmas` 显示 `busy_timeout` 或 `foreign_keys` 没有生效，说明 modernc 没有识别 `_pragma` 参数：检查 v1.60.1 文档里的 DSN 写法并修正 `Open`，不得改测试。若 `tx.Exec(script)` 只执行了第一条语句（表缺失），把迁移脚本按语句逐条执行——注意触发器的 `BEGIN … END;` 内含分号，不能简单按 `;` 切分；在报告里写明采用的做法。若 `TestTimeRoundTrip` 显示扫出的时间不是 UTC 或解析失败，查 v1.60.1 对 DATETIME 列的解析规则后修正 `Open` 的连接参数；改不动就报告 BLOCKED 并附上实际扫出的值，不得放宽测试。

- [ ] **Step 7: 确认既有测试不受影响并提交**

Run: `cd server && go vet ./... && go test ./...`
Expected: 全部 PASS（含 P0 的 localauth、http、cmd/sidecar）。

```bash
git add server/go.mod server/go.sum server/internal/db
git commit -m "feat(server): SQLite 数据层——连接、嵌入式迁移与 P1a 基线表结构"
```

---

### Task 2: 计费纯函数（billing/pricer）

**Files:**
- Create: `server/internal/billing/pricer.go`（从参考仓原样复制）
- Test: `server/internal/billing/pricer_test.go`、`server/internal/billing/extras_test.go`（原样复制）

**Interfaces:**
- Consumes: 无
- Produces（Task 4 的 models 依赖）：`billing.ModelBilling`、`billing.ChargeItem`、`billing.Price`、`billing.ExtraCharge`、`billing.EstimateFreeze(m ModelBilling, req map[string]any) int64`、`billing.SettleAmount(m ModelBilling, req, resp map[string]any) (int64, error)`、`billing.ValidateExtras(extras []ExtraCharge) error`。

这些是纯计算函数，不涉及数据库和扣费。桌面端不计费（spec X3），但 models 的 `BillingConfig()` 和后续计划里的生成链路都引用这些类型，保留可以让搬过来的代码不改调用点。

- [ ] **Step 1: 复制文件**

```bash
mkdir -p server/internal/billing
cp $REF/billing/pricer.go server/internal/billing/pricer.go
cp $REF/billing/pricer_test.go server/internal/billing/pricer_test.go
cp $REF/billing/extras_test.go server/internal/billing/extras_test.go
```

（`REF=/Users/james/github/huabu_studio/apps/api/internal`）

- [ ] **Step 2: 确认这三个文件只引用标准库**

Run: `grep -h '"' server/internal/billing/*.go | grep -E '^\s*"[a-z]' | sort -u`
Expected: 只出现标准库包（`encoding/json`、`fmt`、`math`、`reflect`、`strconv`、`strings`、`testing`、`unicode/utf8`），没有 `github.com/...`。若出现对参考仓其他文件里符号的引用导致编译失败，停下来报告 BLOCKED，不要从参考仓继续搬文件。

- [ ] **Step 3: 运行测试**

Run: `cd server && go test ./internal/billing/ -v`
Expected: 全部 PASS。

- [ ] **Step 4: 提交**

```bash
git add server/internal/billing
git commit -m "feat(server): 移植计费纯函数（billing/pricer）"
```

---

### Task 3: settings 仓库

**Files:**
- Create: `server/internal/settings/repository.go`
- Test: `server/internal/settings/repository_test.go`

**Interfaces:**
- Consumes: `dbtest.Open`（Task 1）
- Produces：`settings.Repository` 接口（`Get(key string) (string, error)`、`Set(key, value string) error`、`All() (map[string]string, error)`）；`settings.SQLiteRepository`；`settings.NewSQLiteRepository(db *sql.DB) *SQLiteRepository`。`Get` 对不存在的 key 返回 `("", nil)`。

- [ ] **Step 1: 写失败的测试**

`server/internal/settings/repository_test.go`：

```go
package settings

import (
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestGetMissingKeyReturnsEmpty(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	v, err := repo.Get("base_domain")
	if err != nil || v != "" {
		t.Fatalf("got %q, %v; want empty, nil", v, err)
	}
}

func TestSetInsertsThenOverwrites(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	if err := repo.Set("base_domain", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Set("base_domain", "https://b.example"); err != nil {
		t.Fatal(err)
	}
	v, err := repo.Get("base_domain")
	if err != nil || v != "https://b.example" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestAllReturnsEveryKey(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	_ = repo.Set("a", "1")
	_ = repo.Set("b", `{"x":2}`)
	all, err := repo.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["a"] != "1" || all["b"] != `{"x":2}` {
		t.Fatalf("all=%v", all)
	}
}

// 后台配置页保存的 JSON 配置（如 canvas_postprocess_config）可能很大；TEXT 无长度限制。
func TestSetLargeValue(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	big := make([]byte, 200_000)
	for i := range big {
		big[i] = 'x'
	}
	if err := repo.Set("big", string(big)); err != nil {
		t.Fatal(err)
	}
	v, _ := repo.Get("big")
	if len(v) != len(big) {
		t.Fatalf("len=%d want %d", len(v), len(big))
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/settings/`
Expected: 编译失败，`undefined: NewSQLiteRepository`

- [ ] **Step 3: 写实现**

从参考仓复制后改写：

```bash
mkdir -p server/internal/settings
cp $REF/settings/repository.go server/internal/settings/repository.go
```

然后在 `server/internal/settings/repository.go` 中做以下替换（共 4 处，其余保持原样）：

1. `type MySQLRepository struct{ db *sql.DB }` → `type SQLiteRepository struct{ db *sql.DB }`
2. `func NewMySQLRepository(db *sql.DB) *MySQLRepository { return &MySQLRepository{db: db} }` → `func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }`
3. 三个方法的接收者 `(r *MySQLRepository)` → `(r *SQLiteRepository)`
4. `Set` 里的 SQL：
   - 原：`"INSERT INTO settings (`key`, value) VALUES (?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value)"`
   - 新：`` `INSERT INTO settings ("key", value) VALUES (?, ?) ON CONFLICT ("key") DO UPDATE SET value = excluded.value` ``
   并把 `Get`、`All` 里的 `` `key` `` 反引号改成双引号 `"key"`（SQLite 标准写法）。

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/settings/ -v`
Expected: 4 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/settings
git commit -m "feat(server): 移植 settings 仓库到 SQLite"
```

---

### Task 4: models 仓库

**Files:**
- Create: `server/internal/models/repository.go`（移植）
- Test: `server/internal/models/repository_test.go`（移植）、`server/internal/models/gen_control_test.go`、`server/internal/models/billing_config_test.go`（原样复制）

**Interfaces:**
- Consumes: `dbtest.Open`（Task 1）；`billing.ModelBilling`、`billing.EstimateFreeze`（Task 2）
- Produces：`models.ModelConfig`（字段与 JSON tag 同参考仓）及其方法 `GenConcurrency()`、`GenRetry(module)`、`InputLimits()`、`InputDuration()`、`BillingConfig()`；常量 `DefaultGenConcurrency`、`ImageInputFormat*`、`ImageRequestMode*`；`models.ErrNotFound`；`models.Repository` 接口（`Create`、`Update`、`Delete`、`List(onlyVisible bool)`、`GetByModelID`、`GetByID`）；`models.SQLiteRepository`、`models.NewSQLiteRepository(db *sql.DB)`；`(*SQLiteRepository).FreezeEstimateForModel(slug string) (int64, error)`。

- [ ] **Step 1: 复制文件**

```bash
mkdir -p server/internal/models
cp $REF/models/repository.go server/internal/models/repository.go
cp $REF/models/repository_test.go server/internal/models/repository_test.go
cp $REF/models/gen_control_test.go server/internal/models/gen_control_test.go
cp $REF/models/billing_config_test.go server/internal/models/billing_config_test.go
```

- [ ] **Step 2: 把 repository_test.go 改成用 SQLite，并补 JSON 存储断言（先让测试失败）**

在 `server/internal/models/repository_test.go` 中：

1. 删掉 `import` 里的 `"os"`、`_ "github.com/go-sql-driver/mysql"`、`"github.com/james/huabu_studio/apps/api/internal/db"`，加上 `"github.com/james/huabu_studio/apps/api/internal/db/dbtest"`。
2. 把整个 `openTestDB` 函数体替换为：

```go
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.Open(t)
}
```

3. 文件中所有 `NewMySQLRepository(` 替换为 `NewSQLiteRepository(`。
4. 文件第一行的路径注释改成 `// server/internal/models/repository_test.go`。
5. 在文件末尾追加：

```go
// JSON 列必须以 TEXT 落库：BLOB 会被 SQLite 的 json 函数当作 JSONB。
func TestJSONColumnsStoredAsText(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSQLiteRepository(conn)
	m, err := repo.Create(ModelConfig{
		Type: "video", Name: "n", ModelID: "json-text-check", BasePath: "/v1/x",
		Enabled:          true,
		RequestTemplate:  json.RawMessage(`{"model":"x"}`),
		PollingTemplate:  json.RawMessage(`{"path":"/t/{{task_id}}"}`),
		UISchema:         json.RawMessage(`{"duration":{"default":5}}`),
		BillingConfigRaw: json.RawMessage(`{"flatFee":1}`),
		GenControlRaw:    json.RawMessage(`{"concurrency":3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"request_template", "polling_template", "ui_schema", "billing_config", "gen_control"} {
		var typ string
		if err := conn.QueryRow(`SELECT typeof(`+col+`) FROM model_configs WHERE id = ?`, m.ID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ != "text" {
			t.Fatalf("%s 存成了 %s，want text", col, typ)
		}
	}
	got, err := repo.GetByID(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GenConcurrency() != 3 || string(got.UISchema) != `{"duration":{"default":5}}` {
		t.Fatalf("回读不一致: concurrency=%d uiSchema=%s", got.GenConcurrency(), got.UISchema)
	}
}

func TestListOnlyVisibleFiltersDisabledAndHidden(t *testing.T) {
	repo := NewSQLiteRepository(openTestDB(t))
	mk := func(id string, enabled, hidden bool, sort int) {
		if _, err := repo.Create(ModelConfig{Type: "image", Name: id, ModelID: id, BasePath: "/p",
			Enabled: enabled, Hidden: hidden, SortOrder: sort}); err != nil {
			t.Fatal(err)
		}
	}
	mk("visible-b", true, false, 2)
	mk("visible-a", true, false, 1)
	mk("disabled", false, false, 0)
	mk("hidden", true, true, 0)
	list, err := repo.List(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ModelID != "visible-a" || list[1].ModelID != "visible-b" {
		t.Fatalf("list=%v", list)
	}
	all, _ := repo.List(false)
	if len(all) != 4 {
		t.Fatalf("all=%d want 4", len(all))
	}
}

func TestCreateDuplicateModelIDFails(t *testing.T) {
	repo := NewSQLiteRepository(openTestDB(t))
	m := ModelConfig{Type: "image", Name: "n", ModelID: "dup", BasePath: "/p"}
	if _, err := repo.Create(m); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(m); err == nil {
		t.Fatal("重复的 model_id 应当违反唯一约束")
	}
}
```

Run: `cd server && go test ./internal/models/`
Expected: 编译失败，`undefined: NewSQLiteRepository`

- [ ] **Step 3: 改写 repository.go**

在 `server/internal/models/repository.go` 中：

1. 类型与构造函数改名：`MySQLRepository` → `SQLiteRepository`（类型定义、构造函数 `NewMySQLRepository` → `NewSQLiteRepository`、所有方法接收者）。
2. **JSON 参数改传 string**：
   - `Create` 和 `Update` 的 `Exec` 参数里的 `[]byte(m.RequestTemplate)` → `string(m.RequestTemplate)`。
   - 两处 `polling = []byte(m.PollingTemplate)` → `polling = string(m.PollingTemplate)`；两处 `uiSchema = []byte(m.UISchema)` → `uiSchema = string(m.UISchema)`。
   - `nullableJSON` 的 `return []byte(raw)` → `return string(raw)`。
3. `List` 里的 `WHERE enabled = TRUE AND hidden = FALSE` → `WHERE enabled = 1 AND hidden = 0`。
4. 不改其余任何逻辑（`scanModelConfig` 里扫到 `[]byte` / `sql.NullString` 的写法在 SQLite 下可用）。

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/models/ -v`
Expected: 参考仓原有 3 个仓库测试 + 新增 3 个 + `gen_control_test.go`、`billing_config_test.go` 全部 PASS，无 skip。

- [ ] **Step 5: 提交**

```bash
git add server/internal/models
git commit -m "feat(server): 移植 models 仓库到 SQLite（JSON 列以 TEXT 落库）"
```

---

### Task 5: canvasstate 画布状态包

**Files:**
- Create: `server/internal/canvasstate/state.go`、`server/internal/canvasstate/op_summary.go`（原样复制）
- Test: `server/internal/canvasstate/state_test.go`、`server/internal/canvasstate/op_summary_test.go`（原样复制）

**Interfaces:**
- Consumes: 无（只用标准库）
- Produces（P1b 的画布 mutation 接口、P1c 的回写依赖）：参考仓 `canvasstate` 包的全部导出符号，包括 `CanvasOpKind` 与 `Op*` 常量、`CanvasOpSummary`，以及 `state.go` 里对快照做 upsert / patch / 删除 / 连线的函数和 `ErrNodeNotFound`。

- [ ] **Step 1: 复制文件**

```bash
mkdir -p server/internal/canvasstate
cp $REF/canvasstate/state.go $REF/canvasstate/op_summary.go $REF/canvasstate/state_test.go $REF/canvasstate/op_summary_test.go server/internal/canvasstate/
```

- [ ] **Step 2: 确认只依赖标准库**

Run: `grep -h '"' server/internal/canvasstate/*.go | grep -E '^\s*"[a-z]' | sort -u`
Expected: 只有标准库包。

- [ ] **Step 3: 运行测试**

Run: `cd server && go test ./internal/canvasstate/ -v`
Expected: 24 个测试全部 PASS（state_test 17 个、op_summary_test 7 个）。

- [ ] **Step 4: 提交**

```bash
git add server/internal/canvasstate
git commit -m "feat(server): 移植 canvasstate 画布状态包"
```

---

### Task 6: projects 仓库

**Files:**
- Create: `server/internal/projects/repository.go`（移植）
- Test: `server/internal/projects/repository_test.go`（新写，替换参考仓按 SQL 文本匹配的 fake driver 测试）

**Interfaces:**
- Consumes: `dbtest.Open`、`db.Time`（Task 1）
- Produces（P1b 的项目与画布接口、P1c 的回写依赖）：`projects.Project`、`projects.Snapshot`、`projects.PendingProject`、`projects.ErrNotFound`、`projects.Repository` 接口（`Create`、`ListForUser`、`GetForUser`、`Update`、`SetCover`、`Delete`、`SaveAuthoritative`、`MutateSnapshot`、`MutateClientSnapshot`、`GetLatest`、`ListPendingDeletion`、`HardDelete`），以及 `SQLiteRepository` 上的额外方法 `GetLatestSnapshot(projectID int64) (json.RawMessage, error)`、`SnapshotProjectsContaining(substr string) ([]int64, error)`；`projects.NewSQLiteRepository(db *sql.DB)`。语义与参考仓逐字一致：
  - `MutateSnapshot`：快照行不存在且回调有改动时建行（version=1、last_client_version=0）；无改动返回当前 version，行不存在返回 0。
  - `MutateClientSnapshot`：同上，但建行与更新时 last_client_version 跟随新 version。
  - `SaveAuthoritative`：不存在则建行 version=1，存在则覆盖 payload 并 version+1，返回写入后的 version。

- [ ] **Step 1: 写失败的测试**

`server/internal/projects/repository_test.go`：

```go
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
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/projects/`
Expected: 编译失败，`undefined: NewSQLiteRepository`

- [ ] **Step 3: 复制并改写 repository.go**

```bash
mkdir -p server/internal/projects
cp $REF/projects/repository.go server/internal/projects/repository.go
```

在 `server/internal/projects/repository.go` 中做以下改动，其余逻辑和注释保持原样：

1. 类型与构造函数改名：`MySQLRepository` → `SQLiteRepository`、`NewMySQLRepository` → `NewSQLiteRepository`、所有方法接收者。
2. `Delete`：`SET deleted_at = NOW()` → `SET deleted_at = CURRENT_TIMESTAMP`。
3. `SaveAuthoritative` 的 SQL：
   - 原：`INSERT INTO workflow_snapshots (project_id, payload, version) VALUES (?, ?, 1) ON DUPLICATE KEY UPDATE payload = VALUES(payload), version = version + 1`
   - 新：`INSERT INTO workflow_snapshots (project_id, payload, version) VALUES (?, ?, 1) ON CONFLICT (project_id) DO UPDATE SET payload = excluded.payload, version = workflow_snapshots.version + 1`
4. `MutateSnapshot` 与 `MutateClientSnapshot` 里两处 `SELECT payload, version FROM workflow_snapshots WHERE project_id = ? FOR UPDATE` 去掉末尾的 ` FOR UPDATE`。并把 `MutateSnapshot` 函数注释里「SELECT…FOR UPDATE 拿当前 payload」「用行锁串行化」改写为：「事务以 BEGIN IMMEDIATE 开始（db.Open 的 _txlock=immediate），一开始就拿到库级写锁，并发的读-改-写排队执行，防丢更新」。接口 `Repository` 上 `MutateSnapshot` 的注释同样把 `SELECT…FOR UPDATE` 改为 `BEGIN IMMEDIATE`。
5. **JSON 参数改传 string**：`SaveAuthoritative`、`MutateSnapshot`（建行与更新两处）、`MutateClientSnapshot`（建行与更新两处）里传给 `Exec` 的 `[]byte(payload)` / `[]byte(next)` 一律改为 `string(payload)` / `string(next)`。读取侧扫到 `[]byte` 的写法不变。
6. `SnapshotProjectsContaining`：`payload LIKE CONCAT('%', ?, '%')` → `payload LIKE '%' || ? || '%'`。
7. `SetCover` 函数注释里「MySQL 对新值与旧值相同的 UPDATE 报告 0 行受影响」一句保留并补一句：「SQLite 报告匹配行数，但存在性仍单独查，两种库行为一致」。

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/projects/ -race -v`
Expected: 16 个测试全部 PASS，`-race` 无数据竞争报告。

- [ ] **Step 5: 全量回归并提交**

Run: `cd server && go vet ./... && go test ./...`
Expected: 全部 PASS。

```bash
git add server/internal/projects
git commit -m "feat(server): 移植 projects 仓库到 SQLite（BEGIN IMMEDIATE 替代 FOR UPDATE）"
```

---

## P1a 完成标准

- `cd server && go vet ./... && go test ./...` 全部通过，没有任何测试被 skip。
- `cd server && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...` 成功（纯 Go 驱动可交叉编译）。
- 6 个包（db、billing、settings、models、canvasstate、projects）的导出 API 与本计划各任务的 Interfaces 一致。
