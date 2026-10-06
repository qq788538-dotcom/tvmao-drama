# P1d 其余画布接口与 P1 收尾 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把画布剩下的服务端接口搬进 sidecar 并在 SQLite 上跑通：后处理（去字幕 / 转高清 / 转白模 / 预估 / 配置）、绘图预设、多角度、打光、AI 角色库（含形象图在线加载 + 本地缓存）、Seedance 合规校验（改走 newapi）、3D 导演台模型库，最后接进路由和 sidecar 装配；顺手清掉 P1a 留下的几个测试小项。

**Architecture:** 从参考仓复制代码，只做最小改动：SQL 方言改 SQLite，`*MediaSigner` 等跨计划依赖改成本文件内的窄接口（P1b 的实现天然满足），Seedance 客户端改为「`base_domain` + 设置里配置的接口路径 + `Bearer upstream_api_key`」。为了和 P1b、P1c 完全并发，第一批任务只依赖 P1a；唯一引用 P1c 类型的任务（Task 5）用 `//go:build p1dstub` 的契约桩编译测试，正常构建永远不编译这些桩，最终装配任务（Task 8）删除它们。

**Tech Stack:** Go 1.26 toolchain（module `github.com/james/huabu_studio/apps/api`）、`modernc.org/sqlite` v1.60.1、`github.com/go-chi/chi/v5` v5.2.1；不新增任何第三方依赖。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（F2、F3、F7、X3、X5、M4、M5、M6、S3、S8、S12、SEC4）；事实依据 `docs/superpowers/plans/p1-research.md` 第 3、4、6 节；沿用 `docs/superpowers/plans/2026-10-06-p1a-sqlite-data-layer.md` 的约定；与 `docs/superpowers/plans/2026-10-06-p1b-storage-media-http.md` 的分工见下文“范围”。

**参考仓：** `/Users/james/github/huabu_studio`，基准 commit `fea92ab`。本计划涉及的参考仓文件在 `fea92ab` 与工作区 HEAD 之间无差异（已用 `git diff --stat fea92ab HEAD -- <路径>` 核对），直接从工作区 `cp`。下文 `REF=/Users/james/github/huabu_studio/apps/api/internal`。

## 范围

P1d 交付：

| 功能 | 参考仓来源 | 本计划任务 |
|---|---|---|
| 绘图预设 / 多角度 / 打光：用户端 GET（admin GET/PUT 方法随文件带过来，路由归 P4） | `http/{gen_image_preset,multi_angle,light_control}_handlers.go`、`canvasgen/*_config.go` | Task 1 |
| AI 角色库列表 + 形象图在线加载与本地缓存（spec F7） | `aicharacters/repo.go`、`http/aicharacter_handlers.go`、迁移 0090 | Task 2 |
| Seedance 合规校验改走 newapi（spec X5） | `seedance/client.go`、`library/seedance_{service,assets_repo}.go`、`http/seedance_handlers.go`、迁移 0075/0083/0084 | Task 3 |
| 3D 导演台：用户模型库数据层 + 全局模型库（只读目录） | `directormodels/**`、`directorlibrary/**`、`http/director_library_handlers.go`、迁移 0092/0093 | Task 4 |
| 后处理：去字幕 / 转高清 / 转白模 / 预估 / 配置；纯 Go 视频时长 | `http/canvas_postprocess_handlers.go`、`mediaprobe/probe.go` | Task 5 |
| P1a 遗留测试小项 | `.superpowers/sdd/2026-10-06-p1a-sqlite-data-layer/progress.md` | Task 6 |
| 3D 导演台用户模型库 HTTP（上传 / 列表 / 删除） | `http/director_model_handlers.go` | Task 7 |
| 路由、装配、后台 worker、删除契约桩、端到端测试 | `cmd/server/main.go:417-432,464-475,540-562,656-657`、`http/router.go` | Task 8 |

**相对 p1-research 第 5 节的调整（与 P1b 计划对齐）：** `/me` 和 P0 遗留里标为 P1 的全部事项已由 P1b 承接（见 P1b 计划“本计划的范围”第 1、3 条），P1d 不再做。P1a 进度记录里的 deferred minor 都没有标注归属，本计划把与 P1b/P1c 无关的测试类小项收进 Task 6。

**不在 P1d：** `canvas-script`（P6）；`/admin/canvas/*` 与合规配置、角色图地址的设置页及其路由（P4，handler 方法 `AdminGet`/`AdminPut` 已随文件带过来，P4 只需注册并按 M10 收紧）；`/admin/director/library/*`（生产库为空、spec M8 的设置页清单里没有它，不移植）；`seedance_characters` 的 1729 条种子导入（P2b）；`settings` 的内置默认值（P2a）；前端（P3a）；newapi 侧的合规接口（P7）。项目管理（drama）里所有用到 Seedance / 角色库的路径（`drama.SeedanceService`、`DramaPortraitHandler`、CLI 的 seedance 接口）不移植。

## Global Constraints

- spec F7：「保留，走 newapi：1729 条角色文字数据内置，形象图在线加载并按需缓存（图片地址写进配置）；生成时的 asset://id 通过 newapi 背后的同一火山账号使用」
- spec X5：「保留，生成前强制校验不动；合规调用改走 newapi provider（接口路径配置在 models.json 的 `features.seedanceCompliance`，接口由用户后续在 newapi 上补充）；未配置时入口提示“合规接口未配置”」——models.json 已被 M4 取代，本计划把这份配置放进 settings 表的 `seedance_compliance_config`（M4：「settings 表每项存一个 JSON 值」）。
- spec M4/M5：后处理、预设、多角度、打光的配置照搬参考仓存 settings 表，「预设、多角度、打光的提示词正文……照搬参考仓在 sidecar 内拼接」；用户端接口**绝不下发** `prompt` / `template`。
- spec X3：「积分、估价、价格说明全部删除」——后处理预估接口照搬（前端是否展示由 P3a 定），时长不再承担防篡改计费的职责。
- spec S12：「P1 先不实现上传，带参考媒体的生成按现状保留 URL 签名逻辑的接口位」。
- Go module 路径 `github.com/james/huabu_studio/apps/api`；包路径与参考仓 `apps/api/internal/*` 一致（`aicharacters`、`seedance`、`library`、`directormodels`、`directorlibrary`、`canvasgen`、`mediaprobe`、`http`）。
- 仓库类型改名：`MySQLRepository` → `SQLiteRepository`、`NewMySQLRepository` → `NewSQLiteRepository`、`MySQLSeedanceAssetsRepository` → `SQLiteSeedanceAssetsRepository`、`NewMySQLSeedanceAssetsRepository` → `NewSQLiteSeedanceAssetsRepository`。其余类型名、方法名、导出常量、JSON tag 与参考仓逐字一致，除非本计划写明改动。
- SQLite 约定沿用 P1a：时间列 `DATETIME`、存 UTC 文本、SQL 里用 `CURRENT_TIMESTAMP`、Go 传时间必须 `db.Time(t)`；JSON 参数传 `string`；布尔存 `INTEGER` 0/1；有 `updated_at` 的表加 `AFTER UPDATE … WHEN NEW.updated_at = OLD.updated_at` 触发器；事务内只用 `tx`。
- **SQLite 的 `LIKE` 没有默认转义字符**：凡是参考仓靠 MySQL 默认 `\` 转义的 `LIKE ?`，一律改成 `LIKE ? ESCAPE '\'`。
- 迁移号段：P1b 用 `0002`。**P1d 占用 `0010`–`0019`**：Task 3 `0010_p1d_seedance.sql`、Task 2 `0011_p1d_ai_characters.sql`、Task 4 `0012_p1d_director.sql`，各由对应任务独占创建。首个正式版发布前若要重新编号，由最终合并统一处理。
- `internal/http` 包内新写的测试替身一律带本任务的领域前缀（`preset…`、`aiChar…`、`seedance…`、`director…`、`postprocess…`），避免与 P1b/P1c 的测试文件重名。
- 从参考仓**原样复制**的文件用 `cp` 得到、不改一个字节（P1c 很可能也复制同一批 `canvasgen/*_config.go` 和 `mediaprobe/probe.go`；字节相同的“双方新增”在 git 合并时不冲突）。
- 代码注释用中文；复制来的注释若描述 MySQL / TOS / 积分防篡改等已不成立的行为，就地改写。
- 不改动 `web/**`、`src-tauri/**`、`docs/specs/**`、`server/internal/localauth/**`、`server/go.mod`、`server/go.sum`（Task 8 的 `go mod tidy` 除外）。

## Review Focus

1. **合规接口没配置（这是装好后的默认状态）时用户点“Seedance2.0 合规校验”** → 必须返回 503 和“合规接口未配置”文案，`GET /seedance/config` 返回 `configured:false`，后台 worker 不调上游、不刷日志，生成照常可用。（Task 3 `TestSeedanceVerifyUnconfigured503`、`TestSeedanceConfigReportsConfigured`、`TestSubmitDisabledWhenAnyPieceMissing`）
2. **用户对本机上传 / 生成的素材点合规校验**（spec S12 未定，签出来的是 `http://127.0.0.1:<端口>/media/…`）→ 必须立刻给出可读原因，不能把回环地址发给上游、也不能在表里留一行永远重试的记录。（Task 3 `TestSubmitRefusesLoopbackURL`、`TestSeedanceVerifyLocalMediaRejected`）
3. **AI 角色形象图：地址没配置、远端 404 或超大** → 列表照常返回、`imageConfigured` 如实反映，媒体请求交给原 handler（404），本地不留半截文件；下载成功后断网也能显示（已缓存）。（Task 2 `TestEnsureBaseNotConfigured`、`TestEnsureRemoteErrorNotCached`、`TestEnsureRejectsOversize`、`TestEnsureDownloadsCharacterImageOnce`）
4. **搜索词或 object key 里带 `%`、`_`**（角色库搜索“100%”“snake_case”；导演台模型文件名几乎必带 `_`）→ 必须按字面匹配，不能被当成通配符（SQLite 没有默认转义）。（Task 2 `TestListSearchTreatsWildcardsLiterally`、Task 4 `TestCountProjectsReferencingEscapesAndScopes`）
5. **用户机器上没有 ffprobe，对一个 moov 在文件末尾的 mp4 做去字幕 / 转高清** → 必须用纯 Go 读出时长并提交；不是 mp4 / mov 时返回 400“无法获取视频时长”，不是 500。（Task 5 `TestProbeMP4V1MoovAtEnd`、`TestPortableFallsBackWithoutFFprobe`、`TestDesub_DefaultProbeReadsMP4WithoutFFprobe`、`TestDesub_DefaultProbeRejectsNonMP4`）

另：预设 / 多角度 / 打光的保密正文不出服务端，由 Task 1 原样复制的 `TestGenImagePresetUserGetStripsPrompt`、`TestMultiAngleUserGetStripsSecrets`、`TestLightControlUserGetStripsSecrets` 覆盖。

## 并发执行安排

**速度优先：第一批 6 个任务互不改同一文件，与 P1b、P1c 的执行同时进行，各自在独立 git worktree 里从 `main` 切出。冲突统一在最终合并（Task 8）解决。**

| 批次 | 任务 | 依赖 | 可开始的时间 |
|---|---|---|---|
| 第一批 | **Task 1**（预设/多角度/打光）、**Task 2**（AI 角色库）、**Task 3**（Seedance 合规）、**Task 4**（导演台数据层 + 全局库目录）、**Task 5**（后处理 + 纯 Go 时长）、**Task 6**（P1a 遗留测试） | 只依赖 P1a（已在 main） | 立即 |
| 第二批 | **Task 7**（导演台用户模型库 HTTP） | P1b 已合并进 main（用 `assets.Storage`、`library.OrphansRepository`、`storageUserRepo`、`readUploadForm`）；Task 4 已合并 | P1b 合并后 |
| 第三批 | **Task 8**（路由、装配、删桩、端到端） | P1b、P1c、P1d 第一二批全部合并进 main | 最后 |

第一批合并顺序无要求；每合并一个就在集成分支上跑一次 `go test -tags p1dstub ./...`（Task 5 合并前不需要 tag）。P1c 合并之前，凡是包含 Task 5 的分支都要带 `-tags p1dstub` 编译 `internal/http`；Task 8 删除桩之后不再需要。

**文件归属（同批零重叠）：**

| 任务 | 独占的路径 | 与其他计划“字节相同的新增”（合并不冲突） |
|---|---|---|
| 1 | `server/internal/canvasgen/{image_preset,multi_angle,light_control}_config.go` 及 `image_preset_params_test.go`、`multi_angle_test.go`、`light_control_test.go`；`server/internal/http/{gen_image_preset,multi_angle,light_control}_handlers.go`、`gen_image_preset_test.go`、`multi_angle_test.go`、`light_control_test.go`、`preset_testhelpers_test.go` | `canvasgen/postprocess_config.go`+`_test.go`（与 Task 5、P1c 相同）；`http/helpers.go`、`http/context.go`（与 P1b Task 5 相同）；`router.go` 删除 `writeJSON`（与 P1b Task 5 相同的删除） |
| 2 | `server/internal/db/migrations/0011_p1d_ai_characters.sql`、`server/internal/aicharacters/**`、`server/internal/http/aicharacter_handlers.go`、`aicharacter_handlers_test.go` | 同上 `helpers.go`、`context.go`、`router.go` 删除 |
| 3 | `server/internal/db/migrations/0010_p1d_seedance.sql`、`server/internal/seedance/**`、`server/internal/library/seedance_*.go`（含测试）、`server/internal/http/seedance_handlers.go`、`seedance_handlers_test.go` | 同上 |
| 4 | `server/internal/db/migrations/0012_p1d_director.sql`、`server/internal/directormodels/**`、`server/internal/directorlibrary/**`、`server/internal/http/director_library_handlers.go`、`director_library_handlers_test.go` | 同上 |
| 5 | `server/internal/mediaprobe/mp4.go`、`mp4_test.go`；`server/internal/canvasgen/p1dstub_enqueue.go`；`server/internal/generation/p1dstub.go`；`server/internal/http/canvas_postprocess_handlers.go`、`canvas_postprocess_handlers_test.go` | `mediaprobe/probe.go`、`probe_test.go`（P1c 若移植则相同）；`canvasgen/postprocess_config.go`+`_test.go`；`helpers.go`、`context.go`、`router.go` 删除 |
| 6 | `server/internal/db/sqlite_test.go`、`server/internal/settings/repository_test.go`、`server/internal/models/repository_test.go` | — |
| 7 | `server/internal/http/director_model_handlers.go`、`director_model_handlers_test.go` | — |
| 8 | `server/internal/http/router.go`、`server/cmd/sidecar/**`、`server/go.mod`、`server/go.sum`；删除全部 `p1dstub*.go`；两处一行改动：P1b 的 `http/asset_handlers.go`、P1c 的 `generation/service.go`（见 Task 8） | — |

**预计的合并冲突与处理（都在 Task 8 处理）：** ① `router.go`：P1b Task 12 重写，Task 8 以 P1b 版本为底加 P1d 路由；② `helpers.go` / `context.go`：若 P1b 改过内容，以 P1b 为准；③ `canvasgen/*_config.go`：若 P1c 改过，以 P1c 为准再跑 Task 1 的测试；④ `users` 表的 `seedance_group_id` / `seedance_group_version`：若别的计划也加了，删掉 `0010` 里的两条 `ALTER`；⑤ 迁移编号冲突则改名。

---

## 移植事实清单

### 新增表的最终结构（由参考仓迁移推导）

| 表 | 参考仓迁移 | SQLite 结构要点 |
|---|---|---|
| `seedance_assets` | 0075 建表，0084 加 `asset_type`（之后无改动，已 grep 0094–0118 核对） | `object_key TEXT PRIMARY KEY`、`user_id INTEGER NOT NULL`、`asset_id TEXT NULL`、`version INTEGER NULL`、`status TEXT NOT NULL`、`error TEXT NULL`、`first_failed_at DATETIME NULL`、`created_at`、`updated_at`（触发器）、`asset_type TEXT NOT NULL DEFAULT 'Image'`；索引 `status`、`user_id` |
| `users` 扩列 | 0075 `seedance_group_id`，0083 `seedance_group_version` | `ALTER TABLE users ADD COLUMN seedance_group_id TEXT NULL`、`ADD COLUMN seedance_group_version INTEGER NOT NULL DEFAULT 0`（p1-research 第 6 节“表需要扩展”） |
| `seedance_characters` | 0090（建表 + 1729 行 `INSERT IGNORE`） | `asset_id TEXT PRIMARY KEY`、`age INTEGER`、`gender`、`occupation`、`description`、`age_group`、`image_url TEXT`、`created_at`、`updated_at`（触发器）；**数据不在迁移里**，P2b 从 `seed/seedance_characters.jsonl` 导入；额外加 `image_url` 索引（`PublicAssetIDs` 每次生成和每个媒体请求都会按它查） |
| `director_user_models` | 0092 | `id INTEGER PRIMARY KEY AUTOINCREMENT`、`user_id`、`name`、`file_name`、`object_key`、`size_bytes`、`mime_type`、`created_at DATETIME`（原 `TIMESTAMP`）；索引 `user_id` |
| `director_library_categories` | 0093 | `id … AUTOINCREMENT`、`name`、`sort_order INTEGER DEFAULT 0`、`enabled INTEGER DEFAULT 1`、`created_at` |
| `director_library_models` | 0093 | `id … AUTOINCREMENT`、`category_id INTEGER NOT NULL REFERENCES director_library_categories(id) ON DELETE RESTRICT`、`name`、`file_name`、`object_key`、`thumb_object_key TEXT NULL`、`size_bytes`、`mime_type`、`sort_order`、`enabled`、`created_at`；索引 `category_id`。生产库为空，内置库也为空 |

### MySQL 方言与改写（P1d 移植代码里的全部）

| 位置 | MySQL | SQLite 改写 | 任务 |
|---|---|---|---|
| `aicharacters/repo.go` `List` | `occupation LIKE ? OR description LIKE ?`（依赖 MySQL 默认 `\` 转义 `escapeLike` 的结果） | `LIKE ? ESCAPE '\'` | 2 |
| `library/seedance_assets_repo.go` `Upsert` | `ON DUPLICATE KEY UPDATE … VALUES(x)` | `ON CONFLICT (object_key) DO UPDATE SET … = excluded.x` | 3 |
| `library/seedance_assets_repo.go` `SetError` | `COALESCE(first_failed_at, NOW())` | `COALESCE(first_failed_at, CURRENT_TIMESTAMP)` | 3 |
| `library/seedance_assets_repo.go` `Upsert` 参数 | 直接传 `*string`、`*int` | 显式转成 `string`/`int64`/`nil` 再传（不依赖驱动对指针的处理） | 3 |
| `users/repository.go:398-413` `Get/SetSeedanceGroup` | 在 `users.Repository` 上 | P1b 新写的 `users` 包只含存储用量方法，P1d 另写 `library.SQLiteSeedanceGroupStore`，SQL 不变 | 3 |
| `directormodels/repo.go` `CountProjectsReferencing`、`directorlibrary/repo.go` `CountProjectsReferencingAllUsers` | `ws.payload LIKE ?`（同上，依赖默认转义） | `ws.payload LIKE ? ESCAPE '\'` | 4 |
| `directorlibrary` `enabled TINYINT(1)` 扫进 `bool` | MySQL 0/1 | 不改代码；`INTEGER` 0/1 经 `database/sql` 转 `bool`（测试覆盖） | 4 |
| `director_*` 的 `created_at TIMESTAMP` | — | 声明为 `DATETIME`，modernc 才会解析成 `time.Time` | 4 |
| `seedance_handlers.go` 归属查询 `SELECT (SELECT COUNT(*) …) + (SELECT COUNT(*) …)` | — | SQLite 原生支持，不改 | 3 |

### 依赖已删除包的代码与改法

| 被移植文件 | 依赖 | 改法 |
|---|---|---|
| `aicharacters/repo.go` | `assets.MediaProtocol`（参考仓定义在 `assets/minio.go`，P1b 不复制） | 包内常量 `mediaProtocol = "media://"`（`library/seedance_service.go` 原本就是这个做法） |
| `http/aicharacter_handlers.go` | `*MediaSigner`（P1b） | 窄接口 `aiCharacterSigner{Sign; SignThumb}`；构造函数多一个 settings 读取函数 |
| `seedance/client.go` | 直连火山方舟素材库：`sd-key` 头、`settings.drama_seedance_base_url`、默认生产域名 `api.laomandi.com` | 重写为 newapi：`base_domain` + `seedance_compliance_config` 的路径 + `Authorization: Bearer <upstream_api_key>`；删掉默认域名；请求/响应 JSON 形状不变 |
| `library/seedance_service.go` | `settings.drama_seedance_sd_key`、`drama_seedance_version`（drama 后台配置） | 改读 `upstream_api_key`、`base_domain`、`seedance_compliance_config`（含 `version`）；`ErrSeedanceDisabled` 文案改为“合规接口未配置…”；新增 `Configured()`；签出回环地址时拒绝（S12） |
| `http/seedance_handlers.go` | `assets.Storage`（只用 `ExtractKey`） | 窄接口 `seedanceKeyExtractor`；新增 `GET /seedance/config` |
| `http/director_library_handlers.go` | `*MediaSigner`、`assets.MediaProtocol` | 窄接口 `directorSigner{Sign; SignThumb}`、包内常量 `directorMediaProtocol` |
| `http/director_model_handlers.go` | `assets.Storage`、`library.OrphansRepository`、`storageUserRepo`、`readUploadForm`、`uniqueUploadFilename`（全在 P1b） | 第二批执行、直接用 P1b 的实现；签名器同上用 `directorSigner` |
| `http/canvas_postprocess_handlers.go` | `*MediaSigner`；`postprocessModelLookup`（参考仓定义在 drama 文件里）；`mediaprobe.ProbeDurationMs`（调系统 ffprobe）；`canvasgen.EnqueueInput`、`generation.Task`/`ErrStorageQuotaExceeded`（P1c） | 窄接口 `postprocessSigner{ObjectKey; Sign}`；`postprocessModelLookup` 搬进本文件；默认探测器换成 `mediaprobe.ProbeDurationMsPortable`；P1c 类型在合并前由 `p1dstub` 桩提供 |
| `cmd/server/main.go:420-432` | `drama.NewSeedanceService`、`usersRepo` 作为素材组存储 | Task 8 只装配 `library.SeedanceService`，素材组存储用 `library.NewSQLiteSeedanceGroupStore(conn)` |
| billing | `CanvasPostprocessHandler.estimateCredits` 用 `billing.EstimateFreeze` | 不改（P1a 已移植纯函数，spec X3 不计费但接口保留） |
| teams | P1d 移植的代码无团队分支 | — |

