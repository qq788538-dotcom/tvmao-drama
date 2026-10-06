# P1 调研结论（2026-09-30）

对参考仓 `/Users/james/github/huabu_studio/apps/api`（commit `fea92ab`）的只读调研，写 P1 各子计划时以此为事实依据。行号均指参考仓该 commit。

## 1. 阻塞性问题：输入媒体以 URL 交给上游

- 生成链路 `generation.signInputMediaReferences`（`image_inputs.go:45-75`）把 inputs 里所有 `media://` 换成 24 小时预签名的公网 URL，再插进上游请求模板，由上游自己下载。
- 只有 `inputs.images` / `inputs.image` 会按 `image_input_format` 转成 base64 / data URI / multipart；**`videos`、`audios`、`video` 永远是 URL**，没有别的路径。
- 内置 47 个模型：`image_input_format` 为 url 的 43 个、data_uri 2 个、base64 2 个；`image_request_mode` 为 json 的 45 个、openai_image_edits 2 个。
- **已启用的模型里有 27 个在带参考媒体时需要公网 URL**：
  - 图片 2 个：`doubao-seedream-5-0-pro-260628-1k`、`-2k`
  - 视频 18 个：doubao-seedance 全系 7 个、cm-seedance-2.0 系列 8 个（含 -mg、-mg-gz、-720-15）、`MiniMax-H3-Autodl`、`fl2va-advanced-h3-rh`、`yd2.0-mini`、`rhart-video-g`（合计以模板为准）
  - 隐藏的后处理 5 个：去字幕 3 个、转高清 1 个、白模 1 个（输入是单个 `video`）
  - 音频 1 个：`doubao-seed-audio-1-0`
- 不需要公网 URL 的：`gemini-3-pro-image-preview`、`gemini-3.1-flash-image-preview`（base64）；`gpt-image-2`、`gpt-image-2.1`（multipart）；文本模型；以及不连参考媒体的纯文生。
- 同样依赖公网 URL 的其他位置：Agent 把图片/视频/音频 URL 发给 planner 和子模型（`agent/multimodal.go:60-80`、`tools_subagent.go:48-68`）；Seedance 合规校验（`library/seedance_service.go:177-183`）。
- 提交时 `http/canvasgen_handlers.go:55-60` 先把 inputs 签名成 URL 再入库，执行时靠 `ExtractKey` 认回 key：本地存储的 `ExtractKey` 必须同时认 `media://` 和 sidecar 自己发出的本地 URL。

**待用户决定**：桌面端如何让上游拿到本机磁盘上的输入媒体（见对话记录，决定后补到 spec）。

## 2. 数据库

- `db.Open` / `db.Migrate` 在 `db/mysql.go`；122 个 migration 含 `PREPARE`、`information_schema`、`UPDATE … JOIN`、`MODIFY COLUMN` 等，**无法在 SQLite 重放，需另写一份 SQLite 基线 schema**。迁移锁 `GET_LOCK` 直接删除。
- 需要的表（22 张）：settings、model_configs、projects、workflow_snapshots、assets、generation_tasks、generation_sink_outbox、canvas_script_tasks、library_folders、library_items、orphan_objects、seedance_assets、seedance_characters、agent_backbone_models、agent_threads、agent_messages、agent_tools、skills、skill_categories、director_user_models、director_library_categories、director_library_models；外加最小 users 表（id、storage_quota_bytes、storage_used_bytes、balance/frozen 列）并预置 id=1 的本地用户。`canvas_gen_tasks`、`user_skills` 已无代码使用，不建。
- 各表最终列定义见调研原文（对话记录）；写基线前逐个核对 migration，尤其 `0081` 的索引块。
- SQLite 要处理的类型：JSON→TEXT；BOOLEAN/TINYINT(1)→INTEGER；MEDIUMTEXT→TEXT；DECIMAL→REAL；约 14 张表的 `ON UPDATE CURRENT_TIMESTAMP` 要用触发器或代码显式写；`agent_messages.created_at` 是微秒精度且 keyset 分页依赖它；外键要 `PRAGMA foreign_keys=ON`（`workflow_snapshots → projects` 级联被代码依赖）。
- MySQL 方言（移植包内约 90 处，generation 约 49）：
  - `ON DUPLICATE KEY UPDATE` 5 处：settings/repository.go:30、projects/repository.go:226、assets/repository.go:100（靠 `LAST_INSERT_ID(id)` + `RowsAffected==1` 判断，必须重写）、library/seedance_assets_repo.go:127、library/orphans_repo.go:34
  - `LAST_INSERT_ID(expr)` 做代次号 3 处：generation/repository.go:559、:579、outbox.go:95 → 改 `UPDATE … RETURNING`
  - `FOR UPDATE` 2 处：projects/repository.go:268、:329 → 去掉，事务用 `BEGIN IMMEDIATE`
  - `NOW()`、`NOW(6)`、`DATE_SUB/DATE_ADD … INTERVAL`：generation、canvasscript、projects、library、agent 多处
  - JSON：`sink->>'$.x'`（generation/repository.go:236）、`JSON_SEARCH`（:287、agent/repository.go:173）、`JSON_EXTRACT/JSON_UNQUOTE/JSON_SET`（:799-840）、`JSON_CONTAINS/JSON_QUOTE/JSON_ARRAY`（agent/skills_repo.go:157、skill_categories_repo.go:93-116）
  - `UPDATE … LIMIT`（generation/repository.go:679）、`IF()`（:833）、`CONCAT`（projects/repository.go:432）、`HAVING` 别名（generation/reconcile.go:49、78，reconcile 不移植）
  - `RowsAffected` 语义差异：MySQL 值没变返回 0，SQLite 返回匹配行数
