# P2a 内置模型与配置数据 Implementation Plan

> **实施后注（2026-10-06，正文未改）**
> - 迁移实际编号为 `0003_builtin_overrides.sql`。种子里的 `base_domain` 保留生产 IP `http://115.191.35.118:3000`（用户授权，发布前换域名只改 seed 一行）；打光/多角度 template 视为"预设提示词"允许编辑；`subtitleRemovalModels`/`modelCandidates` 不允许增删条目。出处：p2a 账本 Ruling。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 sidecar 首次启动时把编译进二进制的内置模型（`model_configs`）和画布配置（`settings`）导入 SQLite，升级时按 model_id 做字段级合并并保留用户改过的字段，并提供一个只放行安全字段、带校验和“恢复默认”的 Go 服务，供 P4 的 HTTP 层调用。

**Architecture:** 新增包 `server/internal/builtin`。种子是仓库根 `seed/` 两个 JSONL 文件的逐字节副本，经 `go:embed` 编译进去；新迁移给 `model_configs`、`settings` 各加一列 `user_overrides`（用户改过的字段路径，JSON 字符串数组），另建 `builtin_seed_state` 记录已导入种子的哈希。所有“可改部分”都被看成一份 JSON 文档，用一套字段路径语法（`name`、`ui_schema.ratio.default`、`presets[rembrandt].prompt`、`$`）表达白名单、覆盖记录和编辑请求；合并 = 以新版种子为底、把覆盖路径上的用户值逐个盖上去，盖不上或不再合法的丢弃。本计划不写 HTTP 接口和 UI（属于 P4）。

**Tech Stack:** Go 1.26（module `github.com/james/huabu_studio/apps/api`）、`modernc.org/sqlite` v1.60.1、`database/sql`、`embed`；不新增依赖。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（M1、M2、M3、M4、M8、M9、M10、M11）；SQLite 约束见 `docs/superpowers/plans/p1-research.md` 第 6 节；种子说明见 `seed/README.md`。

**参考仓（只读）：** `/Users/james/github/huabu_studio/apps/api/internal`（下称 `REF`）。校验规则对照：`REF/http/admin_model_handlers.go`（保存模型前 `billing.ValidateExtras`）、`REF/http/gen_image_preset_handlers.go`、`REF/http/multi_angle_handlers.go`、`REF/http/light_control_handlers.go`、`REF/canvasgen/*_config.go`、`REF/canvasscript/service.go` 的 `Config`。后台导出格式 v1（`apps/admin/src/routes/ModelCenterPage.tsx` 的 `EXPORT_VERSION`）只在开发侧使用；`seed/` 是生产库逐行导出的 JSONL，不是 v1 导出文件，本计划直接读 JSONL。

**相关计划：** P2b（Skill、Skill 分类、Agent 工具、Agent 大模型、AI 角色库的种子，各表尚未建）另行成文，复用本计划的 `builtin_seed_state` 表和 `builtin` 包，见文末“给 P2b / P4 的约定”。P1b（存储与媒体）和 P3a（前端）由其他计划并行编写，本计划不碰它们的文件。

## Global Constraints

- Go module 路径 `github.com/james/huabu_studio/apps/api`；新包路径 `server/internal/builtin`，包名 `builtin`。不新增第三方依赖，不改 `server/go.mod` / `server/go.sum`。
- **不修改 P1a 已有代码**：`server/internal/{db/sqlite.go, db/sqlite_test.go, db/migrations/0001_init.sql, db/dbtest, models, settings, projects, billing, canvasstate}` 一律只读。本计划只新增文件；唯一例外是 Task 9 在 `server/cmd/sidecar/main.go` 加一次调用，以及 Task 2 在 `seed/README.md` 末尾追加一段说明。
- **迁移**：新表、新列只放新迁移文件。P1b 计划已占用 `0002_library.sql`，本计划预留 **`0003_builtin_overrides.sql`**；执行时若 main 上已有别的 `0003_*.sql`，改名为下一个空闲编号。迁移器按完整文件名记录、按文件名排序执行，本迁移只做 `ADD COLUMN` 和建新表，只要求排在 0001 之后，与 P1b 的 0002 无依赖（P2a 先于 P1b 合并也能正常执行）。首个正式版发布前改名安全；开发机上已按旧名执行过的库，删库重建。
- **JSON 参数一律传 `string`**，不得传 `[]byte`（会存成 BLOB）。`user_overrides` 列带 `CHECK (typeof(user_overrides) = 'text')`。
- **时间**：SQL 里用 `CURRENT_TIMESTAMP`；不绑定 `time.Time` 参数。种子里生产库的 `created_at` / `updated_at` 不导入。
- **事务**：所有 `BeginTx(ctx, nil)` 一开始就拿库级写锁（`_txlock=immediate`）。持有事务时只能经 `tx` 读写，不得再经 `*sql.DB` 或 `models.SQLiteRepository` / `settings.SQLiteRepository` 写库（会等满 10 秒后 `SQLITE_BUSY`）；事务内不做网络 I/O。为此本包的 SQL 全部集中在 `store.go`，函数接收 `querier`（`*sql.DB` 或 `*sql.Tx`）。
- **`RowsAffected` 是匹配行数**，本包不依赖它判断是否有改动。
- **种子真源是仓库根 `seed/`**；`server/internal/builtin/seeddata/` 必须与之逐字节一致（`scripts/sync-seed.sh` 同步，`TestEmbeddedSeedMatchesRepoSeed` 检查）。
- **内置数据只能经 `builtin.Service` 修改**：P4 不得用 `models.Repository` 的 `Create` / `Update` / `Delete` 改 `model_configs`，也不得用 `settings.Repository.Set` 写种子里的 key（否则 `user_overrides` 失真，升级时用户修改丢失或技术字段被改）。`upstream_api_key` 不是内置数据，P4 经 `settings.Repository` 读写（spec M3）。
- 代码注释、错误信息用中文；新增未导出标识符在包内不得与本计划其他任务同名（各任务的 Interfaces 已列出全部名字）。

## 设计决策

| # | 问题 | 决定 | 理由 |
|---|---|---|---|
| D1 | 种子放在哪 | `seed/model_configs.jsonl`、`seed/settings.jsonl` 复制到 `server/internal/builtin/seeddata/`，`//go:embed` 嵌入。`scripts/sync-seed.sh` 负责复制，`TestEmbeddedSeedMatchesRepoSeed` 比对仓库根 `seed/` 与副本，不一致则 `go test` 失败 | `go:embed` 不能引用包目录之外的文件；保留 `seed/` 作为唯一真源（spec E1 目录结构、seed/README 的重新导出流程），副本由测试兜底，忘记同步无法通过 CI |
| D2 | JSONL → 表的映射 | `SeedModel` 字段名逐一对应生产列名（`model_id`、`base_path`、`request_template`……），JSON 列是对象，用 `json.RawMessage` 原样取出、以 TEXT 落库；`enabled` / `hidden` 接受 0/1/true/false；`null` → NULL。解码 `DisallowUnknownFields`。`settings.jsonl` 的 `value` 本身就是字符串，原样落库 | 字节级保留生产配置，不经 Go 结构体往返（不丢未知 JSON 字段、不改键顺序）；生产库加新列时重新导出会让测试立即失败，而不是静默丢列 |
| D3 | 保留生产 id | 插入时显式写 `id`；升级时同一 `model_id` 若换了 id，主键改成种子 id（先挪到负数临时 id 再写回，避免撞车） | P2b 的 `agent_tools.source_model_id` 引用生产 id。M8 禁止用户新增模型，P4 不暴露 Create，`model_configs` 里只会有种子行，所以与“用户数据”的 id 冲突不可能发生；种子内 id、model_id 唯一由 `parseSeed` 校验 |
| D4 | 首次启动 / 升级如何识别 | `builtin_seed_state(dataset, hash)`；数据集 `model_center` 的哈希 = sha256(`seedFormatVersion` + 两个 JSONL 文件内容)。哈希相同 → 直接返回（只读，不拿写锁）；不同或没有记录 → 在一个写事务里合并并写入新哈希。首次启动就是“与空库合并”，和升级是同一套算法 | 一套算法覆盖两种情形，没有“首次导入”特例；合并本身幂等（同一种子合并两次结果相同），哈希只是省掉每次启动的写事务。事务失败整体回滚、哈希不写，下次启动重试。`seedFormatVersion` 让合并语义变化时即使种子没变也重新合并。两个文件合用一个哈希，因为配置的校验依赖模型集合 |
| D5 | 如何记录用户改过的字段 | `model_configs.user_overrides`、`settings.user_overrides`：排序后的字段路径 JSON 数组，默认 `'[]'`。编辑时，新值与**当前二进制种子**同一路径的值相等就移出数组（回到跟随内置），否则加入 | 不需要另存“种子基线”：合并后未覆盖的字段恒等于当前种子，编辑时拿嵌入种子比较即可。按路径而不是整列记录，才能做到 M11 的字段级合并（例如只改了 uiSchema 某个参数的默认值，选项列表仍跟随新版） |
| D6 | 字段路径语法 | `seg(.seg)*`，`seg = name | name[key]`，`$` 表示整个值。`name[key]` 按数组元素的 `"key"` 属性寻址 | 预设数组按 key 而不是下标寻址，新版调整预设顺序或插入新预设时用户修改不会错位；同一语法同时用于白名单模式（`*` 通配）、覆盖记录和编辑请求 |
| D7 | 合并算法 | 见 Task 6：删除种子里没有的模型 → 主键对齐 → 新模型插入；已有模型以新版种子行为底，把 `user_overrides` 里每个路径上的用户值盖上去；路径在新版不再可改、找不到挂载点（预设 / uiSchema 参数被删）或新值不再通过校验（选项被删、引用的模型被删）时丢弃该覆盖并记日志。没有覆盖的行、配置原样写成种子原文（逐字节一致）。配置在模型之后合并 | 满足 M11 三条：新模型加入、去掉的模型移除、字段级“没改过跟随新版、改过保留”；丢弃而不是硬保留不合法的值，避免升级后出现指向已删除模型或不存在选项的配置 |
| D8 | M10 可改字段 | 模型：`name`、`enabled`、`hidden`、`sort_order`、`ui_schema.*.default`、`gen_control.concurrency`、`gen_control.modules.canvas.maxRetries`、`gen_control.modules.canvas.retryIntervalSec`。配置：见下表。其余全部只读，包括 `request_template`、`polling_template`、`base_path`、`image_input_format`、`image_request_mode`、`billing_config`、`gen_control.inputLimits/inputDuration`、`gen_control.modules.drama`、所有预设的 `key`/`name`/结构参数、`canvas_script_config.styles` | spec M10 原文：启用/隐藏、显示名、排序、uiSchema 默认值、并发和重试，以及 settings 里的预设提示词、候选模型。drama 模块桌面端不用，不开放；预设名、缩略图、方向等属于结构字段 |
| D9 | `base_domain` | 当作纯文本配置（`kindText`），唯一可改路径 `$`；校验：`http://` 或 `https://`、有主机、不带路径/参数/片段/用户信息、首尾无空白、不以 `/` 结尾。种子里的生产地址随包发布，用户改过则升级保留 | spec M2 全局一个 base_domain；上游地址 = base_domain + 模型的 base_path（以 `/` 开头），结尾多一个 `/` 会拼出 `//v1/…` |
| D10 | 上游 API Key | 不在种子里（`seed/README.md`：导出时已去掉）；`parseSeed` 拒绝名字像密钥的配置（`_key` 结尾、含 token/secret/password）。`upstream_api_key` 存 `settings` 表（spec M3），由 P4 经 `settings.Repository` 读写；`Apply` 只处理种子里的 key，永远不读写它；`Service` 把它当“不是内置数据”（`ErrNotFound`） | 防止重新导出时把密钥编译进安装包；Key 的增删改与内置数据的合并互不影响 |
| D11 | 种子里删掉的配置 key | 保留不删 | 无法区分某个 key 来自旧种子还是运行时写入（如 `upstream_api_key`）；不再被读取的 key 留着无害 |
| D12 | 启动时机 | `cmd/sidecar/main.go` 在 `db.Migrate(conn)` 之后、开始监听和打印 `READY` 之前调用 `builtin.Apply(conn)`，失败则 sidecar 以非 0 退出 | 合并失败说明库或种子有问题，带着半套内置数据启动更难排查；打印 READY 前完成，前端第一次请求时数据已就绪。`db.Open` / `db.Migrate` 的装配由 P1b 的最后一个任务加进 main.go，本计划 Task 9 只加一行调用，必须在其后合并 |

**内置配置的可改字段（D8 续）：**

| key | 值类型 | 可改路径 | 校验 |
|---|---|---|---|
| `base_domain` | 纯文本 | `$` | 见 D9 |
| `canvas_light_control` | JSON | `models`、`template`、`presets[*].prompt` | `models`：image 类型模型 ID 数组、存在、不重复，可为空；`template`：≤2000 字，非空时必须含 `{light}`（空 = 运行时用缺省模板）；预设提示词：字符串 ≤4000 字，可空 |
| `canvas_multi_angle` | JSON | `models`、`template`、`presets[*].prompt` | 同上，占位符 `{angle}` |
| `gen_image_prompt_presets` | JSON | `models`、`presets[*].value.prompt`、`presets[*].value.params[*].options[*].prompt` | 正文不能为空（与参考仓后台一致）≤8000 字；档位片段可空 ≤2000 字 |
| `canvas_postprocess_config` | JSON | `subtitleRemovalModels`、`upscaleModel` | 去字幕列表：对象数组，只允许 `modelId`（video 类型、存在、不重复）、`label`（非空）、`subtitle`（字符串）、`isTimeLimited`（布尔）；`upscaleModel`：空或 video 类型模型 ID |
| `canvas_script_config` | JSON | `modelCandidates`、`concurrency`、`maxRetries` | 候选：对象数组，只允许 `modelId`（text 类型、存在、不重复）、`displayName`（非空）；`concurrency` 1–20；`maxRetries` 0–10 |

模型字段校验：`name` 非空、首尾无空白、≤100 字；`enabled`/`hidden` 布尔；`sort_order` 整数 −1,000,000～1,000,000；`ui_schema.<参数>.default` 必须等于该参数 `options[].value` 之一（按 JSON 语义比较，`10` 与 `10.0` 相等）；`gen_control.concurrency` 0–200（0 = 默认 200）；`maxRetries` 0–10；`retryIntervalSec` 0–600。所有内置种子值都必须通过自己的校验（`TestSeedValuesPassTheirOwnRules`）。

## Review Focus

1. **升级后用户选的 uiSchema 默认值已不在新版选项里** → 丢弃该覆盖、改用新版默认值，不能留下一个不存在的默认值。（Task 6 `TestApplyDropsOverrideWhenDefaultNoLongerOffered`）
2. **用户在配置里选的候选模型在新版里被删了** → 丢弃该覆盖、回到新版候选列表，不能让配置指向不存在的模型。（Task 6 `TestApplyDropsSettingOverrideReferencingRemovedModel`）
3. **用户把字段改回内置值** → 覆盖记录清除，之后升级该字段跟随新版；否则用户一次误改就永远收不到这个字段的更新。（Task 7 `TestServiceUpdateModelBackToSeedClearsOverride`、Task 8 `TestServiceEditsSurviveUpgradeAndRestoreFollowsSeed`）
4. **启动合并中途失败**（磁盘满、约束冲突）→ 整体回滚、不写新哈希，下次启动重试，不留半套数据。（Task 6 `TestApplyRollsBackOnFailure`）
5. **一次保存里夹带技术字段**（P4 表单整包提交、或手工构造请求）→ 整批拒绝、库不变，而不是改了合法字段、静默丢掉非法字段。（Task 7 `TestServiceUpdateModelRejectsTechnicalFieldAtomically`）

## 并发执行安排

用户要求互不依赖的任务在各自的 git worktree 里并行执行。依赖关系与批次：

| 批次 | 任务（可并发） | 前置 |
|---|---|---|
| 1 | **Task 1** 迁移、**Task 2** 种子加载、**Task 3** 字段路径 | 无 |
| 2 | **Task 4** 可改字段策略（需 Task 2、3）、**Task 5** 存储层（需 Task 1、2） | 批次 1 合并 |
| 3 | **Task 6** 启动合并、**Task 7** 编辑服务 | Task 4、5 合并 |
| 4 | **Task 8** 端到端测试 | Task 6、7 合并 |
| 5 | **Task 9** sidecar 接线 | Task 6 合并，**且** P1b 往 `cmd/sidecar/main.go` 加 `db.Open` / `db.Migrate` 的任务已合并 |

**文件归属（并发时只能 `git add` 自己的文件）：**

- Task 1：`server/internal/db/migrations/0003_builtin_overrides.sql`、`server/internal/db/migration_builtin_test.go`
- Task 2：`server/internal/builtin/seed.go`、`seed_test.go`、`seeddata/*.jsonl`、`scripts/sync-seed.sh`、`seed/README.md`（只在末尾追加）
- Task 3：`server/internal/builtin/path.go`、`path_test.go`
- Task 4：`server/internal/builtin/policy.go`、`policy_test.go`
- Task 5：`server/internal/builtin/store.go`、`store_test.go`、`fixture_test.go`
- Task 6：`server/internal/builtin/apply.go`、`apply_test.go`
- Task 7：`server/internal/builtin/service.go`、`service_test.go`
- Task 8：`server/internal/builtin/integration_test.go`
- Task 9：`server/cmd/sidecar/main.go`（一处插入 + 一个 import）、`server/cmd/sidecar/main_test.go`（追加一个测试）

Task 2–8 都在 `builtin` 包内但文件互不重叠；同批任务之间不互相引用对方的标识符（Task 6 与 Task 7 共用的测试辅助都在 Task 5 的 `fixture_test.go` 里）。

---

## 文件结构

```
seed/                                   真源（只追加 README 一段说明）
scripts/sync-seed.sh                    把 seed/ 里内置的 JSONL 复制到 seeddata/
server/internal/
  db/migrations/0003_builtin_overrides.sql   user_overrides 两列 + builtin_seed_state 表
  db/migration_builtin_test.go
  builtin/
    seeddata/model_configs.jsonl        seed/ 的逐字节副本（go:embed）
    seeddata/settings.jsonl
    seed.go          嵌入、解析、校验种子；Seed、SeedModel、SeedSetting、Catalog、modelParts
    path.go          字段路径语法；JSON 文档的取值、赋值、通配展开、语义比较
    policy.go        白名单与校验（spec M10）；applyEdits / overlay 纯函数；行 ↔ 文档转换
    store.go         本包全部 SQL（model_configs、settings、builtin_seed_state）
    apply.go         Apply：首次导入与升级合并（spec M9、M11）
    service.go       Service：编辑、恢复默认、列表（供 P4）
    *_test.go
server/cmd/sidecar/main.go              Task 9：Migrate 之后调用 builtin.Apply
```

---

### Task 1: 迁移——覆盖记录列与种子状态表

**Files:**
- Create: `server/internal/db/migrations/0003_builtin_overrides.sql`
- Test: `server/internal/db/migration_builtin_test.go`