### 参考仓测试的移植

| 参考仓测试 | 处理 | 任务 |
|---|---|---|
| `canvasgen/postprocess_config_test.go`、`image_preset_params_test.go`、`multi_angle_test.go`、`light_control_test.go` | 原样复制 | 1（postprocess 另见 5） |
| `canvasgen/image_preset_test.go` | 不复制（依赖 `generation` 与 `canvasgen.Service`，归 P1c） | — |
| `http/gen_image_preset_test.go`、`multi_angle_test.go`、`light_control_test.go` | 复制，`memSettings` 改名 `presetMemSettings`（参考仓定义在不移植的 `cli_release_test.go`） | 1 |
| `http/aicharacter_handlers_test.go` | 复制，构造函数改用替身签名器 + settings 读取函数；新增 `imageConfigured` 断言 | 2 |
| `seedance/client_test.go` | 复制，改：`NewClient` 多传路径；`sd-key` 断言改 `Authorization`；默认域名测试替换为“未配置”测试 | 3 |
| `library/seedance_service_test.go` | 复制，四处 settings 构造改用 `enabledSettings(n)`；新增回环拒绝、未配置、`Configured` 测试 | 3 |
| `http/seedance_handlers` | 参考仓无测试，新写 | 3 |
| `directormodels/service_test.go`、`directorlibrary/service_test.go` | 原样复制 | 4 |
| 仓库层 | 参考仓无 DB 测试，新写 SQLite 测试 | 2、3、4 |
| `http/canvas_postprocess_handlers_test.go` | 复制，`newNoopSigner()` 换成替身、`httpFakeModelLookup` 改名并补定义；新增两条默认探测器测试 | 5 |
| `mediaprobe/probe_test.go` | 原样复制（无 ffprobe 时自动 skip） | 5 |
| `http/director_model_quota_test.go` | 移植进 `director_model_handlers_test.go`，替身改名、换签名器；新增删除流程测试 | 7 |

---

## 对其他计划的接口假设（以参考仓接口为契约）

| 提供方 | P1d 使用的东西（参考仓签名） | 用在 |
|---|---|---|
| P1b | `(*http.MediaSigner).Sign(string) string`、`SignThumb(string, int) string`、`ObjectKey(string) string` | Task 2、4、5、7、8 |
| P1b | `assets.Storage`（9 个方法，`assets/storage.go:37-59`），`ExtractKey` 认 `media://` 与本地媒体 URL；`GetObject`、`PutObject` | Task 3（ExtractKey）、7、8 |
| P1b | `GET`/`HEAD /media/*`，URL 形如 `http://127.0.0.1:<端口>/media/<逐段转义的 key>?token=<媒体 token>`；`r.URL.Path` 去掉 `/media/` 前缀即 object key | Task 8 包一层 `ImageCache.Middleware` |
| P1b | `library.OrphansRepository.Mark(tx library.Execer, objectKey string, userID, sizeBytes int64) error`、`library.NewSQLiteOrphansRepository(*sql.DB)`、`orphan_objects` 表（0002） | Task 7 |
| P1b | `storageUserRepo{AddStorage(id, delta int64) error; StorageUsage(id int64) (used, quota int64, err error)}`、`readUploadForm`、`uploadError`、`uniqueUploadFilename`、`http.LocalUserID` | Task 7 |
| P1b | `library_items` 表（0002，含 `user_id`、`object_key`） | Task 3 归属查询 |
| P1c | `canvasgen.EnqueueInput{UserID, ProjectID int64; NodeID, ModelID string; Inputs map[string]any; PresetKey string; PresetParams map[string]string}`、`(*canvasgen.Service).Enqueue(ctx, EnqueueInput) (int64, error)` | Task 5、8 |
| P1c | `generation.Task{ID int64; Output json.RawMessage; …}`、`(*generation.Service).Get(ctx, userID, taskID int64) (*generation.Task, error)`、`generation.ErrNotFound`、`generation.ErrStorageQuotaExceeded`、`(*generation.Service).WithRealPersonResolver(RealPersonResolver)`、`SetPromptPresetResolver(func(key string, params map[string]string) (string, bool))` | Task 5、8 |
| P1c | `canvasgen/*_config.go` 原样复制（若 P1c 也复制） | Task 1、5 |

**P1d 给其他计划的约定：**

- **P2a（settings 种子）**：新 key `seedance_compliance_config`（JSON：`createAssetGroupPath`、`createAssetPath`、`getAssetPath`、`version`）与 `ai_character_image_base_url`（字符串，形象图地址前缀）。不预置 = “未配置”。
- **P2b（角色导入）**：往 `seedance_characters` 插 `asset_id, age, gender, occupation, description, age_group, image_url`，`image_url` 保持 `media://tapnow/sd-assets/…` 原样（本计划的缓存和合规兜底都靠这个形态）。
- **P3a（前端）**：`GET /api/v1/seedance/config` → `{"configured": bool}`，为 false 时合规入口提示“合规接口未配置”；`POST /seedance/verify` 未配置时 503、本机素材 400（文案含“公网”）；`GET /canvas/ai-characters` 响应多一个 `imageConfigured`。
- **P4（设置页）**：注册 `/admin/canvas/{postprocess-config,gen-image-presets,multi-angle,light-control}` 的 GET/PUT（handler 方法已在），编辑上面两个新 settings key。
- **P7（newapi）**：三个接口都是 `POST {base_domain}{path}`、`Authorization: Bearer <key>`、`Content-Type: application/json`；请求体与响应体与火山方舟素材库一致：CreateAssetGroup `{"Name","Description"}` → `{"Id"}`；CreateAsset `{"GroupId","URL","Name","AssetType":"Image|Video|Audio"}` → `{"Id"}`；GetAsset `{"Id"}` → `{"Status":"Active|Failed|…"}`；业务错误 `{"Code","Message"}`，允许外包一层 `data` / `Data` / `result` / `Result`。
- **P8（CSP）**：角色形象图由 sidecar 下载后经 `/media/*` 提供，WebView 不直连图片域名，CSP 不需要放行该域名（SEC4 的这一项可以去掉）。

---

## 文件结构

```
server/internal/
  db/migrations/
    0010_p1d_seedance.sql          Task 3：seedance_assets + users 两列
    0011_p1d_ai_characters.sql     Task 2：seedance_characters（无数据）
    0012_p1d_director.sql          Task 4：director_user_models、director_library_*
  canvasgen/
    postprocess_config.go(+_test)  Task 1、5：原样复制（字节相同）
    image_preset_config.go, multi_angle_config.go, light_control_config.go (+tests)   Task 1：原样复制
    p1dstub_enqueue.go             Task 5：//go:build p1dstub 契约桩，Task 8 删除
  generation/p1dstub.go            Task 5：同上
  mediaprobe/
    probe.go, probe_test.go        Task 5：原样复制
    mp4.go, mp4_test.go            Task 5：新写（纯 Go 读 mvhd 时长 + Portable 探测器）
  aicharacters/
    repo.go                        Task 2：移植
    image_cache.go                 Task 2：新写（形象图按需下载缓存 + /media 中间件）
    repo_test.go, image_cache_test.go   Task 2：新写
  seedance/
    client.go                      Task 3：重写为 newapi 版
    client_test.go                 Task 3：移植
  library/
    seedance_service.go            Task 3：移植（配置来源、回环拒绝、Configured）
    seedance_assets_repo.go        Task 3：移植
    seedance_groups_repo.go        Task 3：新写（users 表上的素材组 id）
    seedance_service_test.go       Task 3：移植
    seedance_repo_test.go          Task 3：新写
  directormodels/ repo.go, service.go (+service_test.go 原样, repo_test.go 新写)   Task 4
  directorlibrary/ repo.go, service.go (+service_test.go 原样, repo_test.go 新写)  Task 4
  http/
    helpers.go, context.go         Task 1/2/3/4/5：缺失时原样复制（与 P1b 字节相同）
    gen_image_preset_handlers.go, multi_angle_handlers.go, light_control_handlers.go (+tests), preset_testhelpers_test.go   Task 1
    aicharacter_handlers.go(+_test)      Task 2
    seedance_handlers.go(+_test)         Task 3
    director_library_handlers.go(+_test) Task 4
    canvas_postprocess_handlers.go(+_test)   Task 5
    director_model_handlers.go(+_test)   Task 7
    router.go                      Task 8
server/cmd/sidecar/                Task 8：装配 + p1d_routes_test.go
```

---

### Task 1: 绘图预设、多角度、打光（配置包 + 用户端接口）

**Files:**
- Create（原样复制）: `server/internal/canvasgen/postprocess_config.go`、`image_preset_config.go`、`multi_angle_config.go`、`light_control_config.go`、`postprocess_config_test.go`、`image_preset_params_test.go`、`multi_angle_test.go`、`light_control_test.go`
- Create（原样复制）: `server/internal/http/gen_image_preset_handlers.go`、`multi_angle_handlers.go`、`light_control_handlers.go`
- Create（复制后改名）: `server/internal/http/gen_image_preset_test.go`、`multi_angle_test.go`、`light_control_test.go`
- Create: `server/internal/http/preset_testhelpers_test.go`
- Create（缺失时，原样复制）: `server/internal/http/helpers.go`、`server/internal/http/context.go`
- Modify: `server/internal/http/router.go`（删除 `writeJSON` 与 `encoding/json` 导入）

**Interfaces:**
- Consumes: `settings.Repository`（P1a：`Get(key) (string, error)`、`Set(key, value) error`、`All() (map[string]string, error)`）
- Produces（Task 8 装配用）: `http.NewGenImagePresetHandler(settings.Repository) *GenImagePresetHandler`、`http.NewMultiAngleHandler(settings.Repository) *MultiAngleHandler`、`http.NewLightControlHandler(settings.Repository) *LightControlHandler`，方法 `UserGet`、`AdminGet`、`AdminPut`（全是 `http.HandlerFunc` 形状）；`canvasgen.SettingCanvasPostprocess`、`SettingGenImagePresets`、`SettingCanvasMultiAngle`、`SettingCanvasLightControl`、`LoadCanvasPostprocessConfig`、`LoadGenImagePresetsConfig`、`LoadMultiAngleConfig`、`LoadLightControlConfig`、`MultiAnglePresetKey`、`LightControlPresetKey`、`(GenImagePresetEntry).ComposePresetText`、`(MultiAngleConfig).ComposeMultiAngleText`、`(LightControlConfig).ComposeLightControlText`。

- [ ] **Step 1: http 基础件（与 P1b 字节相同，已存在就跳过）**

```bash
cd /path/to/worktree   # 本任务的 worktree 根目录
REF=/Users/james/github/huabu_studio/apps/api/internal
test -f server/internal/http/helpers.go || cp $REF/http/helpers.go server/internal/http/helpers.go
test -f server/internal/http/context.go || cp $REF/http/context.go server/internal/http/context.go
# router.go 里 P0 写的 writeJSON 与 helpers.go 同名同签名，删掉它和随之无用的 encoding/json 导入
grep -q '^func writeJSON' server/internal/http/router.go && \
  perl -0pi -e 's/\nfunc writeJSON\(w nethttp\.ResponseWriter, status int, v any\) \{.*?\n\}\n//s; s/\t"encoding\/json"\n//' server/internal/http/router.go
cd server && go build ./internal/http/ && cd ..
```

Expected: `go build` 无输出。

- [ ] **Step 2: 复制测试（先让它失败）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/canvasgen
cp $REF/canvasgen/postprocess_config_test.go $REF/canvasgen/image_preset_params_test.go \
   $REF/canvasgen/multi_angle_test.go $REF/canvasgen/light_control_test.go server/internal/canvasgen/
cp $REF/http/gen_image_preset_test.go $REF/http/multi_angle_test.go $REF/http/light_control_test.go server/internal/http/
perl -pi -e 's/newMemSettings\(\)/newPresetMemSettings()/g; s/\*memSettings\b/*presetMemSettings/g; s/^\/\/ memSettings 复用 cli_release_test\.go 里的定义（同包）。$/\/\/ presetMemSettings 定义在 preset_testhelpers_test.go。/' \
  server/internal/http/gen_image_preset_test.go server/internal/http/multi_angle_test.go server/internal/http/light_control_test.go
grep -n 'memSettings' server/internal/http/gen_image_preset_test.go server/internal/http/multi_angle_test.go server/internal/http/light_control_test.go | grep -v presetMemSettings
```

Expected: 最后一条 grep 无输出（全部改名完毕）。

新建 `server/internal/http/preset_testhelpers_test.go`：

```go
package http

// presetMemSettings 是 settings.Repository 的内存实现，供预设 / 多角度 / 打光的测试用。
// 参考仓叫 memSettings、定义在不移植的 cli_release_test.go；改名避免与其他任务的测试替身重名。
type presetMemSettings struct{ kv map[string]string }

func newPresetMemSettings() *presetMemSettings { return &presetMemSettings{kv: map[string]string{}} }

func (s *presetMemSettings) Get(k string) (string, error)    { return s.kv[k], nil }
func (s *presetMemSettings) Set(k, v string) error           { s.kv[k] = v; return nil }
func (s *presetMemSettings) All() (map[string]string, error) { return s.kv, nil }
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd server && go test ./internal/canvasgen/ ./internal/http/ 2>&1 | head -20`
Expected: 编译失败，报 `undefined: LoadCanvasPostprocessConfig`、`undefined: NewGenImagePresetHandler` 一类错误。

- [ ] **Step 4: 复制实现**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/canvasgen/postprocess_config.go $REF/canvasgen/image_preset_config.go \
   $REF/canvasgen/multi_angle_config.go $REF/canvasgen/light_control_config.go server/internal/canvasgen/
cp $REF/http/gen_image_preset_handlers.go $REF/http/multi_angle_handlers.go $REF/http/light_control_handlers.go server/internal/http/
grep -h '^\s*"' server/internal/canvasgen/*_config.go | sort -u
```

Expected: 只出现标准库（`encoding/json`、`fmt`、`regexp`、`strconv`、`strings`）。若出现 `github.com/...`，停下报告 BLOCKED。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd server && go vet ./internal/canvasgen/ ./internal/http/ && go test ./internal/canvasgen/ ./internal/http/ -v -run 'Compose|Preset|MultiAngle|LightControl|Postprocess|Desub|Upscale|LightDirection' 2>&1 | tail -30`
Expected: 全部 PASS（含 `TestGenImagePresetUserGetStripsPrompt`、`TestMultiAngleUserGetStripsSecrets`、`TestLightControlUserGetStripsSecrets`、`TestLoadCanvasPostprocessConfig_LegacyUpscaleModel`）。再跑 `go test ./...` 确认 P1a 的测试没被影响。

- [ ] **Step 6: 提交**

```bash
git add server/internal/canvasgen server/internal/http/helpers.go server/internal/http/context.go server/internal/http/router.go \
  server/internal/http/gen_image_preset_handlers.go server/internal/http/multi_angle_handlers.go server/internal/http/light_control_handlers.go \
  server/internal/http/gen_image_preset_test.go server/internal/http/multi_angle_test.go server/internal/http/light_control_test.go \
  server/internal/http/preset_testhelpers_test.go
git commit -m "feat(server): 移植绘图预设、多角度、打光配置与用户端接口"
```

---

### Task 2: AI 角色库（表、仓库、形象图缓存、列表接口）

**Files:**
- Create: `server/internal/db/migrations/0011_p1d_ai_characters.sql`
- Create（移植）: `server/internal/aicharacters/repo.go`
- Create: `server/internal/aicharacters/image_cache.go`
- Test: `server/internal/aicharacters/repo_test.go`、`server/internal/aicharacters/image_cache_test.go`
- Create（移植）: `server/internal/http/aicharacter_handlers.go`；Test: `server/internal/http/aicharacter_handlers_test.go`
- Create（缺失时，原样复制）: `server/internal/http/helpers.go`、`context.go`；Modify: `server/internal/http/router.go`（同 Task 1 Step 1）

**Interfaces:**
- Consumes: `dbtest.Open(t)`（P1a）
- Produces:
  - `aicharacters.Character`、`ListParams`、`Repository`（`List`、`Get`、`PublicAssetIDs`）、`ErrNotFound`、`SQLiteRepository`、`NewSQLiteRepository(*sql.DB) *SQLiteRepository`
  - `aicharacters.SettingImageBaseURL = "ai_character_image_base_url"`、`ErrImageBaseNotConfigured`
  - `aicharacters.ObjectStore` 接口：`Exists(ctx, key string) bool`、`Put(ctx, key, contentType string, body []byte) error`
  - `aicharacters.NewImageCache(repo Repository, get func(string) (string, error), store ObjectStore) *ImageCache`；`(*ImageCache).Ensure(ctx, key string) (handled bool, err error)`；`(*ImageCache).Middleware(next http.Handler) http.Handler`
  - `http.NewAICharacterHandler(repo aicharacters.Repository, signer aiCharacterSigner, settingsGet func(string) (string, error)) *AICharacterHandler`，方法 `List`；`aiCharacterSigner` = `{Sign(string) string; SignThumb(string, int) string}`（`*MediaSigner` 满足）

**设计说明（spec F7）：** 角色 `image_url` 保持参考仓的 `media://tapnow/sd-assets/<id>.jpg` 形态，列表照参考仓用 `MediaSigner` 签成本地 `/media/…` URL。本地还没有这张图时，`ImageCache.Middleware`（Task 8 包在 P1b 的 `/media/*` 外面）先从 `<ai_character_image_base_url>/<key>` 下载存进本地媒体目录，再交给原 handler。这样合规状态兜底（`PublicAssetIDs`）、生成时的 `asset://` 替换（P1c 的 `replaceRealPersonImageRefs` 先 `ExtractKey` 再 `Resolve`）、快照 canonical 化全部不用改。缓存不写 `assets` 行：公共图不进用户配额和孤儿回收（参考仓 `aicharacters` 包注释的要求）。

- [ ] **Step 1: http 基础件（与 P1b 字节相同，已存在就跳过）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
test -f server/internal/http/helpers.go || cp $REF/http/helpers.go server/internal/http/helpers.go
test -f server/internal/http/context.go || cp $REF/http/context.go server/internal/http/context.go
grep -q '^func writeJSON' server/internal/http/router.go && \
  perl -0pi -e 's/\nfunc writeJSON\(w nethttp\.ResponseWriter, status int, v any\) \{.*?\n\}\n//s; s/\t"encoding\/json"\n//' server/internal/http/router.go
