# P1c 生成链路 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 sidecar 能完整跑通一次 AI 生成：画布提交 → SQLite 持久化队列 → 调上游（同步出图 / 异步轮询）→ 产物立即落本地（失败重试 3 次，仍失败标「未保存到本地」可手动重试）→ outbox 投递回写画布节点并发 `canvas_changed`；进程重启后在途任务立即续跑；同时提供生成历史、模型列表接口。

**Architecture:** 从参考仓原样搬 `upstream`、`generation`（去掉 drama / 团队额度 / 对账）、`canvasgen`、`canvasinputs`、`mediaprobe`、`lifecycle`（只要 instance.go + lifecycle.go），计费换空实现。MySQL 方言集中改写在 `generation/repository.go`、`outbox.go`、`finish.go`，新表放 `0002_generation.sql`。leader 单例组拆成启动时直接起的 goroutine，启动先把上个进程的租约判为过期。输入媒体公网 URL（spec S12）只保留接口位 `generation.InputMediaSigner`，默认行为照搬参考仓签名，但发现最终请求里仍是本机地址时在调上游之前明确失败；data_uri / base64 / multipart 的参考图改为直接读本地存储。与 P1b 的依赖全部收敛成接口（`generation.AssetStorage`、`generation.AssetRecorder`、`generation.ObjectReclaimer`、签名器、通知器），P1b 合并前用带 build tag 的临时 shim 独立编译测试。

**Tech Stack:** Go 1.26（module `github.com/james/huabu_studio/apps/api`）、`modernc.org/sqlite` v1.60.1、`database/sql`、`github.com/go-chi/chi/v5`、`net/http/httptest`。不新增任何依赖（`server/go.mod`、`server/go.sum` 本计划不改）。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（X3、S5、S7、S12、M2、M3、E3、F5）；事实依据 `docs/superpowers/plans/p1-research.md` 第 1、3、4、6 节；house style 见 `docs/superpowers/plans/2026-10-06-p1a-sqlite-data-layer.md`。

**参考仓：** `/Users/james/github/huabu_studio`，commit `fea92ab9ad58ac1aa6752855999c221fcb460d89`（本计划涉及的目录在工作区 HEAD 与该 commit 逐字相同），下文简写 `REF=/Users/james/github/huabu_studio/apps/api/internal`。下文所有「参考仓 file:line」都指这个 commit。

## Global Constraints

- spec X3：「积分、估价、价格说明全部删除；models.json 的 pricing 字段保留不用」→ 计费一律走 `billing.NoopService`（永远成功、不落库）；`/models/estimate*` 恒返回 0；`/models` 列表清空 `adminNote`、`priceNote`。
- spec S5：「生成完成后立即下载到本地；下载失败自动重试 3 次，仍失败则保留上游地址，节点显示“未保存到本地”和重试按钮」。
- spec S7：「照搬参考仓的任务表和恢复逻辑（upstream_called_at、upstream_task_id、ResumeOrphanedTasks），去掉多实例租约；启动时扫描未完成任务，已发出的继续轮询，未发出的重新提交」。`lease_version` 保留（所有 CAS SQL 都带它）。
- spec S12（待定）：「P1 先不实现上传，带参考媒体的生成按现状保留 URL 签名逻辑的接口位」→ **不得实现任何上传到公网主机的代码**。
- spec M2/M3：上游地址取 `settings.base_domain`、Key 取 `settings.upstream_api_key`，每次请求现读；为空时报明确中文错误（research §6：「`settings.base_domain` 未预置，P1c 在它为空时要给出明确错误」）。
- 包路径与参考仓一致：`internal/upstream`、`internal/generation`、`internal/canvasgen`、`internal/canvasinputs`、`internal/mediaprobe`、`internal/lifecycle`、`internal/billing`。类型改名规则同 P1a：`MySQLRepository` → `SQLiteRepository`、`NewMySQLRepository` → `NewSQLiteRepository`；其余导出名、签名、JSON tag 与参考仓一致，本计划明确列出的删改除外。
- SQLite 约束（P1a 最终审查，research §6，逐条遵守）：
  - 除 `ReadOnly` 事务外所有 `Begin` 一开始就拿库级写锁；**持有事务时不得经 `r.db`（而非 `tx`）写库**（自死锁 10 秒后 `SQLITE_BUSY`）；**事务内不得有网络 I/O 或大文件读写**。
  - 不支持 `UPDATE … LIMIT` → `WHERE id IN (SELECT … LIMIT ?)`；`LAST_INSERT_ID(expr)` → `UPDATE … RETURNING`；`IF` → `COALESCE/NULLIF` 或 `iif`；`JSON_SEARCH` → `json_tree`；`RowsAffected` 是匹配行数。
  - 时间：SQL 里取当前时间用 `CURRENT_TIMESTAMP`，相对时间用 `datetime('now', ?)`，参数由 `sqlSecondsOffset(n)` 生成（如 `"-90 seconds"`）；Go 侧时间参数必须经 `db.Time`，绝不直接绑定 `time.Time`。
  - **JSON 参数一律传 `string`，不得传 `[]byte`**（BLOB 会被 json 函数当 JSONB）。**读 JSON 列不得直接 `Scan` 进 `json.RawMessage`**（modernc 返回 `string`，`database/sql` 不做该转换，已实测报错），先扫进 `[]byte` 或 `string`。
  - `UPDATE … RETURNING` 没匹配到行时 `QueryRow().Scan` 返回 `sql.ErrNoRows`（已实测），即「CAS 没抢到」。
  - 新表 `INTEGER PRIMARY KEY AUTOINCREMENT`（`generation_tasks.id` 进对象 key `gen/task-<id>-<i>`）+ `updated_at` 的 `AFTER UPDATE` 触发器；新表放新迁移文件，不改 `0001_init.sql`。
- 代码注释用中文；从参考仓复制的注释若描述的 MySQL / 多实例 / 计费行为已不成立，就地改写。
- **shim 规则**：P1b 尚未合并时，`server/internal/assets` 与 `server/internal/http` 里 P1b 才会提供的符号由 Task 2 的两个 `//go:build p1cshim` 文件临时提供；**本计划所有 `go build` / `go vet` / `go test` 命令都带 `-tags p1cshim`**。执行任一任务前先检查：若 `server/internal/assets/storage.go` 与 `server/internal/http/context.go` 都已存在（P1b 已合并进你的基线），跳过 Task 2 的 shim 步骤、删除已存在的 shim 文件，并把所有命令里的 `-tags p1cshim` 去掉。
- 不改动 `server/internal/{db/sqlite.go,db/dbtest,settings,models,projects,canvasstate,localauth}/**`、`src-tauri/**`、`web/**`、`seed/**`。

## Review Focus

1. **应用重启时有在途任务，且上个进程的心跳在库里仍「新鲜」** → 启动后立即续跑，不等 90 秒：已拿到 `upstream_task_id` 的继续轮询、绝不重新提交；`upstream_called_at` 为空的重新排队执行恰好一次。（Task 7 `TestStartBackgroundExpiresLeasesThenRecovers`、`TestExpirePreviousLeasesMakesOldHeartbeatsStale`；Task 9 `TestRestartResumesInFlightTasks`）
2. **产物下载遇到网络抖动 / 上游临时 5xx** → 自动重试 3 次后成功则正常落本地；仍失败则任务照常成功、保留上游地址、节点 `settings.unsaved=true`；点重试成功后节点内容换成 `media://`、标记清掉，且 URL 含 `&` 时也改得对。（Task 5 `TestPersistOutputsRetriesRemoteDownloadThreeTimes`、`TestPersistOutputsGivesUpAfterThreeRetries`、`TestApplyRepersistRewritesNodeAndClearsFlag`、`TestRepersistHandlerAppliesToCanvas`）
3. **首次启动还没填 base_domain / API Key 就点生成** → 节点失败原因是明确中文提示，且没有调用上游。（Task 6 `TestPrepareExecutionMissingBaseDomain`、`TestPrepareExecutionMissingAPIKey`）
4. **连了本地参考视频/图片去跑只认 URL 的模型（S12 未定）** → 在调用上游之前失败并说明原因；走 data_uri / base64 / multipart 的模型照样能用本地参考图（直接读盘，不经 HTTP）。（Task 6 `TestPrepareExecutionRejectsLocalMediaInURLPayload`、`TestPrepareExecutionDataURIReadsLocalFile`、`TestPrepareExecutionEditsSpoolsLocalFile`）
5. **后台 worker 与 HTTP 写请求同时写库** → 不出现 `SQLITE_BUSY` 或自死锁：带 `inputDuration` 的模型提交（写事务内）不得经 `r.db` 写库；12 个并发领取恰好一个赢家。（Task 3 `TestResolveInputDuration/未命中放行且绝不回填`；Task 4 `TestSubmitWithInputDurationModelDoesNotSelfDeadlock`、`TestClaimQueuedConcurrentExactlyOneWins`）

---

## 并发执行安排

每个任务在独立 worktree 的独立分支上执行；同一批内的任务互不依赖，**可同时开工**。一批全部完成后合并进 P1c 集成分支（例如 `p1c`），下一批从集成分支切出。与其他计划（P1b、P1d、P2a、P3a、P4、P5、P6、P8）的冲突统一在最终合并时解决。

| 批次 | 任务 | 依赖 | 说明 |
|---|---|---|---|
| 第 0 批 | **Task 1** 叶子包（billing 空实现、upstream、lifecycle、mediaprobe、canvasinputs） | 无 | 原样复制为主 |
| 第 0 批 | **Task 2** 迁移 `0002_generation.sql` + P1b 临时 shim | 无 | |
| 第 1 批 | **Task 3** generation / canvasgen 骨架（去依赖、可编译） | Task 1、2 | 机械改动，串行瓶颈，尽快做 |
| 第 2 批 | **Task 4** SQLite 方言改写 + 10 个真实库测试 | Task 3 | |
| 第 2 批 | **Task 5** S5：产物下载重试 + 手动重试保存 | Task 3 | |
| 第 2 批 | **Task 6** S12 接口位 + 本地参考图直读 + 上游配置中文错误 | Task 3 | |
| 第 2 批 | **Task 7** 启动恢复与后台循环 | Task 3 | |
| 第 2 批 | **Task 8** HTTP：`/canvas-tasks*`、`/tasks*`、`/models*` | Task 3 | |
| 第 3 批 | **Task 9** 装配函数 `wireGeneration` + 端到端测试 | 第 2 批全部 | 不依赖 P1b |
| 第 4 批 | **Task 10** 接入 `main.go` / `router.go`，删除 shim | Task 9 **且 P1b 已合并** | 唯一必须等 P1b 的任务 |

**第 2 批内部的共享文件**（同批任务改同一文件的不同位置，git 一般能自动合并；若冲突按「各取各的块」解决）：

- `server/internal/generation/service.go`：Task 5 只改 `persistOutputs` 里调用 `persistSlot` 的一行；Task 6 只改 `Service` 结构体（加一个字段）和 `Submit` 直连路径取 Key / base_domain 与插值后的几行。Task 4、7、8 不改它。
- 其余文件第 2 批内各归一个任务（见各任务 **Files**）。`repository.go`、`outbox.go` 只归 Task 4；`finish.go` 在 Task 3 改完后不再改；`repersist.go` 只归 Task 5；`image_inputs.go`、`execution_plan.go` 只归 Task 6。

**与其他计划共享的文件与归属**（冲突在最终合并时解决）：

| 文件 | 归属 | 说明 |
|---|---|---|
| `server/internal/http/router.go` | 本计划只有 Task 10 改 | 加 `Deps.Generation`、`Deps.GenerationRepersist` 两个字段和两行注册调用；P1b/P1d 等也会改它 |
| `server/cmd/sidecar/main.go` | 本计划只有 Task 10 改 | 只加一段装配；P1b、P8 也会改 |
| `server/internal/db/migrations/0002_generation.sql` | 本计划 Task 2 新建 | 其他计划若也用 `0002_` 前缀不冲突：迁移器按完整文件名记录、按字典序执行，本文件的表与其他计划的表无外键依赖；首个正式版发布前如需统一编号可改名 |
| `server/go.mod`、`server/go.sum` | 本计划不改 | |
| `server/internal/assets/p1c_shim.go`、`server/internal/http/p1c_shim.go` | Task 2 新建、Task 10 删除 | 带 `//go:build p1cshim`，默认构建不参与编译，与 P1b 的真实定义不会重复声明 |

**本计划拥有、其他计划只消费的包**（其他计划不要再复制一份；若已逐字复制，内容相同则合并无冲突）：`internal/upstream`、`internal/generation`、`internal/canvasgen`（含 `image_preset_config.go`、`multi_angle_config.go`、`light_control_config.go`、`postprocess_config.go` 四个纯配置文件——`canvasgen.Enqueue` 和预设解析器编译需要它们，P1d 的预设/多角度/打光接口直接调用其 `Load*Config`）、`internal/canvasinputs`、`internal/mediaprobe`（P1b 的上传接口会 import 它）、`internal/lifecycle`、`internal/billing/{noop,actor,scope}.go`（P5/P6 用 `billing.NoopService`、`billing.WithActor`）。

## 对其他计划接口的假设（契约）

以参考仓同名接口为准；P1b 若改了名，Task 10 按编译错误对接，不改本计划其余代码。

- **assets（P1b）**：`assets.PutObjectInput{Key, ContentType, Body, Reader, Size}` 及其 `Content()`、`assets.StoredObject{URL, Size}`、`assets.AssetRecord`（字段同参考仓 `assets/repository.go:9-22`）、`assets.MediaProtocol = "media://"`、`assets.WalkStrings`。本地存储实现满足 `generation.AssetStorage`（`PutObject`、`DeleteObject`、`SignedURL`、`NewKey`、`StaticKey`、`ExtractKey`、`GetObject`）；`ExtractKey` **只**认 `media://` 前缀和 sidecar 自己签出的本地 URL（research §1 要求），绝不把普通字符串当 key。资产仓库满足 `generation.AssetRecorder`（`Insert`、`FindByTaskSlot`、`InsertSlotTx`、`ListForTask`、`DeleteForTask`、`Get/SetDurationMsByObjectKey`），`InsertSlotTx` 按 `uk_assets_task_output` 幂等（research §6：`INSERT … ON CONFLICT DO NOTHING RETURNING id`，无返回再 `SELECT`）。
- **library（P1b）**：`library.IsStillReferenced(q library.Querier, objectKey string) (bool, error)`、`library.OrphansRepository` 的 `Mark(tx library.Execer, objectKey string, userID, sizeBytes int64) error`；Task 10 用它们写 `generation.ObjectReclaimer` 适配器。
- **http（P1b）**：`userIDFromContext(ctx) int64`、`withUserID(ctx, id) ctx`、`writeError(w, status, msg)`、`decodeJSON(r, v) error`；`*MediaSigner` 有 `Sign(string) string`、`SignJSON(json.RawMessage) json.RawMessage`、`Canonicalize(string) string`；本地身份中间件恒写 userID=1。
- **canvas_changed（P1b 决定 Hub 形态）**：某个对象实现 `NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary)`；nil 时回写照常、不广播。
- **users**：本计划不 import P1b 的 `users` 包，`generation.UserStorage` 由 Task 9 的 `sqliteUserStorage` 直接读写 `users.storage_used_bytes / storage_quota_bytes`（P1a 的 `0001_init.sql` 已建，本地用户配额 1 PiB）。

---

## 文件结构

```
server/internal/
  billing/
    noop.go, noop_test.go          新写：Result、ErrInsufficientBalance、ErrTaskNotFound、NoopService
    actor.go, actor_test.go        取自参考仓 quota_gate.go 的 WithActor / ActorFromContext
    scope.go, scope_test.go        原样复制（WithDramaScope 等）
  upstream/                        原样复制（client.go、spill.go + 4 个测试）
  lifecycle/                       原样复制 instance.go、lifecycle.go + 2 个测试（不要 singleton.go）
  mediaprobe/                      原样复制 probe.go + 测试 + testdata/（桌面端无 ffprobe 时返回 ErrProbeUnavailable）
  canvasinputs/                    原样复制（3 个源文件 + 4 个测试）
  assets/p1c_shim.go               临时（build tag p1cshim），Task 10 删除
  http/
    p1c_shim.go                    临时（build tag p1cshim），Task 10 删除
    generation_iface.go            mediaRefSigner 窄接口
    canvasgen_handlers.go(+test)   移植：/canvas-tasks、/canvas-tasks/{id}、/canvas-tasks/by-node
    canvasgen_node_status_test.go  原样复制
    generation_handlers.go         移植：GET/DELETE /tasks
    generation_apply_handlers.go(+test) 移植：POST /tasks/{id}/apply-to-canvas
    runtime_model_handlers.go      新写：GET /models、POST /models/estimate(-batch)（恒 0）
    generation_routes.go(+test)    GenerationRoutes + registerGenerationRoutes；test 覆盖以上三组接口
    generation_repersist_handlers.go(+test) 新写：POST /tasks/{id}/repersist（S5 手动重试）
  db/
    migrations/0002_generation.sql generation_tasks、generation_sink_outbox
    generation_schema_test.go
  generation/                      除 reconcile.go 外全部移植；新增：
    persist_retry.go(+test)        S5 下载重试
    input_media.go(+test)          S12 接口位、本地媒体直读、可达性检查
    upstream_config.go             上游 Key / base_domain 读取与中文错误
    startup.go(+test)              ExpirePreviousLeases、StartBackground
    sqlite_helpers_test.go         真实库测试用替身
  canvasgen/                       全部原样复制；新增 repersist_apply.go(+test)
server/cmd/sidecar/
  gen_wiring.go, gen_wiring_test.go  装配 + 端到端测试（Task 9）
  gen_library_adapter.go             P1b library → generation.ObjectReclaimer（Task 10）
  main.go                            Task 10 加装配段
```

---

## MySQL → SQLite 方言清单（参考仓 file:line → 改写 → 负责任务）

| 参考仓位置 | MySQL 写法 | SQLite 改写 | 任务 |
|---|---|---|---|
| generation/repository.go:163-168 `Create` | `[]byte(task.Sink)`、`[]byte(task.Input)`；drama/actor 三列 | `string(...)`；INSERT 去掉 `drama_project_id, drama_segment_id, actor_user_id` | 3 |
| repository.go:185-191 `CreateTx` | 同上 | 同上 | 3 |
| repository.go:200、233、301、397、479、635 | `IFNULL(...)` | SQLite 支持，不改 | — |
| repository.go:207、244、319、420、487-488 | `Scan(&t.Input)`（MySQL 驱动给 `[]byte`，可直接赋给 `json.RawMessage`） | modernc 给 `string`，扫 `*json.RawMessage` 直接报错（已实测）→ 先扫进 `[]byte` 再赋值 | 3 |
| repository.go:236 `LatestByCanvasNode` | `sink->>'$.kind'`、`sink->>'$.nodeId'` | `json_extract(sink, '$.kind')`、`json_extract(sink, '$.nodeId')` | 4 |
| repository.go:287 `List` | `JSON_SEARCH(input, 'one', ?) IS NOT NULL` | `EXISTS (SELECT 1 FROM json_tree(generation_tasks.input) jt WHERE jt.type = 'text' AND jt.atom LIKE ?)` | 4 |
| repository.go:297-306 `List` 两阶段取数 | 子查询 `LIMIT ? OFFSET ?` | SQLite 支持，不改 | — |
| repository.go:343-350 `DramaScope` | — | 删除（drama 不移植） | 3 |
| repository.go:400、405 `ListProcessing` | `DATE_SUB(NOW(), INTERVAL ? SECOND)` | `datetime('now', ?)`，参数 `sqlSecondsOffset(-n)` | 4 |
| repository.go:447 `Heartbeat` | `NOW()`；同秒 0 changed rows 的补查 | `CURRENT_TIMESTAMP`；SQLite 报匹配行数，补查保留（无害），注释改写 | 4 |
| repository.go:467 `MarkRunningDirectTx` | `NOW()` | `CURRENT_TIMESTAMP` | 4 |
| repository.go:512 `RunningCountByModel` | `DATE_SUB` | `datetime('now', ?)` | 4 |
| repository.go:558-570 `ClaimQueued` | `NOW()`；`lease_version = LAST_INSERT_ID(lease_version + 1)` + `res.LastInsertId()` | `CURRENT_TIMESTAMP`；`lease_version = lease_version + 1 … RETURNING lease_version`，`QueryRow().Scan`，`sql.ErrNoRows` = 没抢到 | 4 |
| repository.go:578-590 `ClaimStaleRunning` | 同上 + `DATE_SUB` | 同上 + `datetime('now', ?)` | 4 |
| repository.go:598 `MarkUpstreamCalled` | `NOW()` | `CURRENT_TIMESTAMP` | 4 |
| repository.go:661 `MarkRetryPending` | `DATE_ADD(NOW(), INTERVAL ? SECOND)` | `datetime('now', ?)`，参数 `sqlSecondsOffset(+n)` | 4 |
| repository.go:679-682 `PromoteDueRetries` | `UPDATE … WHERE … LIMIT ?`、`NOW()` | `WHERE id IN (SELECT id … ORDER BY id LIMIT ?)`、`CURRENT_TIMESTAMP` | 4 |
| repository.go:694、714、735 | `DATE_SUB` | `datetime('now', ?)` | 4 |
| repository.go:789-828 `ListExternalOutputs`、`repersistTimeLayout` | `DATE_SUB(?, INTERVAL ? DAY)`、`JSON_UNQUOTE(JSON_EXTRACT())`、绑定 `time.Time` | 删除（S5 改为手动重试，不再有补搬 worker） | 4 |
| repository.go:833 `UpdateOutput` | `IF(?='', output_kind, ?)`、`[]byte(output)` | `COALESCE(NULLIF(?, ''), output_kind)`、`string(output)` | 4 |
| repository.go:838-843 `MarkRepersistAttempt` | `JSON_SET` | 删除（同上） | 4 |
| generation/outbox.go:61-62 `InsertOutboxTx` | `[]byte(sink), payload` | `string(sink), string(payload)` | 4 |
| outbox.go:71-72、97-98 | `NOW()` | `CURRENT_TIMESTAMP` | 4 |
| outbox.go:94-95、106 `claimOutboxDue` | `DATE_ADD(NOW(), INTERVAL ? SECOND)`、`LAST_INSERT_ID(delivery_version + 1)` + `LastInsertId()` | `datetime('now', ?)`、`RETURNING delivery_version` | 4 |
| outbox.go:161 `failOutbox` | `DATE_ADD` | `datetime('now', ?)` | 4 |
| generation/finish.go:41-43 | `[]byte(out.output)` | `string(out.output)` | 3 |
| finish.go:92-101 | 读 drama/actor 三列、`WithDramaScope`、`WithActor` | 只读 `user_id, frozen_cents, sink` | 3 |
| generation/queue_stats.go:29-36 | `SUM(CASE …)` | SQLite 支持，不改 | — |
| generation/service.go:1233 `Delete` | `DELETE FROM assets WHERE …` | 不改 | — |
| generation/reconcile.go:49、78 | `HAVING` 别名 | 不移植 | — |
| 各 `*_db_test.go` | `CONCAT(…, UUID(), …)` 建用户、`NOW()`、`DATE_SUB/DATE_ADD … INTERVAL`、`credit_grants` | 用预置的本地用户 `id=1`；`CURRENT_TIMESTAMP`、`datetime('now','-1 hours')` 等；去掉积分桶 | 4 |

---

## 最终表结构（由参考仓迁移 0002、0053、0055、0080、0081、0082、0103、0105 推导）

`generation_tasks` 参考仓最终列：`id, user_id, project_id, model_id, status, upstream_task_id, input, output, frozen_cents, cost_cents, audit_flag, error, created_at, updated_at`（0002）、`drama_project_id, drama_segment_id`（0053）、`owner_instance, heartbeat_at` + `idx_gen_tasks_lease`（0055）、`sink, upstream_called_at, attempt, next_retry_at` + `idx_gt_queue`、`idx_gt_retry`（0080）、`lease_version`（0081）、`output_kind`（0103）、`actor_user_id`（0105）。0004（`minio://` 改名）、0057/0082（注释）、0103 的存量回填在新库上无意义，不移植。

桌面端**去掉** `drama_project_id`、`drama_segment_id`、`actor_user_id`（drama 与团队额度不移植），其余全部保留。`generation_sink_outbox` 来自 0081，原样保留全部列与两个键。JSON 列加 `typeof(...) = 'text'` 约束（与 P1a 的 `0001_init.sql` 一致）。完整 DDL 见 Task 2 Step 3。

## 移植的测试

- 原样复制（只做类型改名/去 drama 等机械改动）：`upstream` 4 个、`lifecycle` 2 个、`mediaprobe` 1 个、`canvasinputs` 4 个、`billing/scope_test.go`、`canvasgen` 全部 12 个、`generation` 非库测试 22 个（`billing_extras`、`edits_e2e`、`heartbeat_lost`、`image_inputs_spool`、`image_inputs`、`input_limits`、`output_kind`、`persist_local`、`polling`、`preset_prompt`、`queue_worker`、`real_person_inputs`、`recover`、`service`、`sink`、`storage_quota`、`submit_freeze`、`submit_split`、`tmpdir`、`uischema_inputs`、`urls`，以及改写的 `input_duration`）、`http/canvasgen_handlers_test.go`、`http/canvasgen_node_status_test.go`。
- 改写到 `dbtest`（参考仓连真实 MySQL 的 `generation/*_db_test.go`，共 12 个，移植 10 个）：`lease`、`retry`、`outbox`、`latest_by_node`、`output_kind`、`finish`、`recover`、`tx_submit`、`delete`、`persist`。
- 不移植：`generation/reconcile_db_test.go`（对账不移植）、`generation/actor_db_test.go`（团队额度不移植）、`billing` 除 `scope_test.go` 外的全部测试（依赖 MySQL 账本或团队）。`generation/repersist_test.go` 由 Task 3 原样复制（此时补搬 worker 代码仍在、可通过），Task 5 删掉 worker 时整体重写为手动重试的用例。

