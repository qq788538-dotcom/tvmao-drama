# P1b 本地存储、媒体与画布 HTTP 接口 Implementation Plan

> **实施后注（2026-10-06，正文未改）**
> - T12 修复轮 9a456ec 后：媒体 key 中的 `%`/`#` 往返、素材库上传加大小上限、关闭先停 worker 再关库、数据目录 0700、EOF 优雅退出；SEC4 改为"在访达中显示"由 sidecar 执行（`POST /api/v1/media/reveal`）。整合后 sidecar 还补了写入 `SHUTDOWN` 行的握手。迁移文件 `0002_library.sql`。出处：p1b 账本、integration-report.md。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 sidecar 成为一个可用的单用户画布后端：媒体文件存在本机数据目录、以 `media://` 落库并在出口换成 `http://127.0.0.1:<端口>/media/…` 本地 URL，项目、画布、上传、素材库、孤儿回收和“在访达中显示”这些 `/api/v1` 接口在 SQLite 上跑通。

**Architecture:** 新增 `assets.LocalStorage`（实现参考仓 `assets.Storage` 的全部 9 个方法），其余包和 handler 从参考仓复制后做最小改动：MySQL 方言改 SQLite，Redis 编辑锁换成恒放行的本地实现，JWT 鉴权换成恒定注入本地用户的中间件，`/admin/gc` 的清扫逻辑抽成 `library.OrphanSweeper` 并由 sidecar 定时运行。媒体文件由 sidecar 的 `GET /media/*` 提供（支持 Range），URL 里带的是**只读媒体专用 token**，不是接口 token。全部业务路由挂在 P0 的 localauth（token + Origin）之内，最后由一个装配任务接进 `cmd/sidecar/main.go`，同时补齐 P0 遗留的优雅退出、数据目录权限等事项。

**Tech Stack:** Go 1.26 toolchain（module `github.com/james/huabu_studio/apps/api`）、`modernc.org/sqlite` v1.60.1、`github.com/go-chi/chi/v5` v5.2.1、新增 `golang.org/x/sync` v0.20.0（仅 errgroup，项目副本用）；Tauri v2 壳（Rust，`tauri-plugin-shell` 2.4.0）。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（D2、D3、X1–X4、M2–M4、M10、S1–S12、SEC1–SEC4）；事实依据见 `docs/superpowers/plans/p1-research.md` 第 3、4 节；上一份子计划 `docs/superpowers/plans/2026-10-06-p1a-sqlite-data-layer.md`（本计划沿用其约定）；P0 遗留见 `docs/superpowers/plans/p0-carryover.md`。

**参考仓：** `/Users/james/github/huabu_studio`，基准 commit `fea92ab`。下文 `REF=/Users/james/github/huabu_studio/apps/api/internal`。参考仓 HEAD（`4d77979`）相对 `fea92ab` 只改了 `http/auth_handlers.go`、`http/auth_handlers_test.go`、`http/router.go` 三个文件，本计划不复制这三个文件，所以直接从工作区 `cp` 与从 `fea92ab` 取出的内容一致。

## 本计划的范围（与 p1-research 第 5 节的差异说明）

P1b 交付：本地文件存储与媒体服务、MediaSigner、资产上传、素材库（目录/条目/存入/应用到画布）、孤儿回收与自动定时清扫、项目接口（列表/新建/读取/改名/删除/快照/图片/封面/副本）与后台清理 worker、画布局部 mutation 与跨项目粘贴登记、`GET /api/v1/me`、“在访达/资源管理器中显示”、本地身份中间件、`cmd/sidecar` 装配，以及 P0 遗留中标为 P1 的全部事项。

相对 p1-research 的原始切分，做了三处调整：
1. **`/me`、本地身份中间件、画布 mutation 接口、项目接口挪进 P1b**（原分在 P1a/P1d）：P1a 实际只做了数据层，没碰 HTTP；这些接口与存储层互相依赖（快照出口签名、删除项目要回收文件），拆开会让 P1b 交付物无法端到端验证。
2. **`canvas_changed` 广播（Agent Hub 的最小子集）留给 P1c**：P1b 里没有任何服务端主动改画布的路径——画布写入全部来自前端自己的 mutation 请求，桌面端又是单窗口（spec S11），广播只会发回发起者本人并被 source mark 跳过。真正需要它的是 P1c 的生成结果回写（`canvasgen.writeNode` → `NotifyCanvasChanged`），届时连同替换 P0 的 `/api/v1/agent/ws` 回显路由、`agent.Frame` 帧格式一起做，并有回写测试兜底。P1b 的 `CanvasMutationHandler` 保留 `SetChangeNotifier` 接口位，装配时不注入（nil = 不广播，参考仓本来就支持）。
3. **P0 遗留里标为 P1 的条目全部放进 P1b**，P1d 不再承接：P1b 第一次引入 SQLite 文件和后台 worker（数据目录权限、优雅退出变得必要），且这些改动都落在 P1b 本来就要改的 `localauth`、`cmd/sidecar/main.go`、`src-tauri/src/lib.rs` 上。

**不在 P1b：** upstream、generation、canvasgen、任务历史、`/canvas-tasks*`、billing 空实现的接线（P1c）；后处理、预设、多角度、打光、AI 角色、Seedance、导演台、脚本节点（P1d / P6 / P7）；Agent（P5）；设置页（P4）；内置数据导入（P2）。**不实现任何把本地媒体上传到公网的逻辑**（spec S12 待定）：`assets.Storage` 接口、`MediaSigner` 的出入口、`ExtractKey` 认回本地 URL 这几个接口位原样保留，S12 定下来后在它们上面加。

## Global Constraints

- Go module 路径保持 `github.com/james/huabu_studio/apps/api`；包路径与参考仓 `apps/api/internal/*` 一致（`assets`、`library`、`users`、`workflows`、`canvasowner`、`http`），新增包 `internal/reveal`。
- 仓库类型统一改名：`MySQLRepository` → `SQLiteRepository`、`NewMySQLRepository` → `NewSQLiteRepository`；带前缀的同理（`MySQLFoldersRepository` → `SQLiteFoldersRepository`、`NewMySQLOrphansRepository` → `NewSQLiteOrphansRepository` 等）。其余类型名、方法名、方法签名、导出常量、JSON tag 与参考仓逐字一致，除非本计划明确写了改动。
- SQLite 约定沿用 P1a：`db.Open` / `db.Migrate` / `dbtest.Open`；时间列声明 `DATETIME`，存 UTC 文本，SQL 里用 `CURRENT_TIMESTAMP`，Go 传时间参数必须经 `db.Time(t)`；JSON 参数传 `string` 不传 `[]byte`；布尔存 `INTEGER` 0/1；`ON UPDATE CURRENT_TIMESTAMP` 用 `AFTER UPDATE … WHEN NEW.x = OLD.x` 触发器；事务一律 `_txlock=immediate`（不写 `FOR UPDATE`）；**事务内只能用 `tx` 执行语句，不得再用 `*sql.DB` 写库**（另一条连接会等写锁直到 `busy_timeout` 后报 `SQLITE_BUSY`）。
- 迁移：`0001_init.sql` 冻结不改。本计划只新增 **`server/internal/db/migrations/0002_library.sql`**，由 Task 2 独占创建；其他任务不得新增或修改迁移文件。
- 本地用户固定 `users.id = 1`、`role = 'admin'`，常量 `http.LocalUserID`；不写 actor（团队分支全部不可达）。
- 数据目录布局（spec S1）：`<data-dir>/data.db`、`<data-dir>/media/<object_key>`。目录权限仅当前用户可读写（Unix `0700`）。spec S8：不生成缩略图，`SignedThumbURL` 返回原图 URL。
- 媒体 URL 形态固定为 `http://127.0.0.1:<端口>/media/<逐段 PathEscape 的 key>?token=<媒体 token>`。媒体 token 由 sidecar 每次启动用 `crypto/rand` 生成 32 字节十六进制，只放行 `GET`/`HEAD /media/*`；接口 token 仍由壳经 stdin 传入（spec SEC3）。
- 落库的媒体引用只允许 canonical 形态 `media://<key>`；本地 URL 只出现在 HTTP 响应里。
- 代码注释用中文，风格与参考仓一致；从参考仓复制的注释若描述的是 MySQL / Redis / TOS 行为且已不成立，就地改写。
- 共享文件归属（并发安全的前提，见“并发执行安排”）：`server/go.mod`、`server/go.sum` 只有 Task 7（`go get golang.org/x/sync`）和 Task 12（`go mod tidy`）能改；`server/internal/http/router.go` 只有 Task 5（删掉重复的 `writeJSON`）和 Task 12（重写路由）能改；`server/cmd/sidecar/*` 只有 Task 12 能改；`server/internal/localauth/*` 与 `src-tauri/**` 只有 Task 3 能改。
- 同一个 Go 包（`internal/http`）由多个任务并发加文件：每个任务只新建自己列出的文件；新写的测试辅助函数/类型名必须带本任务的领域前缀（`media…`、`project…`、`canvas…`、`library…`），避免合并后重名。
- 不改动 `web/**`、`docs/specs/**`。

## Review Focus

1. **用户上传的文件名含 Windows 非法字符或超长**（如 `镜头:1?.png`、200 个汉字的文件名）→ 上传必须成功，回读必须拿到同一份字节。（Task 1 `TestNewKeySanitizesWindowsIllegalNames`、`TestNewKeyTruncatesLongNamesKeepingExtension`；Task 8 `TestMediaUploadedFileWithIllegalNameIsServable`）
2. **上一次启动发出的媒体 URL（端口和 token 都已变）再次传回 sidecar**（P1c 生成任务入库的是签名后的 URL，重启续跑要认回 key；前端也可能带着旧 URL 提交）→ 必须认回 `media://<key>` 落库，不能把旧 URL 原样写进快照。（Task 1 `TestExtractKeyAcceptsAnyLoopbackPortAndMediaScheme`；Task 10 `TestCanvasDBMutationPersistsCanonicalMediaFromAnyPort`）
3. **视频拖动进度条、mediabunny 抽帧发 Range / HEAD 请求，且生成产物的 key 没有扩展名** → 必须 206 + 正确的 `Content-Type`（先查 `assets` / `library_items` 的 `mime_type`，查不到再按扩展名、再嗅探）。（Task 8 `TestMediaServeRangeAndHead`、`TestMediaServeContentTypeForExtensionlessKeys`）
4. **构造的媒体路径想读数据目录里的其他文件**（`/media/../data.db`、`%2e%2e`、`media://../../`）→ 一律 404/400，绝不返回 `media/` 之外的文件（`data.db` 里有 API Key）。（Task 1 `TestPathRejectsTraversalAndIllegalKeys`；Task 8 `TestMediaServeRejectsTraversalAndMissing`；Task 12 `TestE2EMediaTraversalIsRejected`）
5. **媒体 URL（会出现在 DOM、日志、Agent 提示词里）被拿去调接口** → 媒体 token 只能读 `/media/*`，调 `/api/v1/*` 必须 401；接口 token 仍可读媒体。（Task 3 `TestMediaTokenOnlyOpensMediaReads`；Task 12 `TestE2EProjectUploadCanvasMediaLifecycle` 第 5 步）

另：**删掉一个项目时，被另一个项目引用（跨项目粘贴、素材库应用）的文件不能被回收**——Task 9 `TestProjectDeletedIsPurgedAndSharedObjectsSurvive`、Task 11 `TestLibraryItemDeleteKeepsObjectUsedOnCanvas` 覆盖。

## 并发执行安排

每一批的任务互不修改同一文件，可以在各自的 git worktree 里并发执行；下一批的 worktree 必须从“上一批全部合并后”的集成分支切出。

| 批次 | 任务 | 依赖 |
|---|---|---|
| 第一批 | **Task 1**（assets 本地存储 + 仓库）、**Task 2**（迁移 0002 + library 仓库 + 定时清扫）、**Task 3**（localauth 加固 + Tauri 壳） | 只依赖 P1a |
| 第二批 | **Task 5**（http 基础件：helpers、context、身份中间件、MediaSigner、缩略图接口、共享测试替身）、**Task 6**（users 存储用量仓库）、**Task 7**（项目副本 duplicator） | Task 5、7 依赖 Task 1；Task 6 依赖 Task 2 |
| 第三批 | **Task 8**（上传、媒体服务、在访达中显示、/me）、**Task 9**（项目接口 + 清理 worker）、**Task 10**（canvasowner 本地实现 + 画布 mutation + 跨项目粘贴）、**Task 11**（素材库接口 + /admin/gc） | 全部依赖 Task 5；另：8←1、6；9←1、2、6、7；10←1；11←1、2、6 |
| 第四批 | **Task 12**（路由与 sidecar 装配、优雅退出、端到端测试） | 全部 |

（没有 Task 4：原拟的 lifecycle 包经核对 P1b 用不到——项目清理与孤儿清扫都是 `context` 驱动的 goroutine，退出时 `db.Close` 会等在飞的语句；`lifecycle.InstanceID` 等到 P1c 的任务租约才需要，届时随 generation 一起复制。为免改动大量交叉引用，编号保留空缺。）

文件归属一览（同批任务之间零重叠）：

| 任务 | 独占的路径 |
|---|---|
| 1 | `server/internal/assets/**` |
| 2 | `server/internal/db/migrations/0002_library.sql`、`server/internal/library/**` |
| 3 | `server/internal/localauth/**`、`src-tauri/src/lib.rs` |
| 5 | `server/internal/http/{helpers,context,middleware,media_signer,media_thumb_handlers,storage_user_repo}.go` 及对应 `_test.go`、`server/internal/http/testfakes_test.go`；`router.go` 只删 `writeJSON` |
| 6 | `server/internal/users/**` |
| 7 | `server/internal/workflows/**`、`server/go.mod`、`server/go.sum` |
| 8 | `server/internal/http/{asset_handlers,media_handlers,me_handlers}.go` 及测试、`server/internal/reveal/**` |
| 9 | `server/internal/http/{project_handlers,project_cleanup}.go`、`project_snapshot_test.go`、`project_cover_test.go`、`project_handlers_db_test.go` |
| 10 | `server/internal/canvasowner/**`、`server/internal/http/{canvas_mutation_handlers,canvas_asset_import_handlers}.go`、`project_handlers_test.go`、`canvas_*_test.go` |
| 11 | `server/internal/http/{library_folder_handlers,library_item_handlers,library_bridge_handlers,admin_gc_handlers}.go` 及测试 |
| 12 | `server/internal/http/router.go`、`router_test.go`、`server/cmd/sidecar/**`、`server/go.mod`、`server/go.sum` |

---

## 移植事实清单（写计划时从参考仓核实，执行时以此为准）

### 接口与使用方

| 接口（定义处） | 方法 | P1b 的使用方 | 桌面端实现 |
|---|---|---|---|
| `assets.Storage`（`assets/storage.go:37-59`） | `PutObject`、`DeleteObject`、`SignedURL`、`SignedThumbURL`、`ExtractKey`、`GetObject`、`StaticKey`、`NewKey`、`CopyObject` | MediaSigner（ExtractKey、SignedURL、SignedThumbURL）；AssetHandler（NewKey、PutObject）；LibraryItemHandler（PutObject、DeleteObject）；AssetDuplicator（ExtractKey、CopyObject）；OrphanSweeper、DeleteNowQueue（DeleteObject）。P1c：persistSlot（StaticKey、PutObject）、历史 ZIP（GetObject） | `assets.LocalStorage`（Task 1） |
| `assets.Repository`（`assets/repository.go:25-58`） | 12 个方法 | AssetHandler（FindByUserProjectHash、Insert、ListForUser）；ProjectHandler / ProjectCleaner（ListForProject）；LibraryBridge、CanvasAssetImport（Insert）；AssetDuplicator（ListByObjectKey、FindByUserProjectHash、Insert）。P1c：FindByTaskSlot、InsertSlotTx、ListForTask、DeleteForTask；P1d：Get/SetDurationMsByObjectKey | `assets.SQLiteRepository`（Task 1） |
| `storageUserRepo`（`http/asset_handlers.go:29-34`，Task 5 移到 `storage_user_repo.go`） | `AddStorage`、`StorageUsage` | AssetHandler、ProjectHandler、ProjectCleaner、LibraryFolderHandler、LibraryItemHandler | `users.SQLiteRepository`（Task 6） |
| `userStorageAdjuster`（`workflows/duplicator.go`） | `AddStorage` | AssetDuplicator | `users.SQLiteRepository` |
| `cleanupQueue`（`workflows/duplicator.go`） | `EnqueueObjectKey` | AssetDuplicator 失败回滚 | `workflows.DeleteNowQueue`（Task 7，替代参考仓写 `cleanup_jobs` 表 + worker） |
| `library.OrphansRepository`（`library/orphans_repo.go`） | `Mark`、`ListReady`、`Delete` | ProjectCleaner、LibraryFolder/Item handler、OrphanSweeper | `library.SQLiteOrphansRepository`（Task 2） |
| `library.FoldersRepository` / `ItemsRepository` | 7 / 7 个方法 | 素材库三个 handler | `library.SQLite*Repository`（Task 2） |
| `canvasOwner`（`http/canvas_mutation_handlers.go:17-22`） | `Claim`、`Heartbeat`、`Require`、`Probe` | CanvasMutationHandler | `canvasowner.Local`（Task 10，恒放行，替代 Redis） |
| `canvasChangeNotifier`（同上 :27-29） | `NotifyCanvasChanged` | CanvasMutationHandler（`SetChangeNotifier`） | P1b 不注入；P1c 的 Agent Hub 实现 |
| `taskProjectCleanup`（`http/project_handlers.go:18-20`） | `DeleteForProject` | ProjectHandler、ProjectCleaner | P1b 传 nil（两处都判 nil）；P1c 传 generation 仓库 |
| `canvasAssetRefStore`（`http/canvas_asset_import_handlers.go`） | `ProjectOwner`、`CanonicalByKey`、`ProjectHasKey` | CanvasAssetImportHandler | `http.SQLCanvasAssetRefStore`（原样） |

**MediaSigner 出入口（P1b 范围内的全部）：** 出口（`media://` → 本地 URL）：`AssetHandler.Upload/List`、`ProjectHandler.List`（封面 `SignThumb(…,768)`）、`ProjectHandler.GetSnapshot`（`SignJSON`，整快照唯一出口）、`ListImages`/`SetCover`（`SignThumb`）、`CanvasMutationHandler` 广播摘要 `signOpSummary`、素材库 item 的 List/Upload/Update 与 bridge 三个响应、`MediaThumbs`。入口（本地 URL → `media://`）：`CanvasMutationHandler.canonicalize`（UpsertNode/PatchNode/UpsertConnection，画布持久化前唯一入口）；`ObjectKey`：`LibraryBridge.SaveFromURL`、`MediaHandler.Reveal`。

### MySQL 方言与改写（P1b 移植的代码里的全部）

| 位置 | MySQL | SQLite 改写 | 任务 |
|---|---|---|---|
| `assets/repository.go` `InsertSlotTx` | `ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)` + `RowsAffected()==1` 判新插入 | `ON CONFLICT (source_task_id, output_index) DO NOTHING`；`RowsAffected()==1` 取 `LastInsertId`，否则在同一 `tx` 里按槽位 `SELECT id` 回查，`inserted=false` | 1 |
| `library/orphans_repo.go` `Mark` | `ON DUPLICATE KEY UPDATE marked_at = CURRENT_TIMESTAMP` | `ON CONFLICT (object_key) DO UPDATE SET marked_at = CURRENT_TIMESTAMP` | 2 |
| `library/orphans_repo.go` `ListReady` | `marked_at < ?` 传 `time.Time` | 传 `db.Time(cutoff)` | 2 |
| `library/folders_repo.go` `Rename`/`Move`、`items_repo.go` `Update` | `RowsAffected()==0` 判不存在（MySQL 值不变也返回 0，改成同名会误报 404） | 不改代码；SQLite 返回匹配行数，同名改名不再误报（测试钉住） | 2 |
| `library/folders_repo.go` `List` | `ORDER BY f.parent_id, f.name`（utf8mb4_unicode_ci，大小写不敏感） | 不改；SQLite 为 BINARY 排序，NULL 同样排最前。前端自己建树，不依赖名字顺序 | 2 |
| `library/items_repo.go` `List` | `name LIKE ?`（ci 排序规则下大小写不敏感） | 不改；SQLite 的 LIKE 对 ASCII 默认大小写不敏感 | 2 |
| `users/repository.go` `GetByID` | 选 `kind`、`password_changed_at` 列 | 桌面端 users 表没有这两列（P1a 0001），新写的 `GetByID` 只选存在的列 | 6 |
| `users/repository.go` `ReconcileAllStorage`、`ComputeStorageUsedBatch` | `UPDATE users u SET … WHERE a.user_id = u.id` | 不移植（每日对账与管理端批量统计，桌面端配额 1 PiB、`/me` 实时计算） | 6 |
| `IsStillReferenced`、`ProjectHasKey` 的 `SELECT EXISTS(…)` 扫进 `bool` | MySQL 返回 0/1 | 不改；modernc 返回 int64，`database/sql` 可转 bool（测试覆盖） | 2、10 |
| 各处 `IFNULL`、`ORDER BY id DESC LIMIT ?`、`COALESCE` | — | SQLite 原生支持，不改 | — |

### 依赖已删除包的代码与改法

| 被移植文件 | 依赖 | 改法 |
|---|---|---|
| `http/middleware.go` | `jwt`、`auth`、`teams`、`users`、`billing`（AuthMiddleware、RequireAdmin、CORS） | 不复制；只把 `ProjectFromURL` 原样搬进新的 `middleware.go`，另写 `LocalIdentity`。`RequireAdmin` 不需要（本地用户恒为 admin），CORS 由 localauth 负责 |
| `http/auth_handlers.go` `Me` | `users.Repository`、`invitecodes`、JWT | 不复制；新写 `MeHandler`，响应字段与参考仓 `Me` 逐字一致 |
| `http/media_signer.go` | `hashicorp/golang-lru/v2/expirable` | 去掉缓存（理由见 Task 5），公开 API 不变 |
| `http/asset_handlers.go` | `mediaprobe`（调系统 `ffprobe`） | 不移植 mediaprobe；`probe` 变成可选，探测失败只是不记时长、不再拒绝上传（spec X3 不计费；用户机器上没有 ffprobe） |
| `http/project_handlers.go` | `workflows.AssetDuplicator`、`taskProjectCleanup`（generation） | 文件原样复制；duplicator 由 Task 7 提供，taskCleanup 装配时传 nil |
| `workflows/duplicator.go` | `cleanup.go` 的 `cleanup_jobs` 表 + `CleanupWorker` | `duplicator.go` 原样复制；不复制 `cleanup.go`，新写 `DeleteNowQueue` 直接删本地文件 |
| `http/canvas_mutation_handlers.go` | `canvasowner`（Redis 锁） | 文件原样复制；`canvasowner` 包重写为恒放行的 `Local`，保留 `ClaimResult`、`ProbeResult`、`ErrOwnerLost`、`ErrOwnerExpired`、`SourceMark` |
| `http/admin_gc_handlers.go` | 清扫逻辑写在 handler 里 | 重写为调用 `library.OrphanSweeper`，响应字段不变 |
| `http/router.go` | `cliinstall`、`flowcliinstall`、`lifecycle`（healthz）、`static` 及 80 多个 handler | 不复制；Task 12 只注册 P1b 的路由 |
| `users/repository.go` | `billing`（注册赠分建桶） | 不复制；新写只含存储用量方法的 `users` 包 |
| `http/library_item_handlers.go` | `github.com/google/uuid` | 原样保留（v1.6.0 已在 go.mod 的 indirect 依赖里，Task 12 的 `go mod tidy` 会把它挪进直接依赖） |

### 参考仓测试的移植

| 参考仓测试 | 处理 | 任务 |
|---|---|---|
| `assets/put_input_test.go` | 原样复制 | 1 |
| `assets/minio_test.go` | 不移植（MinIO） | — |
| `workflows/duplicator_test.go` | 原样复制 | 7 |
| `canvasowner/owner_test.go` | 只保留 `TestClaimResultJSONTags`、`TestSourceMarkTakesFirstEightHexOfLastSegment`，其余（Redis / miniredis）删除，另写 Local 的测试 | 10 |
| `users/storage_quota_db_test.go`、`repository_grants_db_test.go` 等 | 不移植（会员配额、额度授予），另写存储用量测试 | 6 |
| `http/media_signer_test.go` | 改写（去掉缓存断言，`stubStorage` 移到共享替身文件） | 5 |
| `http/media_thumb_handlers_test.go` | 原样复制 | 5 |
| `http/asset_handlers_test.go` | 移植：替身移到共享文件；两条“探测失败拒绝上传”改为“探测失败照常上传”；新增 413 | 8 |
| `http/project_handlers_test.go` | 拆分：`TestProjectHandlerGetSnapshotIncludesVersion` → Task 9 的 `project_snapshot_test.go`；`fakeProjectRepository` 与两个请求构造函数 → Task 5 共享替身；其余 7 个画布 mutation 测试与 `fakeCanvasOwner` 留在 Task 10 | 5、9、10 |
| `http/project_cover_test.go` | 原样复制 | 9 |
| `http/canvas_concurrent_test.go`、`canvas_op_signing_test.go`、`canvas_observe_test.go` | 原样复制（团队身份分支的测试保留：handler 代码原样保留了 actor 分支，测试保证它没被改坏） | 10 |
| `http/canvas_asset_import_handlers_test.go` | 原样复制 | 10 |
| `http/library_folder_handlers_test.go` | 原样复制（`withTestUserID` 来自共享替身） | 11 |
| `http/router_test.go`（healthz/livez）、`middleware_*_test.go`、`auth_*_test.go` 等 | 不移植（功能已删） | — |

---

## 文件结构

```
server/internal/
  db/migrations/0002_library.sql        Task 2：library_folders、library_items、orphan_objects
  assets/
    storage.go, transform.go            Task 1：原样复制
    repository.go                       Task 1：移植（SQLiteRepository）
    local.go                            Task 1：新写 LocalStorage
    put_input_test.go                   Task 1：原样复制
    local_test.go, repository_test.go   Task 1：新写
  library/
    folders_repo.go, items_repo.go,
    orphans_repo.go, reclaim.go         Task 2：移植
    sweeper.go                          Task 2：新写（自 admin_gc_handlers 抽出 + 定时）
    repo_test.go, sweeper_test.go       Task 2：新写
  users/
    repository.go, repository_test.go   Task 6：新写（存储用量子集）
  workflows/
    duplicator.go, duplicator_test.go   Task 7：原样复制
    cleanup_local.go, cleanup_local_test.go  Task 7：新写 DeleteNowQueue
  canvasowner/
    owner.go, owner_test.go             Task 10：重写为本地恒放行实现
  reveal/
    reveal.go, reveal_darwin.go, reveal_windows.go, reveal_other.go, reveal_test.go   Task 8
  localauth/middleware.go(+_test)       Task 3：Bearer 前缀、媒体 token、Max-Age
  http/
    helpers.go, context.go              Task 5：原样复制
    middleware.go                       Task 5：ProjectFromURL + LocalIdentity
    storage_user_repo.go                Task 5：storageUserRepo 接口
    media_signer.go, media_thumb_handlers.go   Task 5
    testfakes_test.go                   Task 5：跨任务共享的测试替身
    asset_handlers.go                   Task 8：移植
    media_handlers.go                   Task 8：新写（/media/* 与 reveal、SQLMediaMimeStore）
    me_handlers.go                      Task 8：新写
    project_handlers.go, project_cleanup.go    Task 9：原样复制
    canvas_mutation_handlers.go, canvas_asset_import_handlers.go   Task 10：原样复制
    library_folder_handlers.go, library_item_handlers.go, library_bridge_handlers.go   Task 11：原样复制
    admin_gc_handlers.go                Task 11：重写为调用 OrphanSweeper
    router.go                           Task 12：重写
  （diag.go 保留，P3 移进设置页）
server/cmd/sidecar/
  main.go                               Task 12：重写（装配、优雅退出、权限）
  wire.go, wire_test.go                 Task 12：新写（依赖装配 + 端到端测试）
src-tauri/src/lib.rs                    Task 3：0700、debug 才放行开发源、退出握手
```

---

### Task 1: assets——本地文件存储与 assets 仓库

**Files:**
- Create（原样复制）: `server/internal/assets/storage.go`、`server/internal/assets/transform.go`、`server/internal/assets/put_input_test.go`
- Create（移植）: `server/internal/assets/repository.go`
- Create（新写）: `server/internal/assets/local.go`
- Test: `server/internal/assets/local_test.go`、`server/internal/assets/repository_test.go`

**Interfaces:**
- Consumes: `dbtest.Open(t)`、`db.Time`（P1a）
- Produces（Task 5/7/8/9/10/11/12 依赖）：
  - 原样：`assets.Storage`（9 个方法）、`assets.PutObjectInput{Key, ContentType string; Body []byte; Reader io.Reader; Size int64}` 及 `Content()`、`assets.StoredObject{URL string; Size int64}`、`assets.WalkStrings`、`assets.TransformJSONRaw(raw json.RawMessage, fn func(string) string) json.RawMessage`、`assets.TransformJSONBytes`。
  - `assets.MediaProtocol = "media://"`（参考仓定义在 minio.go，桌面端改定义在 local.go）
  - `assets.ErrInvalidKey`、`assets.ValidKey(key string) bool`
  - `assets.LocalConfig{Root, BaseURL, Token string}`、`assets.NewLocalStorage(cfg LocalConfig) (*LocalStorage, error)`、`(*LocalStorage).Root() string`、`(*LocalStorage).Path(key string) (string, error)`，以及 `Storage` 的 9 个方法。
  - `assets.AssetRecord`（字段与 JSON tag 同参考仓）、`assets.Repository`（12 个方法，签名同参考仓）、`assets.SQLiteRepository`、`assets.NewSQLiteRepository(db *sql.DB) *SQLiteRepository`、`(*SQLiteRepository).FindByID(id int64) (*AssetRecord, error)`（未命中返回 `nil, nil`）。

`LocalStorage` 的行为约定（与参考仓 MinIO 实现逐条对照）：