**Interfaces:**
- Consumes: `dbtest.Open(t testing.TB) *sql.DB`（P1a，执行全部嵌入迁移）
- Produces:
  - `model_configs.user_overrides TEXT NOT NULL DEFAULT '[]'`、`settings.user_overrides TEXT NOT NULL DEFAULT '[]'`（均带 `typeof = 'text'` 检查）
  - 表 `builtin_seed_state(dataset TEXT PRIMARY KEY, hash TEXT NOT NULL, applied_at DATETIME, updated_at DATETIME)` 及 `updated_at` 触发器

- [ ] **Step 1: 写失败的测试**

`server/internal/db/migration_builtin_test.go`：

```go
package db_test

import (
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 已有行和新插入的行，user_overrides 都默认为 '[]'（全部字段跟随内置种子）。
func TestUserOverridesDefaultsToEmptyArray(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('text', 'n', 'm', '/v1', '{}')`); err != nil {
		t.Fatal(err)
	}
	var s, m string
	if err := conn.QueryRow(`SELECT user_overrides FROM settings WHERE "key" = 'k'`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT user_overrides FROM model_configs WHERE model_id = 'm'`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	if s != "[]" || m != "[]" {
		t.Fatalf("settings=%q model_configs=%q, want []", s, m)
	}
}

// user_overrides 是 JSON 列，只能以 TEXT 落库；传 []byte 会被存成 BLOB，必须被 CHECK 拒绝。
func TestUserOverridesRejectsBlob(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO settings ("key", value, user_overrides) VALUES ('k', 'v', ?)`, []byte(`[]`)); err == nil {
		t.Fatal("BLOB 应当被 CHECK 约束拒绝")
	}
}

func TestBuiltinSeedStateUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO builtin_seed_state (dataset, hash, updated_at) VALUES ('d', 'h1', '2000-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE builtin_seed_state SET hash = 'h2' WHERE dataset = 'd'`); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := conn.QueryRow(`SELECT updated_at FROM builtin_seed_state WHERE dataset = 'd'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Year() <= 2000 {
		t.Fatalf("updated_at 没有被触发器刷新：%v", updated)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/db/ -run 'UserOverrides|BuiltinSeedState' -v`
Expected: FAIL，报 `no such column: user_overrides` / `no such table: builtin_seed_state`。

- [ ] **Step 3: 写迁移**

`server/internal/db/migrations/0003_builtin_overrides.sql`（合并时若编号已被占用，按 Global Constraints 改名）：

```sql
-- P2a 内置数据（spec M9–M11）：
--   user_overrides：用户改过的字段路径，JSON 字符串数组，如 ["enabled","ui_schema.ratio.default"]；
--     路径语法见 internal/builtin/path.go。'[]' = 全部字段跟随内置种子。
--   builtin_seed_state：每个内置数据集最近一次导入的种子哈希；哈希没变则启动时跳过合并。
-- 只有 ADD COLUMN 和新表，不依赖 0001 之后的其他迁移；编号在合并进 main 时按顺序分配。
ALTER TABLE model_configs ADD COLUMN user_overrides TEXT NOT NULL DEFAULT '[]' CHECK (typeof(user_overrides) = 'text');
ALTER TABLE settings ADD COLUMN user_overrides TEXT NOT NULL DEFAULT '[]' CHECK (typeof(user_overrides) = 'text');

CREATE TABLE builtin_seed_state (
  dataset TEXT PRIMARY KEY,
  hash TEXT NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER trg_builtin_seed_state_updated_at AFTER UPDATE ON builtin_seed_state
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE builtin_seed_state SET updated_at = CURRENT_TIMESTAMP WHERE dataset = NEW.dataset;
END;
```

- [ ] **Step 4: 运行测试，确认通过；全量回归**

Run: `cd server && go test ./internal/db/ -run 'UserOverrides|BuiltinSeedState' -v && go test ./...`
Expected: 3 个新测试 PASS；models、settings 等 P1a 测试仍全部 PASS（新列有默认值，P1a 的 INSERT 不受影响）。

- [ ] **Step 5: 提交**

```bash
git add server/internal/db/migrations/0003_builtin_overrides.sql server/internal/db/migration_builtin_test.go
git commit -m "feat(server): 迁移——内置数据的用户覆盖记录列与种子状态表"
```

---

### Task 2: 嵌入并解析种子

**Files:**
- Create: `server/internal/builtin/seeddata/model_configs.jsonl`、`server/internal/builtin/seeddata/settings.jsonl`（由脚本复制）
- Create: `scripts/sync-seed.sh`
- Create: `server/internal/builtin/seed.go`
- Test: `server/internal/builtin/seed_test.go`
- Modify: `seed/README.md`（末尾追加一段）

**Interfaces:**
- Consumes: `billing.ModelBilling`、`billing.ValidateExtras(extras []billing.ExtraCharge) error`（P1a）
- Produces（Task 4–8 依赖）：
  - `type SeedModel struct{ ID int64; Type, Name, ModelID, BasePath string; Enabled, Hidden flexBool; SortOrder int; RequestTemplate, PollingTemplate, UISchema, BillingConfig, GenControl json.RawMessage; ImageInputFormat, ImageRequestMode, AdminNote, PriceNote, CreatedAt, UpdatedAt string }`
  - `type SeedSetting struct{ Key, Value, UpdatedAt string }`
  - `type Seed struct{ Models []SeedModel; Settings []SeedSetting; Hash string; … }`，方法 `(*Seed).Model(modelID string) (SeedModel, bool)`、`(*Seed).Setting(key string) (SeedSetting, bool)`、`(*Seed).Catalog() Catalog`
  - `type Catalog map[string]string`（model_id → type）
  - `func Load() (*Seed, error)`；`func parseSeed(modelsJSONL, settingsJSONL []byte) (*Seed, error)`
  - `const DatasetModelCenter = "model_center"`；`const seedFormatVersion = "p2a-1"`
  - `type modelParts struct{ Name string; Enabled, Hidden bool; SortOrder int; UISchema, GenControl *string }`；`func (m SeedModel) parts() modelParts`
  - 辅助：`func nullableRaw(raw json.RawMessage) json.RawMessage`、`func rawPtr(raw json.RawMessage) *string`、`type flexBool bool`、`func eachLine`、`func decodeStrict`、`func validateSeedModel`、`var secretKeyRe`、`var seedModelTypes`

- [ ] **Step 1: 建同步脚本并复制种子**

`scripts/sync-seed.sh`：

```bash
#!/usr/bin/env bash
# 把仓库根 seed/ 里由 sidecar 内置的种子文件复制到 go:embed 目录。seed/ 是唯一真源。
# P2b 增加内置文件时把文件名加进下面的列表。
set -euo pipefail
cd "$(dirname "$0")/.."
for f in model_configs.jsonl settings.jsonl; do
  cp "seed/$f" "server/internal/builtin/seeddata/$f"
done
echo "已同步内置种子到 server/internal/builtin/seeddata/"
```

Run: `chmod +x scripts/sync-seed.sh && mkdir -p server/internal/builtin/seeddata && scripts/sync-seed.sh`
Expected: 打印“已同步内置种子…”，`seeddata/` 下有两个文件。

在 `seed/README.md` 末尾追加：

```markdown

## 内置到 sidecar

`model_configs.jsonl`、`settings.jsonl` 由 sidecar 通过 `go:embed` 内置（`server/internal/builtin/seeddata/` 是它们的逐字节副本）。修改或重新导出后运行 `scripts/sync-seed.sh`；`go test ./internal/builtin/` 会检查副本与本目录一致。
```

- [ ] **Step 2: 写失败的测试**

`server/internal/builtin/seed_test.go`：

```go
package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEmbeddedSeed(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Models) != 47 || len(s.Settings) != 6 {
		t.Fatalf("models=%d settings=%d, want 47/6", len(s.Models), len(s.Settings))
	}
	byType := map[string]int{}
	for _, m := range s.Models {
		byType[m.Type]++
	}
	if byType["text"] != 8 || byType["image"] != 11 || byType["video"] != 26 || byType["audio"] != 2 {
		t.Fatalf("按类型计数 %v，want text 8 / image 11 / video 26 / audio 2（seed/README.md）", byType)
	}
	// agent_tools.source_model_id 引用生产 id，必须原样保留。
	if m, ok := s.Model("gpt-image-2"); !ok || m.ID != 9 || m.Type != "image" {
		t.Fatalf("gpt-image-2 = %+v, %v", m, ok)
	}
	for _, key := range []string{"base_domain", "canvas_light_control", "canvas_multi_angle", "canvas_postprocess_config", "canvas_script_config", "gen_image_prompt_presets"} {
		if st, ok := s.Setting(key); !ok || st.Value == "" {
			t.Fatalf("缺少种子配置 %s", key)
		}
	}
	if len(s.Hash) != 64 {
		t.Fatalf("hash=%q", s.Hash)
	}
}

// seeddata/ 必须与仓库根 seed/ 逐字节一致（seed/ 是真源）。
func TestEmbeddedSeedMatchesRepoSeed(t *testing.T) {
	for _, name := range []string{"model_configs.jsonl", "settings.jsonl"} {
		embedded, err := seedFS.ReadFile("seeddata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		repo, err := os.ReadFile(filepath.Join("..", "..", "..", "seed", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, repo) {
			t.Fatalf("server/internal/builtin/seeddata/%s 与 seed/%s 不一致：运行 scripts/sync-seed.sh", name, name)
		}
	}
}

const okModelLine = `{"id":1,"type":"image","name":"A","model_id":"a","base_path":"/v1/a","enabled":1,"hidden":0,"sort_order":0,"request_template":{"model":"a"},"polling_template":null,"ui_schema":null,"billing_config":{"items":null,"flatFee":0,"freezeEstimate":0},"gen_control":null,"image_input_format":"url","image_request_mode":"json","admin_note":"","price_note":"","created_at":"","updated_at":""}`

func TestParseSeedRejectsBadInput(t *testing.T) {
	okSetting := `{"key":"base_domain","value":"https://x.example","updated_at":""}`
	cases := map[string]struct{ models, settings, want string }{
		"重复 id":       {okModelLine + "\n" + strings.Replace(okModelLine, `"model_id":"a"`, `"model_id":"b"`, 1), okSetting, "id 1 重复"},
		"重复 model_id": {okModelLine + "\n" + strings.Replace(okModelLine, `"id":1`, `"id":2`, 1), okSetting, "model_id a 重复"},
		"未知列":         {strings.Replace(okModelLine, `"id":1`, `"id":1,"new_col":1`, 1), okSetting, "unknown field"},
		"非法布尔":        {strings.Replace(okModelLine, `"enabled":1`, `"enabled":2`, 1), okSetting, "布尔值"},
		"请求模板为空":      {strings.Replace(okModelLine, `"request_template":{"model":"a"}`, `"request_template":null`, 1), okSetting, "request_template"},
		"非法加价规则":      {strings.Replace(okModelLine, `"items":null`, `"items":null,"extras":[{"when":"bad","add":1}]`, 1), okSetting, "req:"},
		"密钥类配置":       {okModelLine, `{"key":"upstream_api_key","value":"sk-x","updated_at":""}`, "密钥"},
		"重复配置 key":    {okModelLine, okSetting + "\n" + okSetting, "key base_domain 重复"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseSeed([]byte(c.models), []byte(c.settings))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v, want 包含 %q", err, c.want)
			}
		})
	}
}

func TestSeedHashChangesWithContent(t *testing.T) {
	setting := []byte(`{"key":"base_domain","value":"https://x.example","updated_at":""}`)
	a, err := parseSeed([]byte(okModelLine), setting)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseSeed([]byte(strings.Replace(okModelLine, `"name":"A"`, `"name":"B"`, 1)), setting)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash == b.Hash {
		t.Fatal("种子内容变了，哈希必须变")
	}
}