## 删除的依赖与精确改动（汇总，细节在各任务）

| 被删依赖 | 位置 | 改动 | 任务 |
|---|---|---|---|
| `library`（孤儿回收） | generation/service.go:22、111、144、157、1237-1266 | 字段与参数 `orphans library.OrphansRepository` → `reclaimer ObjectReclaimer`；`Delete` 改调 `reclaimer.ReclaimTx` | 3 |
| `mediaprobe`（ffprobe） | generation/service.go:24、127-129、165；input_duration.go:17-19、69-85 | 删 `durationProbe` 字段；`resolveInputDuration` 只走 `assets.duration_ms` 快路径，查不到放行（桌面端不计费，且原回填会在写事务内经 `r.db` 写库自死锁） | 3 |
| drama 归属 | repository.go:29-30、136-138、145-151、166、168、188、190、341-350；service.go:268-271、361、445-446、455、467；tx_submit.go:112-113、121；finish.go:92、96 | 删字段、删接口方法、删 INSERT 列、删 `WithDramaScope` | 3 |
| 团队 actor | repository.go:31-34、168、190；service.go:447；tx_submit.go:115；finish.go:97-101 | 同上，删 `ActorUserID` 与 `WithActor` | 3 |
| 对账 | generation/reconcile.go | 不复制 | 3 |
| leader 单例 | lifecycle/singleton.go、cmd/server/main.go:653-692 | 不复制；改为 `generation.(*Service).StartBackground` 直接起 goroutine | 1、7 |
| 补搬 worker | generation/repersist.go:13-118、cmd/server/main.go:669 | 删 worker，改为 `RepersistTask` 单任务手动重试 | 5 |
| 真实计费 | cmd/server/main.go:151、873-905 的 `billingAdapter` | `billing.NoopService` + Task 9 的 `noopGenBilling` 适配器 | 1、9 |
| `users.UpstreamKeyResolver` | cmd/server/main.go:194 | Task 9 `settingsUpstreamKey` 只读 `settings.upstream_api_key` | 9 |
| `teams.ErrQuotaExceeded`、余额不足提示 | http/canvasgen_handlers.go:14、72-81 | 删除分支 | 8 |

---

### Task 1: 叶子包（billing 空实现、upstream、lifecycle、mediaprobe、canvasinputs）

**Files:**
- Create: `server/internal/billing/noop.go`、`server/internal/billing/actor.go`、`server/internal/billing/scope.go`（复制）
- Create: `server/internal/upstream/{client.go,spill.go}`（复制）、`server/internal/lifecycle/{instance.go,lifecycle.go}`（复制）、`server/internal/mediaprobe/probe.go`（复制）、`server/internal/canvasinputs/{inputs.go,mentions.go,resolution.go}`（复制）
- Test: `server/internal/billing/{noop_test.go,actor_test.go,scope_test.go}`、`server/internal/upstream/*_test.go`、`server/internal/lifecycle/*_test.go`、`server/internal/mediaprobe/probe_test.go` + `testdata/`、`server/internal/canvasinputs/*_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `billing.Result{NewBalanceCredits int64}`、`billing.ErrInsufficientBalance`、`billing.ErrTaskNotFound`
  - `billing.NoopService`、`billing.NewNoopService() *NoopService`，方法：`Freeze(ctx, userID, taskID, amount int64, note string) (Result, error)`、`Settle(ctx, userID, taskID, frozen, actual int64, note string) (Result, error)`、`Refund(ctx, userID, taskID, frozen int64, note string) (Result, error)`、`FreezeTx(ctx, *sql.Tx, userID, taskID, amount int64, note string) (Result, error)`、`SettleTx(ctx, *sql.Tx, userID, taskID, frozen, actual int64, note string) (Result, error)`、`RefundTx(ctx, *sql.Tx, userID, taskID, frozen int64, note string) (Result, error)`
  - `billing.WithActor(ctx, actorUserID int64) context.Context`、`billing.ActorFromContext(ctx) int64`、`billing.WithDramaScope(ctx, projectID, segmentID int64) context.Context`
  - `upstream.*`、`lifecycle.InstanceID()`、`lifecycle.Go(func())`、`mediaprobe.ProbeDurationMs`、`mediaprobe.ErrProbeUnavailable`、`canvasinputs.*`：与参考仓逐字相同

- [ ] **Step 1: 复制原样文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cd /Users/james/github/miaoju-desktop/server/internal
mkdir -p upstream lifecycle mediaprobe canvasinputs
cp $REF/upstream/*.go upstream/
cp $REF/lifecycle/instance.go $REF/lifecycle/instance_test.go $REF/lifecycle/lifecycle.go $REF/lifecycle/lifecycle_test.go lifecycle/
cp $REF/mediaprobe/probe.go $REF/mediaprobe/probe_test.go mediaprobe/
cp -R $REF/mediaprobe/testdata mediaprobe/
cp $REF/canvasinputs/*.go canvasinputs/
cp $REF/billing/scope.go $REF/billing/scope_test.go billing/
```

- [ ] **Step 2: 确认复制的包只依赖标准库**

Run: `cd /Users/james/github/miaoju-desktop/server && grep -rn '"github.com/james/huabu_studio' internal/upstream internal/lifecycle internal/mediaprobe internal/canvasinputs internal/billing/scope.go`
Expected: 无输出。

在 `server/internal/mediaprobe/probe.go` 的包注释末尾追加一段（其余不改）：

```go
// 桌面端不打包 ffprobe（spec 未包含 ffmpeg）：找不到 ffprobe 时 ProbeDurationMs 返回
// ErrProbeUnavailable，调用方（P1b 的上传接口）按「时长未知」处理，不得因此拒绝上传。
// generation 的 inputDuration 已不再调用本包（见 generation/input_duration.go）。
```

- [ ] **Step 3: 写失败的测试**

`server/internal/billing/noop_test.go`：

```go
package billing

import (
	"context"
	"database/sql"
	"testing"
)

// 以下三个接口逐字对应参考仓的计费窄接口：agent.AgentBilling（agent/backbone.go:197）、
// http 的 chatBilling（chat_handlers.go:53）、canvasscript 的 billingService（service.go:153-158）。
// NoopService 必须同时满足它们，P5/P6 才能直接把它传进去。
type agentBilling interface {
	Freeze(ctx context.Context, userID, taskID, amountCredits int64, note string) (Result, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (Result, error)
}

type canvasScriptBilling interface {
	FreezeTx(ctx context.Context, tx *sql.Tx, userID, taskID, amountCredits int64, note string) (Result, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (Result, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
}

type txBilling interface {
	SettleTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits, actualCredits int64, note string) (Result, error)
	RefundTx(ctx context.Context, tx *sql.Tx, userID, taskID, frozenCredits int64, note string) (Result, error)
}

var (
	_ agentBilling        = (*NoopService)(nil)
	_ canvasScriptBilling = (*NoopService)(nil)
	_ txBilling           = (*NoopService)(nil)
)

func TestNoopServiceAlwaysSucceedsWithoutDB(t *testing.T) {
	s := NewNoopService()
	ctx := context.Background()
	// 桌面端不计费（spec X3）：任何金额、nil 事务都立即成功，不碰数据库。
	if _, err := s.Freeze(ctx, 1, 2, 999999, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreezeTx(ctx, nil, 1, 2, 999999, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settle(ctx, 1, 2, 10, 20, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleTx(ctx, nil, 1, 2, 10, 20, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refund(ctx, 1, 2, 10, "n"); err != nil {
		t.Fatal(err)
	}
	r, err := s.RefundTx(ctx, nil, 1, 2, 10, "n")
	if err != nil || r.NewBalanceCredits != 0 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}
```

`server/internal/billing/actor_test.go`：

```go
package billing

import (
	"context"
	"testing"
)

func TestActorContextRoundTrip(t *testing.T) {
	if got := ActorFromContext(context.Background()); got != 0 {
		t.Fatalf("未设置时应为 0（个人路径），got %d", got)
	}
	if got := ActorFromContext(WithActor(context.Background(), 42)); got != 42 {
		t.Fatalf("got %d want 42", got)
	}
}
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test ./internal/billing/`
Expected: 编译失败，`undefined: NewNoopService`、`undefined: ActorFromContext`。

- [ ] **Step 5: 写实现**

`server/internal/billing/noop.go`：

```go
package billing

import (
	"context"
	"database/sql"
	"errors"
)

// 桌面端不做积分计费（spec X3）。这里保留参考仓 billing/service.go:12-20 的错误值与
// Result 类型，让搬过来的调用方原样编译；真正的 Freeze/Settle/Refund 全部换成 NoopService。

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrTaskNotFound        = errors.New("task not found")
)

// Result 与参考仓 billing.Result 同形。
type Result struct {
	NewBalanceCredits int64 `json:"newBalanceCredits"`
}

// NoopService 满足参考仓各处计费窄接口（agent.AgentBilling、canvasscript 的 billingService、
// chat 的 chatBilling；generation.Billing 经 cmd/sidecar 的适配器）。所有操作立即成功、
// 不落库，nil 事务也可以。
type NoopService struct{}

func NewNoopService() *NoopService { return &NoopService{} }

func (*NoopService) Freeze(context.Context, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) Settle(context.Context, int64, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) Refund(context.Context, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) FreezeTx(context.Context, *sql.Tx, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) SettleTx(context.Context, *sql.Tx, int64, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}

func (*NoopService) RefundTx(context.Context, *sql.Tx, int64, int64, int64, string) (Result, error) {
	return Result{}, nil
}
```

`server/internal/billing/actor.go`（取自参考仓 `billing/quota_gate.go:35-57`，删掉团队额度闸门相关的 `QuotaGate`、`SetQuotaGate`、`cstLoc`、`currentQuotaPeriod`）：

```go
package billing

import "context"

// actorKey 是 ctx 里「真实操作者」的私有 key 类型，避免跨包碰撞（同 dramaScopeKey）。
type actorKey struct{}

// WithActor 把真人成员的 user id 挂到 ctx。桌面端只有一个本地用户、没有团队，
// 保留它只是为了让搬过来的调用方（canvasscript、agent）原样编译；
// actorUserID<=0 或不包 = 个人路径。
func WithActor(ctx context.Context, actorUserID int64) context.Context {
	return context.WithValue(ctx, actorKey{}, actorUserID)
}

// ActorFromContext 取回真实操作者；未设置返回 0（=个人路径）。
func ActorFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(actorKey{}).(int64); ok {
		return v
	}
	return 0
}
```

- [ ] **Step 6: 运行全部叶子包测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet ./internal/billing/ ./internal/upstream/ ./internal/lifecycle/ ./internal/mediaprobe/ ./internal/canvasinputs/ && go test ./internal/billing/ ./internal/upstream/ ./internal/lifecycle/ ./internal/mediaprobe/ ./internal/canvasinputs/`
Expected: 全部 `ok`；`mediaprobe` 在没有 ffprobe 的机器上其真实样本用例 SKIP 属正常。

- [ ] **Step 7: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/billing server/internal/upstream server/internal/lifecycle server/internal/mediaprobe server/internal/canvasinputs
git commit -m "feat(server): 移植 upstream/lifecycle/mediaprobe/canvasinputs，计费改空实现"
```

---

### Task 2: 迁移 0002_generation.sql 与 P1b 临时 shim

**Files:**
- Create: `server/internal/db/migrations/0002_generation.sql`
- Create: `server/internal/assets/p1c_shim.go`、`server/internal/http/p1c_shim.go`（均带 `//go:build p1cshim`）
- Test: `server/internal/db/generation_schema_test.go`

**Interfaces:**
- Consumes: `dbtest.Open`、`db.Migrate`（P1a）；`writeJSON(w, status int, v any)`（P0 `http/router.go`）
- Produces:
  - 表 `generation_tasks`、`generation_sink_outbox`（列见 Step 3）
  - shim（P1b 合并后由真实定义取代）：`assets.PutObjectInput`（含 `Content() (io.Reader, int64)`）、`assets.StoredObject`、`assets.AssetRecord`、`assets.MediaProtocol`、`assets.WalkStrings(v any, transform func(string) string) any`；`http` 包内 `withUserID(ctx, int64) context.Context`、`userIDFromContext(ctx) int64`、`writeError(w, status int, message string)`、`decodeJSON(r, v any) error`

- [ ] **Step 1: 写失败的测试**

`server/internal/db/generation_schema_test.go`：

```go
package db_test

import (
	"sort"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func columns(t *testing.T, table string) []string {
	t.Helper()
	conn := dbtest.Open(t)
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestGenerationTablesCreated(t *testing.T) {
	want := map[string][]string{
		"generation_tasks": {"attempt", "audit_flag", "cost_cents", "created_at", "error", "frozen_cents",
			"heartbeat_at", "id", "input", "lease_version", "model_id", "next_retry_at", "output", "output_kind",
			"owner_instance", "project_id", "sink", "status", "updated_at", "upstream_called_at", "upstream_task_id", "user_id"},
		"generation_sink_outbox": {"attempt", "created_at", "delivery_version", "id", "last_error", "locked_until",
			"next_retry_at", "payload", "sink", "state", "task_id", "updated_at"},
	}
	for table, cols := range want {
		got := columns(t, table)
		if len(got) != len(cols) {
			t.Fatalf("%s 列 = %v，want %v", table, got, cols)
		}
		for i := range cols {
			if got[i] != cols[i] {
				t.Fatalf("%s 列 = %v，want %v", table, got, cols)
			}
		}
	}
}

// id 进对象 key（gen/task-<id>-<i>），删除后绝不能被复用，否则新任务会覆盖旧任务的媒体文件。
func TestGenerationTaskIDsNeverReused(t *testing.T) {
	conn := dbtest.Open(t)
	ins := func() int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input) VALUES (1, 'm', '{}')`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	first := ins()
	if _, err := conn.Exec(`DELETE FROM generation_tasks WHERE id = ?`, first); err != nil {
		t.Fatal(err)
	}
	if second := ins(); second <= first {
		t.Fatalf("删除后新 id=%d 复用了旧 id=%d", second, first)
	}
}

func TestGenerationTasksDefaultsAndUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input, updated_at) VALUES (1, 'm', '{}', '2000-01-01 00:00:00')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	var status string
	var lease, attempt int64
	if err := conn.QueryRow(`SELECT status, lease_version, attempt FROM generation_tasks WHERE id = ?`, id).Scan(&status, &lease, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || lease != 0 || attempt != 0 {
		t.Fatalf("默认值 status=%s lease=%d attempt=%d", status, lease, attempt)
	}
	if _, err := conn.Exec(`UPDATE generation_tasks SET status = 'running' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var updated string
	if err := conn.QueryRow(`SELECT updated_at FROM generation_tasks WHERE id = ?`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated[:4] == "2000" {
		t.Fatal("UPDATE 后 updated_at 没有刷新")
	}
}

// JSON 列必须是 TEXT：BLOB 会被 SQLite 3.45+ 的 json 函数当作 JSONB。
func TestGenerationJSONColumnsRejectBlob(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, input) VALUES (1, 'm', ?)`, []byte(`{}`)); err == nil {
		t.Fatal("input 以 []byte 写入应被 CHECK 约束拒绝")
	}
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (1, ?, '{}')`, []byte(`{}`)); err == nil {
		t.Fatal("sink 以 []byte 写入应被 CHECK 约束拒绝")
	}
}

func TestOutboxOneRowPerTask(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (7, '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload) VALUES (7, '{}', '{}')`); err == nil {
		t.Fatal("同一任务第二行 outbox 应撞 uk_outbox_task")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test ./internal/db/ -run 'Generation|Outbox'`
Expected: FAIL，`no such table: generation_tasks`。

- [ ] **Step 3: 写迁移**

`server/internal/db/migrations/0002_generation.sql`：

```sql
-- P1c 生成链路：任务表与结果投递外发箱。
-- 列由参考仓迁移 0002/0053/0055/0080/0081/0082/0103/0105 推导；去掉 drama_project_id、
-- drama_segment_id、actor_user_id（drama 与团队额度不移植）。约定同 0001：时间 DATETIME 存 UTC
-- 文本，JSON 列 TEXT 且必须是 text 类型，ON UPDATE CURRENT_TIMESTAMP 用触发器实现。
-- 本文件的表与其他迁移的表没有外键依赖，与其他计划的 0002_* 文件执行先后无关。

CREATE TABLE generation_tasks (
  -- AUTOINCREMENT：id 进对象 key gen/task-<id>-<i>，删除后不得复用。
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  project_id INTEGER NULL,
  model_id TEXT NOT NULL,
  -- queued / running / processing / retry_pending / succeeded / failed（settling 为参考仓历史值，代码仍兼容）。
  status TEXT NOT NULL DEFAULT 'queued',
  upstream_task_id TEXT NULL,
  input TEXT NOT NULL CHECK (typeof(input) = 'text'),
  output TEXT NULL CHECK (output IS NULL OR typeof(output) = 'text'),
  -- 产物类型大类 image/video/audio；NULL = 未知或纯文本。
  output_kind TEXT NULL,
  -- 桌面端不计费（spec X3），两列保留只为与参考仓代码同形，值无业务含义。
  frozen_cents INTEGER NOT NULL DEFAULT 0,
  cost_cents INTEGER NULL,
  audit_flag TEXT NULL,
  error TEXT NULL,
  -- 执行租约：哪个进程在跑、最后心跳。启动时上个进程的行会被判为过期（generation/startup.go）。
  owner_instance TEXT NULL,
  heartbeat_at DATETIME NULL,
  -- 结果去向，如 {"kind":"canvas_node","projectId":12,"nodeId":"abc","userId":1}；NULL = 调用方自取。
  sink TEXT NULL CHECK (sink IS NULL OR typeof(sink) = 'text'),
  -- 首次向上游发请求的时刻；NULL = 没调过上游，被中断可安全重排。
  upstream_called_at DATETIME NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  next_retry_at DATETIME NULL,
  -- 执行权代次：每次领取/接管 +1，所有推进状态的写都要带对它。
  lease_version INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_tasks_user ON generation_tasks (user_id);
CREATE INDEX idx_tasks_project ON generation_tasks (project_id);
CREATE INDEX idx_tasks_status ON generation_tasks (status);
CREATE INDEX idx_gen_tasks_lease ON generation_tasks (status, heartbeat_at);
CREATE INDEX idx_gt_queue ON generation_tasks (status, model_id, id);
CREATE INDEX idx_gt_retry ON generation_tasks (status, next_retry_at);

CREATE TRIGGER trg_generation_tasks_updated_at AFTER UPDATE ON generation_tasks
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE generation_tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TABLE generation_sink_outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  sink TEXT NOT NULL CHECK (typeof(sink) = 'text'),
  payload TEXT NOT NULL CHECK (typeof(payload) = 'text'),
  -- pending / delivering / done / dead
  state TEXT NOT NULL DEFAULT 'pending',
  attempt INTEGER NOT NULL DEFAULT 0,
  delivery_version INTEGER NOT NULL DEFAULT 0,
  locked_until DATETIME NULL,
  next_retry_at DATETIME NULL,
  last_error TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT uk_outbox_task UNIQUE (task_id)
);
CREATE INDEX idx_outbox_claim ON generation_sink_outbox (state, next_retry_at, locked_until, id);

CREATE TRIGGER trg_generation_sink_outbox_updated_at AFTER UPDATE ON generation_sink_outbox
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE generation_sink_outbox SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go test ./internal/db/...`
Expected: 全部 PASS（含 P1a 既有用例）。

- [ ] **Step 5: 写 shim（先检查 P1b 是否已合并）**

Run: `cd /Users/james/github/miaoju-desktop/server && ls internal/assets/storage.go internal/http/context.go 2>/dev/null`
若两个文件都存在：跳过本步与 Step 6，直接 Step 7，并按 Global Constraints 去掉后续所有 `-tags p1cshim`。否则创建：

`server/internal/assets/p1c_shim.go`（内容逐字取自参考仓 `assets/storage.go:10-35`、`assets/repository.go:9-22`、`assets/minio.go:104`、`assets/transform.go:5-26`）：

```go
//go:build p1cshim

// 本文件只供 P1c 在 P1b 合并前独立编译、测试（go test -tags p1cshim）。
// 内容逐字取自参考仓 assets 包；P1b 合并后默认构建不再包含本文件，Task 10 删除它。
package assets

import (
	"bytes"
	"io"
	"time"
)

type PutObjectInput struct {
	Key         string
	ContentType string
	Body        []byte
	Reader      io.Reader
	Size        int64
}

func (in PutObjectInput) Content() (io.Reader, int64) {
	if in.Body != nil {
		return bytes.NewReader(in.Body), int64(len(in.Body))
	}
	if in.Reader == nil {
		return bytes.NewReader(nil), 0
	}
	return in.Reader, in.Size
}

type StoredObject struct {
	URL  string
	Size int64
}

type AssetRecord struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"userId"`
	ProjectID     int64     `json:"projectId,omitempty"`
	SourceTaskID  int64     `json:"sourceTaskId,omitempty"`
	ObjectKey     string    `json:"objectKey"`
	SourceKind    string    `json:"sourceKind"`
	URL           string    `json:"url"`
	SizeBytes     int64     `json:"sizeBytes"`
	MimeType      string    `json:"mimeType"`
	ContentHash   string    `json:"contentHash,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	LibraryItemID int64     `json:"libraryItemId,omitempty"`
	DurationMs    int64     `json:"durationMs,omitempty"`
}

const MediaProtocol = "media://"

func WalkStrings(v any, transform func(string) string) any {
	switch x := v.(type) {
	case string:
		return transform(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = WalkStrings(val, transform)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = WalkStrings(val, transform)
		}
		return out
	default:
		return v
	}
}
```

`server/internal/http/p1c_shim.go`（逐字取自参考仓 `http/context.go:5-33`、`http/helpers.go:17-24`；`writeJSON` 已由 P0 的 `router.go` 提供）：

```go
//go:build p1cshim

// 本文件只供 P1c 在 P1b 合并前独立编译、测试（go test -tags p1cshim）。
// 内容逐字取自参考仓 http/context.go 与 http/helpers.go；P1b 合并后默认构建不再包含本文件，
// Task 10 删除它。
package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
)

type contextKey string

const userIDContextKey contextKey = "userID"

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func userIDFromContext(ctx context.Context) int64 {
	value, _ := ctx.Value(userIDContextKey).(int64)
	return value
}