cd server && go build ./internal/http/ && cd ..
```

Expected: `go build` 无输出。

- [ ] **Step 2: 写迁移**

`server/internal/db/migrations/0011_p1d_ai_characters.sql`：

```sql
-- P1d：自由画布「AI 角色库」（参考仓迁移 0090）。只建表；1729 条角色数据由 P2b 从
-- seed/seedance_characters.jsonl 导入，image_url 保持 canonical 的 media://<object_key> 形态。
CREATE TABLE seedance_characters (
  -- Seedance 素材库分配的角色资产 id（asset-YYYYMMDDHHMMSS-xxxxx），生成时以 asset://<id> 引用。
  asset_id TEXT PRIMARY KEY,
  age INTEGER NOT NULL,
  -- 男 / 女
  gender TEXT NOT NULL,
  occupation TEXT NOT NULL,
  description TEXT NOT NULL,
  -- 青年 / 中年 / 老年
  age_group TEXT NOT NULL,
  -- 形象图 canonical 引用 media://<object_key>；图片本体由 sidecar 按需从 ai_character_image_base_url 下载缓存。
  image_url TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- 参考仓没有这个索引；桌面端每次生成（asset:// 替换）和每个 /media 请求（缓存判定）都按 image_url 查。
CREATE INDEX idx_seedance_characters_image_url ON seedance_characters (image_url);

CREATE TRIGGER trg_seedance_characters_updated_at AFTER UPDATE ON seedance_characters
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE seedance_characters SET updated_at = CURRENT_TIMESTAMP WHERE asset_id = NEW.asset_id;
END;
```

- [ ] **Step 3: 写仓库的失败测试**

`server/internal/aicharacters/repo_test.go`：

```go
package aicharacters

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func seedCharacters(t *testing.T, conn *sql.DB) {
	t.Helper()
	rows := []struct {
		id    string
		age   int
		g     string
		occ   string
		desc  string
		group string
		img   string
	}{
		{"asset-001", 25, "女", "演员", "常演宫女", "青年", "media://tapnow/sd-assets/asset-001.jpg"},
		{"asset-002", 45, "男", "厨师", "川菜馆主厨", "中年", "media://tapnow/sd-assets/asset-002.jpg"},
		{"asset-003", 70, "男", "养蜂人", "只卖100%纯蜂蜜", "老年", "media://tapnow/sd-assets/asset-003.png"},
		{"asset-004", 30, "女", "程序员", "只写 snake_case 代码", "青年", "media://tapnow/sd-assets/asset-004.jpg"},
	}
	for _, r := range rows {
		if _, err := conn.Exec(`INSERT INTO seedance_characters
			(asset_id, age, gender, occupation, description, age_group, image_url) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.age, r.g, r.occ, r.desc, r.group, r.img); err != nil {
			t.Fatal(err)
		}
	}
}

func assetIDs(list []Character) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.AssetID)
	}
	return out
}

func TestListFiltersAndOrder(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	cases := []struct {
		name string
		p    ListParams
		want []string
	}{
		{"全部按 asset_id 排序", ListParams{Limit: 10}, []string{"asset-001", "asset-002", "asset-003", "asset-004"}},
		{"性别", ListParams{Gender: "女", Limit: 10}, []string{"asset-001", "asset-004"}},
		{"年龄段", ListParams{AgeGroup: "老年", Limit: 10}, []string{"asset-003"}},
		{"搜职业", ListParams{Search: "演员", Limit: 10}, []string{"asset-001"}},
		{"搜描述", ListParams{Search: "川菜", Limit: 10}, []string{"asset-002"}},
		{"分页", ListParams{Limit: 2, Offset: 1}, []string{"asset-002", "asset-003"}},
	}
	for _, c := range cases {
		got, err := repo.List(c.p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(assetIDs(got), c.want) {
			t.Fatalf("%s: got %v want %v", c.name, assetIDs(got), c.want)
		}
	}
}

// SQLite 的 LIKE 没有默认转义字符：漏写 ESCAPE 时 "_" 会匹配任意单字、"%" 会匹配全部。
func TestListSearchTreatsWildcardsLiterally(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	for search, want := range map[string][]string{
		"%":    {"asset-003"},
		"100%": {"asset-003"},
		"_":    {"asset-004"},
		"e_c":  {"asset-004"},
	} {
		got, err := repo.List(ListParams{Search: search, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(assetIDs(got), want) {
			t.Fatalf("search %q: got %v want %v", search, assetIDs(got), want)
		}
	}
}

func TestGetAndNotFound(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	c, err := repo.Get("asset-002")
	if err != nil {
		t.Fatal(err)
	}
	if c.Age != 45 || c.Gender != "男" || c.ImageURL != "media://tapnow/sd-assets/asset-002.jpg" {
		t.Fatalf("got %+v", c)
	}
	if _, err := repo.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPublicAssetIDsMapsBareKeys(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	got, err := repo.PublicAssetIDs([]string{"tapnow/sd-assets/asset-002.jpg", "u1/2026/10/mine.png"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"tapnow/sd-assets/asset-002.jpg": "asset-002"}) {
		t.Fatalf("got %v", got)
	}
	empty, err := repo.PublicAssetIDs(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空输入应返回空 map: %v %v", empty, err)
	}
}
```

- [ ] **Step 4: 运行确认失败**

Run: `cd server && go test ./internal/aicharacters/ 2>&1 | head`
Expected: 编译失败 `undefined: NewSQLiteRepository`。

- [ ] **Step 5: 移植仓库**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/aicharacters
cp $REF/aicharacters/repo.go server/internal/aicharacters/repo.go
cd server/internal/aicharacters
perl -pi -e 's/NewMySQLRepository/NewSQLiteRepository/g; s/MySQLRepository/SQLiteRepository/g; s/assets\.MediaProtocol/mediaProtocol/g' repo.go
perl -0pi -e 's/\n\t"github\.com\/james\/huabu_studio\/apps\/api\/internal\/assets"\n/\n/' repo.go
perl -pi -e 's/q \+= ` AND \(occupation LIKE \? OR description LIKE \?\)`/q += ` AND (occupation LIKE ? ESCAPE '"'"'\\'"'"' OR description LIKE ? ESCAPE '"'"'\\'"'"')`/' repo.go
perl -pi -e 's/（seedance_characters 表，随 migration 0090 整批导入）/（seedance_characters 表，由内置种子数据导入，见 P2b）/; s/预置数据（migration 0090 导入）/预置数据（内置种子导入）/' repo.go
gofmt -w repo.go
grep -n 'ESCAPE\|mediaProtocol\|assets' repo.go
cd ../../..
```

Expected: grep 显示 `LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\'`、两处 `mediaProtocol`，没有 `assets.` 引用。若 ESCAPE 没替换上（shell 引号被改坏），手工把那一行改成：

```go
		q += ` AND (occupation LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\')`
```

然后在 `repo.go` 的 `var ErrNotFound` 之前加入：

```go
// mediaProtocol 与 assets.MediaProtocol 同值。不 import assets：本包只需要知道
// canonical URL 的形态（同 library/seedance_service.go 的做法）。
const mediaProtocol = "media://"
```

- [ ] **Step 6: 运行仓库测试确认通过**

Run: `cd server && go test ./internal/aicharacters/ -v`
Expected: 4 个测试 PASS。

- [ ] **Step 7: 写形象图缓存的失败测试**

`server/internal/aicharacters/image_cache_test.go`：

```go
package aicharacters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

type memObjectStore struct {
	mu    sync.Mutex
	objs  map[string][]byte
	types map[string]string
}

func newMemObjectStore() *memObjectStore {
	return &memObjectStore{objs: map[string][]byte{}, types: map[string]string{}}
}

func (m *memObjectStore) Exists(_ context.Context, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[key]
	return ok
}

func (m *memObjectStore) Put(_ context.Context, key, contentType string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = body
	m.types[key] = contentType
	return nil
}

// newCacheFixture 起一个假“图片站”，返回缓存、对象存储和命中计数。
func newCacheFixture(t *testing.T, handler http.HandlerFunc, base func(srvURL string) string) (*ImageCache, *memObjectStore, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	store := newMemObjectStore()
	b := base(srv.URL)
	get := func(key string) (string, error) {
		if key == SettingImageBaseURL {
			return b, nil
		}
		return "", nil
	}
	return NewImageCache(NewSQLiteRepository(conn), get, store), store, &hits
}

func jpegHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tapnow/sd-assets/asset-001.jpg" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write([]byte("JPEGDATA"))
}

const charKey = "tapnow/sd-assets/asset-001.jpg"

func TestEnsureDownloadsCharacterImageOnce(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u + "/" })
	handled, err := c.Ensure(context.Background(), charKey)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if string(store.objs[charKey]) != "JPEGDATA" || store.types[charKey] != "image/jpeg" {
		t.Fatalf("缓存内容不对: %q %q", store.objs[charKey], store.types[charKey])
	}
	// 已缓存：不再下载（断网也能显示）。
	handled, err = c.Ensure(context.Background(), charKey)
	if err != nil || handled {
		t.Fatalf("第二次 handled=%v err=%v", handled, err)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("下载次数 = %d, want 1", *hits)
	}
}

func TestEnsureIgnoresNonCharacterKeys(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	handled, err := c.Ensure(context.Background(), "u1/2026/10/mine.png")
	if err != nil || handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 || atomic.LoadInt32(hits) != 0 {
		t.Fatal("非角色图不应下载")
	}
}

func TestEnsureBaseNotConfigured(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(string) string { return "  " })
	handled, err := c.Ensure(context.Background(), charKey)
	if !handled || !errors.Is(err, ErrImageBaseNotConfigured) {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if len(store.objs) != 0 || atomic.LoadInt32(hits) != 0 {
		t.Fatal("未配置时不应下载或落盘")
	}
}

func TestEnsureRemoteErrorNotCached(t *testing.T) {
	c, store, _ := newCacheFixture(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		func(u string) string { return u })
	if _, err := c.Ensure(context.Background(), charKey); err == nil {
		t.Fatal("远端 404 应返回错误")
	}
	if len(store.objs) != 0 {
		t.Fatal("失败不应留下缓存")
	}
}

func TestEnsureRejectsOversize(t *testing.T) {
	old := maxImageBytes
	maxImageBytes = 4
	t.Cleanup(func() { maxImageBytes = old })
	c, store, _ := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	if _, err := c.Ensure(context.Background(), charKey); err == nil {
		t.Fatal("超过上限应返回错误")
	}
	if len(store.objs) != 0 {
		t.Fatal("超限不应留下缓存")
	}
}

// 网关可能回 application/octet-stream：按扩展名兜底，P1b 的 /media 才能给出正确 Content-Type。
func TestEnsureContentTypeFallsBackToExtension(t *testing.T) {
	c, store, _ := newCacheFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("PNGDATA"))
	}, func(u string) string { return u })
	key := "tapnow/sd-assets/asset-003.png"
	if _, err := c.Ensure(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if store.types[key] != "image/png" {
		t.Fatalf("content type = %q, want image/png", store.types[key])
	}
}

func TestMiddlewareCachesBeforeServing(t *testing.T) {
	c, store, hits := newCacheFixture(t, jpegHandler, func(u string) string { return u })
	var sawCached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCached = store.Exists(r.Context(), charKey)
		w.WriteHeader(http.StatusOK)
	})
	h := c.Middleware(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/"+charKey+"?token=t", nil))
	if !sawCached || rec.Code != http.StatusOK {
		t.Fatalf("原 handler 执行时应已缓存: sawCached=%v code=%d", sawCached, rec.Code)
	}

	// 非 GET/HEAD 不触发下载，直接交给原 handler。
	delete(store.objs, charKey)
	before := atomic.LoadInt32(hits)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/media/"+charKey, nil))
	if atomic.LoadInt32(hits) != before {
		t.Fatal("POST 不应触发下载")
	}
}
```

- [ ] **Step 8: 运行确认失败**

Run: `cd server && go test ./internal/aicharacters/ 2>&1 | head`
Expected: 编译失败 `undefined: NewImageCache`。

- [ ] **Step 9: 实现形象图缓存**

`server/internal/aicharacters/image_cache.go`：

```go
package aicharacters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// SettingImageBaseURL 存角色形象图的在线地址前缀（spec F7「图片地址写进配置」）。
// 图片地址 = 前缀 + "/" + object_key（object_key 即 image_url 去掉 media:// 的部分，
// 如 tapnow/sd-assets/asset-xxx.jpg）。空串 = 未配置。
const SettingImageBaseURL = "ai_character_image_base_url"

// ErrImageBaseNotConfigured：要用到形象图但地址前缀没配。
var ErrImageBaseNotConfigured = errors.New("AI 角色形象图地址未配置")

// maxImageBytes 单张形象图下载上限。包级变量只为测试能调小。
var maxImageBytes int64 = 20 << 20

// ObjectStore 是本地媒体存储里缓存要用的两个动作。Task 8 用 P1b 的 assets.Storage 适配。
type ObjectStore interface {
	Exists(ctx context.Context, key string) bool
	Put(ctx context.Context, key, contentType string, body []byte) error
}

// ImageCache 把角色形象图按需下载进本地媒体目录（spec F7「在线加载并按需缓存」）。
// 只写对象、不写 assets 行：公共图不进用户配额、不进孤儿回收（见包注释）。
// 同一张图被并发请求时可能下载两次，Put 覆盖写同一个 key，结果一致，不做去重。
type ImageCache struct {
	repo  Repository
	get   func(key string) (string, error)
	store ObjectStore
	http  *http.Client
}

func NewImageCache(repo Repository, get func(string) (string, error), store ObjectStore) *ImageCache {
	return &ImageCache{repo: repo, get: get, store: store, http: &http.Client{Timeout: 60 * time.Second}}
}

// Ensure 保证角色形象图在本地：本地已有或 key 不是角色图 → (false, nil)，调用方按原逻辑处理；
// 是角色图且本地没有 → 下载落盘，返回 (true, err)。
func (c *ImageCache) Ensure(ctx context.Context, key string) (bool, error) {
	if key == "" || c.store.Exists(ctx, key) {
		return false, nil
	}
	ids, err := c.repo.PublicAssetIDs([]string{key})
	if err != nil {
		return false, err
	}
	if ids[key] == "" {
		return false, nil
	}
	base, err := c.get(SettingImageBaseURL)
	if err != nil {
		return true, err
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return true, ErrImageBaseNotConfigured
	}
	body, contentType, err := c.download(ctx, base+"/"+escapeKeyPath(key))
	if err != nil {
		return true, err
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(key))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return true, c.store.Put(ctx, key, contentType, body)
}

// escapeKeyPath 逐段转义 object key，保留 "/" 分隔。
func escapeKeyPath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// download 拉全量字节（有上限）；Content-Type 只认 image/*，其余返回空串由调用方按扩展名兜底。
func (c *ImageCache) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载角色形象图失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载角色形象图失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("下载角色形象图失败: %w", err)
	}
	if int64(len(body)) > maxImageBytes {
		return nil, "", fmt.Errorf("角色形象图超过 %d 字节上限", maxImageBytes)
	}
	contentType := ""
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && strings.HasPrefix(mt, "image/") {
		contentType = mt
	}
	return body, contentType, nil
}