func TestSeedModelPartsMapsNullJSONToNil(t *testing.T) {
	s, err := parseSeed([]byte(okModelLine), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Models[0].parts()
	if p.UISchema != nil || p.GenControl != nil || !p.Enabled || p.Hidden || p.Name != "A" {
		t.Fatalf("parts=%+v", p)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -v`
Expected: FAIL，编译错误 `undefined: Load` / `undefined: parseSeed` / `undefined: seedFS`。

- [ ] **Step 4: 实现**

`server/internal/builtin/seed.go`：

```go
// Package builtin 管理内置数据（spec M8–M11）：编译进 sidecar 的种子、首次启动导入与升级合并、
// 用户可改字段的白名单与校验、恢复默认。HTTP 接口和设置页属于 P4，它们只通过本包的 Service 写内置数据。
package builtin

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// seeddata/ 下是仓库根 seed/ 同名文件的逐字节副本：go:embed 不能引用包目录之外的文件。
// 真源是 seed/；改了种子运行 scripts/sync-seed.sh，忘了同步会被 TestEmbeddedSeedMatchesRepoSeed 拦住。
//
//go:embed seeddata/model_configs.jsonl seeddata/settings.jsonl
var seedFS embed.FS

// seedFormatVersion 并入种子哈希。合并算法或可改字段白名单的语义变化时递增，
// 让已安装的用户在升级后的首次启动重新合并一次（即使种子文件本身没变）。
const seedFormatVersion = "p2a-1"

// DatasetModelCenter 是本包在 builtin_seed_state 里使用的数据集名（模型中心 + 画布配置）。
// P2b 的 Skill、Agent 工具等数据集各用自己的名字。
const DatasetModelCenter = "model_center"

// SeedModel 是 model_configs.jsonl 的一行。字段名与参考仓生产库列名一致，JSON 列在文件里是对象。
// 解码时拒绝未知字段：生产库加了新列而这里没跟上，重新导出后测试会立即失败，而不是静默丢列。
type SeedModel struct {
	ID               int64           `json:"id"`
	Type             string          `json:"type"`
	Name             string          `json:"name"`
	ModelID          string          `json:"model_id"`
	BasePath         string          `json:"base_path"`
	Enabled          flexBool        `json:"enabled"`
	Hidden           flexBool        `json:"hidden"`
	SortOrder        int             `json:"sort_order"`
	RequestTemplate  json.RawMessage `json:"request_template"`
	PollingTemplate  json.RawMessage `json:"polling_template"`
	UISchema         json.RawMessage `json:"ui_schema"`
	BillingConfig    json.RawMessage `json:"billing_config"`
	GenControl       json.RawMessage `json:"gen_control"`
	ImageInputFormat string          `json:"image_input_format"`
	ImageRequestMode string          `json:"image_request_mode"`
	AdminNote        string          `json:"admin_note"`
	PriceNote        string          `json:"price_note"`
	// 生产库的时间戳只做记录，不导入：本地行的 created_at/updated_at 由本机生成。
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SeedSetting 是 settings.jsonl 的一行。Value 原样是字符串：base_domain 是纯文本，其余是 JSON 文本。
type SeedSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// Seed 是解析并校验过的一份内置种子。
type Seed struct {
	Models   []SeedModel
	Settings []SeedSetting
	// Hash 是种子文件内容（连同 seedFormatVersion）的 sha256，十六进制。
	Hash     string
	models   map[string]int
	settings map[string]int
}

// Model 按 model_id 查种子模型。
func (s *Seed) Model(modelID string) (SeedModel, bool) {
	i, ok := s.models[modelID]
	if !ok {
		return SeedModel{}, false
	}
	return s.Models[i], true
}

// Setting 按 key 查种子配置。
func (s *Seed) Setting(key string) (SeedSetting, bool) {
	i, ok := s.settings[key]
	if !ok {
		return SeedSetting{}, false
	}
	return s.Settings[i], true
}

// Catalog 是配置校验时可以引用的模型：model_id → type（text/image/video/audio）。
type Catalog map[string]string

// Catalog 返回种子里全部模型的 model_id → type。
func (s *Seed) Catalog() Catalog {
	cat := Catalog{}
	for _, m := range s.Models {
		cat[m.ModelID] = m.Type
	}
	return cat
}

// Load 解析编译进二进制的种子。
func Load() (*Seed, error) {
	m, err := seedFS.ReadFile("seeddata/model_configs.jsonl")
	if err != nil {
		return nil, err
	}
	st, err := seedFS.ReadFile("seeddata/settings.jsonl")
	if err != nil {
		return nil, err
	}
	return parseSeed(m, st)
}

// secretKeyRe 匹配密钥类配置名。种子导出时已去掉 Key（seed/README.md），这里再挡一道：
// 万一重新导出时带进了密钥，构建期的测试就会失败，密钥不会被编译进安装包。
var secretKeyRe = regexp.MustCompile(`(?i)(_key$|token|secret|password)`)

var seedModelTypes = map[string]bool{"text": true, "image": true, "video": true, "audio": true}

func parseSeed(modelsJSONL, settingsJSONL []byte) (*Seed, error) {
	s := &Seed{models: map[string]int{}, settings: map[string]int{}}
	ids := map[int64]bool{}
	err := eachLine(modelsJSONL, func(line []byte) error {
		var m SeedModel
		if err := decodeStrict(line, &m); err != nil {
			return err
		}
		if err := validateSeedModel(m); err != nil {
			return err
		}
		if ids[m.ID] {
			return fmt.Errorf("id %d 重复", m.ID)
		}
		if _, dup := s.models[m.ModelID]; dup {
			return fmt.Errorf("model_id %s 重复", m.ModelID)
		}
		ids[m.ID] = true
		s.models[m.ModelID] = len(s.Models)
		s.Models = append(s.Models, m)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("model_configs.jsonl %w", err)
	}
	err = eachLine(settingsJSONL, func(line []byte) error {
		var st SeedSetting
		if err := decodeStrict(line, &st); err != nil {
			return err
		}
		if st.Key == "" {
			return errors.New("key 为空")
		}
		if secretKeyRe.MatchString(st.Key) {
			return fmt.Errorf("种子不得包含密钥类配置 %s", st.Key)
		}
		if _, dup := s.settings[st.Key]; dup {
			return fmt.Errorf("key %s 重复", st.Key)
		}
		s.settings[st.Key] = len(s.Settings)
		s.Settings = append(s.Settings, st)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("settings.jsonl %w", err)
	}
	sum := sha256.New()
	io.WriteString(sum, seedFormatVersion+"\n")
	sum.Write(modelsJSONL)
	io.WriteString(sum, "\n\x00\n")
	sum.Write(settingsJSONL)
	s.Hash = hex.EncodeToString(sum.Sum(nil))
	return s, nil
}

func validateSeedModel(m SeedModel) error {
	if m.ID <= 0 {
		return fmt.Errorf("模型 %q：id 必须是正整数", m.ModelID)
	}
	if m.ModelID == "" || m.Name == "" || m.BasePath == "" {
		return fmt.Errorf("模型 %d：model_id、name、base_path 不能为空", m.ID)
	}
	if !seedModelTypes[m.Type] {
		return fmt.Errorf("模型 %s：未知类型 %q", m.ModelID, m.Type)
	}
	if t := bytes.TrimSpace(m.RequestTemplate); len(t) == 0 || t[0] != '{' {
		return fmt.Errorf("模型 %s：request_template 必须是 JSON 对象", m.ModelID)
	}
	switch m.ImageInputFormat {
	case "", "url", "data_uri", "base64":
	default:
		return fmt.Errorf("模型 %s：未知 image_input_format %q", m.ModelID, m.ImageInputFormat)
	}
	switch m.ImageRequestMode {
	case "", "json", "openai_image_edits":
	default:
		return fmt.Errorf("模型 %s：未知 image_request_mode %q", m.ModelID, m.ImageRequestMode)
	}
	// 与参考仓后台保存模型时的校验一致（admin_model_handlers.go 的 validateBillingExtras）：
	// 配错的加价规则在运行期不报错、静默失效，所以必须在入库前挡住。
	if raw := nullableRaw(m.BillingConfig); raw != nil {
		var mb billing.ModelBilling
		if err := json.Unmarshal(raw, &mb); err != nil {
			return fmt.Errorf("模型 %s：billing_config 解析失败: %w", m.ModelID, err)
		}
		if err := billing.ValidateExtras(mb.Extras); err != nil {
			return fmt.Errorf("模型 %s：%w", m.ModelID, err)
		}
	}
	return nil
}

// eachLine 逐行回调非空行。不用 bufio.Scanner：它默认单行上限 64KB，P2b 的 skills 单行更长。
func eachLine(data []byte, fn func(line []byte) error) error {
	r := bufio.NewReader(bytes.NewReader(data))
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if e := fn(t); e != nil {
				return fmt.Errorf("第 %d 行: %w", n, e)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func decodeStrict(line []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("一行只能有一个 JSON 对象")
	}
	return nil
}

// flexBool 接受 0/1/true/false：生产库是 TINYINT(1)，导出成 0/1。
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "1", "true":
		*b = true
	case "0", "false":
		*b = false
	default:
		return fmt.Errorf("布尔值只接受 0/1/true/false，得到 %s", data)
	}
	return nil
}

// nullableRaw 把缺失或 null 的 JSON 列统一成 nil（落库为 NULL）。
func nullableRaw(raw json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil
	}
	return t
}

func rawPtr(raw json.RawMessage) *string {
	r := nullableRaw(raw)
	if r == nil {
		return nil
	}
	s := string(r)
	return &s
}

// modelParts 是模型行里用户可以改的那部分列（spec M10）。合并（M11）和编辑都以它为单位，
// 其余列（请求/轮询模板、base_path、图片协议、计费等）永远取种子值。
type modelParts struct {
	Name       string
	Enabled    bool
	Hidden     bool
	SortOrder  int
	UISchema   *string // nil = NULL
	GenControl *string // nil = NULL
}

func (m SeedModel) parts() modelParts {
	return modelParts{
		Name:       m.Name,
		Enabled:    bool(m.Enabled),
		Hidden:     bool(m.Hidden),
		SortOrder:  m.SortOrder,
		UISchema:   rawPtr(m.UISchema),
		GenControl: rawPtr(m.GenControl),
	}
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -v`
Expected: `TestLoadEmbeddedSeed`、`TestEmbeddedSeedMatchesRepoSeed`、`TestParseSeedRejectsBadInput`（8 个子测试）、`TestSeedHashChangesWithContent`、`TestSeedModelPartsMapsNullJSONToNil` 全部 PASS。真实种子的 47 个模型全部通过 `validateSeedModel`（含 4 个带 extras 的模型的加价规则校验）。

- [ ] **Step 6: 提交**

```bash
git add scripts/sync-seed.sh seed/README.md server/internal/builtin/seed.go server/internal/builtin/seed_test.go server/internal/builtin/seeddata
git commit -m "feat(server): 内置种子 go:embed 嵌入与解析校验"
```

---

### Task 3: 字段路径与 JSON 文档操作

**Files:**
- Create: `server/internal/builtin/path.go`
- Test: `server/internal/builtin/path_test.go`

**Interfaces:**
- Consumes: 无（只用标准库）
- Produces（Task 4–8 依赖）：
  - `type segment struct{ Name, Key string; Keyed bool }`；`type Path []segment`（空切片 = `$`）
  - `func ParsePath(s string) (Path, error)`；`func mustParsePath(s string) Path`；`func (p Path) String() string`；`func (pattern Path) Match(p Path) bool`
  - `func getPath(doc any, p Path) (any, bool)`；`func setPath(doc any, p Path, v any) (any, error)`；`func expandPattern(doc any, pattern Path) []Path`；`func findKeyed(v any, key string) (map[string]any, bool)`
  - `func decodeJSON(raw []byte) (any, error)`（数字为 `json.Number`）；`func encodeJSON(v any) (string, error)`（不转义 `<>&`）；`func deepCopy(v any) (any, error)`；`func jsonEqual(a, b any) bool`；`func normalizeNumbers(v any) any`；`func sortedKeys[V any](m map[string]V) []string`
  - 测试辅助（`path_test.go`，后续任务的测试会用）：`func mustDecode(t *testing.T, s string) any`

- [ ] **Step 1: 写失败的测试**

`server/internal/builtin/path_test.go`：

```go
package builtin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustDecode(t *testing.T, s string) any {
	t.Helper()
	v, err := decodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParsePathRoundTrip(t *testing.T) {
	for _, s := range []string{"$", "name", "ui_schema.ratio.default", "presets[720_panorama].prompt", "presets[*].value.params[*].options[*].prompt", "gen_control.modules.canvas.maxRetries"} {
		p, err := ParsePath(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if p.String() != s {
			t.Fatalf("round trip %q -> %q", s, p.String())
		}
	}
}

func TestParsePathRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "a..b", "a.b c", "presets[x", "presets[].prompt", "a[b]c]", "$.a", "a/b"} {
		if _, err := ParsePath(s); err == nil {
			t.Fatalf("%q 应当解析失败", s)
		}
	}
}

func TestPatternMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"ui_schema.*.default", "ui_schema.ratio.default", true},
		{"ui_schema.*.default", "ui_schema.ratio.options", false},
		{"presets[*].prompt", "presets[rembrandt].prompt", true},
		{"presets[*].prompt", "presets.prompt", false},
		{"presets[*].prompt", "presets[rembrandt].name", false},
		{"$", "$", true},
		{"models", "models.x", false},
	}
	for _, c := range cases {
		if got := mustParsePath(c.pattern).Match(mustParsePath(c.path)); got != c.want {
			t.Fatalf("%s ~ %s = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestGetPathKeyedAndMissing(t *testing.T) {
	doc := mustDecode(t, `{"presets":[{"key":"a","prompt":"pa"},{"key":"b","prompt":"pb"}],"x":{"y":1}}`)
	if v, ok := getPath(doc, mustParsePath("presets[b].prompt")); !ok || v != "pb" {
		t.Fatalf("got %v %v", v, ok)
	}
	if _, ok := getPath(doc, mustParsePath("presets[c].prompt")); ok {
		t.Fatal("不存在的 key 应当 ok=false")
	}
	if _, ok := getPath(doc, mustParsePath("x.y.z")); ok {
		t.Fatal("穿过非对象应当 ok=false")
	}
	if v, ok := getPath(doc, Path{}); !ok || !reflect.DeepEqual(v, doc) {
		t.Fatal("$ 应当返回整个文档")
	}
}

func TestSetPathAutovivifiesObjectsButNotArrayElements(t *testing.T) {
	var doc any = map[string]any{"gen_control": nil}
	doc, err := setPath(doc, mustParsePath("gen_control.modules.canvas.maxRetries"), json.Number("2"))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := getPath(doc, mustParsePath("gen_control.modules.canvas.maxRetries")); v != json.Number("2") {
		t.Fatalf("got %v", v)
	}
	presets := mustDecode(t, `{"presets":[{"key":"a","prompt":"pa"}]}`)
	if _, err := setPath(presets, mustParsePath("presets[new].prompt"), "x"); err == nil {
		t.Fatal("不能借 [key] 新增数组元素")
	}
	if _, err := setPath(presets, mustParsePath("presets[a]"), "x"); err == nil {
		t.Fatal("不能整体替换数组元素")
	}
	root, err := setPath("old", Path{}, "new")
	if err != nil || root != "new" {
		t.Fatalf("$ 应当替换整个值: %v %v", root, err)
	}
}

func TestExpandPattern(t *testing.T) {
	doc := mustDecode(t, `{"ui_schema":{"ratio":{"default":"1:1"},"duration":{"default":5}},"presets":[{"key":"a","value":{"params":[{"key":"skin","options":[{"key":"n","prompt":""},{"key":"r","prompt":"x"}]}]}},{"key":"b","value":{}}]}`)
	got := []string{}
	for _, p := range expandPattern(doc, mustParsePath("ui_schema.*.default")) {
		got = append(got, p.String())
	}
	if !reflect.DeepEqual(got, []string{"ui_schema.duration.default", "ui_schema.ratio.default"}) {
		t.Fatalf("got %v", got)
	}
	got = got[:0]
	for _, p := range expandPattern(doc, mustParsePath("presets[*].value.params[*].options[*].prompt")) {
		got = append(got, p.String())
	}
	if !reflect.DeepEqual(got, []string{"presets[a].value.params[skin].options[n].prompt", "presets[a].value.params[skin].options[r].prompt"}) {
		t.Fatalf("got %v", got)
	}
}

func TestJSONEqualAndEncoding(t *testing.T) {
	if !jsonEqual(mustDecode(t, `{"a":10,"b":[1,"x"]}`), mustDecode(t, `{"b":[1.0,"x"],"a":10.0}`)) {
		t.Fatal("数值相等、键顺序不同应当相等")
	}
	if jsonEqual(mustDecode(t, `"10"`), mustDecode(t, `10`)) {
		t.Fatal("字符串与数字不相等")
	}
	s, err := encodeJSON(mustDecode(t, `{"p":"a<b>&c","n":1.50}`))
	if err != nil || s != `{"n":1.50,"p":"a<b>&c"}` {
		t.Fatalf("encode=%s %v", s, err)
	}
	if _, err := decodeJSON([]byte(`{"a":1} x`)); err == nil {
		t.Fatal("多余内容应当报错")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -run 'Path|Pattern|JSONEqual' -v`
Expected: FAIL，编译错误 `undefined: ParsePath` 等。

- [ ] **Step 3: 实现**

`server/internal/builtin/path.go`：

```go
package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// 字段路径：user_overrides 里存的、编辑接口收的、白名单里写的都是它的规范字符串。
//
//	path := "$" | seg ("." seg)*
//	seg  := name | name "[" key "]"
//
// name、key 只含 [A-Za-z0-9_-]；白名单模式里可以写 "*" 通配。
// name 取对象字段；name[key] 取数组字段里 "key" 属性等于 key 的元素——预设按 key 寻址而不是下标，
// 新版种子调整预设顺序或插入新预设时，用户对某条预设的修改不会错位。
// "$" 表示整个值（base_domain 这种纯文本配置）。

type segment struct {
	Name  string
	Key   string
	Keyed bool
}

// Path 是解析后的字段路径；空切片表示 "$"。
type Path []segment

var identRe = regexp.MustCompile(`^(\*|[A-Za-z0-9_-]+)$`)

// ParsePath 解析字段路径字符串。
func ParsePath(s string) (Path, error) {
	if s == "$" {
		return Path{}, nil
	}
	if s == "" {
		return nil, errors.New("字段路径为空")
	}
	parts := strings.Split(s, ".")
	out := make(Path, 0, len(parts))
	for _, part := range parts {
		seg := segment{Name: part}
		if i := strings.IndexByte(part, '['); i >= 0 {
			if !strings.HasSuffix(part, "]") {
				return nil, fmt.Errorf("字段路径 %q：%q 缺少 ]", s, part)
			}
			seg = segment{Name: part[:i], Key: part[i+1 : len(part)-1], Keyed: true}
			if !identRe.MatchString(seg.Key) {
				return nil, fmt.Errorf("字段路径 %q：%q 的 key 不合法", s, part)
			}
		}
		if !identRe.MatchString(seg.Name) {
			return nil, fmt.Errorf("字段路径 %q：%q 不合法", s, part)
		}
		out = append(out, seg)
	}
	return out, nil
}

func mustParsePath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

// String 返回规范字符串，与 ParsePath 互逆。
func (p Path) String() string {
	if len(p) == 0 {
		return "$"
	}
	var b strings.Builder
	for i, s := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Name)
		if s.Keyed {
			b.WriteString("[" + s.Key + "]")
		}
	}
	return b.String()
}

// Match 判断具体路径 p 是否命中模式 pattern：段数相同，逐段比较，"*" 匹配任意 name 或 key。
func (pattern Path) Match(p Path) bool {
	if len(pattern) != len(p) {
		return false
	}
	for i := range pattern {
		a, b := pattern[i], p[i]
		if a.Keyed != b.Keyed {
			return false
		}
		if a.Name != "*" && a.Name != b.Name {
			return false
		}
		if a.Keyed && a.Key != "*" && a.Key != b.Key {
			return false
		}
	}
	return true
}

// findKeyed 在数组 v 里找 "key" 属性等于 key 的对象元素。
func findKeyed(v any, key string) (map[string]any, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			if k, _ := m["key"].(string); k == key {
				return m, true
			}
		}
	}
	return nil, false
}

// getPath 取 doc 上路径 p 处的值；任一段不存在返回 ok=false。
func getPath(doc any, p Path) (any, bool) {
	cur := doc
	for _, s := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[s.Name]
		if !ok {
			return nil, false
		}
		if s.Keyed {
			elem, ok := findKeyed(v, s.Key)
			if !ok {
				return nil, false
			}
			v = elem
		}
		cur = v
	}
	return cur, true
}

// setPath 把 doc 上路径 p 处的值设为 v，返回新的根（p 为 "$" 时根被替换为 v）。
// 中途缺失或为 null 的对象字段自动建成空对象（gen_control 为 NULL 的模型也能设并发）；
// 带 [key] 的段必须命中已有元素——用户不能借此新增预设。会原地修改 doc，调用方先 deepCopy。
func setPath(doc any, p Path, v any) (any, error) {
	if len(p) == 0 {
		return v, nil
	}
	if doc == nil {
		doc = map[string]any{}
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 的上一级不是对象", p)
	}
	s := p[0]
	if s.Keyed {
		elem, ok := findKeyed(m[s.Name], s.Key)
		if !ok {
			return nil, fmt.Errorf("找不到 %s[%s]", s.Name, s.Key)
		}
		if len(p) == 1 {
			return nil, fmt.Errorf("不能整体替换 %s[%s]", s.Name, s.Key)
		}
		if _, err := setPath(elem, p[1:], v); err != nil {
			return nil, err
		}
		return m, nil
	}
	if len(p) == 1 {
		m[s.Name] = v
		return m, nil
	}
	child, err := setPath(m[s.Name], p[1:], v)
	if err != nil {
		return nil, err
	}
	m[s.Name] = child
	return m, nil
}

// expandPattern 列出 doc 上实际存在、且命中模式 pattern 的全部具体路径（顺序与文档顺序一致）。
func expandPattern(doc any, pattern Path) []Path {
	var out []Path
	var walk func(cur any, i int, prefix Path)
	walk = func(cur any, i int, prefix Path) {
		if i == len(pattern) {
			out = append(out, append(Path{}, prefix...))
			return
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return
		}
		seg := pattern[i]
		var names []string
		if seg.Name == "*" {
			names = sortedKeys(m)
		} else if _, ok := m[seg.Name]; ok {
			names = []string{seg.Name}
		}
		for _, name := range names {
			v := m[name]
			if !seg.Keyed {
				walk(v, i+1, append(prefix, segment{Name: name}))
				continue
			}
			arr, _ := v.([]any)
			for _, e := range arr {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				k, _ := em["key"].(string)
				if k == "" || (seg.Key != "*" && seg.Key != k) {
					continue
				}
				walk(em, i+1, append(prefix, segment{Name: name, Key: k, Keyed: true}))
			}
		}
	}
	walk(doc, 0, Path{})
	return out
}

// decodeJSON 把一段 JSON 解成通用值；数字保持 json.Number，重新编码时不丢精度、不改写法。
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON 后面有多余内容")
	}
	return v, nil
}

// encodeJSON 编码通用值；不转义 <>&（提示词里常见），不带结尾换行。
func encodeJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func deepCopy(v any) (any, error) {
	s, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return decodeJSON([]byte(s))
}

// jsonEqual 按 JSON 语义比较：数字按数值比（10 与 10.0 相等），对象不看键顺序。
func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(normalizeNumbers(a), normalizeNumbers(b))
}