- 时间：全部 Scan 到 `time.Time`，依赖 `parseTime=true`；写入几乎都用 SQL 侧 `NOW()`。SQLite 下要统一成一种 UTC 文本格式；modernc 只对声明类型为 DATETIME/TIMESTAMP 的列解析时间，`MAX(updated_at)` 这类表达式列要特殊处理（agent_tools_repo.go:105）。
- 并发：后台 worker（队列 2s、outbox 3s、心跳 15s、脚本 2s）与 HTTP 写请求争写锁 → WAL + `busy_timeout` + `BEGIN IMMEDIATE`，连接池调小。
- 测试：参考仓有 11 个必须连真实 MySQL 的测试（`*_db_test.go`：generation 9、canvasscript 1、db 1），辅助在 `db/dbtest/dbtest.go`；`projects/repository_test.go` 用手写 fake driver 按 SQL 文本匹配（改 SQL 要同步改）；其余是接口 fake，不依赖数据库。

## 3. 装配、路由、鉴权

- 唯一装配点 `cmd/server/main.go`。桌面端新写 `cmd/sidecar/main.go` 的装配，不调用 `MustWireCLI` / `MustWireFlow`。`NewRouter` 对大多数 `Deps` 字段不判 nil，未移植的路由必须从 router 删掉。
- 鉴权：`http/context.go` 的 `userIDFromContext` / `userRoleFromContext` / `actorUserIDFromContext`。桌面端用一个中间件恒定写入 userID=1、role=admin，不写 actor（走个人路径）。Agent WS 改成不验 JWT，直接 uid=1（本地 token 已由 localauth 校验）。
- 画布编辑锁：`canvasowner` 依赖 Redis；个人路径下 mutation handler 会调 `owner.Require`，所以要给 `canvasOwner` 接口（Claim / Heartbeat / Require / Probe）一个内存实现（或恒通过）。前端 owner 心跳按 spec X2 删除。
- `lifecycle`：`InstanceID`、`Go`、`BeginDrain`、`Wait` 不依赖 Redis，保留；`singleton.go`（leader 选举）删除。原 leader 组里画布需要的 worker 直接启动：`projectCleaner`（30s）、`librarySeedanceSvc.RunWorker`（30s）、`RunRepersistWorker`（5min）、60s 的孤儿任务恢复循环（启动时先跑一次）。
- billing：保留纯函数和类型（`ModelBilling`、`EstimateFreeze`、`SettleAmount`、`ValidateExtras`、`Result`、`ErrInsufficientBalance`、`WithActor`、`ActorFromContext`、`WithDramaScope`）；`Freeze/Settle/Refund` 及 Tx 版换成永远成功的空实现。注意两个会挡住 0 价的闸门：canvasscript 的 `FreezeEstimateForModel <= 0` 会剔除候选模型（service.go:229、:290）；agent 要求 backbone 的 `model_name` 在 `model_configs` 里有行（backbone.go:301）。
- 存储配额：`ensureStorageQuota` 在 `used >= quota` 时拒绝；本地用户的 quota 要设得足够大，或 users 传 nil 跳过。
- 上游地址和 Key：每次请求现读 `settings.base_domain`；Key 由 `UpstreamKeyResolver.Resolve` 给出，桌面端直接返回 `settings.upstream_api_key`。
- 画布前端用到的路由、后台配置页用到的 `/admin/*` 路由、`http.Deps` 字段清单、config 字段清单：见调研原文（对话记录）。`/admin/settings` 的 GET 返回全部 settings、PUT 无白名单——桌面端要按 M10 收紧。
- `MediaSigner` 出口/入口的完整 file:line 清单见调研原文；画布持久化前的唯一 canonical 入口是 `canvas_mutation_handlers.go:331-335`，整快照的唯一签名出口是 `project_handlers.go:218`。