// Middleware 包在 P1b 的 GET/HEAD /media/* 外面：请求的是角色形象图且本地还没有时，
// 先下载缓存再交给原 handler。下载失败只记日志，原 handler 照常返回 404。
func (c *ImageCache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if key, ok := strings.CutPrefix(r.URL.Path, "/media/"); ok && key != "" {
				if handled, err := c.Ensure(r.Context(), key); handled && err != nil {
					log.Printf("aicharacters: 缓存形象图 %s 失败: %v", key, err)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 10: 运行缓存测试确认通过**

Run: `cd server && go test ./internal/aicharacters/ -v`
Expected: 11 个测试全部 PASS。

- [ ] **Step 11: 复制 handler 测试并改造（先失败）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/aicharacter_handlers_test.go server/internal/http/aicharacter_handlers_test.go
perl -pi -e 's/NewAICharacterHandler\(repo, NewMediaSigner\(&stubStorage\{\}\)\)/NewAICharacterHandler(repo, aiCharFakeSigner{}, aiCharSettings(""))/g' server/internal/http/aicharacter_handlers_test.go
grep -c 'aiCharFakeSigner{}' server/internal/http/aicharacter_handlers_test.go
```

Expected: 输出 `2`。在该文件末尾追加：

```go
// aiCharFakeSigner 复刻 MediaSigner 对 media:// 的签名语义。
type aiCharFakeSigner struct{}

func (aiCharFakeSigner) Sign(s string) string {
	return "https://signed/" + strings.TrimPrefix(s, "media://")
}

func (aiCharFakeSigner) SignThumb(s string, w int) string {
	return fmt.Sprintf("https://signed/%s?w=%d", strings.TrimPrefix(s, "media://"), w)
}

func aiCharSettings(base string) func(string) (string, error) {
	return func(k string) (string, error) {
		if k == aicharacters.SettingImageBaseURL {
			return base, nil
		}
		return "", nil
	}
}

// imageConfigured 让前端在形象图地址没配时给出提示，而不是一屏裂图。
func TestAICharacterListReportsImageConfigured(t *testing.T) {
	repo := &fakeAICharactersRepo{items: manyCharacters(1)}
	for base, want := range map[string]bool{"": false, "  ": false, "https://img.example.com": true} {
		h := NewAICharacterHandler(repo, aiCharFakeSigner{}, aiCharSettings(base))
		rec := httptest.NewRecorder()
		h.List(rec, httptest.NewRequest("GET", "/canvas/ai-characters", nil))
		var body struct {
			ImageConfigured bool `json:"imageConfigured"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.ImageConfigured != want {
			t.Fatalf("base=%q imageConfigured=%v want %v", base, body.ImageConfigured, want)
		}
	}
}
```

Run: `cd server && go test ./internal/http/ -run AICharacter 2>&1 | head`
Expected: 编译失败 `undefined: NewAICharacterHandler`。

- [ ] **Step 12: 写 handler**

`server/internal/http/aicharacter_handlers.go`（参考仓文件的改动：签名器窄接口、settings 读取函数、响应加 `imageConfigured`）：

```go
package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
)

// aiCharacterSigner 是 *MediaSigner 在这里用到的两个方法（P1b 提供实现）。
type aiCharacterSigner interface {
	Sign(string) string
	SignThumb(string, int) string
}

// AICharacterHandler 暴露自由画布的「AI 角色库」列表。只读——这批角色是平台
// 预置数据（内置种子导入），没有任何写入口。形象图经本地 /media 提供，本地没有时
// 由 aicharacters.ImageCache 按需从在线地址下载（spec F7）。
type AICharacterHandler struct {
	repo     aicharacters.Repository
	signer   aiCharacterSigner
	settings func(key string) (string, error)
}

func NewAICharacterHandler(repo aicharacters.Repository, signer aiCharacterSigner, settingsGet func(string) (string, error)) *AICharacterHandler {
	return &AICharacterHandler{repo: repo, signer: signer, settings: settingsGet}
}

// aiCharacterListLimit 是单页上限，也是前端滚动加载的批大小。
const aiCharacterListLimit = 50

type aiCharacterDTO struct {
	AssetID     string `json:"assetId"`
	Age         int    `json:"age"`
	Gender      string `json:"gender"`
	Occupation  string `json:"occupation"`
	Description string `json:"description"`
	AgeGroup    string `json:"ageGroup"`
	// ImageURL 是签名后的本地 URL——详情弹框展示、「应用到画布」的节点内容都用它
	//（节点持久化时会被 canonical 化回 media:// 形式）。ThumbURL 给网格卡片用；
	// 桌面端不生成缩略图（spec S8），与 ImageURL 相同。
	ImageURL string `json:"imageUrl"`
	ThumbURL string `json:"thumbUrl"`
}

// List: GET /canvas/ai-characters?search=&gender=&ageGroup=&offset=
// 无效的过滤值当作不过滤——过滤枚举将来若扩充，老前端传新值也不至于 400。
func (h *AICharacterHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := aicharacters.ListParams{
		Search: q.Get("search"),
		Offset: 0,
		// 多取一条探测还有没有下一页，响应里只回 limit 条。
		Limit: aiCharacterListLimit + 1,
	}
	if g := q.Get("gender"); g == "男" || g == "女" {
		p.Gender = g
	}
	if a := q.Get("ageGroup"); a == "青年" || a == "中年" || a == "老年" {
		p.AgeGroup = a
	}
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		p.Offset = off
	}

	list, err := h.repo.List(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasMore := len(list) > aiCharacterListLimit
	if hasMore {
		list = list[:aiCharacterListLimit]
	}
	items := make([]aiCharacterDTO, 0, len(list))
	for _, c := range list {
		items = append(items, aiCharacterDTO{
			AssetID:     c.AssetID,
			Age:         c.Age,
			Gender:      c.Gender,
			Occupation:  c.Occupation,
			Description: c.Description,
			AgeGroup:    c.AgeGroup,
			ImageURL:    h.signer.Sign(c.ImageURL),
			ThumbURL:    h.signer.SignThumb(c.ImageURL, 400),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"hasMore": hasMore,
		// 形象图地址没配时图片全部加载不出来，前端据此提示（P3a）。
		"imageConfigured": h.imageConfigured(),
	})
}

func (h *AICharacterHandler) imageConfigured() bool {
	if h.settings == nil {
		return false
	}
	base, err := h.settings(aicharacters.SettingImageBaseURL)
	return err == nil && strings.TrimSpace(base) != ""
}
```

- [ ] **Step 13: 运行测试确认通过**

Run: `cd server && go vet ./internal/aicharacters/ ./internal/http/ && go test ./internal/aicharacters/ ./internal/http/ -run 'AICharacter|Ensure|Middleware|List|Get|Public' -v 2>&1 | tail -25 && go test ./...`
Expected: 全部 PASS（含参考仓的 `TestAICharacterListPagination`、`TestAICharacterListFilters`）。

- [ ] **Step 14: 提交**

```bash
git add server/internal/db/migrations/0011_p1d_ai_characters.sql server/internal/aicharacters \
  server/internal/http/aicharacter_handlers.go server/internal/http/aicharacter_handlers_test.go \
  server/internal/http/helpers.go server/internal/http/context.go server/internal/http/router.go
git commit -m "feat(server): AI 角色库表、仓库、形象图按需缓存与列表接口"
```

---

### Task 3: Seedance 合规校验改走 newapi

**Files:**
- Create: `server/internal/db/migrations/0010_p1d_seedance.sql`
- Create（重写）: `server/internal/seedance/client.go`；Test（移植）: `server/internal/seedance/client_test.go`
- Create（移植）: `server/internal/library/seedance_service.go`、`server/internal/library/seedance_assets_repo.go`
- Create: `server/internal/library/seedance_groups_repo.go`
- Test: `server/internal/library/seedance_service_test.go`（移植）、`server/internal/library/seedance_repo_test.go`（新写）
- Create（移植）: `server/internal/http/seedance_handlers.go`；Test: `server/internal/http/seedance_handlers_test.go`
- Create（缺失时，原样复制）: `server/internal/http/helpers.go`、`context.go`；Modify: `server/internal/http/router.go`（同 Task 1 Step 1）

**Interfaces:**
- Consumes: `dbtest.Open(t)`（P1a）；`library_items` 表（P1b 0002；本任务测试里用 `CREATE TABLE IF NOT EXISTS` 兜底）
- Produces:
  - `seedance.SettingComplianceConfig = "seedance_compliance_config"`、`seedance.Paths{CreateAssetGroup, CreateAsset, GetAsset string}`、`(Paths).Complete() bool`、`seedance.ComplianceConfig{Paths; Version int}`、`(ComplianceConfig).EffectiveVersion() int`、`seedance.LoadComplianceConfig(get func(string) (string, error)) (ComplianceConfig, error)`、`seedance.ErrNotConfigured`
  - `seedance.NewClient(baseURL BaseURLProvider, paths PathsProvider) *Client`（`BaseURLProvider func() string`、`PathsProvider func() Paths`），方法签名与参考仓相同：`CreateAssetGroup(ctx, sdKey, name, description string) (string, error)`、`CreateAsset(ctx, sdKey, groupID, url, name, assetType string) (string, error)`、`GetAsset(ctx, sdKey, id string) (string, error)`；`sdKey` 现在是 newapi 的 API Key
  - `library.NewSeedanceService(repo SeedanceAssetsRepository, groups, client, settings, signer) *SeedanceService`（不变）、`(*SeedanceService).Configured() bool`（新增）、`SetPublicAssets`、`Submit`、`Status`、`Resolve`、`RunWorker`、`ReconcileAll`；`library.ErrSeedanceDisabled`、`library.ErrLocalMediaNoPublicURL`（新增）、`library.ErrRealPersonNotReady`
  - `library.SQLiteSeedanceAssetsRepository`、`library.NewSQLiteSeedanceAssetsRepository(*sql.DB)`；`library.SQLiteSeedanceGroupStore`、`library.NewSQLiteSeedanceGroupStore(*sql.DB)`、`library.ErrSeedanceGroupUserNotFound`
  - `http.NewSeedanceHandler(svc *library.SeedanceService, storage seedanceKeyExtractor, db *sql.DB) *SeedanceHandler`，方法 `Verify`、`Status`、`Config`；`seedanceKeyExtractor` = `{ExtractKey(string) (string, bool)}`（`assets.Storage` 满足）

**配置形状（spec X5，本计划的决定）：** settings `seedance_compliance_config` = `{"createAssetGroupPath":"…","createAssetPath":"…","getAssetPath":"…","version":1}`。请求发往 `strings.TrimRight(base_domain, "/") + path`，头 `Authorization: Bearer <upstream_api_key>`。`base_domain`、`upstream_api_key`、三个路径任一为空 = “合规接口未配置”。`version` 取代参考仓的 `drama_seedance_version`（换账号后 +1 触发重建素材组和素材），缺省 1。

- [ ] **Step 1: http 基础件（与 P1b 字节相同，已存在就跳过）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
test -f server/internal/http/helpers.go || cp $REF/http/helpers.go server/internal/http/helpers.go
test -f server/internal/http/context.go || cp $REF/http/context.go server/internal/http/context.go
grep -q '^func writeJSON' server/internal/http/router.go && \
  perl -0pi -e 's/\nfunc writeJSON\(w nethttp\.ResponseWriter, status int, v any\) \{.*?\n\}\n//s; s/\t"encoding\/json"\n//' server/internal/http/router.go
cd server && go build ./internal/http/ && cd ..
```

Expected: `go build` 无输出。

- [ ] **Step 2: 写迁移**

`server/internal/db/migrations/0010_p1d_seedance.sql`：

```sql
-- P1d：画布素材的 Seedance 2.0 合规校验状态（参考仓迁移 0075 建表、0084 加 asset_type），
-- 按 object_key 归属；外加用户级素材组 id 与建组版本（0075、0083）。
CREATE TABLE seedance_assets (
  -- 对象 key，素材的唯一身份；画布 assets 与素材库 library_items 共用同一个 key。
  object_key TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  -- Seedance 素材库 asset-id；生成时把素材引用替换成 asset://<id>。
  asset_id TEXT NULL,
  -- 校验时的合规配置版本号（seedance_compliance_config.version），版本轮换触发重新校验。
  version INTEGER NULL,
  -- pending / active / failed / failed_permanent
  status TEXT NOT NULL,
  error TEXT NULL,
  -- 首次失败时间，驱动 1 小时重试兜底窗口。
  first_failed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  -- 上游 CreateAsset 的 AssetType：Image / Video / Audio；worker 重试时据此重放。
  asset_type TEXT NOT NULL DEFAULT 'Image'
);
CREATE INDEX idx_seedance_assets_status ON seedance_assets (status);
CREATE INDEX idx_seedance_assets_user ON seedance_assets (user_id);

CREATE TRIGGER trg_seedance_assets_updated_at AFTER UPDATE ON seedance_assets
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE seedance_assets SET updated_at = CURRENT_TIMESTAMP WHERE object_key = NEW.object_key;
END;

-- 用户在 Seedance 素材库的素材组 id（首次校验时惰性创建）与建组时的版本号（与当前版本不符即重建）。
ALTER TABLE users ADD COLUMN seedance_group_id TEXT NULL;
ALTER TABLE users ADD COLUMN seedance_group_version INTEGER NOT NULL DEFAULT 0;
```

- [ ] **Step 3: 复制并改造客户端测试（先失败）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/seedance
cp $REF/seedance/client_test.go server/internal/seedance/client_test.go
cd server/internal/seedance
perl -pi -e 's/NewClient\(func\(\) string \{ return srv\.URL \}\)/NewClient(func() string { return srv.URL }, testPaths)/g; s/NewClient\(func\(\) string \{ return current \}\)/NewClient(func() string { return current }, testPaths)/g' client_test.go
perl -pi -e 's/gotKey = r\.Header\.Get\("sd-key"\)/gotKey = r.Header.Get("Authorization")/; s/if gotKey != "sk-test" \{/if gotKey != "Bearer sk-test" {/; s/t\.Errorf\("sd-key = %q", gotKey\)/t.Errorf("Authorization = %q", gotKey)/' client_test.go
# 删除默认域名回退测试（桌面端没有默认域名）
perl -0pi -e 's/\n\/\/ provider 返回空串 → 回退默认生产域名（不发到空 host）。\nfunc TestBaseURLProviderEmptyFallsBackToDefault\(t \*testing\.T\) \{.*?\n\}\n//s' client_test.go
grep -n 'sd-key\|DefaultBaseURL\|resolveBaseURL' client_test.go
cd ../../..
```

Expected: 最后的 grep 无输出。在 `client_test.go` 末尾（`contains` 函数之前）追加：

```go
// testPaths 是参考仓直连火山方舟时的三个路径，用来让移植来的用例保持原断言。
func testPaths() Paths {
	return Paths{CreateAssetGroup: "/asset/CreateAssetGroup", CreateAsset: "/asset/CreateAsset", GetAsset: "/asset/GetAsset"}
}

// 网关地址或路径为空：不发任何请求，直接报“合规接口未配置”。
func TestNotConfiguredWhenBaseOrPathEmpty(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	cases := []*Client{
		NewClient(func() string { return "" }, testPaths),
		NewClient(nil, testPaths),
		NewClient(func() string { return srv.URL }, func() Paths { return Paths{} }),
		NewClient(func() string { return srv.URL }, nil),
	}
	for i, c := range cases {
		if _, err := c.GetAsset(context.Background(), "sk", "id"); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("case %d: err = %v, want ErrNotConfigured", i, err)
		}
	}
	if hits != 0 {
		t.Fatalf("未配置时不应发请求，hits=%d", hits)
	}
}

// 路径与地址每次请求现读，并兼容末尾斜杠、缺前导斜杠。
func TestPathsAndBaseReadPerRequest(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Write([]byte(`{"Status":"Active"}`))
	}))
	defer srv.Close()
	p := Paths{CreateAssetGroup: "/g", CreateAsset: "/a", GetAsset: "newapi/seedance/get"}
	c := NewClient(func() string { return srv.URL + "/" }, func() Paths { return p })
	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatal(err)
	}
	p.GetAsset = "/v2/get"
	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatal(err)
	}
	if len(gotPaths) != 2 || gotPaths[0] != "/newapi/seedance/get" || gotPaths[1] != "/v2/get" {
		t.Fatalf("paths = %v", gotPaths)
	}
}

func TestLoadComplianceConfig(t *testing.T) {
	get := func(v string) func(string) (string, error) {
		return func(k string) (string, error) {
			if k == SettingComplianceConfig {
				return v, nil
			}
			return "", nil
		}
	}
	c, err := LoadComplianceConfig(get(""))
	if err != nil || c.Complete() || c.EffectiveVersion() != 1 {
		t.Fatalf("空配置: %+v %v", c, err)
	}
	c, err = LoadComplianceConfig(get(`{"createAssetGroupPath":"/g","createAssetPath":"/a","getAssetPath":"/s","version":3}`))
	if err != nil || !c.Complete() || c.EffectiveVersion() != 3 || c.CreateAsset != "/a" {
		t.Fatalf("完整配置: %+v %v", c, err)
	}
	c, err = LoadComplianceConfig(get(`{"createAssetGroupPath":"/g","createAssetPath":" ","getAssetPath":"/s"}`))
	if err != nil || c.Complete() {
		t.Fatalf("空白路径应视为未配置: %+v %v", c, err)
	}
	if _, err := LoadComplianceConfig(get(`{broken`)); err == nil {
		t.Fatal("损坏的 JSON 应报错")
	}
}
```

Run: `cd server && go test ./internal/seedance/ 2>&1 | head`
Expected: 编译失败 `undefined: NewClient` / `undefined: Paths`。

- [ ] **Step 4: 写新客户端**

`server/internal/seedance/client.go`：

```go
// Package seedance 是 Seedance 2.0「素材库」合规校验的薄 HTTP 客户端，只覆盖三个端点：
// 建组、建素材、查素材状态。
//
// 桌面端经 newapi 网关转发（spec X5）：地址 = settings.base_domain + 合规接口路径
// （settings.seedance_compliance_config），鉴权 = Bearer <upstream_api_key>。请求与响应的
// JSON 形状保持参考仓直连火山方舟素材库时的样子，newapi 侧按这个形状补接口（spec P7）。
//
// key 不存在 Client 上，而是每次调用作为参数传入；地址与路径每次请求现读——都存在
// settings 表里、设置页可随时改，避免进程内缓存过期值。
package seedance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SettingComplianceConfig 是合规接口配置在 settings 表里的 key（spec M4：每项配置存一个 JSON 值）。
const SettingComplianceConfig = "seedance_compliance_config"

// ErrNotConfigured：网关地址为空，或本次要调的接口路径为空。
var ErrNotConfigured = errors.New("合规接口未配置")

// Paths 是 newapi 上三个合规接口的路径，由用户在设置页填写。
type Paths struct {
	CreateAssetGroup string `json:"createAssetGroupPath"`
	CreateAsset      string `json:"createAssetPath"`
	GetAsset         string `json:"getAssetPath"`
}

// Complete 三个路径都填了才算配置完整。
func (p Paths) Complete() bool {
	return strings.TrimSpace(p.CreateAssetGroup) != "" &&
		strings.TrimSpace(p.CreateAsset) != "" &&
		strings.TrimSpace(p.GetAsset) != ""
}

// ComplianceConfig 是 settings.seedance_compliance_config 的整份内容。
type ComplianceConfig struct {
	Paths
	// Version 换账号或换 key 后 +1：素材组与已通过的素材都会在新版本下重建
	// （即参考仓的 settings.drama_seedance_version，桌面端并进这份配置）。<=0 视为 1。
	Version int `json:"version"`
}

// EffectiveVersion 返回生效的版本号（未设置为 1）。
func (c ComplianceConfig) EffectiveVersion() int {
	if c.Version > 0 {
		return c.Version
	}
	return 1
}

// LoadComplianceConfig 现读配置；未设置返回零值（= 未配置），JSON 损坏返回错误。
func LoadComplianceConfig(get func(key string) (string, error)) (ComplianceConfig, error) {
	raw, err := get(SettingComplianceConfig)
	if err != nil {
		return ComplianceConfig{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return ComplianceConfig{}, nil
	}
	var c ComplianceConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return ComplianceConfig{}, fmt.Errorf("合规接口配置损坏: %w", err)
	}
	return c, nil
}

// BaseURLProvider 现读网关地址（settings.base_domain）。
type BaseURLProvider func() string

// PathsProvider 现读三个接口路径。
type PathsProvider func() Paths

type Client struct {
	baseURL BaseURLProvider
	paths   PathsProvider
	http    *http.Client
}

// NewClient 用 30s 超时的 http.Client 构造。任一 provider 为 nil 或返回空值时，调用返回 ErrNotConfigured。
func NewClient(baseURL BaseURLProvider, paths PathsProvider) *Client {
	return &Client{baseURL: baseURL, paths: paths, http: &http.Client{Timeout: 30 * time.Second}}
}

// endpoint 拼出本次调用的完整地址。
func (c *Client) endpoint(pick func(Paths) string) (string, error) {
	base := ""
	if c.baseURL != nil {
		base = strings.TrimRight(strings.TrimSpace(c.baseURL()), "/")
	}
	var p Paths
	if c.paths != nil {
		p = c.paths()
	}
	path := strings.TrimSpace(pick(p))
	if base == "" || path == "" {
		return "", ErrNotConfigured
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path, nil
}

// post 发一个带 Bearer key 的 JSON POST，把响应 JSON 解到 out。
// 非 2xx 一律当错误，并把响应体塞进 error 方便排障。
func (c *Client) post(ctx context.Context, apiKey, name string, pick func(Paths) string, body, out any) error {
	url, err := c.endpoint(pick)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if apiErr := errorFromResponse(respBody); apiErr != nil {
			apiErr.Detail = fmt.Sprintf("%s: status %d: %s", name, resp.StatusCode, apiErr.Detail)
			return apiErr
		}
		return fmt.Errorf("seedance %s: status %d: %s", name, resp.StatusCode, string(respBody))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("seedance %s: decode response: %w (body=%s)", name, err, string(respBody))
	}
	return nil
}

// APIError 是 seedance 侧返回的业务错误（HTTP 可能是 200，Code 藏在 data 里，
// 也可能是非 2xx 状态码）。单独成类型是为了让上层能按 Code 分类：
// InvalidParameter.* 这类参数错误重试必然同样失败，调用方据此停止重试。
type APIError struct {
	Code   string // 如 "InvalidParameter.AspectRatioTooSmall"；可能为空（未能解析出 Code）
	Detail string // 原始响应体或 Message，保留给日志/前端展示
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return "seedance " + e.Code + ": " + e.Detail
	}
	return "seedance: " + e.Detail
}

// errorFromResponse 尝试把响应体解析成 APIError（能取到 Code 才算数）。
func errorFromResponse(raw []byte) *APIError {
	code, ok := fieldFromResponse(raw, "Code")
	if !ok {
		return nil
	}
	detail, _ := fieldFromResponse(raw, "Message")
	if detail == "" {
		detail = string(raw)
	}
	return &APIError{Code: code, Detail: detail}
}

// fieldFromResponse 从响应里取一个字符串字段，兼容直返 {"Id":...} 与被包一层
// {"data":{"Id":...}} / {"Result":{...}} 等常见网关形态。取不到返回 false。
func fieldFromResponse(raw json.RawMessage, field string) (string, bool) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	if v, ok := m[field].(string); ok && v != "" {
		return v, true
	}
	for _, wrap := range []string{"data", "Data", "result", "Result"} {
		if sub, ok := m[wrap].(map[string]any); ok {
			if v, ok := sub[field].(string); ok && v != "" {
				return v, true
			}
		}
	}
	return "", false
}

// CreateAssetGroup 建一个素材组，返回组 ID。
func (c *Client) CreateAssetGroup(ctx context.Context, sdKey, name, description string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "CreateAssetGroup", func(p Paths) string { return p.CreateAssetGroup }, map[string]any{
		"Name":        name,
		"Description": description,
	}, &raw); err != nil {
		return "", err
	}
	if id, ok := fieldFromResponse(raw, "Id"); ok {
		return id, nil
	}
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance CreateAssetGroup: 响应无 Id: %s", string(raw))
}

// AssetType 取值。上游三类素材共用 CreateAsset/GetAsset，差别只在这一个字段。
const (
	AssetTypeImage = "Image"
	AssetTypeVideo = "Video"
	AssetTypeAudio = "Audio"
)

// CreateAsset 往组里加一个素材（异步），返回素材 ID。assetType 必传。
func (c *Client) CreateAsset(ctx context.Context, sdKey, groupID, url, name, assetType string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "CreateAsset", func(p Paths) string { return p.CreateAsset }, map[string]any{
		"GroupId":   groupID,
		"URL":       url,
		"Name":      name,
		"AssetType": assetType,
	}, &raw); err != nil {
		return "", err
	}
	if id, ok := fieldFromResponse(raw, "Id"); ok {
		return id, nil
	}
	// 解析不出 Id：优先解析业务错误 Code（HTTP 200 但 data 里带 Code 的形态），
	// 让上层能按 Code 分类停止重试；解析不出 Code 再退回带原始响应体的错误。
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance CreateAsset: 响应无 Id: %s", string(raw))
}

// GetAsset 查素材当前状态。Status ∈ Active / Failed / (处理中的其他值)，只有 Active 可用。
func (c *Client) GetAsset(ctx context.Context, sdKey, id string) (string, error) {
	var raw json.RawMessage
	if err := c.post(ctx, sdKey, "GetAsset", func(p Paths) string { return p.GetAsset }, map[string]any{"Id": id}, &raw); err != nil {
		return "", err
	}
	if st, ok := fieldFromResponse(raw, "Status"); ok {
		return st, nil
	}
	if apiErr := errorFromResponse(raw); apiErr != nil {
		return "", apiErr
	}
	return "", fmt.Errorf("seedance GetAsset: 响应无 Status: %s", string(raw))
}
```

Run: `cd server && go test ./internal/seedance/ -v 2>&1 | tail -20`
Expected: 参考仓移植来的 8 个用例（去掉默认域名那条后为 7 个）+ 新增 3 个全部 PASS。

- [ ] **Step 5: 写仓库与素材组存储的失败测试**

`server/internal/library/seedance_repo_test.go`：

```go
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
```

Run: `cd server && go test ./internal/library/ 2>&1 | head`
Expected: 编译失败 `undefined: NewSQLiteSeedanceAssetsRepository`。

- [ ] **Step 6: 移植仓库、新写素材组存储**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/library
cp $REF/library/seedance_assets_repo.go server/internal/library/seedance_assets_repo.go
cd server/internal/library
perl -pi -e 's/NewMySQLSeedanceAssetsRepository/NewSQLiteSeedanceAssetsRepository/g; s/MySQLSeedanceAssetsRepository/SQLiteSeedanceAssetsRepository/g; s/COALESCE\(first_failed_at, NOW\(\)\)/COALESCE(first_failed_at, CURRENT_TIMESTAMP)/' seedance_assets_repo.go
cd ../../..
```

然后在 `seedance_assets_repo.go` 中把 `Upsert` 整个函数替换为：

```go
func (r *SQLiteSeedanceAssetsRepository) Upsert(a SeedanceAsset) error {
	_, err := r.db.Exec(`
        INSERT INTO seedance_assets (object_key, user_id, asset_id, version, asset_type, status, error, first_failed_at)
        VALUES (?, ?, ?, ?, ?, ?, NULL, NULL)
        ON CONFLICT (object_key) DO UPDATE SET
            asset_id = excluded.asset_id, version = excluded.version, asset_type = excluded.asset_type,
            status = excluded.status, error = NULL, first_failed_at = NULL`,
		a.ObjectKey, a.UserID, strPtrArg(a.AssetID), intPtrArg(a.Version), a.AssetType, a.Status)
	return err
}

// strPtrArg / intPtrArg 把可空字段显式转成驱动参数（nil → SQL NULL），不依赖驱动对指针的处理。
func strPtrArg(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func intPtrArg(p *int) any {
	if p == nil {
		return nil
	}
	return int64(*p)
}
```

新建 `server/internal/library/seedance_groups_repo.go`：

```go
package library

import (
	"database/sql"
	"errors"
)

// ErrSeedanceGroupUserNotFound：users 表里没有这个用户。
var ErrSeedanceGroupUserNotFound = errors.New("user not found")

// SQLiteSeedanceGroupStore 读写 users 表上的 Seedance 素材组 id 与建组版本号。
// 参考仓这两个方法在 users.Repository 上（users/repository.go:398-413）；P1b 新写的
// users 包只含存储用量方法，所以放在合规校验自己的包里。
type SQLiteSeedanceGroupStore struct{ db *sql.DB }

func NewSQLiteSeedanceGroupStore(db *sql.DB) *SQLiteSeedanceGroupStore {
	return &SQLiteSeedanceGroupStore{db: db}
}

func (s *SQLiteSeedanceGroupStore) GetSeedanceGroup(userID int64) (string, int, error) {
	var gid sql.NullString
	var ver sql.NullInt64
	err := s.db.QueryRow(`SELECT seedance_group_id, seedance_group_version FROM users WHERE id = ?`, userID).Scan(&gid, &ver)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSeedanceGroupUserNotFound
	}
	if err != nil {
		return "", 0, err
	}
	return gid.String, int(ver.Int64), nil
}

func (s *SQLiteSeedanceGroupStore) SetSeedanceGroup(userID int64, groupID string, version int) error {
	_, err := s.db.Exec(`UPDATE users SET seedance_group_id = ?, seedance_group_version = ? WHERE id = ?`, groupID, version, userID)
	return err
}
```

Run: `cd server && gofmt -l internal/library/ && go test ./internal/library/ -run 'SeedanceAssetsRepo|SeedanceGroupStore' -v 2>&1 | tail -15`
Expected: `gofmt -l` 无输出；6 个仓库测试 PASS（`SeedanceAsset` 等类型都在 repo 文件里，此时 `seedance_service.go` 还不存在也能编译）。

- [ ] **Step 7: 复制并改造服务测试（先失败）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/library/seedance_service_test.go server/internal/library/seedance_service_test.go
cd server/internal/library
perl -pi -e 's/fakeLibrarySettings\{settingSeedanceKey: "sk-x", settingSeedanceVersion: "2"\}/enabledSettings(2)/g; s/fakeLibrarySettings\{settingSeedanceKey: "sk-x"\}/enabledSettings(0)/g' seedance_service_test.go
grep -n 'settingSeedance' seedance_service_test.go
perl -pi -e 's/^\t"strings"$/\t"strconv"\n\t"strings"/' seedance_service_test.go
cd ../../..
```

Expected: grep 无输出。在 `seedance_service_test.go` 末尾追加：

```go
// enabledSettings 是“合规接口已配置”的 settings：key、网关地址、三个路径齐全，version<=0 表示不写版本。
func enabledSettings(version int) fakeLibrarySettings {
	cfg := `{"createAssetGroupPath":"/g","createAssetPath":"/a","getAssetPath":"/s"`
	if version > 0 {
		cfg += `,"version":` + strconv.Itoa(version)
	}
	cfg += `}`
	return fakeLibrarySettings{
		settingUpstreamAPIKey:            "sk-x",
		settingBaseDomain:                "https://gw.example",
		seedance.SettingComplianceConfig: cfg,
	}
}

// spec S12 未定：本机素材签出来是回环地址，上游拉不到。必须就地拒绝，不发上游、不落行。
func TestSubmitRefusesLoopbackURL(t *testing.T) {
	for _, host := range []string{"127.0.0.1:5173", "localhost:9", "[::1]:80"} {
		repo := newFakeAssets()
		client := &fakeLibrarySeedanceClient{}
		svc := NewSeedanceService(repo, &fakeGroupStore{}, client, enabledSettings(0),
			signerStub(func(k string) string { return "http://" + host + "/media/" + strings.TrimPrefix(k, "media://") + "?token=t" }))
		_, err := svc.Submit(context.Background(), 7, "u7/a.png", seedance.AssetTypeImage)
		if !errors.Is(err, ErrLocalMediaNoPublicURL) {
			t.Fatalf("%s: err = %v, want ErrLocalMediaNoPublicURL", host, err)
		}
		if len(client.createAssetCalls) != 0 || len(client.createGroupCalls) != 0 || len(repo.upserts) != 0 {
			t.Fatalf("%s: 回环地址不应发往上游或落行", host)
		}
	}
}

func TestSubmitDisabledWhenAnyPieceMissing(t *testing.T) {
	full := enabledSettings(0)
	for _, drop := range []string{settingUpstreamAPIKey, settingBaseDomain, seedance.SettingComplianceConfig} {
		st := fakeLibrarySettings{}
		for k, v := range full {
			if k != drop {
				st[k] = v
			}
		}
		client := &fakeLibrarySeedanceClient{}
		svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, client, st, fakeLibrarySigner{})
		if _, err := svc.Submit(context.Background(), 7, "k", seedance.AssetTypeImage); !errors.Is(err, ErrSeedanceDisabled) {
			t.Fatalf("缺 %s: err = %v", drop, err)
		}
		if svc.Configured() {
			t.Fatalf("缺 %s 时不应视为已配置", drop)
		}
		if err := svc.ReconcileAll(context.Background()); err != nil || len(client.getAssetCalls) != 0 {
			t.Fatalf("缺 %s 时 worker 应 no-op: %v", drop, err)
		}
	}
	st := enabledSettings(0)
	st[seedance.SettingComplianceConfig] = `{"createAssetGroupPath":"/g","createAssetPath":"/a"}`
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}, st, fakeLibrarySigner{})
	if svc.Configured() {
		t.Fatal("少一个路径也算未配置")
	}
}

func TestConfiguredWhenComplete(t *testing.T) {
	svc := NewSeedanceService(newFakeAssets(), &fakeGroupStore{}, &fakeLibrarySeedanceClient{}, enabledSettings(0), fakeLibrarySigner{})
	if !svc.Configured() {
		t.Fatal("配置齐全应视为已配置")
	}
}

// 前端直接展示这条错误文案（spec X5「未配置时入口提示“合规接口未配置”」）。
func TestErrSeedanceDisabledMentionsUnconfigured(t *testing.T) {
	if !strings.Contains(ErrSeedanceDisabled.Error(), "合规接口未配置") {
		t.Fatalf("文案 = %q", ErrSeedanceDisabled.Error())
	}
}
```

Run: `cd server && go test ./internal/library/ 2>&1 | head`
Expected: 编译失败 `undefined: NewSeedanceService` 等。

- [ ] **Step 8: 移植服务并改配置来源**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/library/seedance_service.go server/internal/library/seedance_service.go
```

在 `server/internal/library/seedance_service.go` 中做以下替换：

(a) 把

```go
// 与 drama 共用同一对 settings key：sd-key 是平台层上游凭证，画布/项目管理共用同一上游账号。
const (
	settingSeedanceKey     = "drama_seedance_sd_key"
	settingSeedanceVersion = "drama_seedance_version"
)
```

替换为

```go
// 桌面端合规校验走 newapi（spec X5）：鉴权用全局 upstream_api_key，网关地址用 base_domain，
// 接口路径与版本号在 seedance_compliance_config 里（见 seedance.ComplianceConfig）。
const (
	settingUpstreamAPIKey = "upstream_api_key"
	settingBaseDomain     = "base_domain"
)
```

(b) 把整个 `config()` 函数（含其上方注释 `// config 现读 sd-key + version。…`）替换为

```go
// config 现读 API Key + 版本号。网关地址、Key、三个接口路径任一缺失 → 返回空 key，
// 调用方据此报 ErrSeedanceDisabled（Submit）或直接不动作（worker）。
func (s *SeedanceService) config() (sdKey string, version int, err error) {
	cfg, err := seedance.LoadComplianceConfig(s.settings.Get)
	if err != nil {
		return "", 0, err
	}
	base, err := s.settings.Get(settingBaseDomain)
	if err != nil {
		return "", 0, err
	}
	key, err := s.settings.Get(settingUpstreamAPIKey)
	if err != nil {
		return "", 0, err
	}
	if !cfg.Complete() || strings.TrimSpace(base) == "" || strings.TrimSpace(key) == "" {
		return "", cfg.EffectiveVersion(), nil
	}
	return key, cfg.EffectiveVersion(), nil
}

// Configured 供前端在入口处提示「合规接口未配置」（GET /seedance/config）。
func (s *SeedanceService) Configured() bool {
	key, _, err := s.config()
	return err == nil && key != ""
}
```

(c) 把

```go
// ErrSeedanceDisabled：管理端没配 sd-key，校验功能整体关闭。
var ErrSeedanceDisabled = errors.New("Seedance 未配置，请联系管理员")
```

替换为

```go
// ErrSeedanceDisabled：网关地址、API Key 或合规接口路径没配，校验功能整体关闭。
var ErrSeedanceDisabled = errors.New("合规接口未配置：请在设置中填写网关地址、API Key 和合规接口路径")

// ErrLocalMediaNoPublicURL：素材只能签出本机回环地址，上游拉不到（spec S12 待定）。
var ErrLocalMediaNoPublicURL = errors.New("本机素材暂无公网地址，暂不能提交合规校验")
```

(d) 把 `signedURL` 函数体替换为

```go
func (s *SeedanceService) signedURL(objectKey string) (string, error) {
	url := s.signer.Sign(mediaProtocol + objectKey)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", errors.New("无法生成素材直链，请联系管理员检查存储配置")
	}
	// 桌面端 MediaSigner 签出的是 http://127.0.0.1:<端口>/media/…，发给上游必然拉取失败，
	// 且会被上游晦涩的 URL 错误掩盖根因。S12 定下公网方案前一律就地拒绝。
	if isLoopbackURL(url) {
		return "", ErrLocalMediaNoPublicURL
	}
	return url, nil
}

// isLoopbackURL 判断 URL 的主机是不是本机回环地址。
func isLoopbackURL(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
```

(e) import 块加入 `"net"` 和 `neturl "net/url"`。

(f) 文件开头 `seedanceAssetName` 之前的注释不动；`NewSeedanceService` 的注释里若有“管理端”字样改为“设置页”。

Run: `cd server && go vet ./internal/library/ ./internal/seedance/ && go test ./internal/library/ ./internal/seedance/ -v 2>&1 | grep -E '^(=== RUN|--- FAIL|FAIL|ok|PASS)' | grep -v '=== RUN'`
Expected: 两个包 `ok`，无 FAIL（参考仓 33 个服务用例 + 新增 4 个 + 仓库 6 个）。

- [ ] **Step 9: 写 handler 的失败测试**

`server/internal/http/seedance_handlers_test.go`：

```go
package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

type seedanceFakeExtractor struct{}

func (seedanceFakeExtractor) ExtractKey(u string) (string, bool) {
	const p = "media://"
	if strings.HasPrefix(u, p) && len(u) > len(p) {
		return u[len(p):], true
	}
	return "", false
}

type seedanceFakeClient struct{ createAssetCalls int }

func (c *seedanceFakeClient) CreateAssetGroup(context.Context, string, string, string) (string, error) {
	return "g-1", nil
}
func (c *seedanceFakeClient) CreateAsset(context.Context, string, string, string, string, string) (string, error) {
	c.createAssetCalls++
	return "asset-new", nil
}
func (c *seedanceFakeClient) GetAsset(context.Context, string, string) (string, error) {
	return "Active", nil
}

type seedanceFakeSettings map[string]string

func (s seedanceFakeSettings) Get(k string) (string, error) { return s[k], nil }

type seedanceFakeSigner func(string) string

func (f seedanceFakeSigner) Sign(s string) string { return f(s) }

type seedanceFakePublic map[string]string

func (p seedanceFakePublic) PublicAssetIDs(keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if id, ok := p[k]; ok {
			out[k] = id
		}
	}
	return out, nil
}

func seedanceConfigured() seedanceFakeSettings {
	return seedanceFakeSettings{
		"upstream_api_key":               "sk-x",
		"base_domain":                    "https://gw.example",
		seedance.SettingComplianceConfig: `{"createAssetGroupPath":"/g","createAssetPath":"/a","getAssetPath":"/s"}`,
	}
}

func seedancePublicSigner(s string) string {
	return "https://pub.example/" + strings.TrimPrefix(s, "media://")
}

func newSeedanceTestHandler(t *testing.T, st seedanceFakeSettings, signer seedanceFakeSigner) (*SeedanceHandler, *library.SeedanceService, *sql.DB, *seedanceFakeClient) {
	t.Helper()
	conn := dbtest.Open(t)
	// library_items 由 P1b 的迁移 0002 建；本任务的工作区里可能还没有，归属查询需要这张表存在。
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS library_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, object_key TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	client := &seedanceFakeClient{}
	svc := library.NewSeedanceService(library.NewSQLiteSeedanceAssetsRepository(conn),
		library.NewSQLiteSeedanceGroupStore(conn), client, st, signer)
	return NewSeedanceHandler(svc, seedanceFakeExtractor{}, conn), svc, conn, client
}

func seedanceInsertAsset(t *testing.T, conn *sql.DB, userID int64, key string) {
	t.Helper()
	if _, err := conn.Exec(`INSERT INTO assets (user_id, object_key, source_kind, url, size_bytes, mime_type)
		VALUES (?, ?, 'upload', ?, 1, 'image/png')`, userID, key, "media://"+key); err != nil {
		t.Fatal(err)
	}
}

func seedanceDo(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/seedance/x", strings.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestSeedanceVerifyUnconfigured503(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, seedanceFakeSettings{}, seedancePublicSigner)
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"image"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "合规接口未配置") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if client.createAssetCalls != 0 {
		t.Fatal("未配置不应调上游")
	}
}

func TestSeedanceVerifyNotOwned403(t *testing.T) {
	h, _, _, _ := newSeedanceTestHandler(t, seedanceConfigured(), seedancePublicSigner)
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u9/other.png","kind":"image"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestSeedanceVerifyUnresolvable400(t *testing.T) {
	h, _, _, _ := newSeedanceTestHandler(t, seedanceConfigured(), seedancePublicSigner)
	for _, body := range []string{`{"url":"https://elsewhere/x.png"}`, `{"url":"media://u1/a.png","kind":"pdf"}`, `{bad`} {
		if rec := seedanceDo(h.Verify, http.MethodPost, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: code=%d", body, rec.Code)
		}
	}
}

func TestSeedanceVerifyOwnedSubmits202(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, seedanceConfigured(), seedancePublicSigner)
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"video"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var got library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.AssetType != seedance.AssetTypeVideo || client.createAssetCalls != 1 {
		t.Fatalf("got %+v calls=%d", got, client.createAssetCalls)
	}
}

func TestSeedanceVerifyLocalMediaRejected(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, seedanceConfigured(), func(s string) string {
		return "http://127.0.0.1:5555/media/" + strings.TrimPrefix(s, "media://") + "?token=t"
	})
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"image"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "公网") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if client.createAssetCalls != 0 {
		t.Fatal("本机地址不应发往上游")
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM seedance_assets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("不应留下校验记录: n=%d err=%v", n, err)
	}
}

func TestSeedanceStatusKeyedByURL(t *testing.T) {
	h, _, conn, _ := newSeedanceTestHandler(t, seedanceConfigured(), seedancePublicSigner)
	if _, err := conn.Exec(`INSERT INTO seedance_assets (object_key, user_id, asset_id, version, status, asset_type)
		VALUES ('u1/a.png', 1, 'asset-9', 1, 'active', 'Image')`); err != nil {
		t.Fatal(err)
	}
	rec := seedanceDo(h.Status, http.MethodPost, `{"urls":["media://u1/a.png","media://u1/never.png","https://x/y.png"]}`)
	var got map[string]library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["media://u1/a.png"].Status != "active" || *got["media://u1/a.png"].AssetID != "asset-9" {
		t.Fatalf("got %+v", got)
	}
}

// AI 角色图是平台预置、已过校验的公共资产：没有用户记录也应显示“已通过”。
func TestSeedanceStatusPublicFallback(t *testing.T) {
	h, svc, _, _ := newSeedanceTestHandler(t, seedanceConfigured(), seedancePublicSigner)
	svc.SetPublicAssets(seedanceFakePublic{"tapnow/sd-assets/asset-1.jpg": "asset-1"})
	rec := seedanceDo(h.Status, http.MethodPost, `{"urls":["media://tapnow/sd-assets/asset-1.jpg"]}`)
	var got map[string]library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	a := got["media://tapnow/sd-assets/asset-1.jpg"]
	if a.Status != "active" || a.AssetID == nil || *a.AssetID != "asset-1" {
		t.Fatalf("got %+v", got)
	}
}

func TestSeedanceConfigReportsConfigured(t *testing.T) {
	for _, c := range []struct {
		st   seedanceFakeSettings
		want bool
	}{{seedanceFakeSettings{}, false}, {seedanceConfigured(), true}} {
		h, _, _, _ := newSeedanceTestHandler(t, c.st, seedancePublicSigner)
		rec := seedanceDo(h.Config, http.MethodGet, "")
		var body struct {
			Configured bool `json:"configured"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("code=%d err=%v", rec.Code, err)
		}
		if body.Configured != c.want {
			t.Fatalf("configured=%v want %v", body.Configured, c.want)
		}
	}
}
```

Run: `cd server && go test ./internal/http/ -run Seedance 2>&1 | head`
Expected: 编译失败 `undefined: NewSeedanceHandler`。

- [ ] **Step 10: 移植 handler**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/seedance_handlers.go server/internal/http/seedance_handlers.go
```

在 `server/internal/http/seedance_handlers.go` 中：

(a) 删除 import `"github.com/james/huabu_studio/apps/api/internal/assets"`。

(b) 把

```go
type SeedanceHandler struct {
	svc     *library.SeedanceService
	storage assets.Storage
	db      *sql.DB
}

func NewSeedanceHandler(svc *library.SeedanceService, storage assets.Storage, db *sql.DB) *SeedanceHandler {
```

替换为

```go
// seedanceKeyExtractor 是 assets.Storage 在这里用到的唯一方法（P1b 的 LocalStorage 认
// media:// 与任意端口的本地媒体 URL）。
type seedanceKeyExtractor interface {
	ExtractKey(urlOrKey string) (string, bool)
}

type SeedanceHandler struct {
	svc     *library.SeedanceService
	storage seedanceKeyExtractor
	db      *sql.DB
}

func NewSeedanceHandler(svc *library.SeedanceService, storage seedanceKeyExtractor, db *sql.DB) *SeedanceHandler {
```

(c) 文件末尾追加：

```go
// Config: GET /seedance/config —— 前端据此在「合规校验」入口提示「合规接口未配置」（spec X5）。
func (h *SeedanceHandler) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"configured": h.svc.Configured()})
}
```

- [ ] **Step 11: 运行测试确认通过**

Run: `cd server && go vet ./internal/http/ ./internal/library/ ./internal/seedance/ && go test ./internal/http/ -run Seedance -v 2>&1 | tail -20 && go test ./...`
Expected: 9 个 handler 测试 PASS，全量测试通过。

- [ ] **Step 12: 提交**

```bash
git add server/internal/db/migrations/0010_p1d_seedance.sql server/internal/seedance server/internal/library/seedance_*.go \
  server/internal/http/seedance_handlers.go server/internal/http/seedance_handlers_test.go \
  server/internal/http/helpers.go server/internal/http/context.go server/internal/http/router.go
git commit -m "feat(server): Seedance 合规校验改走 newapi，未配置与本机素材给出明确提示"
```

---

### Task 4: 3D 导演台数据层与全局模型库目录

**Files:**
- Create: `server/internal/db/migrations/0012_p1d_director.sql`
- Create（移植）: `server/internal/directormodels/repo.go`、`server/internal/directorlibrary/repo.go`
- Create（原样复制）: `server/internal/directormodels/service.go`、`service_test.go`、`server/internal/directorlibrary/service.go`、`service_test.go`
- Test: `server/internal/directormodels/repo_test.go`、`server/internal/directorlibrary/repo_test.go`
- Create（移植）: `server/internal/http/director_library_handlers.go`；Test: `server/internal/http/director_library_handlers_test.go`
- Create（缺失时，原样复制）: `server/internal/http/helpers.go`、`context.go`；Modify: `server/internal/http/router.go`（同 Task 1 Step 1）

**Interfaces:**
- Consumes: `dbtest.Open(t)`；`projects`、`workflow_snapshots` 表（P1a）
- Produces:
  - `directormodels.Model`、`Repository`、`Execer`、`ErrNotFound`、`SQLiteRepository`、`NewSQLiteRepository(*sql.DB)`、`Service{Repo}`、`(*Service).CanCreate(userID) (limit int, ok bool, err error)`、`ValidateUpload(filename string, size int64) error`、`ReferenceLikePattern(objectKey) string`、`UserModelLimit`、`MaxFileSizeBytes`
  - `directorlibrary.Category`、`Model`、`CategoryWithModels`、`Repository`、`Execer`、`ErrNotFound`、`SQLiteRepository`、`NewSQLiteRepository(*sql.DB)`、`Service{Repo}`、`ErrCategoryNotEmpty`
  - `http.directorSigner` 接口 `{Sign(string) string; SignThumb(string, int) string}`、常量 `http.directorMediaProtocol = "media://"`（Task 7 复用）；`http.NewDirectorLibraryHandler(repo directorlibrary.Repository, signer directorSigner) *DirectorLibraryHandler`，方法 `List`

- [ ] **Step 1: http 基础件（与 P1b 字节相同，已存在就跳过）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
test -f server/internal/http/helpers.go || cp $REF/http/helpers.go server/internal/http/helpers.go
test -f server/internal/http/context.go || cp $REF/http/context.go server/internal/http/context.go
grep -q '^func writeJSON' server/internal/http/router.go && \
  perl -0pi -e 's/\nfunc writeJSON\(w nethttp\.ResponseWriter, status int, v any\) \{.*?\n\}\n//s; s/\t"encoding\/json"\n//' server/internal/http/router.go
cd server && go build ./internal/http/ && cd ..
```

Expected: `go build` 无输出。

- [ ] **Step 2: 写迁移**

`server/internal/db/migrations/0012_p1d_director.sql`：

```sql
-- P1d：3D 导演台模型库（参考仓迁移 0092、0093）。
-- 用户级云端模型库：用户上传的 FBX/OBJ/GLB；画布快照里以 media://<object_key> 引用，
-- 删除前扫 workflow_snapshots.payload 做引用检查，删行后进 orphan_objects 走 7 天宽限回收。
CREATE TABLE director_user_models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  -- 展示名：原始文件名去扩展名
  name TEXT NOT NULL,
  -- 原始上传文件名（含扩展名）
  file_name TEXT NOT NULL,
  object_key TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_director_user_models_user ON director_user_models (user_id);

-- 全局模型库：平台资产，用户端只读。桌面端内置库为空（生产库为空，见 spec 附录 P2 记录）。
CREATE TABLE director_library_categories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE director_library_models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  -- 分类下还有模型时数据库层面拒删分类（业务层另有 CountModels 预检）
  category_id INTEGER NOT NULL REFERENCES director_library_categories (id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  file_name TEXT NOT NULL,
  object_key TEXT NOT NULL,
  -- NULL = 无缩略图
  thumb_object_key TEXT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_director_library_models_category ON director_library_models (category_id);
```

- [ ] **Step 3: 复制 service 与原有测试，写仓库的失败测试**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/directormodels server/internal/directorlibrary
cp $REF/directormodels/service.go $REF/directormodels/service_test.go server/internal/directormodels/
cp $REF/directorlibrary/service.go $REF/directorlibrary/service_test.go server/internal/directorlibrary/
```

`server/internal/directormodels/repo_test.go`：

```go
package directormodels

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestInsertListGetDeleteCount(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id1, err := repo.Insert(Model{UserID: 1, Name: "robot", FileName: "robot.fbx", ObjectKey: "u1/2026/10/a_robot.fbx", SizeBytes: 10, MimeType: "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := repo.Insert(Model{UserID: 1, Name: "chair", FileName: "chair.glb", ObjectKey: "u1/2026/10/b_chair.glb", SizeBytes: 20, MimeType: "model/gltf-binary"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListByUser(1)
	if err != nil {
		t.Fatal(err)
	}
	// 同一秒内插入：created_at 相同，按 id DESC 兜底，新的在前。
	if len(list) != 2 || list[0].ID != id2 || list[1].ID != id1 || list[0].CreatedAt.IsZero() {
		t.Fatalf("list = %+v", list)
	}
	m, err := repo.GetByID(id1)
	if err != nil || m.Name != "robot" || m.SizeBytes != 10 {
		t.Fatalf("get = %+v %v", m, err)
	}
	if _, err := repo.GetByID(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// 别人的 user_id 删不掉
	if err := repo.Delete(conn, 2, id1); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountByUser(1); n != 2 {
		t.Fatalf("越权删除不应生效，count=%d", n)
	}
	if err := repo.Delete(conn, 1, id1); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountByUser(1); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func insertProjectWithPayload(t *testing.T, conn *sql.DB, userID int64, payload string, deleted bool) {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (?, 'p')`, userID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, id, payload); err != nil {
		t.Fatal(err)
	}
	if deleted {
		if _, err := conn.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
}

// key 由文件名派生、几乎必带下划线；SQLite 的 LIKE 没有默认转义，漏写 ESCAPE 会误报引用。
func TestCountProjectsReferencingEscapesAndScopes(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@local')`); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(conn)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12Xchair.fbx"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, true)
	insertProjectWithPayload(t, conn, 2, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, false)
	for key, want := range map[string]int64{"u1/ab12_chair.fbx": 1, "u1/ab12Xchair.fbx": 1, "u1/nothing.fbx": 0} {
		n, err := repo.CountProjectsReferencing(1, key)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s: n=%d want %d", key, n, want)
		}
	}
}
```

`server/internal/directorlibrary/repo_test.go`：

```go
package directorlibrary

import (
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestCategoryCRUD(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	id, err := repo.CreateCategory(Category{Name: "家具", SortOrder: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.GetCategory(id)
	if err != nil || c.Name != "家具" || !c.Enabled || c.ModelCount != 0 || c.CreatedAt.IsZero() {
		t.Fatalf("get = %+v %v", c, err)
	}
	if err := repo.UpdateCategory(Category{ID: id, Name: "道具", SortOrder: 1, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	c, _ = repo.GetCategory(id)
	if c.Name != "道具" || c.Enabled || c.SortOrder != 1 {
		t.Fatalf("update = %+v", c)
	}
	if _, err := repo.GetCategory(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestModelThumbNullRoundTrip(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	cat, _ := repo.CreateCategory(Category{Name: "c", Enabled: true})
	id, err := repo.CreateModel(Model{CategoryID: cat, Name: "m", FileName: "m.glb", ObjectKey: "lib/m.glb", SizeBytes: 1, MimeType: "model/gltf-binary", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := conn.QueryRow(`SELECT thumb_object_key IS NULL FROM director_library_models WHERE id = ?`, id).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("空缩略图应落 NULL: %v %v", isNull, err)
	}
	m, _ := repo.GetModel(id)
	if m.ThumbObjectKey != "" || !m.Enabled {
		t.Fatalf("got %+v", m)
	}
	m.ThumbObjectKey = "lib/m.png"
	if err := repo.UpdateModel(conn, *m); err != nil {
		t.Fatal(err)
	}
	m, _ = repo.GetModel(id)
	if m.ThumbObjectKey != "lib/m.png" {
		t.Fatalf("got %+v", m)
	}
}

func TestListEnabledJoinedKeepsEmptyAndFiltersDisabled(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	a, _ := repo.CreateCategory(Category{Name: "A", SortOrder: 1, Enabled: true})
	b, _ := repo.CreateCategory(Category{Name: "B", SortOrder: 2, Enabled: true})
	c, _ := repo.CreateCategory(Category{Name: "C", SortOrder: 0, Enabled: false})
	mk := func(cat int64, name string, sort int, enabled bool) {
		if _, err := repo.CreateModel(Model{CategoryID: cat, Name: name, FileName: name + ".glb", ObjectKey: "lib/" + name, SizeBytes: 1, MimeType: "x", SortOrder: sort, Enabled: enabled}); err != nil {
			t.Fatal(err)
		}
	}
	mk(a, "A1", 2, true)
	mk(a, "A2", 1, true)
	mk(a, "A3", 0, false)
	mk(c, "C1", 0, true)
	out, err := repo.ListEnabledJoined()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].ID != a || out[1].ID != b {
		t.Fatalf("分类 = %+v", out)
	}
	if len(out[0].Models) != 2 || out[0].Models[0].Name != "A2" || out[0].Models[1].Name != "A1" {
		t.Fatalf("A 的模型 = %+v", out[0].Models)
	}
	if out[1].Models == nil || len(out[1].Models) != 0 {
		t.Fatalf("空分类应保留且 models 为空数组: %+v", out[1].Models)
	}
}

func TestDeleteNonEmptyCategoryBlocked(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	cat, _ := repo.CreateCategory(Category{Name: "c", Enabled: true})
	if _, err := repo.CreateModel(Model{CategoryID: cat, Name: "m", FileName: "m.glb", ObjectKey: "lib/m", SizeBytes: 1, MimeType: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteCategory(cat); err == nil {
		t.Fatal("外键 RESTRICT 应拒绝删除非空分类")
	}
	var notEmpty *ErrCategoryNotEmpty
	if err := (&Service{Repo: repo}).DeleteCategory(cat); !errors.As(err, &notEmpty) || notEmpty.Models != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestCountProjectsReferencingAllUsers(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@local')`); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(conn)
	for i, p := range []struct {
		user    int64
		payload string
		deleted bool
	}{
		{1, `"media://lib/a_b.glb"`, false},
		{2, `"media://lib/a_b.glb"`, false},
		{2, `"media://lib/a_b.glb"`, true},
		{1, `"media://lib/aXb.glb"`, false},
	} {
		res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (?, 'p')`, p.user)
		if err != nil {
			t.Fatal(i, err)
		}
		id, _ := res.LastInsertId()
		if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, id, `{"x":`+p.payload+`}`); err != nil {
			t.Fatal(i, err)
		}
		if p.deleted {
			conn.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
		}
	}
	n, err := repo.CountProjectsReferencingAllUsers("lib/a_b.glb")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
```

Run: `cd server && go test ./internal/directormodels/ ./internal/directorlibrary/ 2>&1 | head`
Expected: 编译失败 `undefined: NewSQLiteRepository`。

- [ ] **Step 4: 移植仓库**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/directormodels/repo.go server/internal/directormodels/repo.go
cp $REF/directorlibrary/repo.go server/internal/directorlibrary/repo.go
perl -pi -e 's/NewMySQLRepository/NewSQLiteRepository/g; s/MySQLRepository/SQLiteRepository/g; s/AND ws\.payload LIKE \?$/AND ws.payload LIKE ? ESCAPE '"'"'\\'"'"'/' \
  server/internal/directormodels/repo.go server/internal/directorlibrary/repo.go
grep -n "ESCAPE" server/internal/directormodels/repo.go server/internal/directorlibrary/repo.go
```

Expected: grep 各显示 1 行 `AND ws.payload LIKE ? ESCAPE '\'`。若没有（shell 引号被改坏），手工把两个文件里的 `          AND ws.payload LIKE ?` 改成 `          AND ws.payload LIKE ? ESCAPE '\'`。然后把两个文件包注释里的“schema 见 migration 0092 / 0093”改成“schema 见桌面端迁移 0012_p1d_director.sql（源自参考仓 0092 / 0093）”；把 `directormodels.ReferenceLikePattern` 的注释末尾补一句：“SQLite 的 LIKE 没有默认转义字符，调用方必须写 `ESCAPE '\'`。”

- [ ] **Step 5: 运行数据层测试确认通过**

Run: `cd server && go vet ./internal/directormodels/ ./internal/directorlibrary/ && go test ./internal/directormodels/ ./internal/directorlibrary/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: 参考仓 `service_test.go` 的用例 + 新增 7 个全部 PASS。

- [ ] **Step 6: 写全局库目录接口的失败测试**

`server/internal/http/director_library_handlers_test.go`：

```go
package http

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/directorlibrary"
)

// directorFakeLibraryRepo 只实现 ListEnabledJoined；嵌入接口满足其余方法（被调用会 panic，正好暴露误用）。
type directorFakeLibraryRepo struct {
	directorlibrary.Repository
	cats []directorlibrary.CategoryWithModels
}

func (f directorFakeLibraryRepo) ListEnabledJoined() ([]directorlibrary.CategoryWithModels, error) {
	return f.cats, nil
}

type directorFakeSigner struct{}

func (directorFakeSigner) Sign(s string) string {
	return "https://signed/" + strings.TrimPrefix(s, "media://")
}

func (directorFakeSigner) SignThumb(s string, w int) string {
	return fmt.Sprintf("https://thumb/%d/%s", w, strings.TrimPrefix(s, "media://"))
}

type directorLibraryOut struct {
	Categories []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Models []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			FileName string `json:"fileName"`
			URL      string `json:"url"`
			ThumbURL string `json:"thumbUrl"`
		} `json:"models"`
	} `json:"categories"`
}

func TestDirectorLibraryListSignsAndKeepsEmpty(t *testing.T) {
	repo := directorFakeLibraryRepo{cats: []directorlibrary.CategoryWithModels{
		{Category: directorlibrary.Category{ID: 1, Name: "家具"}, Models: []directorlibrary.Model{
			{ID: 10, Name: "椅子", FileName: "chair.glb", ObjectKey: "lib/chair.glb", ThumbObjectKey: "lib/chair.png"},
			{ID: 11, Name: "桌子", FileName: "desk.glb", ObjectKey: "lib/desk.glb"},
		}},
		{Category: directorlibrary.Category{ID: 2, Name: "空分类"}, Models: []directorlibrary.Model{}},
	}}
	rec := httptest.NewRecorder()
	NewDirectorLibraryHandler(repo, directorFakeSigner{}).List(rec, httptest.NewRequest("GET", "/api/v1/director/library", nil))
	var out directorLibraryOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Categories) != 2 {
		t.Fatalf("categories = %+v", out.Categories)
	}
	m := out.Categories[0].Models
	if m[0].URL != "https://signed/lib/chair.glb" || m[0].ThumbURL != "https://thumb/400/lib/chair.png" {
		t.Fatalf("签名不对: %+v", m[0])
	}
	if m[1].ThumbURL != "" {
		t.Fatalf("无缩略图应为空串: %+v", m[1])
	}
	if !strings.Contains(rec.Body.String(), `"models":[]`) {
		t.Fatalf("空分类的 models 必须是 []：%s", rec.Body)
	}
}

// 桌面端内置库为空：必须返回 {"categories":[]} 而不是 null。
func TestDirectorLibraryListEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	NewDirectorLibraryHandler(directorFakeLibraryRepo{}, directorFakeSigner{}).List(rec, httptest.NewRequest("GET", "/api/v1/director/library", nil))
	if strings.TrimSpace(rec.Body.String()) != `{"categories":[]}` {
		t.Fatalf("body = %s", rec.Body)
	}
}
```

Run: `cd server && go test ./internal/http/ -run DirectorLibrary 2>&1 | head`
Expected: 编译失败 `undefined: NewDirectorLibraryHandler`。

- [ ] **Step 7: 写 handler**

`server/internal/http/director_library_handlers.go`：

```go
package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/directorlibrary"
)

// directorSigner 是 *MediaSigner 在导演台两个 handler 里用到的方法（P1b 提供实现）。
type directorSigner interface {
	Sign(string) string
	SignThumb(string, int) string
}

// directorMediaProtocol 与 assets.MediaProtocol 同值：导演台两张表只存裸 object_key，出图前补前缀再签名。
const directorMediaProtocol = "media://"

// DirectorLibraryHandler 是「3D 导演台·后台全局模型库」的用户端 HTTP 层：
// 只有一个只读目录接口。写路径（参考仓的 AdminDirectorLibraryHandler）桌面端不移植，
// 内置库为空。
type DirectorLibraryHandler struct {
	repo   directorlibrary.Repository
	signer directorSigner
}

func NewDirectorLibraryHandler(repo directorlibrary.Repository, signer directorSigner) *DirectorLibraryHandler {
	return &DirectorLibraryHandler{repo: repo, signer: signer}
}

// List GET /director/library：enabled 分类带各自 enabled 模型，都按 sort_order,id
// 排序。url/thumbUrl 每次现签——DB 只存 object_key；无缩略图时 thumbUrl 固定空串
// （前端契约按空串判断兜底占位图，不能省字段）。
func (h *DirectorLibraryHandler) List(w http.ResponseWriter, r *http.Request) {
	cats, err := h.repo.ListEnabledJoined()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		models := make([]map[string]any, 0, len(c.Models))
		for _, m := range c.Models {
			thumbURL := ""
			if m.ThumbObjectKey != "" {
				thumbURL = h.signer.SignThumb(directorMediaProtocol+m.ThumbObjectKey, 400)
			}
			models = append(models, map[string]any{
				"id":       m.ID,
				"name":     m.Name,
				"fileName": m.FileName,
				"url":      h.signer.Sign(directorMediaProtocol + m.ObjectKey),
				"thumbUrl": thumbURL,
			})
		}
		out = append(out, map[string]any{
			"id":     c.ID,
			"name":   c.Name,
			"models": models,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}
```

- [ ] **Step 8: 运行测试确认通过**

Run: `cd server && go vet ./internal/http/ && go test ./internal/http/ -run DirectorLibrary -v && go test ./...`
Expected: 2 个 handler 测试 PASS，全量通过。

- [ ] **Step 9: 提交**

```bash
git add server/internal/db/migrations/0012_p1d_director.sql server/internal/directormodels server/internal/directorlibrary \
  server/internal/http/director_library_handlers.go server/internal/http/director_library_handlers_test.go \
  server/internal/http/helpers.go server/internal/http/context.go server/internal/http/router.go
git commit -m "feat(server): 3D 导演台模型库数据层与全局库目录接口"
```

---

### Task 5: 后处理接口（去字幕 / 转高清 / 转白模 / 预估 / 配置）与纯 Go 视频时长

**Files:**
- Create（原样复制）: `server/internal/mediaprobe/probe.go`、`probe_test.go`
- Create: `server/internal/mediaprobe/mp4.go`；Test: `server/internal/mediaprobe/mp4_test.go`
- Create（原样复制，与 Task 1 字节相同）: `server/internal/canvasgen/postprocess_config.go`、`postprocess_config_test.go`
- Create（契约桩，`//go:build p1dstub`）: `server/internal/canvasgen/p1dstub_enqueue.go`、`server/internal/generation/p1dstub.go`
- Create（移植）: `server/internal/http/canvas_postprocess_handlers.go`；Test（移植）: `server/internal/http/canvas_postprocess_handlers_test.go`
- Create（缺失时，原样复制）: `server/internal/http/helpers.go`、`context.go`；Modify: `server/internal/http/router.go`（同 Task 1 Step 1）

**Interfaces:**
- Consumes: `projects.Project`、`projects.Snapshot`、`projects.ErrNotFound`（P1a）；`models.ModelConfig`（P1a）；`billing.EstimateFreeze`（P1a）；P1c 的 `canvasgen.EnqueueInput`、`generation.Task`、`generation.ErrNotFound`、`generation.ErrStorageQuotaExceeded`（合并前由桩提供，字段照抄参考仓）
- Produces:
  - `mediaprobe.ProbeDurationMs(ctx, data []byte) (int64, error)`（参考仓原样）、`mediaprobe.ErrProbeUnavailable`、`mediaprobe.ProbeMP4DurationMs(data []byte) (int64, error)`、`mediaprobe.ErrNotMP4`、`mediaprobe.ProbeDurationMsPortable(ctx, data []byte) (int64, error)`（Task 8 也会把 P1b/P1c 的探测器换成它）
  - `http.NewCanvasPostprocessHandler(s settings.Repository, canvas canvasPostprocessEnqueuer, projectRepo canvasPostprocessProjects, assetsRepo canvasPostprocessAssets, gen canvasPostprocessGeneration, modelRepo postprocessModelLookup, signer postprocessSigner) *CanvasPostprocessHandler`，方法 `GetConfig`、`Desub`、`Upscale`、`WhiteModel`、`Estimate`、`AdminGet`、`AdminPut`；`postprocessSigner` = `{ObjectKey(string) string; Sign(string) string}`

**已知限制（spec S12）：** 普通（非限时）去字幕 / 转高清 / 转白模把源视频签成本地 URL 放进 `inputs.video`，上游拉不到；这条路径和其他带参考媒体的生成一样，等 S12 方案落在 P1c 的 URL 签名接口位上。本任务不改这一点。

- [ ] **Step 1: http 基础件 + 判断是否需要契约桩**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
test -f server/internal/http/helpers.go || cp $REF/http/helpers.go server/internal/http/helpers.go
test -f server/internal/http/context.go || cp $REF/http/context.go server/internal/http/context.go
grep -q '^func writeJSON' server/internal/http/router.go && \
  perl -0pi -e 's/\nfunc writeJSON\(w nethttp\.ResponseWriter, status int, v any\) \{.*?\n\}\n//s; s/\t"encoding\/json"\n//' server/internal/http/router.go
cd server
grep -qs 'type EnqueueInput struct' internal/canvasgen/*.go && echo HAVE_ENQUEUE_INPUT
grep -qs 'ErrStorageQuotaExceeded' internal/generation/*.go && echo HAVE_GENERATION
cd ..
```

两行都打印 → P1c 已合并，跳过 Step 2，后续命令去掉 `-tags p1dstub`。否则执行 Step 2，后续命令都带 `-tags p1dstub`。

- [ ] **Step 2: 写契约桩（只在 `-tags p1dstub` 下编译，Task 8 删除）**

`server/internal/canvasgen/p1dstub_enqueue.go`：

```go
//go:build p1dstub

// P1d 与 P1c 并发开发用的契约桩：字段照抄参考仓 canvasgen/service.go:90-101 的 EnqueueInput。
// 只在 -tags p1dstub 下编译；P1c 合并后由 P1d Task 8 删除本文件。
package canvasgen

type EnqueueInput struct {
	UserID       int64
	ProjectID    int64
	NodeID       string
	ModelID      string
	Inputs       map[string]any
	PresetKey    string
	PresetParams map[string]string
}
```

`server/internal/generation/p1dstub.go`：

```go
//go:build p1dstub

// P1d 与 P1c 并发开发用的契约桩：只含后处理 handler 用到的符号，字段与文案照抄参考仓
// generation/repository.go:11-47 与 storage_quota.go:14。只在 -tags p1dstub 下编译；
// P1c 合并后由 P1d Task 8 删除本文件。
package generation

import (
	"encoding/json"
	"errors"
)

type Task struct {
	ID      int64           `json:"id"`
	UserID  int64           `json:"userId"`
	ModelID string          `json:"modelId"`
	Status  string          `json:"status"`
	Output  json.RawMessage `json:"output,omitempty"`
	Error   string          `json:"error,omitempty"`
}

var ErrNotFound = errors.New("task not found")

var ErrStorageQuotaExceeded = errors.New("存储空间已满，请清理后重试再生成")
```

- [ ] **Step 3: 写纯 Go 时长解析的失败测试**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/mediaprobe
cp $REF/mediaprobe/probe_test.go server/internal/mediaprobe/probe_test.go
```

`server/internal/mediaprobe/mp4_test.go`：

```go
package mediaprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func box(typ string, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(body)))
	copy(out[4:8], typ)
	return append(out, body...)
}

func mvhdV0(timescale, duration uint32) []byte {
	p := make([]byte, 100) // version 0, flags 0
	binary.BigEndian.PutUint32(p[12:16], timescale)
	binary.BigEndian.PutUint32(p[16:20], duration)
	return box("mvhd", p)
}

func mvhdV1(timescale uint32, duration uint64) []byte {
	p := make([]byte, 112)
	p[0] = 1
	binary.BigEndian.PutUint32(p[20:24], timescale)
	binary.BigEndian.PutUint64(p[24:32], duration)
	return box("mvhd", p)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var ftyp = box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41"))

func TestProbeMP4V0(t *testing.T) {
	data := concat(ftyp, box("moov", mvhdV0(1000, 5500), box("trak")))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 5500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

// 非 faststart 的文件 moov 在 mdat 之后（录屏、很多上游产物都是这样）。
func TestProbeMP4V1MoovAtEnd(t *testing.T) {
	data := concat(ftyp, box("mdat", make([]byte, 4096)), box("moov", mvhdV1(90000, 9*90000+45000)))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 9500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

func TestProbeMP4LargeSizeBox(t *testing.T) {
	mdatPayload := make([]byte, 32)
	large := make([]byte, 16)
	binary.BigEndian.PutUint32(large[0:4], 1)
	copy(large[4:8], "mdat")
	binary.BigEndian.PutUint64(large[8:16], uint64(16+len(mdatPayload)))
	data := concat(ftyp, large, mdatPayload, box("moov", mvhdV0(600, 1800)))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 3000 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

func TestProbeMP4Rejects(t *testing.T) {
	truncated := box("moov", mvhdV0(1000, 1000))
	binary.BigEndian.PutUint32(truncated[0:4], uint32(len(truncated)+50))
	cases := map[string][]byte{
		"垃圾":     []byte("not a video at all"),
		"无 moov": concat(ftyp, box("mdat", make([]byte, 8))),
		"截断":     truncated,
		"webm":   {0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81},
	}
	for name, data := range cases {
		if _, err := ProbeMP4DurationMs(data); !errors.Is(err, ErrNotMP4) {
			t.Fatalf("%s: err = %v, want ErrNotMP4", name, err)
		}
	}
	if _, err := ProbeMP4DurationMs(concat(ftyp, box("moov", mvhdV0(0, 100)))); err == nil {
		t.Fatal("timescale 为 0 应报错")
	}
}

// 用户机器上通常没有 ffprobe：Portable 必须退回纯 Go 解析。
func TestPortableFallsBackWithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ProbeDurationMs(context.Background(), []byte("x")); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("清空 PATH 后应找不到 ffprobe: %v", err)
	}
	ms, err := ProbeDurationMsPortable(context.Background(), concat(ftyp, box("moov", mvhdV0(1000, 2500))))
	if err != nil || ms != 2500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
	if _, err := ProbeDurationMsPortable(context.Background(), []byte("garbage")); !errors.Is(err, ErrNotMP4) {
		t.Fatalf("err = %v", err)
	}
}
```

Run: `cd server && go test ./internal/mediaprobe/ 2>&1 | head`
Expected: 编译失败 `undefined: ProbeDurationMs`。

- [ ] **Step 4: 复制 ffprobe 实现并写纯 Go 解析**

```bash
cp /Users/james/github/huabu_studio/apps/api/internal/mediaprobe/probe.go server/internal/mediaprobe/probe.go
```

`server/internal/mediaprobe/mp4.go`：

```go
package mediaprobe

import (
	"context"
	"encoding/binary"
	"errors"
)

// ErrNotMP4：数据不是可解析的 ISO BMFF（mp4 / mov），或缺少 moov/mvhd。
var ErrNotMP4 = errors.New("不是可识别的 MP4/MOV 文件，无法读取时长")

// ProbeMP4DurationMs 从 ISO BMFF 的 moov/mvhd 读容器声明的时长（毫秒）。
//
// 参考仓坚持数真实帧（ProbeDurationMs）是为了防止篡改容器时长薅按秒计费；桌面端不计费
// （spec X3），而用户机器上通常没有 ffprobe，这是唯一可用的纯 Go 路径。
func ProbeMP4DurationMs(data []byte) (int64, error) {
	moov, ok := findBox(data, "moov")
	if !ok {
		return 0, ErrNotMP4
	}
	mvhd, ok := findBox(moov, "mvhd")
	if !ok || len(mvhd) < 4 {
		return 0, ErrNotMP4
	}
	var timescale uint32
	var duration uint64
	switch mvhd[0] {
	case 0:
		if len(mvhd) < 20 {
			return 0, ErrNotMP4
		}
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	case 1:
		if len(mvhd) < 32 {
			return 0, ErrNotMP4
		}
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		duration = binary.BigEndian.Uint64(mvhd[24:32])
	default:
		return 0, ErrNotMP4
	}
	if timescale == 0 || duration == 0 {
		return 0, errors.New("MP4 声明的时长为 0")
	}
	return int64((duration*1000 + uint64(timescale)/2) / uint64(timescale)), nil
}

// findBox 在一层 box 序列里找第一个指定类型的 box，返回其负载（不含头）。
func findBox(data []byte, typ string) ([]byte, bool) {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data[0:4]))
		name := string(data[4:8])
		header := uint64(8)
		switch size {
		case 1: // 64 位 largesize
			if len(data) < 16 {
				return nil, false
			}
			size = binary.BigEndian.Uint64(data[8:16])
			header = 16
		case 0: // 延伸到文件末尾
			size = uint64(len(data))
		}
		if size < header || size > uint64(len(data)) {
			return nil, false
		}
		if name == typ {
			return data[header:size], true
		}
		data = data[size:]
	}
	return nil, false
}

// ProbeDurationMsPortable 先用系统 ffprobe 数真实帧；机器上没有 ffprobe 时退回读 MP4 容器时长。
func ProbeDurationMsPortable(ctx context.Context, data []byte) (int64, error) {
	ms, err := ProbeDurationMs(ctx, data)
	if errors.Is(err, ErrProbeUnavailable) {
		return ProbeMP4DurationMs(data)
	}
	return ms, err
}
```

Run: `cd server && go vet ./internal/mediaprobe/ && go test ./internal/mediaprobe/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: 新增 5 个 PASS；参考仓 3 个在没有 ffprobe / 样本时 SKIP。

- [ ] **Step 5: 复制后处理配置与 handler 测试并改造（先失败）**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/canvasgen
cp $REF/canvasgen/postprocess_config.go $REF/canvasgen/postprocess_config_test.go server/internal/canvasgen/
cp $REF/http/canvas_postprocess_handlers_test.go server/internal/http/canvas_postprocess_handlers_test.go
perl -pi -e 's/newNoopSigner\(\)/postprocessNoopSigner{}/g; s/httpFakeModelLookup/postprocessFakeModelLookup/g' server/internal/http/canvas_postprocess_handlers_test.go
grep -c 'postprocessNoopSigner{}' server/internal/http/canvas_postprocess_handlers_test.go
```

Expected: 计数 ≥ 4。在该测试文件末尾追加：

```go
// postprocessFakeModelLookup 满足 postprocessModelLookup：按 modelId 返回预置模型(含隐藏)。
// 参考仓叫 httpFakeModelLookup、定义在不移植的 drama_postprocess_handlers_test.go。
type postprocessFakeModelLookup struct {
	m map[string]*models.ModelConfig
}

func (f *postprocessFakeModelLookup) GetByModelID(id string) (*models.ModelConfig, error) {
	if mc, ok := f.m[id]; ok {
		return mc, nil
	}
	return nil, errors.New("model not found")
}

// postprocessNoopSigner 复刻参考仓 newNoopSigner()（noopStorage）的语义：
// 只认 media://，签成 https://cdn.example.com/<key>；认不出原样返回。
type postprocessNoopSigner struct{}

func (postprocessNoopSigner) ObjectKey(s string) string {
	const p = "media://"
	if strings.HasPrefix(s, p) {
		return s[len(p):]
	}
	return ""
}

func (postprocessNoopSigner) Sign(s string) string {
	const p = "media://"
	if strings.HasPrefix(s, p) {
		return "https://cdn.example.com/" + s[len(p):]
	}
	return s
}

// postprocessMP4 构造一个只有 ftyp + moov/mvhd 的最小 mp4，声明时长 durationMs 毫秒。
func postprocessMP4(durationMs uint32) []byte {
	mvhd := make([]byte, 8+100)
	binary.BigEndian.PutUint32(mvhd[0:4], uint32(len(mvhd)))
	copy(mvhd[4:8], "mvhd")
	binary.BigEndian.PutUint32(mvhd[8+12:8+16], 1000)
	binary.BigEndian.PutUint32(mvhd[8+16:8+20], durationMs)
	moov := make([]byte, 8, 8+len(mvhd))
	binary.BigEndian.PutUint32(moov[0:4], uint32(8+len(mvhd)))
	copy(moov[4:8], "moov")
	moov = append(moov, mvhd...)
	ftyp := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}
	return append(ftyp, moov...)
}

// 桌面端没有 ffprobe：默认探测器必须靠纯 Go 读出 mp4 时长并照常提交、回填。
func TestDesub_DefaultProbeReadsMP4WithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assetsRepo := &desubFakeAssets{}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://video9.mp4", nil),
		assetsRepo, nil,
	)
	h.download = func(context.Context, string) ([]byte, error) { return postprocessMP4(7300), nil }
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if got := enq.captured.Inputs["duration"]; got != 7.3 {
		t.Errorf("inputs.duration = %v, want 7.3", got)
	}
	if ms := assetsRepo.setCalls["video9.mp4"]; ms != 7300 {
		t.Errorf("应回填 7300ms, got %v", assetsRepo.setCalls)
	}
}

func TestDesub_DefaultProbeRejectsNonMP4(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://clip.webm", nil),
		&desubFakeAssets{}, nil,
	)
	h.download = func(context.Context, string) ([]byte, error) { return []byte{0x1a, 0x45, 0xdf, 0xa3}, nil }
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{
		"projectId": 2, "nodeId": "node-sub", "sourceNodeId": "node-src", "modelId": "desub-std",
	}))
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("无法获取视频时长")) {
		t.Fatalf("want 400 无法获取视频时长, got %d body=%s", rec.Code, rec.Body)
	}
	if enq.captured != nil {
		t.Error("不应 enqueue")
	}
}
```

并在该测试文件的 import 块加入 `"encoding/binary"` 和 `"strings"`。

Run: `cd server && go test -tags p1dstub ./internal/http/ -run 'Desub|Upscale|WhiteModel|PostprocessEstimate|CanvasPostprocessConfig|TieredEndpoints' 2>&1 | head`（P1c 已合并则去掉 `-tags p1dstub`）
Expected: 编译失败 `undefined: NewCanvasPostprocessHandler`。

- [ ] **Step 6: 移植 handler**

```bash
cp /Users/james/github/huabu_studio/apps/api/internal/http/canvas_postprocess_handlers.go server/internal/http/canvas_postprocess_handlers.go
```

在 `server/internal/http/canvas_postprocess_handlers.go` 中：

(a) import 块加入 `"github.com/james/huabu_studio/apps/api/internal/models"`。

(b) 在 `canvasPostprocessGeneration` 接口定义之后插入：

```go
// postprocessModelLookup 按 modelId 取模型配置（含隐藏）。参考仓定义在
// drama_agent_task_handlers.go，桌面端不移植 drama，搬到这里。
type postprocessModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// postprocessSigner 是 *MediaSigner 在这里用到的两个方法（P1b 提供实现）。用窄接口而不是
// *MediaSigner，本文件可以先于 P1b 合并编译，测试用替身。
type postprocessSigner interface {
	ObjectKey(string) string
	Sign(string) string
}
```

(c) 结构体字段 `signer *MediaSigner` → `signer postprocessSigner`；构造函数参数 `signer *MediaSigner,` → `signer postprocessSigner,`。

(d) `probe:    mediaprobe.ProbeDurationMs,` → `probe:    mediaprobe.ProbeDurationMsPortable,`；并把结构体里 `download/probe` 字段上方的注释改为：

```go
	// download/probe 是懒回填时长的两步：下载源视频全量字节 → 探测时长（先试系统 ffprobe，
	// 没有就读 MP4 容器时长；桌面端用户机器通常没有 ffprobe）。拆成可替换字段，测试注入 fake。
```

(e) `Desub` 上方注释里“防篡改薅按秒计费”“计费秒数服务端权威”这类描述后补一句：“桌面端不计费（spec X3），服务端时长仍是上游请求里 `duration` 的唯一来源。”

Run: `grep -n '\*MediaSigner\|ProbeDurationMs,' server/internal/http/canvas_postprocess_handlers.go`
Expected: 无输出。

- [ ] **Step 7: 运行测试确认通过**

Run: `cd server && go vet -tags p1dstub ./internal/http/ ./internal/canvasgen/ ./internal/generation/ ./internal/mediaprobe/ && go test -tags p1dstub ./internal/http/ ./internal/canvasgen/ ./internal/mediaprobe/ -v 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`（P1c 已合并则去掉 tag）
Expected: 三个包 `ok`，无 FAIL（参考仓后处理 42 个用例 + 新增 2 个）。再跑 `go test -tags p1dstub ./...`。

- [ ] **Step 8: 提交**

```bash
git add server/internal/mediaprobe server/internal/canvasgen/postprocess_config.go server/internal/canvasgen/postprocess_config_test.go \
  server/internal/http/canvas_postprocess_handlers.go server/internal/http/canvas_postprocess_handlers_test.go \
  server/internal/http/helpers.go server/internal/http/context.go server/internal/http/router.go
git add server/internal/canvasgen/p1dstub_enqueue.go server/internal/generation/p1dstub.go 2>/dev/null || true
git commit -m "feat(server): 移植后处理接口，无 ffprobe 时用纯 Go 读取 MP4 时长"
```

---

### Task 6: P1a 遗留的测试小项

**Files:**
- Modify: `server/internal/db/sqlite_test.go`
- Modify: `server/internal/settings/repository_test.go:45-58`
- Modify: `server/internal/models/repository_test.go`

**Interfaces:**
- Consumes: `db.Open`、`db.Migrate`、`dbtest.Open`、`settings.NewSQLiteRepository`、`models.NewSQLiteRepository`、`(*models.SQLiteRepository).Create/Update/GetByID`（P1a）
- Produces: 无（只改测试）

来源：`.superpowers/sdd/2026-10-06-p1a-sqlite-data-layer/progress.md` 的 deferred minor。已核对现状：“迁移中途失败整体回滚”已有 `TestMigrationDanglingForeignKeyRollsBack` 覆盖，不再做；`MutateClientSnapshot` 注释与 gofmt 已在 `c0982e7` 修掉；`SaveAuthoritative` 回读版本、`MutateSnapshot` 回调持锁属 P1c 注意事项；“外键恢复失败时连接回池”极不可能，接受不修。

- [ ] **Step 1: 补“Open 自己建出带空格的目录”测试**

在 `server/internal/db/sqlite_test.go` 的 `TestOpenPathWithSpaces` 之后追加（import 加 `"runtime"`）：

```go
// TestOpenPathWithSpaces 预先建好了目录；这里覆盖 Open 自己建出带空格的多级目录，且权限仅当前用户。
func TestOpenCreatesMissingDirWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	conn, err := db.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("目录没有被建出来: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("目录权限 = %o, want 700", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: `TestSetLargeValue` 不再吞掉 Get 的错误**

在 `server/internal/settings/repository_test.go` 中把

```go
	v, _ := repo.Get("big")
```

替换为

```go
	v, err := repo.Get("big")
	if err != nil {
		t.Fatal(err)
	}
```

- [ ] **Step 3: models 测试去掉 MySQL 时代的预清理、补 Update 与 null 路径**

在 `server/internal/models/repository_test.go` 中删除三处预清理块（`TestGetByID`、`TestImageProtocolFieldsRoundTrip`、`TestAdminNoteRoundTrip` 各一处），连同 `TestGetByID` 里的两行注释：

```bash
perl -0pi -e 's/\n\t\/\/ 清掉之前中断的测试残留数据，保证测试幂等；\n\t\/\/ 否则 model_id 上的唯一索引会让 Create 触发 UNIQUE 约束失败。//; s/\n\tif _, err := conn\.Exec\(`DELETE FROM model_configs WHERE model_id = \?`, slug\); err != nil \{\n\t\tt\.Fatalf\("pre-clean: %v", err\)\n\t\}\n//g' server/internal/models/repository_test.go
grep -n 'pre-clean\|DELETE FROM model_configs' server/internal/models/repository_test.go
```

Expected: grep 无输出（每个测试用的都是 `dbtest.Open` 的全新库，预清理没有意义）。

在 `TestJSONColumnsStoredAsText` 的最后一个 `if got.GenConcurrency() …` 判断之后、函数结束之前追加：

```go
	// Update 也必须以 TEXT 落库。
	m.UISchema = json.RawMessage(`{"duration":{"default":10}}`)
	m.GenControlRaw = json.RawMessage(`{"concurrency":5}`)
	if _, err := repo.Update(m.ID, m); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"request_template", "polling_template", "ui_schema", "billing_config", "gen_control"} {
		var typ string
		if err := conn.QueryRow(`SELECT typeof(`+col+`) FROM model_configs WHERE id = ?`, m.ID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		if typ != "text" {
			t.Fatalf("Update 后 %s 存成了 %s，want text", col, typ)
		}
	}
```

在文件末尾追加：

```go
// 可选 JSON 列的字面量 null / 空值写成 SQL NULL；request_template 是 NOT NULL 列，
// 字面量 null 原样存成文本 "null"（与参考仓一致，钉住现状）。
func TestNullJSONLiteralsBecomeSQLNull(t *testing.T) {
	conn := openTestDB(t)
	repo := NewSQLiteRepository(conn)
	m, err := repo.Create(ModelConfig{
		Type: "image", Name: "n", ModelID: "null-json", BasePath: "/v1/x",
		RequestTemplate:  json.RawMessage(`null`),
		PollingTemplate:  json.RawMessage(`null`),
		UISchema:         json.RawMessage(``),
		BillingConfigRaw: json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"polling_template", "ui_schema", "billing_config", "gen_control"} {
		var isNull bool
		if err := conn.QueryRow(`SELECT `+col+` IS NULL FROM model_configs WHERE id = ?`, m.ID).Scan(&isNull); err != nil {
			t.Fatal(err)
		}
		if !isNull {
			t.Fatalf("%s 应为 SQL NULL", col)
		}
	}
	var rt string
	if err := conn.QueryRow(`SELECT request_template FROM model_configs WHERE id = ?`, m.ID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "null" {
		t.Fatalf("request_template = %q, want \"null\"", rt)
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `cd server && go vet ./internal/db/ ./internal/settings/ ./internal/models/ && go test ./internal/db/ ./internal/settings/ ./internal/models/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'`
Expected: 全部 PASS，含新增的 `TestOpenCreatesMissingDirWithSpaces`、`TestNullJSONLiteralsBecomeSQLNull`。若 `TestOpenCreatesMissingDirWithSpaces` 的权限断言失败（umask 影响），说明 `db.Open` 的 `MkdirAll(…, 0o700)` 被 umask 收紧之外还有别的问题——停下报告，不要改断言。

- [ ] **Step 5: 提交**

```bash
git add server/internal/db/sqlite_test.go server/internal/settings/repository_test.go server/internal/models/repository_test.go
git commit -m "test(server): 补齐 P1a 遗留的数据层测试小项"
```

---

### Task 7: 3D 导演台用户模型库 HTTP（第二批，P1b 合并后）

**Files:**
- Create（移植）: `server/internal/http/director_model_handlers.go`
- Test: `server/internal/http/director_model_handlers_test.go`

**Interfaces:**
- Consumes: Task 4 的 `directormodels.*`、`directorSigner`、`directorMediaProtocol`；P1b 的 `assets.Storage`、`assets.PutObjectInput`、`assets.StoredObject`、`library.OrphansRepository`、`library.Execer`、`library.NewSQLiteOrphansRepository`、`storageUserRepo`、`readUploadForm`、`uploadError`、`uniqueUploadFilename`、`orphan_objects` 表
- Produces: `http.NewDirectorModelHandler(svc *directormodels.Service, storage assets.Storage, orphans library.OrphansRepository, users storageUserRepo, signer directorSigner, db *sql.DB) *DirectorModelHandler`，方法 `Upload`、`List`、`Delete`

- [ ] **Step 1: 确认 P1b 的依赖已在基线上**

```bash
cd server
grep -n 'type storageUserRepo interface' internal/http/*.go
grep -n 'func readUploadForm\|func uniqueUploadFilename\|type uploadError' internal/http/*.go
grep -n 'func NewSQLiteOrphansRepository\|type Execer interface' internal/library/*.go
grep -n 'type Storage interface' internal/assets/*.go
```

Expected: 每条都有输出。任一缺失 → P1b 尚未合并，停下报告 BLOCKED。若 `assets.Storage` 的方法集与参考仓（9 个方法）不同，Step 2 的替身按 P1b 的实际接口补齐方法。

- [ ] **Step 2: 写失败的测试**

`server/internal/http/director_model_handlers_test.go`：

```go
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/directormodels"
	"github.com/james/huabu_studio/apps/api/internal/library"
)

// directorFakeRepo 是 directormodels.Repository 的手写 fake：条数恒 0（不触发 limit_reached）。
type directorFakeRepo struct{}

func (directorFakeRepo) Insert(directormodels.Model) (int64, error)       { return 1, nil }
func (directorFakeRepo) ListByUser(int64) ([]directormodels.Model, error) { return nil, nil }
func (directorFakeRepo) GetByID(int64) (*directormodels.Model, error) {
	return nil, directormodels.ErrNotFound
}
func (directorFakeRepo) Delete(directormodels.Execer, int64, int64) error { return nil }
func (directorFakeRepo) CountByUser(int64) (int64, error)                 { return 0, nil }
func (directorFakeRepo) CountProjectsReferencing(int64, string) (int64, error) {
	return 0, nil
}

// directorTrackingStorage 实现 assets.Storage，记录是否写过对象。
type directorTrackingStorage struct{ putCalled bool }

func (s *directorTrackingStorage) PutObject(_ context.Context, in assets.PutObjectInput) (assets.StoredObject, error) {
	s.putCalled = true
	_, size := in.Content()
	return assets.StoredObject{URL: "media://" + in.Key, Size: size}, nil
}
func (s *directorTrackingStorage) DeleteObject(context.Context, string) error { return nil }
func (s *directorTrackingStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://cdn.example.com/" + key, nil
}
func (s *directorTrackingStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	return "https://cdn.example.com/" + key, nil
}
func (s *directorTrackingStorage) ExtractKey(u string) (string, bool) {
	if strings.HasPrefix(u, "media://") {
		return strings.TrimPrefix(u, "media://"), true
	}
	return "", false
}
func (s *directorTrackingStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *directorTrackingStorage) StaticKey(_ int64, filename string) string { return "k/" + filename }
func (s *directorTrackingStorage) NewKey(_ int64, filename string) string    { return "k/" + filename }
func (s *directorTrackingStorage) CopyObject(_ context.Context, _, dstKey, _ string) (assets.StoredObject, error) {
	return assets.StoredObject{URL: "media://" + dstKey}, nil
}

type directorFakeUserRepo struct {
	used, quota int64
	added       []int64
}

func (f *directorFakeUserRepo) AddStorage(_ int64, delta int64) error {
	f.added = append(f.added, delta)
	return nil
}
func (f *directorFakeUserRepo) StorageUsage(int64) (int64, int64, error) { return f.used, f.quota, nil }

func doDirectorUpload(t *testing.T, h *DirectorModelHandler, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {"application/octet-stream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/director/models", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), 7))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	return rec
}

// 存储配额预检：超额 409 + 中文文案，不触碰存储（区别于条数上限的 {"error":"limit_reached"}）。
func TestDirectorModelUpload_StorageQuotaExceededRejects409(t *testing.T) {
	storage := &directorTrackingStorage{}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, storage, nil,
		&directorFakeUserRepo{used: 100, quota: 101}, directorFakeSigner{}, nil)
	rec := doDirectorUpload(t, h, "model.fbx", []byte("12345"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("超配额应 409, got %d body=%s", rec.Code, rec.Body)
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["error"] != "存储空间已满，请清理后重试" {
		t.Fatalf("409 body = %v", m)
	}
	if storage.putCalled {
		t.Error("超配额不应写对象")
	}
}

func TestDirectorModelUpload_UnderQuotaPasses(t *testing.T) {
	storage := &directorTrackingStorage{}
	users := &directorFakeUserRepo{used: 10, quota: 1000}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, storage, nil, users, directorFakeSigner{}, nil)
	rec := doDirectorUpload(t, h, "model.obj", []byte("obj-bytes"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("未超额应 201, got %d body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"url":"https://signed/`) || len(users.added) != 1 || users.added[0] != 9 {
		t.Fatalf("body=%s added=%v", rec.Body, users.added)
	}
}

func TestDirectorModelUpload_RejectsUnsupportedExt(t *testing.T) {
	h := NewDirectorModelHandler(&directormodels.Service{Repo: directorFakeRepo{}}, &directorTrackingStorage{}, nil,
		&directorFakeUserRepo{quota: 1 << 40}, directorFakeSigner{}, nil)
	if rec := doDirectorUpload(t, h, "scene.gltf", []byte("x")); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rec.Code)
	}
}

func directorDeleteReq(id string, userID int64, force bool) *http.Request {
	target := "/api/v1/director/models/" + id
	if force {
		target += "?force=1"
	}
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("modelID", id)
	ctx := context.WithValue(withUserID(req.Context(), userID), chi.RouteCtxKey, rctx)
	return req.WithContext(ctx)
}

// 被画布引用的模型默认拦下；force=1 后删行、进孤儿管道（7 天宽限）、释放配额。
func TestDirectorModelDelete_ReferencedNeedsForce(t *testing.T) {
	conn := dbtest.Open(t)
	repo := directormodels.NewSQLiteRepository(conn)
	id, err := repo.Insert(directormodels.Model{UserID: 1, Name: "robot", FileName: "robot.fbx", ObjectKey: "u1/ab_robot.fbx", SizeBytes: 42, MimeType: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	pid, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, pid, `{"n":"media://u1/ab_robot.fbx"}`); err != nil {
		t.Fatal(err)
	}
	users := &directorFakeUserRepo{quota: 1 << 40}
	h := NewDirectorModelHandler(&directormodels.Service{Repo: repo}, &directorTrackingStorage{},
		library.NewSQLiteOrphansRepository(conn), users, directorFakeSigner{}, conn)
	idStr := strconv.FormatInt(id, 10)

	rec := httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(idStr, 1, false))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"referencedProjects":1`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(idStr, 1, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force 删除 code=%d body=%s", rec.Code, rec.Body)
	}
	if _, err := repo.GetByID(id); err == nil {
		t.Fatal("行应已删除")
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects WHERE object_key = 'u1/ab_robot.fbx'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("应进孤儿管道: n=%d err=%v", n, err)
	}
	if len(users.added) != 1 || users.added[0] != -42 {
		t.Fatalf("应释放配额: %v", users.added)
	}
}

func TestDirectorModelDelete_OtherUser404(t *testing.T) {
	conn := dbtest.Open(t)
	repo := directormodels.NewSQLiteRepository(conn)
	id, _ := repo.Insert(directormodels.Model{UserID: 1, Name: "m", FileName: "m.fbx", ObjectKey: "u1/m.fbx", SizeBytes: 1, MimeType: "x"})
	h := NewDirectorModelHandler(&directormodels.Service{Repo: repo}, &directorTrackingStorage{},
		library.NewSQLiteOrphansRepository(conn), &directorFakeUserRepo{}, directorFakeSigner{}, conn)
	rec := httptest.NewRecorder()
	h.Delete(rec, directorDeleteReq(strconv.FormatInt(id, 10), 2, true))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d", rec.Code)
	}
	if _, err := repo.GetByID(id); err != nil {
		t.Fatal("别人的模型不应被删")
	}
}
```

并在该文件 import 块加入 `"strconv"`。

Run: `cd server && go test ./internal/http/ -run DirectorModel 2>&1 | head`
Expected: 编译失败 `undefined: NewDirectorModelHandler`。

- [ ] **Step 3: 移植 handler**

```bash
cp /Users/james/github/huabu_studio/apps/api/internal/http/director_model_handlers.go server/internal/http/director_model_handlers.go
perl -pi -e 's/signer  \*MediaSigner/signer  directorSigner/; s/signer \*MediaSigner,/signer directorSigner,/; s/assets\.MediaProtocol \+ m\.ObjectKey/directorMediaProtocol + m.ObjectKey/' server/internal/http/director_model_handlers.go
grep -n 'MediaSigner\|MediaProtocol' server/internal/http/director_model_handlers.go
```

Expected: grep 只剩 `directorMediaProtocol` 一处。若 P1b 把 `storageUserRepo`、`readUploadForm` 改了名或签名，以 P1b 为准调整本文件的调用（不改 P1b 的文件）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd server && go vet ./internal/http/ && go test ./internal/http/ -run DirectorModel -v && go test ./...`
Expected: 5 个测试 PASS，全量通过。

- [ ] **Step 5: 提交**

```bash
git add server/internal/http/director_model_handlers.go server/internal/http/director_model_handlers_test.go
git commit -m "feat(server): 3D 导演台用户模型库上传、列表与删除接口"
```

---

### Task 8: 路由、装配、删除契约桩、端到端测试（第三批，最后执行）

**Files:**
- Modify: `server/internal/http/router.go`（P1b Task 12 重写后的版本）
- Modify: `server/cmd/sidecar/wire.go`（P1b Task 12 新建的装配文件；若 P1b 实际文件名不同，以 P1b 为准）
- Create: `server/cmd/sidecar/p1d_routes_test.go`
- Delete: `server/internal/canvasgen/p1dstub_enqueue.go`、`server/internal/generation/p1dstub.go`
- Modify（各一行）: `server/internal/http/asset_handlers.go`（P1b）、`server/internal/generation/service.go`（P1c）
- Modify: `server/go.mod`、`server/go.sum`（`go mod tidy`）

**Interfaces:**
- Consumes: Task 1–5、7 的全部构造函数；P1b 的 `Deps`、`NewRouter`、装配函数里的 `conn`、`settingsRepo`、`storage`（`assets.Storage`）、`mediaSigner`、`projectsRepo`、`assetsRepo`、`modelsRepo`、`usersRepo`、`orphansRepo`、根 `ctx`；P1c 的 `canvasGenSvc`、`generationSvc`
- Produces: `/api/v1` 下的 P1d 路由；`http.Deps` 新字段 `CanvasPostprocess`、`GenImagePreset`、`MultiAngle`、`LightControl`、`AICharacter`、`AICharacterImages`、`Seedance`、`DirectorModel`、`DirectorLibrary`

下文代码里的变量名取自参考仓 `cmd/server/main.go`；P1b/P1c 装配文件里的实际名字不同时按其替换，语义一一对应。

- [ ] **Step 1: 合并并解决预期冲突**

在集成分支上依次合并 P1b、P1c、P1d 第一二批。按“并发执行安排”里列出的五类冲突处理，然后：

```bash
cd server
git rm -q internal/canvasgen/p1dstub_enqueue.go internal/generation/p1dstub.go 2>/dev/null || true
grep -rln 'p1dstub' . && echo "仍有 p1dstub 残留" || echo "已清理"
go build ./... && go vet ./...
```

Expected: 打印“已清理”，build/vet 无输出。编译错误若来自 P1c 实际签名与桩不同（例如 `generation.Task` 字段名、`Enqueue` 返回值），改 `canvas_postprocess_handlers.go` 及其测试去适配 P1c，**不改 P1c 的代码**。

- [ ] **Step 2: 写端到端失败测试**

`server/cmd/sidecar/p1d_routes_test.go`（复用 `main_test.go` 的 `buildSidecar`、`startSidecar`、`testToken`；P1b 若改了这些辅助函数的签名，按其调整调用）：

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func p1dGet(t *testing.T, port, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%s: 不是 JSON: %s", path, raw)
	}
	return resp.StatusCode, body
}

// 新装的应用：P1d 的用户端接口全部可用，且“未配置”状态如实反映。
func TestP1dRoutesOnFreshInstall(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())

	for path, keys := range map[string][]string{
		"/api/v1/canvas/postprocess-config": {"subtitleRemoval", "upscaleModels", "whiteModelModel"},
		"/api/v1/canvas/gen-image-presets":  {"models", "presets"},
		"/api/v1/canvas/multi-angle":        {"models", "presets"},
		"/api/v1/canvas/light-control":      {"models", "presets"},
		"/api/v1/canvas/ai-characters":      {"items", "hasMore", "imageConfigured"},
		"/api/v1/seedance/config":           {"configured"},
		"/api/v1/director/library":          {"categories"},
		"/api/v1/director/models":           {"items"},
	} {
		code, body := p1dGet(t, port, path)
		if code != http.StatusOK {
			t.Fatalf("%s: code=%d body=%v", path, code, body)
		}
		for _, k := range keys {
			if _, ok := body[k]; !ok {
				t.Fatalf("%s: 缺字段 %s: %v", path, k, body)
			}
		}
	}
	if _, body := p1dGet(t, port, "/api/v1/seedance/config"); body["configured"] != false {
		t.Fatalf("新装应为未配置: %v", body)
	}
	if _, body := p1dGet(t, port, "/api/v1/canvas/ai-characters"); body["imageConfigured"] != false {
		t.Fatalf("新装形象图地址应为未配置: %v", body)
	}

	// 状态查询不依赖合规配置：未配置时也必须 200（前端每次打开画布都会批量查角标）。
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/api/v1/seedance/status", strings.NewReader(`{"urls":[]}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seedance/status code=%d", resp.StatusCode)
	}
}
```

Run: `cd server && go test ./cmd/sidecar/ -run TestP1dRoutesOnFreshInstall -v 2>&1 | tail`
Expected: FAIL（404 / 不是 JSON）。

- [ ] **Step 3: 路由与 Deps**

在 `server/internal/http/router.go` 的 `Deps` 结构体里加入：

```go
	// ── P1d ──
	CanvasPostprocess *CanvasPostprocessHandler
	GenImagePreset    *GenImagePresetHandler
	MultiAngle        *MultiAngleHandler
	LightControl      *LightControlHandler
	AICharacter       *AICharacterHandler
	// AICharacterImages 包在 /media/* 外面：角色形象图本地没有时先下载缓存（spec F7）。nil = 不包。
	AICharacterImages *aicharacters.ImageCache
	Seedance          *SeedanceHandler
	DirectorModel     *DirectorModelHandler
	DirectorLibrary   *DirectorLibraryHandler
```

（import 加 `"github.com/james/huabu_studio/apps/api/internal/aicharacters"`。）

在 P1b 注册 `/api/v1` 业务路由、已挂本地身份中间件的那个分组里加入（参考仓 `router.go:614-618,626-631,653-658,694-697`）：

```go
		// ── P1d：后处理、预设、多角度、打光 ──
		r.Get("/canvas/postprocess-config", d.CanvasPostprocess.GetConfig)
		r.Get("/canvas/gen-image-presets", d.GenImagePreset.UserGet)
		r.Get("/canvas/multi-angle", d.MultiAngle.UserGet)
		r.Get("/canvas/light-control", d.LightControl.UserGet)
		r.Post("/canvas-tasks/desub", d.CanvasPostprocess.Desub)
		r.Post("/canvas-tasks/postprocess-estimate", d.CanvasPostprocess.Estimate)
		r.Post("/canvas-tasks/upscale", d.CanvasPostprocess.Upscale)
		r.Post("/canvas-tasks/whitemodel", d.CanvasPostprocess.WhiteModel)
		// ── P1d：AI 角色库、Seedance 合规 ──
		r.Get("/canvas/ai-characters", d.AICharacter.List)
		r.Post("/seedance/verify", d.Seedance.Verify)
		r.Post("/seedance/status", d.Seedance.Status)
		r.Get("/seedance/config", d.Seedance.Config)
		// ── P1d：3D 导演台 ──
		r.Post("/director/models", d.DirectorModel.Upload)
		r.Get("/director/models", d.DirectorModel.List)
		r.Delete("/director/models/{modelID}", d.DirectorModel.Delete)
		r.Get("/director/library", d.DirectorLibrary.List)
```

找到 P1b 注册 `GET`/`HEAD /media/*` 的语句，用中间件包一层（`r.With` 返回带中间件的子路由）：

```go
	mediaMW := func(next nethttp.Handler) nethttp.Handler {
		if d.AICharacterImages == nil {
			return next
		}
		return d.AICharacterImages.Middleware(next)
	}
	// 把 P1b 原来的 r.Get("/media/*", X) / r.Head("/media/*", X) 改成：
	r.With(mediaMW).Get("/media/*", X)
	r.With(mediaMW).Head("/media/*", X)
```

`/admin/canvas/*` 路由不在这里注册（P4）。

- [ ] **Step 4: 装配**

在 `server/cmd/sidecar/wire.go` 构造 `Deps` 的函数里、P1b/P1c 的依赖都已建好之后加入（import：`aicharacters`、`directorlibrary`、`directormodels`、`library`、`seedance`、`assets`）：

```go
	// ── P1d ──
	aiCharactersRepo := aicharacters.NewSQLiteRepository(conn)
	charImages := aicharacters.NewImageCache(aiCharactersRepo, settingsRepo.Get, charImageStore{storage})

	// Seedance 合规走 newapi（spec X5）：地址、路径每次请求现读 settings。
	seedanceClient := seedance.NewClient(
		func() string { v, _ := settingsRepo.Get("base_domain"); return v },
		func() seedance.Paths { c, _ := seedance.LoadComplianceConfig(settingsRepo.Get); return c.Paths },
	)
	librarySeedanceSvc := library.NewSeedanceService(
		library.NewSQLiteSeedanceAssetsRepository(conn), library.NewSQLiteSeedanceGroupStore(conn),
		seedanceClient, settingsRepo, mediaSigner)
	// AI 角色图是平台预置、已过校验的公共资产：状态角标兜底 + 生成时自动以 asset://<id> 提交。
	librarySeedanceSvc.SetPublicAssets(aiCharactersRepo)
	generationSvc.WithRealPersonResolver(librarySeedanceSvc)
	// 未配置时 ReconcileAll 自身 no-op；随根 ctx 退出（P1b 的优雅退出会取消它）。
	go librarySeedanceSvc.RunWorker(ctx)

	deps.CanvasPostprocess = apphttp.NewCanvasPostprocessHandler(settingsRepo, canvasGenSvc, projectsRepo, assetsRepo, generationSvc, modelsRepo, mediaSigner)
	deps.GenImagePreset = apphttp.NewGenImagePresetHandler(settingsRepo)
	deps.MultiAngle = apphttp.NewMultiAngleHandler(settingsRepo)
	deps.LightControl = apphttp.NewLightControlHandler(settingsRepo)
	deps.AICharacter = apphttp.NewAICharacterHandler(aiCharactersRepo, mediaSigner, settingsRepo.Get)
	deps.AICharacterImages = charImages
	deps.Seedance = apphttp.NewSeedanceHandler(librarySeedanceSvc, storage, conn)
	deps.DirectorModel = apphttp.NewDirectorModelHandler(
		&directormodels.Service{Repo: directormodels.NewSQLiteRepository(conn)},
		storage, orphansRepo, usersRepo, mediaSigner, conn)
	deps.DirectorLibrary = apphttp.NewDirectorLibraryHandler(directorlibrary.NewSQLiteRepository(conn), mediaSigner)
```

在 `wire.go` 文件末尾加适配器：

```go
// charImageStore 把 assets.Storage 适配成 aicharacters.ObjectStore（角色形象图缓存用）。
type charImageStore struct{ s assets.Storage }

func (c charImageStore) Exists(ctx context.Context, key string) bool {
	rc, err := c.s.GetObject(ctx, key)
	if err != nil {
		return false
	}
	rc.Close()
	return true
}

func (c charImageStore) Put(ctx context.Context, key, contentType string, body []byte) error {
	_, err := c.s.PutObject(ctx, assets.PutObjectInput{Key: key, ContentType: contentType, Body: body})
	return err
}
```

- [ ] **Step 5: 提示词预设解析器与探测器（检查 P1c / P1b 是否已做）**

```bash
cd server
grep -n 'SetPromptPresetResolver(' cmd/sidecar/*.go || echo "缺预设解析器装配"
grep -n 'SetSettingsGetter(' cmd/sidecar/*.go || echo "缺 canvasGenSvc.SetSettingsGetter"
grep -n 'mediaprobe\.ProbeDurationMs\b' internal/generation/service.go internal/http/asset_handlers.go 2>/dev/null
grep -n 'probe' internal/http/asset_handlers.go | head
```

- 若打印“缺预设解析器装配”，在装配函数里 `canvasGenSvc` 建好后加入（参考仓 `cmd/server/main.go:229-260` 原样）：

```go
	canvasGenSvc.SetSettingsGetter(settingsRepo.Get)
	generationSvc.SetPromptPresetResolver(func(key string, params map[string]string) (string, bool) {
		// 多角度/打光保留 key 分流在绘图预设查表之前(各套配置独立，见对应 PresetKey 注释)。
		if key == canvasgen.MultiAnglePresetKey {
			maCfg, err := canvasgen.LoadMultiAngleConfig(settingsRepo.Get)
			if err != nil {
				return "", false
			}
			if text := maCfg.ComposeMultiAngleText(params); text != "" {
				return text, true
			}
			return "", false
		}
		if key == canvasgen.LightControlPresetKey {
			lcCfg, err := canvasgen.LoadLightControlConfig(settingsRepo.Get)
			if err != nil {
				return "", false
			}
			if text := lcCfg.ComposeLightControlText(params); text != "" {
				return text, true
			}
			return "", false
		}
		cfg, err := canvasgen.LoadGenImagePresetsConfig(settingsRepo.Get)
		if err != nil {
			return "", false
		}
		p, ok := cfg.Preset(key)
		if !ok {
			return "", false
		}
		return p.ComposePresetText(params), true
	})
```

- 若 `internal/generation/service.go` 用 `mediaprobe.ProbeDurationMs` 作 `durationProbe` 默认值，改成 `mediaprobe.ProbeDurationMsPortable`（桌面端没有 ffprobe，inputDuration 懒探测会全部失败）。
- 若 P1b 的 `AssetHandler` 有 `probe` 字段且默认 nil（P1b 计划写明“不移植 mediaprobe，probe 可选”），在 `NewAssetHandler` 里把默认值设为 `mediaprobe.ProbeDurationMsPortable`，让上传的视频记下 `duration_ms`（后处理就不用再下载探测）。

- [ ] **Step 6: 运行全部测试**

```bash
cd server
go mod tidy
go build ./... && go vet ./...
go test -count=1 ./...
go test -race ./internal/aicharacters/ ./internal/library/ ./internal/http/
GOOS=windows GOARCH=amd64 go build ./... && GOOS=darwin GOARCH=arm64 go build ./...
grep -rn 'p1dstub' . || echo "无残留"
```

Expected: 全部通过；`TestP1dRoutesOnFreshInstall` PASS；两个交叉编译成功；打印“无残留”。

- [ ] **Step 7: 提交**

```bash
git add -A server
git commit -m "feat(server): 接入 P1d 路由与装配，删除并发开发用的契约桩"
```

---

## P1d 完成标准

- `go test -count=1 ./...` 在不带任何 tag 的情况下通过；仓库里没有 `p1dstub` 文件。
- 新装应用：`/canvas/postprocess-config`、`/canvas/gen-image-presets`、`/canvas/multi-angle`、`/canvas/light-control`、`/canvas/ai-characters`、`/seedance/config`、`/director/library`、`/director/models` 全部 200；`/seedance/config` 为 `configured:false`；`/canvas/ai-characters` 为 `imageConfigured:false`。
- 设置 `seedance_compliance_config`、`base_domain`、`upstream_api_key` 后 `/seedance/config` 变为 `true`；本机素材提交合规校验返回 400 且文案含“公网”。
- 设置 `ai_character_image_base_url` 并导入角色数据（P2b）后，角色形象图第一次加载从远端下载、之后离线可显示。
- 预设 / 多角度 / 打光的用户端响应里没有 `prompt`、`template` 字段。
- 迁移 `0010`–`0012` 在空库上执行成功，`PRAGMA foreign_key_check` 无输出。

## Self-Review 记录

- **Spec 覆盖：** F2/F3 的后处理节点与图片工具条（打光、多角度、绘图预设）→ Task 1、5；F7 → Task 2（缓存）+ Task 3（公共资产兜底）+ Task 8（asset:// 替换装配）；X5 → Task 3；M4/M5 → Task 1（settings 读取、正文服务端拼接、用户端脱敏）；M6（前端写死的模型 ID）属 P3a，后端无改动；S12 → Task 3 回环拒绝 + Task 5 已知限制；SEC4 → 给 P8 的约定；X3 → Task 5 注释与纯 Go 时长。
- **占位扫描：** 无 TBD/TODO；所有新代码给出完整内容，移植文件给出逐条替换。Task 8 依赖 P1b/P1c 尚未定稿的变量名，已写明“按参考仓名字一一对应替换”。
- **类型一致：** `directorSigner`、`directorMediaProtocol`（Task 4 定义，Task 7 使用）；`seedance.Paths`/`ComplianceConfig.Paths`（Task 3 定义，Task 8 `c.Paths`）；`aicharacters.ObjectStore`（Task 2 定义，Task 8 `charImageStore` 实现）；`mediaprobe.ProbeDurationMsPortable`（Task 5 定义，Task 8 使用）；`library.NewSQLiteSeedanceGroupStore`（Task 3 定义，Task 8 使用）。
- **Review Focus：** 5 条均已在对应任务加测试。