func writeError(w nethttp.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func decodeJSON(r *nethttp.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
```

- [ ] **Step 6: 确认 shim 可编译、不影响默认构建**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/assets/ ./internal/http/ && go test -tags p1cshim ./internal/http/ && go test ./internal/http/`
Expected: 全部通过（`internal/assets` 不带 tag 时没有可编译文件属正常，故不对它跑无 tag 命令）。

- [ ] **Step 7: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/db/migrations/0002_generation.sql server/internal/db/generation_schema_test.go server/internal/assets server/internal/http/p1c_shim.go
git commit -m "feat(server): 生成任务与 outbox 表迁移；P1b 合并前的临时 shim"
```

---

### Task 3: generation / canvasgen 骨架（去依赖、可编译）

**Files:**
- Create（复制后修改）: `server/internal/generation/*.go`（参考仓除 `reconcile.go` 外全部非测试文件）、`server/internal/canvasgen/*.go`（全部，含测试）
- Test（复制，部分修改）: `server/internal/generation/` 下的非库测试（见「移植的测试」），其中改写 `input_duration_test.go`、修改 `recover_test.go`、`real_person_inputs_test.go`；新建 `server/internal/generation/scan_db_test.go`

**Interfaces:**
- Consumes: Task 1 全部；Task 2 的 assets shim；P1a 的 `models.ModelConfig` 及方法、`canvasstate.*`
- Produces（第 2 批各任务依赖，名称与参考仓相同的不再列出）：
  - `generation.SQLiteRepository`、`generation.NewSQLiteRepository(db *sql.DB) *SQLiteRepository`
  - `generation.Task`：去掉 `DramaProjectID`、`DramaSegmentID`、`ActorUserID`
  - `generation.SubmitInput`：去掉 `DramaProjectID`、`DramaSegmentID`（保留 `Note`、`ForceAsync`、`Sink`、`PresetKey`、`PresetParams`）
  - `generation.Repository`：去掉 `DramaScope`
  - `generation.ObjectReclaimer` 接口：`ReclaimTx(tx *sql.Tx, objectKey string, userID, sizeBytes int64) (freed bool, err error)`
  - `generation.NewService(repo Repository, modelLookup ModelLookup, settingsLookup SettingsLookup, upstreamKey UpstreamKeyLookup, billing Billing, client *upstream.Client, storage AssetStorage, assetRepo AssetRecorder, userStorage UserStorage, reclaimer ObjectReclaimer, db *sql.DB) *Service`（第 10 个参数类型变了，位置不变）
  - `canvasgen.*`：与参考仓逐字相同

- [ ] **Step 1: 复制文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cd /Users/james/github/miaoju-desktop/server/internal
mkdir -p generation canvasgen
for f in $REF/generation/*.go; do
  case "$(basename $f)" in
    reconcile.go|*_db_test.go) ;;   # 对账不移植；真实库测试由 Task 4 改写后再复制
    *) cp "$f" generation/ ;;
  esac
done
cp $REF/canvasgen/*.go canvasgen/
```

- [ ] **Step 2: 机械改名**

```bash
cd /Users/james/github/miaoju-desktop/server/internal/generation
sed -i '' 's/NewMySQLRepository/NewSQLiteRepository/g; s/MySQLRepository/SQLiteRepository/g' *.go
```

（Linux 上把 `sed -i ''` 换成 `sed -i`。）

- [ ] **Step 3: 删除 drama / actor（repository.go、tx_submit.go、finish.go、service.go）**

`server/internal/generation/repository.go`：

1. `Task` 结构体删掉 `DramaProjectID`、`DramaSegmentID` 两个字段（参考仓 :29-30）以及 `ActorUserID` 字段和它上面的三行注释（:31-34）。
2. `Repository` 接口删掉 `DramaScope` 及其两行注释（:136-138）。
3. 删掉函数 `nullableTaskID`（:145-151）和方法 `DramaScope`（:341-350，连同上方两行注释）。
4. `Create`、`CreateTx` 两处 INSERT（:165-169、:187-191）都替换为（同时把 JSON 参数改成 string）：

```go
		`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, frozen_cents, sink) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.UserID, projectID, task.ModelID, task.Status, string(task.Input), task.FrozenCents, sink,
```

   并把两处 `sink = []byte(task.Sink)` 改为 `sink = string(task.Sink)`。
4a. **TEXT 列不能直接扫进 `json.RawMessage`**（已实测：modernc 把 TEXT 返回为 `string`，`database/sql` 只会把 string 转进 `*string` / `*[]byte`，扫 `*json.RawMessage` 报 `unsupported Scan, storing driver.Value type string into type *json.RawMessage`；参考仓在 MySQL 上拿到的是 `[]byte` 所以没事）。`Get`（:207）、`LatestByCanvasNode`（:244）、`List`（:319）、`ListProcessing`（:420）、`scanQueueTask`（:487-488）五处 Scan 参数里的 `&t.Input` 改为 `&input`：在各自的 `var output []byte`（`scanQueueTask` 里是 `var sink []byte`）旁边声明 `var input []byte`，Scan 成功后紧跟一行 `t.Input = json.RawMessage(input)`。这一条放在骨架任务里做，是因为第 2 批的 HTTP 任务（Task 8）依赖 `Get` / `List` 能在 SQLite 上读回任务。

`server/internal/generation/tx_submit.go`：

5. `s.repo.CreateTx(tx, Task{...})` 字面量（:105-116）删掉 `DramaProjectID`、`DramaSegmentID`、`ActorUserID` 三行。
6. 删掉 `ctx = billing.WithDramaScope(ctx, input.DramaProjectID, input.DramaSegmentID)`（:121）。

`server/internal/generation/finish.go`：

7. `finishSuccessTx` 的参数 `[]byte(out.output)`（:43）改为 `string(out.output)`。
8. `finishTx` 里 :87-101（从 `// 行已被本事务锁定` 到 `ctx = billing.WithActor(ctx, actor.Int64)`）整段替换为：

```go
	// 行已被本事务锁定,读资金与投递材料。
	var userID, frozen int64
	var sink []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id, frozen_cents, sink FROM generation_tasks WHERE id=?`,
		taskID).Scan(&userID, &frozen, &sink); err != nil {
		return false, err
	}
```

9. 删掉 import 里的 `"database/sql"` 和 `".../internal/billing"`（finish.go 已不再用它们）。

`server/internal/generation/service.go`：

10. `SubmitInput` 删掉 `DramaProjectID`、`DramaSegmentID` 两个字段及注释（:268-271）。
11. :361 的日志改为 `log.Printf("[gen] task=%d 入队等待执行 model=%s note=%q", res.TaskID, input.ModelID, input.Note)`；:467 改为 `log.Printf("[gen] submit task=%d model=%s note=%q (direct)", taskID, input.ModelID, input.Note)`。
12. 直连路径的 `s.repo.CreateTx(tx, Task{...})`（:438-448）删掉 `DramaProjectID`、`DramaSegmentID`、`ActorUserID` 三行；删掉 :455 `ctx = billing.WithDramaScope(...)`。

- [ ] **Step 4: 换掉 library 与 mediaprobe（service.go、input_duration.go）**

`server/internal/generation/service.go`：

1. import 删掉 `".../internal/library"`、`".../internal/mediaprobe"`。
2. 在 `UserStorage` 接口（:92-99）之后插入：

```go
// ObjectReclaimer 是删除任务时回收产物对象的窄依赖。参考仓直接调 library.TryReclaim +
// library.IsStillReferenced；桌面端经接口注入（实现由 P1b 的 library 包提供，装配见
// cmd/sidecar），generation 因此不 import library。
type ObjectReclaimer interface {
	// ReclaimTx 在 tx 的可见性下判断 objectKey 是否仍被 assets / library_items 引用；
	// 不再被引用时登记孤儿（7 天后物理删除，spec S9）并返回 true，调用方据此回收存储用量。
	ReclaimTx(tx *sql.Tx, objectKey string, userID, sizeBytes int64) (freed bool, err error)
}
```

3. `Service` 结构体：`orphans     library.OrphansRepository`（:111）改为 `reclaimer   ObjectReclaimer`；删掉 `durationProbe` 字段及其两行注释（:127-129）。
4. `NewService`：参数 `orphans library.OrphansRepository`（:144）改为 `reclaimer ObjectReclaimer`；`orphans:     orphans,`（:157）改为 `reclaimer:   reclaimer,`；删掉 `durationProbe: mediaprobe.ProbeDurationMs,`（:165）。
5. `Delete` 里从 `var freed int64`（:1236）到 `return tx.Commit()`（:1267）整段替换为：

```go
	var freed int64
	if s.reclaimer != nil {
		for _, a := range ownedAssets {
			// 在本事务的可见性下判断引用：asset 行刚在上面删掉，无其他引用的对象登记孤儿、延后物理删除。
			ok, err := s.reclaimer.ReclaimTx(tx, a.ObjectKey, userID, a.SizeBytes)
			if err != nil {
				return err
			}
			if ok {
				freed += a.SizeBytes
			}
		}
	}
	// 配额回收同事务：任一步失败整体回滚，行还在、可重试。
	if s.users != nil && freed > 0 {
		if err := s.users.AddStorageTx(tx, userID, -freed); err != nil {
			return err
		}
	}
	return tx.Commit()
```

`server/internal/generation/input_duration.go`：整个文件替换为：

```go
package generation

import (
	"context"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// InputDurationError：配置了 gen_control.inputDuration 的模型缺少输入视频。独立类型
// （同 InputLimitError 模式）：HTTP 层 errors.As 映射 400，用户连上视频即可自解。
type InputDurationError struct{ Msg string }

func (e *InputDurationError) Error() string { return e.Msg }

// FirstVideoInput 取 inputs.videos 第一个非空字符串。网页路径是 []any、CLI 路径是 []string。
func FirstVideoInput(inputs map[string]any) string {
	switch arr := inputs["videos"].(type) {
	case []any:
		for _, it := range arr {
			if s, ok := it.(string); ok && s != "" {
				return s
			}
		}
	case []string:
		for _, s := range arr {
			if s != "" {
				return s
			}
		}
	}
	return ""
}

// resolveInputDuration 按 gen_control.inputDuration 从首个输入视频取真实时长注入 inputs.duration。
//
// 桌面端差异：参考仓注入时长是为了按秒计费防薅，取不到就拒绝提交，并在 duration_ms 为 NULL
// 时下载对象跑 ffprobe 再回填。桌面端不计费（spec X3）、不带 ffprobe，上游请求模板也不读这个
// 值（它只影响产物 assets.duration_ms），所以只走 duration_ms 快路径：查到就注入，查不到放行。
// 去掉下载 + 探测 + 回填还有一个硬理由：SubmitTx 在写事务里调用本函数，回填经 r.db 写库会
// 自死锁（等 busy_timeout 10 秒后 SQLITE_BUSY），读几百 MB 文件也会长时间占着写锁。
func (s *Service) resolveInputDuration(_ context.Context, cfg *models.InputDurationConfig, inputs map[string]any) error {
	if cfg == nil {
		return nil
	}
	src := FirstVideoInput(inputs)
	if src == "" {
		return &InputDurationError{Msg: "该模型需要连接视频输入"}
	}
	if s.storage == nil || s.assets == nil {
		return nil
	}
	key, ok := s.storage.ExtractKey(src)
	if !ok || key == "" {
		return nil
	}
	ms, found, err := s.assets.GetDurationMsByObjectKey(key)
	if err != nil || !found || ms <= 0 {
		return nil
	}
	inputs["duration"] = float64(ms) / 1000.0
	return nil
}
```

- [ ] **Step 5: 改测试**

1. `server/internal/generation/recover_test.go`：删掉 `fakeRecoverRepo` 的 `dramaScopes map[int64][2]int64` 字段和 `DramaScope` 方法（参考仓 :15、:25-30）。
2. `server/internal/generation/real_person_inputs_test.go`：import 删掉 `".../internal/library"`；在 import 块后加 `var errRealPersonNotReady = errors.New("真人图尚未通过合规校验")`；文件里两处 `library.ErrRealPersonNotReady` 替换为 `errRealPersonNotReady`。
3. `server/internal/generation/input_duration_test.go` 整个替换为：

```go
package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

type fakeDurStorage struct {
	AssetStorage
	key string // ExtractKey 命中时返回的 key；空 = 解析失败
}

func (f fakeDurStorage) ExtractKey(string) (string, bool) { return f.key, f.key != "" }

type fakeDurAssets struct {
	AssetRecorder
	durMs     int64 // >0 = duration_ms 命中
	getErr    error
	setCalled bool
}

func (f *fakeDurAssets) GetDurationMsByObjectKey(string) (int64, bool, error) {
	if f.getErr != nil {
		return 0, false, f.getErr
	}
	return f.durMs, f.durMs > 0, nil
}

func (f *fakeDurAssets) SetDurationMsByObjectKey(string, int64) error {
	f.setCalled = true
	return nil
}

var vcfg = &models.InputDurationConfig{From: "video"}

func TestResolveInputDuration(t *testing.T) {
	t.Run("未配置放行且不动 inputs", func(t *testing.T) {
		in := map[string]any{"duration": 5}
		if err := (&Service{}).resolveInputDuration(context.Background(), nil, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 5 {
			t.Fatal("未配置不得改写 duration")
		}
	})
	t.Run("无视频输入拒绝", func(t *testing.T) {
		svc := &Service{storage: fakeDurStorage{}, assets: &fakeDurAssets{}}
		err := svc.resolveInputDuration(context.Background(), vcfg, map[string]any{})
		var de *InputDurationError
		if !errors.As(err, &de) || de.Msg != "该模型需要连接视频输入" {
			t.Fatalf("期望无视频文案, got %v", err)
		}
	})
	t.Run("duration_ms 命中即注入并覆盖客户端的值", func(t *testing.T) {
		in := map[string]any{"videos": []string{"media://u1/v"}, "duration": 1}
		svc := &Service{storage: fakeDurStorage{key: "u1/v"}, assets: &fakeDurAssets{durMs: 12500}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 12.5 {
			t.Fatalf("got %v want 12.5", in["duration"])
		}
	})
	t.Run("key 解析失败放行且不改 inputs", func(t *testing.T) {
		in := map[string]any{"videos": []any{"https://x/v.mp4"}, "duration": 3}
		svc := &Service{storage: fakeDurStorage{}, assets: &fakeDurAssets{durMs: 9000}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 3 {
			t.Fatalf("got %v want 3", in["duration"])
		}
	})
	t.Run("DB 报错放行", func(t *testing.T) {
		svc := &Service{storage: fakeDurStorage{key: "k"}, assets: &fakeDurAssets{getErr: errors.New("db timeout")}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, map[string]any{"videos": []any{"u"}}); err != nil {
			t.Fatalf("桌面端不计费，取不到时长应放行, got %v", err)
		}
	})
	// SubmitTx 在写事务里调用本函数：任何经 r.db 的写都会自死锁（Review Focus 5）。
	t.Run("未命中放行且绝不回填", func(t *testing.T) {
		as := &fakeDurAssets{}
		svc := &Service{storage: fakeDurStorage{key: "k"}, assets: as}
		in := map[string]any{"videos": []any{"u"}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if as.setCalled {
			t.Fatal("不得调用 SetDurationMsByObjectKey（写事务内经 r.db 写库会自死锁）")
		}
		if _, ok := in["duration"]; ok {
			t.Fatal("查不到时长时不得编造 duration")
		}
	})
}

// Submit 级只测失败路径（注入发生在触碰 repo/billing 之前，nil 依赖安全返回）。
func TestSubmitRejectsMissingInputVideo(t *testing.T) {
	svc := NewService(nil, inputDurationModelLookup{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Submit(context.Background(), SubmitInput{
		ModelID: "m-edit", Inputs: map[string]any{},
	})
	var de *InputDurationError
	if !errors.As(err, &de) {
		t.Fatalf("期望 InputDurationError, got %v", err)
	}
}

type inputDurationModelLookup struct{}

func (inputDurationModelLookup) GetByModelID(string) (*models.ModelConfig, error) {
	return &models.ModelConfig{ModelID: "m-edit", Enabled: true,
		GenControlRaw: json.RawMessage(`{"inputDuration":{"from":"video"}}`)}, nil
}
```

4. 新建 `server/internal/generation/scan_db_test.go`，钉住 Step 3 第 4、4a 条（`Create` / `Get` / `List` / `SinkForTask` 的 SQL 本身已是 SQLite 兼容写法，此时就能在真实库上跑）：

```go
package generation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestTaskJSONRoundTripsThroughText(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	pid := int64(3)
	in := `{"modelId":"m","inputs":{"prompt":"猫"}}`
	id, err := repo.Create(Task{UserID: 1, ProjectID: &pid, ModelID: "m", Status: "succeeded",
		Input: json.RawMessage(in), Sink: json.RawMessage(`{"kind":"canvas_node","nodeId":"n"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var inputType, sinkType string
	if err := conn.QueryRow(`SELECT typeof(input), typeof(sink) FROM generation_tasks WHERE id = ?`, id).Scan(&inputType, &sinkType); err != nil {
		t.Fatal(err)
	}
	if inputType != "text" || sinkType != "text" {
		t.Fatalf("JSON 必须以 TEXT 落库, input=%s sink=%s", inputType, sinkType)
	}
	if _, err := conn.Exec(`UPDATE generation_tasks SET output = '{"outputUrls":["media://k"]}' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(1, id)
	if err != nil {
		t.Fatalf("Get 读不回 TEXT 里的 JSON: %v", err)
	}
	if string(got.Input) != in || !strings.Contains(string(got.Output), "media://k") {
		t.Fatalf("input=%s output=%s", got.Input, got.Output)
	}
	list, total, err := repo.List(ListFilter{UserID: 1})
	if err != nil || total != 1 || string(list[0].Input) != in {
		t.Fatalf("List: total=%d err=%v", total, err)
	}
	sink, err := repo.SinkForTask(id)
	if err != nil || !strings.Contains(string(sink), `"nodeId":"n"`) {
		t.Fatalf("SinkForTask: %s %v", sink, err)
	}
}
```

- [ ] **Step 6: 编译、静态检查并跑非库测试**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/generation/ ./internal/canvasgen/ && go test -tags p1cshim ./internal/generation/ ./internal/canvasgen/`
Expected: 全部 `ok`。若 `go vet` 报 `imported and not used`，删掉对应 import（例如 service.go 里若 `billing` 仍被 `EstimateFreeze` 使用就保留）。再确认没有残留：

Run: `cd /Users/james/github/miaoju-desktop/server && grep -rn 'Drama\|ActorUserID\|library\.\|mediaprobe\|MySQL' internal/generation internal/canvasgen | grep -v '_test.go:.*//' `
Expected: 只剩注释里提到 MySQL 的行；把这些注释按 SQLite 实情改写（例如 repository.go:297-300 两阶段取数注释改成「保留两阶段写法，SQLite 下同样只对 id 排序」），改完再跑一次上面的 vet + test。

- [ ] **Step 7: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/generation server/internal/canvasgen
git commit -m "feat(server): 移植 generation/canvasgen 骨架，去掉 drama、团队额度、对账与 ffprobe 依赖"
```

---

### Task 4: SQLite 方言改写 + 10 个真实库测试

**Files:**
- Modify: `server/internal/generation/repository.go`、`server/internal/generation/outbox.go`
- Create: `server/internal/generation/sqlite_helpers_test.go`、`server/internal/generation/dialect_db_test.go`
- Test（从参考仓复制后改写）: `server/internal/generation/{lease,retry,outbox,latest_by_node,output_kind,finish,recover,tx_submit,delete,persist}_db_test.go`

**Interfaces:**
- Consumes: Task 3 的 `SQLiteRepository`、`Service` 内部字段（`repo`、`billing`、`db`、`storage`、`assets`、`users`、`reclaimer`、`leaseStaleSec`）；`dbtest.Open`
- Produces:
  - `generation.sqlSecondsOffset(sec int) string`（包内，Task 7 也用）
  - `SQLiteRepository` 全部方法在 SQLite 上可用；`ListExternalOutputs`、`MarkRepersistAttempt` 删除；`UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error` 保留（Task 5 用）
  - 测试替身（包内）：`billingSpy`、`billingCall`、`spyOf(*Service) *billingSpy`、`sqlTestAssets`、`sqlTestUsers`、`sqlTestReclaimer`

- [ ] **Step 1: 写测试替身**

`server/internal/generation/sqlite_helpers_test.go`：

```go
package generation

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// 只给 *_db_test.go 用的 SQLite 测试替身。生产实现分别在 P1b 的 assets / library 包和
// cmd/sidecar/gen_wiring.go；这里用同形 SQL 直写，让 generation 的真实库测试不依赖 P1b。

// billingCall / billingSpy：桌面端计费是空实现，原测试里「钱动没动」的断言改成
// 「收口事务里调没调、调了几次 Settle/Refund」——结构性保证不变。
type billingCall struct {
	Op     string
	TaskID int64
	Frozen int64
	Actual int64
}

type billingSpy struct {
	mu        sync.Mutex
	calls     []billingCall
	freezeErr error
}

func (b *billingSpy) record(c billingCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, c)
}

func (b *billingSpy) ops() []billingCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]billingCall(nil), b.calls...)
}

func (b *billingSpy) Freeze(_ context.Context, _, t, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Freeze", TaskID: t, Frozen: a})
	return BillingResult{}, nil
}

func (b *billingSpy) Settle(_ context.Context, _, t, f, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Settle", TaskID: t, Frozen: f, Actual: a})
	return BillingResult{}, nil
}

func (b *billingSpy) Refund(_ context.Context, _, t, f int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "Refund", TaskID: t, Frozen: f})
	return BillingResult{}, nil
}

func (b *billingSpy) FreezeTx(_ context.Context, _ *sql.Tx, _, t, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "FreezeTx", TaskID: t, Frozen: a})
	return BillingResult{}, b.freezeErr
}

func (b *billingSpy) SettleTx(_ context.Context, _ *sql.Tx, _, t, f, a int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "SettleTx", TaskID: t, Frozen: f, Actual: a})
	return BillingResult{}, nil
}

func (b *billingSpy) RefundTx(_ context.Context, _ *sql.Tx, _, t, f int64, _ string) (BillingResult, error) {
	b.record(billingCall{Op: "RefundTx", TaskID: t, Frozen: f})
	return BillingResult{}, nil
}

func spyOf(s *Service) *billingSpy { return s.billing.(*billingSpy) }

// sqlTestAssets 实现 AssetRecorder（槽位幂等语义与 research §6 约定的 P1b 实现一致）。
type sqlTestAssets struct{ db *sql.DB }

func (a sqlTestAssets) Insert(rec assets.AssetRecord) (int64, error) {
	res, err := a.db.Exec(`INSERT INTO assets (user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type)
		VALUES (?, NULLIF(?, 0), NULLIF(?, 0), ?, ?, ?, ?, ?)`,
		rec.UserID, rec.ProjectID, rec.SourceTaskID, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a sqlTestAssets) FindByTaskSlot(taskID int64, outputIndex int) (*assets.AssetRecord, error) {
	var r assets.AssetRecord
	err := a.db.QueryRow(`SELECT id, user_id, object_key, url, size_bytes, mime_type FROM assets
		WHERE source_task_id = ? AND output_index = ?`, taskID, outputIndex).
		Scan(&r.ID, &r.UserID, &r.ObjectKey, &r.URL, &r.SizeBytes, &r.MimeType)
	if err != nil {
		return nil, err
	}
	r.SourceTaskID = taskID
	return &r, nil
}

func (a sqlTestAssets) InsertSlotTx(tx *sql.Tx, rec assets.AssetRecord, outputIndex int) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`INSERT INTO assets (user_id, project_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type, duration_ms, output_index)
		VALUES (?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?)
		ON CONFLICT (source_task_id, output_index) DO NOTHING
		RETURNING id`,
		rec.UserID, rec.ProjectID, rec.SourceTaskID, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType, rec.DurationMs, outputIndex).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	err = tx.QueryRow(`SELECT id FROM assets WHERE source_task_id = ? AND output_index = ?`, rec.SourceTaskID, outputIndex).Scan(&id)
	return id, false, err
}

func (a sqlTestAssets) ListForTask(taskID int64) ([]assets.AssetRecord, error) {
	rows, err := a.db.Query(`SELECT id, user_id, object_key, url, size_bytes, mime_type FROM assets WHERE source_task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assets.AssetRecord
	for rows.Next() {
		var r assets.AssetRecord
		if err := rows.Scan(&r.ID, &r.UserID, &r.ObjectKey, &r.URL, &r.SizeBytes, &r.MimeType); err != nil {
			return nil, err
		}
		r.SourceTaskID = taskID
		out = append(out, r)
	}
	return out, rows.Err()
}

func (a sqlTestAssets) DeleteForTask(taskID int64) (int64, error) {
	res, err := a.db.Exec(`DELETE FROM assets WHERE source_task_id = ?`, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (a sqlTestAssets) GetDurationMsByObjectKey(key string) (int64, bool, error) {
	var ms sql.NullInt64
	err := a.db.QueryRow(`SELECT duration_ms FROM assets WHERE object_key = ? ORDER BY id DESC LIMIT 1`, key).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ms.Int64, true, nil
}

func (a sqlTestAssets) SetDurationMsByObjectKey(key string, ms int64) error {
	_, err := a.db.Exec(`UPDATE assets SET duration_ms = ? WHERE object_key = ?`, ms, key)
	return err
}

type sqlTestUsers struct{ db *sql.DB }

func (u sqlTestUsers) AddStorage(id, delta int64) error {
	_, err := u.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

func (u sqlTestUsers) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	err := u.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota)
	return used, quota, err
}

func (u sqlTestUsers) AddStorageTx(tx *sql.Tx, id, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// sqlTestReclaimer 只看 assets 引用（library_items / orphan_objects 是 P1b 的表），
// 被判定无引用的 key 记进 marked。
type sqlTestReclaimer struct{ marked *[]string }

func (r sqlTestReclaimer) ReclaimTx(tx *sql.Tx, key string, _, _ int64) (bool, error) {
	var referenced bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM assets WHERE object_key = ?)`, key).Scan(&referenced); err != nil {
		return false, err
	}
	if referenced {
		return false, nil
	}
	*r.marked = append(*r.marked, key)
	return true, nil
}
```

- [ ] **Step 2: 复制参考仓真实库测试并改写**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cd /Users/james/github/miaoju-desktop/server/internal/generation
for f in lease retry outbox latest_by_node output_kind persist; do cp $REF/generation/${f}_db_test.go .; done
sed -i '' 's/NewMySQLRepository/NewSQLiteRepository/g; s/MySQLRepository/SQLiteRepository/g' lease_db_test.go retry_db_test.go outbox_db_test.go latest_by_node_db_test.go output_kind_db_test.go persist_db_test.go
```

逐文件修改：

1. `lease_db_test.go`：两处 `NOW()` → `CURRENT_TIMESTAMP`。
2. `outbox_db_test.go`：`DATE_SUB(NOW(), INTERVAL 1 SECOND)` → `datetime('now','-1 seconds')`；`next_retry_at=NOW()` → `next_retry_at=CURRENT_TIMESTAMP`；`DATE_ADD(NOW(), INTERVAL 1 HOUR)` → `datetime('now','+1 hours')`。
3. `retry_db_test.go`（`package generation_test`）：
   - 函数头 `func insertRetryTask(t *testing.T, status string, sink string, attempt int) int64 {` 改为 `func insertRetryTask(t *testing.T, conn *sql.DB, status string, sink string, attempt int) int64 {`，删掉函数体第二行 `conn := dbtest.Open(t)`（原 MySQL 共享库，SQLite 每次 `dbtest.Open` 都是新库，必须用调用方的连接）。
   - 全文 `insertRetryTask(t, ` → `insertRetryTask(t, conn, `。
   - INSERT 里两处 `NOW()` → `CURRENT_TIMESTAMP`；`DATE_SUB(NOW(), INTERVAL 1 SECOND)` → `datetime('now','-1 seconds')`。
   - import 加 `"database/sql"`。