## 4. 存储、生成、回写

- `assets.Storage` 接口 9 个方法（storage.go:37-59）：PutObject、DeleteObject、SignedURL、SignedThumbURL、ExtractKey、GetObject、StaticKey、NewKey、CopyObject。本地实现全部要提供；产物 key 无扩展名，MIME 取自 `assets.mime_type`。
- key 布局：`NewKey` → `<prefix>/u<uid>/<年>/<月不补零>/<文件名>`；`StaticKey` → `<prefix>/u<uid>/<文件名>`；生成产物 `gen/task-<id>-<i>`（`/` 被换成 `_`）。
- `MediaSigner`：LRU 5 万条、缓存 20h、URL 有效期 24h。桌面端 URL 含每次启动不同的端口和 token，缓存不能跨进程，TTL 概念可去掉。
- 产物落库 `persistSlot`（service.go:1398）：`file://` 失败则任务失败；http 失败则保留上游外链、任务成功，由 `RunRepersistWorker` 补搬（spec S5 要求改成重试 3 次后提示并可手动重试）。
- 回写：`generation.RegisterSink("canvas_node", canvasGenSvc.OnGenerationResult)`；`finishTx` 同事务写 `generation_sink_outbox`，outbox worker 投递（必须幂等）；`writeNode` 经 `projects.MutateSnapshot` 改快照后调 `notifier.NotifyCanvasChanged`，通过 Agent Hub 的 WebSocket 发 `canvas_changed` 帧。**所以 Agent Hub 的这一部分在 P1 就需要，不能等 P5。**
- 重启续跑的最小保留集：`upstream_called_at`、`upstream_task_id`、`sink`、`status`、`MarkUpstreamCalled` 先于上游调用、四路分流（`RecoverStaleQueueTasks`）、`ResumeOrphanedTasks`、`finishTx` + outbox、`assets(source_task_id, output_index)` 唯一键、启动时 `PrepareTempDir`。单进程下启动时应把上个进程的心跳视为过期，不必等 90 秒。`lease_version` 保留（所有 CAS SQL 都带它）。
- 并发与重试：`ConcurrencyRegistry`（内存）+ 队列 worker + `attempt/next_retry_at` + `tryScheduleRetry` 保留。
- Agent 的生成工具走直连路径（不带 sink），调用上游前被中断会直接判失败。
- 孤儿 GC：没有后台 worker，物理删除只有 `POST /admin/gc`（宽限 7 天）。spec S9 要求自动回收 → 桌面端加一个定时器（启动时 + 每天）跑同样的逻辑。
- `canvasscript` 依赖 `skillruntime`（shell 执行，spec X6，属 P6）；它没有 `upstream_called_at` 保护，失联后会整体重跑。

## 5. P1 的拆分（写计划时据此分子计划）