func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case int:
		return float64(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalizeNumbers(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalizeNumbers(e)
		}
		return out
	default:
		return v
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -run 'Path|Pattern|JSONEqual' -v`
Expected: 7 个测试全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/builtin/path.go server/internal/builtin/path_test.go
git commit -m "feat(server): 内置数据字段路径语法与 JSON 文档操作"
```

---

### Task 4: 可改字段策略、编辑与合并的纯函数

**Files:**
- Create: `server/internal/builtin/policy.go`
- Test: `server/internal/builtin/policy_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Seed`、`SeedModel.parts()`、`modelParts`、`Catalog`、`Load()`；Task 3 的 `Path`、`ParsePath`、`mustParsePath`、`getPath`、`setPath`、`expandPattern`、`decodeJSON`、`encodeJSON`、`deepCopy`、`jsonEqual`、`sortedKeys`，测试辅助 `mustDecode`
- Produces（Task 5–8 依赖）：
  - 常量 `SettingBaseDomain`、`SettingUpstreamAPIKey`、`SettingCanvasLightControl`、`SettingCanvasMultiAngle`、`SettingCanvasPostprocess`、`SettingCanvasScript`、`SettingGenImagePresets`
  - `type ValidationError struct{ Path, Msg string }`（`Error()` 返回 `Path + "：" + Msg`）
  - `type valueKind int`（`kindJSON`、`kindText`）；`type policy struct{ kind valueKind; rules []rule }`，方法 `find(p Path) (rule, bool)`、`Patterns() []string`
  - `var modelPolicy policy`；`var settingPolicies map[string]policy`
  - `func ModelEditableFields() []string`；`func SettingEditableFields(key string) ([]string, bool)`
  - `func applyEdits(pl policy, cur, seedDoc any, overrides []string, edits map[string]json.RawMessage, cat Catalog) (any, []string, error)`
  - `func overlay(pl policy, base, user any, overrides []string, cat Catalog) (doc any, kept []string, dropped []string)`
  - `func modelDoc(mp modelParts) (any, error)`；`func partsFromDoc(doc any, seed modelParts, overrides []string) (modelParts, error)`
  - `func settingDoc(kind valueKind, value string) (any, error)`；`func settingValueFromDoc(kind valueKind, seedValue string, doc any, overrides []string) (string, error)`
  - 测试辅助（`policy_test.go`，Task 7、8 的测试会用）：`func edits(kv ...string) map[string]json.RawMessage`

- [ ] **Step 1: 写失败的测试**

`server/internal/builtin/policy_test.go`：

```go
package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 每个内置种子值都必须通过它自己的白名单校验，否则用户“原样保存”就会被拒绝；
// 同时保证每个种子配置都有 policy、JSON 配置确实是合法 JSON。
func TestSeedValuesPassTheirOwnRules(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cat := s.Catalog()
	for _, m := range s.Models {
		doc, err := modelDoc(m.parts())
		if err != nil {
			t.Fatalf("%s: %v", m.ModelID, err)
		}
		for _, r := range modelPolicy.rules {
			for _, p := range expandPattern(doc, r.pattern) {
				v, _ := getPath(doc, p)
				if err := r.check(doc, p, v, cat); err != nil {
					t.Errorf("模型 %s 的 %s 不通过校验：%v", m.ModelID, p, err)
				}
			}
		}
	}
	for _, st := range s.Settings {
		pl, ok := settingPolicies[st.Key]
		if !ok {
			t.Errorf("种子配置 %s 没有 policy", st.Key)
			continue
		}
		doc, err := settingDoc(pl.kind, st.Value)
		if err != nil {
			t.Errorf("%s: %v", st.Key, err)
			continue
		}
		matched := 0
		for _, r := range pl.rules {
			for _, p := range expandPattern(doc, r.pattern) {
				matched++
				v, _ := getPath(doc, p)
				if err := r.check(doc, p, v, cat); err != nil {
					t.Errorf("配置 %s 的 %s 不通过校验：%v", st.Key, p, err)
				}
			}
		}
		if matched == 0 {
			t.Errorf("配置 %s 的白名单一个字段都没命中，模式写错了？", st.Key)
		}
	}
}

// 白名单模式不能互为前缀：否则同一份值会被两条覆盖记录同时管理，合并结果取决于顺序。
func TestPolicyPatternsDoNotNest(t *testing.T) {
	all := map[string]policy{"<model>": modelPolicy}
	for k, v := range settingPolicies {
		all[k] = v
	}
	for name, pl := range all {
		for i, a := range pl.rules {
			for j, b := range pl.rules {
				if i != j && len(a.pattern) < len(b.pattern) && a.pattern.Match(b.pattern[:len(a.pattern)]) {
					t.Errorf("%s：%s 是 %s 的前缀", name, a.pattern, b.pattern)
				}
			}
		}
	}
}

func modelTestDoc(t *testing.T) any {
	t.Helper()
	ui := `{"ratio":{"label":"比例","default":"1:1","options":[{"label":"1:1","value":"1:1"},{"label":"16:9","value":"16:9"}]},"duration":{"default":5,"options":[{"value":5},{"value":10}]}}`
	doc, err := modelDoc(modelParts{Name: "A", Enabled: true, UISchema: &ui})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func edits(kv ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}
	return out
}

func TestApplyEditsModelRules(t *testing.T) {
	doc := modelTestDoc(t)
	cases := []struct {
		name  string
		edits map[string]json.RawMessage
		bad   string // 期望报错的路径；空 = 期望成功
	}{
		{"改名", edits("name", `"新名字"`), ""},
		{"空名", edits("name", `"  "`), "name"},
		{"启用必须是布尔", edits("enabled", `1`), "enabled"},
		{"默认值在选项内", edits("ui_schema.ratio.default", `"16:9"`), ""},
		{"数字默认值按数值比较", edits("ui_schema.duration.default", `10.0`), ""},
		{"默认值不在选项内", edits("ui_schema.ratio.default", `"4:3"`), "ui_schema.ratio.default"},
		{"不存在的参数", edits("ui_schema.size.default", `"x"`), "ui_schema.size.default"},
		{"并发上限", edits("gen_control.concurrency", `201`), "gen_control.concurrency"},
		{"并发必须是整数", edits("gen_control.concurrency", `1.5`), "gen_control.concurrency"},
		{"gen_control 为 NULL 也能设重试", edits("gen_control.modules.canvas.maxRetries", `3`), ""},
		{"drama 模块不开放", edits("gen_control.modules.drama.maxRetries", `3`), "gen_control.modules.drama.maxRetries"},
		{"技术字段只读", edits("base_path", `"/x"`), "base_path"},
		{"请求模板只读", edits("request_template", `{}`), "request_template"},
		{"不能整体替换 uiSchema", edits("ui_schema", `{}`), "ui_schema"},
		{"非法 JSON", edits("name", `{`), "name"},
		{"非法路径", edits("a..b", `1`), "a..b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := applyEdits(modelPolicy, doc, doc, nil, c.edits, Catalog{})
			var ve *ValidationError
			if c.bad == "" {
				if err != nil {
					t.Fatalf("意外报错：%v", err)
				}
				return
			}
			if !errors.As(err, &ve) || ve.Path != c.bad {
				t.Fatalf("err=%v, want ValidationError on %s", err, c.bad)
			}
		})
	}
}

func TestApplyEditsTracksOverrides(t *testing.T) {
	seedDoc := modelTestDoc(t)
	doc, ov, err := applyEdits(modelPolicy, seedDoc, seedDoc, nil, edits("name", `"B"`, "ui_schema.ratio.default", `"16:9"`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ov, []string{"name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", ov)
	}
	// 改回与种子相同的值：覆盖记录移除，以后跟随种子。
	_, ov, err = applyEdits(modelPolicy, doc, seedDoc, ov, edits("name", `"A"`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ov, []string{"ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", ov)
	}
}

// 一批编辑里只要有一项不合法，整批作废（调用方据此不写库）。
func TestApplyEditsIsAllOrNothing(t *testing.T) {
	doc := modelTestDoc(t)
	out, ov, err := applyEdits(modelPolicy, doc, doc, nil, edits("name", `"B"`, "base_path", `"/x"`), Catalog{})
	if err == nil || out != nil || ov != nil {
		t.Fatalf("out=%v ov=%v err=%v", out, ov, err)
	}
	if n, _ := getPath(doc, mustParsePath("name")); n != "A" {
		t.Fatal("applyEdits 不得修改传入的文档")
	}
}

func TestSettingRules(t *testing.T) {
	cat := Catalog{"img": "image", "txt": "text", "vid": "video"}
	light := mustDecode(t, `{"models":["img"],"template":"打光：{light}","presets":[{"key":"p1","name":"P1","direction":"","prompt":"a"}]}`)
	presets := mustDecode(t, `{"models":["img"],"presets":[{"key":"k","value":{"name":"N","prompt":"x","params":[{"key":"skin","name":"皮肤","options":[{"key":"n","name":"自然","prompt":""}]}]}}]}`)
	post := mustDecode(t, `{"subtitleRemovalModels":[{"modelId":"vid","label":"标准","subtitle":"","isTimeLimited":false}],"upscaleModel":"vid"}`)
	script := mustDecode(t, `{"modelCandidates":[{"modelId":"txt","displayName":"T"}],"styles":[],"concurrency":5,"maxRetries":3}`)
	cases := []struct {
		name, key string
		doc       any
		edits     map[string]json.RawMessage
		bad       string
	}{
		{"预设提示词", SettingCanvasLightControl, light, edits("presets[p1].prompt", `"新的"`), ""},
		{"预设结构字段只读", SettingCanvasLightControl, light, edits("presets[p1].direction", `"top"`), "presets[p1].direction"},
		{"不能新增预设", SettingCanvasLightControl, light, edits("presets[p9].prompt", `"x"`), "presets[p9].prompt"},
		{"模板缺占位符", SettingCanvasLightControl, light, edits("template", `"没有占位符"`), "template"},
		{"空模板=用缺省", SettingCanvasLightControl, light, edits("template", `""`), ""},
		{"候选模型必须存在", SettingCanvasLightControl, light, edits("models", `["nope"]`), "models"},
		{"候选模型类型必须对", SettingCanvasLightControl, light, edits("models", `["txt"]`), "models"},
		{"候选模型不能重复", SettingCanvasLightControl, light, edits("models", `["img","img"]`), "models"},
		{"候选模型可清空", SettingCanvasLightControl, light, edits("models", `[]`), ""},
		{"绘图预设正文不能为空", SettingGenImagePresets, presets, edits("presets[k].value.prompt", `""`), "presets[k].value.prompt"},
		{"档位片段可为空", SettingGenImagePresets, presets, edits("presets[k].value.params[skin].options[n].prompt", `""`), ""},
		{"绘图预设名只读", SettingGenImagePresets, presets, edits("presets[k].value.name", `"x"`), "presets[k].value.name"},
		{"去字幕列表", SettingCanvasPostprocess, post, edits("subtitleRemovalModels", `[{"modelId":"vid","label":"L"}]`), ""},
		{"去字幕列表不认识的字段", SettingCanvasPostprocess, post, edits("subtitleRemovalModels", `[{"modelId":"vid","label":"L","x":1}]`), "subtitleRemovalModels"},
		{"转高清模型类型", SettingCanvasPostprocess, post, edits("upscaleModel", `"img"`), "upscaleModel"},
		{"脚本候选", SettingCanvasScript, script, edits("modelCandidates", `[{"modelId":"txt","displayName":"GPT"}]`), ""},
		{"脚本风格只读", SettingCanvasScript, script, edits("styles", `[]`), "styles"},
		{"脚本并发下限", SettingCanvasScript, script, edits("concurrency", `0`), "concurrency"},
		{"上游地址", SettingBaseDomain, "http://a.example:3000", edits("$", `"https://gw.example.com"`), ""},
		{"上游地址不能带结尾 /", SettingBaseDomain, "http://a.example:3000", edits("$", `"https://gw.example.com/"`), "$"},
		{"上游地址协议", SettingBaseDomain, "http://a.example:3000", edits("$", `"ftp://gw.example.com"`), "$"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pl := settingPolicies[c.key]
			_, _, err := applyEdits(pl, c.doc, c.doc, nil, c.edits, cat)
			var ve *ValidationError
			if c.bad == "" {
				if err != nil {
					t.Fatalf("意外报错：%v", err)
				}
				return
			}
			if !errors.As(err, &ve) || ve.Path != c.bad {
				t.Fatalf("err=%v, want ValidationError on %s", err, c.bad)
			}
		})
	}
}

func TestOverlayKeepsValidAndDropsInvalid(t *testing.T) {
	cat := Catalog{"img": "image"}
	base := mustDecode(t, `{"models":["img"],"template":"新模板 {light}","presets":[{"key":"p1","prompt":"新 p1"},{"key":"p3","prompt":"p3"}]}`)
	user := mustDecode(t, `{"models":["gone"],"template":"旧 {light}","presets":[{"key":"p1","prompt":"我的 p1"},{"key":"p2","prompt":"我的 p2"}]}`)
	doc, kept, dropped := overlay(settingPolicies[SettingCanvasLightControl], base, user, []string{"models", "presets[p1].prompt", "presets[p2].prompt"}, cat)
	if !reflect.DeepEqual(kept, []string{"presets[p1].prompt"}) || !reflect.DeepEqual(dropped, []string{"models", "presets[p2].prompt"}) {
		t.Fatalf("kept=%v dropped=%v", kept, dropped)
	}
	got, _ := encodeJSON(doc)
	want := `{"models":["img"],"presets":[{"key":"p1","prompt":"我的 p1"},{"key":"p3","prompt":"p3"}],"template":"新模板 {light}"}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestPartsFromDocKeepsSeedTextWhenNotOverridden(t *testing.T) {
	ui := `{"ratio": {"default": "1:1", "options": [{"value": "1:1"}, {"value": "16:9"}]}}`
	gc := `{"concurrency": 5}`
	seed := modelParts{Name: "A", Enabled: true, SortOrder: 3, UISchema: &ui, GenControl: &gc}
	doc, err := modelDoc(seed)
	if err != nil {
		t.Fatal(err)
	}
	doc, ov, err := applyEdits(modelPolicy, doc, doc, nil, edits("hidden", `true`, "gen_control.concurrency", `9`), Catalog{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := partsFromDoc(doc, seed, ov)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Hidden || p.Name != "A" || p.SortOrder != 3 {
		t.Fatalf("parts=%+v", p)
	}
	if *p.UISchema != ui {
		t.Fatalf("未覆盖的 ui_schema 必须保留种子原文，got %s", *p.UISchema)
	}
	if !strings.Contains(*p.GenControl, `"concurrency":9`) {
		t.Fatalf("gen_control=%s", *p.GenControl)
	}
}

func TestSettingValueFromDoc(t *testing.T) {
	seed := `{"models": ["img"], "template": "t {light}"}`
	if v, _ := settingValueFromDoc(kindJSON, seed, nil, nil); v != seed {
		t.Fatal("没有覆盖项时必须原样返回种子值")
	}
	v, err := settingValueFromDoc(kindText, "http://a", "https://b", []string{"$"})
	if err != nil || v != "https://b" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -run 'Policy|Rules|ApplyEdits|Overlay|PartsFromDoc|SettingValueFromDoc' -v`
Expected: FAIL，编译错误 `undefined: modelPolicy` 等。

- [ ] **Step 3: 实现**

`server/internal/builtin/policy.go`：

```go
package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 可改字段白名单（spec M10）。模型和每个内置配置各有一份 policy：只有命中某条规则模式的路径
// 才允许修改，且新值必须通过该规则的校验。白名单之外的字段（请求/轮询模板、base_path、图片协议、
// 计费、预设的结构字段……）对用户只读，升级时永远跟随新版种子。

// 内置配置的 key。
const (
	SettingBaseDomain         = "base_domain"
	SettingUpstreamAPIKey     = "upstream_api_key" // 不在种子里，用户自己填（spec M3）；本包的合并永远不碰它
	SettingCanvasLightControl = "canvas_light_control"
	SettingCanvasMultiAngle   = "canvas_multi_angle"
	SettingCanvasPostprocess  = "canvas_postprocess_config"
	SettingCanvasScript       = "canvas_script_config"
	SettingGenImagePresets    = "gen_image_prompt_presets"
)

type valueKind int

const (
	kindJSON valueKind = iota // settings.value 是 JSON 文本
	kindText                  // settings.value 是纯文本（base_domain）
)

// ValidationError 是一次编辑里第一个不合法的字段。P4 的 HTTP 层把它映射成 400。
type ValidationError struct {
	Path string
	Msg  string
}

func (e *ValidationError) Error() string { return e.Path + "：" + e.Msg }

// checkFunc 校验把路径 p 设为 v 是否合法。doc 是写入前的整份文档，用来查同级结构（如 uiSchema 的 options）。
type checkFunc func(doc any, p Path, v any, cat Catalog) error

type rule struct {
	pattern Path
	check   checkFunc
}

type policy struct {
	kind  valueKind
	rules []rule
}

type ruleDef struct {
	pattern string
	check   checkFunc
}

func newPolicy(kind valueKind, defs ...ruleDef) policy {
	pl := policy{kind: kind}
	for _, d := range defs {
		pl.rules = append(pl.rules, rule{pattern: mustParsePath(d.pattern), check: d.check})
	}
	return pl
}

func (pl policy) find(p Path) (rule, bool) {
	for _, r := range pl.rules {
		if r.pattern.Match(p) {
			return r, true
		}
	}
	return rule{}, false
}

// Patterns 返回白名单模式字符串，供 P4 的设置页决定哪些控件可编辑。
func (pl policy) Patterns() []string {
	out := make([]string, len(pl.rules))
	for i, r := range pl.rules {
		out[i] = r.pattern.String()
	}
	return out
}

var modelPolicy = newPolicy(kindJSON,
	ruleDef{"name", checkName},
	ruleDef{"enabled", checkBool},
	ruleDef{"hidden", checkBool},
	ruleDef{"sort_order", checkInt(-1_000_000, 1_000_000)},
	ruleDef{"ui_schema.*.default", checkUIDefault},
	// 0 = 用默认并发 200（models.DefaultGenConcurrency）。drama 模块桌面端不用，不开放。
	ruleDef{"gen_control.concurrency", checkInt(0, 200)},
	ruleDef{"gen_control.modules.canvas.maxRetries", checkInt(0, 10)},
	ruleDef{"gen_control.modules.canvas.retryIntervalSec", checkInt(0, 600)},
)

var settingPolicies = map[string]policy{
	SettingBaseDomain: newPolicy(kindText, ruleDef{"$", checkBaseDomain}),
	SettingCanvasLightControl: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image")},
		ruleDef{"template", checkTemplate("{light}")},
		ruleDef{"presets[*].prompt", checkPrompt(true, 4000)},
	),
	SettingCanvasMultiAngle: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image")},
		ruleDef{"template", checkTemplate("{angle}")},
		ruleDef{"presets[*].prompt", checkPrompt(true, 4000)},
	),
	SettingGenImagePresets: newPolicy(kindJSON,
		ruleDef{"models", checkModelList("image")},
		// 参考仓后台要求预设正文非空（gen_image_preset_handlers.go）；档位片段允许为空（“无效果”档位）。
		ruleDef{"presets[*].value.prompt", checkPrompt(false, 8000)},
		ruleDef{"presets[*].value.params[*].options[*].prompt", checkPrompt(true, 2000)},
	),
	SettingCanvasPostprocess: newPolicy(kindJSON,
		ruleDef{"subtitleRemovalModels", checkDesubList},
		ruleDef{"upscaleModel", checkModelRef("video", true)},
	),
	SettingCanvasScript: newPolicy(kindJSON,
		ruleDef{"modelCandidates", checkScriptCandidates},
		ruleDef{"concurrency", checkInt(1, 20)},
		ruleDef{"maxRetries", checkInt(0, 10)},
	),
}

// ModelEditableFields 返回模型的可改字段模式。
func ModelEditableFields() []string { return modelPolicy.Patterns() }

// SettingEditableFields 返回内置配置 key 的可改字段模式；不是内置配置返回 ok=false。
func SettingEditableFields(key string) ([]string, bool) {
	pl, ok := settingPolicies[key]
	if !ok {
		return nil, false
	}
	return pl.Patterns(), true
}

// ---- 校验函数 ----

func checkBool(_ any, _ Path, v any, _ Catalog) error {
	if _, ok := v.(bool); !ok {
		return errors.New("必须是 true 或 false")
	}
	return nil
}

func checkName(_ any, _ Path, v any, _ Catalog) error {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return errors.New("不能为空")
	}
	if s != strings.TrimSpace(s) {
		return errors.New("首尾不能有空白")
	}
	if utf8.RuneCountInString(s) > 100 {
		return errors.New("不能超过 100 个字")
	}
	return nil
}

func asInt(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	return i, err == nil
}

func checkInt(min, max int64) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		i, ok := asInt(v)
		if !ok {
			return errors.New("必须是整数")
		}
		if i < min || i > max {
			return fmt.Errorf("必须在 %d 到 %d 之间", min, max)
		}
		return nil
	}
}

// checkUIDefault：uiSchema 参数的默认值必须是该参数 options 里的某个 value。
func checkUIDefault(doc any, p Path, v any, _ Catalog) error {
	opts, ok := getPath(doc, Path{{Name: "ui_schema"}, {Name: p[1].Name}, {Name: "options"}})
	arr, isArr := opts.([]any)
	if !ok || !isArr {
		return fmt.Errorf("参数 %s 没有可选项", p[1].Name)
	}
	for _, o := range arr {
		if m, ok := o.(map[string]any); ok && jsonEqual(m["value"], v) {
			return nil
		}
	}
	return errors.New("不在可选项中")
}

func checkBaseDomain(_ any, _ Path, v any, _ Catalog) error {
	s, ok := v.(string)
	if !ok || s == "" {
		return errors.New("不能为空")
	}
	if s != strings.TrimSpace(s) {
		return errors.New("首尾不能有空白")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("必须是 http:// 或 https:// 开头的地址")
	}
	// 上游地址 = base_domain + 模型的 base_path（以 / 开头），这里多一个 / 就会拼出 //v1/…。
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("只填协议、主机和端口，不要带路径、参数或结尾的 /")
	}
	return nil
}