| 方法 | 行为 |
|---|---|
| `PutObject` | 写 `<root>/<key>`：先写同目录临时文件 `.tmp-*` 再 `rename`，并发读者永远看不到半截文件，`StaticKey` 重放覆盖写同一对象；返回 `media://<key>` 和实际写入字节数 |
| `DeleteObject` | 删除文件；不存在视为成功（同 S3 语义） |
| `SignedURL` | `<BaseURL>/media/<逐段 PathEscape 的 key>?token=<Token>`；不过期，`expiry` 参数忽略 |
| `SignedThumbURL` | 等于 `SignedURL`（spec S8 返回原图，同参考仓 MinIO 模式） |
| `ExtractKey` | 认 `media://<key>`（去 query、PathUnescape）和 `http://127.0.0.1:<任意端口>/media/<key>`；**端口不限**是为了认回上一次启动发出的 URL；解出的 key 不合法（`..` 等）一律不认 |
| `GetObject` | 打开文件；不存在返回包含 `fs.ErrNotExist` 的错误 |
| `StaticKey` | `u<uid>/<净化后的文件名>`（参考仓同布局，无 prefix） |
| `NewKey` | `u<uid>/<年>/<月，不补零>/<净化后的文件名>`（spec S4 沿用参考仓布局） |
| `CopyObject` | 先试硬链接（同盘瞬间完成、不占双份空间），失败（跨卷、FAT/exFAT）退回整文件复制；源被覆盖写（rename 换 inode）或删除不影响副本 |

文件名净化（`NewKey`/`StaticKey` 内部）：`/ \ : * ? " < > |`、控制字符、非法 UTF-8 替换成 `_`；超过 150 字节时截断词干、保留扩展名且不切断 UTF-8 字符；去掉末尾的 `.` 和空格（Windows 会静默截掉）；结果为空时用 `file`。`ValidKey` 拒绝：空串、超过 1024 字节、非法 UTF-8、以 `/` 开头、含控制字符或 `\ : * ? " < > |`、任一段为空 / `.` / `..` / 以 `.` 或空格结尾。

- [ ] **Step 1: 复制原样文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/assets
cp $REF/assets/storage.go $REF/assets/transform.go $REF/assets/put_input_test.go $REF/assets/repository.go server/internal/assets/
```

- [ ] **Step 2: 写失败的测试**

`server/internal/assets/local_test.go`：

```go
package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var _ Storage = (*LocalStorage)(nil)