4. `latest_by_node_db_test.go`：只有上面的 sed 改名。
5. `output_kind_db_test.go`：
   - 两处 `res, err := conn.Exec(\`INSERT INTO users … UUID() …\`)` 及其后的 `if err != nil {…}` 与 `uid, _ := res.LastInsertId()` 整段替换为 `uid := int64(1)`（本地用户由迁移预置）。
   - `[]byte(\`{}\`)` → `` `{}` ``。
   - 顶部注释「回填 SQL 本身跑在 Migrate 里…」两行改为「桌面端没有存量数据，不做回填；这里只钉运行时行为。」
6. `persist_db_test.go`：
   - import 删掉 `".../internal/users"`。
   - `res, _ := conn.Exec(\`INSERT INTO users …\`)` 与 `uid, _ := res.LastInsertId()` 替换为 `uid := int64(1)`。
   - `assets:  assets.NewSQLiteRepository(conn),` → `assets:  sqlTestAssets{db: conn},`；`users:   users.NewSQLiteRepository(conn),` → `users:   sqlTestUsers{db: conn},`。

以下四个文件直接写成新内容（原文件大量依赖真实计费与 MySQL 夹具）：

`server/internal/generation/recover_db_test.go`：

```go
package generation

import (
	"database/sql"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// newRecoverDBHarness：真实 SQLite + 记录计费调用的替身。本地用户 id=1 由迁移预置。
// leaseStaleSec 取 60（参考仓是 1）：「心跳新鲜」用例与断言之间跨过 1 秒边界就会误判，60 秒足够稳定。
func newRecoverDBHarness(t *testing.T) (*sql.DB, *Service, int64) {
	t.Helper()
	conn := dbtest.Open(t)
	svc := &Service{
		repo:          NewSQLiteRepository(conn),
		billing:       &billingSpy{},
		db:            conn,
		leaseStaleSec: 60,
	}
	return conn, svc, 1
}

// insertStaleRunning 插一条心跳早已超时的 running 行。
func insertStaleRunning(t *testing.T, conn *sql.DB, uid int64, sink, upstreamID string, called bool) int64 {
	t.Helper()
	var sinkVal, upVal, calledVal any
	if sink != "" {
		sinkVal = sink
	}
	if upstreamID != "" {
		upVal = upstreamID
	}
	if called {
		calledVal = "2026-01-01 00:00:00"
	}
	res, err := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (?, 'm-rc', 'running', '{}', 40, ?, ?, ?, 'dead-instance', datetime('now','-1 hours'), 3)`,
		uid, sinkVal, upVal, calledVal)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func taskState(t *testing.T, conn *sql.DB, id int64) (status string, lease int64) {
	t.Helper()
	if err := conn.QueryRow(`SELECT status, lease_version FROM generation_tasks WHERE id=?`, id).Scan(&status, &lease); err != nil {
		t.Fatalf("read task: %v", err)
	}
	return
}

func assertOps(t *testing.T, svc *Service, want ...string) {
	t.Helper()
	ops := spyOf(svc).ops()
	if len(ops) != len(want) {
		t.Fatalf("计费调用 %v，want %v", ops, want)
	}
	for i, op := range ops {
		if op.Op != want[i] {
			t.Fatalf("计费调用 %v，want %v", ops, want)
		}
	}
}

// 行 3:没调过上游 + 有 sink(队列任务) → 接管后重排,不碰计费。
func TestTakeoverRequeuesUncalledQueueTask(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "", false)

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "queued" {
		t.Fatalf("应重排回 queued, got %s", status)
	}
	if lease <= 3 {
		t.Fatalf("接管+重排必须推进代次, got %d", lease)
	}
	assertOps(t, svc)
}

// 行 4:没调过上游 + 无 sink(直连孤儿) → 原子失败收口。
func TestTakeoverFailsSinklessDirectOrphanWithRefund(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, "", "", false)

	svc.RecoverStaleQueueTasks()

	if status, _ := taskState(t, conn, id); status != "failed" {
		t.Fatalf("直连孤儿应失败, got %s", status)
	}
	assertOps(t, svc, "RefundTx")
}

// 行 2:调过上游但没拿到 id → 结论不确定,保守失败,且有 sink 必须插 outbox 通知画布。
func TestTakeoverRefundsWhenUpstreamOutcomeUnknown(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "", true)

	svc.RecoverStaleQueueTasks()

	if status, _ := taskState(t, conn, id); status != "failed" {
		t.Fatalf("不确定结论应保守失败, got %s", status)
	}
	assertOps(t, svc, "RefundTx")
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("失败收口应插 outbox 通知发起方, got %d 行", n)
	}
}

// 行 1:有 upstream id → 本接管器不插手(续 poll 归 Resume)。
func TestTakeoverLeavesResumableTasksAlone(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	id := insertStaleRunning(t, conn, uid, `{"kind":"canvas_node"}`, "up-123", true)

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "running" || lease != 3 {
		t.Fatalf("可续 poll 的任务不该被动, got %s lease=%d", status, lease)
	}
	assertOps(t, svc)
}

// 心跳新鲜的任务谁也不许碰——接管 CAS 抢不到。
func TestTakeoverSkipsFreshHeartbeat(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, owner_instance, heartbeat_at, lease_version)
		VALUES (?, 'm-rc', 'running', '{}', 40, '{"kind":"canvas_node"}', 'alive', CURRENT_TIMESTAMP, 3)`, uid)
	id, _ := res.LastInsertId()

	svc.RecoverStaleQueueTasks()

	status, lease := taskState(t, conn, id)
	if status != "running" || lease != 3 {
		t.Fatalf("心跳新鲜的任务不许接管, got %s lease=%d", status, lease)
	}
}

// 排队超时:queued 行经 finishQueuedTimeoutTx 原子失败(CAS 内 lease+1 防与领取竞态)。
func TestSweepStuckQueuedAtomicRefund(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, created_at)
		VALUES (?, 'm-rc', 'queued', '{}', 40, '{"kind":"canvas_node"}', datetime('now','-7 hours'))`, uid)
	id, _ := res.LastInsertId()

	svc.SweepStuckQueued()

	status, lease := taskState(t, conn, id)
	if status != "failed" || lease == 0 {
		t.Fatalf("排队超时应失败且推进代次, got %s lease=%d", status, lease)
	}
	assertOps(t, svc, "RefundTx")
}
```

`server/internal/generation/finish_db_test.go`：

```go
package generation

import (
	"context"
	"encoding/json"
	"testing"
)

// finishTx 三件事（终态 CAS、结算/退款、outbox）同事务的真实 SQLite 验证。

func TestFinishSuccessTxAtomic(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node","projectId":1,"nodeId":"n"}', 2)`, uid)
	id, _ := res.LastInsertId()

	out, _ := json.Marshal(map[string]any{"outputUrls": []string{"media://k"}})
	won, err := svc.finishSuccessTx(context.Background(), id, 2, finishOutcome{
		output: out, actualCredits: 25, auditFlag: "credits_settled", note: "测试结算",
		sinkRes: SinkResult{OutputURLs: []string{"media://k"}},
	})
	if err != nil || !won {
		t.Fatalf("成功收口应赢, won=%v err=%v", won, err)
	}
	var status, outputType string
	var cost int64
	_ = conn.QueryRow(`SELECT status, cost_cents, typeof(output) FROM generation_tasks WHERE id=?`, id).Scan(&status, &cost, &outputType)
	if status != "succeeded" || cost != 25 || outputType != "text" {
		t.Fatalf("终态应 succeeded/25/text, got %s/%d/%s", status, cost, outputType)
	}
	ops := spyOf(svc).ops()
	if len(ops) != 1 || ops[0].Op != "SettleTx" || ops[0].Frozen != 40 || ops[0].Actual != 25 {
		t.Fatalf("应在收口事务内结算一次 (40→25), got %v", ops)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("成功收口应插 outbox, got %d", n)
	}
}

// 失租者的终态 CAS 落空 → 整个事务回滚:状态、计费、outbox 全部原样。
func TestFinishTxStaleLeaseRollsBackEverything(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node"}', 2)`, uid)
	id, _ := res.LastInsertId()

	out, _ := json.Marshal(map[string]any{"outputUrls": []string{"media://k"}})
	won, err := svc.finishSuccessTx(context.Background(), id, 1, finishOutcome{ // 旧代次
		output: out, actualCredits: 25, note: "旧代次结算",
		sinkRes: SinkResult{OutputURLs: []string{"media://k"}},
	})
	if err != nil || won {
		t.Fatalf("旧代次必须落空且无错, won=%v err=%v", won, err)
	}
	status, lease := taskState(t, conn, id)
	if status != "processing" || lease != 2 {
		t.Fatalf("状态必须原样, got %s/%d", status, lease)
	}
	assertOps(t, svc)
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE task_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("outbox 必须未插, got %d", n)
	}
}

// 终态只提交一次:已 failed 的任务第二次收口 CAS 直接落空。
func TestFinishTxDoubleTerminalIsSafeFailure(t *testing.T) {
	conn, svc, uid := newRecoverDBHarness(t)
	res, _ := conn.Exec(`INSERT INTO generation_tasks
		(user_id, model_id, status, input, frozen_cents, sink, lease_version)
		VALUES (?, 'm-f', 'processing', '{}', 40, '{"kind":"canvas_node"}', 2)`, uid)
	id, _ := res.LastInsertId()
	if won, _ := svc.finishFailureTx(context.Background(), id, 2, "第一次失败", "退款"); !won {
		t.Fatal("第一次收口应赢")
	}
	if won, _ := svc.finishFailureTx(context.Background(), id, 3, "第二次失败", "退款"); won {
		t.Fatal("终态任务绝不能被二次收口")
	}
	assertOps(t, svc, "RefundTx")
}
```

`server/internal/generation/delete_db_test.go`：

```go
package generation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// Delete 单事务化的真实 SQLite 验证：①终态删除的全部效果（行删、资产行删、孤儿登记、配额回收）
// 一次生效；②非终态被拦下时零副作用。

func newDeleteTestService(t *testing.T) (*Service, *sql.DB, *[]string) {
	t.Helper()
	conn := dbtest.Open(t)
	marked := &[]string{}
	svc := &Service{
		repo:      NewSQLiteRepository(conn),
		assets:    sqlTestAssets{db: conn},
		users:     sqlTestUsers{db: conn},
		reclaimer: sqlTestReclaimer{marked: marked},
		db:        conn,
	}
	return svc, conn, marked
}

func seedDeleteFixture(t *testing.T, db *sql.DB, status string) (uid, tid int64, key string) {
	t.Helper()
	uid = 1
	if _, err := db.Exec(`UPDATE users SET storage_used_bytes = 300 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	tres, err := db.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents) VALUES (?, 'm-del', ?, '{}', 0)`, uid, status)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ = tres.LastInsertId()
	key = fmt.Sprintf("gen/del-test-obj-%d", tid)
	if _, err := db.Exec(`INSERT INTO assets (user_id, source_task_id, object_key, source_kind, url, size_bytes, mime_type) VALUES (?, ?, ?, 'generation', 'media://x', 300, 'image/png')`, uid, tid, key); err != nil {
		t.Fatal(err)
	}
	return uid, tid, key
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteTerminalTaskFullEffectsAtomic(t *testing.T) {
	svc, db, marked := newDeleteTestService(t)
	uid, tid, key := seedDeleteFixture(t, db, "succeeded")

	if err := svc.Delete(context.Background(), uid, tid); err != nil {
		t.Fatalf("终态删除应成功: %v", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM generation_tasks WHERE id=?`, tid) != 0 {
		t.Fatal("任务行应已删除")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM assets WHERE source_task_id=?`, tid) != 0 {
		t.Fatal("资产行应已删除")
	}
	if len(*marked) != 1 || (*marked)[0] != key {
		t.Fatalf("失去全部引用的对象应登记孤儿, got %v", *marked)
	}
	if used := countRows(t, db, `SELECT storage_used_bytes FROM users WHERE id=?`, uid); used != 0 {
		t.Fatalf("配额应同事务回收到 0, got %d", used)
	}
}

func TestDeleteNonTerminalRejectedNoSideEffects(t *testing.T) {
	svc, db, marked := newDeleteTestService(t)
	uid, tid, _ := seedDeleteFixture(t, db, "running")

	if err := svc.Delete(context.Background(), uid, tid); !errors.Is(err, ErrTaskNotTerminal) {
		t.Fatalf("非终态应返回 ErrTaskNotTerminal, got %v", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM generation_tasks WHERE id=?`, tid) != 1 {
		t.Fatal("任务行不该被动过")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM assets WHERE source_task_id=?`, tid) != 1 {
		t.Fatal("资产行不该被动过")
	}
	if len(*marked) != 0 {
		t.Fatalf("不该有孤儿登记, got %v", *marked)
	}
	if used := countRows(t, db, `SELECT storage_used_bytes FROM users WHERE id=?`, uid); used != 300 {
		t.Fatalf("配额不该变, got %d", used)
	}
}
```

`server/internal/generation/tx_submit_db_test.go`：

```go
package generation_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// SubmitTx 的事务不变量在真实 SQLite 上验证：建行与冻结同事务；冻结失败整体回滚、不留任务行。
// 桌面端计费是空实现，这里用可注入失败的替身钉住「冻结失败 → 回滚」这条结构性保证。

type txTestModels struct {
	price      int64
	genControl string
}

func (m txTestModels) GetByModelID(modelID string) (*models.ModelConfig, error) {
	raw, _ := json.Marshal(map[string]any{"flatFee": m.price})
	mc := &models.ModelConfig{ModelID: modelID, Enabled: true, BillingConfigRaw: raw}
	if m.genControl != "" {
		mc.GenControlRaw = json.RawMessage(m.genControl)
	}
	return mc, nil
}

type txTestBilling struct {
	freezeErr error
	freezes   *[]int64
}

func (b txTestBilling) Freeze(context.Context, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) Settle(context.Context, int64, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) Refund(context.Context, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) FreezeTx(_ context.Context, _ *sql.Tx, _, _, a int64, _ string) (generation.BillingResult, error) {
	*b.freezes = append(*b.freezes, a)
	return generation.BillingResult{}, b.freezeErr
}
func (b txTestBilling) SettleTx(context.Context, *sql.Tx, int64, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}
func (b txTestBilling) RefundTx(context.Context, *sql.Tx, int64, int64, int64, string) (generation.BillingResult, error) {
	return generation.BillingResult{}, nil
}

const uid = int64(1)

func newSubmitTxHarness(t *testing.T, ml generation.ModelLookup, freezeErr error, st generation.AssetStorage, ar generation.AssetRecorder) (*sql.DB, *generation.Service, *[]int64) {
	t.Helper()
	conn := dbtest.Open(t)
	freezes := &[]int64{}
	svc := generation.NewService(
		generation.NewSQLiteRepository(conn), ml, nil, nil,
		txTestBilling{freezeErr: freezeErr, freezes: freezes},
		nil, st, ar, nil, nil, conn,
	)
	return conn, svc, freezes
}

func TestSubmitTxCommitCreatesRowAndFreeze(t *testing.T) {
	conn, svc, freezes := newSubmitTxHarness(t, txTestModels{price: 30}, nil, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	r, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m-fixed",
		Inputs: map[string]any{"prompt": "画只猫"},
		Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1"}`),
	})
	if err != nil {
		t.Fatalf("SubmitTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var status, inputType, sinkType string
	var frozen int64
	if err := conn.QueryRow(`SELECT status, frozen_cents, typeof(input), typeof(sink) FROM generation_tasks WHERE id=?`, r.TaskID).
		Scan(&status, &frozen, &inputType, &sinkType); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "queued" || frozen != 30 || inputType != "text" || sinkType != "text" {
		t.Fatalf("got status=%s frozen=%d input=%s sink=%s", status, frozen, inputType, sinkType)
	}
	if len(*freezes) != 1 || (*freezes)[0] != 30 {
		t.Fatalf("应在同一事务内冻结一次 30, got %v", *freezes)
	}
}

func TestSubmitTxFreezeErrorRollsBackEverything(t *testing.T) {
	conn, svc, _ := newSubmitTxHarness(t, txTestModels{price: 500}, billing.ErrInsufficientBalance, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	_, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m-fixed",
		Inputs: map[string]any{"prompt": "太贵了"},
		Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n2"}`),
	})
	if !errors.Is(err, billing.ErrInsufficientBalance) {
		t.Fatalf("冻结错误应原样透传, got %v", err)
	}
	_ = tx.Rollback()
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE user_id=?`, uid).Scan(&n)
	if n != 0 {
		t.Fatalf("回滚后任务行不该存在, got %d 行", n)
	}
}

func TestSubmitTxRejectsSinkless(t *testing.T) {
	conn, svc, _ := newSubmitTxHarness(t, txTestModels{price: 1}, nil, nil, nil)
	tx, _ := conn.BeginTx(context.Background(), nil)
	defer tx.Rollback()
	if _, err := svc.SubmitTx(context.Background(), tx, generation.SubmitInput{
		UserID: uid, ModelID: "m", Inputs: map[string]any{},
	}); err == nil {
		t.Fatal("sinkless 必须被拒——它是同步语义,与队列执行不兼容")
	}
}

type durStorage struct{ generation.AssetStorage }

func (durStorage) ExtractKey(s string) (string, bool) { return "u1/v", s != "" }

type durAssets struct {
	generation.AssetRecorder
	setCalled bool
}

func (*durAssets) GetDurationMsByObjectKey(string) (int64, bool, error) { return 0, false, nil }
func (d *durAssets) SetDurationMsByObjectKey(string, int64) error {
	d.setCalled = true
	return nil
}

// Review Focus 5：带 inputDuration 的模型走 sink 路径时，resolveInputDuration 在写事务里执行。
// 任何经 r.db 的写都会让本次提交卡 10 秒后 SQLITE_BUSY。
func TestSubmitWithInputDurationModelDoesNotSelfDeadlock(t *testing.T) {
	as := &durAssets{}
	conn, svc, _ := newSubmitTxHarness(t,
		txTestModels{price: 1, genControl: `{"inputDuration":{"from":"video"}}`}, nil, durStorage{}, as)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Submit(context.Background(), generation.SubmitInput{
			UserID: uid, ModelID: "m-edit",
			Inputs: map[string]any{"prompt": "x", "videos": []any{"media://u1/v"}},
			Sink:   json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n3"}`),
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("提交超过 5 秒没有返回：写事务内经 r.db 写库导致自死锁")
	}
	if as.setCalled {
		t.Fatal("不得在提交路径回填 duration_ms")
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE status='queued'`).Scan(&n)
	if n != 1 {
		t.Fatalf("应有 1 条 queued 任务, got %d", n)
	}
}
```

- [ ] **Step 3: 写方言专项测试**

`server/internal/generation/dialect_db_test.go`：

```go
package generation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 针对 MySQL → SQLite 改写点的专项测试（方言清单里每一处改写至少被一个用例经过）。

func TestListSearchMatchesNestedInputStrings(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	for _, in := range []string{
		`{"modelId":"m","inputs":{"prompt":"一只橘猫在跳舞"}}`,
		`{"modelId":"m","inputs":{"prompt":"海边日落","images":["media://a"]}}`,
	} {
		if _, err := repo.Create(Task{UserID: 1, ModelID: "m", Status: "succeeded", Input: json.RawMessage(in)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		search string
		want   int
	}{{"橘猫", 1}, {"日落", 1}, {"狗", 0}, {"prompt", 0}} { // JSON_SEARCH 只搜值不搜键
		_, total, err := repo.List(ListFilter{UserID: 1, Search: tc.search})
		if err != nil {
			t.Fatalf("search=%q: %v", tc.search, err)
		}
		if int(total) != tc.want {
			t.Fatalf("search=%q total=%d want %d", tc.search, total, tc.want)
		}
	}
}

func TestPromoteDueRetriesRespectsLimit(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	for i := 0; i < 3; i++ {
		if _, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, sink, next_retry_at)
			VALUES (1, 'm', 'retry_pending', '{}', '{"kind":"canvas_node"}', datetime('now','-1 minutes'))`); err != nil {
			t.Fatal(err)
		}
	}
	n, err := repo.PromoteDueRetries(2)
	if err != nil || n != 2 {
		t.Fatalf("应只放回 2 个, n=%d err=%v", n, err)
	}
	var left int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_tasks WHERE status='retry_pending'`).Scan(&left)
	if left != 1 {
		t.Fatalf("剩余 retry_pending=%d want 1", left)
	}
}

func TestClaimStaleRunningReturnsNewLease(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, owner_instance, heartbeat_at, lease_version)
		VALUES (1, 'm', 'processing', '{}', 'old', datetime('now','-10 minutes'), 3)`)
	id, _ := res.LastInsertId()
	lease, won, err := repo.ClaimStaleRunning(id, "me", 90)
	if err != nil || !won || lease != 4 {
		t.Fatalf("应接管并拿到代次 4, lease=%d won=%v err=%v", lease, won, err)
	}
	if _, won, _ := repo.ClaimStaleRunning(id, "other", 90); won {
		t.Fatal("刚接管、心跳新鲜的任务不能再被接管")
	}
}

func TestListProcessingStaleFilter(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	ins := func(hb string) int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, upstream_task_id, owner_instance, heartbeat_at)
			VALUES (1, 'm', 'processing', '{}', 'up', 'x', `+hb+`)`)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	stale := ins(`datetime('now','-10 minutes')`)
	ins(`CURRENT_TIMESTAMP`)
	got, err := repo.ListProcessing(0, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != stale {
		t.Fatalf("只应取到心跳过期的那条, got %+v", got)
	}
}

func TestUpdateOutputKeepsKindWhenEmpty(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	res, _ := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output, output_kind)
		VALUES (1, 'm', 'succeeded', '{}', '{}', 'video')`)
	id, _ := res.LastInsertId()
	if err := repo.UpdateOutput(id, json.RawMessage(`{"outputUrls":["media://k"]}`), ""); err != nil {
		t.Fatal(err)
	}
	var kind, typ string
	_ = conn.QueryRow(`SELECT output_kind, typeof(output) FROM generation_tasks WHERE id=?`, id).Scan(&kind, &typ)
	if kind != "video" || typ != "text" {
		t.Fatalf("空 outputKind 不应覆盖原值, kind=%s type=%s", kind, typ)
	}
	if err := repo.UpdateOutput(id, json.RawMessage(`{}`), "image"); err != nil {
		t.Fatal(err)
	}
	_ = conn.QueryRow(`SELECT output_kind FROM generation_tasks WHERE id=?`, id).Scan(&kind)
	if kind != "image" {
		t.Fatalf("非空 outputKind 应覆盖, got %s", kind)
	}
}