1. P1a 数据层：SQLite 连接与基线 schema、时间与 JSON 约定、settings / models / projects / canvasstate、本地身份中间件、画布快照与 mutation 接口。
2. P1b 存储层：本地文件 `assets.Storage`、MediaSigner、上传、素材库、孤儿回收。
3. P1c 生成链路：upstream、generation（队列、租约简化、重试、恢复）、billing 空实现、canvasgen 回写与 `canvas_changed` 通知、任务历史。取决于第 1 节的决定。
4. P1d 其余接口与收尾：后处理、预设、多角度、打光、AI 角色、导演台、`/me`；P0 遗留事项中归 P1 的条目；Go 测试迁移。

## 6. P1a 完成后的约束（来自 P1a 最终审查，2026-10-06，写 P1b / P1c 计划必须遵守）

- **事务**：除 `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})` 外，所有 `Begin` 一开始就拿库级写锁（`_txlock=immediate`）。纯读用无事务或 ReadOnly 事务；modernc 忽略隔离级别；ReadOnly 事务里写入在争用时立即 `SQLITE_BUSY`，不等 busy_timeout。
- **自死锁**：持有事务时再通过 `r.db`（而不是 `tx`）写入，会等 10 秒后 `SQLITE_BUSY`。逐个审计接收 `tx` 的函数（`finishTx`、billing `*Tx`、outbox）和 `MutateSnapshot` 回调；事务内不得有网络 I/O；任何持锁超过 10 秒的操作都会让其他写入失败。
- **迁移**：0001 在首个正式版发布前仍可改，发布后冻结；新表放 0002+；纯 `ADD COLUMN` 安全；迁移文件里的 `PRAGMA` 不生效（已由迁移器处理外键，见 P1a 修复）。
- **新表**：凡 id 会进入对象 key 的（如 `gen/task-<id>-<i>`）必须 `INTEGER PRIMARY KEY AUTOINCREMENT`，否则删除后 id 可能被复用，与旧媒体文件冲突；每张表按 0001 的模式加 `updated_at` 触发器；`UPDATE … RETURNING` 看不到触发器写入的时间。
- **时间**：`datetime('now','-90 seconds')`、`CURRENT_TIMESTAMP` 与 `db.TimeLayout` 一致，租约比较可用；绝不直接绑定 `time.Time`（modernc 会写成带 `-07:00` 的格式）。`strftime('%f')` 只有毫秒；`agent_messages.created_at` 需要微秒时由 Go 生成 `"2006-01-02 15:04:05.000000"`，或 keyset 分页改用 id。`MAX(updated_at)`、`COALESCE(time, …)` 这类无声明类型的列会以字符串返回，扫成 string 再解析，不要全局开 `_texttotime`。
- **方言**：`UPDATE … ORDER BY … LIMIT` 不支持（`generation/repository.go:679` 改成 `WHERE id IN (SELECT … LIMIT n)`）；`GREATEST` → 标量 `MAX(a,b)`；`IF` → `iif`；`LAST_INSERT_ID(expr)` → `UPDATE … RETURNING`；`JSON_SEARCH` / `JSON_CONTAINS` → `EXISTS (SELECT 1 FROM json_each(…))`；assets 的 upsert（`LAST_INSERT_ID(id)` + `RowsAffected==1`）→ `INSERT … ON CONFLICT DO NOTHING RETURNING id`，无返回再 `SELECT`；`RowsAffected` 是匹配行数而非改变行数。
- **表需要扩展**：`users` 缺 `seedance_group_id` / `seedance_group_version`（library seedance 经 `users/repository.go:401,411` 使用）、`kind`、`password_changed_at`、`storage_quota_base_bytes`——要么在新迁移里加，要么不原样复制 `users.Repository`。M11 需要在 `model_configs` 上加"用户改过的字段"列（以后 `ADD COLUMN`）。`settings.base_domain` 未预置，P1c 在它为空时要给出明确错误。
- **数据目录迁移（spec S2）**：关闭连接后连同 `-wal` / `-shm` 一起复制，或用 `VACUUM INTO`；只复制 `data.db` 会丢未 checkpoint 的已提交数据。始终关闭 `rows`，长时间存活的读会阻塞 WAL checkpoint。迁移目录前对新路径同样执行 `db.Open` 的 `?` / `file:` 检查。
- **已知差异（接受）**：触发器在值未变化的 UPDATE 上也会刷新 `updated_at`（MySQL 只在值变化时刷新）；`agent_tools` 的 `MAX(updated_at)` 缓存版本只会多失效几次。