// 根目录放在带空格的「Application Support」下，顺带覆盖 macOS 的真实数据目录形态。
func newLocal(t *testing.T) *LocalStorage {
	t.Helper()
	s, err := NewLocalStorage(LocalConfig{
		Root:    filepath.Join(t.TempDir(), "Application Support", "media"),
		BaseURL: "http://127.0.0.1:5555",
		Token:   "mediatoken",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readKey(t *testing.T, s *LocalStorage, key string) string {
	t.Helper()
	rc, err := s.GetObject(context.Background(), key)
	if err != nil {
		t.Fatalf("GetObject(%q): %v", key, err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestNewLocalStorageCreatesPrivateRoot(t *testing.T) {
	if _, err := NewLocalStorage(LocalConfig{}); err == nil {
		t.Fatal("缺 Root 应报错")
	}
	s := newLocal(t)
	st, err := os.Stat(s.Root())
	if err != nil || !st.IsDir() {
		t.Fatalf("root 不存在: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("perm=%v want 0700", st.Mode().Perm())
	}
}

func TestPutGetDeleteRoundTrip(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	obj, err := s.PutObject(ctx, PutObjectInput{Key: "u1/2026/10/a.png", ContentType: "image/png", Body: []byte("png-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "media://u1/2026/10/a.png" || obj.Size != 9 {
		t.Fatalf("obj=%+v", obj)
	}
	if got := readKey(t, s, "u1/2026/10/a.png"); got != "png-bytes" {
		t.Fatalf("got %q", got)
	}
	// Reader+Size 传法（大文件流式）同样可用；生成产物的 key 没有扩展名。
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "gen/task-1-0", Reader: strings.NewReader("video"), Size: 5}); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "gen/task-1-0"); got != "video" {
		t.Fatalf("got %q", got)
	}
	if err := s.DeleteObject(ctx, "u1/2026/10/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetObject(ctx, "u1/2026/10/a.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("删除后 GetObject err=%v，want ErrNotExist", err)
	}
	if err := s.DeleteObject(ctx, "u1/2026/10/a.png"); err != nil {
		t.Fatalf("删除不存在的对象应视为成功: %v", err)
	}
}

func TestPutObjectOverwritesWithoutLeavingTempFiles(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	key := s.StaticKey(1, "gen/task-9-0")
	for _, body := range []string{"first", "second"} {
		if _, err := s.PutObject(ctx, PutObjectInput{Key: key, Body: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readKey(t, s, key); got != "second" {
		t.Fatalf("got %q", got)
	}
	p, _ := s.Path(key)
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("残留临时文件 %s", e.Name())
		}
	}
}

func TestNewKeyLayout(t *testing.T) {
	s := newLocal(t)
	now := time.Now()
	key := s.NewKey(7, "镜头 1.png")
	want := fmt.Sprintf("u7/%d/%d/镜头 1.png", now.Year(), int(now.Month()))
	if key != want {
		t.Fatalf("key=%q want %q", key, want)
	}
}

// Windows 文件名不能含 \ : * ? " < > |，用户上传的文件名里很常见。
func TestNewKeySanitizesWindowsIllegalNames(t *testing.T) {
	s := newLocal(t)
	key := s.NewKey(7, `镜头:1?<a>|"b"\c*.png`)
	name := key[strings.LastIndex(key, "/")+1:]
	if name != "镜头_1__a___b__c_.png" {
		t.Fatalf("name=%q", name)
	}
	if !ValidKey(key) {
		t.Fatalf("NewKey 产出了非法 key %q", key)
	}
	if _, err := s.PutObject(context.Background(), PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

// 多数文件系统的单个文件名上限是 255 字节。
func TestNewKeyTruncatesLongNamesKeepingExtension(t *testing.T) {
	s := newLocal(t)
	key := s.NewKey(1, strings.Repeat("长", 200)+".mp4")
	name := key[strings.LastIndex(key, "/")+1:]
	if len(name) > 150 || !strings.HasSuffix(name, ".mp4") || !utf8.ValidString(name) {
		t.Fatalf("name=%q len=%d", name, len(name))
	}
	if _, err := s.PutObject(context.Background(), PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

func TestNewKeyFallsBackForEmptyName(t *testing.T) {
	s := newLocal(t)
	for _, in := range []string{"", ".", "..", " . "} {
		key := s.NewKey(1, in)
		if !ValidKey(key) || !strings.HasSuffix(key, "/file") {
			t.Fatalf("NewKey(%q)=%q", in, key)
		}
	}
}

func TestStaticKeyIsDeterministic(t *testing.T) {
	s := newLocal(t)
	a, b := s.StaticKey(3, "gen/task-12-0"), s.StaticKey(3, "gen/task-12-0")
	if a != b || a != "u3/gen_task-12-0" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

// data.db 就在 media/ 的上一级，里面有 API Key：任何能逃出 root 的 key 都必须被拒。
func TestPathRejectsTraversalAndIllegalKeys(t *testing.T) {
	s := newLocal(t)
	bad := []string{"", "../data.db", "u1/../../data.db", "/etc/passwd", `u1\..\data.db`, "C:/Windows/x",
		"u1//a.png", "u1/./a.png", "u1/a.png/", "a\x00b", "u1/a.", "u1/a ", "u1/\xff.png"}
	for _, k := range bad {
		if p, err := s.Path(k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Path(%q)=%q, %v；want ErrInvalidKey", k, p, err)
		}
		if _, err := s.PutObject(context.Background(), PutObjectInput{Key: k, Body: []byte("x")}); err == nil {
			t.Errorf("PutObject(%q) 应失败", k)
		}
	}
	p, err := s.Path("u1/2026/10/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, s.Root()+string(filepath.Separator)) {
		t.Fatalf("p=%q 不在 root=%q 下", p, s.Root())
	}
}

func TestSignedURLRoundTripsThroughExtractKey(t *testing.T) {
	s := newLocal(t)
	for _, key := range []string{"u1/2026/10/a.png", "u1/2026/10/镜头 1#2%.png", "gen/task-3-0"} {
		u, err := s.SignedURL(context.Background(), key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u, "http://127.0.0.1:5555/media/") || !strings.HasSuffix(u, "?token=mediatoken") {
			t.Fatalf("url=%q", u)
		}
		got, ok := s.ExtractKey(u)
		if !ok || got != key {
			t.Fatalf("ExtractKey(%q)=%q,%v want %q", u, got, ok, key)
		}
		thumb, _ := s.SignedThumbURL(context.Background(), key, time.Hour, 400)
		if thumb != u {
			t.Fatalf("缩略图应等于原图（spec S8）: %q vs %q", thumb, u)
		}
	}
	if _, err := s.SignedURL(context.Background(), "../x", time.Hour); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("非法 key 不应签名: %v", err)
	}
}

// 上一次启动发出的 URL（端口不同）也必须认得：生成任务入库时存的是签名 URL，重启续跑要靠它认回 key。
func TestExtractKeyAcceptsAnyLoopbackPortAndMediaScheme(t *testing.T) {
	s := newLocal(t)
	cases := map[string]string{
		"http://127.0.0.1:61234/media/u1/2026/10/a.png?token=old": "u1/2026/10/a.png",
		"http://127.0.0.1:1/media/gen/task-1-0":                    "gen/task-1-0",
		"media://u1/2026/10/a%20b.png":                             "u1/2026/10/a b.png",
		"media://u1/x.png?foo=1":                                   "u1/x.png",
	}
	for in, want := range cases {
		got, ok := s.ExtractKey(in)
		if !ok || got != want {
			t.Errorf("ExtractKey(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"https://cdn.example/media/u1/a.png", "http://localhost:5555/media/u1/a.png",
		"http://127.0.0.1:5555/api/v1/ping", "media://../data.db",
		"http://127.0.0.1:5555/media/..%2Fdata.db", "data:image/png;base64,AAAA", "",
	} {
		if got, ok := s.ExtractKey(in); ok {
			t.Errorf("ExtractKey(%q)=%q 不应识别", in, got)
		}
	}
}

func TestCopyObjectIsIndependentOfSource(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "u1/a.png", Body: []byte("orig")}); err != nil {
		t.Fatal(err)
	}
	obj, err := s.CopyObject(ctx, "u1/a.png", "u2/2026/10/wf-1.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "media://u2/2026/10/wf-1.png" || obj.Size != 4 {
		t.Fatalf("obj=%+v", obj)
	}
	// 项目副本必须与原项目互不影响：源被覆盖写或删除，副本不变。
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "u1/a.png", Body: []byte("changed")}); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "u2/2026/10/wf-1.png"); got != "orig" {
		t.Fatalf("副本被源的覆盖写改动: %q", got)
	}
	if err := s.DeleteObject(ctx, "u1/a.png"); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "u2/2026/10/wf-1.png"); got != "orig" {
		t.Fatalf("got %q", got)
	}
	if _, err := s.CopyObject(ctx, "u1/missing.png", "u2/x.png", ""); err == nil {
		t.Fatal("源不存在应报错")
	}
}
```

`server/internal/assets/repository_test.go`：

```go
package assets

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func newRepo(t *testing.T) (*SQLiteRepository, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	return NewSQLiteRepository(conn), conn
}

func TestInsertAndFindByUserProjectHash(t *testing.T) {
	repo, _ := newRepo(t)
	id, err := repo.Insert(AssetRecord{UserID: 1, ProjectID: 5, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 3, MimeType: "image/png", ContentHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByUserProjectHash(1, 5, "h1")
	if err != nil || got.ID != id || got.ProjectID != 5 || got.URL != "media://u1/a.png" || got.CreatedAt.IsZero() {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByUserProjectHash(1, 6, "h1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨项目不去重，err=%v", err)
	}
	// projectID=0 匹配没挂项目的行
	if _, err := repo.Insert(AssetRecord{UserID: 1, ObjectKey: "u1/b.png", SourceKind: "upload",
		URL: "media://u1/b.png", SizeBytes: 1, MimeType: "image/png", ContentHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.FindByUserProjectHash(1, 0, "h2"); err != nil || got.ProjectID != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByUserProjectHash(1, 5, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("空 hash 必须视为未命中")
	}
}

func TestListAndDeleteForProject(t *testing.T) {
	repo, _ := newRepo(t)
	for i, k := range []string{"k1", "k2"} {
		if _, err := repo.Insert(AssetRecord{UserID: 1, ProjectID: 9, ObjectKey: k, SourceKind: "upload",
			URL: "media://" + k, SizeBytes: int64(10 * (i + 1)), MimeType: "image/png"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.ListForProject(9)
	if err != nil || len(list) != 2 || list[0].CreatedAt.IsZero() {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	freed, err := repo.DeleteForProject(9)
	if err != nil || freed != 30 {
		t.Fatalf("freed=%d err=%v", freed, err)
	}
	if list, _ := repo.ListForProject(9); len(list) != 0 {
		t.Fatal("没删干净")
	}
}

func TestListByObjectKeyAndClearLibraryRef(t *testing.T) {
	repo, conn := newRepo(t)
	repo.Insert(AssetRecord{UserID: 1, ProjectID: 2, ObjectKey: "shared", SourceKind: "upload", URL: "media://shared", SizeBytes: 5, MimeType: "image/png", LibraryItemID: 77})
	repo.Insert(AssetRecord{UserID: 1, ProjectID: 3, ObjectKey: "shared", SourceKind: "canvas_ref", URL: "media://shared", SizeBytes: 0, MimeType: "image/png"})
	rows, err := repo.ListByObjectKey("shared")
	if err != nil || len(rows) != 2 || rows[0].LibraryItemID != 77 || rows[1].ProjectID != 3 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := repo.ClearLibraryRef(77); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE library_item_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("library_item_id 未清空")
	}
}

// 生成产物按 (source_task_id, output_index) 槽位落库：重放复用已有行，不重复插入、不重复记账。
func TestInsertSlotTxIsIdempotentPerSlot(t *testing.T) {
	repo, conn := newRepo(t)
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rec := AssetRecord{UserID: 1, ProjectID: 4, SourceTaskID: 9, ObjectKey: "u1/gen_task-9-0", SourceKind: "generation",
		URL: "media://u1/gen_task-9-0", SizeBytes: 100, MimeType: "image/png"}
	id1, inserted, err := repo.InsertSlotTx(tx, rec, 0)
	if err != nil || !inserted || id1 == 0 {
		t.Fatalf("首次 id=%d inserted=%v err=%v", id1, inserted, err)
	}
	id2, inserted, err := repo.InsertSlotTx(tx, rec, 0)
	if err != nil || inserted || id2 != id1 {
		t.Fatalf("重放 id=%d inserted=%v err=%v，want 复用 %d", id2, inserted, err, id1)
	}
	id3, inserted, err := repo.InsertSlotTx(tx, rec, 1)
	if err != nil || !inserted || id3 == id1 {
		t.Fatalf("新槽位 id=%d inserted=%v err=%v", id3, inserted, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByTaskSlot(9, 0)
	if err != nil || got.ID != id1 || got.SourceTaskID != 9 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := repo.FindByTaskSlot(9, 5); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskAssetsListAndDelete(t *testing.T) {
	repo, _ := newRepo(t)
	repo.Insert(AssetRecord{UserID: 1, SourceTaskID: 3, ObjectKey: "t1", SourceKind: "generation", URL: "media://t1", SizeBytes: 4, MimeType: "image/png"})
	repo.Insert(AssetRecord{UserID: 1, SourceTaskID: 3, ObjectKey: "t2", SourceKind: "generation", URL: "media://t2", SizeBytes: 6, MimeType: "image/png"})
	if list, err := repo.ListForTask(3); err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if freed, err := repo.DeleteForTask(3); err != nil || freed != 10 {
		t.Fatalf("freed=%d err=%v", freed, err)
	}
}

func TestDurationMsByObjectKey(t *testing.T) {
	repo, _ := newRepo(t)
	if _, found, err := repo.GetDurationMsByObjectKey("none"); found || err != nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	repo.Insert(AssetRecord{UserID: 1, ObjectKey: "v", SourceKind: "upload", URL: "media://v", SizeBytes: 1, MimeType: "video/mp4"})
	if d, found, err := repo.GetDurationMsByObjectKey("v"); !found || d != 0 || err != nil {
		t.Fatalf("NULL 时长 d=%d found=%v err=%v", d, found, err)
	}
	if err := repo.SetDurationMsByObjectKey("v", 4200); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := repo.GetDurationMsByObjectKey("v"); d != 4200 {
		t.Fatalf("d=%d", d)
	}
}

func TestFindByIDAndListForUser(t *testing.T) {
	repo, _ := newRepo(t)
	id, _ := repo.Insert(AssetRecord{UserID: 1, ObjectKey: "a", SourceKind: "upload", URL: "media://a", SizeBytes: 1, MimeType: "image/png"})
	got, err := repo.FindByID(id)
	if err != nil || got == nil || got.ObjectKey != "a" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got, err := repo.FindByID(99999); got != nil || err != nil {
		t.Fatalf("不存在应返回 (nil,nil)，got=%+v err=%v", got, err)
	}
	if list, err := repo.ListForUser(1, 0); err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/assets/`
Expected: 编译失败，`undefined: LocalStorage`、`undefined: NewSQLiteRepository`、`undefined: MediaProtocol`（minio.go 没有复制）。

- [ ] **Step 4: 写 local.go**

`server/internal/assets/local.go`：

```go
package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MediaProtocol 是我们存进 DB 的伪 URL scheme，用来替代签名 URL：签名延后到 API 响应时再做。
// （参考仓定义在 minio.go；桌面端不搬 minio.go，定义挪到这里。）
const MediaProtocol = "media://"

// ErrInvalidKey 表示 object key 不合法：可能逃出媒体根目录，或含当前平台不能做文件名的字符。
var ErrInvalidKey = errors.New("invalid object key")

// LocalConfig 配置本地文件存储。
type LocalConfig struct {
	// Root 是媒体根目录，即 <data-dir>/media；不存在会以 0700 创建。
	Root string
	// BaseURL 是 sidecar 本次启动的地址，如 http://127.0.0.1:54321（无尾斜杠）。
	BaseURL string
	// Token 是只放行 GET/HEAD /media/* 的媒体 token，拼进对外 URL 的 ?token=。
	Token string
}

// LocalStorage 把对象存成 <Root>/<key> 的本地文件，实现参考仓的 Storage 接口（spec S3、S4）。
// key 布局沿用参考仓；DB 里只存 media://<key>，所以数据目录整体搬家（spec S2）后引用仍然有效。
type LocalStorage struct {
	root    string
	baseURL string
	token   string
}

func NewLocalStorage(cfg LocalConfig) (*LocalStorage, error) {
	if cfg.Root == "" {
		return nil, errors.New("local storage root is required")
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建媒体目录: %w", err)
	}
	return &LocalStorage{root: root, baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token}, nil
}

// Root 返回媒体根目录的绝对路径。
func (s *LocalStorage) Root() string { return s.root }

// Path 把 key 换算成本地绝对路径。key 不合法时返回 ErrInvalidKey——
// 媒体根目录的上一级就是 data.db（里面有 API Key），任何能逃出根目录的 key 都必须拒绝。
func (s *LocalStorage) Path(key string) (string, error) {
	if !ValidKey(key) {
		return "", ErrInvalidKey
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return p, nil
}

// ValidKey 判断 key 能否安全地映射成媒体根目录下的文件：段不能为空、"."、".."，
// 不能以 "." 或空格结尾（Windows 会静默截掉，两个 key 撞到同一个文件），
// 不能含控制字符和 Windows 文件名禁用字符。
func ValidKey(key string) bool {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) || strings.HasPrefix(key, "/") {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`\:*?"<>|`, r) {
			return false
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return false
		}
	}
	return true
}

// maxNameBytes 是 key 最后一段（文件名）的上限。多数文件系统单个文件名上限 255 字节，
// 上传路径还会加 9 字节随机前缀，留足余量。
const maxNameBytes = 150

// sanitizeName 把用户给的文件名变成能在 macOS 和 Windows 上落盘、且能通过 ValidKey 的一段。
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > maxNameBytes {
		ext := filepath.Ext(out)
		if len(ext) > 16 {
			ext = ""
		}
		stem := out[:len(out)-len(ext)]
		cut := maxNameBytes - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		out = stem[:cut] + ext
	}
	out = strings.TrimRight(out, ". ")
	if out == "" {
		out = "file"
	}
	return out
}

func (s *LocalStorage) PutObject(_ context.Context, input PutObjectInput) (StoredObject, error) {
	p, err := s.Path(input.Key)
	if err != nil {
		return StoredObject{}, err
	}
	reader, _ := input.Content()
	n, err := writeFileAtomic(p, reader)
	if err != nil {
		return StoredObject{}, fmt.Errorf("put object: %w", err)
	}
	return StoredObject{URL: MediaProtocol + input.Key, Size: n}, nil
}

// writeFileAtomic 先写同目录的临时文件再 rename：并发读者永远看不到半截文件，
// 同一 key 的重放覆盖写也是原子的。
func writeFileAtomic(dst string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return n, nil
}

func (s *LocalStorage) DeleteObject(_ context.Context, key string) error {
	p, err := s.Path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LocalStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.Path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// SignedURL 返回 sidecar 的本地媒体 URL。不过期：有效性由本次启动的媒体 token 决定，
// sidecar 重启后端口和 token 都会变，旧 URL 自然失效（ExtractKey 仍能认回 key）。
func (s *LocalStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if !ValidKey(key) {
		return "", ErrInvalidKey
	}
	if s.baseURL == "" {
		return "", errors.New("local storage base URL not configured")
	}
	return s.baseURL + "/media/" + escapeKey(key) + "?token=" + url.QueryEscape(s.token), nil
}

// SignedThumbURL 返回原图 URL：桌面端不生成缩略图（spec S8，与参考仓 MinIO 模式一致）。
func (s *LocalStorage) SignedThumbURL(ctx context.Context, key string, expiry time.Duration, _ int) (string, error) {
	return s.SignedURL(ctx, key, expiry)
}

func escapeKey(key string) string {
	segs := strings.Split(key, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

// localMediaURL 匹配任意端口的 sidecar 媒体 URL。端口不限：上一次启动发出的 URL
// （P1c 生成任务入库时存的是签名后的 inputs）在重启后也要能认回 key。
var localMediaURL = regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]{1,5}/media/([^?#]*)`)

// ExtractKey 在输入是 media:// 伪 URL 或 sidecar 自己发出的本地媒体 URL 时返回 (key, true)。
// 解出的 key 不合法（如 ../data.db）一律不认，外部 URL、data: 等原样不认。
func (s *LocalStorage) ExtractKey(urlOrKey string) (string, bool) {
	var raw string
	switch {
	case strings.HasPrefix(urlOrKey, MediaProtocol):
		raw = strings.TrimPrefix(urlOrKey, MediaProtocol)
		if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
			raw = raw[:idx]
		}
	default:
		m := localMediaURL.FindStringSubmatch(urlOrKey)
		if m == nil {
			return "", false
		}
		raw = m[1]
	}
	key, err := url.PathUnescape(raw)
	if err != nil || !ValidKey(key) {
		return "", false
	}
	return key, true
}

// StaticKey 构造不含时间成分的确定性 key：同一 (userID, filename) 永远同一个 key，
// 生成产物重放覆盖写同一文件，不产生孤儿。
func (s *LocalStorage) StaticKey(userID int64, filename string) string {
	return fmt.Sprintf("u%d/%s", userID, sanitizeName(filename))
}

// NewKey 为一次新上传构造 key，布局 u<userID>/<年>/<月>/<文件名>（同参考仓）。
func (s *LocalStorage) NewKey(userID int64, filename string) string {
	now := time.Now()
	return fmt.Sprintf("u%d/%d/%d/%s", userID, now.Year(), int(now.Month()), sanitizeName(filename))
}

// CopyObject 先试硬链接：同盘瞬间完成、不占双份空间；之后对源的覆盖写走 rename（换 inode）、
// 删除只是减链接数，都不影响副本。跨卷或文件系统不支持硬链接（FAT/exFAT）时退回整文件复制。
func (s *LocalStorage) CopyObject(_ context.Context, srcKey, dstKey, _ string) (StoredObject, error) {
	src, err := s.Path(srcKey)
	if err != nil {
		return StoredObject{}, err
	}
	dst, err := s.Path(dstKey)
	if err != nil {
		return StoredObject{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	if err := os.Link(src, dst); err == nil {
		st, err := os.Stat(dst)
		if err != nil {
			return StoredObject{}, fmt.Errorf("copy object: %w", err)
		}
		return StoredObject{URL: MediaProtocol + dstKey, Size: st.Size()}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	defer f.Close()
	n, err := writeFileAtomic(dst, f)
	if err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	return StoredObject{URL: MediaProtocol + dstKey, Size: n}, nil
}
```

- [ ] **Step 5: 移植 repository.go**

在 `server/internal/assets/repository.go` 中做以下改动，其余保持原样：

1. 类型与构造函数改名：`MySQLRepository` → `SQLiteRepository`、`NewMySQLRepository` → `NewSQLiteRepository`、所有方法接收者。
2. 接口 `Repository` 上 `InsertSlotTx` 的注释里「撞键时复用已有行(id 经 LAST_INSERT_ID(id) 返回),inserted=false」改为「撞键时 ON CONFLICT DO NOTHING，再在同一事务里按槽位查回已有行 id,inserted=false」。
3. 把整个 `InsertSlotTx` 方法（含上方注释）替换为：

```go
// InsertSlotTx 见接口注释。SQLite 没有 MySQL 的 LAST_INSERT_ID(id) 技巧：撞键时 DO NOTHING，
// RowsAffected()==0 说明槽位已被（并发）重放写过，在同一事务里查回已有行的 id。
func (r *SQLiteRepository) InsertSlotTx(tx *sql.Tx, rec AssetRecord, outputIndex int) (int64, bool, error) {
	var projectID, sourceTaskID, durationMs any
	if rec.ProjectID > 0 {
		projectID = rec.ProjectID
	}
	if rec.SourceTaskID > 0 {
		sourceTaskID = rec.SourceTaskID
	}
	if rec.DurationMs > 0 {
		durationMs = rec.DurationMs
	}
	res, err := tx.Exec(
		`INSERT INTO assets (user_id, project_id, source_task_id, output_index, object_key, source_kind, url, size_bytes, mime_type, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (source_task_id, output_index) DO NOTHING`,
		rec.UserID, projectID, sourceTaskID, outputIndex, rec.ObjectKey, rec.SourceKind, rec.URL, rec.SizeBytes, rec.MimeType, durationMs,
	)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return 0, false, err
		}
		return id, true, nil
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM assets WHERE source_task_id = ? AND output_index = ?`, sourceTaskID, outputIndex).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `cd server && go test ./internal/assets/ -race -v`
Expected: `put_input_test` 3 个 + `local_test` 12 个 + `repository_test` 7 个全部 PASS。

若 `TestNewLocalStorageCreatesPrivateRoot` 报权限不是 0700，检查本机 umask 是否比 077 更宽松以外的异常设置；不得放宽测试。若 `TestCopyObjectIsIndependentOfSource` 失败，说明某处覆盖写没有走 `writeFileAtomic` 的 rename（原地写会改到硬链接的副本）。

- [ ] **Step 7: 交叉编译并提交**

Run: `cd server && go vet ./internal/assets/ && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./internal/assets/`
Expected: 无输出。

```bash
git add server/internal/assets
git commit -m "feat(server): 本地文件存储 LocalStorage 与 assets 仓库移植到 SQLite"
```

---

### Task 2: 迁移 0002 + library 仓库 + 孤儿定时清扫

**Files:**
- Create: `server/internal/db/migrations/0002_library.sql`
- Create（移植）: `server/internal/library/folders_repo.go`、`items_repo.go`、`orphans_repo.go`、`reclaim.go`
- Create（新写）: `server/internal/library/sweeper.go`
- Test: `server/internal/library/repo_test.go`、`server/internal/library/sweeper_test.go`

**Interfaces:**
- Consumes: `dbtest.Open`、`db.Time`（P1a）
- Produces：
  - 表 `library_folders`、`library_items`、`orphan_objects`（Task 6 的 `ComputeStorageUsed`、Task 8 的媒体 MIME 查询、Task 9/11 依赖）。
  - 原样：`library.Folder`、`Item`、`ItemFilter`、`OrphanObject`；`ErrFolderNotFound`、`ErrFolderNotEmpty`、`ErrFolderCycle`、`ErrItemNotFound`；接口 `FoldersRepository`、`ItemsRepository`、`OrphansRepository`、`Querier`（`QueryRow`）、`Execer`（`Exec`）；`ReclaimDeps{Querier, Execer, Orphans}`；`IsStillReferenced(q Querier, objectKey string) (bool, error)`；`TryReclaim(d ReclaimDeps, objectKey string, userID, sizeBytes int64) error`。
  - 改名：`NewSQLiteFoldersRepository(db *sql.DB) *SQLiteFoldersRepository`、`NewSQLiteItemsRepository(db *sql.DB) *SQLiteItemsRepository`、`NewSQLiteOrphansRepository(db *sql.DB) *SQLiteOrphansRepository`。
  - 新增：`library.DefaultGracePeriod = 7 * 24 * time.Hour`；`library.SweepResult{Deleted, Skipped, TotalCandidates int}`；`library.NewOrphanSweeper(orphans OrphansRepository, storage objectDeleter, db Querier) *OrphanSweeper`（`objectDeleter` = `interface{ DeleteObject(ctx context.Context, key string) error }`，`*assets.LocalStorage` 满足）；`(*OrphanSweeper).Candidates(grace time.Duration) ([]OrphanObject, error)`；`(*OrphanSweeper).Sweep(ctx context.Context, grace time.Duration) (SweepResult, error)`；`(*OrphanSweeper).Start(ctx context.Context, interval, grace time.Duration)`（启动即跑一次，之后每 interval 一次）。

表结构依据参考仓迁移：`0010_material_library.sql` 建表；`0074` 给两表加的 `system_key`、`seedance_*` 列已被 `0075` 全部删除，最终结构即 0010 的列。外键 `ON DELETE RESTRICT` 保留（handler 的递归删除按“先删素材、再从叶子到根删目录”的顺序写，正好满足 RESTRICT）。

- [ ] **Step 1: 写迁移**

`server/internal/db/migrations/0002_library.sql`：

```sql
-- 桌面端 P1b：个人素材库（目录树 + 条目）与孤儿对象回收表。
-- 列与参考仓 MySQL 最终结构一致：0010_material_library.sql 建表；0074 加的 system_key、seedance_* 列
-- 已被 0075 删除。约定同 0001：时间 DATETIME 存 UTC 文本，ON UPDATE CURRENT_TIMESTAMP 用触发器。

CREATE TABLE library_folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  -- NULL = 根目录；自引用 RESTRICT：有子目录的目录不能直接删。
  parent_id INTEGER NULL REFERENCES library_folders (id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_library_folders_user ON library_folders (user_id);
CREATE INDEX idx_library_folders_parent ON library_folders (parent_id);

CREATE TABLE library_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  folder_id INTEGER NOT NULL REFERENCES library_folders (id) ON DELETE RESTRICT,
  -- 与 assets 共用对象：孤儿回收按 assets ∪ library_items 的 object_key 判断是否仍被引用。
  object_key TEXT NOT NULL,
  url TEXT NOT NULL,
  -- 引用行（存入素材库 / 从画布保存）为 0，不重复占配额。
  size_bytes INTEGER NOT NULL,
  mime_type TEXT NOT NULL,
  kind TEXT NOT NULL,
  width INTEGER NULL,
  height INTEGER NULL,
  duration_ms INTEGER NULL,
  source TEXT NOT NULL DEFAULT 'upload',
  name TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_library_items_user ON library_items (user_id);
CREATE INDEX idx_library_items_folder ON library_items (folder_id);
CREATE INDEX idx_library_items_object_key ON library_items (object_key);

-- 删除记录后已无引用的对象先在这里打标记，过了宽限期（7 天，spec S9）才由清扫真正删文件。
CREATE TABLE orphan_objects (
  object_key TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  size_bytes INTEGER NOT NULL,
  marked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_orphan_marked_at ON orphan_objects (marked_at);

CREATE TRIGGER trg_library_folders_updated_at AFTER UPDATE ON library_folders
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE library_folders SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
```

- [ ] **Step 2: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/library
cp $REF/library/folders_repo.go $REF/library/items_repo.go $REF/library/orphans_repo.go $REF/library/reclaim.go server/internal/library/
```

（不复制 `seedance_assets_repo.go`、`seedance_service.go`、`seedance_service_test.go`：Seedance 属 P1d/P7。）

- [ ] **Step 3: 写失败的测试**

`server/internal/library/repo_test.go`：

```go
package library

import (
	"errors"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestLibraryForeignKeysRestrictDeletes(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'p')`)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO library_folders (user_id, parent_id, name) VALUES (1, ?, 'c')`, parent)
	child, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, ?, 'k', 'media://k', 1, 'image/png', 'image')`, child); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM library_folders WHERE id = ?`, child); err == nil {
		t.Fatal("有素材的目录不应能直接删（ON DELETE RESTRICT）")
	}
	if _, err := conn.Exec(`DELETE FROM library_folders WHERE id = ?`, parent); err == nil {
		t.Fatal("有子目录的目录不应能直接删")
	}
	if _, err := conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, 99999, 'k', 'u', 1, 'image/png', 'image')`); err == nil {
		t.Fatal("指向不存在目录的素材应被外键挡下")
	}
}

func TestLibraryFoldersUpdatedAtTrigger(t *testing.T) {
	conn := dbtest.Open(t)
	res, _ := conn.Exec(`INSERT INTO library_folders (user_id, name, updated_at) VALUES (1, 'a', '2000-01-01 00:00:00')`)
	id, _ := res.LastInsertId()
	if _, err := conn.Exec(`UPDATE library_folders SET name = 'b' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := conn.QueryRow(`SELECT updated_at FROM library_folders WHERE id = ?`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Year() == 2000 {
		t.Fatal("UPDATE 后 updated_at 没有刷新")
	}
}

func TestFolderTreeCRUD(t *testing.T) {
	folders := NewSQLiteFoldersRepository(dbtest.Open(t))
	root, err := folders.Create(1, nil, "角色")
	if err != nil {
		t.Fatal(err)
	}
	child, err := folders.Create(1, &root, "主角")
	if err != nil {
		t.Fatal(err)
	}
	grand, _ := folders.Create(1, &child, "表情")

	list, err := folders.List(1)
	if err != nil || len(list) != 3 || list[0].ID != root || list[0].ParentID != nil {
		t.Fatalf("list=%+v err=%v（根目录 parent_id 为 NULL，应排最前）", list, err)
	}
	got, err := folders.Get(1, child)
	if err != nil || got.ParentID == nil || *got.ParentID != root || got.CreatedAt.IsZero() {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := folders.Get(2, child); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("别的用户读不到: %v", err)
	}
	if _, err := folders.Create(2, &grand, "x"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("不能挂到别人的目录下: %v", err)
	}
	// MySQL 改成同名时 RowsAffected=0 会误报 not found；SQLite 返回匹配行数，不再误报。
	if err := folders.Rename(1, child, "主角"); err != nil {
		t.Fatalf("改成同名不应报错: %v", err)
	}
	if err := folders.Rename(1, 99999, "x"); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("err=%v", err)
	}
	if err := folders.Move(1, root, &grand); !errors.Is(err, ErrFolderCycle) {
		t.Fatalf("移到自己的后代下应报环: %v", err)
	}
	if err := folders.Move(1, grand, nil); err != nil {
		t.Fatalf("移回根目录: %v", err)
	}
	ids, err := folders.DescendantIDs(1, root)
	if err != nil || len(ids) != 2 || ids[0] != root || ids[1] != child {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if err := folders.DeleteIfEmpty(1, root); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("有子目录应报非空: %v", err)
	}
	if err := folders.DeleteIfEmpty(1, child); err != nil {
		t.Fatal(err)
	}
	if err := folders.DeleteIfEmpty(1, root); err != nil {
		t.Fatal(err)
	}
	if err := folders.DeleteIfEmpty(1, root); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestItemsCRUDAndFilters(t *testing.T) {
	conn := dbtest.Open(t)
	folders := NewSQLiteFoldersRepository(conn)
	items := NewSQLiteItemsRepository(conn)
	f1, _ := folders.Create(1, nil, "a")
	f2, _ := folders.Create(1, nil, "b")
	w := 640
	id1, err := items.Insert(Item{UserID: 1, FolderID: f1, ObjectKey: "library/1/x.png", URL: "media://library/1/x.png",
		SizeBytes: 10, MimeType: "image/png", Kind: "image", Width: &w, Source: "upload", Name: "Cat Photo"})
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := items.Insert(Item{UserID: 1, FolderID: f2, ObjectKey: "library/1/y.mp4", URL: "media://library/1/y.mp4",
		SizeBytes: 20, MimeType: "video/mp4", Kind: "video", Source: "upload"})

	it, err := items.Get(1, id1)
	if err != nil || it.Width == nil || *it.Width != 640 || it.Height != nil || it.Name != "Cat Photo" || it.CreatedAt.IsZero() {
		t.Fatalf("it=%+v err=%v", it, err)
	}
	if list, total, err := items.List(ItemFilter{UserID: 1}); err != nil || total != 2 || list[0].ID != id2 {
		t.Fatalf("list=%+v total=%d err=%v（按 id 倒序）", list, total, err)
	}
	if list, total, _ := items.List(ItemFilter{UserID: 1, FolderID: f1}); total != 1 || list[0].ID != id1 {
		t.Fatalf("按目录过滤 list=%+v total=%d", list, total)
	}
	if _, total, _ := items.List(ItemFilter{UserID: 1, Search: "cat"}); total != 1 {
		t.Fatalf("按名称搜索应大小写不敏感，total=%d", total)
	}

	conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@miaoju.desktop')`)
	foreign, _ := folders.Create(2, nil, "别人的")
	if err := items.Update(1, id1, nil, &foreign); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("不能移到别人的目录: %v", err)
	}
	name := "renamed"
	if err := items.Update(1, id1, &name, &f2); err != nil {
		t.Fatal(err)
	}
	if n, _ := items.CountForFolder(1, f2); n != 2 {
		t.Fatalf("count=%d", n)
	}
	if got, _ := items.ListByFolderIDs(1, []int64{f1, f2}); len(got) != 2 {
		t.Fatalf("ListByFolderIDs=%d", len(got))
	}
	if got, err := items.ListByFolderIDs(1, nil); got != nil || err != nil {
		t.Fatalf("空列表 got=%v err=%v", got, err)
	}
	if err := items.Delete(1, id2); err != nil {
		t.Fatal(err)
	}
	if err := items.Delete(1, id2); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("err=%v", err)
	}
	if f, _ := folders.Get(1, f2); f.ItemCount != 1 {
		t.Fatalf("itemCount=%d", f.ItemCount)
	}
}

func TestOrphanMarkRefreshesAndListReady(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	if err := orphans.Mark(conn, "k1", 1, 100); err != nil {
		t.Fatal(err)
	}
	conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00' WHERE object_key = 'k1'`)
	ready, err := orphans.ListReady(DefaultGracePeriod)
	if err != nil || len(ready) != 1 || ready[0].MarkedAt.Year() != 2000 || ready[0].SizeBytes != 100 {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	// 再次 Mark 刷新宽限计时：主键冲突走 ON CONFLICT DO UPDATE。
	if err := orphans.Mark(conn, "k1", 1, 100); err != nil {
		t.Fatal(err)
	}
	if ready, _ := orphans.ListReady(DefaultGracePeriod); len(ready) != 0 {
		t.Fatalf("重新标记后应重新计时，ready=%+v", ready)
	}
	if err := orphans.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects`).Scan(&n)
	if n != 0 {
		t.Fatal("没删掉")
	}
}

func TestTryReclaimOnlyMarksUnreferencedKeys(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	folders := NewSQLiteFoldersRepository(conn)
	items := NewSQLiteItemsRepository(conn)
	f, _ := folders.Create(1, nil, "f")
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'in-assets', 'media://in-assets', 1, 'image/png')`)
	items.Insert(Item{UserID: 1, FolderID: f, ObjectKey: "in-library", URL: "media://in-library", SizeBytes: 1, MimeType: "image/png", Kind: "image", Source: "upload"})

	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"in-assets", "in-library", "gone"} {
		if err := TryReclaim(ReclaimDeps{Querier: tx, Execer: tx, Orphans: orphans}, k, 1, 5); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ready, _ := orphans.ListReady(-time.Hour) // 截止时间在未来：刚标记的也算
	if len(ready) != 1 || ready[0].ObjectKey != "gone" {
		t.Fatalf("只应标记无引用的 key，got %+v", ready)
	}
	if ref, err := IsStillReferenced(conn, "in-library"); err != nil || !ref {
		t.Fatalf("ref=%v err=%v", ref, err)
	}
	if ref, _ := IsStillReferenced(conn, "gone"); ref {
		t.Fatal("gone 不应仍被引用")
	}
}
```

`server/internal/library/sweeper_test.go`：

```go
package library

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

type fakeDeleter struct {
	mu      sync.Mutex
	deleted []string
	failOn  map[string]bool
}

func (f *fakeDeleter) DeleteObject(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn[key] {
		return errors.New("文件被占用")
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func markOld(t *testing.T, conn *sql.DB, orphans *SQLiteOrphansRepository, key string) {
	t.Helper()
	if err := orphans.Mark(conn, key, 1, 10); err != nil {
		t.Fatal(err)
	}
	conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00' WHERE object_key = ?`, key)
}

func orphanKeys(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestSweepDeletesOnlyOldUnreferencedOrphans(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	del := &fakeDeleter{failOn: map[string]bool{"locked": true}}
	markOld(t, conn, orphans, "old-free")
	markOld(t, conn, orphans, "old-referenced")
	markOld(t, conn, orphans, "locked")
	orphans.Mark(conn, "young", 1, 10)
	// 宽限期内被重新引用（如撤销删除后又粘回画布）。
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'old-referenced', 'media://old-referenced', 0, 'image/png')`)

	sw := NewOrphanSweeper(orphans, del, conn)
	cands, err := sw.Candidates(DefaultGracePeriod)
	if err != nil || len(cands) != 3 {
		t.Fatalf("cands=%+v err=%v", cands, err)
	}
	res, err := sw.Sweep(context.Background(), DefaultGracePeriod)
	if err != nil || res.Deleted != 1 || res.Skipped != 2 || res.TotalCandidates != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !reflect.DeepEqual(del.deleted, []string{"old-free"}) {
		t.Fatalf("deleted=%v", del.deleted)
	}
	// 被重新引用的撤销孤儿标记；删文件失败的保留，下一轮重试；未过宽限期的不动。
	if got := orphanKeys(t, conn); !reflect.DeepEqual(got, []string{"locked", "young"}) {
		t.Fatalf("剩余孤儿=%v", got)
	}
}

// 桌面端没有运维去调 /admin/gc：sidecar 启动时必须立即清扫一次（之后每天一次）。
func TestSweeperStartRunsImmediately(t *testing.T) {
	conn := dbtest.Open(t)
	orphans := NewSQLiteOrphansRepository(conn)
	markOld(t, conn, orphans, "k")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewOrphanSweeper(orphans, &fakeDeleter{}, conn).Start(ctx, time.Hour, DefaultGracePeriod)
	deadline := time.Now().Add(3 * time.Second)
	for len(orphanKeys(t, conn)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("Start 后 3 秒内没有执行第一次清扫")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `cd server && go test ./internal/library/`
Expected: 编译失败，`undefined: NewSQLiteFoldersRepository`、`undefined: DefaultGracePeriod`。

- [ ] **Step 5: 改写复制来的仓库文件**

1. `folders_repo.go`：`MySQLFoldersRepository` → `SQLiteFoldersRepository`、`NewMySQLFoldersRepository` → `NewSQLiteFoldersRepository`、所有方法接收者；删除注释「// 系统目录。真人图库：每用户一个，惰性创建，不可删除/重命名/移动。」及其下方空的 `const (\n)` 块（真人图库已在参考仓 0075 删除，这是残留）。
2. `items_repo.go`：`MySQLItemsRepository` → `SQLiteItemsRepository`、`NewMySQLItemsRepository` → `NewSQLiteItemsRepository`、所有方法接收者。
3. `orphans_repo.go`：
   - `MySQLOrphansRepository` → `SQLiteOrphansRepository`、`NewMySQLOrphansRepository` → `NewSQLiteOrphansRepository`、所有方法接收者。
   - `Mark` 的 SQL 末行 `ON DUPLICATE KEY UPDATE marked_at = CURRENT_TIMESTAMP` → `ON CONFLICT (object_key) DO UPDATE SET marked_at = CURRENT_TIMESTAMP`。
   - `ListReady`：`cutoff := time.Now().Add(-olderThan)` → `cutoff := db.Time(time.Now().Add(-olderThan))`，import 增加 `"github.com/james/huabu_studio/apps/api/internal/db"`。
4. `reclaim.go`：不改。

- [ ] **Step 6: 写 sweeper.go**

`server/internal/library/sweeper.go`：

```go
package library

import (
	"context"
	"log"
	"time"
)

// DefaultGracePeriod 是孤儿对象的宽限期（spec S9）：删除后 7 天内撤销、重新引用都来得及。
const DefaultGracePeriod = 7 * 24 * time.Hour

type objectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// SweepResult 是一轮清扫的统计，字段与参考仓 POST /admin/gc 的响应一一对应。
type SweepResult struct {
	Deleted         int
	Skipped         int
	TotalCandidates int
}

// OrphanSweeper 真正删除过了宽限期的孤儿对象。逻辑搬自参考仓 admin_gc_handlers.go 的 Run；
// 参考仓只能由管理员手动触发，桌面端没有运维，改由 sidecar 启动时跑一次、之后每天跑一次（Start）。
type OrphanSweeper struct {
	orphans OrphansRepository
	storage objectDeleter
	db      Querier
}

func NewOrphanSweeper(orphans OrphansRepository, storage objectDeleter, db Querier) *OrphanSweeper {
	return &OrphanSweeper{orphans: orphans, storage: storage, db: db}
}

// Candidates 只列出早于宽限期的孤儿，不碰文件（/admin/gc?dryRun=1 用）。
func (s *OrphanSweeper) Candidates(grace time.Duration) ([]OrphanObject, error) {
	return s.orphans.ListReady(grace)
}

// Sweep 扫描早于宽限期的孤儿，逐个用 IsStillReferenced 复核（宽限期内可能被重新引用），
// 仍无引用才删文件 + 删孤儿行。不调整配额——标记孤儿时调用方已经扣过。
// 删文件失败（如 Windows 上文件正被占用）只计 skipped、保留孤儿行，下一轮重试。
func (s *OrphanSweeper) Sweep(ctx context.Context, grace time.Duration) (SweepResult, error) {
	list, err := s.orphans.ListReady(grace)
	if err != nil {
		return SweepResult{}, err
	}
	res := SweepResult{TotalCandidates: len(list)}
	for _, o := range list {
		if ctx.Err() != nil {
			break
		}
		referenced, err := IsStillReferenced(s.db, o.ObjectKey)
		if err != nil {
			log.Printf("gc: refcheck %s failed: %v", o.ObjectKey, err)
			res.Skipped++
			continue
		}
		if referenced {
			// 在宽限窗口内被重新引用——去掉孤儿标记。
			_ = s.orphans.Delete(o.ObjectKey)
			res.Skipped++
			continue
		}
		if err := s.storage.DeleteObject(ctx, o.ObjectKey); err != nil {
			log.Printf("gc: DeleteObject %s failed: %v", o.ObjectKey, err)
			res.Skipped++
			continue
		}
		if err := s.orphans.Delete(o.ObjectKey); err != nil {
			log.Printf("gc: orphan row delete %s failed: %v", o.ObjectKey, err)
		}
		res.Deleted++
	}
	return res, nil
}

// Start 在后台定时清扫：立即跑一次（应用常常只开几个小时，等满一天才跑等于不跑），之后每 interval 一次。
func (s *OrphanSweeper) Start(ctx context.Context, interval, grace time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			res, err := s.Sweep(ctx, grace)
			if err != nil {
				log.Printf("gc: sweep failed: %v", err)
			} else if res.TotalCandidates > 0 {
				log.Printf("gc: deleted=%d skipped=%d candidates=%d", res.Deleted, res.Skipped, res.TotalCandidates)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
```

- [ ] **Step 7: 运行测试，确认通过**

Run: `cd server && go test ./internal/library/ ./internal/db/... -race -v`
Expected: library 8 个测试 PASS；P1a 的 db 测试仍全部 PASS（0002 被 `Migrate` 自动执行）。

- [ ] **Step 8: 提交**

```bash
git add server/internal/db/migrations/0002_library.sql server/internal/library
git commit -m "feat(server): 素材库与孤儿回收表（迁移 0002）、library 仓库移植与孤儿定时清扫"
```

---

### Task 3: localauth 加固 + Tauri 壳（P0 遗留）

**Files:**
- Modify: `server/internal/localauth/middleware.go`
- Test: `server/internal/localauth/middleware_test.go`（追加）
- Modify: `src-tauri/src/lib.rs`

**Interfaces:**
- Consumes: 无
- Produces：
  - `localauth.Config` 新增字段 `MediaToken string`：非空时，`GET`/`HEAD` 且路径以 `/media/` 开头的请求可用它代替接口 token（Task 12 装配时传入）。
  - `Authorization` 头只有带 `Bearer ` 前缀才被读取；没有或是别的方案（如 `Basic`）时回退到 `?token=`。
  - 预检响应带 `Access-Control-Max-Age: 600`。
  - 壳 → sidecar 的协议补充（Task 12 的 Go 侧实现它）：debug 构建额外传 `--origins tauri://localhost,http://tauri.localhost,http://localhost:5173`；应用退出时先向 sidecar 的 stdin 写一行 `SHUTDOWN`，最多等 3 秒进程退出，等不到再强杀。
  - 数据目录在 Unix 上设为 `0700`（壳先建目录，只改 Go 侧无效）。

- [ ] **Step 1: 写失败的测试**

在 `server/internal/localauth/middleware_test.go` 末尾追加（复用文件里已有的 `testToken`、`do`）：

```go
const testMediaToken = "fedcba9876543210fedcba9876543210"

func doWith(t *testing.T, cfg Config, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mediaCfg() Config {
	return Config{Token: testToken, MediaToken: testMediaToken, AllowedOrigins: []string{"tauri://localhost"}}
}

// P0 遗留：TrimPrefix 不校验前缀，"Basic xxx" 会挡住 ?token= 回退。
func TestNonBearerAuthorizationFallsBackToQuery(t *testing.T) {
	rec := do(t, "GET", "/x?token="+testToken, map[string]string{"Authorization": "Basic Zm9vOmJhcg=="})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

func TestAuthorizationWithoutBearerPrefixRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": testToken})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

// 媒体 URL 会出现在 DOM、日志和 Agent 提示词里：它带的 token 只能读媒体，不能调接口。
func TestMediaTokenOnlyOpensMediaReads(t *testing.T) {
	cfg := mediaCfg()
	cases := []struct {
		method, target string
		headers        map[string]string
		want           int
	}{
		{"GET", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusOK},
		{"HEAD", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusOK},
		{"GET", "/api/v1/ping?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"POST", "/media/u1/a.png?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"GET", "/api/v1/projects", map[string]string{"Authorization": "Bearer " + testMediaToken}, http.StatusUnauthorized},
		{"GET", "/mediax/a.png?token=" + testMediaToken, nil, http.StatusUnauthorized},
		{"GET", "/media/u1/a.png?token=" + testToken, nil, http.StatusOK}, // 接口 token 也能读媒体
	}
	for _, c := range cases {
		if got := doWith(t, cfg, c.method, c.target, c.headers).Code; got != c.want {
			t.Errorf("%s %s: code = %d, want %d", c.method, c.target, got, c.want)
		}
	}
}

func TestEmptyMediaTokenNeverMatches(t *testing.T) {
	cfg := Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}}
	if got := doWith(t, cfg, "GET", "/media/u1/a.png?token=", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", got)
	}
}

// 沙箱 iframe、file:// 页面发出的请求 Origin 是字面量 "null"。
func TestOriginNullRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer " + testToken, "Origin": "null"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestWrongTokenWithAllowedOriginRejected(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer wrong", "Origin": "tauri://localhost"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "tauri://localhost" {
		t.Fatal("白名单来源的 401 也要带 CORS 头，前端才能读到状态码")
	}
}

func TestPreflightForeignOriginRejectedEvenWithToken(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{
		"Origin":                        "https://evil.example",
		"Access-Control-Request-Method": "POST",
		"Authorization":                 "Bearer " + testToken,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestPreflightSetsMaxAge(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{"Origin": "tauri://localhost", "Access-Control-Request-Method": "PATCH"})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("code=%d max-age=%q", rec.Code, rec.Header().Get("Access-Control-Max-Age"))
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/localauth/`
Expected: 编译失败 `unknown field MediaToken in struct literal`。

- [ ] **Step 3: 改 middleware.go**

1. `Config` 结构体在 `Token` 与 `AllowedOrigins` 之间加：

```go
	// MediaToken 是只放行 GET/HEAD /media/* 的第二个 token，由 sidecar 每次启动自己生成，
	// 拼进发给前端的媒体 URL（<img>/<video> 带不了请求头）。媒体 URL 会出现在 DOM、日志、
	// 交给 Agent 的提示词里，单独一个 token 让它泄露也只能读媒体、不能调接口。为空时不启用。
	MediaToken string
```

2. 在白名单来源分支里、`h.Set("Access-Control-Expose-Headers", …)` 之后加一行：

```go
				// 预检结果缓存 10 分钟：画布拖动时每个 PATCH 都先发一次预检，不缓存会翻倍请求数。
				h.Set("Access-Control-Max-Age", "600")
```

3. `if !tokenOK(r, cfg.Token) {` → `if !tokenOK(r, cfg) {`。

4. 把整个 `tokenOK` 函数（含注释）替换为：

```go
// tokenOK 先看 Authorization: Bearer，没有（或是别的认证方案）再看 ?token=。后者给 <img>/<video>
// 和 WebSocket 握手用——它们带不了自定义头。媒体 token 只对 GET/HEAD /media/* 有效。
func tokenOK(r *http.Request, cfg Config) bool {
	if cfg.Token == "" {
		return false
	}
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if equal(got, cfg.Token) {
		return true
	}
	return cfg.MediaToken != "" && isMediaRead(r) && equal(got, cfg.MediaToken)
}

func equal(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func isMediaRead(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/media/")
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/localauth/ -v`
Expected: 原有 10 个 + 新增 9 个全部 PASS。

- [ ] **Step 5: 改 Tauri 壳**

在 `src-tauri/src/lib.rs` 中：

1. 顶部 import 改为：

```rust
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
```

（删掉原来的 `use std::sync::Mutex;` 和 `use std::time::Duration;`。）

2. `Sidecar` 结构体加字段：

```rust
struct Sidecar {
    info: Mutex<Result<ServerInfo, String>>,
    child: Mutex<Option<CommandChild>>,
    /// sidecar 进程已退出（日志转发任务收到 Terminated 时置位）；退出握手据此判断要不要强杀。
    exited: Arc<AtomicBool>,
}
```

3. 在 `const READY_TIMEOUT` 之后加：

```rust
/// 开发服务器的来源。只在 debug 构建里经 --origins 交给 sidecar；release 包不放行它（P0 遗留）。
pub const DEV_ORIGINS: &str = "tauri://localhost,http://tauri.localhost,http://localhost:5173";

/// 应用退出时等 sidecar 自己收尾（停 HTTP、停后台任务、关数据库）的最长时间，超时再强杀。
const SHUTDOWN_GRACE: Duration = Duration::from_secs(3);

/// sidecar 的命令行参数。debug 构建额外放行 Vite 开发服务器的来源。
pub fn sidecar_args(data_dir: &str, debug: bool) -> Vec<String> {
    let mut args = vec!["--data-dir".to_string(), data_dir.to_string()];
    if debug {
        args.push("--origins".to_string());
        args.push(DEV_ORIGINS.to_string());
    }
    args
}

/// 轮询等 flag 置位，最多等 timeout；返回是否等到。
pub fn wait_flag(flag: &AtomicBool, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        if flag.load(Ordering::SeqCst) {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
}

/// 数据目录只允许当前用户访问：data.db 里有 API Key（spec M3）。
/// Windows 的 %APPDATA% 本身就是用户私有目录，不另设 ACL。
fn make_private(dir: &std::path::Path) -> Result<(), String> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("无法设置数据目录权限：{e}"))?;
    }
    #[cfg(not(unix))]
    let _ = dir;
    Ok(())
}
```

4. `start_sidecar` 的返回类型改为 `Result<(ServerInfo, CommandChild, Arc<AtomicBool>), String>`，函数体内：
   - `std::fs::create_dir_all(&data_dir)…?;` 之后加一行 `make_private(&data_dir)?;`
   - `.args(["--data-dir", &data_dir.to_string_lossy()])` 改为 `.args(sidecar_args(&data_dir.to_string_lossy(), cfg!(debug_assertions)))`
   - 在 `// 握手完成后继续把 sidecar 的日志转到壳的 stderr。` 之前加：

```rust
    let exited = Arc::new(AtomicBool::new(false));
    let exited_flag = exited.clone();
```

   - 日志转发循环里 `CommandEvent::Terminated(payload) => {` 分支内、`eprintln!` 之前加 `exited_flag.store(true, Ordering::SeqCst);`
   - 末尾 `Ok((ServerInfo { port, token }, child))` 改为 `Ok((ServerInfo { port, token }, child, exited))`

5. `setup` 里两个分支改为：

```rust
            let state = match start_sidecar(app.handle()) {
                Ok((info, child, exited)) => Sidecar {
                    info: Mutex::new(Ok(info)),
                    child: Mutex::new(Some(child)),
                    exited,
                },
                // 启动失败不退出进程：让窗口照常打开，由前端显示失败原因。
                Err(reason) => Sidecar {
                    info: Mutex::new(Err(format!("本地服务启动失败：{reason}"))),
                    child: Mutex::new(None),
                    exited: Arc::new(AtomicBool::new(true)),
                },
            };
```

6. `.run(|app, event| { … })` 的闭包体替换为：

```rust
            // 正常退出时先请 sidecar 自己收尾，等不到再强杀；崩溃场景由 sidecar 自己靠 stdin EOF 兜底。
            if let RunEvent::Exit = event {
                let state = app.state::<Sidecar>();
                let child = state.child.lock().unwrap().take();
                if let Some(mut child) = child {
                    let _ = child.write(b"SHUTDOWN\n");
                    if !wait_flag(&state.exited, SHUTDOWN_GRACE) {
                        let _ = child.kill();
                    }
                }
            }
```

7. 在 `mod tests` 末尾追加：

```rust
    #[test]
    fn sidecar_args_adds_dev_origins_only_in_debug() {
        assert_eq!(sidecar_args("/d", false), vec!["--data-dir", "/d"]);
        let dbg = sidecar_args("/d", true);
        assert_eq!(dbg.len(), 4);
        assert_eq!(dbg[2], "--origins");
        assert!(dbg[3].contains("http://localhost:5173"));
        assert!(dbg[3].contains("tauri://localhost"));
    }

    #[test]
    fn wait_flag_returns_when_flag_set() {
        use std::sync::atomic::{AtomicBool, Ordering};
        use std::sync::Arc;
        let flag = Arc::new(AtomicBool::new(false));
        let setter = flag.clone();
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(100));
            setter.store(true, Ordering::SeqCst);
        });
        assert!(wait_flag(&flag, Duration::from_secs(2)));
    }

    #[test]
    fn wait_flag_times_out() {
        let flag = std::sync::atomic::AtomicBool::new(false);
        let start = Instant::now();
        assert!(!wait_flag(&flag, Duration::from_millis(100)));
        assert!(start.elapsed() < Duration::from_secs(1));
    }
```

- [ ] **Step 6: 运行 Rust 测试**

Run: `cd src-tauri && cargo test && cargo build`
Expected: 原有 7 个 + 新增 3 个测试 PASS；`cargo build` 无警告（`make_private` 在 macOS 上走 unix 分支）。

注意：本任务合并后、Task 12 合并前，sidecar 还不认 `SHUTDOWN` 行（P0 的实现把 token 之后的 stdin 全部丢弃），壳会等满 3 秒再强杀——行为正确，只是退出慢 3 秒；Task 12 合并后恢复即时退出。

- [ ] **Step 7: 提交**

```bash
git add server/internal/localauth src-tauri/src/lib.rs
git commit -m "feat: localauth 校验 Bearer 前缀、媒体专用 token、预检缓存；壳收紧数据目录权限、debug 才放行开发源、退出握手"
```

---

### Task 5: http 基础件——helpers、context、本地身份、MediaSigner、缩略图接口、共享测试替身

**Files:**
- Create（原样复制）: `server/internal/http/helpers.go`、`server/internal/http/context.go`、`server/internal/http/media_thumb_handlers.go`、`server/internal/http/media_thumb_handlers_test.go`
- Create（移植）: `server/internal/http/media_signer.go`
- Create（新写）: `server/internal/http/middleware.go`、`server/internal/http/storage_user_repo.go`、`server/internal/http/testfakes_test.go`
- Test: `server/internal/http/media_signer_test.go`（改写）、`server/internal/http/middleware_test.go`
- Modify: `server/internal/http/router.go`（只删 P0 的 `writeJSON` 及其 `encoding/json` import）

**Interfaces:**
- Consumes: `assets.Storage`、`assets.MediaProtocol`、`assets.TransformJSONRaw`、`assets.NewLocalStorage`、`assets.LocalConfig`（Task 1）；`projects.Repository` 等（P1a）
- Produces（Task 8–12 依赖）：
  - `writeJSON(w http.ResponseWriter, status int, body any)`、`writeError(w, status int, message string)`（响应体 `{"error": message}`）、`decodeJSON(r *http.Request, v any) error`。
  - `withProjectID`/`projectIDFromContext`、`withUserID`/`userIDFromContext`、`withActorUserID`/`actorUserIDFromContext`、`withUserRole`/`userRoleFromContext`；`userIDContextKey` 等常量。
  - `const LocalUserID int64 = 1`；`func LocalIdentity(next http.Handler) http.Handler`；`func ProjectFromURL(next http.Handler) http.Handler`（URL 参数 `projectID` 非正整数时 400 `invalid project id`）。
  - `type storageUserRepo interface { AddStorage(id int64, delta int64) error; StorageUsage(id int64) (used int64, quota int64, err error) }`。
  - `const MediaURLExpiry = 24 * time.Hour`；`NewMediaSigner(storage assets.Storage) *MediaSigner`；方法 `Sign(s string) string`、`SignThumb(s string, maxWidth int) string`、`ObjectKey(s string) string`、`Canonicalize(s string) string`、`SignJSON(raw json.RawMessage) json.RawMessage`、`CanonicalizeJSON(raw json.RawMessage) json.RawMessage`；nil 接收者安全。
  - `MediaThumbs(signer *MediaSigner) http.HandlerFunc`；常量 `thumbBatchMax=500`、`thumbMaxWidthCap=2048`、`thumbDefaultWidth=400`。
  - 共享测试替身（`testfakes_test.go`，供 Task 8–11 的测试使用）：`stubStorage`（SignedURL 计数）、`fakeAssetStorage`、`fakeAssetRepo{nextID; onInsert}`、`fakeAssetStorageUserRepo{used, quota}`、`fakeProjectRepository{project, snapshot, mutatePayload, mutatedPayload, mutateVersion, mutateErr, mutateCalled}`、`newProjectSnapshotRequest(t, method, body)`、`newProjectCanvasRequest(t, method, path, body, params)`（两者固定 projectID=42、userID=7）、`withTestUserID(req, uid)`。

**MediaSigner 去掉缓存的理由：** 参考仓的 LRU（5 万条、20 小时）是为了让 24 小时有效的预签名 URL 在一段时间内保持不变、浏览器磁盘缓存能命中，并省掉重复的 HMAC 计算。本地 URL 是确定的：同一次启动内同一 key 永远得到同一 URL，浏览器缓存照样命中；换一次启动端口和 token 都变，缓存跨不了进程；签名本身只是字符串拼接。去掉后少一个依赖（`hashicorp/golang-lru`），行为语义不变。

- [ ] **Step 1: 复制原样文件，删掉 router.go 里重复的 writeJSON**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/helpers.go $REF/http/context.go $REF/http/media_thumb_handlers.go $REF/http/media_thumb_handlers_test.go server/internal/http/
```

在 `server/internal/http/router.go` 中删除整个 `func writeJSON(...) {...}` 函数，并从 import 块删除 `"encoding/json"`（`helpers.go` 提供同名函数；P0 的 ping、diag 调用点签名兼容，响应 `Content-Type` 从 `application/json; charset=utf-8` 变为参考仓的 `application/json`，P0 测试不检查这个头）。

- [ ] **Step 2: 写共享测试替身**

`server/internal/http/testfakes_test.go`（内容搬自参考仓 `asset_handlers_test.go`、`media_signer_test.go`、`project_handlers_test.go`、`skills_handlers_test.go`，集中到这里是因为第三批的四个任务并发开发、各自的测试都要用）：

```go
package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// ─── 存储替身 ────────────────────────────────────────────────────────────────

// stubStorage 统计 SignedURL 的调用次数；其他 Storage 方法都是空 stub。
type stubStorage struct {
	signCalls atomic.Int64
}

func (s *stubStorage) PutObject(context.Context, assets.PutObjectInput) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}
func (s *stubStorage) DeleteObject(context.Context, string) error { return nil }
func (s *stubStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	n := s.signCalls.Add(1)
	return fmt.Sprintf("https://example.test/%s?sig=%d", key, n), nil
}
func (s *stubStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	n := s.signCalls.Add(1)
	return fmt.Sprintf("https://example.test/%s?sig=%d", key, n), nil
}
func (s *stubStorage) ExtractKey(urlOrKey string) (string, bool) {
	// 任何以 "media://" 开头的都识别为我们自己的。
	const prefix = "media://"
	if len(urlOrKey) > len(prefix) && urlOrKey[:len(prefix)] == prefix {
		return urlOrKey[len(prefix):], true
	}
	return "", false
}
func (s *stubStorage) StaticKey(userID int64, filename string) string { return s.NewKey(userID, filename) }
func (s *stubStorage) NewKey(int64, string) string                    { return "" }
func (s *stubStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *stubStorage) CopyObject(context.Context, string, string, string) (assets.StoredObject, error) {
	return assets.StoredObject{}, errors.New("not used")
}

// fakeAssetStorage 是 assets.Storage 的手写 fake，PutObject 直接回声成功，不落盘。
type fakeAssetStorage struct{}

func (fakeAssetStorage) PutObject(_ context.Context, input assets.PutObjectInput) (assets.StoredObject, error) {
	_, size := input.Content()
	return assets.StoredObject{URL: "https://example.com/" + input.Key, Size: size}, nil
}
func (fakeAssetStorage) DeleteObject(context.Context, string) error { return nil }
func (fakeAssetStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return key, nil
}
func (fakeAssetStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, _ int) (string, error) {
	return key, nil
}
func (fakeAssetStorage) ExtractKey(urlOrKey string) (string, bool) { return urlOrKey, true }
func (fakeAssetStorage) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (fakeAssetStorage) StaticKey(userID int64, filename string) string { return filename }
func (fakeAssetStorage) NewKey(userID int64, filename string) string    { return filename }
func (fakeAssetStorage) CopyObject(_ context.Context, _, dstKey, _ string) (assets.StoredObject, error) {
	return assets.StoredObject{URL: dstKey}, nil
}

// ─── assets / users 仓库替身 ─────────────────────────────────────────────────

// fakeAssetRepo 是 assets.Repository 的手写 fake；onInsert 钩子让测试观察 Insert 收到的记录。
type fakeAssetRepo struct {
	nextID   int64
	onInsert func(rec assets.AssetRecord)
}

func (f *fakeAssetRepo) Insert(rec assets.AssetRecord) (int64, error) {
	if f.onInsert != nil {
		f.onInsert(rec)
	}
	f.nextID++
	return f.nextID, nil
}
func (f *fakeAssetRepo) ListForUser(int64, int) ([]assets.AssetRecord, error) { return nil, nil }
func (f *fakeAssetRepo) ListForProject(int64) ([]assets.AssetRecord, error)   { return nil, nil }
func (f *fakeAssetRepo) DeleteForProject(int64) (int64, error)                { return 0, nil }
func (f *fakeAssetRepo) ListForTask(int64) ([]assets.AssetRecord, error)      { return nil, nil }
func (f *fakeAssetRepo) DeleteForTask(int64) (int64, error)                   { return 0, nil }
func (f *fakeAssetRepo) ListByObjectKey(string) ([]assets.AssetRecord, error) { return nil, nil }
func (f *fakeAssetRepo) ClearLibraryRef(int64) error                          { return nil }
func (f *fakeAssetRepo) FindByUserProjectHash(int64, int64, string) (*assets.AssetRecord, error) {
	// 总是走"未命中"分支，让 Upload 走真实写入路径而不是去重短路。
	return nil, sql.ErrNoRows
}
func (f *fakeAssetRepo) FindByTaskSlot(int64, int) (*assets.AssetRecord, error) {
	return nil, sql.ErrNoRows
}
func (f *fakeAssetRepo) InsertSlotTx(*sql.Tx, assets.AssetRecord, int) (int64, bool, error) {
	return 0, false, nil
}
func (f *fakeAssetRepo) GetDurationMsByObjectKey(string) (int64, bool, error) { return 0, false, nil }
func (f *fakeAssetRepo) SetDurationMsByObjectKey(string, int64) error         { return nil }

// fakeAssetStorageUserRepo 是 storageUserRepo 的手写 fake。零值 = 配额充裕（quota 1GB）。
type fakeAssetStorageUserRepo struct {
	used, quota int64
}

func (fakeAssetStorageUserRepo) AddStorage(int64, int64) error { return nil }
func (f fakeAssetStorageUserRepo) StorageUsage(int64) (int64, int64, error) {
	if f.quota == 0 {
		return f.used, 1 << 30, nil
	}
	return f.used, f.quota, nil
}

// ─── projects 仓库替身与请求构造 ─────────────────────────────────────────────

type fakeProjectRepository struct {
	project        *projects.Project
	snapshot       *projects.Snapshot
	mutatePayload  json.RawMessage
	mutatedPayload json.RawMessage
	mutateVersion  int64
	mutateErr      error
	mutateCalled   bool
}

func (f *fakeProjectRepository) Create(int64, string, string) (int64, error) { return 0, nil }
func (f *fakeProjectRepository) ListForUser(int64, int, int) ([]projects.Project, int, error) {
	return nil, 0, nil
}
func (f *fakeProjectRepository) GetForUser(_, _ int64) (*projects.Project, error) {
	if f.project == nil {
		return nil, projects.ErrNotFound
	}
	return f.project, nil
}
func (f *fakeProjectRepository) Update(int64, int64, string, string) error { return nil }
func (f *fakeProjectRepository) SetCover(int64, int64, string) error       { return nil }
func (f *fakeProjectRepository) Delete(int64, int64) error                 { return nil }
func (f *fakeProjectRepository) SaveAuthoritative(int64, json.RawMessage) (int64, error) {
	return 0, nil
}
func (f *fakeProjectRepository) MutateSnapshot(int64, func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	return 0, nil
}
func (f *fakeProjectRepository) MutateClientSnapshot(_ int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	f.mutateCalled = true
	if f.mutateErr != nil {
		return 0, f.mutateErr
	}
	next, changed, err := fn(f.mutatePayload)
	if err != nil {
		return 0, err
	}
	if changed {
		f.mutatedPayload = next
	}
	if f.mutateVersion != 0 {
		return f.mutateVersion, nil
	}
	return 1, nil
}
func (f *fakeProjectRepository) GetLatest(int64) (*projects.Snapshot, error) { return f.snapshot, nil }
func (f *fakeProjectRepository) ListPendingDeletion(int) ([]projects.PendingProject, error) {
	return nil, nil
}
func (f *fakeProjectRepository) HardDelete(int64) error { return nil }

func newProjectSnapshotRequest(t *testing.T, method string, body []byte) *http.Request {
	t.Helper()
	if body == nil {
		body = []byte{}
	}
	req := httptest.NewRequest(method, "/api/v1/projects/42/snapshot", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "42")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withProjectID(ctx, 42)
	return req.WithContext(withUserID(ctx, 7))
}

func newProjectCanvasRequest(t *testing.T, method string, path string, body []byte, params map[string]string) *http.Request {
	t.Helper()
	if body == nil {
		body = []byte(`{}`)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "42")
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withProjectID(ctx, 42)
	return req.WithContext(withUserID(ctx, 7))
}

// withTestUserID 通过请求 context 注入一个 uid，让 handler 的 userIDFromContext 能看到调用方。
func withTestUserID(req *http.Request, uid int64) *http.Request {
	ctx := context.WithValue(req.Context(), userIDContextKey, uid)
	return req.WithContext(ctx)
}
```

- [ ] **Step 3: 写失败的测试**

`server/internal/http/media_signer_test.go`：

```go
package http

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

func TestMediaSigner_SignsThroughStorage(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	if got := ms.Sign("media://key/a.png"); got != "https://example.test/key/a.png?sig=1" {
		t.Fatalf("Sign=%q", got)
	}
	if got := ms.SignThumb("media://key/a.png", 400); !strings.HasPrefix(got, "https://example.test/key/a.png") {
		t.Fatalf("SignThumb=%q", got)
	}
}

func TestMediaSigner_DifferentKeysIndependent(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	a := ms.Sign("media://key/a.png")
	b := ms.Sign("media://key/b.png")
	if a == b {
		t.Fatalf("different keys should hash to different signed URLs, both got %q", a)
	}
	if got := st.signCalls.Load(); got != 2 {
		t.Fatalf("expected 2 SignedURL calls (one per key), got %d", got)
	}
}

func TestMediaSigner_NonMediaPassesThrough(t *testing.T) {
	st := &stubStorage{}
	ms := NewMediaSigner(st)
	external := "https://other-cdn.example/foo.png"
	if got := ms.Sign(external); got != external {
		t.Fatalf("external URL must pass unchanged, got %q", got)
	}
	if got := st.signCalls.Load(); got != 0 {
		t.Fatalf("non-media URL must not invoke SignedURL, got %d calls", got)
	}
}

func newLocalSigner(t *testing.T) *MediaSigner {
	t.Helper()
	st, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewMediaSigner(st)
}

// canonical（media://）是唯一允许落库的形态；不论 URL 来自哪次启动（端口不同）都要认回来。
func TestMediaSigner_CanonicalizeLocalURLs(t *testing.T) {
	ms := newLocalSigner(t)
	cases := map[string]string{
		"http://127.0.0.1:5555/media/u1/a.png?token=mt":    "media://u1/a.png",
		"http://127.0.0.1:40000/media/u1/a.png?token=old":  "media://u1/a.png",
		"media://u1/a.png":                                 "media://u1/a.png",
		"https://cdn.example/x.png":                        "https://cdn.example/x.png",
		"http://127.0.0.1:5555/media/%E9%95%9C%E5%A4%B4.png": "media://镜头.png",
	}
	for in, want := range cases {
		if got := ms.Canonicalize(in); got != want {
			t.Errorf("Canonicalize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMediaSigner_JSONRoundTrip(t *testing.T) {
	ms := newLocalSigner(t)
	raw := json.RawMessage(`{"nodes":[{"content":"media://u1/a.png","history":["media://u1/b.png","https://cdn.example/c.png"]}]}`)
	signed := ms.SignJSON(raw)
	if strings.Contains(string(signed), "media://") || !strings.Contains(string(signed), "http://127.0.0.1:5555/media/u1/a.png?token=mt") {
		t.Fatalf("signed=%s", signed)
	}
	var want, got any
	json.Unmarshal(raw, &want)
	json.Unmarshal(ms.CanonicalizeJSON(signed), &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("往返不一致: %v vs %v", want, got)
	}
}

func TestMediaSigner_ObjectKey(t *testing.T) {
	ms := newLocalSigner(t)
	if got := ms.ObjectKey("http://127.0.0.1:5555/media/u1/a%20b.png?token=mt"); got != "u1/a b.png" {
		t.Fatalf("got %q", got)
	}
	if got := ms.ObjectKey("data:image/png;base64,AAAA"); got != "" {
		t.Fatalf("got %q", got)
	}
	var nilSigner *MediaSigner
	if nilSigner.ObjectKey("media://x") != "" || nilSigner.Sign("media://x") != "media://x" {
		t.Fatal("nil signer 必须安全")
	}
}
```

`server/internal/http/middleware_test.go`：

```go
package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestLocalIdentityInjectsLocalAdmin(t *testing.T) {
	var uid, actor int64 = 0, -1
	var role string
	h := LocalIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid = userIDFromContext(r.Context())
		role = userRoleFromContext(r.Context())
		actor = actorUserIDFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/me", nil))
	if uid != LocalUserID || uid != 1 || role != "admin" || actor != 0 {
		t.Fatalf("uid=%d role=%q actor=%d；want 1/admin/0（个人身份，团队分支不可达）", uid, role, actor)
	}
}

func TestProjectFromURL(t *testing.T) {
	var got int64
	r := chi.NewRouter()
	r.With(ProjectFromURL).Get("/p/{projectID}", func(w http.ResponseWriter, r *http.Request) {
		got = projectIDFromContext(r.Context())
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/p/42", nil))
	if rec.Code != http.StatusOK || got != 42 {
		t.Fatalf("code=%d got=%d", rec.Code, got)
	}
	for _, bad := range []string{"/p/abc", "/p/0", "/p/-3"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code=%d want 400", bad, rec.Code)
		}
	}
}
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `cd server && go test ./internal/http/`
Expected: 编译失败，`undefined: NewMediaSigner`、`undefined: LocalIdentity`。

- [ ] **Step 5: 写实现**

`server/internal/http/storage_user_repo.go`：

```go
package http

// storageUserRepo 是上传、项目、素材库各 handler 共用的存储用量接口（参考仓定义在 asset_handlers.go，
// 挪到独立文件是因为多个任务的 handler 并发移植、都要用它）。
type storageUserRepo interface {
	AddStorage(id int64, delta int64) error
	// StorageUsage 取 (used, quota) 做上传前的配额预检——记账(AddStorage)与
	// 预检必须来自同一个计数器，否则闸和账各说各话。
	StorageUsage(id int64) (used int64, quota int64, err error)
}
```

`server/internal/http/middleware.go`：

```go
package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// LocalUserID 是桌面端唯一的本地用户（spec X4；迁移 0001 预置 users.id=1，role=admin）。
const LocalUserID int64 = 1

// LocalIdentity 取代参考仓的 AuthMiddleware.Require：桌面端没有登录，请求能走到这里说明
// 已经过了 localauth 的 token + Origin 校验，直接注入本地用户身份。
// 不写 actor——走个人路径，handler 里的团队分支全部不可达（spec X2）。
func LocalIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUserID(r.Context(), LocalUserID)
		ctx = withUserRole(ctx, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ProjectFromURL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "projectID")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid project id")
			return
		}
		next.ServeHTTP(w, r.WithContext(withProjectID(r.Context(), id)))
	})
}
```

`server/internal/http/media_signer.go`：

```go
package http

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// MediaURLExpiry 是传给 storage.SignedURL 的有效期参数。本地 URL 不过期（有效性由每次启动的
// 媒体 token 决定），保留这个常量只为与参考仓的调用点一致。
const MediaURLExpiry = 24 * time.Hour

// MediaSigner 在 API 边界上，于 `media://<key>`（canonical 的 DB 形式）和 sidecar 本地媒体 URL
// （出口形式 http://127.0.0.1:<端口>/media/<key>?token=…）之间互转。
//
// 与参考仓的差异：去掉了 LRU 缓存。参考仓缓存是为了让预签名 URL 在 20 小时内保持稳定、浏览器
// 磁盘缓存能命中；本地 URL 本身就是确定的（同一次启动内同一 key 永远同一 URL），换一次启动端口和
// token 都变，缓存跨不了进程，签名也只是字符串拼接。
type MediaSigner struct {
	storage assets.Storage
	ctx     context.Context
}

func NewMediaSigner(storage assets.Storage) *MediaSigner {
	return &MediaSigner{storage: storage, ctx: context.Background()}
}

// Sign 解析单个字符串：如果它是我们的媒体（`media://key`，或任意一次启动发出的本地媒体 URL），
// 返回本次启动的本地 URL；否则原样返回。
func (m *MediaSigner) Sign(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	url, err := m.storage.SignedURL(m.ctx, key, MediaURLExpiry)
	if err != nil || url == "" {
		return s
	}
	return url
}

// SignThumb 与 Sign 同理，出缩略图 URL；本地存储不生成缩略图，返回的就是原图 URL（spec S8）。
func (m *MediaSigner) SignThumb(s string, maxWidth int) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	url, err := m.storage.SignedThumbURL(m.ctx, key, MediaURLExpiry, maxWidth)
	if err != nil || url == "" {
		return s
	}
	return url
}

// ObjectKey 把任意形态的媒体引用（media:// 伪 URL、本地 URL）还原成裸 object key。
// 认不出（外部 URL、data: 等）返回空串。
func (m *MediaSigner) ObjectKey(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return ""
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok {
		return ""
	}
	return key
}

// Canonicalize 是逆操作：把本地媒体 URL 转回 `media://<key>` 形式以便持久化。外部 URL 原样返回。
func (m *MediaSigner) Canonicalize(s string) string {
	if m == nil || m.storage == nil || s == "" {
		return s
	}
	if strings.HasPrefix(s, assets.MediaProtocol) {
		return s
	}
	key, ok := m.storage.ExtractKey(s)
	if !ok || key == "" {
		return s
	}
	return assets.MediaProtocol + key
}

// SignJSON 遍历一个 JSON raw message，给找到的每个字符串签名。
func (m *MediaSigner) SignJSON(raw json.RawMessage) json.RawMessage {
	if m == nil {
		return raw
	}
	return assets.TransformJSONRaw(raw, m.Sign)
}

// CanonicalizeJSON 遍历一个 JSON raw message，把每个字符串 canonicalize。
func (m *MediaSigner) CanonicalizeJSON(raw json.RawMessage) json.RawMessage {
	if m == nil {
		return raw
	}
	return assets.TransformJSONRaw(raw, m.Canonicalize)
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `cd server && go vet ./internal/http/ && go test ./internal/http/ -race -v`
Expected: P0 的 8 个路由测试 + `media_thumb_handlers_test` 3 个 + `media_signer_test` 6 个 + `middleware_test` 2 个全部 PASS。

- [ ] **Step 7: 提交**

```bash
git add server/internal/http/helpers.go server/internal/http/context.go server/internal/http/middleware.go \
  server/internal/http/middleware_test.go server/internal/http/storage_user_repo.go server/internal/http/media_signer.go \
  server/internal/http/media_signer_test.go server/internal/http/media_thumb_handlers.go \
  server/internal/http/media_thumb_handlers_test.go server/internal/http/testfakes_test.go server/internal/http/router.go
git commit -m "feat(server): http 基础件——本地身份中间件、MediaSigner（本地 URL）、缩略图接口与共享测试替身"
```

---

### Task 6: users——本地用户与存储用量

**Files:**
- Create: `server/internal/users/repository.go`
- Test: `server/internal/users/repository_test.go`

**Interfaces:**
- Consumes: `dbtest.Open`（P1a）；表 `library_items`（Task 2）
- Produces（Task 8/9/11/12 依赖）：`users.User{ID int64; Email, Status, Role string; StorageQuotaBytes, StorageUsedBytes, BalanceCredits, FrozenCredits int64; UpstreamAPIKey string; CreatedAt, UpdatedAt time.Time}`；`users.ErrNotFound`；`users.NewSQLiteRepository(db *sql.DB) *SQLiteRepository`；方法 `GetByID(id int64) (*User, error)`、`AddStorage(id, delta int64) error`、`AddStorageTx(tx *sql.Tx, id, delta int64) error`、`StorageUsage(id int64) (used, quota int64, err error)`（无此用户返回 `ErrNotFound`）、`ComputeStorageUsed(id int64) (int64, error)`（assets + library_items 实时求和）。`*SQLiteRepository` 满足 http 包的 `storageUserRepo` 与 workflows 的 `userStorageAdjuster`。

参考仓 `users.Repository` 有 24 个方法（注册、团队影子用户、会员配额、改密、上游 Key……），画布链路只用到上面这几个；其余随登录、会员、团队一起删除（spec X3、X4）。`GetUpstreamKey` 等 P1c 需要时再加。不定义 `Repository` 接口——各使用方都声明了自己的窄接口。

- [ ] **Step 1: 写失败的测试**

`server/internal/users/repository_test.go`：

```go
package users

import (
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestGetByIDReturnsLocalAdmin(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	u, err := repo.GetByID(1)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "admin" || u.Email != "local@miaoju.desktop" || u.StorageQuotaBytes != 1125899906842624 || u.CreatedAt.IsZero() {
		t.Fatalf("u=%+v", u)
	}
	if _, err := repo.GetByID(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestAddStorageAndUsage(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	if err := repo.AddStorage(1, 100); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddStorage(1, -30); err != nil {
		t.Fatal(err)
	}
	tx, _ := conn.Begin()
	if err := repo.AddStorageTx(tx, 1, 5); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	used, quota, err := repo.StorageUsage(1)
	if err != nil || used != 75 || quota != 1125899906842624 {
		t.Fatalf("used=%d quota=%d err=%v", used, quota, err)
	}
	if _, _, err := repo.StorageUsage(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

// /me 展示的用量从 assets + library_items 实时算，不信增量计数器（会漂移）。
func TestComputeStorageUsedSumsAssetsAndLibrary(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'a', 'media://a', 100, 'image/png'), (1, 'b', 'media://b', 0, 'image/png')`)
	res, _ := conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'f')`)
	fid, _ := res.LastInsertId()
	conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind) VALUES (1, ?, 'c', 'media://c', 23, 'image/png', 'image')`, fid)
	got, err := repo.ComputeStorageUsed(1)
	if err != nil || got != 123 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if got, _ := repo.ComputeStorageUsed(5); got != 0 {
		t.Fatalf("无数据的用户应为 0，got %d", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./internal/users/`
Expected: 编译失败 `undefined: NewSQLiteRepository`。

- [ ] **Step 3: 写实现**

`server/internal/users/repository.go`：

```go
// Package users 是桌面端的本地用户仓库：只有 id=1 一个用户（spec X4，迁移 0001 预置），
// 只保留画布链路用到的读取与存储用量方法。参考仓的注册、团队影子用户、会员配额、改密等
// 方法随登录与会员一起删除（spec X3、X4）。SQL 与参考仓同名方法逐字一致。
package users

import (
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID                int64
	Email             string
	Status            string
	Role              string
	StorageQuotaBytes int64
	StorageUsedBytes  int64
	BalanceCredits    int64
	FrozenCredits     int64
	// UpstreamAPIKey 非空 = 该用户上游调用用自己的 SK；空 = 回落全局 settings.upstream_api_key（P1c 用）。
	UpstreamAPIKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

var ErrNotFound = errors.New("user not found")

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// GetByID 读一个用户。桌面端 users 表没有参考仓的 kind、password_changed_at 列，这里只选存在的列。
func (r *SQLiteRepository) GetByID(id int64) (*User, error) {
	var u User
	err := r.db.QueryRow(`SELECT id, email, status, role, storage_quota_bytes, storage_used_bytes,
		balance_credits, frozen_credits, COALESCE(upstream_api_key, ''), created_at, updated_at
		FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.Status, &u.Role, &u.StorageQuotaBytes, &u.StorageUsedBytes,
			&u.BalanceCredits, &u.FrozenCredits, &u.UpstreamAPIKey, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AddStorage 无条件地按 delta 调整 storage_used_bytes。想要强制执行配额上限的调用方必须在调用前
// 显式做预检查（StorageUsage → 比较 used+delta 与 quota）。
func (r *SQLiteRepository) AddStorage(id int64, delta int64) error {
	_, err := r.db.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// AddStorageTx 在调用方事务里累计用量（与 asset 行插入同事务，P1c 的生成产物落库用）。
func (r *SQLiteRepository) AddStorageTx(tx *sql.Tx, id int64, delta int64) error {
	_, err := tx.Exec(`UPDATE users SET storage_used_bytes = storage_used_bytes + ? WHERE id = ?`, delta, id)
	return err
}

// StorageUsage 一条 SELECT 取回 (used, quota)，供各上传入口做配额预检。
// 本地用户的 quota 是迁移预置的 1 PiB，预检实际上永远放行。
func (r *SQLiteRepository) StorageUsage(id int64) (int64, int64, error) {
	var used, quota int64
	if err := r.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id = ?`, id).Scan(&used, &quota); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}
	return used, quota, nil
}

// ComputeStorageUsed 返回某用户权威的存储用量——assets 加上 library_items 的实时 SUM，
// 用于展示，保证这个数字永远不会和计数器漂移。
func (r *SQLiteRepository) ComputeStorageUsed(id int64) (int64, error) {
	var assetsSum, librarySum int64
	if err := r.db.QueryRow(`SELECT IFNULL(SUM(size_bytes), 0) FROM assets WHERE user_id = ?`, id).Scan(&assetsSum); err != nil {
		return 0, err
	}
	if err := r.db.QueryRow(`SELECT IFNULL(SUM(size_bytes), 0) FROM library_items WHERE user_id = ?`, id).Scan(&librarySum); err != nil {
		return 0, err
	}
	return assetsSum + librarySum, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/users/ -v`
Expected: 3 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add server/internal/users
git commit -m "feat(server): 本地用户仓库（存储用量子集）"
```

---

### Task 7: workflows——项目副本用的 AssetDuplicator

**Files:**
- Create（原样复制）: `server/internal/workflows/duplicator.go`、`server/internal/workflows/duplicator_test.go`
- Create（新写）: `server/internal/workflows/cleanup_local.go`
- Test: `server/internal/workflows/cleanup_local_test.go`
- Modify: `server/go.mod`、`server/go.sum`（加 `golang.org/x/sync v0.20.0`）

**Interfaces:**
- Consumes: `assets.Storage`、`assets.Repository`、`assets.TransformJSONRaw`、`assets.NewLocalStorage`（Task 1）
- Produces（Task 9/12 依赖）：原样 `workflows.NewAssetDuplicator(storage assets.Storage, assetsRepo assets.Repository, users userStorageAdjuster, gc cleanupQueue) *AssetDuplicator`、`(*AssetDuplicator).Duplicate(ctx, targetUserID, targetProjectID int64, sourceKind string, snapshot json.RawMessage) (*DuplicateResult, error)`、`(*AssetDuplicator).EstimateBytes(snapshot json.RawMessage) (int64, error)`、`DuplicateResult{Snapshot json.RawMessage; NewAssetIDs []int64; BytesCopied int64}`；新增 `workflows.NewDeleteNowQueue(storage interface{ DeleteObject(ctx context.Context, key string) error }) *DeleteNowQueue`，`(*DeleteNowQueue).EnqueueObjectKey(key string) error`（永远返回 nil）。

spec X1 删除了发布工作流，但“项目副本”（`POST /projects/{id}/duplicate`）复用同一个 duplicator，所以只搬 `duplicator.go`。参考仓失败回滚把已复制对象写进 `cleanup_jobs` 表、由 30 秒一拍的 worker 删除（对象存储删除可能瞬时失败）；本地删文件是同步、几乎不失败的系统调用，所以 `DeleteNowQueue` 直接删，不建 `cleanup_jobs` 表、不起 worker。

- [ ] **Step 1: 复制文件并加依赖**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/workflows
cp $REF/workflows/duplicator.go $REF/workflows/duplicator_test.go server/internal/workflows/
cd server && go get golang.org/x/sync@v0.20.0
```

若 `go get` 因网络失败：该版本已在本机模块缓存（`~/go/pkg/mod/golang.org/x/sync@v0.20.0`），改用 `GOPROXY=off GOFLAGS=-mod=mod go get golang.org/x/sync@v0.20.0`。

- [ ] **Step 2: 写失败的测试**

`server/internal/workflows/cleanup_local_test.go`：

```go
package workflows

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

type recordingDeleter struct {
	keys []string
	err  error
}

func (d *recordingDeleter) DeleteObject(_ context.Context, key string) error {
	d.keys = append(d.keys, key)
	return d.err
}

func TestDeleteNowQueueDeletesImmediately(t *testing.T) {
	d := &recordingDeleter{}
	q := NewDeleteNowQueue(d)
	if err := q.EnqueueObjectKey("u1/x.png"); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueObjectKey(""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.keys, []string{"u1/x.png"}) {
		t.Fatalf("keys=%v（空 key 不应触发删除）", d.keys)
	}
}

// 删除失败只记日志：走到回滚时调用方的主操作已经失败，不能再用回滚错误覆盖它。
func TestDeleteNowQueueSwallowsErrors(t *testing.T) {
	q := NewDeleteNowQueue(&recordingDeleter{err: errors.New("busy")})
	if err := q.EnqueueObjectKey("u1/x.png"); err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	var nilQueue *DeleteNowQueue
	if err := nilQueue.EnqueueObjectKey("k"); err != nil {
		t.Fatal(err)
	}
}

// 复制到一半失败：磁盘上只能剩源文件，已复制出去的副本必须被删掉，否则是永远没人删的泄漏。
func TestDuplicateRollbackRemovesCopiedFilesFromDisk(t *testing.T) {
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var want []string
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("src/%d.png", i)
		if _, err := storage.PutObject(ctx, assets.PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		want = append(want, key)
	}
	repo := &fakeAssetsRepo{insertAll: true} // 每个对象都“已复制但没入库”
	d := NewAssetDuplicator(storage, repo, &fakeStorageAdjuster{}, NewDeleteNowQueue(storage))
	if _, err := d.Duplicate(ctx, 2, 9, "project_duplicate", snapshotWithKeys(5)); err == nil {
		t.Fatal("期望 Duplicate 返回错误")
	}
	var files []string
	filepath.WalkDir(storage.Root(), func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(storage.Root(), p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("回滚后磁盘上的文件=%v want 只剩源文件 %v", files, want)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/workflows/`
Expected: 编译失败 `undefined: NewDeleteNowQueue`。

- [ ] **Step 4: 写 cleanup_local.go**

`server/internal/workflows/cleanup_local.go`：

```go
package workflows

import (
	"context"
	"log"
)

type objectDeleter interface {
	DeleteObject(ctx context.Context, key string) error
}

// DeleteNowQueue 是桌面端的 cleanupQueue。参考仓把失败复制留下的对象写进 cleanup_jobs 表，由
// worker 异步重试删除（对象存储的删除可能瞬时失败）；本地删文件是同步、几乎不失败的系统调用，
// 所以直接删。删不掉只记日志：这个对象没有任何 asset 行引用，孤儿清扫也看不到它，只能靠日志排查
// （见计划“风险”一节）。
type DeleteNowQueue struct {
	storage objectDeleter
}

func NewDeleteNowQueue(storage objectDeleter) *DeleteNowQueue {
	return &DeleteNowQueue{storage: storage}
}

// EnqueueObjectKey 立即删除 key 对应的文件；永远返回 nil——走到这里时调用方的主操作已经失败，
// 不该再用回滚的错误覆盖它（与参考仓 CleanupRepo.EnqueueObjectKey 吞错的口径一致）。
func (q *DeleteNowQueue) EnqueueObjectKey(key string) error {
	if q == nil || q.storage == nil || key == "" {
		return nil
	}
	if err := q.storage.DeleteObject(context.Background(), key); err != nil {
		log.Printf("duplicate rollback: DeleteObject %q: %v", key, err)
	}
	return nil
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd server && go test ./internal/workflows/ -race -v`
Expected: 参考仓 `duplicator_test.go` 的 4 个 + 新增 3 个全部 PASS。

- [ ] **Step 6: 提交**

```bash
git add server/go.mod server/go.sum server/internal/workflows
git commit -m "feat(server): 移植项目副本 AssetDuplicator，失败回滚直接删本地文件"
```

---

### Task 8: 资产上传、媒体服务、“在访达中显示”、/me

**Files:**
- Create（移植）: `server/internal/http/asset_handlers.go`、`server/internal/http/asset_handlers_test.go`
- Create（新写）: `server/internal/http/media_handlers.go`、`server/internal/http/me_handlers.go`
- Create（新写）: `server/internal/reveal/reveal.go`、`reveal_darwin.go`、`reveal_windows.go`、`reveal_other.go`
- Test: `server/internal/http/media_handlers_test.go`、`server/internal/http/me_handlers_test.go`、`server/internal/reveal/reveal_test.go`

**Interfaces:**
- Consumes: `assets.*`（Task 1）；`storageUserRepo`、`MediaSigner`、`writeJSON/writeError/decodeJSON`、`withUserID`、`LocalUserID`、`fakeAssetStorage`/`fakeAssetRepo`/`fakeAssetStorageUserRepo`（Task 5）；`users.SQLiteRepository`、`users.User`（Task 6）；表 `library_items`（Task 2）
- Produces（Task 12 依赖）：
  - `NewAssetHandler(storage assets.Storage, repo assets.Repository, users storageUserRepo, signer *MediaSigner) *AssetHandler`；`(*AssetHandler).Upload`（`POST /api/v1/assets`，multipart 字段 `file`，可选 `?projectId=`）、`(*AssetHandler).List`（`GET /api/v1/assets`）。响应体与参考仓逐字一致。
  - `NewMediaHandler(files mediaFileStore, signer *MediaSigner, mimes mediaMimeStore, reveal func(path string) error) *MediaHandler`，其中 `mediaFileStore = interface{ Path(key string) (string, error) }`（`*assets.LocalStorage` 满足）、`mediaMimeStore = interface{ MimeByObjectKey(objectKey string) (string, error) }`；`(*MediaHandler).Serve`（挂 `GET`/`HEAD /media/*`）、`(*MediaHandler).Reveal`（`POST /api/v1/media/reveal`，body `{"url": "<本地 URL 或 media://key>"}`，成功 204）。
  - `NewSQLMediaMimeStore(db *sql.DB) *SQLMediaMimeStore`。
  - `NewMeHandler(users meUserRepo) *MeHandler`（`meUserRepo = interface{ GetByID(int64) (*users.User, error); ComputeStorageUsed(int64) (int64, error) }`）；`(*MeHandler).Me`（`GET /api/v1/me`）。
  - `reveal.InFileManager(path string) error`。

**“在访达中显示”的设计决定：** 由 sidecar 换算并打开文件夹（macOS `open -R <path>`，Windows `explorer.exe /select,"<path>"`），而不是把真实路径交给前端、再由 Tauri 的 opener 插件打开。理由：(1) spec SEC4 要求 Tauri capabilities 最小授权——这样壳不需要新增任何权限，WebView 也拿不到可以打开任意路径的能力；(2) 数据目录可改（spec S2），Tauri capability 的路径 scope 是静态的，跟不上；(3) 真实路径不出 sidecar。这与 SEC4 文字里“capabilities 包含在访达中显示”字面不同，效果是授权面更小，请评审确认。

**媒体服务的约定：** key 取自 `r.URL.Path` 去掉 `/media/` 前缀（已解码），交给 `Path` 校验，非法或文件不存在一律 404（不区分原因）。`Content-Type` 依次取：`assets.mime_type`（最新一行）→ `library_items.mime_type` → 扩展名 → 前 512 字节嗅探；值为 `application/octet-stream` 时视为未知继续往下。Range、HEAD、`If-Modified-Since` 交给 `http.ServeContent`。另加 `X-Content-Type-Options: nosniff`、`Cache-Control: private, max-age=86400` 和 `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`——用户上传的 SVG/HTML 若被当作页面直接打开，脚本也跑不起来（嵌在 `<img>`/`<video>` 里时这个头不生效，不影响展示）。

**上传的两处改动：** (1) 探测时长从“失败即拒绝”改为“尽力而为”，默认不探测；(2) 单个文件上限 1 GiB，超过返回 413（参考仓靠 nginx 的 body 上限挡，sidecar 前面没有 nginx，`io.ReadAll` 会把整个文件读进内存）。

- [ ] **Step 1: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/asset_handlers.go $REF/http/asset_handlers_test.go server/internal/http/
mkdir -p server/internal/reveal
```

- [ ] **Step 2: 改写 asset_handlers_test.go（先让测试失败）**

在 `server/internal/http/asset_handlers_test.go` 中：

1. import 块整体替换为（删掉的 `database/sql`、`io`、`time`、`mediaprobe` 只被搬走的替身或删掉的测试用到）：

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)
```

2. 删除 `fakeAssetRepo` 类型及其全部方法、`fakeAssetStorage` 类型及其全部方法、`fakeAssetStorageUserRepo` 类型及其方法（已在 Task 5 的 `testfakes_test.go`）。保留 `newTestAssetHandler`、`doUpload`、`trackingAssetStorage`、`dedupAssetRepo` 和其余测试。
3. 删除 `TestUpload_ProbeFailureRejects` 和 `TestUpload_ProbeFailureDoesNotOrphanObject` 两个测试，在原位置加入：

```go
// 桌面端探测失败只是不记时长，上传照常成功（spec X3 不计费；用户机器上没有 ffprobe）。
func TestUpload_ProbeFailureStillStoresWithoutDuration(t *testing.T) {
	gotDuration := int64(-1)
	h := newTestAssetHandler(t)
	h.probe = func(ctx context.Context, b []byte) (int64, error) { return 0, errors.New("ffprobe unavailable") }
	h.repo.(*fakeAssetRepo).onInsert = func(rec assets.AssetRecord) { gotDuration = rec.DurationMs }
	rec := doUpload(t, h, "clip.mp4", "video/mp4", []byte("v"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if gotDuration != 0 {
		t.Fatalf("探测失败应不记时长，duration_ms=%d", gotDuration)
	}
}

func TestUpload_NoProbeConfiguredStoresVideo(t *testing.T) {
	h := newTestAssetHandler(t) // NewAssetHandler 默认不探测
	if rec := doUpload(t, h, "clip.mp4", "video/mp4", []byte("v")); rec.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
}

// sidecar 前面没有 nginx 挡 body 大小，超限必须在读进内存之前拒绝，且不写存储。
func TestUpload_TooLargeReturns413(t *testing.T) {
	storage := &trackingAssetStorage{}
	h := NewAssetHandler(storage, &fakeAssetRepo{}, fakeAssetStorageUserRepo{}, NewMediaSigner(storage))
	h.maxUploadBytes = 64
	rec := doUpload(t, h, "big.png", "image/png", bytes.Repeat([]byte("x"), 1024))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body=%s，want 413", rec.Code, rec.Body.String())
	}
	if storage.putCalled {
		t.Error("超限不应写存储")
	}
}
```

Run: `cd server && go test ./internal/http/ -run 'Upload|WebAsset'`
Expected: 编译失败——`asset_handlers.go` 里 `storageUserRepo` 重复声明、`h.maxUploadBytes` 未定义、`mediaprobe` 包不存在。

- [ ] **Step 3: 改写 asset_handlers.go**

在 `server/internal/http/asset_handlers.go` 中：

1. 删除 import `"github.com/james/huabu_studio/apps/api/internal/mediaprobe"`。
2. 删除整个 `type storageUserRepo interface { … }` 声明（Task 5 已移到 `storage_user_repo.go`）。
3. 在 `type AssetHandler struct` 之前加：

```go
// assetMaxUploadBytes 是单个上传文件的上限。参考仓靠 nginx 的 body 上限挡；sidecar 前面没有
// nginx，而下面 readUploadForm 会把整个文件读进内存，必须在 handler 里限住。
const assetMaxUploadBytes int64 = 1 << 30
```

4. `AssetHandler` 结构体里把 `probe` 字段的注释改为：

```go
	// probe 可选（nil = 不探测时长）。参考仓用系统 ffprobe 取权威时长防按秒计费被篡改的元数据骗；
	// 桌面端不计费（spec X3），用户机器上也没有 ffprobe，所以默认不探测；探测失败也只是不记时长。
	probe func(ctx context.Context, data []byte) (int64, error)
	// maxUploadBytes 是单个文件的上限，默认 assetMaxUploadBytes；测试可调小。
	maxUploadBytes int64
```

5. `NewAssetHandler` 的 return 改为：

```go
	return &AssetHandler{storage: storage, repo: repo, users: users, signer: signer, maxUploadBytes: assetMaxUploadBytes}
```

6. `storeUpload` 里从 `// 探测必须在 PutObject 之前做：…` 注释开始、到 `durationMs = d\n\t}` 结束的整段（声明 `var durationMs int64` 和 `if strings.HasPrefix(contentType, "video/") { … }`）替换为：

```go
	// 时长探测改为尽力而为：探测失败只是不记时长（duration_ms 为 NULL），不再拒绝上传——
	// 参考仓拒绝无权威时长的视频是为了按秒计费不被篡改的元数据骗，桌面端不计费（spec X3）。
	// 探测仍放在 PutObject 之前，保持参考仓的顺序不变式。
	var durationMs int64
	if strings.HasPrefix(contentType, "video/") && h.probe != nil {
		if d, perr := h.probe(ctx, body); perr == nil {
			durationMs = d
		}
	}
```

7. `readUploadForm` 里 `if perr := r.ParseMultipartForm(64 << 20); perr != nil {` 分支体改为：

```go
		var tooLarge *http.MaxBytesError
		if errors.As(perr, &tooLarge) || strings.Contains(perr.Error(), "request body too large") {
			return "", "", nil, &uploadError{Status: http.StatusRequestEntityTooLarge, Message: "文件过大，单个文件不能超过 1GB"}
		}
		return "", "", nil, &uploadError{Status: http.StatusBadRequest, Message: "invalid multipart form"}
```

8. `Upload` 方法第一行 `userID := userIDFromContext(r.Context())` 之后加：

```go
	if h.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	}
```

Run: `cd server && go test ./internal/http/ -run 'Upload|WebAsset' -v`
Expected: 参考仓保留的 6 个上传测试 + 新增 3 个 PASS。

- [ ] **Step 4: 写媒体服务、reveal、/me 的失败测试**

`server/internal/http/media_handlers_test.go`：

```go
package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/users"
)

var mediaPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 24)...)

type mediaEnv struct {
	conn      *sql.DB
	storage   *assets.LocalStorage
	handler   *MediaHandler
	revealed  []string
	revealErr error
	dataDir   string
}

// newMediaEnv 的目录布局模拟真实数据目录：<dataDir>/media 是媒体根，<dataDir>/secret.txt 代表 data.db。
func newMediaEnv(t *testing.T) *mediaEnv {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(dataDir, "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secret.txt"), []byte("API-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &mediaEnv{conn: dbtest.Open(t), storage: storage, dataDir: dataDir}
	env.handler = NewMediaHandler(storage, NewMediaSigner(storage), NewSQLMediaMimeStore(env.conn), func(p string) error {
		env.revealed = append(env.revealed, p)
		return env.revealErr
	})
	return env
}

func (e *mediaEnv) put(t *testing.T, key string, body []byte, assetMime string) {
	t.Helper()
	if _, err := e.storage.PutObject(t.Context(), assets.PutObjectInput{Key: key, Body: body}); err != nil {
		t.Fatal(err)
	}
	if assetMime != "" {
		if _, err := e.conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, ?, ?, ?, ?)`,
			key, "media://"+key, len(body), assetMime); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *mediaEnv) serve(method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.handler.Serve(rec, req)
	return rec
}

func (e *mediaEnv) reveal(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/reveal", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.handler.Reveal(rec, req)
	return rec
}

// <video> 拖进度条、mediabunny 抽帧都靠 Range；没有 206 就只能从头播。
func TestMediaServeRangeAndHead(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/2026/10/v.mp4", []byte("0123456789"), "video/mp4")

	rec := env.serve("GET", "/media/u1/2026/10/v.mp4", map[string]string{"Range": "bytes=2-5"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Range") != "bytes 2-5/10" || h.Get("Content-Type") != "video/mp4" ||
		h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers=%v", h)
	}
	head := env.serve("HEAD", "/media/u1/2026/10/v.mp4", nil)
	if head.Code != http.StatusOK || head.Header().Get("Content-Length") != "10" || head.Body.Len() != 0 {
		t.Fatalf("HEAD code=%d len=%q body=%d", head.Code, head.Header().Get("Content-Length"), head.Body.Len())
	}
}

// 生成产物的 key 没有扩展名（gen/task-<id>-<i>），Content-Type 只能来自库里的 mime_type 或嗅探。
func TestMediaServeContentTypeForExtensionlessKeys(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/gen_task-1-0", []byte("not really an image"), "image/webp")
	env.put(t, "u1/gen_task-2-0", mediaPNG, "")
	env.put(t, "u1/gen_task-3-0", mediaPNG, "application/octet-stream")
	env.put(t, "library/1/voice", []byte("ID3fake"), "")
	res, _ := env.conn.Exec(`INSERT INTO library_folders (user_id, name) VALUES (1, 'f')`)
	fid, _ := res.LastInsertId()
	env.conn.Exec(`INSERT INTO library_items (user_id, folder_id, object_key, url, size_bytes, mime_type, kind)
		VALUES (1, ?, 'library/1/voice', 'media://library/1/voice', 7, 'audio/mpeg', 'audio')`, fid)

	cases := map[string]string{
		"/media/u1/gen_task-1-0":  "image/webp", // assets.mime_type 优先
		"/media/u1/gen_task-2-0":  "image/png",  // 库里没有 → 嗅探
		"/media/u1/gen_task-3-0":  "image/png",  // octet-stream 视为未知 → 嗅探
		"/media/library/1/voice":  "audio/mpeg", // 回落 library_items.mime_type
	}
	for target, want := range cases {
		rec := env.serve("GET", target, nil)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != want {
			t.Errorf("%s: code=%d type=%q want %q", target, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
}

// media/ 的上一级就是 data.db：任何逃出媒体根目录的路径都必须 404，且绝不能吐出内容。
func TestMediaServeRejectsTraversalAndMissing(t *testing.T) {
	env := newMediaEnv(t)
	os.MkdirAll(filepath.Join(env.storage.Root(), "u1"), 0o700)
	for _, target := range []string{
		"/media/../secret.txt", "/media/%2e%2e/secret.txt", "/media/u1/..%2f..%2fsecret.txt",
		"/media/", "/media/u1", "/media/u1/missing.png", "/media/u1/a.png%00",
	} {
		rec := env.serve("GET", target, nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "API-KEY") {
			t.Errorf("%s: code=%d body=%q，want 404", target, rec.Code, rec.Body.String())
		}
	}
}

func TestMediaRevealOpensLocalPath(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/2026/10/a b.png", mediaPNG, "image/png")
	signed := NewMediaSigner(env.storage).Sign("media://u1/2026/10/a b.png")
	want, _ := env.storage.Path("u1/2026/10/a b.png")
	for _, u := range []string{signed, "media://u1/2026/10/a b.png", "http://127.0.0.1:40000/media/u1/2026/10/a%20b.png?token=old"} {
		env.revealed = nil
		body, _ := json.Marshal(map[string]string{"url": u})
		rec := env.reveal(string(body))
		if rec.Code != http.StatusNoContent || len(env.revealed) != 1 || env.revealed[0] != want {
			t.Fatalf("url=%s code=%d revealed=%v want %s", u, rec.Code, env.revealed, want)
		}
	}
}

func TestMediaRevealRejectsUnknownAndMissing(t *testing.T) {
	env := newMediaEnv(t)
	cases := map[string]int{
		`{"url":"https://cdn.example/a.png"}`: http.StatusBadRequest,
		`{"url":"media://../secret.txt"}`:     http.StatusBadRequest,
		`{"url":"media://u1/missing.png"}`:    http.StatusNotFound,
		`{"url":""}`:                          http.StatusBadRequest,
		`not json`:                            http.StatusBadRequest,
	}
	for body, want := range cases {
		if rec := env.reveal(body); rec.Code != want {
			t.Errorf("%s: code=%d want %d", body, rec.Code, want)
		}
	}
	if len(env.revealed) != 0 {
		t.Fatalf("非法请求不应打开文件夹，revealed=%v", env.revealed)
	}
}

func TestMediaRevealReportsFailure(t *testing.T) {
	env := newMediaEnv(t)
	env.put(t, "u1/a.png", mediaPNG, "image/png")
	env.revealErr = errors.New("open failed")
	if rec := env.reveal(`{"url":"media://u1/a.png"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d want 500", rec.Code)
	}
}

// Review Focus 1：Windows 非法字符的文件名上传后，用返回的 URL 能原样读回。
func TestMediaUploadedFileWithIllegalNameIsServable(t *testing.T) {
	env := newMediaEnv(t)
	signer := NewMediaSigner(env.storage)
	ah := NewAssetHandler(env.storage, assets.NewSQLiteRepository(env.conn), users.NewSQLiteRepository(env.conn), signer)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="镜头:1?.png"`},
		"Content-Type":        {"image/png"},
	})
	part.Write(mediaPNG)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), LocalUserID))
	rec := httptest.NewRecorder()
	ah.Upload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		URL string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.URL, "http://127.0.0.1:5555/media/u1/") {
		t.Fatalf("url=%q", out.URL)
	}
	path := strings.TrimPrefix(strings.SplitN(out.URL, "?", 2)[0], "http://127.0.0.1:5555")
	got := env.serve("GET", path, nil)
	body, _ := io.ReadAll(got.Body)
	if got.Code != http.StatusOK || !bytes.Equal(body, mediaPNG) || got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve code=%d type=%q len=%d", got.Code, got.Header().Get("Content-Type"), len(body))
	}
}
```

`server/internal/http/me_handlers_test.go`：

```go
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/users"
)

func TestMeReturnsLocalAdminWithLiveStorage(t *testing.T) {
	conn := dbtest.Open(t)
	conn.Exec(`INSERT INTO assets (user_id, object_key, url, size_bytes, mime_type) VALUES (1, 'k', 'media://k', 123, 'image/png')`)
	h := NewMeHandler(users.NewSQLiteRepository(conn))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req.WithContext(withUserID(req.Context(), LocalUserID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	want := map[string]any{
		"id": float64(1), "email": "local@miaoju.desktop", "role": "admin", "balanceCredits": float64(0),
		"storageQuotaBytes": float64(1125899906842624), "storageUsedBytes": float64(123),
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s=%v want %v", k, body[k], v)
		}
	}
}

func TestMeUnknownUserReturns401(t *testing.T) {
	h := NewMeHandler(users.NewSQLiteRepository(dbtest.Open(t)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req.WithContext(withUserID(req.Context(), 99)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}
```

`server/internal/reveal/reveal_test.go`：

```go
package reveal

import "testing"

// explorer 自己解析 /select, 参数，不认 Go 的标准转义：只能给路径加引号，整段参数不能被包进引号。
func TestWindowsCmdLineQuotesOnlyThePath(t *testing.T) {
	got := windowsCmdLine(`C:\Users\张 三\AppData\Roaming\com.miaoju.desktop\media\u1\2026\10\a b.png`)
	want := `explorer.exe /select,"C:\Users\张 三\AppData\Roaming\com.miaoju.desktop\media\u1\2026\10\a b.png"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
```

- [ ] **Step 5: 运行测试，确认失败**

Run: `cd server && go test ./internal/http/ ./internal/reveal/`
Expected: 编译失败，`undefined: NewMediaHandler`、`undefined: NewMeHandler`、`undefined: windowsCmdLine`。

- [ ] **Step 6: 写实现**

`server/internal/http/media_handlers.go`：

```go
package http

import (
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

// mediaFileStore 把 object key 换算成本地文件路径；key 不合法时返回错误（*assets.LocalStorage 实现）。
type mediaFileStore interface {
	Path(key string) (string, error)
}

// mediaMimeStore 查对象登记过的 MIME。生成产物的 key 没有扩展名，只能靠它。
type mediaMimeStore interface {
	MimeByObjectKey(objectKey string) (string, error)
}

// MediaHandler 提供 sidecar 的本地媒体文件（spec S3），以及“在访达/资源管理器中显示”。
// URL 里的 token 由 localauth 校验（媒体专用 token 只放行 GET/HEAD /media/*）。
type MediaHandler struct {
	files  mediaFileStore
	signer *MediaSigner
	mimes  mediaMimeStore
	reveal func(path string) error
}

func NewMediaHandler(files mediaFileStore, signer *MediaSigner, mimes mediaMimeStore, reveal func(path string) error) *MediaHandler {
	return &MediaHandler{files: files, signer: signer, mimes: mimes, reveal: reveal}
}

// mediaCSP 让用户上传的 SVG/HTML 被当作页面直接打开时也跑不了脚本；嵌在 <img>/<video> 里时不生效。
const mediaCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// Serve 处理 GET/HEAD /media/<key>。key 非法、文件不存在、是目录，一律 404，不区分原因。
func (h *MediaHandler) Serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/media/")
	p, err := h.files.Path(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", h.contentType(key, f))
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", mediaCSP)
	// URL 带本次启动的 token，同一次启动内同一 key 的 URL 不变，可以放心缓存。
	hdr.Set("Cache-Control", "private, max-age=86400")
	// ServeContent 负责 Range / 206 / HEAD / If-Modified-Since；Content-Type 已设好，它不会再嗅探。
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// contentType 依次取：库里登记的 MIME → 扩展名 → 前 512 字节嗅探。
// application/octet-stream 等于没登记（上传时浏览器没给类型），继续往下找。
func (h *MediaHandler) contentType(key string, f *os.File) string {
	if h.mimes != nil {
		if m, err := h.mimes.MimeByObjectKey(key); err == nil && m != "" && m != "application/octet-stream" {
			return m
		}
	}
	if m := mime.TypeByExtension(strings.ToLower(path.Ext(key))); m != "" {
		return m
	}
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	_, _ = f.Seek(0, io.SeekStart)
	return http.DetectContentType(buf[:n])
}

type revealRequest struct {
	URL string `json:"url"`
}

// Reveal 处理 POST /api/v1/media/reveal：把节点里的媒体 URL 换算成本机真实路径，在访达 / 资源管理器
// 里选中它。由 sidecar 打开而不是把路径交给前端：Tauri 壳不必授予任何文件权限（spec SEC4），
// 真实路径也不出 sidecar。
func (h *MediaHandler) Reveal(w http.ResponseWriter, r *http.Request) {
	var body revealRequest
	if err := decodeJSON(r, &body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	key := h.signer.ObjectKey(body.URL)
	if key == "" {
		writeError(w, http.StatusBadRequest, "无法识别这个媒体")
		return
	}
	p, err := h.files.Path(key)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法识别这个媒体")
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeError(w, http.StatusNotFound, "文件不存在，可能已被删除")
		return
	}
	if h.reveal == nil {
		writeError(w, http.StatusNotImplemented, "当前系统不支持")
		return
	}
	if err := h.reveal(p); err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开所在文件夹")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SQLMediaMimeStore 按 object_key 查 MIME：先 assets（上传与生成产物），再 library_items（素材库上传）。
type SQLMediaMimeStore struct {
	db *sql.DB
}

func NewSQLMediaMimeStore(db *sql.DB) *SQLMediaMimeStore {
	return &SQLMediaMimeStore{db: db}
}

func (s *SQLMediaMimeStore) MimeByObjectKey(objectKey string) (string, error) {
	for _, q := range []string{
		`SELECT mime_type FROM assets WHERE object_key = ? ORDER BY id DESC LIMIT 1`,
		`SELECT mime_type FROM library_items WHERE object_key = ? ORDER BY id DESC LIMIT 1`,
	} {
		var m string
		err := s.db.QueryRow(q, objectKey).Scan(&m)
		if err == nil && m != "" && m != "application/octet-stream" {
			return m, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}
```

`server/internal/http/me_handlers.go`：

```go
package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/users"
)

type meUserRepo interface {
	GetByID(id int64) (*users.User, error)
	ComputeStorageUsed(id int64) (int64, error)
}

// MeHandler 是参考仓 AuthHandler.Me 的桌面版：没有登录、邀请码、JWT（spec X4），
// 只回本地用户的资料，响应字段与参考仓逐字一致（前端头像菜单、存储用量直接读它）。
type MeHandler struct {
	users meUserRepo
}

func NewMeHandler(users meUserRepo) *MeHandler { return &MeHandler{users: users} }

func (h *MeHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	user, err := h.users.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not found")
		return
	}
	// 存储用量从 assets + library_items 实时计算（权威），而不是用那个增量维护、会漂移的计数器。
	storageUsed := user.StorageUsedBytes
	if v, cerr := h.users.ComputeStorageUsed(userID); cerr == nil {
		storageUsed = v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                user.ID,
		"email":             user.Email,
		"role":              user.Role,
		"balanceCredits":    user.BalanceCredits,
		"storageQuotaBytes": user.StorageQuotaBytes,
		"storageUsedBytes":  storageUsed,
	})
}
```

`server/internal/reveal/reveal.go`：

```go
// Package reveal 在系统文件管理器里选中一个文件（spec S3「在访达/资源管理器中显示」）。
package reveal

// InFileManager 打开 path 所在的文件夹并选中它。path 必须是已存在文件的绝对路径。
func InFileManager(path string) error { return inFileManager(path) }

// windowsCmdLine 拼 explorer 的原始命令行。explorer 自己解析 /select, 参数，不认 Go 的标准转义
// （会把整个 "/select,C:\a b.png" 包进一对引号，结果选不中文件），所以只给路径本身加引号。
// 放在不带构建标签的文件里，是为了在 macOS 上也能测它。
func windowsCmdLine(path string) string {
	return `explorer.exe /select,"` + path + `"`
}
```

`server/internal/reveal/reveal_darwin.go`：

```go
package reveal

import "os/exec"

func inFileManager(path string) error {
	return exec.Command("open", "-R", path).Run()
}
```

`server/internal/reveal/reveal_windows.go`：

```go
package reveal

import (
	"os/exec"
	"syscall"
)

func inFileManager(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: windowsCmdLine(path)}
	// explorer 成功时也常返回退出码 1，只看能不能启动起来。
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
```

`server/internal/reveal/reveal_other.go`：

```go
//go:build !darwin && !windows

package reveal

import (
	"os/exec"
	"path/filepath"
)

// Linux 不是目标平台（spec D6），只为能编译：打开所在目录，不选中文件。
func inFileManager(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
```

- [ ] **Step 7: 运行测试，确认通过；三平台编译**

Run: `cd server && go test ./internal/http/ ./internal/reveal/ -race -v`
Expected: 新增 media 7 个、me 2 个、reveal 1 个全部 PASS，Task 5 及以前的测试仍 PASS。

Run: `cd server && GOOS=windows GOARCH=amd64 go vet ./internal/reveal/ ./internal/http/ && GOOS=linux go vet ./internal/reveal/`
Expected: 无输出。

若 `TestUpload_TooLargeReturns413` 返回 400：说明 `ParseMultipartForm` 的错误没有包住 `*http.MaxBytesError` 也不含该文本；打印 `perr` 的实际类型与文本，按实际情况调整 `readUploadForm` 的判断，不得放宽测试。

- [ ] **Step 8: 提交**

```bash
git add server/internal/http/asset_handlers.go server/internal/http/asset_handlers_test.go \
  server/internal/http/media_handlers.go server/internal/http/media_handlers_test.go \
  server/internal/http/me_handlers.go server/internal/http/me_handlers_test.go server/internal/reveal
git commit -m "feat(server): 资产上传、本地媒体服务（Range）、在访达中显示与 /me"
```

---

### Task 9: 项目接口与软删项目的清理 worker

**Files:**
- Create（原样复制）: `server/internal/http/project_handlers.go`、`server/internal/http/project_cleanup.go`、`server/internal/http/project_cover_test.go`
- Create（从参考仓 `project_handlers_test.go` 拆出）: `server/internal/http/project_snapshot_test.go`
- Test（新写）: `server/internal/http/project_handlers_db_test.go`

**Interfaces:**
- Consumes: `assets.Storage`/`Repository`/`LocalStorage`/`SQLiteRepository`（Task 1）；`library.OrphansRepository`、`TryReclaim`、`IsStillReferenced`、`NewSQLiteOrphansRepository`（Task 2）；`MediaSigner`、`storageUserRepo`、`writeJSON`、`decodeJSON`、上下文函数、`LocalUserID`、`fakeProjectRepository`、`newProjectSnapshotRequest`（Task 5）；`users.SQLiteRepository`（Task 6）；`workflows.AssetDuplicator`、`NewDeleteNowQueue`（Task 7）；`projects.*`（P1a）
- Produces（Task 12 依赖）：
  - `NewProjectHandler(repo projects.Repository, signer *MediaSigner, storage assets.Storage, assetRepo assets.Repository, users storageUserRepo, taskCleanup taskProjectCleanup, orphans library.OrphansRepository, db *sql.DB, duplicator *workflows.AssetDuplicator) *ProjectHandler`；方法 `Create`、`List`、`Get`、`Update`、`Delete`、`GetSnapshot`、`ListImages`、`SetCover`、`Duplicate`（路由见 Task 12）。
  - `type taskProjectCleanup interface { DeleteForProject(projectID int64) error }`（P1b 装配传 nil）。
  - `NewProjectCleaner(repo projects.Repository, assetRepo assets.Repository, users storageUserRepo, taskCleanup taskProjectCleanup, orphans library.OrphansRepository, db *sql.DB) *ProjectCleaner`；`(*ProjectCleaner).Start(ctx context.Context, interval time.Duration)`（立即清一次，之后每 interval 一次，每次最多 10 个项目）。

两个文件原样复制即可编译：`project_handlers.go` 依赖的 `workflows`、`library`、`assets` 都已就位，`storageUserRepo` 在 Task 5；生成任务的清理接口 `taskProjectCleanup` 在 P1b 传 nil（`ProjectCleaner.purge` 判 nil 跳过，`ProjectHandler` 不使用该字段），P1c 接 generation 后改传 generation 仓库。`ProjectCleaner` 的事务里只用 `tx`（`DELETE FROM assets` + `TryReclaim`），符合 SQLite 单写者约束。

- [ ] **Step 1: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/project_handlers.go $REF/http/project_cleanup.go $REF/http/project_cover_test.go server/internal/http/
```

- [ ] **Step 2: 写测试**

`server/internal/http/project_snapshot_test.go`（参考仓 `project_handlers_test.go` 的第一个测试，原样）：

```go
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/projects"
)

func TestProjectHandlerGetSnapshotIncludesVersion(t *testing.T) {
	repo := &fakeProjectRepository{
		project: &projects.Project{ID: 42, UserID: 7, Name: "demo"},
		snapshot: &projects.Snapshot{
			Payload:  json.RawMessage(`{"nodes":[]}`),
			SyncedAt: time.Unix(123, 0).UTC(),
			Version:  6,
		},
	}
	handler := NewProjectHandler(repo, nil, nil, nil, nil, nil, nil, nil, nil)
	rec := httptest.NewRecorder()

	handler.GetSnapshot(rec, newProjectSnapshotRequest(t, http.MethodGet, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Payload json.RawMessage `json:"payload"`
		Version int64           `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version != 6 {
		t.Fatalf("version = %d, want 6", body.Version)
	}
	if string(body.Payload) != `{"nodes":[]}` {
		t.Fatalf("payload = %s, want original payload", body.Payload)
	}
}
```

`server/internal/http/project_handlers_db_test.go`：

```go
package http

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/users"
	"github.com/james/huabu_studio/apps/api/internal/workflows"
)

type projectEnv struct {
	conn     *sql.DB
	storage  *assets.LocalStorage
	projects *projects.SQLiteRepository
	assets   *assets.SQLiteRepository
	users    *users.SQLiteRepository
	handler  *ProjectHandler
	cleaner  *ProjectCleaner
}

func newProjectEnv(t *testing.T) *projectEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	ar := assets.NewSQLiteRepository(conn)
	ur := users.NewSQLiteRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	dup := workflows.NewAssetDuplicator(storage, ar, ur, workflows.NewDeleteNowQueue(storage))
	return &projectEnv{
		conn: conn, storage: storage, projects: pr, assets: ar, users: ur,
		handler: NewProjectHandler(pr, NewMediaSigner(storage), storage, ar, ur, nil, orphans, conn, dup),
		cleaner: NewProjectCleaner(pr, ar, ur, nil, orphans, conn),
	}
}

// putAsset 写文件 + 插 asset 行 + 记账，模拟一次真实上传。
func (e *projectEnv) putAsset(t *testing.T, projectID int64, key, mime string, body []byte) int64 {
	t.Helper()
	if _, err := e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: key, ContentType: mime, Body: body}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	id, err := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: projectID, ObjectKey: key, SourceKind: "upload",
		URL: "media://" + key, SizeBytes: int64(len(body)), MimeType: mime, ContentHash: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.users.AddStorage(LocalUserID, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *projectEnv) used(t *testing.T) int64 {
	t.Helper()
	used, _, err := e.users.StorageUsage(LocalUserID)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func projectReq(method string, projectID int64, body string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	ctx := withUserID(req.Context(), LocalUserID)
	return req.WithContext(withProjectID(ctx, projectID))
}

func TestProjectCRUDHandlers(t *testing.T) {
	e := newProjectEnv(t)
	rec := httptest.NewRecorder()
	e.handler.Create(rec, projectReq(http.MethodPost, 0, `{"name":""}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空名字 code=%d want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.Create(rec, projectReq(http.MethodPost, 0, `{"name":"未命名项目"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p projects.Project
	json.Unmarshal(rec.Body.Bytes(), &p)
	rec = httptest.NewRecorder()
	e.handler.Update(rec, projectReq(http.MethodPut, p.ID, `{"name":"改名","description":"d"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "改名") {
		t.Fatalf("update code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.Delete(rec, projectReq(http.MethodDelete, p.ID, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.Get(rec, projectReq(http.MethodGet, p.ID, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("软删后 get code=%d want 404", rec.Code)
	}
}

func TestProjectGetSnapshotSignsMedia(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	e.projects.SaveAuthoritative(pid, json.RawMessage(`{"nodes":[{"id":"n1","content":"media://u1/a.png"}]}`))
	rec := httptest.NewRecorder()
	e.handler.GetSnapshot(rec, projectReq(http.MethodGet, pid, ""))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "http://127.0.0.1:5555/media/u1/a.png?token=mt") || strings.Contains(body, "media://") {
		t.Fatalf("快照出口必须换成本地 URL：code=%d body=%s", rec.Code, body)
	}
}

func TestProjectDuplicateCopiesMediaAndRewritesSnapshot(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "原项目", "说明")
	e.putAsset(t, pid, "u1/2026/10/a.png", "image/png", []byte("png-a"))
	if _, err := e.projects.SaveAuthoritative(pid, json.RawMessage(
		`{"nodes":[{"id":"n1","content":"media://u1/2026/10/a.png"},{"id":"n2","content":"https://cdn.example/x.png"}],"connections":[]}`)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	e.handler.Duplicate(rec, projectReq(http.MethodPost, pid, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ProjectID int64 `json:"projectId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	p, err := e.projects.GetForUser(LocalUserID, out.ProjectID)
	if err != nil || p.Name != "原项目 - 副本" || p.Description != "说明" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	snap, err := e.projects.GetLatest(out.ProjectID)
	if err != nil || snap == nil {
		t.Fatalf("副本没有快照: %v", err)
	}
	var doc struct {
		Nodes []struct{ ID, Content string }
	}
	json.Unmarshal(snap.Payload, &doc)
	newRef := doc.Nodes[0].Content
	if !strings.HasPrefix(newRef, "media://u1/") || newRef == "media://u1/2026/10/a.png" {
		t.Fatalf("副本应引用新 key，got %q", newRef)
	}
	if doc.Nodes[1].Content != "https://cdn.example/x.png" {
		t.Fatalf("外部 URL 应原样保留，got %q", doc.Nodes[1].Content)
	}
	rc, err := e.storage.GetObject(context.Background(), strings.TrimPrefix(newRef, "media://"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "png-a" {
		t.Fatalf("副本内容=%q", b)
	}
	if got := e.used(t); got != 10 {
		t.Fatalf("副本应真实占用配额：used=%d want 10", got)
	}
	if rows, _ := e.assets.ListForProject(out.ProjectID); len(rows) != 1 || rows[0].SizeBytes != 5 {
		t.Fatalf("副本 asset 行=%+v", rows)
	}
}

func TestProjectDuplicateEmptyProject(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "空", "")
	rec := httptest.NewRecorder()
	e.handler.Duplicate(rec, projectReq(http.MethodPost, pid, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ProjectID int64 `json:"projectId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if snap, _ := e.projects.GetLatest(out.ProjectID); snap != nil {
		t.Fatal("空项目的副本不应有快照")
	}
}

// 删项目只回收不再被任何地方引用的文件：被别的项目粘贴引用的必须留下（否则那边的节点变死链）。
func TestProjectDeletedIsPurgedAndSharedObjectsSurvive(t *testing.T) {
	e := newProjectEnv(t)
	pa, _ := e.projects.Create(LocalUserID, "A", "")
	pb, _ := e.projects.Create(LocalUserID, "B", "")
	e.putAsset(t, pa, "u1/shared.png", "image/png", []byte("12345"))
	e.putAsset(t, pa, "u1/only-a.png", "image/png", []byte("1234567"))
	e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pb, ObjectKey: "u1/shared.png", SourceKind: "canvas_ref",
		URL: "media://u1/shared.png", SizeBytes: 0, MimeType: "image/png"})
	e.projects.SaveAuthoritative(pa, json.RawMessage(`{"nodes":[]}`))

	rec := httptest.NewRecorder()
	e.handler.Delete(rec, projectReq(http.MethodDelete, pa, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if _, err := e.projects.GetForUser(LocalUserID, pa); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("软删后应立即读不到: %v", err)
	}
	if err := e.cleaner.purgePending(10); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.assets.ListForProject(pa); len(rows) != 0 {
		t.Fatalf("A 的 asset 行没删干净: %+v", rows)
	}
	var orphanKeys []string
	rows, _ := e.conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		orphanKeys = append(orphanKeys, k)
	}
	rows.Close()
	if !reflect.DeepEqual(orphanKeys, []string{"u1/only-a.png"}) {
		t.Fatalf("孤儿=%v，want 只有 only-a（shared 仍被 B 引用）", orphanKeys)
	}
	if got := e.used(t); got != 5 {
		t.Fatalf("只应回收 only-a 的 7 字节：used=%d want 5", got)
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, pa).Scan(&n)
	if n != 0 {
		t.Fatal("项目行没有硬删")
	}
	e.conn.QueryRow(`SELECT COUNT(*) FROM workflow_snapshots WHERE project_id = ?`, pa).Scan(&n)
	if n != 0 {
		t.Fatal("快照没有随项目级联删除")
	}
	// 文件本身留给 7 天后的孤儿清扫（支持撤销），此刻都还在。
	for _, k := range []string{"u1/shared.png", "u1/only-a.png"} {
		p, _ := e.storage.Path(k)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 不应被立即删除: %v", k, err)
		}
	}
	if rows, _ := e.assets.ListForProject(pb); len(rows) != 1 {
		t.Fatalf("B 的引用行被误删: %+v", rows)
	}
}

func TestProjectListImagesAndSetCover(t *testing.T) {
	e := newProjectEnv(t)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	img := e.putAsset(t, pid, "u1/cover.png", "image/png", []byte("img"))
	vid := e.putAsset(t, pid, "u1/clip.mp4", "video/mp4", []byte("vid"))
	other, _ := e.projects.Create(LocalUserID, "other", "")
	foreign := e.putAsset(t, other, "u1/foreign.png", "image/png", []byte("f"))

	rec := httptest.NewRecorder()
	e.handler.ListImages(rec, projectReq(http.MethodGet, pid, ""))
	var list struct {
		Data []projectImage `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Data) != 1 || list.Data[0].ID != img || !strings.HasPrefix(list.Data[0].URL, "http://127.0.0.1:5555/media/u1/cover.png") {
		t.Fatalf("images=%+v", list.Data)
	}

	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, img)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/u1/cover.png") {
		t.Fatalf("setCover code=%d body=%s", rec.Code, rec.Body.String())
	}
	var stored string
	e.conn.QueryRow(`SELECT cover_url FROM projects WHERE id = ?`, pid).Scan(&stored)
	if stored != "media://u1/cover.png" {
		t.Fatalf("库里必须存 canonical，got %q", stored)
	}
	rec = httptest.NewRecorder()
	e.handler.List(rec, projectReq(http.MethodGet, 0, ""))
	var page struct {
		Data []projects.Project `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &page)
	found := false
	for _, p := range page.Data {
		if p.ID == pid {
			found = strings.HasPrefix(p.CoverURL, "http://127.0.0.1:5555/media/u1/cover.png")
		}
	}
	if !found {
		t.Fatalf("列表封面没有签名: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, vid)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("视频做封面 code=%d want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.handler.SetCover(rec, projectReq(http.MethodPut, pid, fmt.Sprintf(`{"assetId":%d}`, foreign)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("别的项目的图 code=%d want 404", rec.Code)
	}
}
```

- [ ] **Step 3: 运行测试**

Run: `cd server && go test ./internal/http/ -run 'Project' -race -v`
Expected: `project_snapshot_test` 1 个、`project_cover_test` 1 个、`project_handlers_db_test` 6 个全部 PASS。

本任务的生产代码是原样复制，测试预期一次通过；若失败，先确认 Task 1/2/6/7 已合并，再按失败信息定位到移植代码（不得改测试的断言）。可以临时把 `project_cleanup.go` 里 `library.TryReclaim(...)` 那行注释掉再跑 `TestProjectDeletedIsPurgedAndSharedObjectsSurvive`，确认它会失败（测试确实覆盖到回收逻辑），然后恢复。

- [ ] **Step 4: 提交**

```bash
git add server/internal/http/project_handlers.go server/internal/http/project_cleanup.go \
  server/internal/http/project_cover_test.go server/internal/http/project_snapshot_test.go \
  server/internal/http/project_handlers_db_test.go
git commit -m "feat(server): 项目接口（含副本、封面）与软删项目清理 worker"
```

---

### Task 10: canvasowner 本地实现、画布 mutation、跨项目粘贴登记

**Files:**
- Create（重写）: `server/internal/canvasowner/owner.go`、`server/internal/canvasowner/owner_test.go`
- Create（原样复制）: `server/internal/http/canvas_mutation_handlers.go`、`server/internal/http/canvas_asset_import_handlers.go`、`server/internal/http/canvas_concurrent_test.go`、`server/internal/http/canvas_op_signing_test.go`、`server/internal/http/canvas_observe_test.go`、`server/internal/http/canvas_asset_import_handlers_test.go`
- Create（移植）: `server/internal/http/project_handlers_test.go`
- Test（新写）: `server/internal/http/canvas_mutation_db_test.go`

**Interfaces:**
- Consumes: `assets.NewLocalStorage`、`assets.SQLiteRepository`（Task 1）；`MediaSigner`、上下文函数、`LocalUserID`、`fakeProjectRepository`、`newProjectCanvasRequest`、`fakeAssetStorage`（Task 5）；`canvasstate.*`、`projects.*`（P1a）
- Produces（Task 12 依赖）：
  - `canvasowner.NewLocal() *Local`，方法 `Claim(ctx, projectID, userID, actorUserID int64) (ClaimResult, error)`、`Heartbeat(ctx, projectID int64, ownerToken string) error`、`Require(ctx, projectID int64, ownerToken string) error`、`Probe(ctx, projectID int64) (ProbeResult, error)`；原样保留 `ClaimResult`、`ProbeResult`、`ErrOwnerLost`、`ErrOwnerExpired`、`SourceMark(ownerToken string) string`。
  - `NewCanvasMutationHandler(repo projects.Repository, owner canvasOwner, signer *MediaSigner) *CanvasMutationHandler`；`SetChangeNotifier(n canvasChangeNotifier)`；方法 `ClaimOwner`、`ProbeOwner`、`HeartbeatOwner`、`UpsertNode`、`PatchNode`、`DeleteNodes`、`PatchNodePositions`、`UpsertConnection`、`DeleteConnection`。
  - `NewCanvasAssetImportHandler(store canvasAssetRefStore, inserter assetRefInserter) *CanvasAssetImportHandler`、`ImportRefs`；`NewSQLCanvasAssetRefStore(db *sql.DB) *SQLCanvasAssetRefStore`。

**编辑锁的处理：** spec X2 删除 owner 编辑锁，S11 单窗口，桌面端没有可争的对手。handler 原样保留（个人路径仍会调 `owner.Require`），`canvasowner` 包换成恒放行的 `Local`：`Claim` 照常发三段式随机 token（前端用末段前 8 位识别广播回声，P1c 之后有用），`Require`/`Heartbeat` 对任何 token（含空串）都放行——P3 按 X2 删掉前端心跳后可能不再 Claim，空 token 不能把画布锁死；`Probe` 恒为“无人持锁”。handler 里的团队（actor>0）分支因为 `LocalIdentity` 不写 actor 而不可达，代码与对应测试原样保留（测试保证分支逻辑没被改坏，删掉反而要改动更多参考代码）。

- [ ] **Step 1: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
mkdir -p server/internal/canvasowner
cp $REF/http/canvas_mutation_handlers.go $REF/http/canvas_asset_import_handlers.go \
   $REF/http/canvas_concurrent_test.go $REF/http/canvas_op_signing_test.go $REF/http/canvas_observe_test.go \
   $REF/http/canvas_asset_import_handlers_test.go $REF/http/project_handlers_test.go server/internal/http/
```

- [ ] **Step 2: 移植 project_handlers_test.go**

在 `server/internal/http/project_handlers_test.go` 中删除：`TestProjectHandlerGetSnapshotIncludesVersion`（已在 Task 9 的 `project_snapshot_test.go`）、`fakeProjectRepository` 类型及其全部方法、`newProjectSnapshotRequest`、`newProjectCanvasRequest`（已在 Task 5 的 `testfakes_test.go`）。保留其余 7 个 `TestCanvasMutation*` 测试和 `fakeCanvasOwner`。import 块整体替换为：

```go
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)
```

- [ ] **Step 3: 写 canvasowner 与画布 DB 测试（先失败）**

`server/internal/canvasowner/owner_test.go`：

```go
package canvasowner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLocalClaimIssuesUniqueThreeSegmentTokens(t *testing.T) {
	l := NewLocal()
	a, err := l.Claim(context.Background(), 42, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := l.Claim(context.Background(), 42, 1, 0)
	if a.OwnerToken == b.OwnerToken {
		t.Fatal("每次 Claim 应发不同的 token（前端靠它区分标签页做回声识别）")
	}
	segs := strings.Split(a.OwnerToken, ":")
	if len(segs) != 3 || segs[0] != "1" || segs[1] != "0" || len(segs[2]) != 32 {
		t.Fatalf("token=%q want <uid>:<actor>:<32位hex>", a.OwnerToken)
	}
	if !a.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiresAt=%v 应在未来", a.ExpiresAt)
	}
	if SourceMark(a.OwnerToken) != segs[2][:8] {
		t.Fatalf("SourceMark=%q", SourceMark(a.OwnerToken))
	}
}

// 单用户单窗口（spec S11、X2）：锁永远放行，包括前端不再 Claim 时发来的空 token。
func TestLocalNeverRejects(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()
	for _, tok := range []string{"", "stale", "7:0:abcdef"} {
		if err := l.Require(ctx, 42, tok); err != nil {
			t.Fatalf("Require(%q)=%v", tok, err)
		}
		if err := l.Heartbeat(ctx, 42, tok); err != nil {
			t.Fatalf("Heartbeat(%q)=%v", tok, err)
		}
	}
	if res, err := l.Probe(ctx, 42); err != nil || res.Held {
		t.Fatalf("Probe=%+v err=%v，want 无人持锁", res, err)
	}
}

func TestClaimResultJSONTags(t *testing.T) {
	payload, err := json.Marshal(ClaimResult{
		OwnerToken: "7:token",
		ExpiresAt:  time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal err=%v", err)
	}
	got := string(payload)
	if !strings.Contains(got, `"ownerToken":"7:token"`) {
		t.Fatalf("ClaimResult JSON missing ownerToken tag: %s", got)
	}
	if !strings.Contains(got, `"expiresAt":`) {
		t.Fatalf("ClaimResult JSON missing expiresAt tag: %s", got)
	}
	if strings.Contains(got, "OwnerToken") || strings.Contains(got, "ExpiresAt") {
		t.Fatalf("ClaimResult JSON used Go field names: %s", got)
	}
}

func TestSourceMarkTakesFirstEightHexOfLastSegment(t *testing.T) {
	if got := SourceMark("7:42:0123456789abcdef"); got != "01234567" {
		t.Fatalf("SourceMark 三段式 got=%q, want 01234567", got)
	}
	if got := SourceMark("7:0123456789abcdef"); got != "01234567" {
		t.Fatalf("SourceMark 两段式 got=%q, want 01234567", got)
	}
	if got := SourceMark(""); got != "" {
		t.Fatalf("SourceMark 空 token got=%q, want 空", got)
	}
	token := "7:42:0123456789abcdef"
	got := SourceMark(token)
	if len(got) != 8 {
		t.Fatalf("SourceMark got=%q(len=%d), want 8 位", got, len(got))
	}
	if got == token || strings.Contains(got, "89abcdef") {
		t.Fatalf("SourceMark got=%q 泄露了 token 内容", got)
	}
}
```

`server/internal/http/canvas_mutation_db_test.go`：

```go
package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

type canvasEnv struct {
	conn      *sql.DB
	handler   *CanvasMutationHandler
	projectID int64
}

func newCanvasEnv(t *testing.T) *canvasEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := projects.NewSQLiteRepository(conn)
	pid, err := pr.Create(LocalUserID, "画布", "")
	if err != nil {
		t.Fatal(err)
	}
	return &canvasEnv{conn: conn, handler: NewCanvasMutationHandler(pr, canvasowner.NewLocal(), NewMediaSigner(storage)), projectID: pid}
}

func (e *canvasEnv) req(method, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(e.projectID, 10))
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = withUserID(ctx, LocalUserID)
	return req.WithContext(withProjectID(ctx, e.projectID))
}

func (e *canvasEnv) storedPayload(t *testing.T) string {
	t.Helper()
	var p string
	if err := e.conn.QueryRow(`SELECT payload FROM workflow_snapshots WHERE project_id = ?`, e.projectID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Review Focus 2：前端带着上一次启动的 URL（端口、token 都已变）提交，落库必须是 media://。
func TestCanvasDBMutationPersistsCanonicalMediaFromAnyPort(t *testing.T) {
	e := newCanvasEnv(t)
	rec := httptest.NewRecorder()
	e.handler.ClaimOwner(rec, e.req(http.MethodPost, "", nil))
	var claim struct {
		OwnerToken string `json:"ownerToken"`
	}
	json.Unmarshal(rec.Body.Bytes(), &claim)
	if rec.Code != http.StatusOK || claim.OwnerToken == "" {
		t.Fatalf("claim code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	e.handler.UpsertNode(rec, e.req(http.MethodPost,
		`{"ownerToken":"`+claim.OwnerToken+`","node":{"id":"n1","type":"image","content":"http://127.0.0.1:61000/media/u1/2026/10/a.png?token=stale"}}`, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert code=%d body=%s", rec.Code, rec.Body.String())
	}
	p := e.storedPayload(t)
	if !strings.Contains(p, `"media://u1/2026/10/a.png"`) || strings.Contains(p, "61000") || strings.Contains(p, "token=") {
		t.Fatalf("落库 payload=%s", p)
	}

	rec = httptest.NewRecorder()
	e.handler.PatchNode(rec, e.req(http.MethodPatch,
		`{"ownerToken":"x","patch":{"content":"http://127.0.0.1:5555/media/u1/b.png?token=mt"}}`, map[string]string{"nodeID": "n1"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch code=%d body=%s", rec.Code, rec.Body.String())
	}
	if p := e.storedPayload(t); !strings.Contains(p, `"media://u1/b.png"`) || strings.Contains(p, "5555") {
		t.Fatalf("落库 payload=%s", p)
	}
}

func TestCanvasDBLocalOwnerNeverBlocksWrites(t *testing.T) {
	e := newCanvasEnv(t)
	rec := httptest.NewRecorder()
	e.handler.UpsertNode(rec, e.req(http.MethodPost, `{"node":{"id":"n1","type":"text"}}`, nil)) // 没有 ownerToken
	if rec.Code != http.StatusOK {
		t.Fatalf("无 token 写入 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.ProbeOwner(rec, e.req(http.MethodGet, "", nil))
	var probe map[string]any
	json.Unmarshal(rec.Body.Bytes(), &probe)
	if rec.Code != http.StatusOK || probe["held"] != false {
		t.Fatalf("probe code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	e.handler.HeartbeatOwner(rec, e.req(http.MethodPost, `{"ownerToken":""}`, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat code=%d", rec.Code)
	}
}

func TestCanvasDBAssetRefStoreAndImport(t *testing.T) {
	conn := dbtest.Open(t)
	pr := projects.NewSQLiteRepository(conn)
	pa, _ := pr.Create(LocalUserID, "A", "")
	pb, _ := pr.Create(LocalUserID, "B", "")
	ar := assets.NewSQLiteRepository(conn)
	ar.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pa, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 9, MimeType: "image/png"})

	store := NewSQLCanvasAssetRefStore(conn)
	if owner, err := store.ProjectOwner(pa); err != nil || owner != LocalUserID {
		t.Fatalf("owner=%d err=%v", owner, err)
	}
	if _, err := store.ProjectOwner(99999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v want ErrNoRows", err)
	}
	if url, mime, found, err := store.CanonicalByKey(LocalUserID, "u1/a.png"); err != nil || !found || url != "media://u1/a.png" || mime != "image/png" {
		t.Fatalf("url=%q mime=%q found=%v err=%v", url, mime, found, err)
	}
	if _, _, found, _ := store.CanonicalByKey(LocalUserID, "u1/none.png"); found {
		t.Fatal("不存在的 key 不应 found")
	}
	if has, err := store.ProjectHasKey(pa, "u1/a.png"); err != nil || !has {
		t.Fatalf("has=%v err=%v", has, err)
	}
	if has, _ := store.ProjectHasKey(pb, "u1/a.png"); has {
		t.Fatal("B 还没有这个 key")
	}

	h := NewCanvasAssetImportHandler(store, ar)
	for i := 0; i < 2; i++ { // 重复粘贴必须幂等
		rec := doImportRefs(t, h, LocalUserID, pb, map[string]any{"objectKeys": []string{"u1/a.png", "u1/none.png"}})
		var res importAssetRefsResult
		json.Unmarshal(rec.Body.Bytes(), &res)
		if rec.Code != http.StatusOK || res.Imported != 1 || !reflect.DeepEqual(res.Missing, []string{"u1/none.png"}) {
			t.Fatalf("第 %d 次 code=%d res=%+v", i+1, rec.Code, res)
		}
	}
	rows, _ := ar.ListForProject(pb)
	if len(rows) != 1 || rows[0].SourceKind != "canvas_ref" || rows[0].SizeBytes != 0 {
		t.Fatalf("B 的引用行=%+v", rows)
	}
}
```

Run: `cd server && go test ./internal/canvasowner/ ./internal/http/`
Expected: 编译失败——`canvasowner` 包不存在（`undefined: NewLocal`），http 包因 `canvas_mutation_handlers.go` 引用 `canvasowner` 而编译失败。

- [ ] **Step 4: 写 canvasowner/owner.go**

`server/internal/canvasowner/owner.go`：

```go
// Package canvasowner 是画布「单人编辑锁」。参考仓用 Redis 实现，防止同一账号的多个标签页
// 互相顶掉编辑；桌面端单用户、单窗口（spec S11），多人协作与编辑锁已删除（spec X2），没有可争的
// 对手。这里保留参考仓的类型、错误与 SourceMark，给 CanvasMutationHandler 一个恒放行的实现，
// 让 handler 代码和前端协议原样不动。
package canvasowner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrOwnerLost    = errors.New("canvas owner lost")
	ErrOwnerExpired = errors.New("canvas owner expired")
)

type ClaimResult struct {
	OwnerToken string    `json:"ownerToken"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// ProbeResult 是一次「谁在编辑」的只读探测结果。Held=false 时其余字段无意义。
type ProbeResult struct {
	Held         bool
	UserID       int64
	ActorUserID  int64
	StaleSeconds int64
}

// claimTTL 只用来填 ClaimResult.ExpiresAt（参考仓生产配置 24h），本地实现不会真的过期。
const claimTTL = 24 * time.Hour

// Local 是桌面端的编辑锁：Claim 照常发放随机 ownerToken（前端用它末段前 8 位做广播回声识别），
// Require / Heartbeat 对任何 token 都放行（包括空串——前端按 X2 删掉心跳后可能不再 Claim），
// Probe 永远回答「无人持锁」。
type Local struct{}

func NewLocal() *Local { return &Local{} }

func (l *Local) Claim(_ context.Context, _ int64, userID int64, actorUserID int64) (ClaimResult, error) {
	token, err := newOwnerToken(userID, actorUserID)
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{OwnerToken: token, ExpiresAt: time.Now().Add(claimTTL)}, nil
}

func (l *Local) Heartbeat(context.Context, int64, string) error { return nil }

func (l *Local) Require(context.Context, int64, string) error { return nil }

func (l *Local) Probe(context.Context, int64) (ProbeResult, error) { return ProbeResult{}, nil }

// SourceMark 是变更广播帧里的「谁改的」指纹：锁值最后一段（随机 hex）的前 8 个字符。
// 帧会推给项目下所有连接，带完整 token 等于把编辑凭据广播出去；前 8 位足够认出自己的回声。
// 前端必须用同一条规则（token.split(':').pop().slice(0,8)）算。
func SourceMark(ownerToken string) string {
	if ownerToken == "" {
		return ""
	}
	segs := strings.Split(ownerToken, ":")
	last := segs[len(segs)-1]
	if len(last) > 8 {
		return last[:8]
	}
	return last
}

// newOwnerToken 生成 "<userID>:<actorID>:<32 位随机 hex>"，与参考仓锁值格式一致。
func newOwnerToken(userID, actorUserID int64) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strconv.FormatInt(userID, 10) + ":" +
		strconv.FormatInt(actorUserID, 10) + ":" +
		hex.EncodeToString(b[:]), nil
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd server && go test ./internal/canvasowner/ ./internal/http/ -race -v`
Expected: canvasowner 4 个 PASS；http 包里 `project_handlers_test` 7 个、`canvas_concurrent_test` 4 个、`canvas_op_signing_test` 3 个、`canvas_observe_test` 9 个、`canvas_asset_import_handlers_test` 5 个、`canvas_mutation_db_test` 3 个全部 PASS，之前任务的测试仍 PASS。

- [ ] **Step 6: 提交**

```bash
git add server/internal/canvasowner server/internal/http/canvas_mutation_handlers.go \
  server/internal/http/canvas_asset_import_handlers.go server/internal/http/project_handlers_test.go \
  server/internal/http/canvas_concurrent_test.go server/internal/http/canvas_op_signing_test.go \
  server/internal/http/canvas_observe_test.go server/internal/http/canvas_asset_import_handlers_test.go \
  server/internal/http/canvas_mutation_db_test.go
git commit -m "feat(server): 画布局部 mutation 与跨项目粘贴登记；编辑锁换成单用户恒放行实现"
```

---

### Task 11: 素材库接口与 /admin/gc

**Files:**
- Create（原样复制）: `server/internal/http/library_folder_handlers.go`、`library_item_handlers.go`、`library_bridge_handlers.go`、`library_folder_handlers_test.go`
- Create（重写）: `server/internal/http/admin_gc_handlers.go`
- Test（新写）: `server/internal/http/library_handlers_db_test.go`

**Interfaces:**
- Consumes: `assets.*`（Task 1）；`library.*`、`library.OrphanSweeper`、`library.DefaultGracePeriod`（Task 2）；`MediaSigner`、`storageUserRepo`、`LocalIdentity`、`withTestUserID`（Task 5）；`users.SQLiteRepository`（Task 6）；`projects.SQLiteRepository`（P1a）
- Produces（Task 12 依赖）：
  - `NewLibraryFolderHandler(f library.FoldersRepository, items library.ItemsRepository, orphans library.OrphansRepository, users storageUserRepo, db *sql.DB) *LibraryFolderHandler`（`List`、`Create`、`Update`、`Delete`）
  - `NewLibraryItemHandler(items library.ItemsRepository, folders library.FoldersRepository, orphans library.OrphansRepository, storage assets.Storage, users storageUserRepo, signer *MediaSigner, db *sql.DB) *LibraryItemHandler`（`List`、`Upload`、`Update`、`Delete`）
  - `NewLibraryBridgeHandler(items library.ItemsRepository, folders library.FoldersRepository, ar assets.Repository, signer *MediaSigner, db *sql.DB) *LibraryBridgeHandler`（`SaveToLibrary`、`SaveFromURL`、`ApplyToCanvas`）
  - `NewAdminGCHandler(sweeper *library.OrphanSweeper) *AdminGCHandler`（`Run`：`POST /api/v1/admin/gc`，`?dryRun=1` 只列候选；响应字段与参考仓一致）

三个素材库 handler 原样复制即可编译（它们只用 `library`、`assets`、`MediaSigner`、`storageUserRepo` 和 `*sql.DB`；`library_item_handlers.go` 的 `github.com/google/uuid` 已在 go.mod 的 indirect 依赖里）。事务内只用 `tx`，符合 SQLite 单写者约束。`admin_gc_handlers.go` 重写为调用 Task 2 的 `OrphanSweeper`（清扫逻辑只保留一份，定时器与手动入口共用）。桌面端本地用户恒为 admin，`/admin/gc` 不再需要 `RequireAdmin`，留着便于排查。

- [ ] **Step 1: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
cp $REF/http/library_folder_handlers.go $REF/http/library_item_handlers.go $REF/http/library_bridge_handlers.go \
   $REF/http/library_folder_handlers_test.go server/internal/http/
```

- [ ] **Step 2: 写失败的测试**

`server/internal/http/library_handlers_db_test.go`：

```go
package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/users"
)

var libraryPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 16)...)

type libraryEnv struct {
	conn     *sql.DB
	storage  *assets.LocalStorage
	signer   *MediaSigner
	folders  *library.SQLiteFoldersRepository
	assets   *assets.SQLiteRepository
	users    *users.SQLiteRepository
	projects *projects.SQLiteRepository
	router   http.Handler
}

func newLibraryEnv(t *testing.T) *libraryEnv {
	t.Helper()
	conn := dbtest.Open(t)
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "mt",
	})
	if err != nil {
		t.Fatal(err)
	}
	folders := library.NewSQLiteFoldersRepository(conn)
	items := library.NewSQLiteItemsRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	ar := assets.NewSQLiteRepository(conn)
	ur := users.NewSQLiteRepository(conn)
	signer := NewMediaSigner(storage)
	folderH := NewLibraryFolderHandler(folders, items, orphans, ur, conn)
	itemH := NewLibraryItemHandler(items, folders, orphans, storage, ur, signer, conn)
	bridge := NewLibraryBridgeHandler(items, folders, ar, signer, conn)
	gc := NewAdminGCHandler(library.NewOrphanSweeper(orphans, storage, conn))

	// 注册方式与 Task 12 的 router.go 一致（参考仓 router.go 素材库段）。
	r := chi.NewRouter()
	r.Use(LocalIdentity)
	r.Route("/library", func(r chi.Router) {
		r.Get("/folders", folderH.List)
		r.Post("/folders", folderH.Create)
		r.Patch("/folders/{folderID}", folderH.Update)
		r.Delete("/folders/{folderID}", folderH.Delete)
		r.Post("/folders/{folderID}/items/upload", itemH.Upload)
		r.Get("/items", itemH.List)
		r.Patch("/items/{itemID}", itemH.Update)
		r.Delete("/items/{itemID}", itemH.Delete)
		r.Post("/items/{itemID}/apply-to-canvas", bridge.ApplyToCanvas)
	})
	r.Post("/projects/{projectID}/assets/{assetID}/save-to-library", bridge.SaveToLibrary)
	r.Post("/library/save-from-url", bridge.SaveFromURL)
	r.Post("/admin/gc", gc.Run)

	return &libraryEnv{conn: conn, storage: storage, signer: signer, folders: folders, assets: ar, users: ur,
		projects: projects.NewSQLiteRepository(conn), router: r}
}

func (e *libraryEnv) do(method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *libraryEnv) doJSON(method, path, body string) *httptest.ResponseRecorder {
	return e.do(method, path, strings.NewReader(body), "application/json")
}

func (e *libraryEnv) upload(t *testing.T, folderID int64, filename, contentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	mw.Close()
	return e.do(http.MethodPost, fmt.Sprintf("/library/folders/%d/items/upload", folderID), &buf, mw.FormDataContentType())
}

type libraryItemJSON struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	SizeBytes int64  `json:"sizeBytes"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Name      string `json:"name"`
}

func (e *libraryEnv) uploadOK(t *testing.T, folderID int64) libraryItemJSON {
	t.Helper()
	rec := e.upload(t, folderID, "主角.png", "image/png", libraryPNG)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload code=%d body=%s", rec.Code, rec.Body.String())
	}
	var it libraryItemJSON
	json.Unmarshal(rec.Body.Bytes(), &it)
	return it
}

func (e *libraryEnv) mkFolder(t *testing.T, name string, parent *int64) int64 {
	t.Helper()
	id, err := e.folders.Create(LocalUserID, parent, name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *libraryEnv) used(t *testing.T) int64 {
	t.Helper()
	used, _, err := e.users.StorageUsage(LocalUserID)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func (e *libraryEnv) orphanKeys(t *testing.T) []string {
	t.Helper()
	rows, err := e.conn.Query(`SELECT object_key FROM orphan_objects ORDER BY object_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestLibraryUploadStoresFileAndChargesQuota(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "角色", nil)
	it := e.uploadOK(t, fid)
	if !strings.HasPrefix(it.URL, "http://127.0.0.1:5555/media/library/1/") || !strings.Contains(it.URL, ".png?token=mt") {
		t.Fatalf("url=%q", it.URL)
	}
	if it.Kind != "image" || it.Name != "主角" || it.SizeBytes != int64(len(libraryPNG)) || it.Source != "upload" {
		t.Fatalf("item=%+v", it)
	}
	rc, err := e.storage.GetObject(context.Background(), e.signer.ObjectKey(it.URL))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, libraryPNG) {
		t.Fatal("落盘内容不一致")
	}
	if got := e.used(t); got != int64(len(libraryPNG)) {
		t.Fatalf("used=%d", got)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/library/items?folder_id=%d", fid), nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/library/1/") {
		t.Fatalf("list code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLibraryUploadRejectsUnsupportedMime(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	if rec := e.upload(t, fid, "notes.txt", "text/plain", []byte("hi")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("code=%d want 415", rec.Code)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("被拒的上传不应记账，used=%d", got)
	}
	if _, err := os.Stat(filepath.Join(e.storage.Root(), "library")); !os.IsNotExist(err) {
		t.Fatal("被拒的上传不应落盘")
	}
}

func TestLibraryItemDeleteMarksOrphanAndFreesQuota(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	it := e.uploadOK(t, fid)
	key := e.signer.ObjectKey(it.URL)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/library/items/%d", it.ID), nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := e.orphanKeys(t); !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("孤儿=%v want [%s]", got, key)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("used=%d want 0", got)
	}
	p, _ := e.storage.Path(key)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("文件要等 7 天宽限期后才删（支持撤销）")
	}
}

// 素材应用到画布后再从素材库删掉：画布还在用，文件不能进孤儿队列，配额也不退。
func TestLibraryItemDeleteKeepsObjectUsedOnCanvas(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	it := e.uploadOK(t, fid)
	pid, _ := e.projects.Create(LocalUserID, "画布", "")
	rec := e.doJSON(http.MethodPost, fmt.Sprintf("/library/items/%d/apply-to-canvas", it.ID), fmt.Sprintf(`{"projectId":%d}`, pid))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "http://127.0.0.1:5555/media/library/1/") {
		t.Fatalf("apply code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, _ := e.assets.ListForProject(pid)
	if len(rows) != 1 || rows[0].SourceKind != "library_ref" || rows[0].SizeBytes != 0 {
		t.Fatalf("引用行=%+v", rows)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/library/items/%d", it.ID), nil, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete code=%d", rec.Code)
	}
	if got := e.orphanKeys(t); len(got) != 0 {
		t.Fatalf("画布仍在用，不应标孤儿：%v", got)
	}
	if got := e.used(t); got != int64(len(libraryPNG)) {
		t.Fatalf("配额不应退回，used=%d", got)
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM assets WHERE library_item_id IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Fatal("删除素材后 assets.library_item_id 应清空")
	}
}

func TestLibraryFolderDeleteNonEmptyAndForce(t *testing.T) {
	e := newLibraryEnv(t)
	parent := e.mkFolder(t, "父", nil)
	child := e.mkFolder(t, "子", &parent)
	it := e.uploadOK(t, child)
	key := e.signer.ObjectKey(it.URL)

	rec := e.do(http.MethodDelete, fmt.Sprintf("/library/folders/%d", parent), nil, "")
	var conflict map[string]any
	json.Unmarshal(rec.Body.Bytes(), &conflict)
	if rec.Code != http.StatusConflict || conflict["childCount"] != float64(1) || conflict["itemCount"] != float64(0) {
		t.Fatalf("非空目录 code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodDelete, fmt.Sprintf("/library/folders/%d?force=1", parent), nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force code=%d body=%s", rec.Code, rec.Body.String())
	}
	if list, _ := e.folders.List(LocalUserID); len(list) != 0 {
		t.Fatalf("目录树没删干净: %+v", list)
	}
	if got := e.orphanKeys(t); !reflect.DeepEqual(got, []string{key}) {
		t.Fatalf("孤儿=%v", got)
	}
	if got := e.used(t); got != 0 {
		t.Fatalf("used=%d", got)
	}
}

// 节点只持有 URL：按 URL 存入素材库必须认得本次和上一次启动发出的本地 URL。
func TestLibrarySaveFromURLAcceptsLocalURLs(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	pid, _ := e.projects.Create(LocalUserID, "p", "")
	e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: "u1/a.png", Body: libraryPNG})
	aid, _ := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pid, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: int64(len(libraryPNG)), MimeType: "image/png"})

	for _, u := range []string{e.signer.Sign("media://u1/a.png"), "http://127.0.0.1:40000/media/u1/a.png?token=old"} {
		rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":%q,"folderId":%d}`, u, fid))
		var it libraryItemJSON
		json.Unmarshal(rec.Body.Bytes(), &it)
		if rec.Code != http.StatusCreated || it.SizeBytes != 0 || it.Source != "from_project" {
			t.Fatalf("url=%s code=%d body=%s", u, rec.Code, rec.Body.String())
		}
		var linked int64
		e.conn.QueryRow(`SELECT library_item_id FROM assets WHERE id = ?`, aid).Scan(&linked)
		if linked != it.ID {
			t.Fatalf("asset 应盖上 library_item_id=%d，got %d", it.ID, linked)
		}
	}
	if rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":"https://cdn.example/x.png","folderId":%d}`, fid)); rec.Code != http.StatusBadRequest {
		t.Fatalf("外部 URL code=%d want 400", rec.Code)
	}
	if rec := e.doJSON(http.MethodPost, "/library/save-from-url", fmt.Sprintf(`{"url":"media://u1/none.png","folderId":%d}`, fid)); rec.Code != http.StatusNotFound {
		t.Fatalf("未登记的媒体 code=%d want 404", rec.Code)
	}
}

func TestLibrarySaveToLibraryChecksProject(t *testing.T) {
	e := newLibraryEnv(t)
	fid := e.mkFolder(t, "f", nil)
	pa, _ := e.projects.Create(LocalUserID, "A", "")
	pb, _ := e.projects.Create(LocalUserID, "B", "")
	aid, _ := e.assets.Insert(assets.AssetRecord{UserID: LocalUserID, ProjectID: pa, ObjectKey: "u1/a.png", SourceKind: "upload",
		URL: "media://u1/a.png", SizeBytes: 3, MimeType: "image/png"})
	body := fmt.Sprintf(`{"folderId":%d,"name":"存档"}`, fid)
	if rec := e.doJSON(http.MethodPost, fmt.Sprintf("/projects/%d/assets/%d/save-to-library", pb, aid), body); rec.Code != http.StatusBadRequest {
		t.Fatalf("别的项目 code=%d want 400", rec.Code)
	}
	rec := e.doJSON(http.MethodPost, fmt.Sprintf("/projects/%d/assets/%d/save-to-library", pa, aid), body)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"name":"存档"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLibraryAdminGCDryRunAndRun(t *testing.T) {
	e := newLibraryEnv(t)
	e.storage.PutObject(context.Background(), assets.PutObjectInput{Key: "u1/old.png", Body: []byte("x")})
	e.conn.Exec(`INSERT INTO orphan_objects (object_key, user_id, size_bytes, marked_at) VALUES ('u1/old.png', 1, 1, '2000-01-01 00:00:00')`)
	p, _ := e.storage.Path("u1/old.png")

	rec := e.do(http.MethodPost, "/admin/gc?dryRun=1", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":1`) || !strings.Contains(rec.Body.String(), `"dryRun":true`) {
		t.Fatalf("dryRun code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("dryRun 不应删文件")
	}
	rec = e.do(http.MethodPost, "/admin/gc", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":1`) || !strings.Contains(rec.Body.String(), `"totalCandidates":1`) {
		t.Fatalf("gc code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("过了宽限期且无引用的文件应被删除")
	}
}
```

Run: `cd server && go test ./internal/http/ -run 'Library|Folder'`
Expected: 编译失败 `undefined: NewAdminGCHandler`。

- [ ] **Step 3: 写 admin_gc_handlers.go**

`server/internal/http/admin_gc_handlers.go`：

```go
package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/library"
)

// AdminGCHandler 是 POST /api/v1/admin/gc：手动触发一次孤儿清扫。清扫逻辑在 library.OrphanSweeper，
// sidecar 也用它在启动时和每天自动清扫（spec S9）；这个入口留着便于排查，响应字段与参考仓一致。
type AdminGCHandler struct {
	sweeper *library.OrphanSweeper
}

func NewAdminGCHandler(sweeper *library.OrphanSweeper) *AdminGCHandler {
	return &AdminGCHandler{sweeper: sweeper}
}

// Run 删除早于宽限期（7 天）且复核后仍无引用的孤儿对象。?dryRun=1 只返回候选列表，不碰文件。
func (h *AdminGCHandler) Run(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("dryRun") == "1" {
		list, err := h.sweeper.Candidates(library.DefaultGracePeriod)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"candidates": list,
			"count":      len(list),
			"dryRun":     true,
		})
		return
	}
	res, err := h.sweeper.Sweep(r.Context(), library.DefaultGracePeriod)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         res.Deleted,
		"skipped":         res.Skipped,
		"totalCandidates": res.TotalCandidates,
	})
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./internal/http/ -run 'Library|Folder' -race -v`
Expected: `library_folder_handlers_test` 1 个 + `library_handlers_db_test` 8 个全部 PASS。

- [ ] **Step 5: 全包回归并提交**

Run: `cd server && go vet ./internal/http/ && go test ./internal/http/ -race`
Expected: PASS。

```bash
git add server/internal/http/library_folder_handlers.go server/internal/http/library_item_handlers.go \
  server/internal/http/library_bridge_handlers.go server/internal/http/library_folder_handlers_test.go \
  server/internal/http/admin_gc_handlers.go server/internal/http/library_handlers_db_test.go
git commit -m "feat(server): 素材库接口（目录、条目、存入、应用到画布）与 /admin/gc 复用孤儿清扫"
```

---

### Task 12: 路由与 sidecar 装配、优雅退出、端到端测试

**Files:**
- Modify（重写）: `server/internal/http/router.go`
- Modify: `server/internal/http/router_test.go`（追加）
- Modify（重写）: `server/cmd/sidecar/main.go`
- Create: `server/cmd/sidecar/wire.go`
- Test: `server/cmd/sidecar/wire_test.go`（新写）、`server/cmd/sidecar/main_test.go`（修改 + 追加）
- Modify: `server/go.mod`、`server/go.sum`（`go mod tidy`）

**Interfaces:**
- Consumes: 前面所有任务的 Produces。
- Produces：
  - `http.Deps` 新增字段 `MediaSigner *MediaSigner`、`Media *MediaHandler`、`Me *MeHandler`、`Asset *AssetHandler`、`Project *ProjectHandler`、`CanvasMutation *CanvasMutationHandler`、`CanvasAssetImport *CanvasAssetImportHandler`、`LibraryFolder *LibraryFolderHandler`、`LibraryItem *LibraryItemHandler`、`LibraryBridge *LibraryBridgeHandler`、`AdminGC *AdminGCHandler`（保留 P0 的 `Auth`、`DataDir`、`Version`）。
  - `http.MustWire(d Deps)`：业务字段有 nil 就 panic（路由按字段注册，漏装配不会编译失败，直到请求进来才 nil 解引用——参考仓 `MustWireCLI` 的同一思路）。
  - `main.wire(conn *sql.DB, storage *assets.LocalStorage, auth localauth.Config, dataDir, version string) *app`，`app{deps apphttp.Deps; cleaner *apphttp.ProjectCleaner; sweeper *library.OrphanSweeper}`。
  - 启动协议（与 Task 3 的壳对应）：stdin 第一行 token（4096 字节内、长度 32–256）；打开 `<data-dir>/data.db` 并迁移；监听后打印 `READY <port>`；之后 stdin 读到一行 `SHUTDOWN` 或 EOF（以及 SIGINT/SIGTERM）时停止 HTTP（最多等 5 秒）、停后台 worker、关数据库，以 0 退出。默认 Origin 白名单去掉 `http://localhost:5173`。

**路由表（全部在 localauth 之内）：**

| 路由 | handler | 备注 |
|---|---|---|
| `GET`/`HEAD /media/*` | `Media.Serve` | 不在 `/api/v1` 下，不挂身份中间件；媒体 token 可访问 |
| `GET /api/v1/ping`、`GET /api/v1/agent/ws`、`POST /api/v1/diag/upload`、`GET /api/v1/diag/media/{name}` | P0 原样 | WS 回显 P1c/P5 替换；diag P3 移进设置页 |
| 以下均挂 `LocalIdentity` | | |
| `GET /api/v1/me` | `Me.Me` | |
| `GET`/`POST /api/v1/projects` | `Project.List` / `Create` | |
| `/api/v1/projects/{projectID}`（挂 `ProjectFromURL`）：`GET`/`PUT`/`DELETE /`、`GET /snapshot`、`GET /images`、`PUT /cover`、`POST /duplicate`、`POST /assets/import-refs`、`GET /canvas-owner/probe`、`POST /canvas/owner/claim`、`POST /canvas/owner/heartbeat`、`POST /canvas/nodes`、`PATCH /canvas/nodes/positions`、`PATCH /canvas/nodes/{nodeID}`、`DELETE /canvas/nodes`、`POST /canvas/connections`、`DELETE /canvas/connections/{connectionID}` | Project / CanvasAssetImport / CanvasMutation | 路径与参考仓逐字一致 |
| `POST`/`GET /api/v1/assets` | `Asset.Upload` / `List` | |
| `POST /api/v1/media/thumbs` | `MediaThumbs(MediaSigner)` | 返回原图 URL（S8） |
| `POST /api/v1/media/reveal` | `Media.Reveal` | 新增 |
| `/api/v1/library`：`GET`/`POST /folders`、`PATCH`/`DELETE /folders/{folderID}`、`POST /folders/{folderID}/items/upload`、`GET /items`、`PATCH`/`DELETE /items/{itemID}`、`POST /items/{itemID}/apply-to-canvas` | Library* | |
| `POST /api/v1/projects/{projectID}/assets/{assetID}/save-to-library`、`POST /api/v1/library/save-from-url` | `LibraryBridge` | 与参考仓相同，注册在 `Route` 块之外（chi 静态段优先于挂载的通配） |
| `POST /api/v1/admin/gc` | `AdminGC.Run` | |

- [ ] **Step 1: 写失败的端到端测试与路由测试**

`server/cmd/sidecar/wire_test.go`：

```go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

const e2eMediaToken = "fedcba9876543210fedcba9876543210"

var e2ePNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 24)...)

type e2eEnv struct {
	base    string
	conn    *sql.DB
	storage *assets.LocalStorage
	app     *app
}

// newE2E 用真实的装配函数起一个完整的 sidecar HTTP 服务（只是不经过 stdin 握手）。
func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	conn := dbtest.Open(t)
	srv := httptest.NewUnstartedServer(nil)
	base := "http://" + srv.Listener.Addr().String()
	dataDir := t.TempDir()
	storage, err := assets.NewLocalStorage(assets.LocalConfig{Root: filepath.Join(dataDir, "media"), BaseURL: base, Token: e2eMediaToken})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "secret.txt"), []byte("API-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := wire(conn, storage, localauth.Config{Token: testToken, MediaToken: e2eMediaToken, AllowedOrigins: []string{"tauri://localhost"}}, dataDir, "test")
	apphttp.MustWire(a.deps)
	srv.Config.Handler = apphttp.NewRouter(a.deps)
	srv.Start()
	t.Cleanup(srv.Close)
	return &e2eEnv{base: base, conn: conn, storage: storage, app: a}
}

func (e *e2eEnv) call(t *testing.T, method, path string, body io.Reader, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.base+path, body)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (e *e2eEnv) callJSON(t *testing.T, method, path, body string) (*http.Response, []byte) {
	return e.call(t, method, path, strings.NewReader(body), "application/json")
}

func (e *e2eEnv) upload(t *testing.T, projectID int64, filename string) (id int64, url string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {"image/png"},
	})
	part.Write(append(append([]byte{}, e2ePNG...), filename...)) // 内容各不相同，避免去重命中
	mw.Close()
	resp, b := e.call(t, http.MethodPost, fmt.Sprintf("/api/v1/assets?projectId=%d", projectID), &buf, mw.FormDataContentType())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s: code=%d body=%s", filename, resp.StatusCode, b)
	}
	var out struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	json.Unmarshal(b, &out)
	return out.ID, out.URL
}

// 从前端视角走一遍：建项目 → 上传 → 媒体 URL 读回（Range）→ 画布引用 → 快照往返 → 素材库 → 删除项目 → 清理与回收。
func TestE2EProjectUploadCanvasMediaLifecycle(t *testing.T) {
	e := newE2E(t)

	// 1. 建项目
	resp, b := e.callJSON(t, http.MethodPost, "/api/v1/projects", `{"name":"未命名项目"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %s", resp.StatusCode, b)
	}
	var proj struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(b, &proj)

	// 2. 上传（文件名带 Windows 非法字符）
	assetID, mediaURL := e.upload(t, proj.ID, "镜头:1?.png")
	_, tempURL := e.upload(t, proj.ID, "临时.png")

	// 3. 媒体 URL 只带媒体 token，不带接口 token
	if !strings.HasPrefix(mediaURL, e.base+"/media/") || !strings.Contains(mediaURL, "token="+e2eMediaToken) || strings.Contains(mediaURL, testToken) {
		t.Fatalf("mediaURL=%q", mediaURL)
	}

	// 4. <video>/<img> 形态的请求：不带 Authorization，只靠 URL 里的 token；Range 返回 206
	req, _ := http.NewRequest(http.MethodGet, mediaURL, nil)
	req.Header.Set("Range", "bytes=0-7")
	mresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	mbody, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusPartialContent || !bytes.Equal(mbody, e2ePNG[:8]) || mresp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("media: code=%d type=%q body=%q", mresp.StatusCode, mresp.Header.Get("Content-Type"), mbody)
	}

	// 5. 媒体 token 不能调接口（Review Focus 5）
	presp, err := http.Get(e.base + "/api/v1/projects?token=" + e2eMediaToken)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()
	if presp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("媒体 token 调接口 code=%d want 401", presp.StatusCode)
	}

	// 6. 画布引用：提交本地 URL，落库必须是 media://
	resp, b = e.callJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/canvas/nodes", proj.ID),
		fmt.Sprintf(`{"ownerToken":"t","node":{"id":"n1","type":"image","content":%q}}`, mediaURL))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert node: %d %s", resp.StatusCode, b)
	}
	var stored string
	e.conn.QueryRow(`SELECT payload FROM workflow_snapshots WHERE project_id = ?`, proj.ID).Scan(&stored)
	if !strings.Contains(stored, `"media://u1/`) || strings.Contains(stored, "token=") {
		t.Fatalf("落库 payload=%s", stored)
	}
	resp, b = e.call(t, http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/snapshot", proj.ID), nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), e.base+"/media/u1/") {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, b)
	}

	// 7. /me
	resp, b = e.call(t, http.MethodGet, "/api/v1/me", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"role":"admin"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, b)
	}

	// 8. 素材库：建目录、按 URL 存入、按 asset 存入（两条都注册在 /library 挂载块之外）
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/library/folders", `{"name":"素材"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create folder: %d %s", resp.StatusCode, b)
	}
	var folder struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(b, &folder)
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/library/save-from-url", fmt.Sprintf(`{"url":%q,"folderId":%d}`, mediaURL, folder.ID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save-from-url: %d %s", resp.StatusCode, b)
	}
	resp, b = e.callJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/assets/%d/save-to-library", proj.ID, assetID), fmt.Sprintf(`{"folderId":%d}`, folder.ID))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save-to-library: %d %s", resp.StatusCode, b)
	}

	// 9. 缩略图接口返回原图 URL；reveal 对认不出的 URL 返回 400（不会真的打开访达）
	resp, b = e.callJSON(t, http.MethodPost, "/api/v1/media/thumbs", fmt.Sprintf(`{"urls":[%q]}`, mediaURL))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), e.base+"/media/u1/") {
		t.Fatalf("thumbs: %d %s", resp.StatusCode, b)
	}
	resp, _ = e.callJSON(t, http.MethodPost, "/api/v1/media/reveal", `{"url":"https://cdn.example/x.png"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reveal 外部 URL code=%d want 400", resp.StatusCode)
	}

	// 10. 删除项目 → 清理 worker 回收：被素材库引用的文件留下，只在项目里的进孤儿队列
	resp, _ = e.call(t, http.MethodDelete, fmt.Sprintf("/api/v1/projects/%d", proj.ID), nil, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project code=%d", resp.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.app.cleaner.Start(ctx, 20*time.Millisecond)
	tempKey := strings.TrimPrefix(strings.SplitN(tempURL, "?", 2)[0], e.base+"/media/")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		e.conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects WHERE object_key = ?`, tempKey).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("5 秒内项目清理没有把临时文件标成孤儿")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var orphanCount int
	e.conn.QueryRow(`SELECT COUNT(*) FROM orphan_objects`).Scan(&orphanCount)
	if orphanCount != 1 {
		t.Fatalf("孤儿数=%d want 1（素材库还引用着第一张图）", orphanCount)
	}

	// 11. 过了宽限期的孤儿被清扫删除；仍被引用的文件照常可读
	e.conn.Exec(`UPDATE orphan_objects SET marked_at = '2000-01-01 00:00:00'`)
	if res, err := e.app.sweeper.Sweep(context.Background(), library.DefaultGracePeriod); err != nil || res.Deleted != 1 {
		t.Fatalf("sweep res=%+v err=%v", res, err)
	}
	gone, _ := http.Get(tempURL)
	gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Fatalf("被清扫的文件 code=%d want 404", gone.StatusCode)
	}
	kept, _ := http.Get(mediaURL)
	kept.Body.Close()
	if kept.StatusCode != http.StatusOK {
		t.Fatalf("仍被素材库引用的文件 code=%d want 200", kept.StatusCode)
	}
}

// Review Focus 4：经真实路由的穿越尝试（编码后的 ..）一律 404，绝不吐出数据目录里的其他文件。
func TestE2EMediaTraversalIsRejected(t *testing.T) {
	e := newE2E(t)
	for _, p := range []string{"/media/%2e%2e/secret.txt", "/media/..%2fsecret.txt", "/media/u1/%2e%2e/%2e%2e/secret.txt"} {
		resp, err := http.Get(e.base + p + "?token=" + e2eMediaToken)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || strings.Contains(string(b), "API-KEY") {
			t.Fatalf("%s: code=%d body=%q", p, resp.StatusCode, b)
		}
	}
}

func TestMustWirePanicsOnMissingHandler(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("缺 handler 时 MustWire 应 panic")
		}
	}()
	apphttp.MustWire(apphttp.Deps{})
}
```

在 `server/internal/http/router_test.go` 末尾追加：

```go
// P0 遗留：WebSocket 握手的外来 Origin 必须被 localauth 挡下，哪怕 token 正确。
func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/agent/ws?token=" + testToken
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: nethttp.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("外来 Origin 也连上了")
	}
	if resp == nil || resp.StatusCode != nethttp.StatusForbidden {
		t.Fatalf("resp = %v, want 403", resp)
	}
}

// 业务路由挂在 localauth 之内：没有 token 一律 401（不会因为 handler 未装配而 panic）。
func TestBusinessRoutesRequireToken(t *testing.T) {
	srv := newTestServer(t)
	for _, p := range []string{"/api/v1/me", "/api/v1/projects", "/media/u1/a.png"} {
		resp, err := nethttp.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != nethttp.StatusUnauthorized {
			t.Fatalf("%s: code = %d, want 401", p, resp.StatusCode)
		}
	}
}
```

修改 `server/cmd/sidecar/main_test.go`：

1. `startSidecar` 改为接受额外参数：签名改为 `func startSidecar(t *testing.T, bin, dataDir string, extraArgs ...string) (*exec.Cmd, io.WriteCloser, string)`，第一行改为 `cmd := exec.Command(bin, append([]string{"--data-dir", dataDir}, extraArgs...)...)`。已有调用点不用改。
2. 在文件末尾追加：

```go
func TestReadTokenRejectsTooLong(t *testing.T) {
	for _, n := range []int{300, 5000} {
		if _, err := readToken(bufio.NewReader(strings.NewReader(strings.Repeat("a", n) + "\n"))); err == nil {
			t.Fatalf("%d 字节的 token 应被拒绝", n)
		}
	}
}

func TestWaitForShutdownReturnsOnShutdownLineOrEOF(t *testing.T) {
	done := make(chan struct{})
	go func() {
		waitForShutdown(bufio.NewReader(strings.NewReader("noise\nSHUTDOWN\r\nmore\n")))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("读到 SHUTDOWN 行后应返回")
	}
	waitForShutdown(bufio.NewReader(strings.NewReader(""))) // EOF 立即返回
}

// 壳正常退出时写一行 SHUTDOWN：sidecar 关库后以 0 退出，库文件已建好。
func TestShutdownLineExitsCleanly(t *testing.T) {
	bin := buildSidecar(t)
	dataDir := t.TempDir()
	cmd, stdin, _ := startSidecar(t, bin, dataDir)
	fmt.Fprintln(stdin, "SHUTDOWN")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出码非 0: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SHUTDOWN 后 5 秒仍未退出")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data.db")); err != nil {
		t.Fatalf("data.db 没有建在数据目录: %v", err)
	}
}

// P0 遗留：P0 用 0755 建过目录的老安装，再启动时要收紧成 0700。
func TestDataDirBecomesPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的 %APPDATA% 本身是用户私有目录，chmod 只管只读位")
	}
	bin := buildSidecar(t)
	dataDir := filepath.Join(t.TempDir(), "com.miaoju.desktop")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dataDir, 0o755)
	startSidecar(t, bin, dataDir)
	st, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("perm=%v want 0700", st.Mode().Perm())
	}
}

// P0 遗留：开发服务器的来源只在壳的 debug 构建经 --origins 传入，默认白名单里没有它。
func TestDefaultOriginsExcludeDevServer(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())
	check := func(origin string, want int) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/api/v1/ping", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("Origin %s: code=%d want %d", origin, resp.StatusCode, want)
		}
	}
	check("tauri://localhost", http.StatusOK)
	check("http://tauri.localhost", http.StatusOK)
	check("http://localhost:5173", http.StatusForbidden)
}

func TestOriginsFlagOverridesDefault(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir(), "--origins", "http://localhost:5173")
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/api/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "http://localhost:5173")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("code=%d want 200", resp.StatusCode)
	}
}

// P0 遗留：启动失败必须以退出码 1 结束，并在 stderr 留下原因（壳据此显示错误页）。
func TestStartupFailuresExitOneWithStderr(t *testing.T) {
	bin := buildSidecar(t)
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"缺 --data-dir", nil, testToken + "\n"},
		{"token 过短", []string{"--data-dir", t.TempDir()}, "short\n"},
		{"stdin 直接关闭", []string{"--data-dir", t.TempDir()}, ""},
	}
	for _, c := range cases {
		cmd := exec.Command(bin, c.args...)
		cmd.Stdin = strings.NewReader(c.stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("%s: err=%v，want 退出码 1", c.name, err)
		}
		if strings.TrimSpace(stderr.String()) == "" {
			t.Fatalf("%s: stderr 为空", c.name)
		}
	}
}
```

并在 `main_test.go` 的 import 块里加入 `"bytes"`、`"errors"`（其余 `bufio`、`fmt`、`io`、`net/http`、`os`、`os/exec`、`path/filepath`、`runtime`、`strings`、`testing`、`time` 已在）。

Run: `cd server && go test ./cmd/sidecar/ ./internal/http/`
Expected: 编译失败——`undefined: wire`、`undefined: apphttp.MustWire`、`undefined: waitForShutdown`。

- [ ] **Step 2: 重写 router.go**

`server/internal/http/router.go` 整体替换为：

```go
// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，业务 handler 直接落在这个包里。
package http

import (
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// Deps 是 DI 容器，由 cmd/sidecar 的 wire 填充。
type Deps struct {
	Auth    localauth.Config
	DataDir string
	Version string

	MediaSigner       *MediaSigner
	Media             *MediaHandler
	Me                *MeHandler
	Asset             *AssetHandler
	Project           *ProjectHandler
	CanvasMutation    *CanvasMutationHandler
	CanvasAssetImport *CanvasAssetImportHandler
	LibraryFolder     *LibraryFolderHandler
	LibraryItem       *LibraryItemHandler
	LibraryBridge     *LibraryBridgeHandler
	AdminGC           *AdminGCHandler
}

// MustWire 在启动期校验业务 handler 都已装配，缺一个就 panic。路由按字段注册，漏装配不会编译失败，
// 直到有请求进来才 nil 解引用（参考仓 MustWireCLI 的同一思路）。不放进 NewRouter：P0 的路由测试
// 只装配了 ping/diag，放进去会把它们打挂。
func MustWire(d Deps) {
	var missing []string
	for name, ok := range map[string]bool{
		"MediaSigner": d.MediaSigner != nil, "Media": d.Media != nil, "Me": d.Me != nil, "Asset": d.Asset != nil,
		"Project": d.Project != nil, "CanvasMutation": d.CanvasMutation != nil, "CanvasAssetImport": d.CanvasAssetImport != nil,
		"LibraryFolder": d.LibraryFolder != nil, "LibraryItem": d.LibraryItem != nil, "LibraryBridge": d.LibraryBridge != nil,
		"AdminGC": d.AdminGC != nil,
	} {
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		panic("sidecar 依赖未接线（检查 cmd/sidecar/wire.go）: " + strings.Join(missing, ", "))
	}
}

func NewRouter(d Deps) nethttp.Handler {
	r := chi.NewRouter()
	// 鉴权必须挂在最外层：挂在 Group 里的话，预检请求（OPTIONS）打到只注册了
	// GET 的路径会先被 chi 判成 405，根本走不到中间件。
	r.Use(localauth.Middleware(d.Auth))

	// 本地媒体文件：<img>/<video> 直接请求，token 走 ?token=（媒体专用 token 只能访问这里）。
	r.Get("/media/*", d.Media.Serve)
	r.Head("/media/*", d.Media.Serve)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
		})
		// P0 的回显占位；P1c 换成只做 canvas_changed 的最小 Agent hub，P5 换成完整 Agent。
		r.Get("/agent/ws", wsEcho)
		// 诊断页（P3 移进“设置 → 环境诊断”）。
		diag := &diagHandler{dir: d.DataDir}
		r.Post("/diag/upload", diag.upload)
		r.Get("/diag/media/{name}", diag.media)

		r.Group(func(r chi.Router) {
			r.Use(LocalIdentity)

			r.Get("/me", d.Me.Me)

			r.Get("/projects", d.Project.List)
			r.Post("/projects", d.Project.Create)
			r.Route("/projects/{projectID}", func(r chi.Router) {
				r.Use(ProjectFromURL)
				r.Get("/", d.Project.Get)
				r.Put("/", d.Project.Update)
				r.Delete("/", d.Project.Delete)
				r.Get("/snapshot", d.Project.GetSnapshot)
				r.Get("/images", d.Project.ListImages)
				r.Put("/cover", d.Project.SetCover)
				r.Post("/duplicate", d.Project.Duplicate)
				// 「复制节点」跨项目粘贴后的资产引用登记。
				r.Post("/assets/import-refs", d.CanvasAssetImport.ImportRefs)
				// 编辑锁三个端点保留（前端协议不变），本地实现恒放行（spec X2、S11）。
				r.Get("/canvas-owner/probe", d.CanvasMutation.ProbeOwner)
				r.Post("/canvas/owner/claim", d.CanvasMutation.ClaimOwner)
				r.Post("/canvas/owner/heartbeat", d.CanvasMutation.HeartbeatOwner)
				r.Post("/canvas/nodes", d.CanvasMutation.UpsertNode)
				r.Patch("/canvas/nodes/positions", d.CanvasMutation.PatchNodePositions)
				r.Patch("/canvas/nodes/{nodeID}", d.CanvasMutation.PatchNode)
				r.Delete("/canvas/nodes", d.CanvasMutation.DeleteNodes)
				r.Post("/canvas/connections", d.CanvasMutation.UpsertConnection)
				r.Delete("/canvas/connections/{connectionID}", d.CanvasMutation.DeleteConnection)
			})

			r.Post("/assets", d.Asset.Upload)
			r.Get("/assets", d.Asset.List)

			// 画布展示层批量换缩略图 URL；本地不生成缩略图，返回原图 URL（spec S8）。
			r.Post("/media/thumbs", MediaThumbs(d.MediaSigner))
			// 节点右键「在访达/资源管理器中显示」（spec S3）。
			r.Post("/media/reveal", d.Media.Reveal)

			r.Route("/library", func(r chi.Router) {
				r.Get("/folders", d.LibraryFolder.List)
				r.Post("/folders", d.LibraryFolder.Create)
				r.Patch("/folders/{folderID}", d.LibraryFolder.Update)
				r.Delete("/folders/{folderID}", d.LibraryFolder.Delete)
				r.Post("/folders/{folderID}/items/upload", d.LibraryItem.Upload)
				r.Get("/items", d.LibraryItem.List)
				r.Patch("/items/{itemID}", d.LibraryItem.Update)
				r.Delete("/items/{itemID}", d.LibraryItem.Delete)
				r.Post("/items/{itemID}/apply-to-canvas", d.LibraryBridge.ApplyToCanvas)
			})
			r.Post("/projects/{projectID}/assets/{assetID}/save-to-library", d.LibraryBridge.SaveToLibrary)
			r.Post("/library/save-from-url", d.LibraryBridge.SaveFromURL)

			// 手动触发孤儿清扫（sidecar 也会在启动时和每天自动跑）。
			r.Post("/admin/gc", d.AdminGC.Run)
		})
	})
	return r
}
```

（`r.Get("/media/*", d.Media.Serve)` 在 `d.Media` 为 nil 时只是保存一个 nil 接收者的方法值，注册不会 panic；P0 的路由测试不调用这些路由。）

- [ ] **Step 3: 写 wire.go，重写 main.go**

`server/cmd/sidecar/wire.go`：

```go
package main

import (
	"database/sql"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/reveal"
	"github.com/james/huabu_studio/apps/api/internal/users"
	"github.com/james/huabu_studio/apps/api/internal/workflows"
)

// app 是装配好的 sidecar：HTTP 依赖 + 需要在启动后运行的后台任务。
type app struct {
	deps    apphttp.Deps
	cleaner *apphttp.ProjectCleaner
	sweeper *library.OrphanSweeper
}

// wire 是唯一的装配点（对应参考仓 cmd/server/main.go 里画布相关的那部分）。
// 生成任务清理（taskProjectCleanup）在 P1b 传 nil，P1c 接 generation 后改传 generation 仓库；
// 画布变更广播（SetChangeNotifier）在 P1b 不注入，P1c 接 Agent Hub。
func wire(conn *sql.DB, storage *assets.LocalStorage, auth localauth.Config, dataDir, version string) *app {
	signer := apphttp.NewMediaSigner(storage)
	projectsRepo := projects.NewSQLiteRepository(conn)
	assetsRepo := assets.NewSQLiteRepository(conn)
	usersRepo := users.NewSQLiteRepository(conn)
	orphans := library.NewSQLiteOrphansRepository(conn)
	folders := library.NewSQLiteFoldersRepository(conn)
	items := library.NewSQLiteItemsRepository(conn)
	duplicator := workflows.NewAssetDuplicator(storage, assetsRepo, usersRepo, workflows.NewDeleteNowQueue(storage))
	sweeper := library.NewOrphanSweeper(orphans, storage, conn)

	deps := apphttp.Deps{
		Auth:              auth,
		DataDir:           dataDir,
		Version:           version,
		MediaSigner:       signer,
		Media:             apphttp.NewMediaHandler(storage, signer, apphttp.NewSQLMediaMimeStore(conn), reveal.InFileManager),
		Me:                apphttp.NewMeHandler(usersRepo),
		Asset:             apphttp.NewAssetHandler(storage, assetsRepo, usersRepo, signer),
		Project:           apphttp.NewProjectHandler(projectsRepo, signer, storage, assetsRepo, usersRepo, nil, orphans, conn, duplicator),
		CanvasMutation:    apphttp.NewCanvasMutationHandler(projectsRepo, canvasowner.NewLocal(), signer),
		CanvasAssetImport: apphttp.NewCanvasAssetImportHandler(apphttp.NewSQLCanvasAssetRefStore(conn), assetsRepo),
		LibraryFolder:     apphttp.NewLibraryFolderHandler(folders, items, orphans, usersRepo, conn),
		LibraryItem:       apphttp.NewLibraryItemHandler(items, folders, orphans, storage, usersRepo, signer, conn),
		LibraryBridge:     apphttp.NewLibraryBridgeHandler(items, folders, assetsRepo, signer, conn),
		AdminGC:           apphttp.NewAdminGCHandler(sweeper),
	}
	cleaner := apphttp.NewProjectCleaner(projectsRepo, assetsRepo, usersRepo, nil, orphans, conn)
	return &app{deps: deps, cleaner: cleaner, sweeper: sweeper}
}
```

`server/cmd/sidecar/main.go` 整体替换为：

```go
// sidecar 是桌面端的本地服务进程，由 Tauri 壳拉起。
//
// 启动协议（壳与 sidecar 的唯一契约，改动要两边一起改，壳侧见 src-tauri/src/lib.rs）：
//  1. 壳通过 stdin 写入一行 token；
//  2. sidecar 打开 <data-dir>/data.db 并迁移，监听 127.0.0.1 的随机端口，就绪后向 stdout 打印 "READY <port>"；
//  3. 之后 stdin 上读到一行 "SHUTDOWN"（壳正常退出）或 EOF（壳崩溃、被强杀），sidecar 停止接收请求、
//     停掉后台任务、关闭数据库后以 0 退出。
//
// token 走 stdin 而不是参数或环境变量：后两者同机其他进程读得到。
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// version 由构建脚本通过 -ldflags "-X main.version=…" 注入。
var version = "dev"

// defaultOrigins 只含打包后的两个来源。开发服务器 http://localhost:5173 由壳在 debug 构建里
// 经 --origins 传入（P0 遗留：release 包不能放行它）。
const defaultOrigins = "tauri://localhost,http://tauri.localhost"

const (
	maxTokenLen          = 256
	shutdownTimeout      = 5 * time.Second
	projectCleanInterval = 30 * time.Second
	orphanSweepInterval  = 24 * time.Hour
)

func main() {
	log.SetOutput(os.Stderr)
	if err := run(); err != nil {
		log.Printf("sidecar 启动失败: %v", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "数据目录（必填）")
	origins := flag.String("origins", defaultOrigins, "允许的 Origin，逗号分隔")
	flag.Parse()
	if *dataDir == "" {
		return errors.New("缺少 --data-dir")
	}
	if err := ensurePrivateDir(*dataDir); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}

	stdin := bufio.NewReader(os.Stdin)
	token, err := readToken(stdin)
	if err != nil {
		return err
	}

	conn, err := db.Open(filepath.Join(*dataDir, "data.db"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("数据库迁移: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("监听端口: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mediaToken, err := randomHex(32)
	if err != nil {
		ln.Close()
		return fmt.Errorf("生成媒体 token: %w", err)
	}
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root:    filepath.Join(*dataDir, "media"),
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		Token:   mediaToken,
	})
	if err != nil {
		ln.Close()
		return err
	}

	a := wire(conn, storage, localauth.Config{
		Token:          token,
		MediaToken:     mediaToken,
		AllowedOrigins: splitOrigins(*origins),
	}, *dataDir, version)
	apphttp.MustWire(a.deps)
	srv := &nethttp.Server{
		Handler: apphttp.NewRouter(a.deps),
		// P0 遗留：防慢速请求头占满连接。
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.cleaner.Start(ctx, projectCleanInterval)
	a.sweeper.Start(ctx, orphanSweepInterval, library.DefaultGracePeriod)

	go func() {
		waitForShutdown(stdin)
		log.Print("收到退出信号（stdin），sidecar 正在关闭")
		cancel()
	}()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			log.Print("收到系统信号，sidecar 正在关闭")
			cancel()
		case <-ctx.Done():
		}
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	fmt.Printf("READY %d\n", port)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	sctx, scancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Printf("关闭 HTTP 服务: %v", err)
	}
	// 后台任务随 ctx 取消停下；deferred conn.Close 会等在飞的语句结束。
	return nil
}

// ensurePrivateDir 建数据目录并收紧成仅当前用户可访问（data.db 里有 API Key，spec M3）。
// P0 用 0755 建过的老目录也在这里改掉。Windows 的 %APPDATA% 本身是用户私有目录，chmod 只管只读位。
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(dir, 0o700)
}

// readToken 读 stdin 第一行。ReadSlice 受 bufio 默认 4096 字节缓冲限制，超长输入直接报错，
// 不会把任意长的一行读进内存。TrimSpace 同时处理 Windows 的 \r\n。
func readToken(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", fmt.Errorf("token 长度超过 %d", maxTokenLen)
	}
	token := strings.TrimSpace(string(line))
	if token == "" && err != nil {
		return "", fmt.Errorf("读取 token: %w", err)
	}
	if len(token) < 32 {
		return "", errors.New("token 长度不足 32")
	}
	if len(token) > maxTokenLen {
		return "", fmt.Errorf("token 长度超过 %d", maxTokenLen)
	}
	return token, nil
}

// waitForShutdown 阻塞到 stdin 上出现一行 SHUTDOWN 或 EOF（壳退出、崩溃、被强杀都会关管道）。
func waitForShutdown(r *bufio.Reader) {
	for {
		line, err := r.ReadString('\n')
		if strings.TrimSpace(line) == "SHUTDOWN" || err != nil {
			return
		}
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
```

- [ ] **Step 4: 整理依赖**

Run: `cd server && go mod tidy && git diff --stat go.mod go.sum`
Expected: `github.com/google/uuid` 从 indirect 挪进直接依赖，`golang.org/x/sync` 已在；没有新增其他模块。若 tidy 需要联网失败，用 `GOPROXY=off go mod tidy`（所需模块都在本机缓存）。

- [ ] **Step 5: 运行全部测试**

Run: `cd server && go vet ./... && go test ./... -race`
Expected: 全部 PASS，包括 `wire_test` 3 个、`router_test` 新增 2 个、`main_test` 原有 7 个 + 新增 7 个。

若 `TestE2EProjectUploadCanvasMediaLifecycle` 在第 8 步 save-from-url / save-to-library 返回 404 或 405：说明 chi 没有让静态段优先于 `/library`、`/projects/{projectID}` 的挂载通配；不要改测试，把这两条路由挪进对应的 `r.Route` 块内（`/library` 块里注册 `POST /save-from-url`；`/projects/{projectID}` 块里注册 `POST /assets/{assetID}/save-to-library`，并把 `SaveToLibrary` 读 `projectID` 的方式保持不变——它用 `chi.URLParam(r, "projectID")`，在子路由里同样取得到），并在报告里说明。

- [ ] **Step 6: 交叉编译与二进制冒烟**

Run: `cd server && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/sidecar && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /dev/null ./cmd/sidecar`
Expected: 成功。

手动冒烟（可选，验证真实二进制）：

```bash
cd server && go build -o /tmp/p1b-sidecar ./cmd/sidecar
TOKEN=$(openssl rand -hex 32); DIR=$(mktemp -d)/"Application Support"/com.miaoju.desktop
mkfifo /tmp/p1b-in; (/tmp/p1b-sidecar --data-dir "$DIR" < /tmp/p1b-in > /tmp/p1b-out 2>&1 &) ; exec 3>/tmp/p1b-in; echo "$TOKEN" >&3
sleep 1; PORT=$(awk '/^READY/{print $2}' /tmp/p1b-out)
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"冒烟"}' http://127.0.0.1:$PORT/api/v1/projects
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:$PORT/api/v1/me
echo SHUTDOWN >&3; exec 3>&-; sleep 1; pgrep -f p1b-sidecar || echo "已退出"; rm /tmp/p1b-in
```

Expected: 建项目返回 201 JSON，`/me` 返回 `"role":"admin"`，`SHUTDOWN` 后进程退出，`$DIR` 权限为 `drwx------`、其中有 `data.db`。

- [ ] **Step 7: 提交**

```bash
git add server/internal/http/router.go server/internal/http/router_test.go server/cmd/sidecar server/go.mod server/go.sum
git commit -m "feat(server): 装配 P1b 路由与依赖；sidecar 打开 SQLite、媒体 token、优雅退出、数据目录 0700"
```

---

## P1b 完成标准

- `cd server && go vet ./... && go test ./... -race` 全部通过，没有任何测试被 skip（`TestDataDirBecomesPrivate` 只在 Windows 上 skip）。
- `cd server && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...` 成功。
- `cd src-tauri && cargo test && cargo build` 通过、无警告。
- `pnpm --dir web test`、`pnpm --dir web build` 仍通过（P1b 不改前端；P0 诊断页依赖的 ping/diag/WS 路由路径不变）。
- 用 `pnpm tauri dev` 启动应用，诊断页 3 项连接检查仍 PASS（验证 debug 构建传入的 `--origins` 让开发源生效）。
- 各任务 Interfaces 中列出的导出 API 与实现一致。

## 风险与未决问题

1. **（待确认）`canvas_changed` 广播推迟到 P1c。** 理由见文首“范围”第 2 条。若评审要求 P1b 就提供最小 hub，需要额外一个任务：在 `internal/agent` 建只含 `Register/Unregister/NotifyCanvasChanged` 的 Hub、复制 `agent/envelope.go` 的帧定义、把 `/api/v1/agent/ws` 从回显换成 hub 注册，并在装配时 `CanvasMutation.SetChangeNotifier(hub)`。
2. **（待确认）“在访达中显示”由 sidecar 打开**，壳不加 capability。与 spec SEC4 文字（capabilities 含“在访达中显示”）不同，但授权面更小；若坚持由壳打开，则改为 `Reveal` 返回真实路径、壳用 opener 插件，并需给 capability 加路径 scope（数据目录可改，scope 难以静态声明）。
3. **（待确认）媒体专用 token。** spec SEC2 只写了“每次启动随机 token”；本计划多加了一个只读媒体 token，媒体 URL 泄露（DOM、日志、Agent 提示词）时不至于泄露接口权限。代价：localauth 多一条规则。
4. **上传时长探测取消**：参考仓用 ffprobe 取权威时长防计费作弊；桌面端不计费，且用户机器上没有 ffprobe。P1d 的去字幕/转高清若需要服务端时长（参考仓 `CanvasPostprocessHandler.Desub` 读 `GetDurationMsByObjectKey`，读不到时会懒探测），届时需要纯 Go 的 mp4 时长解析或改信前端上报——P1d 计划要处理。
5. **单文件上限 1 GiB 且整文件读进内存**（沿用参考仓 `io.ReadAll`）。大视频上传会占同等内存；若实际用户常传更大的文件，后续把 `storeUpload` 改为边算哈希边流式写临时文件。
6. **Windows 上覆盖/删除正在被读取的文件会失败**（Go 打开文件不带 `FILE_SHARE_DELETE`）：`PutObject` 覆盖写（仅 P1c 生成产物重放时发生）可能报错；孤儿清扫删除失败会保留孤儿行、次日重试（已有测试）。需在 Windows 实机上观察。
7. **崩溃留下的 `.tmp-*` 临时文件**不会被任何逻辑清理（孤儿清扫只认 `orphan_objects`）。量很小，暂不处理；若要处理，可在 sidecar 启动时删除媒体目录下一天前的 `.tmp-*`。
8. **项目副本回滚时删文件失败**只记日志（`DeleteNowQueue`），该文件没有 asset 行，孤儿清扫看不到——与参考仓 `cleanup_jobs` 重试 10 次相比更弱，本地删除几乎不会失败，接受。
9. **`ExtractKey` 认任意端口的 `http://127.0.0.1:<port>/media/…`**：若用户画布里恰好引用了本机另一个服务的同形 URL，会被当成自己的媒体（找不到文件就是 404）。概率极低，换来的是重启后旧 URL 仍可认回（P1c 任务恢复依赖）。
10. **spec S12（输入媒体的公网 URL）仍待定**：P1b 的 `SignedURL` 只产出本机 URL，任何需要上游下载输入媒体的生成在 P1c 都会失败，直到 S12 定案。接口位（`Storage`、`MediaSigner`、`ExtractKey`）已保留。

## 自检记录

- **spec 覆盖：** S1（data.db + media/，0700：Task 12、3）· S2（只存 `media://`，数据目录可整体搬：Task 1 说明）· S3（`media://` 转本地 URL：Task 1、5；在访达中显示：Task 8）· S4（参考仓 key 布局 + content_hash 去重：Task 1、8）· S8（缩略图返回原图：Task 1、5）· S9（孤儿 7 天宽限 + 自动定时：Task 2、12）· S11（单窗口 → 编辑锁恒放行：Task 10）· S12（不实现上传，保留接口位：范围说明、风险 10）· X1（发布删除，只留副本用的 duplicator：Task 7）· X2（编辑锁删除：Task 10）· X3（不计费 → 时长探测可选：Task 8）· X4（本地用户：Task 5、6、8）· SEC1/SEC2（localauth 不变 + 媒体 token：Task 3、12）· SEC3（token 仍走 stdin：Task 12）· SEC4（壳不新增 capability：Task 8 决定）· D2/D3（REST 沿用参考仓路径：Task 12 路由表）。M2–M4、M10 不涉及 P1b 代码（settings 表已在 P1a，设置接口属 P4）。
- **P0 遗留：** 数据目录 0700（Go：Task 12；Rust：Task 3）· 开发源只在 debug（Task 3 + Task 12 默认值）· 优雅退出（Task 3 + Task 12）· Bearer 前缀（Task 3）· Max-Age（Task 3）· ReadHeaderTimeout 与 token 长度上限（Task 12）· 缺失测试：`Origin: null`、错 token + 白名单 Origin、带 token 的外来 Origin 预检（Task 3）、WS 外来 Origin 403（Task 12）、二进制级缺参数/短 token/`--origins`/退出码 1 + stderr（Task 12）。
- **类型一致性：** `assets.LocalStorage.Path` 被 `mediaFileStore` 使用；`library.NewOrphanSweeper` 的 `objectDeleter` 由 `*assets.LocalStorage` 满足；`users.SQLiteRepository` 满足 `storageUserRepo`、`userStorageAdjuster`、`meUserRepo`；`canvasowner.Local` 满足 `canvasOwner`；`NewProjectHandler` 9 个参数、`NewProjectCleaner` 6 个参数在 Task 9、12 一致；`NewAdminGCHandler(*library.OrphanSweeper)` 在 Task 11、12 一致。
- **占位符扫描：** 无 TBD / “类似 Task N” / 无代码的步骤；原样复制的文件给出 `cp` 命令，移植的文件逐条列出改动。