func checkModelRef(typ string, allowEmpty bool) checkFunc {
	return func(_ any, _ Path, v any, cat Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是模型 ID 字符串")
		}
		if s == "" && allowEmpty {
			return nil
		}
		return requireModel(cat, s, typ)
	}
}

func requireModel(cat Catalog, id, typ string) error {
	if got, ok := cat[id]; !ok || got != typ {
		return fmt.Errorf("模型 %q 不存在或不是 %s 模型", id, typ)
	}
	return nil
}

func checkModelList(typ string) checkFunc {
	return func(_ any, _ Path, v any, cat Catalog) error {
		arr, ok := v.([]any)
		if !ok {
			return errors.New("必须是模型 ID 数组")
		}
		seen := map[string]bool{}
		for _, e := range arr {
			s, ok := e.(string)
			if !ok {
				return errors.New("必须是模型 ID 数组")
			}
			if err := requireModel(cat, s, typ); err != nil {
				return err
			}
			if seen[s] {
				return fmt.Errorf("模型 %q 重复", s)
			}
			seen[s] = true
		}
		return nil
	}
}

func checkTemplate(placeholder string) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是字符串")
		}
		if utf8.RuneCountInString(s) > 2000 {
			return errors.New("不能超过 2000 个字")
		}
		// 空模板 = 运行时用内置缺省模板（参考仓 canvasgen 的 Default*Template）。
		if s != "" && !strings.Contains(s, placeholder) {
			return fmt.Errorf("必须包含占位符 %s", placeholder)
		}
		return nil
	}
}

func checkPrompt(allowEmpty bool, maxRunes int) checkFunc {
	return func(_ any, _ Path, v any, _ Catalog) error {
		s, ok := v.(string)
		if !ok {
			return errors.New("必须是字符串")
		}
		if !allowEmpty && strings.TrimSpace(s) == "" {
			return errors.New("不能为空")
		}
		if utf8.RuneCountInString(s) > maxRunes {
			return fmt.Errorf("不能超过 %d 个字", maxRunes)
		}
		return nil
	}
}

// objectList 校验 v 是对象数组、每个对象只含 allowed 里的字段。
func objectList(v any, allowed ...string) ([]map[string]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, errors.New("必须是数组")
	}
	out := make([]map[string]any, 0, len(arr))
	for i, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("第 %d 项必须是对象", i+1)
		}
		for k := range m {
			if !slices.Contains(allowed, k) {
				return nil, fmt.Errorf("第 %d 项含有不认识的字段 %s", i+1, k)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func checkDesubList(_ any, _ Path, v any, cat Catalog) error {
	items, err := objectList(v, "modelId", "label", "subtitle", "isTimeLimited")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, m := range items {
		id, _ := m["modelId"].(string)
		if err := requireModel(cat, id, "video"); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if seen[id] {
			return fmt.Errorf("模型 %q 重复", id)
		}
		seen[id] = true
		if label, _ := m["label"].(string); strings.TrimSpace(label) == "" {
			return fmt.Errorf("第 %d 项：label 不能为空", i+1)
		}
		if sub, ok := m["subtitle"]; ok {
			if _, isStr := sub.(string); !isStr {
				return fmt.Errorf("第 %d 项：subtitle 必须是字符串", i+1)
			}
		}
		if tl, ok := m["isTimeLimited"]; ok {
			if _, isBool := tl.(bool); !isBool {
				return fmt.Errorf("第 %d 项：isTimeLimited 必须是 true 或 false", i+1)
			}
		}
	}
	return nil
}

func checkScriptCandidates(_ any, _ Path, v any, cat Catalog) error {
	items, err := objectList(v, "modelId", "displayName")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, m := range items {
		id, _ := m["modelId"].(string)
		if err := requireModel(cat, id, "text"); err != nil {
			return fmt.Errorf("第 %d 项：%w", i+1, err)
		}
		if seen[id] {
			return fmt.Errorf("模型 %q 重复", id)
		}
		seen[id] = true
		if name, _ := m["displayName"].(string); strings.TrimSpace(name) == "" {
			return fmt.Errorf("第 %d 项：displayName 不能为空", i+1)
		}
	}
	return nil
}

// ---- 编辑与合并（纯函数，不碰数据库）----

// applyEdits 在 cur（当前文档）上应用一批编辑，返回新文档和新的覆盖路径集合（已排序）。
// 全部合法才返回结果，任何一项不合法整体报 *ValidationError，调用方不写库。
// 编辑后的值与 seedDoc（当前二进制的种子）同一路径的值相等时，该路径移出覆盖集合，回到“跟随内置”。
func applyEdits(pl policy, cur, seedDoc any, overrides []string, edits map[string]json.RawMessage, cat Catalog) (any, []string, error) {
	if len(edits) == 0 {
		return nil, nil, &ValidationError{Path: "$", Msg: "没有要修改的字段"}
	}
	doc, err := deepCopy(cur)
	if err != nil {
		return nil, nil, err
	}
	ov := map[string]bool{}
	for _, o := range overrides {
		ov[o] = true
	}
	for _, k := range sortedKeys(edits) {
		p, err := ParsePath(k)
		if err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		r, ok := pl.find(p)
		if !ok {
			return nil, nil, &ValidationError{Path: k, Msg: "该字段不可修改"}
		}
		v, err := decodeJSON(edits[k])
		if err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: "不是合法的 JSON：" + err.Error()}
		}
		if err := r.check(doc, p, v, cat); err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		if doc, err = setPath(doc, p, v); err != nil {
			return nil, nil, &ValidationError{Path: k, Msg: err.Error()}
		}
		if sv, found := getPath(seedDoc, p); found && jsonEqual(sv, v) {
			delete(ov, p.String())
		} else {
			ov[p.String()] = true
		}
	}
	return doc, sortedKeys(ov), nil
}

// overlay 以 base（新版种子文档）为底，把 user 文档里 overrides 列出的字段逐个盖上去（spec M11）。
// 覆盖项在新版里不再可改、找不到挂载位置（预设或 uiSchema 参数被删）、或不再通过校验
// （选项被删、引用的模型被删）时丢弃，该字段改为跟随新版。返回合成文档、保留的和丢弃的覆盖路径。
func overlay(pl policy, base, user any, overrides []string, cat Catalog) (any, []string, []string) {
	doc, err := deepCopy(base)
	if err != nil {
		return base, nil, overrides
	}
	var kept, dropped []string
	for _, k := range overrides {
		if applyOne(pl, &doc, user, k, cat) {
			kept = append(kept, k)
		} else {
			dropped = append(dropped, k)
		}
	}
	return doc, kept, dropped
}

func applyOne(pl policy, doc *any, user any, k string, cat Catalog) bool {
	p, err := ParsePath(k)
	if err != nil {
		return false
	}
	r, ok := pl.find(p)
	if !ok {
		return false
	}
	v, ok := getPath(user, p)
	if !ok {
		return false
	}
	if v, err = deepCopy(v); err != nil {
		return false
	}
	if r.check(*doc, p, v, cat) != nil {
		return false
	}
	nd, err := setPath(*doc, p, v)
	if err != nil {
		return false
	}
	*doc = nd
	return true
}

// ---- 行 ↔ 文档 ----

// modelDoc 把模型的可改部分转成文档，字段名与列名一致。
func modelDoc(mp modelParts) (any, error) {
	doc := map[string]any{
		"name":        mp.Name,
		"enabled":     mp.Enabled,
		"hidden":      mp.Hidden,
		"sort_order":  json.Number(strconv.Itoa(mp.SortOrder)),
		"ui_schema":   nil,
		"gen_control": nil,
	}
	for col, raw := range map[string]*string{"ui_schema": mp.UISchema, "gen_control": mp.GenControl} {
		if raw == nil {
			continue
		}
		v, err := decodeJSON([]byte(*raw))
		if err != nil {
			return nil, fmt.Errorf("%s 不是合法 JSON: %w", col, err)
		}
		doc[col] = v
	}
	return doc, nil
}

// partsFromDoc 把文档写回模型的可改部分：被覆盖的字段取 doc，其余一律取 seed。
// ui_schema / gen_control 只要有一个子路径被覆盖就整列重新编码，否则保留种子原文（逐字节一致）。
func partsFromDoc(doc any, seed modelParts, overrides []string) (modelParts, error) {
	m, ok := doc.(map[string]any)
	if !ok {
		return modelParts{}, errors.New("模型文档不是对象")
	}
	has := func(field string) bool { return slices.Contains(overrides, field) }
	hasUnder := func(col string) bool {
		for _, o := range overrides {
			if strings.HasPrefix(o, col+".") {
				return true
			}
		}
		return false
	}
	out := seed
	if has("name") {
		s, ok := m["name"].(string)
		if !ok {
			return modelParts{}, errors.New("name 类型错误")
		}
		out.Name = s
	}
	for field, dst := range map[string]*bool{"enabled": &out.Enabled, "hidden": &out.Hidden} {
		if has(field) {
			b, ok := m[field].(bool)
			if !ok {
				return modelParts{}, fmt.Errorf("%s 类型错误", field)
			}
			*dst = b
		}
	}
	if has("sort_order") {
		i, ok := asInt(m["sort_order"])
		if !ok {
			return modelParts{}, errors.New("sort_order 类型错误")
		}
		out.SortOrder = int(i)
	}
	for col, dst := range map[string]**string{"ui_schema": &out.UISchema, "gen_control": &out.GenControl} {
		if !hasUnder(col) {
			continue
		}
		s, err := encodeJSON(m[col])
		if err != nil {
			return modelParts{}, err
		}
		*dst = &s
	}
	return out, nil
}

// settingDoc 把配置值转成文档：纯文本配置的文档就是字符串本身。
func settingDoc(kind valueKind, value string) (any, error) {
	if kind == kindText {
		return value, nil
	}
	return decodeJSON([]byte(value))
}