func TestOutboxPayloadStoredAsText(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	tx, _ := conn.BeginTx(context.Background(), nil)
	if err := repo.InsertOutboxTx(tx, 5, json.RawMessage(`{"kind":"canvas_node"}`), SinkResult{OutputURLs: []string{"media://k"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var s, p string
	_ = conn.QueryRow(`SELECT typeof(sink), typeof(payload) FROM generation_sink_outbox WHERE task_id=5`).Scan(&s, &p)
	if s != "text" || p != "text" {
		t.Fatalf("sink=%s payload=%s want text", s, p)
	}
}
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run 'DB|Takeover|Sweep|Finish|Delete|SubmitTx|SubmitWith|Claim|Heartbeat|Stale|Retry|Outbox|Latest|ListFilter|ListSearch|ListProcessing|UpdateOutput|Persist'`
Expected: FAIL，典型报错 `no such function: NOW`、`near "LIMIT": syntax error`、`no such function: JSON_SEARCH`、`CHECK constraint failed`。

- [ ] **Step 5: 改写 repository.go**

在 `server/internal/generation/repository.go`：

1. import 加 `"fmt"`；在 `ErrDataURIInput` 之后加：

```go
// sqlSecondsOffset 生成 datetime('now', ?) 的修饰串：-90 → "-90 seconds"，30 → "+30 seconds"。
// 替代 MySQL 的 DATE_SUB/DATE_ADD(NOW(), INTERVAL ? SECOND)；结果与 CURRENT_TIMESTAMP 同格式，可直接比较。
func sqlSecondsOffset(sec int) string { return fmt.Sprintf("%+d seconds", sec) }
```

2. `LatestByCanvasNode` 的 WHERE 第二行改为：`AND json_extract(sink, '$.kind') = 'canvas_node' AND json_extract(sink, '$.nodeId') = ?`。
3. `List` 的搜索条件改为：

```go
	if f.Search != "" {
		// JSON_SEARCH(input,'one',?) 的 SQLite 等价：递归遍历 input 里所有字符串值做 LIKE（不匹配键名）。
		conds = append(conds, "EXISTS (SELECT 1 FROM json_tree(generation_tasks.input) jt WHERE jt.type = 'text' AND jt.atom LIKE ?)")
		args = append(args, "%"+f.Search+"%")
	}
```

   并把两阶段取数上方注释改为「两阶段取数：内层只对 id 排序分页，外层按主键取宽行。SQLite 下没有 sort buffer 问题，保留写法是为了与参考仓同形。」
4. `ListProcessing` 两处条件改为：

```go
	if maxAgeSec > 0 {
		q += ` AND created_at >= datetime('now', ?)`
		args = append(args, sqlSecondsOffset(-maxAgeSec))
	}
	if staleSec > 0 {
		q += ` AND (owner_instance IS NULL OR heartbeat_at IS NULL OR heartbeat_at < datetime('now', ?))`
		args = append(args, sqlSecondsOffset(-staleSec))
	}
```

5. `Heartbeat`：`SET heartbeat_at=NOW()` → `SET heartbeat_at=CURRENT_TIMESTAMP`；把「0 行 ≠ 一定失租…MySQL 按 changed rows 报 0」那段注释改为「SQLite 的 RowsAffected 是匹配行数，同秒续租也返回 1；保留按代次复核作为失租的唯一判据。」
6. `MarkRunningDirectTx`、`MarkUpstreamCalled`：`NOW()` → `CURRENT_TIMESTAMP`。
7. `RunningCountByModel`：`AND heartbeat_at > DATE_SUB(NOW(), INTERVAL ? SECOND)` → `AND heartbeat_at > datetime('now', ?)`，参数 `freshSec` → `sqlSecondsOffset(-freshSec)`。
8. `ClaimQueued`、`ClaimStaleRunning` 整个函数体替换为：

```go
// ClaimQueued CAS 领取。WHERE status='queued' 保证并发领取恰好一个成功；
// 新代次取自同一条语句的 RETURNING（替代 MySQL 的 LAST_INSERT_ID(expr)）。
func (r *SQLiteRepository) ClaimQueued(taskID int64, instance string) (int64, bool, error) {
	var lease int64
	err := r.db.QueryRow(`UPDATE generation_tasks
		SET status='running', owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP,
		    lease_version = lease_version + 1
		WHERE id=? AND status='queued'
		RETURNING lease_version`, instance, taskID).Scan(&lease)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return lease, lease > 0, nil
}

// ClaimStaleRunning 接管一个心跳超时的 running/processing/settling 任务：CAS 抢占 + 代次+1，
// 旧执行者此后的一切写都会落空。
func (r *SQLiteRepository) ClaimStaleRunning(taskID int64, instance string, staleSec int) (int64, bool, error) {
	var lease int64
	err := r.db.QueryRow(`UPDATE generation_tasks
		SET owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP,
		    lease_version = lease_version + 1
		WHERE id=? AND status IN ('running','processing','settling')
		  AND (owner_instance IS NULL OR heartbeat_at IS NULL OR heartbeat_at < datetime('now', ?))
		RETURNING lease_version`, instance, taskID, sqlSecondsOffset(-staleSec)).Scan(&lease)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return lease, lease > 0, nil
}
```

9. `MarkRetryPending`：`next_retry_at=DATE_ADD(NOW(), INTERVAL ? SECOND)` → `next_retry_at=datetime('now', ?)`，对应参数 `intervalSec` → `sqlSecondsOffset(intervalSec)`。
10. `PromoteDueRetries` 的 Exec 替换为：

```go
	// SQLite 不支持 UPDATE … LIMIT：先在子查询里按 id 选出这一批。
	res, err := r.db.Exec(`UPDATE generation_tasks
		SET status='queued', owner_instance=NULL, heartbeat_at=NULL, lease_version=lease_version+1
		WHERE id IN (
			SELECT id FROM generation_tasks
			 WHERE status='retry_pending' AND next_retry_at <= CURRENT_TIMESTAMP
			 ORDER BY id LIMIT ?)`, limit)
```

11. `ListStaleRunning`、`ListQueuedOlderThan`、`ListQueuedSinkless`：`DATE_SUB(NOW(), INTERVAL ? SECOND)` → `datetime('now', ?)`，参数 `staleSec` / `sec` → `sqlSecondsOffset(-staleSec)` / `sqlSecondsOffset(-sec)`。
12. 删掉 `// ---- RepersistStore` 小节里的 `repersistTimeLayout` 常量、`ListExternalOutputs`、`MarkRepersistAttempt`（S5 改为手动重试，不再有补搬 worker）；`UpdateOutput` 替换为：

```go
// UpdateOutput 整体替换 output JSON；outputKind 非空时同步 output_kind 列。只改仍是 succeeded 的行。
func (r *SQLiteRepository) UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error {
	_, err := r.db.Exec(
		`UPDATE generation_tasks SET output=?, output_kind=COALESCE(NULLIF(?, ''), output_kind) WHERE id=? AND status='succeeded'`,
		string(output), outputKind, taskID)
	return err
}
```

- [ ] **Step 6: 改写 outbox.go**

在 `server/internal/generation/outbox.go`：

1. 文件头 fencing 注释里「代次取值只能用同一次 Exec 的 Result.LastInsertId()…」改为「代次取自领取语句的 `RETURNING delivery_version`」。
2. `InsertOutboxTx` 的参数 `taskID, []byte(sink), payload` → `taskID, string(sink), string(payload)`。
3. `claimOutboxDue`：候选查询里两处 `NOW()` → `CURRENT_TIMESTAMP`；循环体里从 `res, err := r.db.Exec(` 到 `version, err := res.LastInsertId() …` 的部分替换为：

```go
		var version int64
		err := r.db.QueryRow(`UPDATE generation_sink_outbox
			SET state='delivering', locked_until = datetime('now', ?),
			    attempt = attempt + 1, delivery_version = delivery_version + 1
			WHERE id = ?
			  AND ((state='pending' AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP))
			    OR (state='delivering' AND locked_until < CURRENT_TIMESTAMP))
			RETURNING delivery_version`, sqlSecondsOffset(outboxLeaseSec), id).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			continue // 别的 worker 先领走了
		}
		if err != nil {
			return nil, err
		}
```

   import 加 `"errors"`。
4. `failOutbox` 退避分支：`next_retry_at = DATE_ADD(NOW(), INTERVAL ? SECOND)` → `next_retry_at = datetime('now', ?)`，参数 `backoff` → `sqlSecondsOffset(backoff)`。

- [ ] **Step 7: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/generation/ && go test -tags p1cshim -race ./internal/generation/`
Expected: 全部 PASS，无 SKIP。再确认没有方言残留：

Run: `cd /Users/james/github/miaoju-desktop/server && grep -n 'NOW()\|DATE_SUB\|DATE_ADD\|INTERVAL\|LAST_INSERT_ID\|JSON_SEARCH\|JSON_EXTRACT\|JSON_SET\|->>\|IF(\|\[\]byte(task\|\[\]byte(out\|\[\]byte(sink' internal/generation/*.go | grep -v _test.go`
Expected: 无输出。

- [ ] **Step 8: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/generation
git commit -m "feat(server): generation 仓库与 outbox 改写为 SQLite 方言，移植 10 个真实库测试"
```

---

### Task 5: S5 产物下载重试与手动「保存到本地」

**Files:**
- Create: `server/internal/generation/persist_retry.go`、`server/internal/generation/persist_retry_test.go`
- Modify: `server/internal/generation/service.go`（`persistOutputs` 内一行）、`server/internal/generation/repersist.go`（整体重写）、`server/internal/generation/persist_local_test.go`（一处）、`server/internal/generation/repersist_test.go`（整体重写）
- Modify: `server/internal/canvasgen/service.go`（`OnGenerationResult` 成功分支）
- Create: `server/internal/canvasgen/repersist_apply.go`、`server/internal/canvasgen/repersist_apply_test.go`
- Create: `server/internal/http/generation_repersist_handlers.go`、`server/internal/http/generation_repersist_handlers_test.go`

**Interfaces:**
- Consumes: Task 3 的 `Service.persistSlot`、`Service.repo`、`Service.storage`、`waitWithContext`、`outputKindFromMime`、`anyStrings`、`anyInt64s`、`durationMsFromInputs`；canvasgen 的 `writeNode`、`nodeSettings`、`canvasSinkRef`、`sinkKindCanvasNode`；Task 2 的 http shim
- Produces:
  - `generation.RepersistReplacement{Index int; OldURL, NewURL string; AssetID int64}`
  - `generation.RepersistResult{Sink json.RawMessage; OutputURLs []string; AssetIDs []int64; Replacements []RepersistReplacement}`
  - `generation.ErrNothingToRepersist`
  - `(*generation.Service).RepersistTask(ctx context.Context, userID, taskID int64) (*RepersistResult, error)`
  - `generation.RepersistStore` 接口收窄为 `UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error`
  - `(*canvasgen.Service).ApplyRepersist(ctx context.Context, sink json.RawMessage, reps []generation.RepersistReplacement) error`
  - 画布节点约定（P3a 消费）：产物没能落本地时 `settings.unsaved = true`、`settings.genTaskId = <任务 id>`；重试成功后两个键被删除
  - `http.NewGenerationRepersistHandler(gen repersistGen, canvas repersistCanvas, signer repersistSigner) *GenerationRepersistHandler`、`(*GenerationRepersistHandler).Repersist`、`registerRepersistRoute(r chi.Router, h *GenerationRepersistHandler)`（注册 `POST /api/v1/tasks/{taskID}/repersist`）

- [ ] **Step 1: 写下载重试的失败测试**

`server/internal/generation/persist_retry_test.go`：

```go
package generation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func zeroPersistRetryDelays(t *testing.T) {
	t.Helper()
	old := persistRetryDelays
	persistRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { persistRetryDelays = old })
}

// 前 failures 次返回 500，之后返回 PNG。
func flakyServer(t *testing.T, failures int32) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= failures {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nDATA"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPersistOutputsRetriesRemoteDownloadThreeTimes(t *testing.T) {
	zeroPersistRetryDelays(t)
	srv, hits := flakyServer(t, 3)
	st := &captureStorage{}
	svc := &Service{storage: st, httpClient: srv.Client()}
	urls, ids, mimes, err := svc.persistOutputs(context.Background(), 1, 77, nil, []string{srv.URL + "/a.png"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(hits) != 4 {
		t.Fatalf("首次 + 重试 3 次 = 4 次请求, got %d", *hits)
	}
	if !strings.HasPrefix(urls[0], "media://") || mimes[0] != "image/png" {
		t.Fatalf("第 4 次成功后应落本地, url=%s mime=%s", urls[0], mimes[0])
	}
	_ = ids
}

func TestPersistOutputsGivesUpAfterThreeRetries(t *testing.T) {
	zeroPersistRetryDelays(t)
	srv, hits := flakyServer(t, 100)
	svc := &Service{storage: &captureStorage{}, httpClient: srv.Client()}
	raw := srv.URL + "/a.png?x=1&y=2"
	urls, ids, _, err := svc.persistOutputs(context.Background(), 1, 78, nil, []string{raw}, 0)
	if err != nil {
		t.Fatalf("http 产物下载失败不应让任务失败: %v", err)
	}
	if atomic.LoadInt32(hits) != 4 {
		t.Fatalf("应恰好尝试 4 次, got %d", *hits)
	}
	if urls[0] != raw || ids[0] != 0 {
		t.Fatalf("应保留上游地址且 assetId=0, url=%s id=%d", urls[0], ids[0])
	}
}

func TestPersistOutputsDoesNotRetryDataURL(t *testing.T) {
	zeroPersistRetryDelays(t)
	st := &countingFailStorage{}
	svc := &Service{storage: st}
	_, _, _, _ = svc.persistOutputs(context.Background(), 1, 79, nil, []string{"data:image/png;base64,aGk="}, 0)
	if st.puts != 1 {
		t.Fatalf("data: 产物失败是确定性的，不应重试, puts=%d", st.puts)
	}
}

type countingFailStorage struct {
	fakeInputMediaStorage
	puts int
}

func (c *countingFailStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	c.puts++
	return assets.StoredObject{}, errors.New("disk full")
}
```

本文件完整 import 块：

```go
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run 'PersistOutputs'`
Expected: 编译失败，`undefined: persistRetryDelays`。

- [ ] **Step 3: 实现下载重试**

`server/internal/generation/persist_retry.go`：

```go
package generation

import (
	"context"
	"log"
	"strings"
	"time"
)

// persistRetryDelays 是单个产物槽位下载/落盘失败后的重试间隔（spec S5：失败自动重试 3 次）。
// 包级变量而非 Service 字段：测试置零即可，生产不需要配置。
var persistRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

func isRemoteURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// persistSlotWithRetry 在 persistSlot 外包一层重试。只重试 http(s) 产物——data: 解码失败、
// file:// 读失败都是确定性的，重试没有意义。重试全部失败时返回最后一次的错误，由
// persistOutputs 保留上游地址（任务照常成功，节点显示「未保存到本地」）。
func (s *Service) persistSlotWithRetry(ctx context.Context, userID, taskID, projectID int64, i int, rawURL string, durationMs int64) (string, int64, string, error) {
	stored, id, mime, err := s.persistSlot(ctx, userID, taskID, projectID, i, rawURL, durationMs)
	if err == nil || !isRemoteURL(rawURL) {
		return stored, id, mime, err
	}
	for n, d := range persistRetryDelays {
		if werr := waitWithContext(ctx, d); werr != nil {
			return "", 0, "", err
		}
		log.Printf("generation: persist output retry %d/%d task=%d idx=%d after: %v", n+1, len(persistRetryDelays), taskID, i, err)
		stored, id, mime, err = s.persistSlot(ctx, userID, taskID, projectID, i, rawURL, durationMs)
		if err == nil {
			return stored, id, mime, nil
		}
	}
	return "", 0, "", err
}
```

`server/internal/generation/service.go` 的 `persistOutputs` 里（参考仓 :1371）：

```go
		stored, assetID, mime, err := s.persistSlot(ctx, userID, taskID, pid, i, rawURL, durationMs)
```

改为：

```go
		stored, assetID, mime, err := s.persistSlotWithRetry(ctx, userID, taskID, pid, i, rawURL, durationMs)
```

并把其下方 http 失败分支注释里「补搬由 RunRepersistWorker 按 outputUrls 形态扫出来重试」改为「已自动重试 3 次；之后由用户在节点上点「重试」走 RepersistTask（spec S5）」。

`server/internal/generation/persist_local_test.go`：在 `TestPersistOutputs_HTTPFailureStillKeepsUpstreamURL` 函数体第一行插入 `zeroPersistRetryDelays(t)`（否则会真的等 17 秒）。

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run 'PersistOutputs'`
Expected: PASS。

- [ ] **Step 5: 写手动重试的失败测试（generation）**

`server/internal/generation/repersist_test.go` 整个替换为：

```go
package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRepersistStore struct {
	updated map[int64]json.RawMessage
	kinds   map[int64]string
}

func (f *fakeRepersistStore) UpdateOutput(id int64, out json.RawMessage, kind string) error {
	if f.updated == nil {
		f.updated, f.kinds = map[int64]json.RawMessage{}, map[int64]string{}
	}
	f.updated[id] = out
	f.kinds[id] = kind
	return nil
}

// repersistRepo 在 fakeRecoverRepo 上覆盖 Get / SinkForTask。
type repersistRepo struct {
	fakeRecoverRepo
	task *Task
	sink json.RawMessage
}

func (r *repersistRepo) Get(userID, taskID int64) (*Task, error) {
	if r.task == nil || r.task.ID != taskID || r.task.UserID != userID {
		return nil, ErrNotFound
	}
	return r.task, nil
}

func (r *repersistRepo) SinkForTask(int64) (json.RawMessage, error) { return r.sink, nil }

func pngServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nDATA"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRepersistTaskMovesUnsavedSlots(t *testing.T) {
	srv := pngServer(t)
	old := srv.URL + "/1.png?a=1&b=2"
	pid := int64(9)
	repo := &repersistRepo{
		task: &Task{ID: 7, UserID: 1, ProjectID: &pid, Status: "succeeded",
			Output: json.RawMessage(`{"outputUrls":["` + old + `","media://kept"],"assetIds":[0,11]}`)},
		sink: json.RawMessage(`{"kind":"canvas_node","projectId":9,"nodeId":"n1","userId":1}`),
	}
	store := &fakeRepersistStore{}
	svc := &Service{repo: repo, storage: &captureStorage{}, httpClient: srv.Client(), repersist: store}

	res, err := svc.RepersistTask(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Replacements) != 1 || res.Replacements[0].Index != 0 || res.Replacements[0].OldURL != old {
		t.Fatalf("只应搬第 0 个槽位, got %+v", res.Replacements)
	}
	if !strings.HasPrefix(res.OutputURLs[0], "media://") || res.OutputURLs[1] != "media://kept" {
		t.Fatalf("outputUrls=%v", res.OutputURLs)
	}
	if string(res.Sink) != string(repo.sink) {
		t.Fatalf("应带回 sink 供画布回写, got %s", res.Sink)
	}
	var out map[string]any
	_ = json.Unmarshal(store.updated[7], &out)
	if urls := anyStrings(out["outputUrls"]); !strings.HasPrefix(urls[0], "media://") {
		t.Fatalf("output 应已回写本地地址, got %v", urls)
	}
	if store.kinds[7] != "image" {
		t.Fatalf("outputKind 应按实测 mime 回写 image, got %q", store.kinds[7])
	}
}

func TestRepersistTaskNothingToDo(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 8, UserID: 1, Status: "succeeded",
		Output: json.RawMessage(`{"outputUrls":["media://a"],"assetIds":[3]}`)}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 8); !errors.Is(err, ErrNothingToRepersist) {
		t.Fatalf("全部已在本地应返回 ErrNothingToRepersist, got %v", err)
	}
}

func TestRepersistTaskRejectsNonSucceeded(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 9, UserID: 1, Status: "processing"}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 9); !errors.Is(err, ErrNothingToRepersist) {
		t.Fatalf("未成功的任务没有可保存的产物, got %v", err)
	}
}

func TestRepersistTaskNotFoundForOtherUser(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 10, UserID: 2, Status: "succeeded"}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
```

- [ ] **Step 6: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run 'Repersist'`
Expected: 编译失败，`unknown field repersist`、`undefined: ErrNothingToRepersist`。

- [ ] **Step 7: 重写 repersist.go，并给 Service 加字段**

`server/internal/generation/repersist.go` 整个替换为：

```go
package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 手动「保存到本地」（spec S5）：产物首搬重试 3 次仍失败时，任务照常成功、output 里保留上游
// 外链（assetIds 对应位置为 0），画布节点显示「未保存到本地」和重试按钮；用户点重试走这里。
// 参考仓的 RunRepersistWorker（每 5 分钟自动补搬、翻倍退避 10 次）桌面端不启用：spec 要求的是
// 「重试 3 次后提示并可手动重试」。补搬与首搬共用 persistSlot，key / URL / asset 行逐字同形。

// ErrNothingToRepersist：任务不是成功态，或所有产物都已在本地。HTTP 层映射 409。
var ErrNothingToRepersist = errors.New("该任务没有需要保存到本地的产物")

// RepersistStore 是补搬需要的窄写接口（SQLiteRepository 实现）。
type RepersistStore interface {
	// UpdateOutput 整体替换 output JSON；outputKind 非空时同步 output_kind 列。
	UpdateOutput(taskID int64, output json.RawMessage, outputKind string) error
}

// RepersistReplacement 是一个被搬回本地的槽位。
type RepersistReplacement struct {
	Index   int
	OldURL  string
	NewURL  string
	AssetID int64
}

// RepersistResult 交给 HTTP 层：Sink 用于回写画布节点，OutputURLs/AssetIDs 是回写后的整组产物。
type RepersistResult struct {
	Sink         json.RawMessage
	OutputURLs   []string
	AssetIDs     []int64
	Replacements []RepersistReplacement
}

func (s *Service) repersistStore() RepersistStore {
	if s.repersist != nil {
		return s.repersist
	}
	if r, ok := s.repo.(RepersistStore); ok {
		return r
	}
	return nil
}

// RepersistTask 把一个成功任务里仍是上游外链的产物搬回本地。任一槽位失败整体返回错误
// （已搬成功的槽位 asset 行留着，下次重试经 FindByTaskSlot 直接复用，不重复下载）。
func (s *Service) RepersistTask(ctx context.Context, userID, taskID int64) (*RepersistResult, error) {
	t, err := s.repo.Get(userID, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != "succeeded" {
		return nil, ErrNothingToRepersist
	}
	store := s.repersistStore()
	if store == nil || s.storage == nil {
		return nil, errors.New("repersist unavailable")
	}
	urls, assetIDs, reps, err := s.repersistTask(ctx, store, *t)
	if err != nil {
		return nil, err
	}
	sink, err := s.repo.SinkForTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("load sink: %w", err)
	}
	return &RepersistResult{Sink: sink, OutputURLs: urls, AssetIDs: assetIDs, Replacements: reps}, nil
}

func (s *Service) repersistTask(ctx context.Context, store RepersistStore, t Task) ([]string, []int64, []RepersistReplacement, error) {
	var output map[string]any
	if err := json.Unmarshal(t.Output, &output); err != nil {
		return nil, nil, nil, fmt.Errorf("output 不是合法 JSON: %w", err)
	}
	urls := anyStrings(output["outputUrls"])
	assetIDs := anyInt64s(output["assetIds"], len(urls))
	pid := int64(0)
	if t.ProjectID != nil {
		pid = *t.ProjectID
	}
	durMs := int64(0)
	if len(t.Input) > 0 {
		var in struct {
			Inputs map[string]any `json:"inputs"`
		}
		if json.Unmarshal(t.Input, &in) == nil {
			durMs = durationMsFromInputs(in.Inputs)
		}
	}

	var reps []RepersistReplacement
	outputKind := ""
	for i, raw := range urls {
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			continue
		}
		newURL, assetID, mime, err := s.repersistSlot(ctx, t.UserID, t.ID, pid, i, raw, durMs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("slot=%d: %w", i, err)
		}
		urls[i] = newURL
		assetIDs[i] = assetID
		if i == 0 {
			outputKind = outputKindFromMime(mime)
		}
		reps = append(reps, RepersistReplacement{Index: i, OldURL: raw, NewURL: newURL, AssetID: assetID})
	}
	if len(reps) == 0 {
		return nil, nil, nil, ErrNothingToRepersist
	}
	output["outputUrls"] = urls
	output["assetIds"] = assetIDs
	if outputKind != "" {
		output["outputKind"] = outputKind
	}
	next, err := json.Marshal(output)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := store.UpdateOutput(t.ID, next, outputKind); err != nil {
		return nil, nil, nil, fmt.Errorf("回写 output: %w", err)
	}
	return urls, assetIDs, reps, nil
}

// repersistSlot 先查槽位复用(上一轮可能对象已传成功、只是后面的回写失败),再走 persistSlot。
func (s *Service) repersistSlot(ctx context.Context, userID, taskID, projectID int64, i int, raw string, durMs int64) (string, int64, string, error) {
	if s.assets != nil {
		if existing, err := s.assets.FindByTaskSlot(taskID, i); err == nil && existing != nil {
			return existing.URL, existing.ID, existing.MimeType, nil
		}
	}
	return s.persistSlot(ctx, userID, taskID, projectID, i, raw, durMs)
}

func anyStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func anyInt64s(v any, n int) []int64 {
	out := make([]int64, n)
	arr, _ := v.([]any)
	for i := 0; i < n && i < len(arr); i++ {
		switch x := arr[i].(type) {
		case float64:
			out[i] = int64(x)
		case json.Number:
			f, _ := x.Int64()
			out[i] = f
		}
	}
	return out
}
```

`server/internal/generation/service.go` 的 `Service` 结构体**末尾**（`promptPreset` 字段之后）加一个字段：

```go
	// repersist 只供测试注入；nil 时 repersistStore() 回退到 repo（SQLiteRepository 实现 UpdateOutput）。
	repersist RepersistStore
```

（Task 6 也会在 `Service` 结构体加字段；两边都加在末尾，合并冲突时两行都保留。）

- [ ] **Step 8: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/`
Expected: 全部 PASS。

- [ ] **Step 9: 写画布侧的失败测试**

`server/internal/canvasgen/repersist_apply_test.go`：

```go
package canvasgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

func nodeOf(t *testing.T, payload json.RawMessage, id string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	for _, raw := range doc["nodes"].([]any) {
		if n := raw.(map[string]any); n["id"] == id {
			return n
		}
	}
	t.Fatalf("node %s not found", id)
	return nil
}

func TestOnGenerationResultMarksUnsaved(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"n1","type":"gen-image","settings":{}}]}`)}
	s := &Service{snaps: fake, now: time.Now}
	ref := json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1","userId":1}`)
	err := s.OnGenerationResult(context.Background(), ref, generation.SinkResult{
		TaskID: 42, OutputURLs: []string{"https://cdn.example.com/a.png?x=1&y=2"}, AssetIDs: []int64{0},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := nodeOf(t, fake.result, "n1")["settings"].(map[string]any)
	if st["unsaved"] != true || st["genTaskId"] != float64(42) {
		t.Fatalf("未落本地应标 unsaved + genTaskId, got %v", st)
	}
}

func TestOnGenerationResultLocalOutputNotUnsaved(t *testing.T) {
	fake := &fakeMutator{payload: json.RawMessage(`{"nodes":[{"id":"n1","type":"gen-image","settings":{"unsaved":true,"genTaskId":1}}]}`)}
	s := &Service{snaps: fake, now: time.Now}
	ref := json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n1"}`)
	if err := s.OnGenerationResult(context.Background(), ref, generation.SinkResult{
		TaskID: 43, OutputURLs: []string{"media://u1/gen/task-43-0"}, AssetIDs: []int64{5},
	}); err != nil {
		t.Fatal(err)
	}
	st := nodeOf(t, fake.result, "n1")["settings"].(map[string]any)
	if _, ok := st["unsaved"]; ok {
		t.Fatalf("落了本地的结果不应带 unsaved, got %v", st)
	}
	if _, ok := st["genTaskId"]; ok {
		t.Fatalf("genTaskId 应一并清掉, got %v", st)
	}
}

// URL 带 & 时 json.Marshal 会写成 &，按字节替换（RewriteMediaRef）改不到；
// ApplyRepersist 按节点结构改，必须改对。
func TestApplyRepersistRewritesNodeAndClearsFlag(t *testing.T) {
	old := "https://cdn.example.com/a.png?x=1&y=2"
	snap, _ := json.Marshal(map[string]any{"nodes": []any{map[string]any{
		"id": "n1", "type": "gen-image", "content": old,
		"settings": map[string]any{"unsaved": true, "genTaskId": 42, "history": []any{"media://older", map[string]any{"url": old}}},
	}}})
	if !strings.Contains(string(snap), `&`) {
		t.Fatal("前提：快照里的 & 被转义")
	}
	fake := &fakeMutator{payload: snap}
	notifier := &fakeNotifier{}
	s := &Service{snaps: fake, notifier: notifier, now: time.Now}
	err := s.ApplyRepersist(context.Background(),
		json.RawMessage(`{"kind":"canvas_node","projectId":3,"nodeId":"n1","userId":1}`),
		[]generation.RepersistReplacement{{Index: 0, OldURL: old, NewURL: "media://u1/gen/task-42-0", AssetID: 77}})
	if err != nil {
		t.Fatal(err)
	}
	n := nodeOf(t, fake.result, "n1")
	st := n["settings"].(map[string]any)
	if n["content"] != "media://u1/gen/task-42-0" {
		t.Fatalf("content 应换成本地地址, got %v", n["content"])
	}
	if hist := st["history"].([]any); hist[1].(map[string]any)["url"] != "media://u1/gen/task-42-0" || hist[0] != "media://older" {
		t.Fatalf("history 里对应条目应改写、其他条目原样, got %v", hist)
	}
	if _, ok := st["unsaved"]; ok {
		t.Fatalf("unsaved 应清掉, got %v", st)
	}
	if st["assetId"] != float64(77) {
		t.Fatalf("第 0 个槽位应写回 assetId, got %v", st["assetId"])
	}
	if len(notifier.calls) != 1 || notifier.calls[0].projectID != 3 {
		t.Fatalf("回写后应广播一次 canvas_changed, got %+v", notifier.calls)
	}
}

func TestApplyRepersistIgnoresNonCanvasSink(t *testing.T) {
	s := &Service{snaps: &fakeMutator{}, now: time.Now}
	if err := s.ApplyRepersist(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRepersist(context.Background(), json.RawMessage(`{"kind":"other"}`), nil); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 10: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/canvasgen/ -run 'Unsaved|ApplyRepersist'`
Expected: 编译失败，`s.ApplyRepersist undefined`。

- [ ] **Step 11: 实现画布侧**

`server/internal/canvasgen/repersist_apply.go`：

```go
package canvasgen

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// isUnsavedOutput：产物没能落本地（spec S5：重试 3 次仍失败，保留了上游地址）。
func isUnsavedOutput(url string, assetIDs []int64) bool {
	remote := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return remote && (len(assetIDs) == 0 || assetIDs[0] == 0)
}

// ApplyRepersist 在用户手动「保存到本地」成功后，把源节点里的上游外链换成本地地址并清掉
// unsaved / genTaskId。按节点结构改而不是按字节替换：writeNode 用 json.Marshal 落库，URL 里的
// & 会被写成 &，RewriteMediaRef 的 bytes.ReplaceAll 对这类 URL 改不到。
// sink 不是画布节点（其他发起方/直连任务）时什么都不做。
func (s *Service) ApplyRepersist(_ context.Context, sink json.RawMessage, reps []generation.RepersistReplacement) error {
	var ref canvasSinkRef
	if len(sink) == 0 || json.Unmarshal(sink, &ref) != nil || ref.Kind != sinkKindCanvasNode || ref.ProjectID == 0 || ref.NodeID == "" {
		return nil
	}
	byOld := make(map[string]generation.RepersistReplacement, len(reps))
	for _, r := range reps {
		byOld[r.OldURL] = r
	}
	return s.writeNode(ref.UserID, ref.ProjectID, ref.NodeID, func(node map[string]any) {
		settings := nodeSettings(node)
		if c, _ := node["content"].(string); c != "" {
			if r, ok := byOld[c]; ok {
				node["content"] = r.NewURL
				if r.Index == 0 && r.AssetID > 0 {
					settings["assetId"] = r.AssetID
				}
			}
		}
		if hist, ok := settings["history"].([]any); ok {
			for i, h := range hist {
				switch v := h.(type) {
				case string:
					if r, ok := byOld[v]; ok {
						hist[i] = r.NewURL
					}
				case map[string]any:
					if u, _ := v["url"].(string); u != "" {
						if r, ok := byOld[u]; ok {
							v["url"] = r.NewURL
						}
					}
				}
			}
		}
		delete(settings, "unsaved")
		delete(settings, "genTaskId")
	})
}
```

`server/internal/canvasgen/service.go` 的 `OnGenerationResult` 成功分支里，在 `clearNodeError(node)`（参考仓 :210）之后插入：

```go
		// spec S5：产物没能保存到本地 → 节点显示「未保存到本地」和重试按钮；重试走
		// POST /api/v1/tasks/{genTaskId}/repersist，成功后 ApplyRepersist 删掉这两个键。
		if isUnsavedOutput(url, result.AssetIDs) {
			settings["unsaved"] = true
			settings["genTaskId"] = result.TaskID
		} else {
			delete(settings, "unsaved")
			delete(settings, "genTaskId")
		}
```

- [ ] **Step 12: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/canvasgen/`
Expected: 全部 PASS（含参考仓原有用例）。

- [ ] **Step 13: 写 HTTP 的失败测试**

`server/internal/http/generation_repersist_handlers_test.go`：

```go
package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

type fakeRepersistGen struct {
	res *generation.RepersistResult
	err error
}

func (f fakeRepersistGen) RepersistTask(_ context.Context, userID, taskID int64) (*generation.RepersistResult, error) {
	if userID != 1 || taskID != 7 {
		return nil, generation.ErrNotFound
	}
	return f.res, f.err
}

type fakeRepersistCanvas struct {
	applied  int
	rewrites [][2]string
}

func (f *fakeRepersistCanvas) ApplyRepersist(context.Context, json.RawMessage, []generation.RepersistReplacement) error {
	f.applied++
	return nil
}

func (f *fakeRepersistCanvas) RewriteMediaRef(_ context.Context, _ int64, oldURL, newURL string) error {
	f.rewrites = append(f.rewrites, [2]string{oldURL, newURL})
	return nil
}

type prefixSigner struct{}

func (prefixSigner) Sign(s string) string { return "signed:" + s }

func doRepersist(t *testing.T, h *GenerationRepersistHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	registerRepersistRoute(r, h)
	req := httptest.NewRequest(nethttp.MethodPost, "/api/v1/tasks/"+id+"/repersist", nil)
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRepersistHandlerAppliesToCanvas(t *testing.T) {
	canvas := &fakeRepersistCanvas{}
	h := NewGenerationRepersistHandler(fakeRepersistGen{res: &generation.RepersistResult{
		Sink:         json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n"}`),
		OutputURLs:   []string{"media://k"},
		AssetIDs:     []int64{5},
		Replacements: []generation.RepersistReplacement{{Index: 0, OldURL: "https://x/a.png", NewURL: "media://k", AssetID: 5}},
	}}, canvas, prefixSigner{})
	rec := doRepersist(t, h, "7")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"signed:media://k"`) {
		t.Fatalf("返回的 outputUrls 应已签名: %s", rec.Body.String())
	}
	if canvas.applied != 1 || len(canvas.rewrites) != 1 || canvas.rewrites[0][1] != "media://k" {
		t.Fatalf("应回写源节点并尽力改写其他副本, applied=%d rewrites=%v", canvas.applied, canvas.rewrites)
	}
}

func TestRepersistHandlerErrorMapping(t *testing.T) {
	cases := []struct {
		id   string
		err  error
		want int
	}{
		{"8", nil, nethttp.StatusNotFound},
		{"7", generation.ErrNothingToRepersist, nethttp.StatusConflict},
		{"7", errors.New("fetch 404"), nethttp.StatusBadGateway},
		{"abc", nil, nethttp.StatusBadRequest},
	}
	for _, c := range cases {
		h := NewGenerationRepersistHandler(fakeRepersistGen{err: c.err}, &fakeRepersistCanvas{}, nil)
		if rec := doRepersist(t, h, c.id); rec.Code != c.want {
			t.Fatalf("id=%s err=%v got %d want %d", c.id, c.err, rec.Code, c.want)
		}
	}
}
```

（`withUserID` 由 Task 2 的 shim 或 P1b 的 `context.go` 提供。）

- [ ] **Step 14: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/http/ -run Repersist`
Expected: 编译失败，`undefined: NewGenerationRepersistHandler`。

- [ ] **Step 15: 实现 HTTP**

`server/internal/http/generation_repersist_handlers.go`：

```go
package http

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

type repersistGen interface {
	RepersistTask(ctx context.Context, userID, taskID int64) (*generation.RepersistResult, error)
}

type repersistCanvas interface {
	ApplyRepersist(ctx context.Context, sink json.RawMessage, reps []generation.RepersistReplacement) error
	RewriteMediaRef(ctx context.Context, userID int64, oldURL, newURL string) error
}

// repersistSigner 是 P1b *MediaSigner 的最小子集（只用 Sign）。
type repersistSigner interface {
	Sign(s string) string
}

// GenerationRepersistHandler：节点「未保存到本地」的重试按钮（spec S5）。
type GenerationRepersistHandler struct {
	gen    repersistGen
	canvas repersistCanvas
	signer repersistSigner
}

func NewGenerationRepersistHandler(gen repersistGen, canvas repersistCanvas, signer repersistSigner) *GenerationRepersistHandler {
	return &GenerationRepersistHandler{gen: gen, canvas: canvas, signer: signer}
}

func registerRepersistRoute(r chi.Router, h *GenerationRepersistHandler) {
	if h == nil {
		return
	}
	r.Post("/api/v1/tasks/{taskID}/repersist", h.Repersist)
}

// Repersist POST /api/v1/tasks/{taskID}/repersist → {outputUrls(已签名), assetIds}
func (h *GenerationRepersistHandler) Repersist(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID := userIDFromContext(r.Context())
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil || taskID <= 0 {
		writeError(w, nethttp.StatusBadRequest, "invalid task id")
		return
	}
	res, err := h.gen.RepersistTask(r.Context(), userID, taskID)
	if err != nil {
		switch {
		case errors.Is(err, generation.ErrNotFound):
			writeError(w, nethttp.StatusNotFound, "task not found")
		case errors.Is(err, generation.ErrNothingToRepersist):
			writeError(w, nethttp.StatusConflict, err.Error())
		default:
			writeError(w, nethttp.StatusBadGateway, "保存到本地失败："+err.Error())
		}
		return
	}
	if h.canvas != nil {
		// 源节点按结构回写（必须成功，否则节点一直显示未保存）；其他画布里的副本尽力按字节改写。
		if err := h.canvas.ApplyRepersist(r.Context(), res.Sink, res.Replacements); err != nil {
			writeError(w, nethttp.StatusInternalServerError, err.Error())
			return
		}
		for _, rep := range res.Replacements {
			if err := h.canvas.RewriteMediaRef(r.Context(), userID, rep.OldURL, rep.NewURL); err != nil {
				log.Printf("repersist: task=%d 改写其他画布副本失败 old=%s: %v", taskID, rep.OldURL, err)
			}
		}
	}
	urls := make([]string, len(res.OutputURLs))
	for i, u := range res.OutputURLs {
		urls[i] = u
		if h.signer != nil {
			urls[i] = h.signer.Sign(u)
		}
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"outputUrls": urls, "assetIds": res.AssetIDs})
}
```

- [ ] **Step 16: 运行测试，确认通过并提交**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/generation/ ./internal/canvasgen/ ./internal/http/ && go test -tags p1cshim ./internal/generation/ ./internal/canvasgen/ ./internal/http/`
Expected: 全部 PASS。

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/generation server/internal/canvasgen server/internal/http/generation_repersist_handlers.go server/internal/http/generation_repersist_handlers_test.go
git commit -m "feat(server): 产物下载失败重试 3 次，节点标记未保存到本地并支持手动重试（spec S5）"
```

---

### Task 6: S12 接口位、本地参考图直读、上游配置中文错误

**Files:**
- Create: `server/internal/generation/input_media.go`、`server/internal/generation/upstream_config.go`、`server/internal/generation/input_media_test.go`
- Modify: `server/internal/generation/image_inputs.go`（`signInputMediaReferences`、`fetchInputImage`、`spoolInputImage`）、`server/internal/generation/execution_plan.go`（`prepareExecution`）、`server/internal/generation/service.go`（`Service` 结构体末尾一个字段；`Submit` 直连路径取 Key/base_domain 与插值后几行）

**Interfaces:**
- Consumes: Task 3 的 `Service`、`prepareExecution`、`prepareImageInputs`、`normalizeImageMIME`、`maxInputImageBytes`、`walkInputStrings`、`cloneInputs`
- Produces:
  - `generation.InputMediaSigner` 接口：`SignInputMedia(ctx context.Context, inputs map[string]any) (map[string]any, error)`
  - `(*generation.Service).SetInputMediaSigner(m InputMediaSigner)`
  - `generation.ErrLocalMediaUnreachable`、`generation.ErrUpstreamKeyNotConfigured`、`generation.ErrBaseDomainNotConfigured`

- [ ] **Step 1: 写失败的测试**

`server/internal/generation/input_media_test.go`：

```go
package generation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

const localBase = "http://127.0.0.1:5555/media/"

// localMediaStorage 模拟 P1b 的本地存储：ExtractKey 认 media:// 和 sidecar 自己签出的本地 URL。
type localMediaStorage struct {
	AssetStorage
	files map[string][]byte
}

func (l *localMediaStorage) ExtractKey(u string) (string, bool) {
	for _, p := range []string{"media://", localBase} {
		if strings.HasPrefix(u, p) {
			return strings.TrimPrefix(u, p), true
		}
	}
	return "", false
}

func (l *localMediaStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return localBase + key, nil
}

func (l *localMediaStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := l.files[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type planModels struct{ m *models.ModelConfig }

func (p planModels) GetByModelID(string) (*models.ModelConfig, error) { return p.m, nil }

type planKeys struct{ key string }

func (p planKeys) Resolve(int64) (string, error) { return p.key, nil }

type planSettings map[string]string

func (p planSettings) Get(k string) (string, error) { return p[k], nil }

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func planService(m *models.ModelConfig, settings planSettings, key string) *Service {
	return &Service{
		models:      planModels{m: m},
		upstreamKey: planKeys{key: key},
		settings:    settings,
		storage:     &localMediaStorage{files: map[string][]byte{"u1/a": pngBytes}},
	}
}

func okSettings() planSettings { return planSettings{"base_domain": "https://api.example.com"} }

func TestPrepareExecutionDataURIReadsLocalFile(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatDataURI,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatalf("data_uri 模型应能用本地参考图: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if !strings.Contains(string(plan.payload), want) {
		t.Fatalf("payload 应内嵌本地文件的 data URI, got %s", plan.payload)
	}
}

func TestPrepareExecutionEditsSpoolsLocalFile(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		ImageRequestMode: models.ImageRequestModeOpenAIImageEdits,
		RequestTemplate:  json.RawMessage(`{"prompt":"{{prompt}}"}`)}, okSettings(), "sk")
	svc.tmpDir = t.TempDir()
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"prompt": "p", "images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatalf("multipart 模型应能用本地参考图: %v", err)
	}
	defer removePreparedFiles(plan.preparedImages)
	if len(plan.preparedImages) != 1 || plan.preparedImages[0].Path == "" {
		t.Fatalf("应落一个临时文件, got %+v", plan.preparedImages)
	}
	got, _ := os.ReadFile(plan.preparedImages[0].Path)
	if !bytes.Equal(got, pngBytes) || plan.preparedImages[0].ContentType != "image/png" {
		t.Fatalf("临时文件内容/类型不对, type=%s", plan.preparedImages[0].ContentType)
	}
}

func TestPrepareExecutionRejectsLocalMediaInURLPayload(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	_, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if !errors.Is(err, ErrLocalMediaUnreachable) {
		t.Fatalf("上游拿不到本机地址，应在调上游前失败, got %v", err)
	}
}

type publicSigner struct{}

func (publicSigner) SignInputMedia(_ context.Context, in map[string]any) (map[string]any, error) {
	out, err := walkInputStrings(in, func(s string) (string, error) {
		return strings.Replace(s, "media://", "https://public.example.com/", 1), nil
	})
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func TestPrepareExecutionInjectedSignerMakesURLReachable(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"image":"{{image}}"}`)}, okSettings(), "sk")
	svc.SetInputMediaSigner(publicSigner{})
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"images": []any{"media://u1/a"}}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.payload), "https://public.example.com/u1/a") {
		t.Fatalf("S12 接入的签名器结果应进 payload, got %s", plan.payload)
	}
}

func TestPrepareExecutionMissingBaseDomain(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{}`)}, planSettings{}, "sk")
	_, err := svc.prepareExecution(context.Background(), 1, "m", map[string]any{}, "", "", nil)
	if !errors.Is(err, ErrBaseDomainNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestPrepareExecutionMissingAPIKey(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, RequestTemplate: json.RawMessage(`{}`)}, okSettings(), "")
	_, err := svc.prepareExecution(context.Background(), 1, "m", map[string]any{}, "", "", nil)
	if !errors.Is(err, ErrUpstreamKeyNotConfigured) {
		t.Fatalf("got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run PrepareExecution`
Expected: 编译失败，`undefined: ErrLocalMediaUnreachable`、`svc.SetInputMediaSigner undefined`。

- [ ] **Step 3: 写 upstream_config.go 与 input_media.go**

`server/internal/generation/upstream_config.go`：

```go
package generation

import (
	"errors"
	"fmt"
)

// 首次启动 settings 里还没有 base_domain / upstream_api_key（种子数据不含 Key，research §6）。
// 错误文案直接写进任务 error 列、显示在画布节点上，必须是用户能照着做的中文。
var (
	ErrUpstreamKeyNotConfigured = errors.New("未配置上游 API Key，请在设置中填写后重试")
	ErrBaseDomainNotConfigured  = errors.New("未配置上游地址（base_domain），请在设置中填写后重试")
)

// upstreamCredentials 现读本次调用要用的 Key 与上游地址（spec M2/M3：每次请求现读 settings）。
func (s *Service) upstreamCredentials(userID int64) (apiKey, baseDomain string, err error) {
	apiKey, err = s.upstreamKey.Resolve(userID)
	if err != nil {
		return "", "", fmt.Errorf("load upstream api key: %w", err)
	}
	if apiKey == "" {
		return "", "", ErrUpstreamKeyNotConfigured
	}
	baseDomain, err = s.settings.Get("base_domain")
	if err != nil {
		return "", "", fmt.Errorf("load base_domain: %w", err)
	}
	if baseDomain == "" {
		return "", "", ErrBaseDomainNotConfigured
	}
	return apiKey, baseDomain, nil
}
```

`server/internal/generation/input_media.go`：

```go
package generation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// InputMediaSigner 是 spec S12「输入媒体如何让上游拿到」的接入点（待定）。实现方把 inputs 里的
// 本地媒体引用（media://key 或 sidecar 签出的本地 URL）换成上游能直接下载的地址并返回新 map
// （不得修改入参）。未注入时走参考仓原逻辑 storage.SignedURL——桌面端得到的是 127.0.0.1
// 地址，上游拿不到，最终由 ensureNoLocalMedia 在调上游之前明确失败。
// 本计划不实现任何上传到公网主机的逻辑。
type InputMediaSigner interface {
	SignInputMedia(ctx context.Context, inputs map[string]any) (map[string]any, error)
}

// SetInputMediaSigner 注入 S12 方案的实现（setter 惯例同 SetTempDir）。
func (s *Service) SetInputMediaSigner(m InputMediaSigner) { s.inputSigner = m }

// ErrLocalMediaUnreachable：插值后的上游请求里仍有本机媒体地址。在调上游之前失败，
// 不产生任何上游调用。
var ErrLocalMediaUnreachable = errors.New("该模型要求参考图/视频/音频是上游能直接下载的网址，桌面端暂不支持把本地文件交给它（待定事项 S12）")

// ensureNoLocalMedia 扫描最终请求体里的每个字符串，发现 storage 认得的本地媒体引用就拒绝。
// data_uri / base64 / multipart 的参考图在此之前已被换成内容本身，不会命中。
func (s *Service) ensureNoLocalMedia(payload []byte) error {
	if s.storage == nil || len(payload) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}
	found := false
	assets.WalkStrings(decoded, func(v string) string {
		if !found {
			if key, ok := s.storage.ExtractKey(v); ok && key != "" {
				found = true
			}
		}
		return v
	})
	if found {
		return ErrLocalMediaUnreachable
	}
	return nil
}

// openLocalMedia：ref 是本地存储里的媒体（media:// 或 sidecar 签出的本地 URL）时直接读盘，
// 不走 HTTP——sidecar 的本地 URL 带 token 校验，自己请求自己既绕又脆。ok=false 表示不是本地媒体。
func (s *Service) openLocalMedia(ctx context.Context, ref string) (rc io.ReadCloser, ok bool, err error) {
	if s.storage == nil {
		return nil, false, nil
	}
	key, isLocal := s.storage.ExtractKey(ref)
	if !isLocal || key == "" {
		return nil, false, nil
	}
	rc, err = s.storage.GetObject(ctx, key)
	if err != nil {
		return nil, true, fmt.Errorf("read local media %q: %w", key, err)
	}
	return rc, true, nil
}

// readLocalMedia 读本地参考图进内存（data_uri / base64 模式），上限与 fetchInputImage 相同。
func (s *Service) readLocalMedia(ctx context.Context, ref string) ([]byte, string, bool, error) {
	rc, ok, err := s.openLocalMedia(ctx, ref)
	if !ok || err != nil {
		return nil, "", ok, err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, maxInputImageBytes+1))
	if err != nil {
		return nil, "", true, err
	}
	if len(body) > maxInputImageBytes {
		return nil, "", true, fmt.Errorf("image exceeds %d bytes", maxInputImageBytes)
	}
	return body, normalizeImageMIME(http.DetectContentType(body)), true, nil
}

// sniffReader 嗅探前 512 字节的 MIME，返回可继续从头读的 reader。
func sniffReader(r io.Reader) (io.Reader, string) {
	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	return br, http.DetectContentType(head)
}
```

- [ ] **Step 4: 接线**

`server/internal/generation/service.go`：

1. `Service` 结构体**末尾**加字段（Task 5 也在末尾加了 `repersist`，合并时两行都保留）：

```go
	// inputSigner 见 SetInputMediaSigner（spec S12 接入点）；nil = 参考仓原逻辑。
	inputSigner InputMediaSigner
```

2. `Submit` 直连路径里从 `apiKey, err := s.upstreamKey.Resolve(input.UserID)`（参考仓 :372）到 base_domain 判空结束（:386）整段替换为：

```go
	apiKey, baseDomain, err := s.upstreamCredentials(input.UserID)
	if err != nil {
		return nil, err
	}
```

3. 同一路径 `payload, err := upstream.InterpolateTemplate(...)` 的错误判断（:398-401）之后插入：

```go
	if err := s.ensureNoLocalMedia(payload); err != nil {
		removePreparedFiles(preparedImages)
		return nil, err
	}
```

`server/internal/generation/execution_plan.go` 的 `prepareExecution`：

4. 从 `apiKey, err := s.upstreamKey.Resolve(userID)`（参考仓 :70）到 base_domain 判空结束（:84）整段替换为：

```go
	apiKey, baseDomain, err := s.upstreamCredentials(userID)
	if err != nil {
		return nil, err
	}
```

5. `payload, err := upstream.InterpolateTemplate(...)` 的错误判断（:96-99）之后插入：

```go
	// S12 未定：插值后的请求里还留着本机地址，说明这个模型要上游自己去下载参考媒体，
	// 而上游访问不到本机。调上游之前失败，不浪费一次上游调用。
	if err := s.ensureNoLocalMedia(payload); err != nil {
		removePreparedFiles(preparedImages)
		return nil, err
	}
```

`server/internal/generation/image_inputs.go`：

6. `signInputMediaReferences` 函数体最前面（`if s == nil || s.storage == nil …` 之前）插入：

```go
	if s != nil && s.inputSigner != nil && inputs != nil {
		return s.inputSigner.SignInputMedia(ctx, cloneInputs(inputs))
	}
```

7. `fetchInputImage` 在 `data:` 分支之后、http 校验之前插入：

```go
	if body, mime, ok, err := s.readLocalMedia(ctx, ref); ok {
		return body, mime, err
	}
```

8. `spoolInputImage` 的 `switch` 里，在 `case strings.HasPrefix(ref, "data:"):` 分支之后、http 分支之前插入（本地签名 URL 也是 http://127.0.0.1，必须先判）：

```go
	case s.storage != nil && func() bool { k, ok := s.storage.ExtractKey(ref); return ok && k != "" }():
		rc, _, err := s.openLocalMedia(ctx, ref)
		if err != nil {
			return spooledFile{}, err
		}
		defer rc.Close()
		src, mime = sniffReader(rc)
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/generation/ && go test -tags p1cshim ./internal/generation/`
Expected: 全部 PASS（含参考仓 `image_inputs_test.go` 的 `TestSignInputMediaReferencesSignsMediaProtocolImages`：未注入签名器时行为不变）。

- [ ] **Step 6: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/generation
git commit -m "feat(server): 输入媒体 S12 接入点；本地参考图直读；本机地址与未配置上游时明确报错"
```

---

### Task 7: 启动恢复与后台循环

**Files:**
- Create: `server/internal/generation/startup.go`、`server/internal/generation/startup_test.go`

**Interfaces:**
- Consumes: Task 3 的 `ResumeOrphanedTasks`、`RecoverStaleQueueTasks`、`SweepStuckQueued`、`RunQueueWorker`、`RunOutboxWorker`、`fakeRecoverRepo`（`recover_test.go`）；`lifecycle.InstanceID()`；Task 2 的表。不使用 Task 4 的任何新增符号（两者并行）。
- Produces:
  - `(*generation.SQLiteRepository).ExpirePreviousLeases(instance string) (tasks, outbox int64, err error)`
  - `(*generation.Service).StartBackground(ctx context.Context)`：同步执行「过期旧租约 → 恢复一轮」，然后起 60 秒恢复循环、队列 worker、outbox worker 三个 goroutine

- [ ] **Step 1: 写失败的测试**

`server/internal/generation/startup_test.go`：

```go
package generation

import (
	"context"
	"sync"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 单进程桌面端：启动时库里 running/processing 行的心跳都来自已经死掉的上个进程，
// 必须立即视为过期，不能等 90 秒（Review Focus 1）。
func TestExpirePreviousLeasesMakesOldHeartbeatsStale(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	ins := func(status, owner string) int64 {
		res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, owner_instance, heartbeat_at, lease_version)
			VALUES (1, 'm', ?, '{}', ?, CURRENT_TIMESTAMP, 1)`, status, owner)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	oldRunning := ins("running", "old-proc")
	oldProcessing := ins("processing", "old-proc")
	mine := ins("running", "me")
	done := ins("succeeded", "old-proc")
	if _, err := conn.Exec(`INSERT INTO generation_sink_outbox (task_id, sink, payload, state, locked_until)
		VALUES (1, '{}', '{}', 'delivering', datetime('now','+5 minutes'))`); err != nil {
		t.Fatal(err)
	}

	tasks, outbox, err := repo.ExpirePreviousLeases("me")
	if err != nil || tasks != 2 || outbox != 1 {
		t.Fatalf("tasks=%d outbox=%d err=%v, want 2/1", tasks, outbox, err)
	}
	hbNull := func(id int64) bool {
		var isNull bool
		_ = conn.QueryRow(`SELECT heartbeat_at IS NULL FROM generation_tasks WHERE id=?`, id).Scan(&isNull)
		return isNull
	}
	if !hbNull(oldRunning) || !hbNull(oldProcessing) {
		t.Fatal("上个进程的在途任务心跳应被清空（视为过期）")
	}
	if hbNull(mine) || hbNull(done) {
		t.Fatal("本进程的任务与终态任务不该被动")
	}
	var claimable int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM generation_sink_outbox WHERE state='delivering' AND locked_until < CURRENT_TIMESTAMP`).Scan(&claimable)
	if claimable != 1 {
		t.Fatal("上个进程领走的 outbox 行应立即可被重新领取")
	}
}

type startupSpyRepo struct {
	fakeRecoverRepo
	mu    sync.Mutex
	calls []string
}

func (r *startupSpyRepo) rec(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *startupSpyRepo) ExpirePreviousLeases(string) (int64, int64, error) {
	r.rec("expire")
	return 0, 0, nil
}

func (r *startupSpyRepo) ListProcessing(int, int) ([]Task, error) {
	r.rec("ListProcessing")
	return nil, nil
}

func (r *startupSpyRepo) ListStaleRunning(int) ([]Task, error) {
	r.rec("ListStaleRunning")
	return nil, nil
}

func (r *startupSpyRepo) ListQueuedSinkless(int, int) ([]Task, error) {
	r.rec("ListQueuedSinkless")
	return nil, nil
}

func (r *startupSpyRepo) ListQueuedOlderThan(int, int) ([]Task, error) {
	r.rec("ListQueuedOlderThan")
	return nil, nil
}

func TestStartBackgroundExpiresLeasesThenRecovers(t *testing.T) {
	repo := &startupSpyRepo{}
	svc := &Service{repo: repo, leaseStaleSec: 90}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 只验证同步的首轮；后台 goroutine 拿到已取消的 ctx 立即退出
	svc.StartBackground(ctx)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	want := []string{"expire", "ListProcessing", "ListStaleRunning", "ListQueuedSinkless", "ListQueuedOlderThan"}
	if len(repo.calls) < len(want) {
		t.Fatalf("calls=%v want 前缀 %v", repo.calls, want)
	}
	for i, w := range want {
		if repo.calls[i] != w {
			t.Fatalf("calls=%v want 前缀 %v（必须先过期旧租约，再续 poll，再接管，再兜底）", repo.calls, want)
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/generation/ -run 'ExpirePrevious|StartBackground'`
Expected: 编译失败，`repo.ExpirePreviousLeases undefined`、`svc.StartBackground undefined`。

- [ ] **Step 3: 实现**

`server/internal/generation/startup.go`：

```go
package generation

import (
	"context"
	"log"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/lifecycle"
)

// 启动恢复（spec S7：照搬参考仓的恢复逻辑，去掉多实例租约）。
//
// 参考仓靠 Redis leader 选举跑一组单例 worker，并且故意不在启动时扫孤儿（蓝绿发布时旧实例
// 还活着）。桌面端单实例单进程（spec S11），这两个前提都不存在：
//   - leader 组拆成这里直接起的 goroutine；
//   - 启动时库里所有「别的 owner」的在途行都来自已经死掉的上个进程，心跳必须立即判过期，
//     否则要干等 LEASE_STALE_SEC（90 秒）才续跑，用户看到的是重开应用后节点卡住一分半。

// recoverInterval 与参考仓 main.go 的孤儿扫描周期一致。
const recoverInterval = 60 * time.Second

// leaseExpirer 由 SQLiteRepository 实现；做成可选接口是为了测试用 fake repo。
type leaseExpirer interface {
	ExpirePreviousLeases(instance string) (tasks, outbox int64, err error)
}

// ExpirePreviousLeases 把 owner 不是本进程的在途任务心跳清空、把上个进程领走的 outbox 行租约
// 拨到过去。之后 ResumeOrphanedTasks / RecoverStaleQueueTasks 的「心跳为空或超时」条件立即命中，
// claimOutboxDue 也能立即重新领取。只在启动时、后台循环开始之前调用一次。
func (r *SQLiteRepository) ExpirePreviousLeases(instance string) (int64, int64, error) {
	res, err := r.db.Exec(`UPDATE generation_tasks SET heartbeat_at = NULL
		WHERE status IN ('running','processing','settling')
		  AND heartbeat_at IS NOT NULL
		  AND (owner_instance IS NULL OR owner_instance <> ?)`, instance)
	if err != nil {
		return 0, 0, err
	}
	tasks, _ := res.RowsAffected()
	// 字面量而非 sqlSecondsOffset：本任务与 Task 4 并行，不依赖它新增的辅助函数。
	res, err = r.db.Exec(`UPDATE generation_sink_outbox SET locked_until = datetime('now', '-1 seconds')
		WHERE state = 'delivering' AND (locked_until IS NULL OR locked_until >= CURRENT_TIMESTAMP)`)
	if err != nil {
		return tasks, 0, err
	}
	outbox, _ := res.RowsAffected()
	return tasks, outbox, nil
}

// StartBackground 启动生成链路的全部后台工作。调用前必须已 RegisterSink 并完成
// SetTempDir / SetLeaseConfig。首轮恢复同步执行（毫秒级，只查库；续 poll 自己起 goroutine），
// 返回后队列 worker 立即可领取被重排的任务。ctx 取消时三个循环退出。
func (s *Service) StartBackground(ctx context.Context) {
	if le, ok := s.repo.(leaseExpirer); ok {
		tasks, outbox, err := le.ExpirePreviousLeases(lifecycle.InstanceID())
		if err != nil {
			log.Printf("[gen-startup] 过期上个进程的租约失败: %v", err)
		} else if tasks+outbox > 0 {
			log.Printf("[gen-startup] 接管上个进程遗留: 在途任务 %d 个, 投递中 outbox %d 行", tasks, outbox)
		}
	}
	s.recoverOnce()
	go s.runRecoverLoop(ctx)
	go s.RunQueueWorker(ctx)
	go s.RunOutboxWorker(ctx)
}

// recoverOnce 顺序与参考仓 main.go:674-685 一致：续 poll → 接管四路分流 → 排队超时兜底。
func (s *Service) recoverOnce() {
	s.ResumeOrphanedTasks()
	s.RecoverStaleQueueTasks()
	s.SweepStuckQueued()
}

func (s *Service) runRecoverLoop(ctx context.Context) {
	t := time.NewTicker(recoverInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.recoverOnce()
		}
	}
}
```

- [ ] **Step 4: 运行测试，确认通过并提交**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/generation/ && go test -tags p1cshim -race ./internal/generation/ -run 'ExpirePrevious|StartBackground'`
Expected: PASS。

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/generation/startup.go server/internal/generation/startup_test.go
git commit -m "feat(server): 生成链路启动恢复，上个进程的租约立即判过期（spec S7）"
```

---

### Task 8: HTTP `/canvas-tasks*`、`/tasks*`、`/models*`

**Files:**
- Create: `server/internal/http/generation_iface.go`、`server/internal/http/runtime_model_handlers.go`、`server/internal/http/generation_routes.go`
- Create（复制后修改）: `server/internal/http/canvasgen_handlers.go`、`server/internal/http/generation_handlers.go`、`server/internal/http/generation_apply_handlers.go`
- Test: `server/internal/http/canvasgen_handlers_test.go`、`server/internal/http/canvasgen_node_status_test.go`（均原样复制）、`server/internal/http/generation_routes_test.go`（新写：canvas-tasks、tasks、models 三组用例都在这里，经真实路由注册函数）、`server/internal/http/generation_apply_handlers_test.go`（新写）

**Interfaces:**
- Consumes: Task 3 的 `generation.Service`（`List`、`Delete`、`Get`）、`canvasgen.Service`（`Enqueue`、`GetStatus`、`GetNodeStatus`）；Task 2 的 shim；P1a 的 `models.ModelConfig`、`dbtest.Open`
- Produces:
  - `mediaRefSigner` 接口（包内）：`Sign(string) string`、`SignJSON(json.RawMessage) json.RawMessage`、`Canonicalize(string) string`
  - `NewCanvasGenHandler(svc *canvasgen.Service, signer mediaRefSigner) *CanvasGenHandler`
  - `NewGenerationHandler(service *generation.Service, signer mediaRefSigner) *GenerationHandler`
  - `NewGenerationApplyHandler(service *generation.Service, ar generationApplyAssets, signer mediaRefSigner, db *sql.DB) *GenerationApplyHandler`，`generationApplyAssets`：`FindByTaskSlot`、`ListForTask`、`Insert`（`generation.AssetRecorder` 满足）
  - `NewRuntimeModelHandler(repo runtimeModelLister) *RuntimeModelHandler`，`runtimeModelLister`：`List(onlyVisible bool) ([]models.ModelConfig, error)`
  - `GenerationRoutes{CanvasGen *CanvasGenHandler; Generation *GenerationHandler; Apply *GenerationApplyHandler; Models *RuntimeModelHandler}`、`registerGenerationRoutes(r chi.Router, h GenerationRoutes)`：nil 字段不注册；路径为绝对路径 `/api/v1/...`

- [ ] **Step 1: 复制并改写 handler**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cd /Users/james/github/miaoju-desktop/server/internal/http
cp $REF/http/canvasgen_handlers.go $REF/http/canvasgen_handlers_test.go $REF/http/canvasgen_node_status_test.go \
   $REF/http/generation_handlers.go $REF/http/generation_apply_handlers.go .
```

`server/internal/http/generation_iface.go`：

```go
package http

import "encoding/json"

// mediaRefSigner 是 P1b *MediaSigner 的窄子集（参考仓 http/media_signer.go 同名方法），
// 生成链路的 handler 只依赖它，P1b 合并前可用 fake 测试。
type mediaRefSigner interface {
	Sign(s string) string
	SignJSON(raw json.RawMessage) json.RawMessage
	Canonicalize(s string) string
}
```

`canvasgen_handlers.go`：

1. import 删掉 `".../internal/billing"`、`".../internal/teams"`。
2. 结构体字段 `signer *MediaSigner` → `signer mediaRefSigner`；`NewCanvasGenHandler(svc *canvasgen.Service, signer *MediaSigner)` → `NewCanvasGenHandler(svc *canvasgen.Service, signer mediaRefSigner)`。
3. `Submit` 里 :55-60 替换为：

```go
	// 入库前把 inputs 里的媒体引用统一成 canonical media://key：桌面端签名 URL 带本次启动的
	// 端口和 token，排队中的任务跨重启后旧 URL 就失效了；执行时 prepareExecution 再现签。
	if h.signer != nil {
		if canon, ok := assets.WalkStrings(body.Inputs, h.signer.Canonicalize).(map[string]any); ok {
			body.Inputs = canon
		}
	}
```

4. 删掉 :72-81（余额不足、团队额度两个分支）。

`generation_handlers.go`：字段 `signer *MediaSigner` → `signer mediaRefSigner`；构造函数参数同改。

`generation_apply_handlers.go`：

5. 在 import 块后加：

```go
// generationApplyAssets 是 P1b assets 仓库的窄子集（generation.AssetRecorder 也满足）。
type generationApplyAssets interface {
	FindByTaskSlot(taskID int64, outputIndex int) (*assets.AssetRecord, error)
	ListForTask(taskID int64) ([]assets.AssetRecord, error)
	Insert(rec assets.AssetRecord) (int64, error)
}
```

6. 字段 `assets  assets.Repository` → `assets  generationApplyAssets`，`signer  *MediaSigner` → `signer  mediaRefSigner`；构造函数参数 `ar assets.Repository, signer *MediaSigner` → `ar generationApplyAssets, signer mediaRefSigner`。
7. 末尾响应里 `"url":      h.signer.Sign(rec.URL),` 改为先算 `url := rec.URL; if h.signer != nil { url = h.signer.Sign(url) }`，响应用 `"url": url`。

`canvasgen_handlers_test.go`、`canvasgen_node_status_test.go` 不改（`NewCanvasGenHandler(…, nil)` 传无类型 nil，对接口参数同样成立）。

- [ ] **Step 2: 写新增 handler 的失败测试**

`server/internal/http/generation_routes_test.go`：

```go
package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// fakeSigner：media://k → http://127.0.0.1:9/media/k，Canonicalize 反之。
type fakeSigner struct{}

const fakeLocal = "http://127.0.0.1:9/media/"

func (fakeSigner) Sign(s string) string {
	if strings.HasPrefix(s, "media://") {
		return fakeLocal + strings.TrimPrefix(s, "media://")
	}
	return s
}

func (f fakeSigner) SignJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(raw), "media://", fakeLocal))
}

func (fakeSigner) Canonicalize(s string) string {
	if strings.HasPrefix(s, fakeLocal) {
		return "media://" + strings.TrimPrefix(s, fakeLocal)
	}
	return s
}

type captureGenClient struct{ got generation.SubmitInput }

func (c *captureGenClient) Submit(_ context.Context, in generation.SubmitInput) (*generation.SubmitResult, error) {
	c.got = in
	return &generation.SubmitResult{TaskID: 11, Status: "queued"}, nil
}
func (c *captureGenClient) Get(context.Context, int64, int64) (*generation.Task, error) {
	return &generation.Task{Status: "succeeded", Output: json.RawMessage(`{"outputUrls":["media://k"]}`)}, nil
}
func (c *captureGenClient) LatestByCanvasNode(context.Context, int64, int64, string) (*generation.Task, error) {
	return nil, generation.ErrNotFound
}

func serve(t *testing.T, routes GenerationRoutes, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	registerGenerationRoutes(r, routes)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 签名 URL 带本次启动的端口和 token：入库前必须还原成 media://，否则排队任务跨重启后取不到参考图。
func TestCanvasTaskSubmitCanonicalizesSignedURLs(t *testing.T) {
	gen := &captureGenClient{}
	routes := GenerationRoutes{CanvasGen: NewCanvasGenHandler(canvasgen.NewService(gen, nil), fakeSigner{})}
	rec := serve(t, routes, nethttp.MethodPost, "/api/v1/canvas-tasks",
		`{"projectId":1,"nodeId":"n1","modelId":"m","inputs":{"prompt":"p","images":["`+fakeLocal+`u1/a"]}}`)
	if rec.Code != nethttp.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	imgs := gen.got.Inputs["images"].([]any)
	if imgs[0] != "media://u1/a" {
		t.Fatalf("入库前应还原为 media://, got %v", imgs[0])
	}
	if gen.got.UserID != 1 {
		t.Fatalf("userID 应取自上下文, got %d", gen.got.UserID)
	}
}

func TestCanvasTaskStatusSignsOutputs(t *testing.T) {
	routes := GenerationRoutes{CanvasGen: NewCanvasGenHandler(canvasgen.NewService(&captureGenClient{}, nil), fakeSigner{})}
	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/canvas-tasks/11", "")
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), fakeLocal+"k") {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTasksListAndDelete(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output, output_kind)
		VALUES (1, 'm', 'succeeded', '{"inputs":{"images":["media://in"]}}', '{"outputUrls":["media://out"]}', 'image')`)
	if err != nil {
		t.Fatal(err)
	}
	doneID, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input) VALUES (1, 'm', 'running', '{}')`)
	runningID, _ := res.LastInsertId()
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Generation: NewGenerationHandler(svc, fakeSigner{})}

	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/tasks?kind=image", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fakeLocal+"out") || !strings.Contains(rec.Body.String(), fakeLocal+"in") {
		t.Fatalf("input/output 里的媒体引用都应签名: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("kind=image 只应命中 1 条: %s", rec.Body.String())
	}

	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/"+itoa(runningID), ""); rec.Code != nethttp.StatusConflict {
		t.Fatalf("进行中的任务删除应 409, got %d", rec.Code)
	}
	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/"+itoa(doneID), ""); rec.Code != nethttp.StatusNoContent {
		t.Fatalf("终态任务删除应 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/999", ""); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("不存在应 404, got %d", rec.Code)
	}
}

func TestModelsListHidesNotesAndEstimatesAreZero(t *testing.T) {
	conn := dbtest.Open(t)
	repo := models.NewSQLiteRepository(conn)
	if _, err := repo.Create(models.ModelConfig{Type: "image", Name: "n", ModelID: "m1", BasePath: "/p", Enabled: true,
		AdminNote: "内部备注", PriceNote: "100积分/张", RequestTemplate: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	routes := GenerationRoutes{Models: NewRuntimeModelHandler(repo)}
	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/models", "")
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), `"modelId":"m1"`) {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "内部备注") || strings.Contains(rec.Body.String(), "100积分") {
		t.Fatalf("spec X3：不得返回备注与价格说明: %s", rec.Body.String())
	}
	if rec := serve(t, routes, nethttp.MethodPost, "/api/v1/models/estimate", `{"modelId":"m1","inputs":{}}`); !strings.Contains(rec.Body.String(), `"credits":0`) {
		t.Fatalf("估价恒为 0: %s", rec.Body.String())
	}
	rec = serve(t, routes, nethttp.MethodPost, "/api/v1/models/estimate-batch", `{"items":[{"modelId":"m1"},{"modelId":"x"}]}`)
	if !strings.Contains(rec.Body.String(), `"total":0`) || !strings.Contains(rec.Body.String(), `"items":[0,0]`) {
		t.Fatalf("批量估价恒为 0: %s", rec.Body.String())
	}
}

func TestRoutesSkipNilHandlers(t *testing.T) {
	if rec := serve(t, GenerationRoutes{}, nethttp.MethodGet, "/api/v1/models", ""); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("未装配的路由不应注册, got %d", rec.Code)
	}
}
```

本文件 import 块还需加 `"strconv"`。

`server/internal/http/generation_apply_handlers_test.go`：

```go
package http

import (
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

type fakeApplyAssets struct {
	slot     *assets.AssetRecord
	inserted []assets.AssetRecord
}

func (f *fakeApplyAssets) FindByTaskSlot(int64, int) (*assets.AssetRecord, error) { return f.slot, nil }
func (f *fakeApplyAssets) ListForTask(int64) ([]assets.AssetRecord, error)       { return nil, nil }
func (f *fakeApplyAssets) Insert(rec assets.AssetRecord) (int64, error) {
	f.inserted = append(f.inserted, rec)
	return 99, nil
}

func TestApplyToCanvasInsertsReferenceRow(t *testing.T) {
	conn := dbtest.Open(t)
	res, _ := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	pid, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output) VALUES (1, 'm', 'succeeded', '{}', '{"outputUrls":["media://k"],"assetIds":[5]}')`)
	tid, _ := res.LastInsertId()
	fa := &fakeApplyAssets{slot: &assets.AssetRecord{ID: 5, ObjectKey: "k", URL: "media://k", MimeType: "video/mp4"}}
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Apply: NewGenerationApplyHandler(svc, fa, fakeSigner{}, conn)}

	rec := serve(t, routes, nethttp.MethodPost, "/api/v1/tasks/"+itoa(tid)+"/apply-to-canvas", `{"projectId":`+itoa(pid)+`,"outputIndex":0}`)
	if rec.Code != nethttp.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"kind":"video"`) || !strings.Contains(rec.Body.String(), fakeLocal+"k") {
		t.Fatalf("响应应带 kind 与签名 url: %s", rec.Body.String())
	}
	if len(fa.inserted) != 1 || fa.inserted[0].SourceKind != "generation_ref" || fa.inserted[0].SizeBytes != 0 || fa.inserted[0].SourceTaskID != 0 {
		t.Fatalf("应插一条 generation_ref 引用行（不带 source_task_id、不计配额）, got %+v", fa.inserted)
	}
}

func TestApplyToCanvasRejectsUnknownProject(t *testing.T) {
	conn := dbtest.Open(t)
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Apply: NewGenerationApplyHandler(svc, &fakeApplyAssets{}, nil, conn)}
	if rec := serve(t, routes, nethttp.MethodPost, "/api/v1/tasks/1/apply-to-canvas", `{"projectId":12345,"outputIndex":0}`); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./internal/http/`
Expected: 编译失败，`undefined: GenerationRoutes`、`undefined: registerGenerationRoutes`、`undefined: NewRuntimeModelHandler`。

- [ ] **Step 4: 实现**

`server/internal/http/runtime_model_handlers.go`：

```go
package http

import (
	nethttp "net/http"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

type runtimeModelLister interface {
	List(onlyVisible bool) ([]models.ModelConfig, error)
}

// RuntimeModelHandler：画布取可用模型列表。估价接口保留路由但恒返回 0（spec X3 删除积分与估价；
// 前端迁移完成前仍有调用方，返回 0 比 404 平滑）。参考仓同名类型在 admin_model_handlers.go，
// 桌面端单独成文件，避免与 P4 的后台模型中心 handler 冲突。
type RuntimeModelHandler struct {
	repo runtimeModelLister
}

func NewRuntimeModelHandler(repo runtimeModelLister) *RuntimeModelHandler {
	return &RuntimeModelHandler{repo: repo}
}

// List GET /api/v1/models → {data:[ModelConfig]}（只含启用且未隐藏的模型）
func (h *RuntimeModelHandler) List(w nethttp.ResponseWriter, r *nethttp.Request) {
	list, err := h.repo.List(true)
	if err != nil {
		writeError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []models.ModelConfig{}
	}
	for i := range list {
		list[i].AdminNote = ""
		list[i].PriceNote = "" // spec X3：价格说明删除
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"data": list})
}

// Estimate POST /api/v1/models/estimate → {credits:0}
func (h *RuntimeModelHandler) Estimate(w nethttp.ResponseWriter, r *nethttp.Request) {
	writeJSON(w, nethttp.StatusOK, map[string]any{"credits": 0})
}

// EstimateBatch POST /api/v1/models/estimate-batch → {total:0, items:[0…]}（条数与请求一致）
func (h *RuntimeModelHandler) EstimateBatch(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, nethttp.StatusBadRequest, "invalid body")
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"total": 0, "items": make([]int64, len(body.Items))})
}
```

`server/internal/http/generation_routes.go`：

```go
package http

import "github.com/go-chi/chi/v5"

// GenerationRoutes 是 P1c 生成链路的 HTTP 入口集合，由 cmd/sidecar 的 wireGeneration 构造。
// nil 字段对应的路由不注册（测试、或依赖未装配时）。
type GenerationRoutes struct {
	CanvasGen  *CanvasGenHandler
	Generation *GenerationHandler
	Apply      *GenerationApplyHandler
	Models     *RuntimeModelHandler
}

// registerGenerationRoutes 注册绝对路径，调用方负责把它挂在带本地身份中间件（userID=1）的路由上。
// 路径与参考仓 router.go:255-259、638-647 一致；desub/upscale/whitemodel/postprocess-estimate 属 P1d。
func registerGenerationRoutes(r chi.Router, h GenerationRoutes) {
	if h.Models != nil {
		r.Get("/api/v1/models", h.Models.List)
		r.Post("/api/v1/models/estimate", h.Models.Estimate)
		r.Post("/api/v1/models/estimate-batch", h.Models.EstimateBatch)
	}
	if h.Generation != nil {
		r.Get("/api/v1/tasks", h.Generation.List)
		r.Delete("/api/v1/tasks/{taskID}", h.Generation.Delete)
	}
	if h.Apply != nil {
		r.Post("/api/v1/tasks/{taskID}/apply-to-canvas", h.Apply.ApplyToCanvas)
	}
	if h.CanvasGen != nil {
		r.Post("/api/v1/canvas-tasks", h.CanvasGen.Submit)
		r.Get("/api/v1/canvas-tasks/by-node", h.CanvasGen.NodeStatus)
		r.Get("/api/v1/canvas-tasks/{taskID}", h.CanvasGen.Status)
	}
}
```

- [ ] **Step 5: 运行测试，确认通过并提交**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./internal/http/ && go test -tags p1cshim ./internal/http/`
Expected: 全部 PASS（含参考仓的 `TestCanvasGenSubmitInputLimit400` 等与 P0 既有路由测试）。

```bash
cd /Users/james/github/miaoju-desktop
git add server/internal/http/generation_iface.go server/internal/http/canvasgen_handlers.go server/internal/http/canvasgen_handlers_test.go \
  server/internal/http/canvasgen_node_status_test.go server/internal/http/generation_handlers.go server/internal/http/generation_apply_handlers.go \
  server/internal/http/generation_apply_handlers_test.go server/internal/http/runtime_model_handlers.go server/internal/http/generation_routes.go \
  server/internal/http/generation_routes_test.go
git commit -m "feat(server): 生成链路 HTTP 接口（canvas-tasks、tasks 历史、models 列表，估价恒 0）"
```

---

### Task 9: 装配函数 wireGeneration 与端到端测试

**Files:**
- Create: `server/cmd/sidecar/gen_wiring.go`
- Test: `server/cmd/sidecar/gen_wiring_test.go`

**Interfaces:**
- Consumes: 第 2 批全部产出；P1a 的 `settings.NewSQLiteRepository`、`models.NewSQLiteRepository`、`projects.NewSQLiteRepository`（`MutateSnapshot`、`SnapshotProjectsContaining`、`SaveAuthoritative`、`GetLatestSnapshot`、`Create`）；`billing.NewNoopService`；`upstream.NewClient`
- Produces:
  - `genExternal{Storage generation.AssetStorage; Assets generation.AssetRecorder; Reclaimer generation.ObjectReclaimer; Signer genMediaSigner; Notifier genCanvasNotifier}`
  - `genWiring{Gen *generation.Service; CanvasGen *canvasgen.Service; Routes apphttp.GenerationRoutes; Repersist *apphttp.GenerationRepersistHandler}`
  - `wireGeneration(conn *sql.DB, dataDir string, ext genExternal) (*genWiring, error)`（不启动后台；由调用方 `w.Gen.StartBackground(ctx)`）

- [ ] **Step 1: 写失败的端到端测试**

`server/cmd/sidecar/gen_wiring_test.go`：

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// memGenStorage：内存版本地存储（生产是 P1b 的磁盘实现）。
type memGenStorage struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func (m *memGenStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	r, _ := in.Content()
	b, err := io.ReadAll(r)
	if err != nil {
		return assets.StoredObject{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objs == nil {
		m.objs = map[string][]byte{}
	}
	m.objs[in.Key] = b
	return assets.StoredObject{URL: "media://" + in.Key, Size: int64(len(b))}, nil
}
func (m *memGenStorage) DeleteObject(context.Context, string) error { return nil }
func (m *memGenStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://127.0.0.1:1/media/" + key, nil
}
func (m *memGenStorage) NewKey(_ int64, name string) string    { return "u1/2026/10/" + name }
func (m *memGenStorage) StaticKey(_ int64, name string) string { return "u1/" + strings.ReplaceAll(name, "/", "_") }
func (m *memGenStorage) ExtractKey(u string) (string, bool) {
	if strings.HasPrefix(u, "media://") {
		return strings.TrimPrefix(u, "media://"), true
	}
	return "", false
}
func (m *memGenStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(strings.NewReader(string(m.objs[key]))), nil
}

type countingNotifier struct{ n atomic.Int32 }

func (c *countingNotifier) NotifyCanvasChanged(int64, int64, int64, string, *canvasstate.CanvasOpSummary) {
	c.n.Add(1)
}

// fakeUpstream：同步出图模型 POST /v1/images/generations；异步视频模型 GET /v1/tasks/up-1。
type fakeUpstream struct {
	srv        *httptest.Server
	imagePosts atomic.Int32
	videoPosts atomic.Int32
	videoPolls atomic.Int32
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/images/generations":
			f.imagePosts.Add(1)
			_, _ = io.WriteString(w, `{"data":[{"url":"`+f.srv.URL+`/out.png"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video/generations":
			f.videoPosts.Add(1)
			_, _ = io.WriteString(w, `{"task_id":"up-new"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/up-1":
			f.videoPolls.Add(1)
			_, _ = io.WriteString(w, `{"status":"completed","video_url":"`+f.srv.URL+`/out.mp4"}`)
		case r.URL.Path == "/out.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nIMG"))
		case r.URL.Path == "/out.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4DATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type harness struct {
	w        *genWiring
	projects *projects.SQLiteRepository
	pid      int64
	up       *fakeUpstream
	notifier *countingNotifier
	exec     func(q string, args ...any)
	query    func(q string, args ...any) string
}

func newHarness(t *testing.T, nodeIDs ...string) *harness {
	t.Helper()
	conn := dbtest.Open(t)
	up := newFakeUpstream(t)
	st := settings.NewSQLiteRepository(conn)
	_ = st.Set("base_domain", up.srv.URL)
	_ = st.Set("upstream_api_key", "sk-test")
	mr := models.NewSQLiteRepository(conn)
	if _, err := mr.Create(models.ModelConfig{Type: "image", Name: "img", ModelID: "img-test", BasePath: "/v1/images/generations",
		Enabled: true, RequestTemplate: json.RawMessage(`{"model":"img-test","prompt":"{{prompt}}"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.Create(models.ModelConfig{Type: "video", Name: "vid", ModelID: "vid-test", BasePath: "/v1/video/generations",
		Enabled: true, RequestTemplate: json.RawMessage(`{"model":"vid-test","prompt":"{{prompt}}"}`),
		PollingTemplate: json.RawMessage(`{"path":"/v1/tasks/{{task_id}}","interval":1,"statusField":"status","completedValue":"completed","urlField":"video_url"}`)}); err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	pid, err := pr.Create(1, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []any
	for _, id := range nodeIDs {
		nodes = append(nodes, map[string]any{"id": id, "type": "gen-image", "status": "generating", "settings": map[string]any{}})
	}
	snap, _ := json.Marshal(map[string]any{"nodes": nodes, "connections": []any{}})
	if _, err := pr.SaveAuthoritative(pid, snap); err != nil {
		t.Fatal(err)
	}
	notifier := &countingNotifier{}
	w, err := wireGeneration(conn, t.TempDir(), genExternal{Storage: &memGenStorage{}, Notifier: notifier})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		w: w, projects: pr, pid: pid, up: up, notifier: notifier,
		exec: func(q string, args ...any) {
			if _, err := conn.Exec(q, args...); err != nil {
				t.Fatal(err)
			}
		},
		query: func(q string, args ...any) string {
			var s string
			_ = conn.QueryRow(q, args...).Scan(&s)
			return s
		},
	}
}

func (h *harness) node(t *testing.T, id string) map[string]any {
	t.Helper()
	raw, err := h.projects.GetLatestSnapshot(h.pid)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nodes []map[string]any `json:"nodes"`
	}
	_ = json.Unmarshal(raw, &doc)
	for _, n := range doc.Nodes {
		if n["id"] == id {
			return n
		}
	}
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

func TestCanvasGenerationEndToEnd(t *testing.T) {
	h := newHarness(t, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.w.Gen.StartBackground(ctx)

	taskID, err := h.w.CanvasGen.Enqueue(ctx, canvasgen.EnqueueInput{UserID: 1, ProjectID: h.pid, NodeID: "n1", ModelID: "img-test",
		Inputs: map[string]any{"prompt": "一只猫"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "节点 n1 回写为 succeeded", func() bool {
		n := h.node(t, "n1")
		return n != nil && n["status"] == "succeeded"
	})
	n := h.node(t, "n1")
	if c, _ := n["content"].(string); !strings.HasPrefix(c, "media://") {
		t.Fatalf("产物应已落本地, content=%v", n["content"])
	}
	if h.query(`SELECT status FROM generation_tasks WHERE id=?`, taskID) != "succeeded" {
		t.Fatal("任务应为 succeeded")
	}
	waitFor(t, "outbox 投递完成", func() bool {
		return h.query(`SELECT state FROM generation_sink_outbox WHERE task_id=?`, taskID) == "done"
	})
	if h.notifier.n.Load() == 0 {
		t.Fatal("回写后应发 canvas_changed")
	}
}

// Review Focus 1 / spec S7：上个进程留下两条心跳仍「新鲜」的在途任务——
// n2 已领取但没调过上游（应重新执行恰好一次），n3 已拿到上游任务号（应续轮询、绝不重新提交）。
func TestRestartResumesInFlightTasks(t *testing.T) {
	h := newHarness(t, "n2", "n3")
	sink := func(node string) string {
		return `{"kind":"canvas_node","projectId":` + itoa(h.pid) + `,"nodeId":"` + node + `","userId":1}`
	}
	h.exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, sink, owner_instance, heartbeat_at, lease_version)
		VALUES (1, ?, 'img-test', 'running', '{"modelId":"img-test","inputs":{"prompt":"x"},"note":""}', ?, 'old-proc', CURRENT_TIMESTAMP, 1)`,
		h.pid, sink("n2"))
	h.exec(`INSERT INTO generation_tasks (user_id, project_id, model_id, status, input, sink, upstream_task_id, upstream_called_at, owner_instance, heartbeat_at, lease_version)
		VALUES (1, ?, 'vid-test', 'processing', '{"modelId":"vid-test","inputs":{"prompt":"y"},"note":""}', ?, 'up-1', CURRENT_TIMESTAMP, 'old-proc', CURRENT_TIMESTAMP, 1)`,
		h.pid, sink("n3"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	h.w.Gen.StartBackground(ctx)

	waitFor(t, "n2、n3 都回写成功", func() bool {
		a, b := h.node(t, "n2"), h.node(t, "n3")
		return a != nil && b != nil && a["status"] == "succeeded" && b["status"] == "succeeded"
	})
	if time.Since(start) > 20*time.Second {
		t.Fatalf("续跑用了 %s：上个进程的心跳没有被立即判过期", time.Since(start))
	}
	if got := h.up.imagePosts.Load(); got != 1 {
		t.Fatalf("未调过上游的任务应重新执行恰好一次, POST 次数=%d", got)
	}
	if got := h.up.videoPosts.Load(); got != 0 {
		t.Fatalf("已拿到上游任务号的任务绝不能重新提交, POST 次数=%d", got)
	}
	if h.up.videoPolls.Load() == 0 {
		t.Fatal("已拿到上游任务号的任务应续轮询")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
```

本文件 import 块还需加 `"strconv"`。

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd /Users/james/github/miaoju-desktop/server && go test -tags p1cshim ./cmd/sidecar/ -run 'EndToEnd|Restart'`
Expected: 编译失败，`undefined: wireGeneration`、`undefined: genExternal`。

- [ ] **Step 3: 实现**

`server/cmd/sidecar/gen_wiring.go`：

```go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/settings"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

// 生成链路的装配（参考仓 cmd/server/main.go:194-263、630-705 的桌面版）。

const (
	genLeaseHeartbeatSec = 15                // 参考仓 LEASE_HEARTBEAT_SEC 默认值
	genLeaseStaleSec     = 90                // 参考仓 LEASE_STALE_SEC 默认值
	genUpstreamTimeout   = 300 * time.Second // 参考仓 UPSTREAM_TIMEOUT_SEC 默认值
)

// genCanvasNotifier 与 canvasgen 未导出的 canvasChangeNotifier 同形（P1b 决定由谁实现）。
type genCanvasNotifier interface {
	NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary)
}

// genMediaSigner 与 http 的 mediaRefSigner、canvasgen 的 mediaSigner 同形；P1b 的 *apphttp.MediaSigner 满足。
type genMediaSigner interface {
	Sign(s string) string
	SignJSON(raw json.RawMessage) json.RawMessage
	Canonicalize(s string) string
}

// genExternal 是生成链路向 P1b 要的依赖。字段可为 nil：Storage 为 nil 时产物不落本地；
// Assets 为 nil 时不建 asset 行、不注册 apply-to-canvas；Reclaimer 为 nil 时删任务不登记孤儿；
// Signer 为 nil 时 URL 原样返回；Notifier 为 nil 时回写不广播。
type genExternal struct {
	Storage   generation.AssetStorage
	Assets    generation.AssetRecorder
	Reclaimer generation.ObjectReclaimer
	Signer    genMediaSigner
	Notifier  genCanvasNotifier
}

type genWiring struct {
	Gen       *generation.Service
	CanvasGen *canvasgen.Service
	Routes    apphttp.GenerationRoutes
	Repersist *apphttp.GenerationRepersistHandler
}

// wireGeneration 构造生成链路的全部对象并注册 canvas_node sink。不启动后台循环：
// 调用方在 HTTP 就绪前调用 w.Gen.StartBackground(ctx)。
func wireGeneration(conn *sql.DB, dataDir string, ext genExternal) (*genWiring, error) {
	settingsRepo := settings.NewSQLiteRepository(conn)
	modelsRepo := models.NewSQLiteRepository(conn)
	projectsRepo := projects.NewSQLiteRepository(conn)

	gen := generation.NewService(
		generation.NewSQLiteRepository(conn),
		modelsRepo,
		settingsRepo,
		settingsUpstreamKey{settings: settingsRepo},
		noopGenBilling{svc: billing.NewNoopService()},
		upstream.NewClient(upstream.ClientOptions{Timeout: genUpstreamTimeout}),
		ext.Storage,
		ext.Assets,
		sqliteUserStorage{db: conn},
		ext.Reclaimer,
		conn,
	)
	gen.SetLeaseConfig(genLeaseHeartbeatSec, genLeaseStaleSec)
	tmp := filepath.Join(dataDir, "tmp", "gen")
	if err := generation.PrepareTempDir(tmp, generation.TempDirStaleAfter); err != nil {
		return nil, fmt.Errorf("gen temp dir: %w", err)
	}
	gen.SetTempDir(tmp)

	cg := canvasgen.NewService(gen, projectsRepo)
	generation.RegisterSink("canvas_node", cg.OnGenerationResult)
	cg.SetSettingsGetter(settingsRepo.Get)
	gen.SetPromptPresetResolver(promptPresetResolver(settingsRepo.Get))
	if ext.Notifier != nil {
		cg.SetChangeNotifier(ext.Notifier)
	}
	if ext.Signer != nil {
		cg.SetMediaSigner(ext.Signer)
	}
	cg.SetSnapshotSearcher(projectsRepo)

	routes := apphttp.GenerationRoutes{
		CanvasGen:  apphttp.NewCanvasGenHandler(cg, ext.Signer),
		Generation: apphttp.NewGenerationHandler(gen, ext.Signer),
		Models:     apphttp.NewRuntimeModelHandler(modelsRepo),
	}
	if ext.Assets != nil {
		routes.Apply = apphttp.NewGenerationApplyHandler(gen, ext.Assets, ext.Signer, conn)
	}
	return &genWiring{
		Gen:       gen,
		CanvasGen: cg,
		Routes:    routes,
		Repersist: apphttp.NewGenerationRepersistHandler(gen, cg, ext.Signer),
	}, nil
}

// promptPresetResolver 逐字移植参考仓 main.go:230-263：多角度/打光保留 key 先分流，
// 其余查绘图预设表；每次现读 settings，后台改正文后排队中的任务执行时自动生效。
func promptPresetResolver(get func(string) (string, error)) func(string, map[string]string) (string, bool) {
	return func(key string, params map[string]string) (string, bool) {
		if key == canvasgen.MultiAnglePresetKey {
			maCfg, err := canvasgen.LoadMultiAngleConfig(get)
			if err != nil {
				return "", false
			}
			if text := maCfg.ComposeMultiAngleText(params); text != "" {
				return text, true
			}
			return "", false
		}
		if key == canvasgen.LightControlPresetKey {
			lcCfg, err := canvasgen.LoadLightControlConfig(get)
			if err != nil {
				return "", false
			}
			if text := lcCfg.ComposeLightControlText(params); text != "" {
				return text, true
			}
			return "", false
		}
		cfg, err := canvasgen.LoadGenImagePresetsConfig(get)
		if err != nil {
			return "", false
		}
		p, ok := cfg.Preset(key)
		if !ok {
			return "", false
		}
		return p.ComposePresetText(params), true
	}
}

// settingsUpstreamKey：桌面端没有「用户独立 key」，直接读全局 settings.upstream_api_key（spec M3）。
type settingsUpstreamKey struct {
	settings interface{ Get(string) (string, error) }
}

func (k settingsUpstreamKey) Resolve(int64) (string, error) { return k.settings.Get("upstream_api_key") }

// sqliteUserStorage 实现 generation.UserStorage：直接读写 users 表（P1a 0001 已建，本地用户配额 1 PiB）。
type sqliteUserStorage struct{ db *sql.DB }

func (u sqliteUserStorage) AddStorage(id, delta int64) error {
	_, err := u.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

func (u sqliteUserStorage) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	err := u.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota)
	return used, quota, err
}

func (u sqliteUserStorage) AddStorageTx(tx *sql.Tx, id, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// noopGenBilling 把 billing.NoopService 适配成 generation.Billing（参考仓 main.go:873-905 的 billingAdapter）。
type noopGenBilling struct{ svc *billing.NoopService }

func (b noopGenBilling) Freeze(ctx context.Context, u, t, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Freeze(ctx, u, t, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) Settle(ctx context.Context, u, t, f, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Settle(ctx, u, t, f, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) Refund(ctx context.Context, u, t, f int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.Refund(ctx, u, t, f, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) FreezeTx(ctx context.Context, tx *sql.Tx, u, t, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.FreezeTx(ctx, tx, u, t, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) SettleTx(ctx context.Context, tx *sql.Tx, u, t, f, a int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.SettleTx(ctx, tx, u, t, f, a, n)
	return generation.BillingResult{}, err
}

func (b noopGenBilling) RefundTx(ctx context.Context, tx *sql.Tx, u, t, f int64, n string) (generation.BillingResult, error) {
	_, err := b.svc.RefundTx(ctx, tx, u, t, f, n)
	return generation.BillingResult{}, err
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./cmd/sidecar/ && go test -tags p1cshim -race ./cmd/sidecar/`
Expected: 全部 PASS（端到端两条约 5–10 秒：队列 2 秒一轮、outbox 3 秒一轮）。

- [ ] **Step 5: 全量回归并提交**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet -tags p1cshim ./... && go test -tags p1cshim ./...`
Expected: 全部 PASS。

```bash
cd /Users/james/github/miaoju-desktop
git add server/cmd/sidecar/gen_wiring.go server/cmd/sidecar/gen_wiring_test.go
git commit -m "feat(server): 生成链路装配与端到端测试（含重启续跑）"
```

---

### Task 10: 接入 main.go / router.go，删除 shim（需 P1b 已合并）

**Files:**
- Modify: `server/internal/http/router.go`、`server/cmd/sidecar/main.go`
- Create: `server/cmd/sidecar/gen_library_adapter.go`
- Delete: `server/internal/assets/p1c_shim.go`、`server/internal/http/p1c_shim.go`

**Interfaces:**
- Consumes: Task 9 的 `wireGeneration`、`genExternal`；Task 8 的 `GenerationRoutes`、`registerGenerationRoutes`；Task 5 的 `GenerationRepersistHandler`、`registerRepersistRoute`；P1b 在 `main.go` 里构造的本地存储、资产仓库、孤儿仓库、`*apphttp.MediaSigner`、canvas_changed 通知对象，以及 P1b 的本地身份中间件
- Produces: `apphttp.Deps.Generation apphttp.GenerationRoutes`、`apphttp.Deps.GenerationRepersist *apphttp.GenerationRepersistHandler`；sidecar 启动即提供生成接口并跑后台

- [ ] **Step 1: 确认前置条件**

Run: `cd /Users/james/github/miaoju-desktop/server && ls internal/assets/storage.go internal/http/context.go internal/library/reclaim.go && grep -n 'func NewMediaSigner\|func (m \*MediaSigner) Canonicalize\|func IsStillReferenced\|Mark(tx Execer' -r internal/`
Expected: 三个文件存在，四个符号都能找到。任何一个缺失：停下，回报「P1b 尚未提供 X」，不要自行实现。

- [ ] **Step 2: 删除 shim 并去掉 build tag 编译**

```bash
cd /Users/james/github/miaoju-desktop
git rm server/internal/assets/p1c_shim.go server/internal/http/p1c_shim.go
cd server && go build ./... && go vet ./...
```

Expected: 通过。若报 `undefined: withUserID`（P1b 的 context.go 没有这个测试辅助函数），在 `server/internal/http/generation_test_helpers_test.go` 写入：

```go
package http

import "context"

// 测试辅助：P1b 的 context.go 若没有 withUserID，P1c 的 handler 测试用这个。
func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}
```

（只在确实缺失时创建；若 P1b 的 key 常量名不是 `userIDContextKey`，按 P1b 的名字改。）

- [ ] **Step 3: 写 library 适配器**

`server/cmd/sidecar/gen_library_adapter.go`：

```go
package main

import (
	"database/sql"

	"github.com/james/huabu_studio/apps/api/internal/library"
)

// libraryReclaimer 把 P1b 的 library 孤儿回收适配成 generation.ObjectReclaimer
// （参考仓 generation/service.go:1237-1266 的 TryReclaim + IsStillReferenced 合一）。
type libraryReclaimer struct{ orphans library.OrphansRepository }

func (l libraryReclaimer) ReclaimTx(tx *sql.Tx, objectKey string, userID, sizeBytes int64) (bool, error) {
	referenced, err := library.IsStillReferenced(tx, objectKey)
	if err != nil {
		return false, err
	}
	if referenced {
		return false, nil
	}
	return true, l.orphans.Mark(tx, objectKey, userID, sizeBytes)
}
```

- [ ] **Step 4: 改 router.go**

在 `server/internal/http/router.go` 的 `Deps` 结构体末尾加：

```go
	// Generation 是 P1c 生成链路的 HTTP 入口（nil 字段不注册对应路由）。
	Generation GenerationRoutes
	// GenerationRepersist 是节点「未保存到本地」的重试接口（spec S5）。
	GenerationRepersist *GenerationRepersistHandler
```

在 `NewRouter` 里、P1b 挂上本地身份中间件（写入 userID=1）之后注册业务路由的位置，加两行：

```go
	registerGenerationRoutes(r, d.Generation)
	registerRepersistRoute(r, d.GenerationRepersist)
```

（若 P1b 把业务路由放在 `r.Group(func(r chi.Router) { r.Use(identity) … })` 里，把这两行放进同一个 Group，传 Group 的 `r`。判断方法：P1b 的 `/api/v1/projects` 注册在哪，就跟它放在一起。）

- [ ] **Step 5: 改 main.go**

在 `server/cmd/sidecar/main.go` 的 `run()` 里，P1b 打开数据库并构造存储/资产/签名器/通知对象之后、`apphttp.NewRouter(...)` 之前插入（左侧变量名换成 P1b 实际使用的名字；类型不满足接口时编译会指出）：

```go
	genW, err := wireGeneration(conn, *dataDir, genExternal{
		Storage:   storage,                                  // P1b 本地 assets.Storage
		Assets:    assetsRepo,                               // P1b assets 仓库
		Reclaimer: libraryReclaimer{orphans: orphansRepo},   // P1b library 孤儿仓库
		Signer:    mediaSigner,                              // P1b *apphttp.MediaSigner
		Notifier:  canvasNotifier,                           // P1b 的 canvas_changed 实现；没有则删掉这一行
	})
	if err != nil {
		return fmt.Errorf("装配生成链路: %w", err)
	}
	// 后台循环随进程存活；sidecar 在 stdin EOF 时 os.Exit，在途任务由下次启动的 StartBackground 续跑。
	genW.Gen.StartBackground(context.Background())
```

并在 `apphttp.Deps{...}` 字面量里加：

```go
		Generation:          genW.Routes,
		GenerationRepersist: genW.Repersist,
```

import 补 `"context"`（若尚无）。

- [ ] **Step 6: 全量回归**

Run: `cd /Users/james/github/miaoju-desktop/server && go vet ./... && go test -race ./...`
Expected: 全部 PASS，且不再需要 `-tags p1cshim`。

Run: `cd /Users/james/github/miaoju-desktop/server && grep -rn 'p1cshim' . || echo clean`
Expected: `clean`。

- [ ] **Step 7: 冒烟**

```bash
cd /Users/james/github/miaoju-desktop/server && go build -o /tmp/sidecar-p1c ./cmd/sidecar
D=$(mktemp -d); TOKEN=0123456789abcdef0123456789abcdef
mkfifo "$D/in"
/tmp/sidecar-p1c --data-dir "$D" < "$D/in" > "$D/out" 2> "$D/err" & PID=$!
exec 3>"$D/in"; echo "$TOKEN" >&3; sleep 1
PORT=$(awk '/^READY/ {print $2}' "$D/out")
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/api/v1/models"; echo
ls -d "$D/tmp/gen"; grep -i panic "$D/err" || echo "no panic"
exec 3>&-; wait $PID
```

Expected: curl 返回 `{"data":[...]}`（种子数据未导入时为 `{"data":[]}`）；`$D/tmp/gen` 存在；输出 `no panic`；关闭 stdin 后 sidecar 退出。

- [ ] **Step 8: 提交**

```bash
cd /Users/james/github/miaoju-desktop
git add -A server/internal/http/router.go server/cmd/sidecar server/internal/assets server/internal/http
git commit -m "feat(server): sidecar 接入生成链路并启动后台恢复，移除 P1c 临时 shim"
```