// settingValueFromDoc 把文档写回配置值：没有覆盖项时原样返回种子值（逐字节一致）。
func settingValueFromDoc(kind valueKind, seedValue string, doc any, overrides []string) (string, error) {
	if len(overrides) == 0 {
		return seedValue, nil
	}
	if kind == kindText {
		s, ok := doc.(string)
		if !ok {
			return "", errors.New("纯文本配置的值必须是字符串")
		}
		return s, nil
	}
	return encodeJSON(doc)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -v`
Expected: 全部 PASS。`TestSeedValuesPassTheirOwnRules` 证明 47 个模型和 6 项配置的每个可改字段的种子值都能通过校验；若失败，说明校验上限或白名单模式与生产数据不符，按失败信息调整规则（不得改种子）。

- [ ] **Step 5: 提交**

```bash
git add server/internal/builtin/policy.go server/internal/builtin/policy_test.go
git commit -m "feat(server): 内置数据可改字段白名单、校验与合并纯函数（spec M10/M11）"
```

---

### Task 5: 存储层

**Files:**
- Create: `server/internal/builtin/store.go`
- Test: `server/internal/builtin/store_test.go`
- Create: `server/internal/builtin/fixture_test.go`（Task 6、7、8 共用的测试辅助）

**Interfaces:**
- Consumes: Task 1 的列和表；Task 2 的 `SeedModel`、`parts()`、`modelParts`、`Catalog`、`nullableRaw`、`rawPtr`、`parseSeed`、`DatasetModelCenter`；`dbtest.Open`
- Produces（Task 6–8 依赖）：
  - `var ErrNotFound = errors.New("内置数据不存在")`
  - `type querier interface{ ExecContext; QueryContext; QueryRowContext }`（`*sql.DB`、`*sql.Tx` 都满足）
  - `type modelRow struct{ ID int64; ModelID, Type, BasePath string; Parts modelParts; RequestTemplate string; PollingTemplate, BillingConfig *string; ImageInputFormat, ImageRequestMode, AdminNote, PriceNote string; Overrides []string }`
  - `func rowFromSeed(m SeedModel) modelRow`
  - `func loadModelRows(ctx context.Context, q querier) (map[string]modelRow, error)`（按 model_id 索引）
  - `func loadModelRow(ctx context.Context, q querier, modelID string) (modelRow, error)`（不存在返回 `ErrNotFound`）
  - `func insertModelRow(ctx, q, r modelRow) error`（显式写 id）；`func updateModelRow(ctx, q, r modelRow) error`（按 model_id 整行覆盖，含 id）；`func deleteModelRow(ctx, q, modelID string) error`；`func moveModelID(ctx, q, from, to int64) error`
  - `func loadCatalog(ctx, q) (Catalog, error)`
  - `type settingRow struct{ Key, Value string; Overrides []string }`；`func loadSettingRow(ctx, q, key string) (settingRow, bool, error)`；`func upsertSettingRow(ctx, q, r settingRow) error`
  - `func readSeedHash(ctx, q, dataset string) (string, bool, error)`；`func writeSeedHash(ctx, q, dataset, hash string) error`
  - `func encodeOverrides([]string) string`（空 → `"[]"`）；`func decodeOverrides(string) ([]string, error)`（空数组 → nil）
  - 测试辅助（`fixture_test.go`）：`seedModelLine(t, id int64, modelID, typ string, over map[string]any) string`、`seedSettingLine(t, key, value string) string`、`mustSeed(t, models, settings []string) *Seed`、`ratioUI(def string, values ...string) map[string]any`、`insertSeedRows(t, conn *sql.DB, s *Seed)`、`modelByID(t, conn, modelID string) modelRow`、`settingByKey(t, conn, key string) settingRow`

- [ ] **Step 1: 写测试辅助和失败的测试**

`server/internal/builtin/fixture_test.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// 测试用的小种子。字段与 seed/model_configs.jsonl 一致，over 覆盖默认值。

func seedModelLine(t *testing.T, id int64, modelID, typ string, over map[string]any) string {
	t.Helper()
	m := map[string]any{
		"id": id, "type": typ, "name": modelID, "model_id": modelID, "base_path": "/v1/" + typ,
		"enabled": 1, "hidden": 0, "sort_order": 0,
		"request_template": map[string]any{"model": modelID}, "polling_template": nil, "ui_schema": nil,
		"billing_config": map[string]any{"items": nil, "flatFee": 0, "freezeEstimate": 0}, "gen_control": nil,
		"image_input_format": "url", "image_request_mode": "json", "admin_note": "", "price_note": "",
		"created_at": "2026-01-01 00:00:00.000000", "updated_at": "2026-01-01 00:00:00.000000",
	}
	for k, v := range over {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func seedSettingLine(t *testing.T, key, value string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"key": key, "value": value, "updated_at": "2026-01-01 00:00:00.000000"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustSeed(t *testing.T, models, settings []string) *Seed {
	t.Helper()
	s, err := parseSeed([]byte(strings.Join(models, "\n")+"\n"), []byte(strings.Join(settings, "\n")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ratioUI 是带两个选项的 uiSchema。
func ratioUI(def string, values ...string) map[string]any {
	opts := []any{}
	for _, v := range values {
		opts = append(opts, map[string]any{"label": v, "value": v})
	}
	return map[string]any{"ratio": map[string]any{"label": "比例", "default": def, "options": opts}}
}

// insertSeedRows 把小种子直接写进库（不经 Apply），给不依赖合并逻辑的测试用。
func insertSeedRows(t *testing.T, conn *sql.DB, s *Seed) {
	t.Helper()
	ctx := context.Background()
	for _, m := range s.Models {
		if err := insertModelRow(ctx, conn, rowFromSeed(m)); err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range s.Settings {
		if err := upsertSettingRow(ctx, conn, settingRow{Key: st.Key, Value: st.Value}); err != nil {
			t.Fatal(err)
		}
	}
}

func modelByID(t *testing.T, conn *sql.DB, modelID string) modelRow {
	t.Helper()
	r, err := loadModelRow(context.Background(), conn, modelID)
	if err != nil {
		t.Fatalf("%s: %v", modelID, err)
	}
	return r
}

func settingByKey(t *testing.T, conn *sql.DB, key string) settingRow {
	t.Helper()
	r, found, err := loadSettingRow(context.Background(), conn, key)
	if err != nil || !found {
		t.Fatalf("%s: found=%v err=%v", key, found, err)
	}
	return r
}
```

`server/internal/builtin/store_test.go`：

```go
package builtin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestModelRowRoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{
		seedModelLine(t, 7, "img-a", "image", map[string]any{"ui_schema": ratioUI("1:1", "1:1", "16:9"), "polling_template": map[string]any{"path": "/x"}}),
	}, nil)
	r := rowFromSeed(s.Models[0])
	r.Overrides = []string{"name"}
	if err := insertModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	got, err := loadModelRow(ctx, conn, "img-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("got  %+v\nwant %+v", got, r)
	}
	// JSON 列必须以 TEXT 落库（BLOB 会让 json 函数按 JSONB 解析）。
	var typ string
	if err := conn.QueryRow(`SELECT typeof(ui_schema) || typeof(request_template) || typeof(gen_control) FROM model_configs WHERE id = 7`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "texttextnull" {
		t.Fatalf("typeof = %s", typ)
	}
	if _, err := loadModelRow(ctx, conn, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestUpdateModelRowCanChangeID(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{seedModelLine(t, 7, "img-a", "image", nil)}, nil)
	r := rowFromSeed(s.Models[0])
	if err := insertModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	r.ID = 70
	r.Parts.Name = "改过"
	if err := updateModelRow(ctx, conn, r); err != nil {
		t.Fatal(err)
	}
	if err := moveModelID(ctx, conn, 70, -70); err != nil {
		t.Fatal(err)
	}
	rows, err := loadModelRows(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows["img-a"]; got.ID != -70 || got.Parts.Name != "改过" {
		t.Fatalf("got %+v", got)
	}
	if err := deleteModelRow(ctx, conn, "img-a"); err != nil {
		t.Fatal(err)
	}
	if cat, _ := loadCatalog(ctx, conn); len(cat) != 0 {
		t.Fatalf("catalog=%v", cat)
	}
}

func TestSettingRowAndSeedHash(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	if _, found, err := loadSettingRow(ctx, conn, "k"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if err := upsertSettingRow(ctx, conn, settingRow{Key: "k", Value: `{"a":1}`, Overrides: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := upsertSettingRow(ctx, conn, settingRow{Key: "k", Value: `{"a":2}`}); err != nil {
		t.Fatal(err)
	}
	r, found, err := loadSettingRow(ctx, conn, "k")
	if err != nil || !found || r.Value != `{"a":2}` || r.Overrides != nil {
		t.Fatalf("r=%+v found=%v err=%v", r, found, err)
	}
	if _, ok, err := readSeedHash(ctx, conn, DatasetModelCenter); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, h := range []string{"h1", "h2"} {
		if err := writeSeedHash(ctx, conn, DatasetModelCenter, h); err != nil {
			t.Fatal(err)
		}
	}
	if h, ok, err := readSeedHash(ctx, conn, DatasetModelCenter); !ok || err != nil || h != "h2" {
		t.Fatalf("h=%q ok=%v err=%v", h, ok, err)
	}
}

// settings.Repository.Set（P1a）写入的行没有覆盖记录，读出来是 nil。
func TestDecodeOverridesEmpty(t *testing.T) {
	if o, err := decodeOverrides("[]"); err != nil || o != nil {
		t.Fatalf("o=%v err=%v", o, err)
	}
	if _, err := decodeOverrides("{"); err == nil {
		t.Fatal("损坏的 user_overrides 应当报错")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -run 'ModelRow|SettingRow|DecodeOverrides' -v`
Expected: FAIL，编译错误 `undefined: rowFromSeed` 等。

- [ ] **Step 3: 实现**

`server/internal/builtin/store.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// 本文件是本包唯一直接写 SQL 的地方。所有函数都接收 querier（*sql.DB 或 *sql.Tx）：
// 在事务里必须传 tx——持有事务时再经 *sql.DB 写库会等满 busy_timeout 后 SQLITE_BUSY（p1-research.md 第 6 节）。
// 不复用 models.SQLiteRepository 的写方法，原因同上（它固定用 r.db）。

// ErrNotFound 表示要改的模型或配置不是内置数据。
var ErrNotFound = errors.New("内置数据不存在")

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// modelRow 是 model_configs 的一行（不含时间戳）。
type modelRow struct {
	ID               int64
	ModelID          string
	Type             string
	BasePath         string
	Parts            modelParts
	RequestTemplate  string
	PollingTemplate  *string
	BillingConfig    *string
	ImageInputFormat string
	ImageRequestMode string
	AdminNote        string
	PriceNote        string
	Overrides        []string
}

func rowFromSeed(m SeedModel) modelRow {
	r := modelRow{
		ID:               m.ID,
		ModelID:          m.ModelID,
		Type:             m.Type,
		BasePath:         m.BasePath,
		Parts:            m.parts(),
		RequestTemplate:  string(nullableRaw(m.RequestTemplate)),
		PollingTemplate:  rawPtr(m.PollingTemplate),
		BillingConfig:    rawPtr(m.BillingConfig),
		ImageInputFormat: m.ImageInputFormat,
		ImageRequestMode: m.ImageRequestMode,
		AdminNote:        m.AdminNote,
		PriceNote:        m.PriceNote,
	}
	// 与 models 仓库的 normalizeImageProtocolDefaults 一致。
	if r.ImageInputFormat == "" {
		r.ImageInputFormat = "url"
	}
	if r.ImageRequestMode == "" {
		r.ImageRequestMode = "json"
	}
	return r
}

func encodeOverrides(o []string) string {
	if len(o) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(o)
	return string(b)
}

func decodeOverrides(s string) ([]string, error) {
	var o []string
	if err := json.Unmarshal([]byte(s), &o); err != nil {
		return nil, fmt.Errorf("user_overrides 不是字符串数组: %w", err)
	}
	if len(o) == 0 {
		return nil, nil
	}
	return o, nil
}

// nullStr 把 *string 转成 SQL 参数：nil → NULL，否则 string（TEXT 落库，不能传 []byte）。
func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

const modelRowColumns = `id, model_id, type, base_path, name, enabled, hidden, sort_order, ui_schema, gen_control,
	request_template, polling_template, billing_config, image_input_format, image_request_mode, admin_note, price_note, user_overrides`

func scanModelRow(sc interface{ Scan(...any) error }) (modelRow, error) {
	var r modelRow
	var ui, gc, poll, bill sql.NullString
	var ov string
	if err := sc.Scan(&r.ID, &r.ModelID, &r.Type, &r.BasePath, &r.Parts.Name, &r.Parts.Enabled, &r.Parts.Hidden, &r.Parts.SortOrder,
		&ui, &gc, &r.RequestTemplate, &poll, &bill, &r.ImageInputFormat, &r.ImageRequestMode, &r.AdminNote, &r.PriceNote, &ov); err != nil {
		return modelRow{}, err
	}
	r.Parts.UISchema, r.Parts.GenControl = strPtr(ui), strPtr(gc)
	r.PollingTemplate, r.BillingConfig = strPtr(poll), strPtr(bill)
	var err error
	if r.Overrides, err = decodeOverrides(ov); err != nil {
		return modelRow{}, fmt.Errorf("模型 %s: %w", r.ModelID, err)
	}
	return r, nil
}

// loadModelRows 读出全部模型行，按 model_id 索引。
func loadModelRows(ctx context.Context, q querier) (map[string]modelRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+modelRowColumns+` FROM model_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]modelRow{}
	for rows.Next() {
		r, err := scanModelRow(rows)
		if err != nil {
			return nil, err
		}
		out[r.ModelID] = r
	}
	return out, rows.Err()
}

func loadModelRow(ctx context.Context, q querier, modelID string) (modelRow, error) {
	r, err := scanModelRow(q.QueryRowContext(ctx, `SELECT `+modelRowColumns+` FROM model_configs WHERE model_id = ?`, modelID))
	if errors.Is(err, sql.ErrNoRows) {
		return modelRow{}, ErrNotFound
	}
	return r, err
}

func insertModelRow(ctx context.Context, q querier, r modelRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO model_configs (id, model_id, type, base_path, name, enabled, hidden, sort_order, ui_schema, gen_control,
		request_template, polling_template, billing_config, image_input_format, image_request_mode, admin_note, price_note, user_overrides)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ModelID, r.Type, r.BasePath, r.Parts.Name, r.Parts.Enabled, r.Parts.Hidden, r.Parts.SortOrder,
		nullStr(r.Parts.UISchema), nullStr(r.Parts.GenControl), r.RequestTemplate, nullStr(r.PollingTemplate), nullStr(r.BillingConfig),
		r.ImageInputFormat, r.ImageRequestMode, r.AdminNote, r.PriceNote, encodeOverrides(r.Overrides))
	return err
}

// updateModelRow 按 model_id 整行覆盖，主键 id 也一并写成 r.ID（升级时种子可能给同一模型换了 id）。
func updateModelRow(ctx context.Context, q querier, r modelRow) error {
	_, err := q.ExecContext(ctx, `UPDATE model_configs SET id = ?, type = ?, base_path = ?, name = ?, enabled = ?, hidden = ?, sort_order = ?,
		ui_schema = ?, gen_control = ?, request_template = ?, polling_template = ?, billing_config = ?, image_input_format = ?,
		image_request_mode = ?, admin_note = ?, price_note = ?, user_overrides = ? WHERE model_id = ?`,
		r.ID, r.Type, r.BasePath, r.Parts.Name, r.Parts.Enabled, r.Parts.Hidden, r.Parts.SortOrder,
		nullStr(r.Parts.UISchema), nullStr(r.Parts.GenControl), r.RequestTemplate, nullStr(r.PollingTemplate), nullStr(r.BillingConfig),
		r.ImageInputFormat, r.ImageRequestMode, r.AdminNote, r.PriceNote, encodeOverrides(r.Overrides), r.ModelID)
	return err
}

func deleteModelRow(ctx context.Context, q querier, modelID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM model_configs WHERE model_id = ?`, modelID)
	return err
}

func moveModelID(ctx context.Context, q querier, from, to int64) error {
	_, err := q.ExecContext(ctx, `UPDATE model_configs SET id = ? WHERE id = ?`, to, from)
	return err
}

// loadCatalog 读出 model_id → type，供配置里的模型引用校验。
func loadCatalog(ctx context.Context, q querier) (Catalog, error) {
	rows, err := q.QueryContext(ctx, `SELECT model_id, type FROM model_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cat := Catalog{}
	for rows.Next() {
		var id, typ string
		if err := rows.Scan(&id, &typ); err != nil {
			return nil, err
		}
		cat[id] = typ
	}
	return cat, rows.Err()
}

type settingRow struct {
	Key       string
	Value     string
	Overrides []string
}

func loadSettingRow(ctx context.Context, q querier, key string) (settingRow, bool, error) {
	r := settingRow{Key: key}
	var ov string
	err := q.QueryRowContext(ctx, `SELECT value, user_overrides FROM settings WHERE "key" = ?`, key).Scan(&r.Value, &ov)
	if errors.Is(err, sql.ErrNoRows) {
		return settingRow{}, false, nil
	}
	if err != nil {
		return settingRow{}, false, err
	}
	if r.Overrides, err = decodeOverrides(ov); err != nil {
		return settingRow{}, false, fmt.Errorf("配置 %s: %w", key, err)
	}
	return r, true, nil
}

func upsertSettingRow(ctx context.Context, q querier, r settingRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings ("key", value, user_overrides) VALUES (?, ?, ?)
		ON CONFLICT ("key") DO UPDATE SET value = excluded.value, user_overrides = excluded.user_overrides`,
		r.Key, r.Value, encodeOverrides(r.Overrides))
	return err
}

func readSeedHash(ctx context.Context, q querier, dataset string) (string, bool, error) {
	var h string
	err := q.QueryRowContext(ctx, `SELECT hash FROM builtin_seed_state WHERE dataset = ?`, dataset).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

func writeSeedHash(ctx context.Context, q querier, dataset, hash string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO builtin_seed_state (dataset, hash) VALUES (?, ?)
		ON CONFLICT (dataset) DO UPDATE SET hash = excluded.hash, applied_at = CURRENT_TIMESTAMP`, dataset, hash)
	return err
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/builtin/store.go server/internal/builtin/store_test.go server/internal/builtin/fixture_test.go
git commit -m "feat(server): 内置数据存储层（model_configs/settings/builtin_seed_state）"
```

---

### Task 6: 启动时导入与升级合并

**Files:**
- Create: `server/internal/builtin/apply.go`
- Test: `server/internal/builtin/apply_test.go`

**Interfaces:**
- Consumes: Task 2 `Load`、`Seed`（`Model`、`Catalog`）、`SeedSetting`；Task 4 `modelPolicy`、`settingPolicies`、`overlay`、`modelDoc`、`partsFromDoc`、`settingDoc`、`settingValueFromDoc`；Task 5 全部存储函数和测试辅助
- Produces（Task 8、9 依赖）：
  - `func Apply(conn *sql.DB) error`——sidecar 启动入口
  - `func applySeed(ctx context.Context, conn *sql.DB, s *Seed) error`（可注入种子，测试用）
  - 内部：`applyModels`、`applyMergeModel`、`applySettings`、`applyMergeSetting`
  - 测试辅助（`apply_test.go`）：`mustApply(t, conn, s)`、`simulateUserEdit(t, conn, sql, args...)`

合并算法（`applySeed`）：
1. 读 `builtin_seed_state` 中 `model_center` 的哈希，与 `Seed.Hash` 相同就返回。
2. `BEGIN`（立即拿写锁）。
3. 模型：删除种子里没有的 model_id → 主键与种子不同的行挪到负数临时 id → 逐个种子模型：库里没有则插入种子行；有则 `overlay(modelPolicy, 新种子文档, 当前文档, 当前覆盖)`，`partsFromDoc` 写回，技术列一律取新种子，`user_overrides` 写保留下来的路径，主键写成种子 id。
4. 配置（在模型之后，校验用合并后的模型集合）：逐个种子 key：库里没有或没有覆盖 → 写种子原文；有覆盖 → `overlay` 后写回。不在种子里的 key 不碰。
5. 写入新哈希，提交。任何一步出错回滚。

- [ ] **Step 1: 写失败的测试**

`server/internal/builtin/apply_test.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func mustApply(t *testing.T, conn *sql.DB, s *Seed) {
	t.Helper()
	if err := applySeed(context.Background(), conn, s); err != nil {
		t.Fatal(err)
	}
}

// simulateUserEdit 模拟用户经 Service 改过字段后的库状态（Service 属于另一个任务，这里直接写库）。
func simulateUserEdit(t *testing.T, conn *sql.DB, sqlText string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(sqlText, args...); err != nil {
		t.Fatal(err)
	}
}

func TestApplyFirstLaunchImportsEmbeddedSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if err := Apply(conn); err != nil {
		t.Fatal(err)
	}
	s, _ := Load()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM model_configs WHERE user_overrides = '[]'`).Scan(&n); err != nil || n != 47 {
		t.Fatalf("模型数=%d err=%v", n, err)
	}
	for _, m := range s.Models {
		r := modelByID(t, conn, m.ModelID)
		if r.ID != m.ID {
			t.Fatalf("%s 的 id=%d，必须保留生产 id %d", m.ModelID, r.ID, m.ID)
		}
	}
	for _, st := range s.Settings {
		if got := settingByKey(t, conn, st.Key); got.Value != st.Value || got.Overrides != nil {
			t.Fatalf("%s 与种子不一致", st.Key)
		}
	}
	h, ok, err := readSeedHash(context.Background(), conn, DatasetModelCenter)
	if err != nil || !ok || h != s.Hash {
		t.Fatalf("hash=%q ok=%v err=%v", h, ok, err)
	}
}

// 种子没变时第二次启动什么都不做（连库里的漂移也不纠正——说明根本没进合并）。
func TestApplySkipsWhenHashUnchanged(t *testing.T) {
	conn := dbtest.Open(t)
	s := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)}, nil)
	mustApply(t, conn, s)
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '漂移' WHERE model_id = 'img-a'`)
	mustApply(t, conn, s)
	if got := modelByID(t, conn, "img-a").Parts.Name; got != "漂移" {
		t.Fatalf("name=%q：哈希未变不应重新合并", got)
	}
}

func TestApplyUpgradeAddsAndRemovesModels(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 2, "img-b", "image", nil),
	}, nil))
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 3, "img-c", "image", nil),
	}, nil))
	rows, err := loadModelRows(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["img-b"]; ok || len(rows) != 2 || rows["img-c"].ID != 3 {
		t.Fatalf("rows=%v", rows)
	}
}

func TestApplyUpgradeUntouchedFieldsFollowSeed(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A1"})}, nil))
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A2", "base_path": "/v2/images", "hidden": 1})}, nil))
	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "A2" || r.BasePath != "/v2/images" || !r.Parts.Hidden {
		t.Fatalf("got %+v", r)
	}
}

func TestApplyUpgradeKeepsUserChangedFields(t *testing.T) {
	conn := dbtest.Open(t)
	gc1 := map[string]any{"concurrency": 5, "modules": map[string]any{"canvas": map[string]any{"maxRetries": 0, "retryIntervalSec": 0}}}
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{
		"name": "A1", "ui_schema": ratioUI("1:1", "1:1", "16:9"), "gen_control": gc1,
	})}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的', enabled = 0,
		ui_schema = '{"ratio":{"label":"比例","default":"16:9","options":[{"label":"1:1","value":"1:1"},{"label":"16:9","value":"16:9"}]}}',
		gen_control = '{"concurrency":9,"modules":{"canvas":{"maxRetries":0,"retryIntervalSec":0}}}',
		user_overrides = '["enabled","gen_control.concurrency","name","ui_schema.ratio.default"]' WHERE model_id = 'img-a'`)

	gc2 := map[string]any{"concurrency": 50, "modules": map[string]any{"canvas": map[string]any{"maxRetries": 2, "retryIntervalSec": 10}}, "inputLimits": map[string]any{"video": map[string]any{"max": 1}}}
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{
		"name": "A2", "hidden": 1, "ui_schema": ratioUI("1:1", "1:1", "16:9", "9:16"), "gen_control": gc2,
	})}, nil))

	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "我的" || r.Parts.Enabled || !r.Parts.Hidden {
		t.Fatalf("name/enabled 应保留用户值、hidden 应跟随新版：%+v", r.Parts)
	}
	doc, err := modelDoc(r.Parts)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"ui_schema.ratio.default":               `"16:9"`, // 用户值
		"gen_control.concurrency":               `9`,      // 用户值
		"gen_control.modules.canvas.maxRetries": `2`,      // 新版
		"gen_control.inputLimits.video.max":     `1`,      // 新版
	} {
		v, _ := getPath(doc, mustParsePath(path))
		if got, _ := encodeJSON(v); got != want {
			t.Errorf("%s = %s, want %s", path, got, want)
		}
	}
	if opts, _ := getPath(doc, mustParsePath("ui_schema.ratio.options")); len(opts.([]any)) != 3 {
		t.Errorf("新版新增的选项没有进来：%v", opts)
	}
	if !reflect.DeepEqual(r.Overrides, []string{"enabled", "gen_control.concurrency", "name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", r.Overrides)
	}
}

// Review Focus 1：用户选的默认值在新版里不再是可选项 → 丢弃该覆盖，用新版默认值。
func TestApplyDropsOverrideWhenDefaultNoLongerOffered(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"ui_schema": ratioUI("1:1", "1:1", "4:3")})}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET ui_schema = '{"ratio":{"label":"比例","default":"4:3","options":[{"label":"1:1","value":"1:1"},{"label":"4:3","value":"4:3"}]}}',
		user_overrides = '["ui_schema.ratio.default"]' WHERE model_id = 'img-a'`)
	s2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"ui_schema": ratioUI("16:9", "1:1", "16:9")})}, nil)
	mustApply(t, conn, s2)
	r := modelByID(t, conn, "img-a")
	if r.Overrides != nil || *r.Parts.UISchema != *s2.Models[0].parts().UISchema {
		t.Fatalf("应回到新版 uiSchema 原文：overrides=%v ui=%s", r.Overrides, *r.Parts.UISchema)
	}
}

// 新版给同一个 model_id 换了 id（且旧 id 被另一个模型占用）：主键跟随种子，用户修改保留。
func TestApplyRenumberedModelKeepsOverride(t *testing.T) {
	conn := dbtest.Open(t)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", nil),
		seedModelLine(t, 2, "img-b", "image", nil),
	}, nil))
	simulateUserEdit(t, conn, `UPDATE model_configs SET name = '我的 A', user_overrides = '["name"]' WHERE model_id = 'img-a'`)
	mustApply(t, conn, mustSeed(t, []string{
		seedModelLine(t, 2, "img-a", "image", nil),
		seedModelLine(t, 1, "img-b", "image", nil),
	}, nil))
	a, b := modelByID(t, conn, "img-a"), modelByID(t, conn, "img-b")
	if a.ID != 2 || b.ID != 1 || a.Parts.Name != "我的 A" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func lightSetting(t *testing.T, models string, template string, presets string) string {
	t.Helper()
	return seedSettingLine(t, SettingCanvasLightControl, `{"models":`+models+`,"template":"`+template+`","presets":`+presets+`}`)
}

func TestApplySettingOverrideKeptAndStructureUpdated(t *testing.T) {
	conn := dbtest.Open(t)
	img := []string{seedModelLine(t, 1, "img-a", "image", nil)}
	mustApply(t, conn, mustSeed(t, img, []string{
		lightSetting(t, `["img-a"]`, "旧 {light}", `[{"key":"p1","name":"P1","prompt":"内置 p1"}]`),
	}))
	simulateUserEdit(t, conn, `UPDATE settings SET value = ?, user_overrides = '["presets[p1].prompt"]' WHERE "key" = ?`,
		`{"models":["img-a"],"presets":[{"key":"p1","name":"P1","prompt":"我的 p1"}],"template":"旧 {light}"}`, SettingCanvasLightControl)
	mustApply(t, conn, mustSeed(t, img, []string{
		lightSetting(t, `["img-a"]`, "新 {light}", `[{"key":"p1","name":"P1 新名","prompt":"内置 p1 v2"},{"key":"p2","name":"P2","prompt":"p2"}]`),
	}))
	got := settingByKey(t, conn, SettingCanvasLightControl)
	want := `{"models":["img-a"],"presets":[{"key":"p1","name":"P1 新名","prompt":"我的 p1"},{"key":"p2","name":"P2","prompt":"p2"}],"template":"新 {light}"}`
	if got.Value != want || !reflect.DeepEqual(got.Overrides, []string{"presets[p1].prompt"}) {
		t.Fatalf("got  %s %v\nwant %s", got.Value, got.Overrides, want)
	}
}

// Review Focus 2：用户选的候选模型在新版里被删了 → 丢弃该覆盖，用新版候选列表。
func TestApplyDropsSettingOverrideReferencingRemovedModel(t *testing.T) {
	conn := dbtest.Open(t)
	v1 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)},
		[]string{lightSetting(t, `["img-a"]`, "{light}", `[]`)})
	mustApply(t, conn, v1)
	simulateUserEdit(t, conn, `UPDATE settings SET value = '{"models":["img-b"],"presets":[],"template":"{light}"}', user_overrides = '["models"]' WHERE "key" = ?`, SettingCanvasLightControl)
	v2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil)},
		[]string{lightSetting(t, `["img-a"]`, "{light}", `[]`)})
	mustApply(t, conn, v2)
	got := settingByKey(t, conn, SettingCanvasLightControl)
	if seedV, _ := v2.Setting(SettingCanvasLightControl); got.Value != seedV.Value || got.Overrides != nil {
		t.Fatalf("got %s %v", got.Value, got.Overrides)
	}
}

// Review Focus 4：合并中途失败 → 整体回滚，哈希不写入，下次启动重试。
func TestApplyRollsBackOnFailure(t *testing.T) {
	conn := dbtest.Open(t)
	v1 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", nil), seedModelLine(t, 2, "img-b", "image", nil)}, nil)
	mustApply(t, conn, v1)
	simulateUserEdit(t, conn, `CREATE TRIGGER boom BEFORE INSERT ON model_configs WHEN NEW.model_id = 'img-c' BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	v2 := mustSeed(t, []string{seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "改名"}), seedModelLine(t, 3, "img-c", "image", nil)}, nil)
	if err := applySeed(context.Background(), conn, v2); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	rows, _ := loadModelRows(context.Background(), conn)
	if _, ok := rows["img-b"]; !ok || rows["img-a"].Parts.Name != "img-a" {
		t.Fatalf("失败后库应保持 v1：%v", rows)
	}
	if h, _, _ := readSeedHash(context.Background(), conn, DatasetModelCenter); h != v1.Hash {
		t.Fatal("失败时不得写入新哈希")
	}
	simulateUserEdit(t, conn, `DROP TRIGGER boom`)
	mustApply(t, conn, v2)
	if _, err := loadModelRow(context.Background(), conn, "img-c"); err != nil {
		t.Fatalf("重试应成功：%v", err)
	}
}

func TestApplyLeavesNonSeedSettingsAndKeepsBaseDomainOverride(t *testing.T) {
	conn := dbtest.Open(t)
	models := []string{seedModelLine(t, 1, "img-a", "image", nil)}
	mustApply(t, conn, mustSeed(t, models, []string{seedSettingLine(t, SettingBaseDomain, "http://old.example:3000")}))
	simulateUserEdit(t, conn, `INSERT INTO settings ("key", value) VALUES (?, 'sk-user')`, SettingUpstreamAPIKey)
	simulateUserEdit(t, conn, `UPDATE settings SET value = 'https://mine.example', user_overrides = '["$"]' WHERE "key" = ?`, SettingBaseDomain)
	mustApply(t, conn, mustSeed(t, models, []string{seedSettingLine(t, SettingBaseDomain, "http://new.example:3000")}))
	if got := settingByKey(t, conn, SettingUpstreamAPIKey); got.Value != "sk-user" {
		t.Fatalf("upstream_api_key 被改动：%q", got.Value)
	}
	if got := settingByKey(t, conn, SettingBaseDomain); got.Value != "https://mine.example" {
		t.Fatalf("base_domain=%q，应保留用户值", got.Value)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -run TestApply -v`
Expected: FAIL，编译错误 `undefined: applySeed` / `undefined: Apply`。

- [ ] **Step 3: 实现**

`server/internal/builtin/apply.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// Apply 把编译进二进制的种子合并进库（spec M9、M11）。sidecar 每次启动在 db.Migrate 之后、
// 开始监听之前调用一次。
//
// builtin_seed_state 里记录的哈希与本次种子相同就直接返回，不拿写锁。否则在一个写事务里完成全部合并：
// 首次启动就是“与空库合并”，和升级走同一套算法；任何一步失败整体回滚、哈希不写入，下次启动重试。
func Apply(conn *sql.DB) error {
	s, err := Load()
	if err != nil {
		return fmt.Errorf("解析内置种子: %w", err)
	}
	return applySeed(context.Background(), conn, s)
}

func applySeed(ctx context.Context, conn *sql.DB, s *Seed) error {
	h, ok, err := readSeedHash(ctx, conn, DatasetModelCenter)
	if err != nil {
		return err
	}
	if ok && h == s.Hash {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyModels(ctx, tx, s); err != nil {
		return fmt.Errorf("合并内置模型: %w", err)
	}
	// 配置在模型之后合并：配置里引用的模型要按合并后的模型集合校验。
	if err := applySettings(ctx, tx, s); err != nil {
		return fmt.Errorf("合并内置配置: %w", err)
	}
	if err := writeSeedHash(ctx, tx, DatasetModelCenter, s.Hash); err != nil {
		return err
	}
	return tx.Commit()
}

// applyModels 按 model_id 合并模型。
func applyModels(ctx context.Context, q querier, s *Seed) error {
	cur, err := loadModelRows(ctx, q)
	if err != nil {
		return err
	}
	// 1. 新版种子里没有的模型删除。M8 规定模型全部内置、用户不能新增，所以库里不存在需要保留的“用户模型”。
	for id := range cur {
		if _, ok := s.Model(id); !ok {
			if err := deleteModelRow(ctx, q, id); err != nil {
				return err
			}
			delete(cur, id)
		}
	}
	// 2. 主键与种子不一致的行先挪到负数临时主键，第 3 步再写成种子 id，避免逐行改主键时撞上别的行。
	//    种子保留生产 id 是因为 P2b 的 agent_tools.source_model_id 引用它。
	for _, m := range s.Models {
		if r, ok := cur[m.ModelID]; ok && r.ID != m.ID {
			if err := moveModelID(ctx, q, r.ID, -r.ID); err != nil {
				return err
			}
			r.ID = -r.ID
			cur[m.ModelID] = r
		}
	}
	cat := s.Catalog()
	// 3. 新模型插入；已有模型以新版种子为底，盖上用户改过的字段。
	for _, m := range s.Models {
		seedRow := rowFromSeed(m)
		r, ok := cur[m.ModelID]
		if !ok {
			if err := insertModelRow(ctx, q, seedRow); err != nil {
				return fmt.Errorf("插入 %s: %w", m.ModelID, err)
			}
			continue
		}
		merged, err := applyMergeModel(seedRow, r, cat)
		if err != nil {
			return fmt.Errorf("合并 %s: %w", m.ModelID, err)
		}
		if err := updateModelRow(ctx, q, merged); err != nil {
			return fmt.Errorf("更新 %s: %w", m.ModelID, err)
		}
	}
	return nil
}

func applyMergeModel(seedRow, cur modelRow, cat Catalog) (modelRow, error) {
	if len(cur.Overrides) == 0 {
		return seedRow, nil
	}
	base, err := modelDoc(seedRow.Parts)
	if err != nil {
		return modelRow{}, err
	}
	user, err := modelDoc(cur.Parts)
	if err != nil {
		// 库里的值坏了就放弃用户修改，回到内置值，不能让启动失败。
		log.Printf("内置数据合并：模型 %s 的当前值无法解析（%v），丢弃用户修改 %v", cur.ModelID, err, cur.Overrides)
		return seedRow, nil
	}
	doc, kept, dropped := overlay(modelPolicy, base, user, cur.Overrides, cat)
	if len(dropped) > 0 {
		log.Printf("内置数据合并：模型 %s 的用户修改 %v 在新版中已失效，改为跟随内置值", cur.ModelID, dropped)
	}
	parts, err := partsFromDoc(doc, seedRow.Parts, kept)
	if err != nil {
		return modelRow{}, err
	}
	out := seedRow
	out.Parts = parts
	out.Overrides = kept
	return out, nil
}

// applySettings 合并种子里的配置 key。不在种子里的 key（upstream_api_key 等）一概不碰；
// 旧版种子有、新版去掉的 key 也保留不删——无法区分它来自种子还是运行时写入，留着不生效也无害。
func applySettings(ctx context.Context, q querier, s *Seed) error {
	cat, err := loadCatalog(ctx, q)
	if err != nil {
		return err
	}
	for _, st := range s.Settings {
		pl, ok := settingPolicies[st.Key]
		if !ok {
			return fmt.Errorf("种子配置 %s 没有对应的可改字段策略", st.Key)
		}
		cur, found, err := loadSettingRow(ctx, q, st.Key)
		if err != nil {
			return err
		}
		row := settingRow{Key: st.Key, Value: st.Value}
		if found && len(cur.Overrides) > 0 {
			row, err = applyMergeSetting(pl, st, cur, cat)
			if err != nil {
				return fmt.Errorf("合并 %s: %w", st.Key, err)
			}
		}
		if err := upsertSettingRow(ctx, q, row); err != nil {
			return err
		}
	}
	return nil
}

func applyMergeSetting(pl policy, st SeedSetting, cur settingRow, cat Catalog) (settingRow, error) {
	base, err := settingDoc(pl.kind, st.Value)
	if err != nil {
		return settingRow{}, fmt.Errorf("种子值无法解析: %w", err)
	}
	user, err := settingDoc(pl.kind, cur.Value)
	if err != nil {
		log.Printf("内置数据合并：配置 %s 的当前值无法解析（%v），丢弃用户修改 %v", st.Key, err, cur.Overrides)
		return settingRow{Key: st.Key, Value: st.Value}, nil
	}
	doc, kept, dropped := overlay(pl, base, user, cur.Overrides, cat)
	if len(dropped) > 0 {
		log.Printf("内置数据合并：配置 %s 的用户修改 %v 在新版中已失效，改为跟随内置值", st.Key, dropped)
	}
	v, err := settingValueFromDoc(pl.kind, st.Value, doc, kept)
	if err != nil {
		return settingRow{}, err
	}
	return settingRow{Key: st.Key, Value: v, Overrides: kept}, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -run TestApply -v && go test ./internal/builtin/`
Expected: 11 个 `TestApply*` 全部 PASS（部分用例会在 stderr 打出“用户修改在新版中已失效”的日志，属预期）；包内其余测试仍 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/builtin/apply.go server/internal/builtin/apply_test.go
git commit -m "feat(server): 内置数据首次导入与升级字段级合并（spec M9/M11）"
```

---

### Task 7: 编辑服务（供 P4 调用）

**Files:**
- Create: `server/internal/builtin/service.go`
- Test: `server/internal/builtin/service_test.go`

**Interfaces:**
- Consumes: Task 2 `Load`、`Seed`；Task 4 `modelPolicy`、`settingPolicies`、`applyEdits`、`modelDoc`、`partsFromDoc`、`settingDoc`、`settingValueFromDoc`、`ValidationError`，测试辅助 `edits`；Task 5 存储函数、`ErrNotFound`、测试辅助 `insertSeedRows`、`mustSeed`、`seedModelLine`、`seedSettingLine`、`ratioUI`、`modelByID`；`models.NewSQLiteRepository(db).List(onlyVisible bool)`、`GetByModelID(modelID string)`（P1a）
- Produces（P4 的 HTTP 层依赖）：
  - `type Service struct{ … }`；`func NewService(conn *sql.DB) (*Service, error)`
  - `type ModelState struct{ models.ModelConfig; Overrides []string \`json:"overrides"\` }`
  - `type SettingState struct{ Key string \`json:"key"\`; Value string \`json:"value"\`; Overrides []string \`json:"overrides"\` }`
  - `func (s *Service) ListModels(ctx context.Context) ([]ModelState, error)`
  - `func (s *Service) UpdateModel(ctx context.Context, modelID string, edits map[string]json.RawMessage) (ModelState, error)`
  - `func (s *Service) RestoreModelDefault(ctx context.Context, modelID string) (ModelState, error)`
  - `func (s *Service) ListSettings(ctx context.Context) ([]SettingState, error)`
  - `func (s *Service) UpdateSetting(ctx context.Context, key string, edits map[string]json.RawMessage) (SettingState, error)`
  - `func (s *Service) RestoreSettingDefault(ctx context.Context, key string) (SettingState, error)`
  - 错误：不是内置数据 → `ErrNotFound`（P4 映射 404）；字段不可改或校验失败 → `*ValidationError`（P4 映射 400，`Path` 指出字段）
  - 编辑请求格式：`edits` 的 key 是字段路径（`ModelEditableFields()` / `SettingEditableFields(key)` 返回的模式的具体化），value 是新值的 JSON。例：`{"name":"\"我的模型\"","ui_schema.ratio.default":"\"16:9\""}`；`base_domain` 用 `{"$":"\"https://gw.example.com\""}`

- [ ] **Step 1: 写失败的测试**

`server/internal/builtin/service_test.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// svcFixture 建库、直接写入小种子（不经 Apply），返回绑定该种子的 Service。
func svcFixture(t *testing.T, s *Seed) (*Service, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	insertSeedRows(t, conn, s)
	return &Service{db: conn, seed: s}, conn
}

func svcSeedV1(t *testing.T) *Seed {
	return mustSeed(t, []string{
		seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A", "ui_schema": ratioUI("1:1", "1:1", "16:9")}),
		seedModelLine(t, 2, "img-b", "image", nil),
		seedModelLine(t, 3, "txt-a", "text", nil),
	}, []string{
		seedSettingLine(t, SettingBaseDomain, "http://gw.example:3000"),
		seedSettingLine(t, SettingCanvasLightControl, `{"models":["img-a"],"template":"{light}","presets":[{"key":"p1","name":"P1","direction":"","prompt":"内置"}]}`),
	})
}

func TestServiceUpdateModel(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	st, err := svc.UpdateModel(context.Background(), "img-a", edits(
		"name", `"我的 A"`, "enabled", `false`, "ui_schema.ratio.default", `"16:9"`, "gen_control.concurrency", `3`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "我的 A" || st.Enabled || st.GenConcurrency() != 3 || !strings.Contains(string(st.UISchema), `"default":"16:9"`) {
		t.Fatalf("state=%+v", st)
	}
	if !reflect.DeepEqual(st.Overrides, []string{"enabled", "gen_control.concurrency", "name", "ui_schema.ratio.default"}) {
		t.Fatalf("overrides=%v", st.Overrides)
	}
}

// Review Focus 5：一批编辑里夹带技术字段 → 整批拒绝，库不变。
func TestServiceUpdateModelRejectsTechnicalFieldAtomically(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	_, err := svc.UpdateModel(context.Background(), "img-a", edits("name", `"改了"`, "base_path", `"/evil"`))
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Path != "base_path" {
		t.Fatalf("err=%v", err)
	}
	r := modelByID(t, conn, "img-a")
	if r.Parts.Name != "A" || r.BasePath != "/v1/image" || r.Overrides != nil {
		t.Fatalf("库被改动：%+v", r)
	}
}

// Review Focus 3：把字段改回内置值 → 覆盖记录清除（升级时它因此跟随新版，见 integration_test.go）。
func TestServiceUpdateModelBackToSeedClearsOverride(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"临时"`)); err != nil {
		t.Fatal(err)
	}
	st, err := svc.UpdateModel(ctx, "img-a", edits("name", `"A"`))
	if err != nil || len(st.Overrides) != 0 {
		t.Fatalf("overrides=%v err=%v", st.Overrides, err)
	}
	if r := modelByID(t, conn, "img-a"); r.Overrides != nil || r.Parts.Name != "A" {
		t.Fatalf("row=%+v", r)
	}
}

func TestServiceRestoreModelDefault(t *testing.T) {
	s := svcSeedV1(t)
	svc, conn := svcFixture(t, s)
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"我的"`, "hidden", `true`, "ui_schema.ratio.default", `"16:9"`)); err != nil {
		t.Fatal(err)
	}
	st, err := svc.RestoreModelDefault(ctx, "img-a")
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "A" || st.Hidden || len(st.Overrides) != 0 {
		t.Fatalf("state=%+v", st)
	}
	if got, want := *modelByID(t, conn, "img-a").Parts.UISchema, *s.Models[0].parts().UISchema; got != want {
		t.Fatalf("ui_schema=%s，应逐字节恢复为种子 %s", got, want)
	}
}

func TestServiceUpdateSetting(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	st, err := svc.UpdateSetting(ctx, SettingCanvasLightControl, edits("presets[p1].prompt", `"我的提示词"`, "models", `["img-b","img-a"]`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Value, `"prompt":"我的提示词"`) || !strings.Contains(st.Value, `"models":["img-b","img-a"]`) {
		t.Fatalf("value=%s", st.Value)
	}
	if !reflect.DeepEqual(st.Overrides, []string{"models", "presets[p1].prompt"}) {
		t.Fatalf("overrides=%v", st.Overrides)
	}
	for name, e := range map[string]map[string]string{
		"不存在的模型": {"models": `["nope"]`},
		"文本模型":   {"models": `["txt-a"]`},
		"结构字段":   {"presets[p1].direction": `"top"`},
		"新增预设":   {"presets[p9].prompt": `"x"`},
	} {
		raw := []string{}
		for k, v := range e {
			raw = append(raw, k, v)
		}
		var ve *ValidationError
		if _, err := svc.UpdateSetting(ctx, SettingCanvasLightControl, edits(raw...)); !errors.As(err, &ve) {
			t.Errorf("%s：err=%v，want ValidationError", name, err)
		}
	}
}

func TestServiceBaseDomain(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	st, err := svc.UpdateSetting(ctx, SettingBaseDomain, edits("$", `"https://mine.example"`))
	if err != nil || st.Value != "https://mine.example" || !reflect.DeepEqual(st.Overrides, []string{"$"}) {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	var ve *ValidationError
	if _, err := svc.UpdateSetting(ctx, SettingBaseDomain, edits("$", `"https://mine.example/"`)); !errors.As(err, &ve) {
		t.Fatalf("结尾 / 应当被拒绝：%v", err)
	}
	st, err = svc.RestoreSettingDefault(ctx, SettingBaseDomain)
	if err != nil || st.Value != "http://gw.example:3000" || len(st.Overrides) != 0 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestServiceNotFound(t *testing.T) {
	svc, conn := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "nope", edits("name", `"x"`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := svc.UpdateSetting(ctx, SettingUpstreamAPIKey, edits("$", `"sk"`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upstream_api_key 不是内置配置：%v", err)
	}
	if _, err := svc.RestoreSettingDefault(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := conn.Exec(`INSERT INTO settings ("key", value) VALUES (?, 'sk-user')`, SettingUpstreamAPIKey); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSettings(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListSettings 只返回内置配置：%v %v", list, err)
	}
}

func TestServiceListModelsIncludesOverrides(t *testing.T) {
	svc, _ := svcFixture(t, svcSeedV1(t))
	ctx := context.Background()
	if _, err := svc.UpdateModel(ctx, "img-b", edits("sort_order", `5`)); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListModels(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	for _, m := range list {
		want := []string{}
		if m.ModelID == "img-b" {
			want = []string{"sort_order"}
		}
		if !reflect.DeepEqual(m.Overrides, want) {
			t.Fatalf("%s overrides=%v", m.ModelID, m.Overrides)
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/builtin/ -run TestService -v`
Expected: FAIL，编译错误 `undefined: Service`。

- [ ] **Step 3: 实现**

`server/internal/builtin/service.go`：

```go
package builtin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// Service 是设置页（P4）修改内置数据的唯一入口：只放行白名单字段（spec M10）、保存前校验、
// 记录用户改过的字段（spec M11）、提供“恢复默认”。内置模型和内置配置不得再经
// models.Repository 的 Create/Update/Delete 或 settings.Repository.Set 写入，否则覆盖记录会失真。
type Service struct {
	db   *sql.DB
	seed *Seed
}

// NewService 用编译进二进制的种子创建 Service。调用前 Apply 必须已经成功。
func NewService(conn *sql.DB) (*Service, error) {
	s, err := Load()
	if err != nil {
		return nil, err
	}
	return &Service{db: conn, seed: s}, nil
}

// ModelState 是一个内置模型的当前值和用户改过的字段。
type ModelState struct {
	models.ModelConfig
	Overrides []string `json:"overrides"`
}

// SettingState 是一项内置配置的当前值（原文）和用户改过的字段。
type SettingState struct {
	Key       string   `json:"key"`
	Value     string   `json:"value"`
	Overrides []string `json:"overrides"`
}

func nonNil(o []string) []string {
	if o == nil {
		return []string{}
	}
	return o
}

// ListModels 返回全部内置模型（含隐藏、停用的），顺序同 models.Repository.List。
func (s *Service) ListModels(ctx context.Context) ([]ModelState, error) {
	list, err := models.NewSQLiteRepository(s.db).List(false)
	if err != nil {
		return nil, err
	}
	rows, err := loadModelRows(ctx, s.db)
	if err != nil {
		return nil, err
	}
	out := make([]ModelState, 0, len(list))
	for _, m := range list {
		out = append(out, ModelState{ModelConfig: m, Overrides: nonNil(rows[m.ModelID].Overrides)})
	}
	return out, nil
}

func (s *Service) modelState(ctx context.Context, modelID string) (ModelState, error) {
	m, err := models.NewSQLiteRepository(s.db).GetByModelID(modelID)
	if err != nil {
		return ModelState{}, err
	}
	r, err := loadModelRow(ctx, s.db, modelID)
	if err != nil {
		return ModelState{}, err
	}
	return ModelState{ModelConfig: *m, Overrides: nonNil(r.Overrides)}, nil
}

// UpdateModel 修改一个内置模型。edits 的 key 是字段路径（见 ModelEditableFields），value 是新值的 JSON。
// 任何一项不在白名单或校验不通过，返回 *ValidationError，库不变。
func (s *Service) UpdateModel(ctx context.Context, modelID string, edits map[string]json.RawMessage) (ModelState, error) {
	sm, ok := s.seed.Model(modelID)
	if !ok {
		return ModelState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelState{}, err
	}
	defer tx.Rollback()
	cur, err := loadModelRow(ctx, tx, modelID)
	if err != nil {
		return ModelState{}, err
	}
	cat, err := loadCatalog(ctx, tx)
	if err != nil {
		return ModelState{}, err
	}
	seedParts := sm.parts()
	curDoc, err := modelDoc(cur.Parts)
	if err != nil {
		return ModelState{}, err
	}
	seedDoc, err := modelDoc(seedParts)
	if err != nil {
		return ModelState{}, err
	}
	doc, ov, err := applyEdits(modelPolicy, curDoc, seedDoc, cur.Overrides, edits, cat)
	if err != nil {
		return ModelState{}, err
	}
	if cur.Parts, err = partsFromDoc(doc, seedParts, ov); err != nil {
		return ModelState{}, err
	}
	cur.Overrides = ov
	if err := updateModelRow(ctx, tx, cur); err != nil {
		return ModelState{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelState{}, err
	}
	return s.modelState(ctx, modelID)
}

// RestoreModelDefault 把一个内置模型整行恢复成种子值并清空覆盖记录（设置页的“恢复默认”）。
func (s *Service) RestoreModelDefault(ctx context.Context, modelID string) (ModelState, error) {
	sm, ok := s.seed.Model(modelID)
	if !ok {
		return ModelState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelState{}, err
	}
	defer tx.Rollback()
	if _, err := loadModelRow(ctx, tx, modelID); err != nil {
		return ModelState{}, err
	}
	if err := updateModelRow(ctx, tx, rowFromSeed(sm)); err != nil {
		return ModelState{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelState{}, err
	}
	return s.modelState(ctx, modelID)
}

// ListSettings 返回全部内置配置（种子里的 key，按种子顺序）。不含 upstream_api_key 等非内置 key。
func (s *Service) ListSettings(ctx context.Context) ([]SettingState, error) {
	out := make([]SettingState, 0, len(s.seed.Settings))
	for _, st := range s.seed.Settings {
		state, err := s.settingState(ctx, s.db, st.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}

func (s *Service) settingState(ctx context.Context, q querier, key string) (SettingState, error) {
	r, found, err := loadSettingRow(ctx, q, key)
	if err != nil {
		return SettingState{}, err
	}
	if !found {
		return SettingState{}, fmt.Errorf("内置配置 %s 不在库里，Apply 没有执行？", key)
	}
	return SettingState{Key: key, Value: r.Value, Overrides: nonNil(r.Overrides)}, nil
}

// UpdateSetting 修改一项内置配置。edits 的 key 是字段路径（见 SettingEditableFields），
// 纯文本配置（base_domain）用 "$" 表示整个值。
func (s *Service) UpdateSetting(ctx context.Context, key string, edits map[string]json.RawMessage) (SettingState, error) {
	st, ok := s.seed.Setting(key)
	pl, hasPolicy := settingPolicies[key]
	if !ok || !hasPolicy {
		return SettingState{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SettingState{}, err
	}
	defer tx.Rollback()
	cur, found, err := loadSettingRow(ctx, tx, key)
	if err != nil {
		return SettingState{}, err
	}
	if !found {
		cur = settingRow{Key: key, Value: st.Value}
	}
	cat, err := loadCatalog(ctx, tx)
	if err != nil {
		return SettingState{}, err
	}
	curDoc, err := settingDoc(pl.kind, cur.Value)
	if err != nil {
		return SettingState{}, fmt.Errorf("配置 %s 的当前值无法解析: %w", key, err)
	}
	seedDoc, err := settingDoc(pl.kind, st.Value)
	if err != nil {
		return SettingState{}, err
	}
	doc, ov, err := applyEdits(pl, curDoc, seedDoc, cur.Overrides, edits, cat)
	if err != nil {
		return SettingState{}, err
	}
	v, err := settingValueFromDoc(pl.kind, st.Value, doc, ov)
	if err != nil {
		return SettingState{}, err
	}
	if err := upsertSettingRow(ctx, tx, settingRow{Key: key, Value: v, Overrides: ov}); err != nil {
		return SettingState{}, err
	}
	if err := tx.Commit(); err != nil {
		return SettingState{}, err
	}
	return s.settingState(ctx, s.db, key)
}

// RestoreSettingDefault 把一项内置配置恢复成种子值并清空覆盖记录。
func (s *Service) RestoreSettingDefault(ctx context.Context, key string) (SettingState, error) {
	st, ok := s.seed.Setting(key)
	if !ok {
		return SettingState{}, ErrNotFound
	}
	if err := upsertSettingRow(ctx, s.db, settingRow{Key: key, Value: st.Value}); err != nil {
		return SettingState{}, err
	}
	return s.settingState(ctx, s.db, key)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/builtin/ -run TestService -v && go test ./internal/builtin/`
Expected: `service_test.go` 的 8 个 `TestService*` 全部 PASS；包内其余测试仍 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/builtin/service.go server/internal/builtin/service_test.go
git commit -m "feat(server): 内置数据编辑服务——白名单字段修改与恢复默认（spec M10）"
```

---

### Task 8: 端到端测试——编辑跨升级保留、恢复默认后跟随新版

**Files:**
- Test: `server/internal/builtin/integration_test.go`

**Interfaces:**
- Consumes: Task 6 `Apply`、`applySeed`；Task 7 `Service`、`NewService`、`UpdateModel`、`RestoreModelDefault`、`UpdateSetting`；Task 4 测试辅助 `edits`；Task 5 测试辅助
- Produces: 无（只有测试）

- [ ] **Step 1: 写测试**

`server/internal/builtin/integration_test.go`：

```go
package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 跨 Apply 与 Service 的端到端约定（spec M10 + M11）：
// 经 Service 改过的字段在升级后保留；改回内置值或“恢复默认”后的字段在升级后跟随新版。
func TestServiceEditsSurviveUpgradeAndRestoreFollowsSeed(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.Open(t)
	version := func(tag string) *Seed {
		return mustSeed(t, []string{
			seedModelLine(t, 1, "img-a", "image", map[string]any{"name": "A " + tag, "ui_schema": ratioUI("1:1", "1:1", "16:9")}),
			seedModelLine(t, 2, "img-b", "image", map[string]any{"name": "B " + tag}),
			seedModelLine(t, 3, "img-c", "image", map[string]any{"name": "C " + tag}),
		}, []string{
			seedSettingLine(t, SettingCanvasLightControl, `{"models":["img-a"],"template":"`+tag+` {light}","presets":[{"key":"p1","name":"P1","prompt":"内置 `+tag+`"}]}`),
		})
	}
	v1, v2 := version("v1"), version("v2")
	if err := applySeed(ctx, conn, v1); err != nil {
		t.Fatal(err)
	}
	svc := &Service{db: conn, seed: v1}
	if _, err := svc.UpdateModel(ctx, "img-a", edits("name", `"我的 A"`, "ui_schema.ratio.default", `"16:9"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-b", edits("name", `"临时 B"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreModelDefault(ctx, "img-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-c", edits("name", `"临时 C"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "img-c", edits("name", `"C v1"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSetting(ctx, SettingCanvasLightControl, edits("presets[p1].prompt", `"我的提示词"`)); err != nil {
		t.Fatal(err)
	}

	if err := applySeed(ctx, conn, v2); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"img-a": "我的 A", "img-b": "B v2", "img-c": "C v2"} {
		if got := modelByID(t, conn, id).Parts.Name; got != want {
			t.Errorf("%s name=%q, want %q", id, got, want)
		}
	}
	if ui := *modelByID(t, conn, "img-a").Parts.UISchema; !strings.Contains(ui, `"default":"16:9"`) {
		t.Errorf("img-a 的默认比例应保留用户值：%s", ui)
	}
	light := settingByKey(t, conn, SettingCanvasLightControl).Value
	if !strings.Contains(light, `"prompt":"我的提示词"`) || !strings.Contains(light, `"template":"v2 {light}"`) {
		t.Errorf("light=%s", light)
	}
}

// 真实种子：Service 能在内置数据上完成一次典型编辑。
func TestNewServiceOnEmbeddedSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if err := Apply(conn); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.UpdateSetting(ctx, SettingGenImagePresets, edits("presets[storyboard].value.prompt", `"我的故事板"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateModel(ctx, "gpt-image-2", edits("gen_control.modules.canvas.maxRetries", `1`)); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: 运行测试**

Run: `cd server && go test ./internal/builtin/ -run 'TestServiceEditsSurviveUpgrade|TestNewServiceOnEmbeddedSeed' -v`
Expected: 两个测试 PASS。若失败，说明 Task 6 与 Task 7 对覆盖记录的约定不一致（例如 Service 写入的路径字符串不是规范形式），回到对应任务修正，不改本测试。

- [ ] **Step 3: 全量回归并提交**

Run: `cd server && go vet ./... && go test ./... && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...`
Expected: 全部 PASS，Windows 交叉编译成功。

```bash
git add server/internal/builtin/integration_test.go
git commit -m "test(server): 内置数据编辑跨升级保留与恢复默认的端到端测试"
```

---

### Task 9: sidecar 启动时调用 Apply

**前置条件（开始前检查）：** P1b 在 `server/cmd/sidecar/main.go` 打开数据库并执行迁移的任务已合并。

Run: `grep -n "db.Migrate" server/cmd/sidecar/main.go`
Expected: 有一处匹配。**没有匹配就停止本任务并报告 BLOCKED**——不要自己往 main.go 加 `db.Open`，那是 P1b 的改动范围，两边同时加会冲突。

**Files:**
- Modify: `server/cmd/sidecar/main.go`（在 `db.Migrate` 的错误处理之后插入一段；加一个 import）
- Test: `server/cmd/sidecar/main_test.go`（末尾追加一个测试）

**Interfaces:**
- Consumes: Task 6 `builtin.Apply(conn *sql.DB) error`；P1b 在 `run()` 里打开的 `*sql.DB`（下文按变量名 `conn` 书写，若 P1b 用了别的名字按实际替换）和数据库文件 `<data-dir>/data.db`（spec S1；若 P1b 用了别的文件名，测试里按实际替换）
- Produces: 首次启动在打印 `READY` 前完成内置数据导入；导入失败时 sidecar 以非 0 退出、不打印 `READY`

- [ ] **Step 1: 写失败的测试**

在 `server/cmd/sidecar/main_test.go` 末尾追加（若该文件尚未 import `github.com/james/huabu_studio/apps/api/internal/db`，在 import 块里加上）：

```go
// 首次启动在打印 READY 之前就已把内置模型和配置导入 data.db（spec M9）。
func TestBuiltinSeedImportedOnStartup(t *testing.T) {
	bin := buildSidecar(t)
	dataDir := t.TempDir()
	startSidecar(t, bin, dataDir)
	conn, err := db.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var models, settings int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM model_configs`).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM settings WHERE "key" IN ('base_domain', 'canvas_light_control', 'canvas_multi_angle', 'canvas_postprocess_config', 'canvas_script_config', 'gen_image_prompt_presets')`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if models != 47 || settings != 6 {
		t.Fatalf("models=%d settings=%d, want 47/6", models, settings)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./cmd/sidecar/ -run TestBuiltinSeedImportedOnStartup -v`
Expected: FAIL，`models=0 settings=0, want 47/6`。

- [ ] **Step 3: 接线**

在 `server/cmd/sidecar/main.go` 的 `run()` 里，紧跟 `db.Migrate(conn)` 的错误处理之后、`net.Listen` 和打印 `READY` 之前插入：

```go
	// 内置模型和画布配置（spec M9/M11）：首次启动导入，升级后按 model_id 字段级合并。
	if err := builtin.Apply(conn); err != nil {
		return fmt.Errorf("导入内置数据: %w", err)
	}
```

import 块加一行：

```go
	"github.com/james/huabu_studio/apps/api/internal/builtin"
```

只做这两处改动，不调整 main.go 的其他代码。

- [ ] **Step 4: 运行测试，确认通过；全量回归**

Run: `cd server && go test ./cmd/sidecar/ -v && go vet ./... && go test ./...`
Expected: 新测试 PASS；`TestHandshakeAndPing`、`TestExitsWhenStdinCloses`、`TestFailsWithoutReadyWhenDataDirUnusable` 等原有测试仍 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/cmd/sidecar/main.go server/cmd/sidecar/main_test.go
git commit -m "feat(server): sidecar 启动时导入并合并内置数据"
```

---

## 给 P2b / P4 的约定

- **P2b**：Skill、Skill 分类、Agent 工具、Agent 大模型、AI 角色库各自作为新数据集写进 `builtin_seed_state`（`dataset` 用各自的名字），种子文件加进 `scripts/sync-seed.sh` 的列表和 `seeddata/` 的 `//go:embed` 行，`TestEmbeddedSeedMatchesRepoSeed` 的文件列表同步扩充。`Apply` 会在升级时**删除**新版种子里没有的模型、并可能**改写**模型主键：若 `agent_tools.source_model_id` 声明外键，必须带 `ON DELETE CASCADE`（或 `SET NULL`）和 `ON UPDATE CASCADE`，否则合并会因外键失败、sidecar 无法启动；更稳妥的做法是 P2b 的数据集在 `applySeed` 的同一事务里、`applyModels` 之后合并。`agent_backbone_models.model_name` 引用的是 model_id 字符串，不受主键变化影响。
- **P4**：设置页的模型中心和各配置页只调用 `builtin.Service`；`GET /admin/settings` 不得原样返回全部 settings（含 `upstream_api_key`），`PUT /admin/settings` 无白名单的写法不得移植；参考仓后台的模型 Create / Delete 路由不移植。参考仓后台的后处理配置页保存的是 `upscaleModels`（列表）和 `whiteModelModel`，而内置种子只有旧字段 `upscaleModel`，本计划只开放 `upscaleModel`——P4 若要沿用列表形态，需要先扩充 `settingPolicies[SettingCanvasPostprocess]` 并补测试。表单提交时只发改动过的字段路径；`ValidationError.Path` 用于把错误定位到控件。

## P2a 完成标准

- `cd server && go vet ./... && go test ./...` 全部通过，没有任何测试被 skip。
- `cd server && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...` 成功。
- 新库首次启动后 `model_configs` 47 行（id 与生产一致）、`settings` 6 个内置 key，`user_overrides` 全部为 `'[]'`，`builtin_seed_state` 有一行 `model_center`。
- `builtin` 包的导出 API 与 Task 7 的 Interfaces 一致。

## 自检记录

- **Spec 覆盖**：M1（同结构 `model_configs`、JSON 列 TEXT，Task 1/5）· M2（全局 `base_domain`，D9、Task 4/7）· M3（Key 存 settings、合并不碰，D10、Task 6 `TestApplyLeavesNonSeedSettingsAndKeepsBaseDomainOverride`）· M4（配置存 settings 一 key 一 JSON，Task 4/6/7；Agent 大模型表属 P2b）· M5（提示词正文本地可见可编辑，Task 4 的 `presets[*].prompt` 等）· M7（不做导入导出：本计划无任何导入导出入口，种子只在构建期嵌入）· M8（只能改不能增删：Service 无 Create/Delete，`[key]` 段不能新增预设，Task 3/7）· M9（go:embed、首次导入，Task 2/6/9）· M10（白名单、只读技术字段、后端校验、恢复默认，Task 4/7）· M11（按 model_id 合并、字段级覆盖记录，Task 1/6/8）。S2（数据目录迁移）不涉及本计划的数据结构：`user_overrides` 与 `builtin_seed_state` 都在 data.db 内，随库一起迁移。
- **占位符扫描**：无 TBD/TODO；Task 9 中“按实际替换”仅指 P1b 尚未确定的变量名和文件名，已给出默认值（`conn`、`data.db`）和检查命令。
- **类型一致性**：`modelParts`、`Catalog` 定义在 Task 2，`ValidationError`、`applyEdits`、`overlay` 在 Task 4，`modelRow`、`settingRow`、`ErrNotFound` 在 Task 5，后续任务按 Interfaces 中的签名使用。各任务代码已在仓库副本上按依赖子集逐一编译并通过测试。
- **Review Focus**：5 条均有对应测试，分别在 Task 6（3 条）、Task 7（2 条）、Task 8（第 3 条的升级部分）。
