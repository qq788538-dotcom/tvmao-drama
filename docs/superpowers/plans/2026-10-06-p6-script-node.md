# P6 脚本节点“剧本生成分镜”（内置 Python + 命令白名单）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让桌面端脚本节点“剧本生成分镜”在 macOS 与 Windows 上都能跑通：sidecar 用内置的独立 Python 执行 Skill 自带校验脚本，命令经白名单解析后直接 exec（不经过 bash），只能写本次任务工作区。

**Architecture:** 从参考仓移植 `skillruntime`（Eino DeepAgent + Skill 中间件）和 `canvasscript`（任务表 + worker），把参考仓 Linux 专用的 `SandboxedShell`（srt/bubblewrap）换成新写的 `RestrictedShell`：自己解析一种受限命令语法，只接受 `python3 <Skill 自带脚本> [参数…]` 和内置命令 `embed-text`，用 python-build-standalone 解释器直接 exec。Skill 目录用 `go:embed` 编进 sidecar、启动时释放到数据目录；Python 作为 Tauri 资源随 dmg / zip 分发，sidecar 按自身可执行文件位置找到它。

**Tech Stack:** Go 1.26（module `github.com/james/huabu_studio/apps/api`）、`github.com/cloudwego/eino` v0.8.13、`eino-ext/components/model/openai` v0.1.13、`modernc.org/sqlite` v1.60.1、python-build-standalone `20261003`（CPython 3.12.15，`install_only_stripped`）、Tauri v2 bundle resources、bash（仅构建脚本）。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（X6、F2、X3、A1、D6、D8、E4、E5）；事实依据 `docs/superpowers/plans/p1-research.md` §3、§6。

**参考仓：** `/Users/james/github/huabu_studio`（只读），下文 `REF=/Users/james/github/huabu_studio/apps/api/internal`，`REF_SKILLS=/Users/james/github/huabu_studio/skills`。

## 关键决策（实现前请复核）

1. **Python 发行版**：astral-sh/python-build-standalone，release `20261003`，CPython `3.12.15`，`install_only_stripped` 变体，每个目标一份：
   | target triple | 文件 | 压缩包大小 | sha256 |
   |---|---|---|---|
   | aarch64-apple-darwin | `cpython-3.12.15+20261003-aarch64-apple-darwin-install_only_stripped.tar.gz` | 25.0 MB | `ad8d0c637c0a36b967b310e2c07254f4d2ca8cabaa7699e55ed6290aceb481a2` |
   | x86_64-apple-darwin | `cpython-3.12.15+20261003-x86_64-apple-darwin-install_only_stripped.tar.gz` | 24.7 MB | `562c30864ece2cb1d3e0ad66a1acd498611a47e5a10ce81b99158bef1ccbd355` |
   | x86_64-pc-windows-msvc | `cpython-3.12.15+20261003-x86_64-pc-windows-msvc-install_only_stripped.tar.gz` | 22.0 MB | `6fba7f2ae506facf41d457ea8293c7497910a675c69a4e954875169410a50402` |

   理由：可重定位（不依赖 PYTHONHOME、不需要安装），三个目标同一个版本，能在 macOS 上交叉准备 Windows 版本；校验脚本只用标准库（argparse/json/re/math/decimal/pathlib/typing，已逐个核对 8 个 Skill 的 `scripts/*.py`），所以不装 pip、不装第三方包，并删掉 test/idlelib/tkinter/ensurepip 等目录。sha256 取自 GitHub release API 的 `digest` 字段（2026-10-06 查询）。
2. **安装位置**：构建时解到 `src-tauri/resources/python/`（目录本身入库，里面只提交 `README.md`，其余 gitignore）。macOS 由 Tauri `bundle.resources` 拷到 `喵剧AI.app/Contents/Resources/python/`；Windows zip（`--no-bundle`，Tauri 不拷资源）由 `package-windows.sh` 拷到 `miaoju-windows-x64-<ver>/python/`，与 `sidecar.exe` 同级。解释器：macOS `python/bin/python3`（构建时把符号链接换成实体文件），Windows `python/python.exe`。
3. **sidecar 找 Python 的顺序**：`--python-home` 参数 → 环境变量 `MIAOJU_PYTHON_HOME` → `<sidecar 所在目录>/../Resources/python`（macOS .app）→ `<sidecar 所在目录>/python`（Windows zip、`cargo tauri dev`）。都找不到时 sidecar 照常启动、打印警告，脚本任务失败并显示“内置 Python 运行时缺失”。**不改 Tauri 壳（`src-tauri/src/lib.rs`）**。
4. **Skill 目录打包**：8 个 Skill（`seed/settings.jsonl` 中 `canvas_script_config.styles[].skillName`：real-person-short-drama-v1/v2/v3、real-person-narration-storyboard-v1、3d-animation-style-v1/v2、novel-real-person-short-drama-v1、novel-3d-animation-style-v1；去掉 `tests/`、`agents/` 后约 1 MB）复制到 `server/internal/skillbundle/skills/`，`//go:embed all:skills` 编进 sidecar，每次启动清空并释放到 `<data-dir>/runtime/skills/`。选 go:embed 而不是 Tauri 资源：两个平台同一机制、Go 测试直接可用、版本永远与 sidecar 一致。
5. **Windows 没有 bash 的处理**：生产代码里不再有任何 shell。`RestrictedShell` 自己做分词（单引号、双引号、空白），只认两种命令，用 `exec.Command(python, "-E", "-s", "-B", "-u", "-X", "utf8", <脚本绝对路径>, 参数…)` 直接起进程；Windows 上加 `CREATE_NO_WINDOW`，避免每次校验弹黑框。
6. **白名单规则**：
   - 第一个词必须是 `python`/`python3`/`python3.N`/`py`（可带 `.exe`），一律映射到内置解释器；或者是内置命令 `embed-text`。其他一律拒绝。
   - 脚本参数不能以 `-` 开头（挡住 `-c`、`-m`），必须是 `.py` 相对路径；**在原始 Skill 目录（只读、可信）里解析并执行原件**，不执行工作区里的副本，所以模型改写或新建的 `.py` 不会被执行。路径不能有 `..` 段、不能是绝对路径、盘符、UNC、带冒号、Windows 设备名，经符号链接解析后必须仍在 Skill 目录内。
   - 后续参数：数字原样；`-x` / `--flag` 原样；`--flag=值` 拆开校验值；其余一律当作工作区文件路径，按上面规则校验，且已存在的部分经 `EvalSymlinks` 后必须在工作区内。
   - 不加引号出现 `| & ; < > $ \` ( ) * ? [ ] { } ~ ! #` 或换行即拒绝；唯一容忍命令末尾的 `2>&1`（本来就合并 stdout/stderr）。双引号里出现 `$`、反引号也拒绝。
   - 子进程环境从零构造：`PATH=<python bin 目录>`、`HOME`/`USERPROFILE`=工作区、`TMPDIR`/`TEMP`/`TMP`=工作区 `.tmp`、`SKILL_DIR`/`WORK_DIR`=工作区、`LANG`/`LC_ALL=C.UTF-8`，Windows 另加 `SYSTEMROOT`。`-E` 忽略 `PYTHON*` 变量，`-s` 不加载用户 site，`-B` 不写 `.pyc`（避免写进 .app 破坏签名），`-X utf8` 保证中文报错在 Windows 上不乱码。
   - 单条命令 30 秒超时（返回码 124），输出上限 1 MiB（`Truncated=true`），拒绝时返回码 126 并附用法说明。拒绝、超时都作为普通工具结果返回给模型，不中断 Run。
   - “只能写任务工作区”：命令只能运行经审计的只读校验脚本（Task 2 有测试断言脚本里没有 `open(`、`write_text`、`subprocess`、`socket` 等）；唯一会写文件的是 `embed-text`，它复用 `WorkspaceBackend.Write`（拒绝越界与符号链接，原子写入）。不做系统级沙箱（macOS sandbox-exec / Windows 无等价物），见“待定问题”。
7. **`embed-text <JSON 文件> <字段名> <文本文件>`**：两个小说风格的 SKILL.md 要求“用脚本把 `source.txt` 原样注入 `sourceText`（例如用 python 读文件后写入 JSON），不得手抄”。参考仓靠 `python3 -c` 或临时脚本完成，白名单下不允许，所以提供这个内置命令：把文本文件按原样写进 JSON 顶层字段，**保持已有字段顺序**（`validate_storyboard.py` 会检查顶层字段顺序）。提示语写进 agent 指令和拒绝消息。
8. **替换方式**：`skillruntime.ShellFactory` 签名从 `func(root string)` 改为 `func(workspace, skillDir string)`，`Runtime.Run` 多传原始 Skill 目录。删除 `SandboxedShell`、`SandboxConfig`；`TrustedShell` 移到 `_test.go`，只供测试注入，生产无法装配。
9. **计费**：spec X3 删除积分。`canvasscript` 去掉 billing 依赖；候选模型只在 `models.GetByModelID` 查不到时剔除，**不再因估价 `<= 0` 剔除**（参考仓 service.go:229、:290 的闸门在零价下会把所有候选剔光）。响应里的 `credits` 恒为 0，仅为前端兼容保留；`frozen_cents` 写 0、`actor_user_id` 写 NULL。
10. **worker 启动**：不用 lifecycle leader。启动时 `RecoverOnStartup` 把上个进程遗留的 `running` 全部视为失联（单进程，不必等 120 秒心跳超时）：未用尽重试的回到 `pending`，用尽的判失败；然后 `go RunWorker(ctx)`（2 秒一轮抢占）。去掉心跳 goroutine 和周期回收；`owner_instance` 写本进程随机 id，`heartbeat_at` 只在抢占时写一次。
11. **迁移文件**：`server/internal/db/migrations/0060_canvas_script_tasks.sql`。用 0060 是为了避开其他子计划从 0002 起顺序编号；最终合并时如统一重排，只改文件名。

## Global Constraints

- 平台：macOS ≥ 14（aarch64、x86_64）与 Windows x64；不做 Linux（D6：“macOS + Windows（Linux WebKitGTK 对 WebCodecs 支持弱）”；D8：“最低 macOS 14”）。
- 分发：“macOS 直接发安装包（dmg），Windows 直接发 zip 包（解压即用）；不做自动更新”（E4）；“macOS 和 Windows 都不做开发者签名和公证。macOS 只保留构建时自动带的临时签名（ad-hoc）”（E5）。
- X6 原文：“第一期就做：内置独立 Python 运行时 + 命令白名单（只允许执行 Skill 目录内脚本、只能写任务工作区）；需解决 Windows 没有 bash 的问题”。
- X3 原文：“积分、估价、价格说明全部删除”；A1：“Eino 运行时原样复制，计费空实现”。
- Go module 路径 `github.com/james/huabu_studio/apps/api`，包路径与参考仓一致（`skillruntime`、`canvasscript`、`http`），新包 `skillbundle`。
- 生产代码不得调用 bash / sh / cmd.exe，不得把 `os.Environ()` 传给子进程。
- SQLite（p1-research §6）：时间列 `DATETIME`、SQL 里用 `CURRENT_TIMESTAMP`、不得直接绑定 `time.Time`；新表加 `updated_at` 触发器；会被前端长期持有的 id 用 `INTEGER PRIMARY KEY AUTOINCREMENT`；`RowsAffected` 是匹配行数；不得修改 `0001_init.sql`。
- 代码注释用中文；从参考仓复制的注释若提到 srt / MySQL / 积分且已不成立，就地改写。
- 本计划不改 `src-tauri/src/**`、`web/**`、`seed/**`；后台“脚本设置”配置接口（`/admin/canvas-script-config`）归 P4，P4 直接调用 `canvasscript.Service.LoadConfig` / `SaveConfig`。

## Review Focus

1. **模型照 bash 习惯发命令**（`python3 -c …`、`cd scripts && python3 …`、`| head`、`> out.txt`、未替换的 `<DURATION_SEC>`）→ 期望返回“命令被拒绝”+ 可用写法、返回码 126，Run 继续，模型能自行改正。（Task 3 `TestParseCommandRejects`、Task 5 `TestRestrictedShellRejectsDisallowedCommandsWithExit126`）
2. **模型在工作区改写或新建 `.py` 再执行** → 期望只执行原始 Skill 脚本，新建的脚本被拒绝。（Task 5 `TestRestrictedShellRunsOriginalNotWorkspaceCopy`）
3. **小说风格要求把 5000 字中文原文逐字注入 JSON**（含引号、反斜杠、换行；`validate_storyboard.py` 校验顶层字段顺序）→ 期望 `embed-text` 后内容逐字一致、字段顺序不变，真实校验器不再报“必须从 source.txt 原样注入”。（Task 5 `TestEmbedTextCopiesSourceVerbatimAndKeepsKeyOrder`、Task 8 `TestBundledPythonEmbedTextSatisfiesAdaptedValidator`）
4. **数据目录路径带空格和中文**（macOS `Application Support`、Windows 中文用户名）→ 期望脚本照常运行、参数不被拆开。（Task 8 两个测试都在 `Application Support/张三` 下运行）
5. **任务执行中应用被关闭** → 期望重启后任务回到排队或判失败，不会永远停在“执行中”。（Task 6 `TestRecoverOnStartup`、Task 7 `TestWireCanvasScriptExtractsSkillsAndRecoversRunningTasks`）

## 并发执行安排

速度优先：每个任务在独立 worktree 执行，冲突在最终合并时统一解决。

| 批次 | 任务 | 依赖 | 说明 |
|---|---|---|---|
| 第一批（并发） | **Task 1** Python 获取与打包脚本 | 无 | 只碰构建脚本与 Tauri 配置 |
| | **Task 2** Skill 打包（skillbundle） | 无 | 新包 |
| | **Task 3** 命令解析与参数规则 | 无 | `skillruntime` 包内只新增 `command.go`，可单独编译 |
| | **Task 4** skillruntime 移植 | 无 | 改 go.mod |
| 第二批（并发） | **Task 5** RestrictedShell + 找 Python + embed-text | Task 3、Task 4 | |
| | **Task 6** canvas_script_tasks 迁移 + canvasscript 移植 | Task 4（只需 `skillruntime.Snapshot/RunRequest/RunResult/ModelConfig` 类型；Task 4 第 2 步提交后即可开工） | |
| 第三批（并发） | **Task 7** HTTP 接口 + sidecar 装配 | Task 2、Task 5、Task 6（handler 部分只依赖 Task 6） | |
| | **Task 8** 内置 Python 集成测试 + 打包验收 | Task 1、Task 2、Task 5 | |

**共享文件归属**（只有归属任务可以改；与其他子计划的冲突在最终合并时解决）：

| 文件 | 归属 | 可能冲突的其他计划 |
|---|---|---|
| `server/go.mod`、`server/go.sum` | Task 4 | P5（同样引入 eino，版本一致即可合并）、P1b/P1c |
| `src-tauri/tauri.conf.json`（`bundle.resources`） | Task 1 | P8 / 其他打包改动 |
| `scripts/build-sidecar.sh`、`scripts/package-windows.sh`、`.gitignore` | Task 1 | P8 |
| `server/internal/db/migrations/0060_canvas_script_tasks.sql` | Task 6 | 编号若与他人冲突，合并时改名 |
| `server/internal/http/router.go` | Task 7 | P1b/P1c/P1d/P4/P5（都加路由） |
| `server/cmd/sidecar/main.go` | Task 7 | P1b/P1c（都加 db.Open 与装配）；合并时只保留一份 `db.Open`/`Migrate`/`ctx` |
| `server/internal/skillruntime/runner.go` 的 agent 指令文案 | Task 4 | 无（Task 5 只实现、不改文案） |

**对其他计划的接口假设：**
- P2 把 `canvas_script_config` 和模型 `canvas-gpt-5.5` 预置进 SQLite；本计划测试自行写入配置，不依赖 P2。
- 上游 Key：本计划在装配处用 `settings.upstream_api_key` 实现 `Resolve(userID)`（p1-research §3）；若 P1c 已有 `UpstreamKeyResolver`，合并时换成它。
- 用户 id：handler 用常量 `canvasScriptUserID = 1`（P1a 迁移预置的本地用户）；P1b 的本地身份中间件合并后可改用 `userIDFromContext`，行为不变。
- 数据库文件名 `<data-dir>/data.db`。

---

## 文件结构

```
scripts/
  fetch-python.sh                 下载、校验、裁剪 python-build-standalone 到 src-tauri/resources/python/   (Task 1)
  sync-skills.sh                  从参考仓复制 8 个 Skill 到 server/internal/skillbundle/skills/          (Task 2)
  build-sidecar.sh                末尾调用 fetch-python.sh                                                 (Task 1，修改)
  package-windows.sh              把 python/ 拷进 zip                                                       (Task 1，修改)
src-tauri/
  tauri.conf.json                 bundle.resources 加 python/                                              (Task 1，修改)
  resources/python/README.md      占位，保证 tauri-build 在未下载 Python 时也能编译                         (Task 1)
server/internal/
  skillbundle/
    bundle.go                     go:embed + Names + Extract                                              (Task 2)
    bundle_test.go                                                                                         (Task 2)
    skills/<8 个 Skill>/…         sync-skills.sh 生成，入库                                                (Task 2)
  skillruntime/
    command.go                    分词、命令语法、路径静态规则、参数映射                                    (Task 3)
    command_test.go                                                                                        (Task 3)
    catalog.go workspace.go cleaner.go model_retry.go model_trace.go artifact_output.go  原样复制          (Task 4)
    runner.go                     复制后改 ShellFactory 签名与指令文案                                     (Task 4)
    shell_output.go               cappedBuffer、输出上限、默认超时                                         (Task 4)
    trusted_shell_test.go         仅测试用的 TrustedShell                                                  (Task 4)
    *_test.go                     参考仓测试复制（shell_test.go 删掉 srt 部分）                            (Task 4)
    restricted_shell.go           RestrictedShell、RestrictedShellFactory                                  (Task 5)
    embed_text.go                 embed-text 与保序 JSON                                                   (Task 5)
    python_runtime.go             LocatePython、PythonInterpreter                                          (Task 5)
    exec_windows.go exec_other.go 子进程平台差异（CREATE_NO_WINDOW、SYSTEMROOT）                           (Task 5)
    restricted_shell_test.go python_runtime_test.go                                                        (Task 5)
    bundled_python_test.go        用内置 Python 跑真实校验脚本                                             (Task 8)
  db/migrations/0060_canvas_script_tasks.sql                                                               (Task 6)
  canvasscript/
    service.go                    移植（去计费、去 lifecycle、SQLite 方言）                                (Task 6)
    service_test.go service_db_test.go                                                                     (Task 6)
  http/
    canvas_script_handlers.go     用户侧三个接口                                                           (Task 7)
    canvas_script_handlers_test.go                                                                         (Task 7)
    router.go                     Deps.CanvasScript 与路由                                                 (Task 7，修改)
server/cmd/sidecar/
  canvas_script.go                wireCanvasScript 装配                                                    (Task 7)
  canvas_script_test.go                                                                                    (Task 7)
  main.go                         --python-home、db、ctx、调用 wireCanvasScript                            (Task 7，修改)
```

---

### Task 1: Python 运行时获取、裁剪与打包

**Files:**
- Create: `scripts/fetch-python.sh`
- Create: `src-tauri/resources/python/README.md`
- Modify: `scripts/build-sidecar.sh`（末尾加一行）
- Modify: `scripts/package-windows.sh`（拷贝 python/）
- Modify: `src-tauri/tauri.conf.json`（`bundle.resources`）
- Modify: `.gitignore`

**Interfaces:**
- Consumes: 无
- Produces（Task 5、Task 8 依赖的目录约定）：
  - 构建后 `src-tauri/resources/python/` 是一个完整的 Python home：macOS 解释器 `bin/python3`（实体文件，非符号链接），Windows 解释器 `python.exe`；目录里没有任何符号链接。
  - `src-tauri/resources/python/.stamp` 内容为压缩包文件名，用于跳过重复准备。
  - macOS .app 内路径 `Contents/Resources/python/`；Windows zip 内路径 `miaoju-windows-x64-<ver>/python/`。

- [ ] **Step 1: 写占位 README 与 gitignore**

`src-tauri/resources/python/README.md`：

```markdown
# 内置 Python 运行时

本目录由 `scripts/fetch-python.sh <target-triple>` 生成（python-build-standalone，CPython 3.12.15），
供脚本节点执行 Skill 校验脚本。除本文件外全部内容都不入库。
本文件必须保留：tauri.conf.json 的 bundle.resources 引用了本目录，目录不存在时 cargo 编译会失败。
```

在 `.gitignore` 末尾追加：

```gitignore

# 内置 Python（scripts/fetch-python.sh 生成）
src-tauri/resources/python/*
!src-tauri/resources/python/README.md
```

- [ ] **Step 2: 写 fetch-python.sh**

`scripts/fetch-python.sh`：

```bash
#!/usr/bin/env bash
# 下载 python-build-standalone，校验 sha256，裁剪后放到 src-tauri/resources/python/。
# Tauri 把该目录作为资源打进 .app（Contents/Resources/python）；Windows zip 由 package-windows.sh 拷贝。
# 用法：scripts/fetch-python.sh [target-triple]，缺省为本机 triple。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:-$(rustc -vV | sed -n 's/^host: //p')}"
PBS_RELEASE=20261003
PY_VERSION=3.12.15
case "$TARGET" in
  aarch64-apple-darwin)   SHA=ad8d0c637c0a36b967b310e2c07254f4d2ca8cabaa7699e55ed6290aceb481a2 ;;
  x86_64-apple-darwin)    SHA=562c30864ece2cb1d3e0ad66a1acd498611a47e5a10ce81b99158bef1ccbd355 ;;
  x86_64-pc-windows-msvc) SHA=6fba7f2ae506facf41d457ea8293c7497910a675c69a4e954875169410a50402 ;;
  *) echo "不支持的目标平台: $TARGET" >&2; exit 1 ;;
esac

NAME="cpython-${PY_VERSION}+${PBS_RELEASE}-${TARGET}-install_only_stripped.tar.gz"
URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE}/${NAME//+/%2B}"
DEST=src-tauri/resources/python
STAMP="$DEST/.stamp"

if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$NAME" ]; then
  echo "内置 Python 已是 $NAME，跳过"
  exit 0
fi

CACHE=out/cache/python
mkdir -p "$CACHE"
if [ ! -f "$CACHE/$NAME" ]; then
  curl -fL --retry 3 -o "$CACHE/$NAME.part" "$URL"
  mv "$CACHE/$NAME.part" "$CACHE/$NAME"
fi
GOT="$(shasum -a 256 "$CACHE/$NAME" | awk '{print $1}')"
if [ "$GOT" != "$SHA" ]; then
  echo "sha256 不匹配：期望 $SHA，实际 $GOT（已删除缓存，请重试）" >&2
  rm -f "$CACHE/$NAME"
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
tar -xzf "$CACHE/$NAME" -C "$TMP"
P="$TMP/python"

# 裁剪：Skill 校验脚本只用标准库（argparse/json/re/math/decimal/pathlib/typing），不需要 pip、GUI、测试集。
case "$TARGET" in
  *windows*)
    LIB="$P/Lib"
    rm -rf "$P/include" "$P/libs" "$P/Scripts" "$P/tcl" \
           "$P/DLLs/_tkinter.pyd" "$P/DLLs/tcl86t.dll" "$P/DLLs/tk86t.dll"
    ;;
  *)
    LIB="$P/lib/python3.12"
    rm -rf "$P/include" "$P/share" "$P"/lib/tcl8* "$P"/lib/tk8* "$P"/lib/itcl* "$P"/lib/thread* \
           "$P/lib/pkgconfig" "$P"/bin/idle3* "$P"/bin/pydoc3* "$P"/bin/pip* "$P"/bin/2to3*
    # 符号链接换成实体文件：bin/python3 -> python3.12。资源拷贝和 zip 不保证保留符号链接。
    REAL="$(readlink "$P/bin/python3")"
    rm -f "$P/bin/python3"
    mv "$P/bin/$REAL" "$P/bin/python3"
    find "$P/bin" -type l -delete
    ;;
esac
rm -rf "$LIB"/test "$LIB"/idlelib "$LIB"/tkinter "$LIB"/turtledemo "$LIB"/ensurepip \
       "$LIB"/lib2to3 "$LIB"/pydoc_data "$LIB"/site-packages/pip*

# macOS：每个 Mach-O 补一个 ad-hoc 签名（E5）。arm64 上未签名的可执行文件根本无法运行，
# 外层 .app 的签名也会因内部有未签名代码而失败。
case "$TARGET" in
  *apple-darwin)
    find "$P" -type f \( -perm -u+x -o -name '*.so' -o -name '*.dylib' \) -print0 |
      while IFS= read -r -d '' f; do
        if file -b "$f" | grep -q 'Mach-O'; then codesign --force --sign - "$f" >/dev/null; fi
      done
    ;;
esac

# 本机能跑的目标顺手冒烟：确认校验脚本用到的标准库都在。
HOST="$(rustc -vV | sed -n 's/^host: //p')"
if [ "$TARGET" = "$HOST" ]; then
  "$P/bin/python3" -E -s -B -X utf8 -c 'import argparse, json, re, math, decimal, pathlib, typing; print("内置 Python 冒烟通过")'
fi

mkdir -p "$DEST"
find "$DEST" -mindepth 1 -maxdepth 1 ! -name README.md -exec rm -rf {} +
cp -RL "$P"/. "$DEST"/
echo "$NAME" > "$STAMP"
echo "已准备内置 Python：$DEST（$(du -sh "$DEST" | cut -f1)）"
```

```bash
chmod +x scripts/fetch-python.sh
```

- [ ] **Step 3: 运行并验证（本机 macOS arm64）**

Run: `scripts/fetch-python.sh aarch64-apple-darwin`
Expected: 输出“内置 Python 冒烟通过”和“已准备内置 Python：src-tauri/resources/python（约 40M）”。

Run: `find src-tauri/resources/python -type l | wc -l; src-tauri/resources/python/bin/python3 -E -s -B -X utf8 -c 'print("中文")'`
Expected: `0` 和 `中文`。

Run: `scripts/fetch-python.sh aarch64-apple-darwin`
Expected: `内置 Python 已是 cpython-3.12.15+20261003-aarch64-apple-darwin-install_only_stripped.tar.gz，跳过`

Run: `scripts/fetch-python.sh x86_64-pc-windows-msvc && ls src-tauri/resources/python/python.exe && scripts/fetch-python.sh aarch64-apple-darwin`
Expected: Windows 版本能准备出 `python.exe`（不冒烟），随后切回 macOS 版本。

- [ ] **Step 4: 接入构建脚本与 Tauri 资源**

`scripts/build-sidecar.sh` 末尾（`echo "已生成 …"` 之后）追加：

```bash
# 内置 Python 与 sidecar 同目标（脚本节点 X6）。
scripts/fetch-python.sh "$TARGET"
```

`src-tauri/tauri.conf.json` 的 `bundle` 里、`externalBin` 之后加一行：

```json
    "resources": { "resources/python/": "python/" },
```

`scripts/package-windows.sh` 中 `cp "$REL/miaoju.exe" "$REL/sidecar.exe" "$STAGE/"` 之后加：

```bash
# 内置 Python：sidecar 在自身所在目录的 python/ 下找解释器（--no-bundle 不会拷 Tauri 资源）。
[ -f src-tauri/resources/python/python.exe ] || { echo "内置 Python 不是 Windows 版本，先运行 scripts/fetch-python.sh $TARGET" >&2; exit 1; }
cp -R src-tauri/resources/python "$STAGE/python"
rm -f "$STAGE/python/README.md" "$STAGE/python/.stamp"
```

- [ ] **Step 5: 验证 dmg 内容**

Run: `pnpm build && ls "src-tauri/target/release/bundle/macos/喵剧AI.app/Contents/Resources/python/bin/python3" && "src-tauri/target/release/bundle/macos/喵剧AI.app/Contents/Resources/python/bin/python3" -E -c 'import json; print("ok")' && codesign --verify --deep --strict "src-tauri/target/release/bundle/macos/喵剧AI.app" && echo signed-ok`
Expected: 路径存在、打印 `ok`、`signed-ok`。

若 `Resources/python/` 下没有 `bin/`（Tauri 版本不支持目录映射），把 resources 改为 `{ "resources/python/**/*": "python/" }` 后重跑本步；若子目录被拍平，则改用 `"resources": ["resources/python/"]` 并把 Task 5 的 macOS 候选路径改为 `../Resources/resources/python`（同步修改 `python_runtime.go` 与其测试）。

Run: `cd src-tauri && cargo test`
Expected: PASS（占位 README 让未下载 Python 的 worktree 也能编译）。

- [ ] **Step 6: Commit**

```bash
git add scripts/fetch-python.sh scripts/build-sidecar.sh scripts/package-windows.sh src-tauri/tauri.conf.json src-tauri/resources/python/README.md .gitignore
git commit -m "build: 内置 python-build-standalone 3.12.15 运行时（mac dmg 资源 / Windows zip）"
```

---

### Task 2: Skill 目录打包（skillbundle）

**Files:**
- Create: `scripts/sync-skills.sh`
- Create: `server/internal/skillbundle/bundle.go`
- Create: `server/internal/skillbundle/skills/**`（脚本生成，入库）
- Test: `server/internal/skillbundle/bundle_test.go`

**Interfaces:**
- Consumes: 无
- Produces：
  - `skillbundle.Names() ([]string, error)`：内置 Skill 目录名，按字典序。
  - `skillbundle.Extract(dst string) (string, error)`：清空 `dst` 后释放整棵 Skill 树，返回 `dst` 的绝对路径；每个 Skill 位于 `<dst>/<name>/SKILL.md`。

- [ ] **Step 1: 写同步脚本并运行**

`scripts/sync-skills.sh`：

```bash
#!/usr/bin/env bash
# 把脚本节点用到的 Skill 从参考仓复制到 server/internal/skillbundle/skills（go:embed 编进 sidecar）。
# 名单与 seed/settings.jsonl 中 canvas_script_config.styles[].skillName 一致；风格配置变了就重跑并提交。
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${HUABU_SKILLS_DIR:-/Users/james/github/huabu_studio/skills}"
DEST=server/internal/skillbundle/skills
SKILLS=(
  real-person-short-drama-v3
  real-person-narration-storyboard-v1
  real-person-short-drama-v2
  real-person-short-drama-v1
  3d-animation-style-v2
  3d-animation-style-v1
  novel-real-person-short-drama-v1
  novel-3d-animation-style-v1
)

rm -rf "$DEST"
mkdir -p "$DEST"
for s in "${SKILLS[@]}"; do
  [ -f "$SRC/$s/SKILL.md" ] || { echo "缺少 $SRC/$s/SKILL.md" >&2; exit 1; }
  rsync -a --exclude 'tests/' --exclude 'agents/' --exclude '.DS_Store' \
        --exclude '__pycache__/' --exclude '*.pyc' "$SRC/$s/" "$DEST/$s/"
done
echo "已同步 ${#SKILLS[@]} 个 Skill 到 $DEST"
```

Run: `chmod +x scripts/sync-skills.sh && scripts/sync-skills.sh && find server/internal/skillbundle/skills -name '*.py' | sort`
Expected: “已同步 8 个 Skill”，列出 8 个 `scripts/__init__.py`、8 个 `validate_storyboard.py`、`real-person-narration-storyboard-v1/scripts/validate_narration_plan.py`、两个小说 Skill 的 `validate_adapted_script.py`；没有 `tests/`。

- [ ] **Step 2: 写失败的测试**

`server/internal/skillbundle/bundle_test.go`：

```go
package skillbundle

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestExtractWritesEverySkill(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "Application Support", "skills")
	got, err := Extract(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("Extract 应返回绝对路径，got %q", got)
	}
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 8 {
		t.Fatalf("应内置 8 个 Skill，got %v", names)
	}
	for _, name := range names {
		for _, rel := range []string{"SKILL.md", "scripts/validate_storyboard.py", "scripts/__init__.py"} {
			if _, err := os.Stat(filepath.Join(got, name, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s/%s 未释放: %v", name, rel, err)
			}
		}
		if _, err := os.Stat(filepath.Join(got, name, "tests")); err == nil {
			t.Errorf("%s/tests 不应打包", name)
		}
	}
}

func TestExtractReplacesStaleTree(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(dst, "old-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old-skill", "SKILL.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old-skill")); !os.IsNotExist(err) {
		t.Fatalf("旧版本残留的 Skill 应被清掉: %v", err)
	}
}

// 白名单只允许运行这些脚本，“只能写任务工作区”靠它们本身不写文件、不联网、不起子进程来保证。
// 同步了新版本 Skill 后这里失败，说明要重新评估白名单。
func TestBundledScriptsHaveNoSideEffects(t *testing.T) {
	forbidden := regexp.MustCompile(`subprocess|os\.system|os\.popen|socket|urllib|http\.client|requests|shutil|ctypes|` +
		`write_text\(|write_bytes\(|\bopen\(|os\.remove|os\.unlink|os\.rename|os\.replace|os\.environ|\beval\(|\bexec\(|__import__`)
	count := 0
	err := fs.WalkDir(bundled, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".py") {
			return err
		}
		count++
		data, err := bundled.ReadFile(p)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for line := 1; sc.Scan(); line++ {
			if m := forbidden.FindString(sc.Text()); m != "" {
				t.Errorf("%s:%d 含有 %q", p, line, m)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 19 {
		t.Fatalf("应扫描到至少 19 个 .py（8 个 __init__ + 11 个校验脚本），got %d", count)
	}
}

func TestSeedCanvasScriptStylesAreBundled(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "seed", "settings.jsonl"))
	if os.IsNotExist(err) {
		t.Skip("seed/settings.jsonl 不在预期位置")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	found := false
	for sc.Scan() {
		var row struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Key != "canvas_script_config" {
			continue
		}
		found = true
		var cfg struct {
			Styles []struct {
				SkillName string `json:"skillName"`
			} `json:"styles"`
		}
		if err := json.Unmarshal([]byte(row.Value), &cfg); err != nil {
			t.Fatal(err)
		}
		for _, s := range cfg.Styles {
			if !have[s.SkillName] {
				t.Errorf("风格引用的 Skill %q 没有内置，运行 scripts/sync-skills.sh", s.SkillName)
			}
		}
	}
	if !found {
		t.Fatal("seed 里没有 canvas_script_config")
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd server && go test ./internal/skillbundle/`
Expected: FAIL（`undefined: Extract` / `bundled` / `root`）

- [ ] **Step 4: 实现**

`server/internal/skillbundle/bundle.go`：

```go
// Package skillbundle 把脚本节点用到的 Skill 目录编进 sidecar，启动时释放到数据目录，
// 供 skillruntime.Catalog 按普通目录读取。Skill 内容来自参考仓 skills/，用 scripts/sync-skills.sh 同步。
package skillbundle

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// all: 前缀不能省：否则 go:embed 会跳过 scripts/__init__.py 这类以 _ 开头的文件。
//
//go:embed all:skills
var bundled embed.FS

const root = "skills"

// Names 返回内置 Skill 的目录名（字典序）。
func Names() ([]string, error) {
	entries, err := fs.ReadDir(bundled, root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Extract 清空 dst 后把整棵 Skill 树释放进去，返回 dst 的绝对路径。
// 每次启动都重新释放：版本永远与 sidecar 一致，用户误改的文件也会被还原。
func Extract(dst string) (string, error) {
	if strings.TrimSpace(dst) == "" {
		return "", errors.New("内置 Skill 释放目录为空")
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(abs); err != nil {
		return "", fmt.Errorf("清理旧的内置 Skill 目录: %w", err)
	}
	err = fs.WalkDir(bundled, root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		target := filepath.Join(abs, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundled.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("释放内置 Skill: %w", err)
	}
	return abs, nil
}
```

- [ ] **Step 5: 运行确认通过**

Run: `cd server && go test ./internal/skillbundle/ -v`
Expected: 4 个测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add scripts/sync-skills.sh server/internal/skillbundle
git commit -m "feat(server): 内置脚本节点 Skill 目录（go:embed，启动释放）"
```

---

### Task 3: 受限命令解析与参数规则

**Files:**
- Create: `server/internal/skillruntime/command.go`
- Test: `server/internal/skillruntime/command_test.go`

**Interfaces:**
- Consumes: 无（只用标准库，可在 skillruntime 包其他文件尚未存在时单独编译）
- Produces（Task 5 依赖）：
  - `type CommandKind int`；常量 `CommandPython`、`CommandEmbedText`
  - `type ParsedCommand struct { Kind CommandKind; Script string; Args []string }`
  - `func ParseCommand(line string) (ParsedCommand, error)`；拒绝时 `errors.Is(err, ErrCommandRejected)`，`err.Error()` 是给模型看的中文原因
  - `var ErrCommandRejected`、`func rejectf(format string, args ...any) error`
  - `func checkRelPath(p string) error`、`func toSlash(p string) string`
  - `func mapScriptArgs(args []string, resolvePath func(string) (string, error)) ([]string, error)`
  - 常量 `embedTextCommand = "embed-text"`

- [ ] **Step 1: 写失败的测试**

`server/internal/skillruntime/command_test.go`：

```go
package skillruntime

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommandAccepts(t *testing.T) {
	cases := []struct {
		line string
		want ParsedCommand
	}{
		{"python3 scripts/validate_storyboard.py candidate.json --duration-sec 10",
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec", "10"}}},
		{`python scripts\validate_storyboard.py candidate.json`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json"}}},
		{`python3 "scripts/validate_storyboard.py" 'candidate.json' --duration-sec=10`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec=10"}}},
		{"python3 scripts/validate_storyboard.py candidate.json --duration-sec 10 2>&1",
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec", "10"}}},
		{"  python3.12   ./scripts/a.py  ",
			ParsedCommand{Kind: CommandPython, Script: "./scripts/a.py", Args: []string{}}},
		{`python3 scripts/a.py "带 空格.json" "say \"hi\""`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/a.py", Args: []string{"带 空格.json", `say "hi"`}}},
		{"embed-text adapted_script.json sourceText source.txt",
			ParsedCommand{Kind: CommandEmbedText, Args: []string{"adapted_script.json", "sourceText", "source.txt"}}},
	}
	for _, c := range cases {
		got, err := ParseCommand(c.line)
		if err != nil {
			t.Errorf("%q: 不应拒绝: %v", c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %#v want %#v", c.line, got, c.want)
		}
	}
}

func TestParseCommandRejects(t *testing.T) {
	cases := []struct{ line, reason string }{
		{"", "命令为空"},
		{"   ", "命令为空"},
		{`bash -lc "python3 x.py"`, "不支持的命令"},
		{"cat candidate.json", "不支持的命令"},
		{"PYTHONPATH=. python3 scripts/a.py", "不支持的命令"},
		{`python3 -c "print(1)"`, "不允许 python 选项"},
		{"python3 -m http.server", "不允许 python 选项"},
		{"python3", "缺少脚本路径"},
		{"python3 scripts/run.sh", "只能运行 .py 脚本"},
		{"python3 /tmp/x.py", "绝对路径"},
		{"python3 ../other/scripts/x.py", "不能包含 .."},
		{"python3 scripts/a.py | head", "不支持 shell 语法"},
		{"python3 scripts/a.py > out.txt", "不支持 shell 语法"},
		{"cd scripts && python3 a.py", "不支持 shell 语法"},
		{"python3 scripts/a.py; rm -rf /", "不支持 shell 语法"},
		{"python3 scripts/a.py $(whoami)", "不支持 shell 语法"},
		{"python3 scripts/a.py `id`", "不支持 shell 语法"},
		{`python3 scripts/a.py "$HOME"`, "不支持变量或命令替换"},
		{"python3 scripts/a.py *.json", "不支持 shell 语法"},
		{"python3 scripts/a.py candidate.json --duration-sec <DURATION_SEC>", "不支持 shell 语法"},
		{`python3 "scripts/a.py`, "引号未闭合"},
		{"python3 scripts/a.py\nrm x", "不支持多行命令"},
		{"embed-text a.json sourceText", "用法：embed-text"},
	}
	for _, c := range cases {
		_, err := ParseCommand(c.line)
		if err == nil {
			t.Errorf("%q: 应被拒绝", c.line)
			continue
		}
		if !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%q: 错误应可用 errors.Is(ErrCommandRejected) 识别: %v", c.line, err)
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%q: 原因 %q 不含 %q", c.line, err.Error(), c.reason)
		}
	}
}

func TestCheckRelPath(t *testing.T) {
	for _, ok := range []string{"candidate.json", "./out/plan.json", "scripts/validate_storyboard.py", "带 空格.json"} {
		if err := checkRelPath(ok); err != nil {
			t.Errorf("%q 应允许: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x.json", "scripts/../../x", `C:\Windows\x`, "C:x", `\\server\share\x`,
		"candidate.json:stream", "NUL", "con.txt", "out/Com1", "~/x"} {
		if err := checkRelPath(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		} else if !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%q: 应是 ErrCommandRejected: %v", bad, err)
		}
	}
}

func TestMapScriptArgs(t *testing.T) {
	var seen []string
	resolve := func(p string) (string, error) {
		seen = append(seen, p)
		return "WS/" + p, nil
	}
	got, err := mapScriptArgs([]string{"candidate.json", "--duration-sec", "10", "-v", "--source=source.txt", "--ratio=1.5", "-3", `out\plan.json`}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"WS/candidate.json", "--duration-sec", "10", "-v", "--source=WS/source.txt", "--ratio=1.5", "-3", "WS/out/plan.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if !reflect.DeepEqual(seen, []string{"candidate.json", "source.txt", "out/plan.json"}) {
		t.Fatalf("只有路径参数应交给 resolvePath，got %#v", seen)
	}
	for _, bad := range [][]string{{"../x.json"}, {"--source=/etc/passwd"}, {"C:/x"}} {
		if _, err := mapScriptArgs(bad, resolve); !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%v 应拒绝，got %v", bad, err)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/skillruntime/ -run 'TestParseCommand|TestCheckRelPath|TestMapScriptArgs'`
Expected: FAIL（`undefined: ParseCommand` 等）

- [ ] **Step 3: 实现**

`server/internal/skillruntime/command.go`：

```go
package skillruntime

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 受限 shell 的命令语法（X6）：execute 不经过任何 shell，这里自己分词并只认两种命令：
//
//	python3 <Skill 自带 .py 脚本> [参数…]
//	embed-text <JSON 文件> <字段名> <文本文件>
//
// 其余一律拒绝。拒绝原因直接作为工具结果给模型看，所以要写清楚“该怎么写”。

type CommandKind int

const (
	CommandPython CommandKind = iota + 1
	CommandEmbedText
)

const embedTextCommand = "embed-text"

type ParsedCommand struct {
	Kind   CommandKind
	Script string   // CommandPython：脚本相对路径，分隔符已统一为 /
	Args   []string // CommandPython：脚本参数原样；CommandEmbedText：JSON 路径、字段名、文本路径
}

// ErrCommandRejected 标记“命令不在白名单内”，与 I/O 等真正的错误区分开。
var ErrCommandRejected = errors.New("命令被拒绝")

type commandRejectedError struct{ reason string }

func (e *commandRejectedError) Error() string { return e.reason }
func (e *commandRejectedError) Unwrap() error { return ErrCommandRejected }

func rejectf(format string, args ...any) error {
	return &commandRejectedError{reason: fmt.Sprintf(format, args...)}
}

var (
	pythonNamePattern = regexp.MustCompile(`^(?i)(python(3(\.[0-9]+)?)?(\.exe)?|py)$`)
	flagPattern       = regexp.MustCompile(`^--?[A-Za-z][A-Za-z0-9_-]*$`)
	numberPattern     = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	windowsDeviceName = regexp.MustCompile(`^(?i)(con|prn|aux|nul|com[0-9]|lpt[0-9])(\..*)?$`)
)

// 不加引号时出现即拒绝：管道、重定向、串联、变量/命令替换、通配、子 shell、注释、历史展开。
const shellMetaChars = "|&;<>$`()*?[]{}~!#"

// 唯一容忍的重定向：模型习惯在命令末尾加 2>&1，而这里本来就合并 stdout/stderr。
const mergeStderrSuffix = " 2>&1"

func ParseCommand(line string) (ParsedCommand, error) {
	tokens, err := tokenizeCommand(line)
	if err != nil {
		return ParsedCommand{}, err
	}
	head := tokens[0]
	if head == embedTextCommand {
		if len(tokens) != 4 {
			return ParsedCommand{}, rejectf("用法：embed-text <JSON 文件> <字段名> <文本文件>")
		}
		return ParsedCommand{Kind: CommandEmbedText, Args: []string{toSlash(tokens[1]), tokens[2], toSlash(tokens[3])}}, nil
	}
	if !pythonNamePattern.MatchString(head) {
		return ParsedCommand{}, rejectf("不支持的命令 %q：execute 不是 bash，只能用 python3 运行 Skill 自带脚本，或使用 embed-text", head)
	}
	if len(tokens) < 2 {
		return ParsedCommand{}, rejectf("缺少脚本路径：用法 python3 scripts/<脚本名>.py [参数…]")
	}
	script := tokens[1]
	if strings.HasPrefix(script, "-") {
		return ParsedCommand{}, rejectf("不允许 python 选项 %q（例如 -c、-m）：只能直接运行 Skill 自带的 .py 脚本", script)
	}
	script = toSlash(script)
	if !strings.HasSuffix(strings.ToLower(script), ".py") {
		return ParsedCommand{}, rejectf("只能运行 .py 脚本：%q", script)
	}
	if err := checkRelPath(script); err != nil {
		return ParsedCommand{}, err
	}
	return ParsedCommand{Kind: CommandPython, Script: script, Args: append([]string{}, tokens[2:]...)}, nil
}

// tokenizeCommand 按 POSIX 习惯分词的最小子集：空白分隔、单引号内全部原样、
// 双引号内只认 \" 和 \\。引号外的反斜杠按普通字符处理（兼容 Windows 路径）。
func tokenizeCommand(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if strings.ContainsRune(line, 0) {
		return nil, rejectf("命令里不能有 NUL 字符")
	}
	if strings.ContainsAny(line, "\r\n") {
		return nil, rejectf("不支持多行命令：每次只执行一条命令")
	}
	if strings.HasSuffix(line, mergeStderrSuffix) {
		line = strings.TrimSpace(strings.TrimSuffix(line, mergeStderrSuffix))
	}
	if line == "" {
		return nil, rejectf("命令为空")
	}
	const (
		stateNone = iota
		stateSingle
		stateDouble
	)
	var tokens []string
	var cur strings.Builder
	inToken := false
	state := stateNone
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch state {
		case stateSingle:
			if c == '\'' {
				state = stateNone
			} else {
				cur.WriteRune(c)
			}
		case stateDouble:
			switch c {
			case '"':
				state = stateNone
			case '\\':
				if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
					cur.WriteRune(runes[i])
				} else {
					cur.WriteRune(c)
				}
			case '$', '`':
				return nil, rejectf("不支持变量或命令替换（%q）：参数请直接写出具体值", string(c))
			default:
				cur.WriteRune(c)
			}
		default:
			switch {
			case c == ' ' || c == '\t':
				if inToken {
					tokens = append(tokens, cur.String())
					cur.Reset()
					inToken = false
				}
				continue
			case c == '\'':
				state = stateSingle
			case c == '"':
				state = stateDouble
			case strings.ContainsRune(shellMetaChars, c):
				return nil, rejectf("不支持 shell 语法 %q：execute 不是 bash，不支持管道、重定向、&&、;、变量和通配符", string(c))
			default:
				cur.WriteRune(c)
			}
		}
		inToken = true
	}
	if state != stateNone {
		return nil, rejectf("引号未闭合")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	if len(tokens) == 0 {
		return nil, rejectf("命令为空")
	}
	return tokens, nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// checkRelPath 是不碰文件系统的静态检查：只允许不含 .. 的相对路径，
// 挡住绝对路径、盘符（C:x）、UNC、NTFS 备用数据流（a:b）和 Windows 设备名。
func checkRelPath(p string) error {
	p = toSlash(p)
	switch {
	case p == "":
		return rejectf("路径为空")
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, "~"), len(p) >= 2 && p[1] == ':':
		return rejectf("只能使用工作区内的相对路径，不能用绝对路径：%q", p)
	case strings.Contains(p, ":"):
		return rejectf("路径里不能有冒号：%q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return rejectf("路径不能包含 ..：%q", p)
		}
		if windowsDeviceName.MatchString(seg) {
			return rejectf("路径不能使用设备名：%q", p)
		}
	}
	return nil
}

// mapScriptArgs 逐个处理脚本参数：数字、-x / --flag 原样保留；--flag=值 拆开处理值；
// 其余一律视为工作区文件路径，先做静态检查，再交给 resolvePath 做文件系统检查。
func mapScriptArgs(args []string, resolvePath func(string) (string, error)) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if i := strings.IndexByte(a, '='); i > 0 && flagPattern.MatchString(a[:i]) {
			v, err := mapArgValue(a[i+1:], resolvePath)
			if err != nil {
				return nil, err
			}
			out = append(out, a[:i]+"="+v)
			continue
		}
		if numberPattern.MatchString(a) || flagPattern.MatchString(a) {
			out = append(out, a)
			continue
		}
		v, err := mapArgValue(a, resolvePath)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func mapArgValue(v string, resolvePath func(string) (string, error)) (string, error) {
	if numberPattern.MatchString(v) {
		return v, nil
	}
	if err := checkRelPath(v); err != nil {
		return "", err
	}
	return resolvePath(toSlash(v))
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd server && go test ./internal/skillruntime/ -run 'TestParseCommand|TestCheckRelPath|TestMapScriptArgs' -v`
Expected: 4 个测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add server/internal/skillruntime/command.go server/internal/skillruntime/command_test.go
git commit -m "feat(skillruntime): 受限命令语法解析与路径规则（不经过 bash）"
```

---

### Task 4: skillruntime 移植（替换 shell 接口）

**Files:**
- Create（从参考仓复制）: `server/internal/skillruntime/{catalog,workspace,cleaner,model_retry,model_trace,artifact_output,runner}.go` 及对应 `_test.go`
- Create: `server/internal/skillruntime/shell_output.go`
- Create: `server/internal/skillruntime/trusted_shell_test.go`
- Create: `server/internal/skillruntime/shell_test.go`（参考仓文件删掉 srt 部分）
- Modify: `server/internal/skillruntime/runner.go`、`runner_test.go`（复制后修改）
- Modify: `server/go.mod`、`server/go.sum`

**Interfaces:**
- Consumes: 无
- Produces（Task 5、6、7、8 依赖）：
  - `type ShellFactory func(workspace, skillDir string) (filesystem.Shell, error)`：`workspace` 是本次 Run 的可写工作区（Skill 的副本），`skillDir` 是原始 Skill 目录（只读、可信）。
  - 参考仓原样：`NewCatalog(root string) (*Catalog, error)`、`(*Catalog).Snapshot(ctx, name) (Snapshot, error)`、`NewRunWorkspace(root string) (*RunWorkspace, error)`、`(*RunWorkspace).Create(source string) (string, error)`、`(*RunWorkspace).Root() string`、`NewWorkspaceCleaner(root string) *WorkspaceCleaner`、`(*WorkspaceCleaner).Run(ctx)`、`NewWorkspaceBackend(root string) (*WorkspaceBackend, error)`、`(*WorkspaceBackend).Write(ctx, *filesystem.WriteRequest) error`、`NewRuntime(catalog, workspace, factory ModelFactory, maxIterations int, shellFactory ShellFactory) *Runtime`、`(*Runtime).Snapshot`、`(*Runtime).Run(ctx, RunRequest) (RunResult, error)`、类型 `Snapshot`、`RunRequest`、`RunResult`、`ModelConfig`、包内函数 `withinRoot(root, path string) bool`。
  - `shell_output.go`：`type cappedBuffer struct{…}`（`Write`、`String`、字段 `truncated`）、`const maxShellOutputBytes = 1024 * 1024`、`const defaultShellTimeout = 30 * time.Second`。
  - 测试辅助（仅 `_test.go`）：`NewTrustedShell(root string)`、`TrustedShellFactory(workspace, skillDir string)`、`writeTestFile`（参考仓已有）、`shellScriptModel`、`shellModelState`。

- [ ] **Step 1: 加依赖**

```bash
cd server && go get github.com/cloudwego/eino@v0.8.13 github.com/cloudwego/eino-ext/components/model/openai@v0.1.13 github.com/bmatcuk/doublestar/v4@v4.10.0 gopkg.in/yaml.v3@v3.0.1
```

- [ ] **Step 2: 复制参考仓文件**

```bash
REF=/Users/james/github/huabu_studio/apps/api/internal
D=server/internal/skillruntime
mkdir -p $D
for f in catalog workspace cleaner model_retry model_trace artifact_output runner; do
  cp $REF/skillruntime/$f.go $D/
  cp $REF/skillruntime/${f}_test.go $D/
done
# shell_test.go：保留 TrustedShell 测试（1-56 行）和 shellScriptModel / TestRuntimeExposesExecuteToolForSkillScripts（152 行起），
# 删掉 writeExecutable 与三个 SandboxedShell 测试（57-151 行）。
sed -n '1,56p;152,279p' $REF/skillruntime/shell_test.go > $D/shell_test.go
cd server && go mod tidy
```

（此时 `go build ./internal/skillruntime/` 应已通过——`runner.go` 仍是旧的 `ShellFactory` 签名、不依赖 `shell.go`；测试因缺 `TrustedShell` 暂不能编译，后续步骤补上。Task 6 只需 `Snapshot`/`RunRequest`/`RunResult`/`ModelConfig` 类型，为让它尽早开工，本步先单独提交一次。）

Run: `cd server && go build ./internal/skillruntime/`
Expected: 无输出。

```bash
git add server/go.mod server/go.sum server/internal/skillruntime
git commit -m "chore(skillruntime): 从参考仓复制 skillruntime（待替换 shell）"
```

- [ ] **Step 3: 写 shell_output.go**

`server/internal/skillruntime/shell_output.go`：

```go
package skillruntime

import (
	"bytes"
	"time"
)

// 单条 Skill 命令的输出上限与默认超时（与参考仓一致）。
const (
	maxShellOutputBytes = 1024 * 1024
	defaultShellTimeout = 30 * time.Second
)

// cappedBuffer 只保留前 max 字节，超出部分丢弃并标记 truncated；Write 永远报告全部写入，
// 免得子进程因管道写失败而提前退出。
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLen, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	return originalLen, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
```

- [ ] **Step 4: 写仅测试用的 TrustedShell**

`server/internal/skillruntime/trusted_shell_test.go`：

```go
package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloudwego/eino/adk/filesystem"
)

// TrustedShell 用本机 bash 直接执行命令、不做任何限制。只存在于 _test.go：
// runner 的单元测试靠它保持 hermetic，生产代码无法引用它（生产只能装配 RestrictedShell）。
type TrustedShell struct {
	root string
	bash string
	tmp  string
}

func TrustedShellFactory(workspace, _ string) (filesystem.Shell, error) {
	return NewTrustedShell(workspace)
}

func NewTrustedShell(root string) (*TrustedShell, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve trusted shell root: %w", err)
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("trusted shell root is not a directory")
	}
	tmp := filepath.Join(realRoot, ".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, err
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return nil, errors.New("bash is required for TrustedShell tests")
	}
	return &TrustedShell{root: realRoot, bash: bash, tmp: tmp}, nil
}

func (s *TrustedShell) Execute(ctx context.Context, input *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if input == nil || input.Command == "" {
		return nil, errors.New("command is required")
	}
	cmd := exec.CommandContext(ctx, s.bash, "-lc", input.Command)
	cmd.Dir = s.root
	cmd.Env = append(os.Environ(), "SKILL_DIR="+s.root, "WORK_DIR="+s.root, "TMPDIR="+s.tmp, "PYTHONUNBUFFERED=1")
	output := &cappedBuffer{max: maxShellOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			if errors.Is(err, fs.ErrPermission) {
				return nil, fmt.Errorf("execute command: %w", err)
			}
			return nil, err
		}
		exitCode = exitErr.ExitCode()
	}
	return &filesystem.ExecuteResponse{Output: output.String(), ExitCode: &exitCode, Truncated: output.truncated}, nil
}

var _ filesystem.Shell = (*TrustedShell)(nil)
```

- [ ] **Step 5: 修改 runner.go**

1）把 `ShellFactory` 的定义与注释替换为：

```go
// ShellFactory 为一次 Run 造 Skill `execute` 用的 Shell。workspace 是本次 Run 独占的可写工作区
// （Skill 目录的副本），skillDir 是原始 Skill 目录（只读、可信）——白名单只执行 skillDir 里的原件，
// 模型在工作区里改写或新建的脚本不会被执行。生产装配 RestrictedShellFactory；工厂为 nil 时 Run 直接报错。
type ShellFactory func(workspace, skillDir string) (filesystem.Shell, error)
```

2）`Run` 里的调用：

```go
	skillShell, err := r.shellFactory(runFS.Root(), selectedFS.Root())
```

3）把 `genericAgentInstruction` 里的这一行

```
execute 工具在受限沙箱中运行 Skill 命令：只能读写当前工作区，无网络访问，环境不含任何外部密钥。不要尝试访问工作区以外的文件或联网。
```

替换为：

```
execute 工具不是 bash，只支持两种命令：python3 scripts/<脚本名>.py [参数…]（只能运行当前 Skill 自带的脚本，文件参数用工作区相对路径）；embed-text <JSON 文件> <字段名> <文本文件>（把文本文件原样写入 JSON 顶层字段，需要把 source.txt 逐字注入 JSON 时用它，例如 embed-text adapted_script.json sourceText source.txt）。不支持管道、重定向、&&、;、python -c 和其他任何命令；查看和写入文件请用文件工具。无网络访问，环境不含任何外部密钥。
```

并把 `workspacePathInstruction` 最后一行 `Shell 命令已经从该工作区运行。` 替换为：

```
execute 命令已经从该工作区运行；它不是 bash，只支持 python3 运行 Skill 自带脚本和 embed-text。
```

4）文件头 `ShellFactory` 以外提到 srt / SandboxedShell 的注释（`NewRuntime` 附近若有）改为“生产注入 RestrictedShell”。

- [ ] **Step 6: 修改 runner_test.go 与 shell_test.go**

`runner_test.go` 中 `TestRuntimeUsesInjectedShellFactory` 的工厂：

```go
	factory := func(root, _ string) (filesystem.Shell, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return NewTrustedShell(root)
	}
```

其余 `TrustedShellFactory` 引用不用改（签名已在 Step 4 匹配）。`shell_test.go` 若编译报 `"time" imported and not used`，删掉该 import；`runner_test.go` 第 23 行注释“退回裸 bash 执行”改为“退回不受限执行”。

- [ ] **Step 7: 运行确认通过**

Run: `cd server && go vet ./internal/skillruntime/ && go test ./internal/skillruntime/`
Expected: PASS（含 Task 3 的测试若已合入）。`grep -rn "Sandboxed\|srt" server/internal/skillruntime` 无输出。

- [ ] **Step 8: Commit**

```bash
git add server/internal/skillruntime server/go.mod server/go.sum
git commit -m "feat(skillruntime): ShellFactory 传入原始 Skill 目录，删除 Linux 专用 srt 沙箱"
```

---

### Task 5: RestrictedShell、内置 Python 定位与 embed-text

**Files:**
- Create: `server/internal/skillruntime/restricted_shell.go`
- Create: `server/internal/skillruntime/embed_text.go`
- Create: `server/internal/skillruntime/python_runtime.go`
- Create: `server/internal/skillruntime/exec_windows.go`、`exec_other.go`
- Test: `server/internal/skillruntime/restricted_shell_test.go`、`python_runtime_test.go`

**Interfaces:**
- Consumes: Task 3 的 `ParseCommand`、`ParsedCommand`、`CommandPython`、`CommandEmbedText`、`ErrCommandRejected`、`rejectf`、`checkRelPath`、`mapScriptArgs`；Task 4 的 `ShellFactory`、`withinRoot`、`cappedBuffer`、`maxShellOutputBytes`、`defaultShellTimeout`、`NewWorkspaceBackend`、`NewCatalog`、`NewRunWorkspace`、`NewRuntime`、测试辅助 `writeTestFile`、`shellScriptModel`、`shellModelState`。
- Produces（Task 7、8 依赖）：
  - `type RestrictedShellConfig struct { Python string; Timeout time.Duration }`
  - `func NewRestrictedShell(workspace, skillDir string, cfg RestrictedShellConfig) (*RestrictedShell, error)`
  - `func RestrictedShellFactory(cfg RestrictedShellConfig) ShellFactory`
  - `const PythonHomeEnv = "MIAOJU_PYTHON_HOME"`
  - `func PythonInterpreter(home string) string`
  - `func LocatePython(explicitHome string) (string, error)`：返回解释器绝对路径。
  - 返回码：拒绝 `126`，超时 `124`。

- [ ] **Step 1: 写失败的测试**

`server/internal/skillruntime/python_runtime_test.go`：

```go
package skillruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makePythonHome(t *testing.T, home string) string {
	t.Helper()
	p := PythonInterpreter(home)
	writeTestFile(t, p, "fake")
	return p
}

func TestLocatePythonOrder(t *testing.T) {
	base := t.TempDir()
	explicit := filepath.Join(base, "explicit")
	env := filepath.Join(base, "env")
	exeDir := filepath.Join(base, "喵剧AI.app", "Contents", "MacOS")
	exe := filepath.Join(exeDir, "sidecar")
	writeTestFile(t, exe, "bin")

	wantExplicit := makePythonHome(t, explicit)
	wantEnv := makePythonHome(t, env)
	wantMac := makePythonHome(t, filepath.Join(exeDir, "..", "Resources", "python"))

	if got, err := locatePython(explicit, env, exe); err != nil || !sameFile(t, got, wantExplicit) {
		t.Fatalf("显式参数优先: got %q %v", got, err)
	}
	if got, err := locatePython("", env, exe); err != nil || !sameFile(t, got, wantEnv) {
		t.Fatalf("其次环境变量: got %q %v", got, err)
	}
	if got, err := locatePython("", "", exe); err != nil || !sameFile(t, got, wantMac) {
		t.Fatalf("再次 .app 的 Resources: got %q %v", got, err)
	}

	winDir := filepath.Join(base, "miaoju-windows-x64")
	winExe := filepath.Join(winDir, "sidecar.exe")
	writeTestFile(t, winExe, "bin")
	wantWin := makePythonHome(t, filepath.Join(winDir, "python"))
	if got, err := locatePython("", "", winExe); err != nil || !sameFile(t, got, wantWin) {
		t.Fatalf("最后 exe 同级 python/: got %q %v", got, err)
	}
}

// sameFile 按文件身份比较：macOS 的 t.TempDir() 在 /var 下，EvalSymlinks 后变成 /private/var，字符串不相等。
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

func TestLocatePythonReportsCandidates(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "sidecar")
	writeTestFile(t, exe, "bin")
	_, err := locatePython("", "", exe)
	if err == nil || !strings.Contains(err.Error(), "找不到内置 Python") || !strings.Contains(err.Error(), "Resources") {
		t.Fatalf("应报出尝试过的路径: %v", err)
	}
}

func TestPythonInterpreterLayout(t *testing.T) {
	got := PythonInterpreter("H")
	if !(got == filepath.Join("H", "python.exe") || got == filepath.Join("H", "bin", "python3")) {
		t.Fatalf("unexpected interpreter path %q", got)
	}
	_ = os.Getenv(PythonHomeEnv)
}
```

`server/internal/skillruntime/restricted_shell_test.go`：

```go
package skillruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
)

// testPython 单元测试用本机 python3；内置 Python 的真实运行在 Task 8 的 bundled_python_test.go。
func testPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("本机没有 python3")
	return ""
}

const echoArgsScript = `import os, sys
print("ARGS=" + "|".join(sys.argv[1:]))
print("CWD=" + os.getcwd())
print("HOME=" + os.environ.get("HOME", os.environ.get("USERPROFILE", "")))
print("TMP=" + os.environ.get("TMPDIR", os.environ.get("TEMP", "")))
print("SECRET=" + os.environ.get("MIAOJU_SECRET", "<none>"))
print("中文校验通过")
`

type shellFixture struct {
	shell     *RestrictedShell
	workspace string
	skillDir  string
	outside   string
}

func newShellFixture(t *testing.T, cfg RestrictedShellConfig) shellFixture {
	t.Helper()
	base := t.TempDir()
	skillDir := filepath.Join(base, "skills", "demo")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: demo\ndescription: demo\n---\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "echo_args.py"), echoArgsScript)
	writeTestFile(t, filepath.Join(skillDir, "scripts", "exit3.py"), "import sys\nprint('bye')\nsys.exit(3)\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "sleep.py"), "import time\ntime.sleep(5)\n")
	writeTestFile(t, filepath.Join(skillDir, "scripts", "flood.py"), "import sys\nsys.stdout.write('x' * (2 * 1024 * 1024))\n")
	outside := filepath.Join(base, "outside")
	writeTestFile(t, filepath.Join(outside, "secret.json"), `{"k":"v"}`)
	ws, err := NewRunWorkspace(filepath.Join(base, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := ws.Create(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Python == "" {
		cfg.Python = testPython(t)
	}
	shell, err := NewRestrictedShell(runDir, skillDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return shellFixture{shell: shell, workspace: shell.workspace, skillDir: shell.skillDir, outside: outside}
}

func run(t *testing.T, s *RestrictedShell, command string) *filesystem.ExecuteResponse {
	t.Helper()
	resp, err := s.Execute(context.Background(), &filesystem.ExecuteRequest{Command: command})
	if err != nil {
		t.Fatalf("%q: Execute 不应返回 error: %v", command, err)
	}
	return resp
}

func exitCode(resp *filesystem.ExecuteResponse) int {
	if resp.ExitCode == nil {
		return -1
	}
	return *resp.ExitCode
}

func TestRestrictedShellRunsSkillScriptWithWorkspaceCwd(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	writeTestFile(t, filepath.Join(f.workspace, "candidate.json"), "{}")
	resp := run(t, f.shell, "python3 scripts/echo_args.py candidate.json --duration-sec 10 --source=source.txt 2>&1")
	if exitCode(resp) != 0 {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	wantArgs := "ARGS=candidate.json|--duration-sec|10|--source=source.txt"
	if !strings.Contains(resp.Output, wantArgs) || !strings.Contains(resp.Output, "CWD="+f.workspace) {
		t.Fatalf("参数或 cwd 不对: %s", resp.Output)
	}
	if !strings.Contains(resp.Output, "中文校验通过") {
		t.Fatalf("中文输出应原样返回（-X utf8）: %q", resp.Output)
	}
}

func TestRestrictedShellScrubsEnvironment(t *testing.T) {
	t.Setenv("MIAOJU_SECRET", "leak")
	t.Setenv("PYTHONPATH", "/somewhere")
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/echo_args.py")
	if !strings.Contains(resp.Output, "SECRET=<none>") {
		t.Fatalf("父进程环境变量泄露给了脚本: %s", resp.Output)
	}
	if !strings.Contains(resp.Output, "HOME="+f.workspace) || !strings.Contains(resp.Output, "TMP="+filepath.Join(f.workspace, ".tmp")) {
		t.Fatalf("HOME/TMP 应指向工作区: %s", resp.Output)
	}
}

func TestRestrictedShellRunsOriginalNotWorkspaceCopy(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	// 模型用 write_file 改写了工作区里的脚本副本，又新建了一个脚本。
	writeTestFile(t, filepath.Join(f.workspace, "scripts", "echo_args.py"), "print('TAMPERED')\n")
	writeTestFile(t, filepath.Join(f.workspace, "scripts", "evil.py"), "print('EVIL')\n")

	resp := run(t, f.shell, "python3 scripts/echo_args.py")
	if strings.Contains(resp.Output, "TAMPERED") || !strings.Contains(resp.Output, "ARGS=") {
		t.Fatalf("应执行原始 Skill 脚本，而不是工作区副本: %s", resp.Output)
	}
	resp = run(t, f.shell, "python3 scripts/evil.py")
	if exitCode(resp) != exitRejected || strings.Contains(resp.Output, "EVIL") || !strings.Contains(resp.Output, "Skill 目录里没有脚本") {
		t.Fatalf("工作区里新建的脚本必须拒绝: exit=%d %s", exitCode(resp), resp.Output)
	}
}

func TestRestrictedShellRejectsDisallowedCommandsWithExit126(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	for _, cmd := range []string{
		`python3 -c "import os; print(os.listdir('/'))"`,
		"cd scripts && python3 echo_args.py",
		"python3 scripts/echo_args.py | head -5",
		"python3 scripts/echo_args.py candidate.json --duration-sec <DURATION_SEC>",
		"ls -la",
		"rm -rf .",
	} {
		resp := run(t, f.shell, cmd)
		if exitCode(resp) != exitRejected {
			t.Errorf("%q: exit=%d，应为 %d", cmd, exitCode(resp), exitRejected)
		}
		if !strings.Contains(resp.Output, "命令被拒绝") || !strings.Contains(resp.Output, "embed-text <JSON 文件>") {
			t.Errorf("%q: 拒绝消息应说明原因和可用写法: %s", cmd, resp.Output)
		}
	}
}

func TestRestrictedShellRejectsPathEscapes(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	cases := []string{
		"python3 scripts/echo_args.py ../outside/secret.json",
		"python3 scripts/echo_args.py " + filepath.ToSlash(filepath.Join(f.outside, "secret.json")),
		"python3 scripts/echo_args.py --source=../../x.txt",
		"python3 ../demo/scripts/echo_args.py",
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(f.outside, "secret.json"), filepath.Join(f.workspace, "link.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.outside, filepath.Join(f.workspace, "linkdir")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.outside, "secret.json"), filepath.Join(f.skillDir, "scripts", "link.py")); err != nil {
			t.Fatal(err)
		}
		cases = append(cases,
			"python3 scripts/echo_args.py link.json",
			"python3 scripts/echo_args.py linkdir/secret.json",
			"python3 scripts/echo_args.py linkdir/new.json",
			"python3 scripts/link.py",
		)
	}
	for _, cmd := range cases {
		resp := run(t, f.shell, cmd)
		if exitCode(resp) != exitRejected || strings.Contains(resp.Output, "ARGS=") {
			t.Errorf("%q: 越界路径必须拒绝且不执行脚本: exit=%d %s", cmd, exitCode(resp), resp.Output)
		}
	}
}

func TestRestrictedShellReportsNonZeroExit(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/exit3.py")
	if exitCode(resp) != 3 || !strings.Contains(resp.Output, "bye") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
}

func TestRestrictedShellTimeout(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{Timeout: 300 * time.Millisecond})
	start := time.Now()
	resp := run(t, f.shell, "python3 scripts/sleep.py")
	if exitCode(resp) != exitTimeout || !strings.Contains(resp.Output, "被终止") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("超时后应尽快返回，用了 %s", time.Since(start))
	}
}

func TestRestrictedShellCapsOutput(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	resp := run(t, f.shell, "python3 scripts/flood.py")
	if !resp.Truncated || len(resp.Output) > maxShellOutputBytes {
		t.Fatalf("truncated=%v len=%d", resp.Truncated, len(resp.Output))
	}
}

func TestNewRestrictedShellRequiresPython(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRestrictedShell(dir, dir, RestrictedShellConfig{}); err == nil || !strings.Contains(err.Error(), "内置 Python") {
		t.Fatalf("缺少解释器应报错: %v", err)
	}
	if _, err := NewRestrictedShell(dir, dir, RestrictedShellConfig{Python: filepath.Join(dir, "nope")}); err == nil {
		t.Fatal("解释器不存在应报错")
	}
}

func orderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	fields, err := decodeOrderedObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestEmbedTextCopiesSourceVerbatimAndKeepsKeyOrder(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	source := strings.Repeat("安岁推开门，说：“你回来了？”\\n<b>&</b>\t", 200) + "\n最后一行没有换行"
	writeTestFile(t, filepath.Join(f.workspace, "source.txt"), source)
	writeTestFile(t, filepath.Join(f.workspace, "adapted_script.json"),
		`{"sourceText":"手抄的摘要","sourceSentences":[{"id":"S001"}],"scriptLines":[],"scriptText":"x"}`)

	resp := run(t, f.shell, "embed-text adapted_script.json sourceText source.txt")
	if exitCode(resp) != 0 || !strings.HasPrefix(resp.Output, "OK:") {
		t.Fatalf("exit=%d output=%s", exitCode(resp), resp.Output)
	}
	raw, err := os.ReadFile(filepath.Join(f.workspace, "adapted_script.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SourceText      string            `json:"sourceText"`
		SourceSentences []json.RawMessage `json:"sourceSentences"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SourceText != source {
		t.Fatal("sourceText 必须与 source.txt 逐字一致")
	}
	if len(doc.SourceSentences) != 1 {
		t.Fatal("其他字段必须保留")
	}
	if got := strings.Join(orderedKeys(t, raw), ","); got != "sourceText,sourceSentences,scriptLines,scriptText" {
		t.Fatalf("字段顺序必须保持: %s", got)
	}

	// 字段不存在时追加到末尾；JSON 文件不存在时新建。
	resp = run(t, f.shell, "embed-text new.json sourceText source.txt")
	if exitCode(resp) != 0 {
		t.Fatalf("新建 JSON 失败: %s", resp.Output)
	}
	writeTestFile(t, filepath.Join(f.workspace, "plan.json"), `{"b":1,"a":2}`)
	run(t, f.shell, "embed-text plan.json zz source.txt")
	raw, _ = os.ReadFile(filepath.Join(f.workspace, "plan.json"))
	if got := strings.Join(orderedKeys(t, raw), ","); got != "b,a,zz" {
		t.Fatalf("新字段应追加在末尾: %s", got)
	}
	if bytes.Contains(raw, []byte(`\u003c`)) {
		t.Fatal("不应转义 < > &，保持原文可读")
	}
}

func TestEmbedTextRejectsBadInput(t *testing.T) {
	f := newShellFixture(t, RestrictedShellConfig{})
	writeTestFile(t, filepath.Join(f.workspace, "source.txt"), "原文")
	writeTestFile(t, filepath.Join(f.workspace, "array.json"), `[1,2]`)
	for _, cmd := range []string{
		"embed-text array.json sourceText source.txt",
		"embed-text a.json source-text source.txt",
		"embed-text a.json sourceText missing.txt",
		"embed-text ../a.json sourceText source.txt",
		"embed-text a.json sourceText ../outside/secret.json",
	} {
		if resp := run(t, f.shell, cmd); exitCode(resp) != exitRejected {
			t.Errorf("%q: exit=%d output=%s", cmd, exitCode(resp), resp.Output)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.workspace), "a.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("越界写入不应发生")
	}
}

// Runtime 必须把原始 Skill 目录交给 RestrictedShell：模型发出的 python3 scripts/generate.py 能跑通。
func TestRuntimeWithRestrictedShellRunsSkillScript(t *testing.T) {
	python := testPython(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "script-style", "SKILL.md"), "---\nname: script-style\ndescription: Script style\n---\nRun scripts/generate.py and use its result.\n")
	writeTestFile(t, filepath.Join(root, "script-style", "scripts", "generate.py"), "from pathlib import Path\nprint(Path(\"candidate.json\").read_text())\n")
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "script-style")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &shellModelState{}
	rt := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &shellScriptModel{state: state}, nil
	}, 10, RestrictedShellFactory(RestrictedShellConfig{Python: python}))
	result, err := rt.Run(context.Background(), RunRequest{Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "run it"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final from script" || !state.sawExecuteTool || !state.sawScriptOutput {
		t.Fatalf("result=%#v state=%#v", result, state)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/skillruntime/ -run 'Restricted|EmbedText|LocatePython|PythonInterpreter'`
Expected: FAIL（`undefined: NewRestrictedShell` 等）

- [ ] **Step 3: 实现 python_runtime.go**

```go
package skillruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PythonHomeEnv 开发时可用它指向 src-tauri/resources/python。
const PythonHomeEnv = "MIAOJU_PYTHON_HOME"

// PythonInterpreter 返回 python-build-standalone 目录里的解释器路径。
func PythonInterpreter(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "python.exe")
	}
	return filepath.Join(home, "bin", "python3")
}

// LocatePython 依次尝试：--python-home → $MIAOJU_PYTHON_HOME → <sidecar>/../Resources/python（macOS .app）
// → <sidecar 同级>/python（Windows zip、cargo tauri dev）。返回解释器绝对路径。
func LocatePython(explicitHome string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return locatePython(explicitHome, os.Getenv(PythonHomeEnv), exe)
}

func locatePython(explicitHome, envHome, exe string) (string, error) {
	var homes []string
	if explicitHome != "" {
		homes = append(homes, explicitHome)
	}
	if envHome != "" {
		homes = append(homes, envHome)
	}
	if exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		homes = append(homes, filepath.Join(dir, "..", "Resources", "python"), filepath.Join(dir, "python"))
	}
	var tried []string
	for _, home := range homes {
		p := PythonInterpreter(home)
		tried = append(tried, p)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			abs, err := filepath.Abs(p)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("找不到内置 Python 运行时（已尝试：%s）", strings.Join(tried, "、"))
}
```

- [ ] **Step 4: 实现 exec_windows.go / exec_other.go**

`server/internal/skillruntime/exec_windows.go`：

```go
//go:build windows

package skillruntime

import (
	"os"
	"os/exec"
	"syscall"
)

// sidecar 本身没有控制台；不加 CREATE_NO_WINDOW 的话，每次起 python.exe 都会弹出一个黑框。
const createNoWindow = 0x08000000

func configureChildProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Windows 上 Python 初始化随机数需要 SYSTEMROOT，缺了会直接崩溃。
func platformEnv() []string {
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		root = `C:\Windows`
	}
	return []string{"SYSTEMROOT=" + root}
}
```

`server/internal/skillruntime/exec_other.go`：

```go
//go:build !windows

package skillruntime

import "os/exec"

func configureChildProcess(*exec.Cmd) {}

func platformEnv() []string { return nil }
```

- [ ] **Step 5: 实现 restricted_shell.go**

```go
package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

// 返回码约定：126 = 命令不在白名单（与 sh 的“无法执行”一致），124 = 超时（与 timeout(1) 一致）。
const (
	exitRejected = 126
	exitTimeout  = 124
)

const restrictedShellUsage = `execute 不是 bash，只支持两种命令（不支持管道、重定向、&&、;、变量、通配符和 python -c）：
  python3 scripts/<脚本名>.py [参数…]
      只能运行当前 Skill 自带的脚本；文件参数必须是工作区内的相对路径，例如：
      python3 scripts/validate_storyboard.py candidate.json --duration-sec 10
  embed-text <JSON 文件> <字段名> <文本文件>
      把文本文件原样写入 JSON 顶层字段（保持其他字段与顺序），例如：
      embed-text adapted_script.json sourceText source.txt
查看、写入文件请用文件工具（ls / read_file / write_file / edit_file）。`

type RestrictedShellConfig struct {
	Python  string        // 内置解释器绝对路径（LocatePython 的结果）
	Timeout time.Duration // 单条命令墙钟上限，<=0 用 30s
}

// RestrictedShell 是桌面端唯一的 Skill execute 实现（X6）：不经过任何 shell，
// 只用内置 Python 执行原始 Skill 目录里的脚本，文件参数限定在本次 Run 工作区内，环境从零构造。
type RestrictedShell struct {
	workspace string // EvalSymlinks 后的 Run 工作区
	skillDir  string // EvalSymlinks 后的原始 Skill 目录
	tmp       string
	python    string
	timeout   time.Duration
}

func NewRestrictedShell(workspace, skillDir string, cfg RestrictedShellConfig) (*RestrictedShell, error) {
	if strings.TrimSpace(cfg.Python) == "" {
		return nil, errors.New("内置 Python 运行时缺失，无法执行 Skill 脚本")
	}
	python, err := filepath.Abs(cfg.Python)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(python); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("内置 Python 运行时不可用（%s）: %v", python, err)
	}
	ws, err := realDirectory(workspace)
	if err != nil {
		return nil, fmt.Errorf("Skill Run 工作区: %w", err)
	}
	sd, err := realDirectory(skillDir)
	if err != nil {
		return nil, fmt.Errorf("Skill 目录: %w", err)
	}
	tmp := filepath.Join(ws, ".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultShellTimeout
	}
	return &RestrictedShell{workspace: ws, skillDir: sd, tmp: tmp, python: python, timeout: timeout}, nil
}

// RestrictedShellFactory 解释器缺失时不在启动期报错，而是每次 Run 造 shell 时报错，任务以可读原因失败。
func RestrictedShellFactory(cfg RestrictedShellConfig) ShellFactory {
	return func(workspace, skillDir string) (filesystem.Shell, error) {
		return NewRestrictedShell(workspace, skillDir, cfg)
	}
}

func realDirectory(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s 不是目录", real)
	}
	return real, nil
}

func (s *RestrictedShell) Execute(ctx context.Context, input *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if input == nil || strings.TrimSpace(input.Command) == "" {
		return nil, errors.New("command is required")
	}
	resp, err := s.execute(ctx, input.Command)
	if errors.Is(err, ErrCommandRejected) {
		code := exitRejected
		return &filesystem.ExecuteResponse{Output: "命令被拒绝：" + err.Error() + "\n\n" + restrictedShellUsage + "\n", ExitCode: &code}, nil
	}
	return resp, err
}

func (s *RestrictedShell) execute(ctx context.Context, line string) (*filesystem.ExecuteResponse, error) {
	parsed, err := ParseCommand(line)
	if err != nil {
		return nil, err
	}
	if parsed.Kind == CommandEmbedText {
		return s.embedText(ctx, parsed.Args)
	}
	script, err := s.resolveScript(parsed.Script)
	if err != nil {
		return nil, err
	}
	args, err := mapScriptArgs(parsed.Args, s.resolveWorkspacePath)
	if err != nil {
		return nil, err
	}
	return s.runPython(ctx, script, args)
}

// resolveScript 在原始 Skill 目录里找脚本并返回原件的绝对路径；工作区里的副本永远不会被执行。
func (s *RestrictedShell) resolveScript(rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(s.skillDir, filepath.FromSlash(rel)))
	if !withinRoot(s.skillDir, candidate) {
		return "", rejectf("脚本必须在 Skill 目录内：%q", rel)
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return "", rejectf("Skill 目录里没有脚本 %q（只能运行 Skill 自带脚本，不能运行工作区里新写的脚本）", rel)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", rejectf("脚本必须是普通文件：%q", rel)
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.skillDir, real) {
		return "", rejectf("脚本经符号链接指向 Skill 目录之外：%q", rel)
	}
	return real, nil
}

// resolveWorkspacePath 校验工作区相对路径：静态规则 + 已存在的最深一级经 EvalSymlinks 后仍在工作区内。
// 返回相对工作区的路径（子进程 cwd 就是工作区）。
func (s *RestrictedShell) resolveWorkspacePath(rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(s.workspace, filepath.FromSlash(rel)))
	if !withinRoot(s.workspace, candidate) {
		return "", rejectf("路径越出工作区：%q", rel)
	}
	existing := candidate
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", rejectf("路径越出工作区：%q", rel)
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.workspace, real) {
		return "", rejectf("路径经符号链接指向工作区之外：%q", rel)
	}
	return filepath.Rel(s.workspace, candidate)
}

func (s *RestrictedShell) runPython(ctx context.Context, script string, args []string) (*filesystem.ExecuteResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	// -E 忽略 PYTHON* 环境变量；-s 不加载用户 site；-B 不写 .pyc（Skill 目录与 .app 都不该被写）；
	// -u 不缓冲；-X utf8 让中文输出在 Windows 上也是 UTF-8。
	argv := append([]string{"-E", "-s", "-B", "-u", "-X", "utf8", script}, args...)
	cmd := exec.CommandContext(runCtx, s.python, argv...)
	cmd.Dir = s.workspace
	cmd.Env = restrictedEnv(s.workspace, s.tmp, filepath.Dir(s.python))
	output := &cappedBuffer{max: maxShellOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 2 * time.Second
	configureChildProcess(cmd)

	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		code := exitTimeout
		return &filesystem.ExecuteResponse{
			Output:    output.String() + fmt.Sprintf("\n脚本运行超过 %s 被终止\n", s.timeout),
			ExitCode:  &code,
			Truncated: output.truncated,
		}, nil
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("运行内置 Python: %w", err)
		}
		exitCode = exitErr.ExitCode()
	}
	return &filesystem.ExecuteResponse{Output: output.String(), ExitCode: &exitCode, Truncated: output.truncated}, nil
}

// restrictedEnv 从零构造子进程环境，绝不继承 os.Environ()（里面有上游 Key、代理等）。
func restrictedEnv(workspace, tmp, pythonBin string) []string {
	env := []string{
		"PATH=" + pythonBin,
		"HOME=" + workspace,
		"USERPROFILE=" + workspace,
		"SKILL_DIR=" + workspace,
		"WORK_DIR=" + workspace,
		"TMPDIR=" + tmp,
		"TEMP=" + tmp,
		"TMP=" + tmp,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	return append(env, platformEnv()...)
}

var _ filesystem.Shell = (*RestrictedShell)(nil)
```

- [ ] **Step 6: 实现 embed_text.go**

```go
package skillruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk/filesystem"
)

const maxEmbedTextBytes = 4 << 20

var jsonFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// embedText 实现 embed-text：小说风格 Skill 要求把 source.txt 原样注入 JSON，
// 白名单不允许 python -c，于是由 sidecar 内置完成。保持已有字段顺序（validate_storyboard.py 会校验顶层顺序）。
func (s *RestrictedShell) embedText(ctx context.Context, args []string) (*filesystem.ExecuteResponse, error) {
	jsonRel, field, textRel := args[0], args[1], args[2]
	if !jsonFieldPattern.MatchString(field) {
		return nil, rejectf("字段名只能由字母、数字、下划线组成：%q", field)
	}
	jsonPath, err := s.resolveWorkspacePath(jsonRel)
	if err != nil {
		return nil, err
	}
	textPath, err := s.resolveWorkspacePath(textRel)
	if err != nil {
		return nil, err
	}
	textAbs := filepath.Join(s.workspace, textPath)
	info, err := os.Stat(textAbs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, rejectf("文本文件不存在：%q", textRel)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxEmbedTextBytes {
		return nil, rejectf("文本文件必须是不超过 4 MiB 的普通文件：%q", textRel)
	}
	text, err := os.ReadFile(textAbs)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(text) {
		return nil, rejectf("文本文件不是 UTF-8：%q", textRel)
	}

	var fields []orderedField
	raw, err := os.ReadFile(filepath.Join(s.workspace, jsonPath))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if fields, err = decodeOrderedObject(raw); err != nil {
			return nil, rejectf("%s 不是 JSON 对象：%v", jsonRel, err)
		}
	}
	value, err := marshalNoEscape(string(text))
	if err != nil {
		return nil, err
	}
	content, err := encodeOrderedObject(setOrderedField(fields, field, value))
	if err != nil {
		return nil, err
	}
	backend, err := NewWorkspaceBackend(s.workspace)
	if err != nil {
		return nil, err
	}
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: jsonPath, Content: content}); err != nil {
		return nil, err
	}
	code := 0
	return &filesystem.ExecuteResponse{
		Output:   fmt.Sprintf("OK: 已把 %s 原样写入 %s 的 %s 字段（%d 字）\n", textRel, jsonRel, field, utf8.RuneCount(text)),
		ExitCode: &code,
	}, nil
}

type orderedField struct {
	Key   string
	Value json.RawMessage
}

func decodeOrderedObject(raw []byte) ([]orderedField, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("顶层必须是对象")
	}
	var fields []orderedField
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, orderedField{Key: key, Value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("对象之后还有多余内容")
	}
	return fields, nil
}

// setOrderedField 替换同名字段（保留第一个位置、删掉重复项）；不存在则追加到末尾。
func setOrderedField(fields []orderedField, key string, value json.RawMessage) []orderedField {
	out := make([]orderedField, 0, len(fields)+1)
	replaced := false
	for _, f := range fields {
		if f.Key != key {
			out = append(out, f)
			continue
		}
		if !replaced {
			out = append(out, orderedField{Key: key, Value: value})
			replaced = true
		}
	}
	if !replaced {
		out = append(out, orderedField{Key: key, Value: value})
	}
	return out
}

func encodeOrderedObject(fields []orderedField) (string, error) {
	var b strings.Builder
	b.WriteString("{\n")
	for i, f := range fields {
		key, err := marshalNoEscape(f.Key)
		if err != nil {
			return "", err
		}
		b.WriteString("  ")
		b.Write(key)
		b.WriteString(": ")
		b.Write(f.Value)
		if i < len(fields)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
```

- [ ] **Step 7: 运行确认通过**

Run: `cd server && go vet ./internal/skillruntime/ && go test ./internal/skillruntime/ -v -run 'Restricted|EmbedText|LocatePython|PythonInterpreter'`
Expected: 全部 PASS（`TestRestrictedShellRejectsPathEscapes` 在 macOS 上含符号链接用例）。

Run: `cd server && GOOS=windows GOARCH=amd64 go vet ./internal/skillruntime/`
Expected: 无输出（Windows 分支可编译）。

- [ ] **Step 8: Commit**

```bash
git add server/internal/skillruntime/{restricted_shell,embed_text,python_runtime,exec_windows,exec_other}.go server/internal/skillruntime/{restricted_shell,python_runtime}_test.go
git commit -m "feat(skillruntime): RestrictedShell——内置 Python 直接执行 Skill 脚本 + 白名单 + embed-text"
```

---

### Task 6: canvas_script_tasks 迁移与 canvasscript 移植

**Files:**
- Create: `server/internal/db/migrations/0060_canvas_script_tasks.sql`
- Create: `server/internal/canvasscript/service.go`
- Test: `server/internal/canvasscript/service_test.go`、`service_db_test.go`

**Interfaces:**
- Consumes: Task 4 的 `skillruntime.Snapshot`、`RunRequest`、`RunResult`、`ModelConfig`；P1a 的 `models.ModelConfig`（字段 `RequestTemplate json.RawMessage`）、`models.ErrNotFound`、`dbtest.Open(t)`。
- Produces（Task 7 依赖）：
  - `canvasscript.NewService(db *sql.DB, settings settingsStore, models modelCatalog, keys keyResolver, skills skillRunner) *Service`；窄接口：`settingsStore{Get(key string) (string, error); Set(key, value string) error}`、`modelCatalog{GetByModelID(modelID string) (*models.ModelConfig, error)}`、`keyResolver{Resolve(userID int64) (string, error)}`、`skillRunner{Snapshot(ctx, name string) (skillruntime.Snapshot, error); Run(ctx, skillruntime.RunRequest) (skillruntime.RunResult, error)}`
  - `(*Service).LoadConfig() (Config, error)`、`SaveConfig(Config) error`（供 P4）
  - `(*Service).EnabledStyles(userTags []string) ([]StyleEntry, []ModelCandidateView, error)`
  - `(*Service).Submit(ctx, userID, projectID int64, nodeID, styleName, script string, durationSec int, model string, userTags []string) (int64, error)`
  - `(*Service).Get(ctx, userID, taskID int64) (*Task, error)`
  - `(*Service).RecoverOnStartup(ctx) error`、`(*Service).RunWorker(ctx)`
  - 常量 `StatusPending/Running/Succeeded/Failed`、`SettingKey`、`MaxScriptRunes`、`MinDurationSec`、`MaxDurationSec`

- [ ] **Step 1: 写迁移**

`server/internal/db/migrations/0060_canvas_script_tasks.sql`：

```sql
-- 自由画布脚本节点“剧本生成分镜”任务表（P6）。列与参考仓 0088 + 0095 + 0105 的最终结构一致。
-- 桌面端没有积分（X3）：frozen_cents 恒写 0，actor_user_id 恒为 NULL，保留列只为与参考仓同构。
-- id 用 AUTOINCREMENT：前端节点长期持有 taskId，删除后复用 id 会让旧节点查到别的任务。
CREATE TABLE canvas_script_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  project_id INTEGER NOT NULL,
  node_id TEXT NOT NULL,
  skill_name TEXT NOT NULL,
  duration_sec INTEGER NOT NULL,
  script TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  result TEXT NULL,
  error TEXT NULL,
  frozen_cents INTEGER NOT NULL DEFAULT 0,
  owner_instance TEXT NULL,
  heartbeat_at DATETIME NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  model TEXT NOT NULL DEFAULT '',
  actor_user_id INTEGER NULL
);

CREATE INDEX idx_cst_status ON canvas_script_tasks (status, updated_at);
CREATE INDEX idx_cst_user ON canvas_script_tasks (user_id, id);

CREATE TRIGGER trg_canvas_script_tasks_updated_at AFTER UPDATE ON canvas_script_tasks
FOR EACH ROW WHEN NEW.updated_at = OLD.updated_at
BEGIN
  UPDATE canvas_script_tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
```

- [ ] **Step 2: 写失败的测试**

`server/internal/canvasscript/service_test.go`（纯函数）：

```go
package canvasscript

import (
	"encoding/json"
	"testing"
)

func TestVisibleTo(t *testing.T) {
	cases := []struct {
		name     string
		entry    StyleEntry
		userTags []string
		want     bool
	}{
		{"空标签全员可见", StyleEntry{}, nil, true},
		{"命中任一标签放行", StyleEntry{UserTags: []string{"vip", "beta"}}, []string{"beta"}, true},
		{"未命中拒绝", StyleEntry{UserTags: []string{"vip"}}, []string{"normal"}, false},
		{"用户无标签且风格限定则拒绝", StyleEntry{UserTags: []string{"vip"}}, nil, false},
	}
	for _, c := range cases {
		if got := c.entry.VisibleTo(c.userTags); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	var zero Config
	if zero.concurrency() != 1 || zero.maxAttempts() != 2 {
		t.Errorf("默认并发 1、共 2 次: conc=%d attempts=%d", zero.concurrency(), zero.maxAttempts())
	}
	if c := (Config{Concurrency: 3, MaxRetries: 2}); c.concurrency() != 3 || c.maxAttempts() != 3 {
		t.Errorf("显式配置未生效: conc=%d attempts=%d", c.concurrency(), c.maxAttempts())
	}
	if bad := (Config{Concurrency: -1, MaxRetries: 99}); bad.concurrency() != 1 || bad.maxAttempts() != 2 {
		t.Errorf("越界值应回默认: conc=%d attempts=%d", bad.concurrency(), bad.maxAttempts())
	}
}

func TestValidateCandidate(t *testing.T) {
	if err := validateCandidate(`{"storyboard":[{"name":"镜1"}]}`); err != nil {
		t.Errorf("合法产物不应报错: %v", err)
	}
	for _, bad := range []string{`not json`, `{"storyboard":[]}`, `{"character":[]}`} {
		if err := validateCandidate(bad); err == nil {
			t.Errorf("%s 应报错", bad)
		}
	}
}

func TestStyleKey(t *testing.T) {
	if k := (StyleEntry{Name: "真人短剧"}).Key(); k != "真人短剧" {
		t.Errorf("无版本 Key=%q", k)
	}
	if k := (StyleEntry{Name: "真人短剧", Version: "v2"}).Key(); k != "真人短剧 v2" {
		t.Errorf("带版本 Key=%q", k)
	}
}

func TestPassthroughModelFields(t *testing.T) {
	got := passthroughModelFields(json.RawMessage(`{"model":"x","messages":"{{messages}}","stream":true,"max_tokens":8000,"prompt":"{{prompt}}","reasoning_effort":"high"}`))
	if len(got) != 2 || got["max_tokens"] != float64(8000) || got["reasoning_effort"] != "high" {
		t.Fatalf("只应透传 max_tokens 与 reasoning_effort: %#v", got)
	}
	if passthroughModelFields(json.RawMessage(`not json`)) != nil || passthroughModelFields(nil) != nil {
		t.Fatal("坏模板或空模板应返回 nil")
	}
}
```

`server/internal/canvasscript/service_db_test.go`：

```go
package canvasscript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

type fakeSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func newFakeSettings(cfg Config) *fakeSettings {
	b, _ := json.Marshal(cfg)
	return &fakeSettings{m: map[string]string{SettingKey: string(b), "base_domain": "https://up.example"}}
}

func (f *fakeSettings) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[key], nil
}

func (f *fakeSettings) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = value
	return nil
}

type fakeModels struct{ missing map[string]bool }

func (f fakeModels) GetByModelID(id string) (*models.ModelConfig, error) {
	if f.missing[id] {
		return nil, models.ErrNotFound
	}
	return &models.ModelConfig{RequestTemplate: json.RawMessage(`{"max_tokens":8000,"messages":"{{messages}}"}`)}, nil
}

type fakeKeys struct{ key string }

func (f fakeKeys) Resolve(int64) (string, error) { return f.key, nil }

type fakeSkills struct {
	mu      sync.Mutex
	snapErr error
	runErr  error
	content string
	runs    []skillruntime.RunRequest
}

func (f *fakeSkills) Snapshot(_ context.Context, name string) (skillruntime.Snapshot, error) {
	if f.snapErr != nil {
		return skillruntime.Snapshot{}, f.snapErr
	}
	return skillruntime.Snapshot{Name: name}, nil
}

func (f *fakeSkills) Run(_ context.Context, req skillruntime.RunRequest) (skillruntime.RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, req)
	if f.runErr != nil {
		return skillruntime.RunResult{}, f.runErr
	}
	return skillruntime.RunResult{Content: f.content}, nil
}

const (
	testModel      = "canvas-gpt-5.5"
	testStyle      = "真人短剧风格 v3"
	validCandidate = `{"storyboard":[{"name":"镜1"}]}`
)

func testConfig() Config {
	return Config{
		ModelCandidates: []ModelCandidate{{ModelID: testModel, DisplayName: "gpt-5.5"}},
		Styles: []StyleEntry{
			{Name: "真人短剧风格", Version: "v3", SkillName: "real-person-short-drama-v3", Enabled: true},
			{Name: "停用风格", SkillName: "x", Enabled: false},
		},
		Concurrency: 1,
		MaxRetries:  1,
	}
}

func newTestService(t *testing.T, cfg Config, sk *fakeSkills, m fakeModels) (*Service, *sql.DB) {
	t.Helper()
	conn := dbtest.Open(t)
	return NewService(conn, newFakeSettings(cfg), m, fakeKeys{key: "sk-test"}, sk), conn
}

func submitOne(t *testing.T, svc *Service) int64 {
	t.Helper()
	id, err := svc.Submit(context.Background(), 1, 7, "node-1", testStyle, "第一场：安岁推门。", 10, testModel, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func taskRow(t *testing.T, conn *sql.DB, id int64) (status, errMsg string, attempts int) {
	t.Helper()
	var e sql.NullString
	if err := conn.QueryRow(`SELECT status, error, attempts FROM canvas_script_tasks WHERE id=?`, id).Scan(&status, &e, &attempts); err != nil {
		t.Fatal(err)
	}
	return status, e.String, attempts
}

func TestSubmitPersistsPendingTask(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	var status, model, skill string
	var frozen, attempts int64
	var actor sql.NullInt64
	err := conn.QueryRow(`SELECT status, model, skill_name, frozen_cents, attempts, actor_user_id FROM canvas_script_tasks WHERE id=?`, id).
		Scan(&status, &model, &skill, &frozen, &attempts, &actor)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusPending || model != testModel || skill != "real-person-short-drama-v3" || frozen != 0 || attempts != 0 || actor.Valid {
		t.Fatalf("status=%s model=%s skill=%s frozen=%d attempts=%d actor=%v", status, model, skill, frozen, attempts, actor)
	}
}

func TestSubmitValidation(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	cases := []struct {
		name, style, script string
		dur                 int
		model, want         string
	}{
		{"空剧本", testStyle, "  ", 10, testModel, "剧本内容不能为空"},
		{"超长", testStyle, strings.Repeat("字", MaxScriptRunes+1), 10, testModel, "剧本最长"},
		{"时长过短", testStyle, "x", 3, testModel, "时长需在"},
		{"时长过长", testStyle, "x", 16, testModel, "时长需在"},
		{"未选模型", testStyle, "x", 10, "", "请选择模型"},
		{"模型不在候选", testStyle, "x", 10, "other", "所选模型不可用"},
		{"风格不存在", "不存在 v9", "x", 10, testModel, "所选风格不存在或已停用"},
		{"风格停用", "停用风格", "x", 10, testModel, "所选风格不存在或已停用"},
	}
	for _, c := range cases {
		_, err := svc.Submit(context.Background(), 1, 7, "n", c.style, c.script, c.dur, c.model, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v want %q", c.name, err, c.want)
		}
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM canvas_script_tasks`).Scan(&n)
	if n != 0 {
		t.Fatalf("校验失败不应落库，got %d 行", n)
	}
}

func TestSubmitRejectsMissingModelAndSkill(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{missing: map[string]bool{testModel: true}})
	if _, err := svc.Submit(context.Background(), 1, 7, "n", testStyle, "x", 10, testModel, nil); err == nil || !strings.Contains(err.Error(), "所选模型不可用") {
		t.Fatalf("模型中心查不到应拒绝: %v", err)
	}
	svc2, _ := newTestService(t, testConfig(), &fakeSkills{snapErr: errors.New("no such skill")}, fakeModels{})
	if _, err := svc2.Submit(context.Background(), 1, 7, "n", testStyle, "x", 10, testModel, nil); err == nil || !strings.Contains(err.Error(), "风格对应的 skill 不可用") {
		t.Fatalf("skill 不存在应拒绝: %v", err)
	}
}

// 桌面端没有积分（X3）：零价模型必须保留，只剔除模型中心查不到的候选。
func TestEnabledStylesKeepsZeroPriceCandidates(t *testing.T) {
	cfg := testConfig()
	cfg.ModelCandidates = append(cfg.ModelCandidates, ModelCandidate{ModelID: "deleted-model", DisplayName: "已删除"})
	svc, _ := newTestService(t, cfg, &fakeSkills{}, fakeModels{missing: map[string]bool{"deleted-model": true}})
	styles, cands, err := svc.EnabledStyles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(styles) != 1 || styles[0].Key() != testStyle {
		t.Fatalf("只应返回启用的风格: %#v", styles)
	}
	if len(cands) != 1 || cands[0].ModelID != testModel || cands[0].Credits != 0 {
		t.Fatalf("零价候选应保留、已删除的剔除: %#v", cands)
	}
}

func TestClaimOneConcurrentSingleWinner(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := svc.claimOne(context.Background()); ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("应恰好一个抢到，got %d", wins)
	}
	status, _, attempts := taskRow(t, conn, id)
	var owner string
	_ = conn.QueryRow(`SELECT owner_instance FROM canvas_script_tasks WHERE id=?`, id).Scan(&owner)
	if status != StatusRunning || attempts != 1 || owner != svc.instanceID {
		t.Fatalf("status=%s attempts=%d owner=%s", status, attempts, owner)
	}
}

func TestExecuteSuccessStoresResult(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, _ := newTestService(t, testConfig(), sk, fakeModels{})
	id := submitOne(t, svc)
	task, ok := svc.claimOne(context.Background())
	if !ok {
		t.Fatal("应抢到任务")
	}
	svc.execute(context.Background(), task, testConfig())
	got, err := svc.Get(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusSucceeded || got.Result != validCandidate {
		t.Fatalf("status=%s result=%s", got.Status, got.Result)
	}
	req := sk.runs[0]
	if !strings.Contains(req.Input, "DURATION_SEC=10") || req.SourceText != "第一场：安岁推门。" {
		t.Fatalf("Input/SourceText 不对: %#v", req)
	}
	if req.Model.BaseURL != "https://up.example" || req.Model.APIKey != "sk-test" || req.Model.Model != testModel {
		t.Fatalf("模型配置不对: %#v", req.Model)
	}
	if _, ok := req.Model.ExtraFields["messages"]; ok || req.Model.ExtraFields["max_tokens"] != float64(8000) {
		t.Fatalf("ExtraFields 透传不对: %#v", req.Model.ExtraFields)
	}
}

func TestExecuteFailRequeuesThenFails(t *testing.T) {
	sk := &fakeSkills{runErr: errors.New("上游 500")}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{})
	id := submitOne(t, svc)
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, _ := taskRow(t, conn, id); status != StatusPending || !strings.Contains(msg, "生成失败") {
		t.Fatalf("第 1 次失败应重排: status=%s err=%s", status, msg)
	}
	task, _ = svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, attempts := taskRow(t, conn, id); status != StatusFailed || attempts != 2 || !strings.Contains(msg, "上游 500") {
		t.Fatalf("用尽重试应判失败: status=%s attempts=%d err=%s", status, attempts, msg)
	}
}

func TestExecuteBadCandidateFails(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{content: `{"storyboard":[]}`}, fakeModels{})
	id := submitOne(t, svc)
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if _, msg, _ := taskRow(t, conn, id); !strings.Contains(msg, "产物校验失败") {
		t.Fatalf("err=%s", msg)
	}
}

func TestExecuteEmptyModelFailsImmediately(t *testing.T) {
	sk := &fakeSkills{content: validCandidate}
	svc, conn := newTestService(t, testConfig(), sk, fakeModels{})
	res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model) VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', '')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	task, _ := svc.claimOne(context.Background())
	svc.execute(context.Background(), task, testConfig())
	if status, msg, _ := taskRow(t, conn, id); status != StatusFailed || msg != "任务未记录模型，请重新提交" || len(sk.runs) != 0 {
		t.Fatalf("status=%s err=%s runs=%d", status, msg, len(sk.runs))
	}
}

func TestRecoverOnStartup(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	insert := func(attempts int) int64 {
		res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model, status, attempts, owner_instance, heartbeat_at)
			VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', ?, 'running', ?, 'old-process', CURRENT_TIMESTAMP)`, testModel, attempts)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	retryable, exhausted := insert(1), insert(2)
	if err := svc.RecoverOnStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var owner sql.NullString
	_ = conn.QueryRow(`SELECT owner_instance FROM canvas_script_tasks WHERE id=?`, retryable).Scan(&owner)
	if status, _, _ := taskRow(t, conn, retryable); status != StatusPending || owner.Valid {
		t.Fatalf("未用尽重试应回到 pending 并清租约: status=%s owner=%v", status, owner)
	}
	if status, msg, _ := taskRow(t, conn, exhausted); status != StatusFailed || !strings.Contains(msg, "上次运行中断") {
		t.Fatalf("用尽重试应判失败: status=%s err=%s", status, msg)
	}
}

func TestGetOnlyReturnsOwnTask(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	if _, err := svc.Get(context.Background(), 2, id); err == nil || err.Error() != "任务不存在" {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskIDsNeverReused(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	first := submitOne(t, svc)
	if _, err := conn.Exec(`DELETE FROM canvas_script_tasks WHERE id=?`, first); err != nil {
		t.Fatal(err)
	}
	if second := submitOne(t, svc); second <= first {
		t.Fatalf("删除后 id 不应复用: first=%d second=%d", first, second)
	}
}

func TestUpdatedAtTrigger(t *testing.T) {
	svc, conn := newTestService(t, testConfig(), &fakeSkills{}, fakeModels{})
	id := submitOne(t, svc)
	if _, err := conn.Exec(`UPDATE canvas_script_tasks SET updated_at='2000-01-01 00:00:00' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE canvas_script_tasks SET status='running' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var updated string
	_ = conn.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM canvas_script_tasks WHERE id=?`, id).Scan(&updated)
	if strings.HasPrefix(updated, "2000-") {
		t.Fatalf("状态变更应刷新 updated_at: %s", updated)
	}
}

func TestRunWorkerProcessesPendingTask(t *testing.T) {
	svc, _ := newTestService(t, testConfig(), &fakeSkills{content: validCandidate}, fakeModels{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.RunWorker(ctx)
	id := submitOne(t, svc)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Get(context.Background(), 1, id)
		if err == nil && got.Status == StatusSucceeded {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker 应在一轮抢占内跑完任务")
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd server && go test ./internal/canvasscript/`
Expected: FAIL（`undefined: NewService` 等）

- [ ] **Step 4: 实现 service.go**

`server/internal/canvasscript/service.go`：

```go
// Package canvasscript 自由画布脚本节点的“剧本生成分镜”：用户贴剧本 + 选风格（skill）+ 主切割时长
// → 异步跑 Skill → 产出 candidate.json 存任务表，前端轮询拿到后自行导入节点。
//
// 桌面端相对参考仓的改动：没有积分（X3），去掉 billing 依赖与零价闸门；单进程，
// 去掉 lifecycle 实例租约与心跳，启动时把上个进程遗留的 running 一次性回收（RecoverOnStartup）。
package canvasscript

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

const (
	SettingKey = "canvas_script_config"

	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	MinDurationSec = 4
	MaxDurationSec = 15

	// MaxScriptRunes 剧本长度上限（按字符数），前端同值拦截，这里是权威一道。
	MaxScriptRunes = 5000
)

// StyleEntry 一个可选风格：展示名 + 版本 + skills 目录名。Enabled=false 保留但用户不可见。
type StyleEntry struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	SkillName string   `json:"skillName"`
	Enabled   bool     `json:"enabled"`
	UserTags  []string `json:"userTags"`
}

// Key 用户侧风格标识："name" 或 "name version"。
func (e StyleEntry) Key() string {
	if e.Version != "" {
		return e.Name + " " + e.Version
	}
	return e.Name
}

// VisibleTo 标签任一命中即可见；空标签全员可见。
func (e StyleEntry) VisibleTo(tags []string) bool {
	if len(e.UserTags) == 0 {
		return true
	}
	for _, want := range e.UserTags {
		for _, t := range tags {
			if t == want {
				return true
			}
		}
	}
	return false
}

// ModelCandidate 可选 chat 模型：模型中心 modelId + 展示名。列表第一项为前端默认。
type ModelCandidate struct {
	ModelID     string `json:"modelId"`
	DisplayName string `json:"displayName"`
}

// ModelCandidateView 下发前端的候选。Credits 恒为 0：桌面端没有积分（X3），字段只为前端兼容保留。
type ModelCandidateView struct {
	ModelID     string `json:"modelId"`
	DisplayName string `json:"displayName"`
	Credits     int64  `json:"credits"`
}

// Config “设置 → 脚本设置”的完整配置。
type Config struct {
	ModelCandidates []ModelCandidate `json:"modelCandidates"`
	Styles          []StyleEntry     `json:"styles"`
	Concurrency     int              `json:"concurrency"`
	MaxRetries      int              `json:"maxRetries"`
}

func (c Config) concurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return 1
}

func (c Config) maxAttempts() int {
	if c.MaxRetries >= 1 && c.MaxRetries <= 10 {
		return c.MaxRetries + 1
	}
	return 2
}

type Task struct {
	ID          int64
	UserID      int64
	ProjectID   int64
	NodeID      string
	SkillName   string
	DurationSec int
	Script      string
	Model       string
	Status      string
	Result      string
	Error       string
	Attempts    int64
	UpdatedAt   time.Time
}

type settingsStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// modelCatalog 即 models.SQLiteRepository 的窄面。
type modelCatalog interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// keyResolver 解析上游 Key；桌面端由装配处读 settings.upstream_api_key。
type keyResolver interface {
	Resolve(userID int64) (string, error)
}

type skillRunner interface {
	Snapshot(ctx context.Context, name string) (skillruntime.Snapshot, error)
	Run(ctx context.Context, req skillruntime.RunRequest) (skillruntime.RunResult, error)
}

type Service struct {
	db         *sql.DB
	settings   settingsStore
	models     modelCatalog
	keys       keyResolver
	skills     skillRunner
	inflight   atomic.Int32
	instanceID string // 本进程随机 id，写入 owner_instance 便于排查
}

func NewService(db *sql.DB, settings settingsStore, models modelCatalog, keys keyResolver, skills skillRunner) *Service {
	return &Service{db: db, settings: settings, models: models, keys: keys, skills: skills, instanceID: newInstanceID()}
}

func newInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoadConfig 读配置；未配置返回零值（风格列表为空 = 入口不可用）。
func (s *Service) LoadConfig() (Config, error) {
	raw, err := s.settings.Get(SettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("canvas_script_config 解析: %w", err)
	}
	return cfg, nil
}

func (s *Service) SaveConfig(cfg Config) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settings.Set(SettingKey, string(b))
}

// EnabledStyles 用户可见的风格 + 可选模型。模型中心查不到的候选剔除（配错 / 被删）；
// 不再按估价剔除——桌面端所有模型都是零价，参考仓的 credits<=0 闸门会把候选剔光。
func (s *Service) EnabledStyles(userTags []string) ([]StyleEntry, []ModelCandidateView, error) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	out := make([]StyleEntry, 0, len(cfg.Styles))
	for _, st := range cfg.Styles {
		if st.Enabled && st.VisibleTo(userTags) {
			out = append(out, st)
		}
	}
	cands := make([]ModelCandidateView, 0, len(cfg.ModelCandidates))
	for _, c := range cfg.ModelCandidates {
		if _, err := s.models.GetByModelID(c.ModelID); err != nil {
			log.Printf("canvasscript: 候选模型 %q 在模型中心不存在（%v），已剔除", c.ModelID, err)
			continue
		}
		cands = append(cands, ModelCandidateView{ModelID: c.ModelID, DisplayName: c.DisplayName})
	}
	return out, cands, nil
}

// Submit 校验并落任务，返回任务 id。
func (s *Service) Submit(ctx context.Context, userID, projectID int64, nodeID, styleName, script string, durationSec int, model string, userTags []string) (int64, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return 0, errors.New("剧本内容不能为空")
	}
	if utf8.RuneCountInString(script) > MaxScriptRunes {
		return 0, fmt.Errorf("剧本最长 %d 字", MaxScriptRunes)
	}
	if durationSec < MinDurationSec || durationSec > MaxDurationSec {
		return 0, fmt.Errorf("时长需在 %d-%d 秒", MinDurationSec, MaxDurationSec)
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return 0, err
	}
	if model == "" {
		return 0, errors.New("请选择模型")
	}
	inList := false
	for _, c := range cfg.ModelCandidates {
		if c.ModelID == model {
			inList = true
			break
		}
	}
	if !inList {
		return 0, errors.New("所选模型不可用")
	}
	// 二次校验：堵“拉完候选之后、提交之前模型被删”的竞态。
	if _, err := s.models.GetByModelID(model); err != nil {
		return 0, errors.New("所选模型不可用")
	}
	var style *StyleEntry
	for i := range cfg.Styles {
		if cfg.Styles[i].Key() == styleName && cfg.Styles[i].Enabled && cfg.Styles[i].VisibleTo(userTags) {
			style = &cfg.Styles[i]
			break
		}
	}
	if style == nil {
		return 0, errors.New("所选风格不存在或已停用")
	}
	if _, err := s.skills.Snapshot(ctx, style.SkillName); err != nil {
		return 0, fmt.Errorf("风格对应的 skill 不可用: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, projectID, nodeID, style.SkillName, durationSec, script, model)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get 查任务（只允许提交者本人查）。
func (s *Service) Get(ctx context.Context, userID, taskID int64) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, project_id, node_id, skill_name, duration_sec, model, status,
		       COALESCE(result,''), COALESCE(error,''), attempts, updated_at
		FROM canvas_script_tasks WHERE id=? AND user_id=?`, taskID, userID)
	var t Task
	if err := row.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.NodeID, &t.SkillName, &t.DurationSec, &t.Model,
		&t.Status, &t.Result, &t.Error, &t.Attempts, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("任务不存在")
		}
		return nil, err
	}
	return &t, nil
}

const tickInterval = 2 * time.Second

// RecoverOnStartup 单进程：启动时遗留的 running 一定属于已退出的上个进程，不必等心跳超时。
// 未用尽重试的回到 pending；用尽的判失败。
func (s *Service) RecoverOnStartup(ctx context.Context) error {
	cfg, _ := s.LoadConfig()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE canvas_script_tasks SET status='pending', owner_instance=NULL, heartbeat_at=NULL
		WHERE status='running' AND attempts < ?`, cfg.maxAttempts()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE canvas_script_tasks SET status='failed', error='上次运行中断且重试次数用尽，请重新提交'
		WHERE status='running'`); err != nil {
		return err
	}
	return tx.Commit()
}

// RunWorker 常驻循环：每 2 秒在并发额度内抢占 pending 任务。ctx 取消即退出。
func (s *Service) RunWorker(ctx context.Context) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.claimAndRun(ctx)
		}
	}
}

func (s *Service) claimAndRun(ctx context.Context) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return
	}
	for int(s.inflight.Load()) < cfg.concurrency() {
		t, ok := s.claimOne(ctx)
		if !ok {
			return
		}
		s.inflight.Add(1)
		go func(task *Task) {
			defer s.inflight.Add(-1)
			s.execute(ctx, task, cfg)
		}(t)
	}
}

// claimOne 用一条 UPDATE … RETURNING 原子抢占最早的 pending（替代参考仓的 SELECT + CAS 两步）。
func (s *Service) claimOne(ctx context.Context) (*Task, bool) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE canvas_script_tasks
		SET status='running', owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP, attempts=attempts+1
		WHERE id = (SELECT id FROM canvas_script_tasks WHERE status='pending' ORDER BY id LIMIT 1)
		  AND status='pending'
		RETURNING id, user_id, project_id, node_id, skill_name, duration_sec, script, model, attempts`, s.instanceID)
	var t Task
	if err := row.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.NodeID, &t.SkillName, &t.DurationSec, &t.Script, &t.Model, &t.Attempts); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("canvasscript: 抢占任务失败: %v", err)
		}
		return nil, false
	}
	return &t, true
}

func (s *Service) execute(ctx context.Context, t *Task, cfg Config) {
	// 失败分流：还有重试额度 → 重排 pending；用尽 → failed。
	fail := func(msg string) {
		if int(t.Attempts) < cfg.maxAttempts() {
			log.Printf("canvasscript: task=%d attempt=%d 失败将重试: %s", t.ID, t.Attempts, msg)
			_, _ = s.db.ExecContext(ctx, `
				UPDATE canvas_script_tasks SET status='pending', owner_instance=NULL, heartbeat_at=NULL, error=?
				WHERE id=? AND status='running'`, msg, t.ID)
			return
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE canvas_script_tasks SET status='failed', error=? WHERE id=?`, msg, t.ID)
	}

	if t.Model == "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE canvas_script_tasks SET status='failed', error=? WHERE id=?`,
			"任务未记录模型，请重新提交", t.ID)
		return
	}
	snapshot, err := s.skills.Snapshot(ctx, t.SkillName)
	if err != nil {
		fail("skill 不可用: " + err.Error())
		return
	}
	baseDomain, err := s.settings.Get("base_domain")
	if err != nil || strings.TrimSpace(baseDomain) == "" {
		fail("base_domain 未配置")
		return
	}
	sk, err := s.keys.Resolve(t.UserID)
	if err != nil || strings.TrimSpace(sk) == "" {
		fail("上游 API Key 未配置")
		return
	}

	input := fmt.Sprintf("使用skill %s\n\nDURATION_SEC=%d\n\n内容：\n%s", t.SkillName, t.DurationSec, t.Script)
	result, err := s.skills.Run(ctx, skillruntime.RunRequest{
		Skill:      snapshot,
		Model:      s.skillModelConfig(baseDomain, sk, t.Model),
		Input:      input,
		SourceText: t.Script,
	})
	if err != nil {
		fail("生成失败: " + err.Error())
		return
	}
	if err := validateCandidate(result.Content); err != nil {
		fail("产物校验失败: " + err.Error())
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE canvas_script_tasks SET status='succeeded', result=? WHERE id=?`, result.Content, t.ID); err != nil {
		log.Printf("canvasscript: 写结果失败 task=%d: %v", t.ID, err)
	}
}

// skillModelConfig 把模型中心那行的请求模板整份透传给上游（与参考仓一致）。
func (s *Service) skillModelConfig(baseDomain, sk, modelName string) skillruntime.ModelConfig {
	var template json.RawMessage
	if m, err := s.models.GetByModelID(modelName); err == nil && m != nil {
		template = m.RequestTemplate
	}
	return skillruntime.ModelConfig{
		BaseURL:     baseDomain,
		APIKey:      sk,
		Model:       modelName,
		ExtraFields: passthroughModelFields(template),
	}
}

// passthroughModelFields 从请求模板挑出可直接发给上游的字段：剔掉 runtime 自己管的
// model / messages / stream，以及值形如 {{x}} 的插值占位。
func passthroughModelFields(template json.RawMessage) map[string]any {
	if len(template) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(template, &raw); err != nil {
		log.Printf("canvasscript: 请求模板解析失败，跳过参数透传: %v", err)
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch k {
		case "model", "messages", "stream":
			continue
		}
		if str, ok := v.(string); ok {
			if trimmed := strings.TrimSpace(str); strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
				continue
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateCandidate 产物形状把关：必须是含非空 storyboard 数组的 JSON 对象。
func validateCandidate(content string) error {
	var doc struct {
		Storyboard []json.RawMessage `json:"storyboard"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("非法 JSON: %w", err)
	}
	if len(doc.Storyboard) == 0 {
		return errors.New("storyboard 为空")
	}
	return nil
}
```

- [ ] **Step 5: 运行确认通过**

Run: `cd server && go vet ./internal/canvasscript/ && go test ./internal/canvasscript/ ./internal/db/... -v -count=1`
Expected: 全部 PASS（`TestUpdatedAtTrigger` 验证触发器；`TestClaimOneConcurrentSingleWinner` 无 `SQLITE_BUSY`）。

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/migrations/0060_canvas_script_tasks.sql server/internal/canvasscript
git commit -m "feat(server): canvas_script_tasks 表与 canvasscript 服务（无计费、单进程启动回收）"
```

---

### Task 7: HTTP 接口与 sidecar 装配

**Files:**
- Create: `server/internal/http/canvas_script_handlers.go`
- Test: `server/internal/http/canvas_script_handlers_test.go`
- Modify: `server/internal/http/router.go`
- Create: `server/cmd/sidecar/canvas_script.go`
- Test: `server/cmd/sidecar/canvas_script_test.go`
- Modify: `server/cmd/sidecar/main.go`

**Interfaces:**
- Consumes: Task 6 的 `canvasscript.NewService` 等；Task 5 的 `skillruntime.LocatePython`、`RestrictedShellFactory`、`RestrictedShellConfig`；Task 4 的 `NewCatalog`、`NewRunWorkspace`、`NewWorkspaceCleaner`、`NewRuntime`；Task 2 的 `skillbundle.Extract`；P1a 的 `db.Open`、`db.Migrate`、`settings.NewSQLiteRepository`、`models.NewSQLiteRepository`、`dbtest.Open`。
- Produces：
  - 路由：`GET /api/v1/canvas-script/styles` → `{"styles":[{"name","version"}],"modelCandidates":[{"modelId","displayName","credits":0}]}`；`POST /api/v1/canvas-script-tasks`（body `{projectId,nodeId,style,durationSec,script,model}`）→ `{"taskId","status":"pending"}`；`GET /api/v1/canvas-script-tasks/{taskID}` → `{"status","nodeId","durationSec","result"?, "error"?}`。错误统一 `{"error": "…"}`。
  - `apphttp.NewCanvasScriptHandler(svc *canvasscript.Service) *CanvasScriptHandler`；`Deps.CanvasScript *CanvasScriptHandler`（nil 时不注册路由）。
  - `wireCanvasScript(ctx context.Context, conn *sql.DB, dataDir, pythonHome string) (*apphttp.CanvasScriptHandler, error)`
  - sidecar 新参数 `--python-home`；数据目录下新增 `runtime/skills/`、`runtime/skill-runs/`。

- [ ] **Step 1: 写失败的 handler 测试**

`server/internal/http/canvas_script_handlers_test.go`：

```go
package http

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasscript"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

type csFakeModels struct{}

func (csFakeModels) GetByModelID(string) (*models.ModelConfig, error) { return &models.ModelConfig{}, nil }

type csFakeKeys struct{}

func (csFakeKeys) Resolve(int64) (string, error) { return "sk", nil }

type csFakeSkills struct{}

func (csFakeSkills) Snapshot(_ context.Context, name string) (skillruntime.Snapshot, error) {
	return skillruntime.Snapshot{Name: name}, nil
}
func (csFakeSkills) Run(context.Context, skillruntime.RunRequest) (skillruntime.RunResult, error) {
	return skillruntime.RunResult{}, nil
}

func newCanvasScriptRouter(t *testing.T) (nethttp.Handler, *canvasscript.Service) {
	t.Helper()
	conn := dbtest.Open(t)
	st := settings.NewSQLiteRepository(conn)
	svc := canvasscript.NewService(conn, st, csFakeModels{}, csFakeKeys{}, csFakeSkills{})
	if err := svc.SaveConfig(canvasscript.Config{
		ModelCandidates: []canvasscript.ModelCandidate{{ModelID: "canvas-gpt-5.5", DisplayName: "gpt-5.5"}},
		Styles:          []canvasscript.StyleEntry{{Name: "真人短剧风格", Version: "v3", SkillName: "real-person-short-drama-v3", Enabled: true}},
	}); err != nil {
		t.Fatal(err)
	}
	h := NewCanvasScriptHandler(svc)
	r := chi.NewRouter()
	r.Get("/api/v1/canvas-script/styles", h.Styles)
	r.Post("/api/v1/canvas-script-tasks", h.Submit)
	r.Get("/api/v1/canvas-script-tasks/{taskID}", h.Status)
	return r, svc
}

func doJSON(t *testing.T, h nethttp.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestCanvasScriptStylesHidesSkillName(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	code, out := doJSON(t, h, "GET", "/api/v1/canvas-script/styles", "")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "real-person-short-drama-v3") {
		t.Fatalf("skillName 不应下发: %s", raw)
	}
	cands := out["modelCandidates"].([]any)
	if len(cands) != 1 || cands[0].(map[string]any)["credits"] != float64(0) {
		t.Fatalf("候选应保留且 credits=0: %s", raw)
	}
}

func TestCanvasScriptSubmitAndPollStatus(t *testing.T) {
	h, svc := newCanvasScriptRouter(t)
	code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks",
		`{"projectId":7,"nodeId":"node-1","style":"真人短剧风格 v3","durationSec":10,"script":"第一场","model":"canvas-gpt-5.5"}`)
	if code != 200 || out["status"] != "pending" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	id := int64(out["taskId"].(float64))
	path := "/api/v1/canvas-script-tasks/" + strconv.FormatInt(id, 10)
	if code, out = doJSON(t, h, "GET", path, ""); code != 200 || out["status"] != "pending" || out["nodeId"] != "node-1" {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if _, ok := out["result"]; ok {
		t.Fatal("未成功时不应带 result")
	}
	_ = svc // 结果写入由 worker 完成，这里只验证轮询形状
}

func TestCanvasScriptSubmitValidation(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	if code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks", `{"projectId":7,"nodeId":"n","style":"真人短剧风格 v3"}`); code != 400 || out["error"] == nil {
		t.Fatalf("缺 model 应 400: %d %v", code, out)
	}
	if code, _ := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks", `not json`); code != 400 {
		t.Fatalf("坏 body 应 400: %d", code)
	}
	if code, out := doJSON(t, h, "POST", "/api/v1/canvas-script-tasks",
		`{"projectId":7,"nodeId":"n","style":"真人短剧风格 v3","durationSec":99,"script":"x","model":"canvas-gpt-5.5"}`); code != 400 || !strings.Contains(out["error"].(string), "时长") {
		t.Fatalf("业务校验错误应 400 并带原因: %d %v", code, out)
	}
}

func TestCanvasScriptStatusNotFound(t *testing.T) {
	h, _ := newCanvasScriptRouter(t)
	if code, _ := doJSON(t, h, "GET", "/api/v1/canvas-script-tasks/999", ""); code != 404 {
		t.Fatalf("code=%d", code)
	}
	if code, _ := doJSON(t, h, "GET", "/api/v1/canvas-script-tasks/abc", ""); code != 400 {
		t.Fatalf("code=%d", code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/http/ -run CanvasScript`
Expected: FAIL（`undefined: NewCanvasScriptHandler`）

- [ ] **Step 3: 实现 handler**

`server/internal/http/canvas_script_handlers.go`：

```go
package http

import (
	"encoding/json"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasscript"
)

// canvasScriptUserID 桌面端固定本地用户（P1a 迁移预置 users.id=1）。
// 合并 P1b 的本地身份中间件后可改用 userIDFromContext，行为不变。
const canvasScriptUserID int64 = 1

// CanvasScriptHandler 脚本节点“剧本生成分镜”的用户侧接口：风格列表、提交、轮询。
// 后台“脚本设置”（读写 canvas_script_config）归 P4，直接调用 Service.LoadConfig / SaveConfig。
type CanvasScriptHandler struct {
	svc *canvasscript.Service
}

func NewCanvasScriptHandler(svc *canvasscript.Service) *CanvasScriptHandler {
	return &CanvasScriptHandler{svc: svc}
}

func canvasScriptError(w nethttp.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func (h *CanvasScriptHandler) Styles(w nethttp.ResponseWriter, _ *nethttp.Request) {
	styles, cands, err := h.svc.EnabledStyles(nil)
	if err != nil {
		canvasScriptError(w, nethttp.StatusInternalServerError, err.Error())
		return
	}
	type styleOut struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	out := make([]styleOut, 0, len(styles))
	for _, s := range styles {
		// skillName 是内部映射，不下发。
		out = append(out, styleOut{Name: s.Name, Version: s.Version})
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"styles": out, "modelCandidates": cands})
}

type canvasScriptSubmitBody struct {
	ProjectID   int64  `json:"projectId"`
	NodeID      string `json:"nodeId"`
	Style       string `json:"style"`
	DurationSec int    `json:"durationSec"`
	Script      string `json:"script"`
	Model       string `json:"model"`
}

func (h *CanvasScriptHandler) Submit(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body canvasScriptSubmitBody
	r.Body = nethttp.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		canvasScriptError(w, nethttp.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.NodeID == "" || body.Style == "" || body.Model == "" {
		canvasScriptError(w, nethttp.StatusBadRequest, "projectId/nodeId/style/model required")
		return
	}
	taskID, err := h.svc.Submit(r.Context(), canvasScriptUserID, body.ProjectID, body.NodeID, body.Style, body.Script, body.DurationSec, body.Model, nil)
	if err != nil {
		canvasScriptError(w, nethttp.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"taskId": taskID, "status": canvasscript.StatusPending})
}

func (h *CanvasScriptHandler) Status(w nethttp.ResponseWriter, r *nethttp.Request) {
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil {
		canvasScriptError(w, nethttp.StatusBadRequest, "invalid taskID")
		return
	}
	t, err := h.svc.Get(r.Context(), canvasScriptUserID, taskID)
	if err != nil {
		canvasScriptError(w, nethttp.StatusNotFound, err.Error())
		return
	}
	out := map[string]any{"status": t.Status, "nodeId": t.NodeID, "durationSec": t.DurationSec}
	if t.Status == canvasscript.StatusSucceeded {
		out["result"] = t.Result
	}
	if t.Status == canvasscript.StatusFailed {
		out["error"] = t.Error
	}
	writeJSON(w, nethttp.StatusOK, out)
}
```

- [ ] **Step 4: 注册路由**

`server/internal/http/router.go`：`Deps` 增加字段

```go
	// CanvasScript 脚本节点接口（P6）；nil 时不注册，便于只测其他路由。
	CanvasScript *CanvasScriptHandler
```

`NewRouter` 中 diag 路由之后、`return r` 之前加：

```go
	if d.CanvasScript != nil {
		r.Get("/api/v1/canvas-script/styles", d.CanvasScript.Styles)
		r.Post("/api/v1/canvas-script-tasks", d.CanvasScript.Submit)
		r.Get("/api/v1/canvas-script-tasks/{taskID}", d.CanvasScript.Status)
	}
```

Run: `cd server && go test ./internal/http/ -v`
Expected: 全部 PASS（含原有 router 测试）。

- [ ] **Step 5: 写失败的装配测试**

`server/cmd/sidecar/canvas_script_test.go`：

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db"
)

func TestWireCanvasScriptExtractsSkillsAndRecoversRunningTasks(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "Application Support", "com.miaoju.desktop")
	conn, err := db.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	res, err := conn.Exec(`INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model, status, attempts, owner_instance)
		VALUES (1, 7, 'n', 'real-person-short-drama-v3', 10, '剧本', 'canvas-gpt-5.5', 'running', 1, 'old')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := wireCanvasScript(ctx, conn, dataDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("handler 不应为 nil")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runtime", "skills", "real-person-short-drama-v3", "scripts", "validate_storyboard.py")); err != nil {
		t.Fatalf("内置 Skill 应释放到数据目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "runtime", "skill-runs")); err != nil {
		t.Fatalf("Run 工作区根目录应已创建: %v", err)
	}
	var status string
	if err := conn.QueryRow(`SELECT status FROM canvas_script_tasks WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("上个进程遗留的 running 应回到 pending，got %s", status)
	}
}
```

Run: `cd server && go test ./cmd/sidecar/ -run WireCanvasScript`
Expected: FAIL（`undefined: wireCanvasScript`）

- [ ] **Step 6: 实现装配**

`server/cmd/sidecar/canvas_script.go`：

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"

	"github.com/james/huabu_studio/apps/api/internal/canvasscript"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/settings"
	"github.com/james/huabu_studio/apps/api/internal/skillbundle"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

// skillMaxIterations 与参考仓 DRAMA_AGENT_MAX_ITERATIONS 默认值一致（重流程 skill 正常就近 20 轮）。
const skillMaxIterations = 40

// wireCanvasScript 装配脚本节点（P6）：释放内置 Skill、找内置 Python、启动回收与 worker。
// 找不到 Python 不阻止启动，只打印警告；任务执行时以“内置 Python 运行时缺失”失败。
func wireCanvasScript(ctx context.Context, conn *sql.DB, dataDir, pythonHome string) (*apphttp.CanvasScriptHandler, error) {
	runtimeDir := filepath.Join(dataDir, "runtime")
	skillsDir, err := skillbundle.Extract(filepath.Join(runtimeDir, "skills"))
	if err != nil {
		return nil, err
	}
	catalog, err := skillruntime.NewCatalog(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("加载内置 Skill: %w", err)
	}
	workspace, err := skillruntime.NewRunWorkspace(filepath.Join(runtimeDir, "skill-runs"))
	if err != nil {
		return nil, fmt.Errorf("创建 Skill Run 工作区: %w", err)
	}
	go skillruntime.NewWorkspaceCleaner(workspace.Root()).Run(ctx)

	python, err := skillruntime.LocatePython(pythonHome)
	if err != nil {
		log.Printf("警告: %v；脚本节点任务将失败", err)
	} else {
		log.Printf("内置 Python: %s", python)
	}
	shellFactory := skillruntime.RestrictedShellFactory(skillruntime.RestrictedShellConfig{Python: python})
	rt := skillruntime.NewRuntime(catalog, workspace, nil, skillMaxIterations, shellFactory)

	settingsRepo := settings.NewSQLiteRepository(conn)
	svc := canvasscript.NewService(conn, settingsRepo, models.NewSQLiteRepository(conn), settingsKeyResolver{settingsRepo}, rt)
	if err := svc.RecoverOnStartup(ctx); err != nil {
		return nil, fmt.Errorf("回收上次未完成的脚本任务: %w", err)
	}
	go svc.RunWorker(ctx)
	return apphttp.NewCanvasScriptHandler(svc), nil
}

// settingsKeyResolver 桌面端上游 Key 就是 settings.upstream_api_key（p1-research §3）。
// P1c 若已提供 UpstreamKeyResolver，合并时替换为它。
type settingsKeyResolver struct {
	s interface{ Get(string) (string, error) }
}

func (r settingsKeyResolver) Resolve(int64) (string, error) { return r.s.Get("upstream_api_key") }
```

`server/cmd/sidecar/main.go` 修改（若合并时其他计划已加入等价的 `db.Open` / `Migrate` / `ctx`，只保留一份）：

1）import 增加 `"context"`、`"path/filepath"`、`"github.com/james/huabu_studio/apps/api/internal/db"`。

2）flag 定义处增加：

```go
	pythonHome := flag.String("python-home", "", "内置 Python 目录（缺省按 sidecar 位置查找）")
```

3）在 `readToken` 成功之后、`net.Listen` 之前插入：

```go
	conn, err := db.Open(filepath.Join(*dataDir, "data.db"))
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("数据库迁移: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canvasScript, err := wireCanvasScript(ctx, conn, *dataDir, *pythonHome)
	if err != nil {
		return err
	}
```

4）`apphttp.Deps{…}` 增加 `CanvasScript: canvasScript,`。

- [ ] **Step 7: 运行确认通过**

Run: `cd server && go vet ./... && go test ./cmd/sidecar/ ./internal/http/ -count=1 -v`
Expected: 全部 PASS（原有握手端到端测试仍通过；日志里出现“警告: 找不到内置 Python…”属预期，除非设置了 `MIAOJU_PYTHON_HOME`）。

- [ ] **Step 8: Commit**

```bash
git add server/internal/http/canvas_script_handlers.go server/internal/http/canvas_script_handlers_test.go server/internal/http/router.go server/cmd/sidecar/canvas_script.go server/cmd/sidecar/canvas_script_test.go server/cmd/sidecar/main.go
git commit -m "feat(server): 脚本节点接口与 sidecar 装配（内置 Skill 释放、启动回收、worker）"
```

---

### Task 8: 内置 Python 集成测试与打包验收

**Files:**
- Test: `server/internal/skillruntime/bundled_python_test.go`

**Interfaces:**
- Consumes: Task 1 的 `src-tauri/resources/python/` 布局；Task 2 的 `skillbundle.Extract`；Task 4 的 `NewRunWorkspace`、`Create`；Task 5 的 `NewRestrictedShell`、`RestrictedShellConfig`、`PythonInterpreter`、`PythonHomeEnv`。
- Produces: 无（验收）。

- [ ] **Step 1: 写测试**

`server/internal/skillruntime/bundled_python_test.go`：

```go
package skillruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"

	"github.com/james/huabu_studio/apps/api/internal/skillbundle"
	"github.com/james/huabu_studio/apps/api/internal/skillruntime"
)

// bundledPython 用 Task 1 准备的内置 Python；没准备时跳过（CI / 未运行 fetch-python.sh 的 worktree）。
func bundledPython(t *testing.T) string {
	t.Helper()
	home := os.Getenv(skillruntime.PythonHomeEnv)
	if home == "" {
		home = filepath.Join("..", "..", "..", "src-tauri", "resources", "python")
	}
	p := skillruntime.PythonInterpreter(home)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("未找到内置 Python（%s）：先运行 scripts/fetch-python.sh", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// newBundledRun 在带空格和中文的数据目录里释放内置 Skill，并为指定 Skill 建一个 Run 工作区。
func newBundledRun(t *testing.T, skill string) (*skillruntime.RestrictedShell, string) {
	t.Helper()
	python := bundledPython(t)
	base := filepath.Join(t.TempDir(), "Application Support", "张三")
	skillsDir, err := skillbundle.Extract(filepath.Join(base, "runtime", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(skillsDir, skill)
	ws, err := skillruntime.NewRunWorkspace(filepath.Join(base, "runtime", "skill-runs"))
	if err != nil {
		t.Fatal(err)
	}
	runDir, err := ws.Create(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := skillruntime.NewRestrictedShell(runDir, skillDir, skillruntime.RestrictedShellConfig{Python: python})
	if err != nil {
		t.Fatal(err)
	}
	return shell, runDir
}

func execute(t *testing.T, shell *skillruntime.RestrictedShell, command string) (int, string) {
	t.Helper()
	resp, err := shell.Execute(context.Background(), &filesystem.ExecuteRequest{Command: command})
	if err != nil {
		t.Fatalf("%q: %v", command, err)
	}
	return *resp.ExitCode, resp.Output
}

func TestBundledPythonRunsRealValidator(t *testing.T) {
	shell, runDir := newBundledRun(t, "real-person-short-drama-v3")
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(`{"storyboard":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := execute(t, shell, "python3 scripts/validate_storyboard.py candidate.json --duration-sec 10")
	if code != 1 || !strings.Contains(out, "ERROR: 顶层 storyboard 不得为空") {
		t.Fatalf("真实校验脚本应报出中文错误并以 1 退出: exit=%d\n%s", code, out)
	}
}

func TestBundledPythonEmbedTextSatisfiesAdaptedValidator(t *testing.T) {
	shell, runDir := newBundledRun(t, "novel-real-person-short-drama-v1")
	source := "安岁推开门。她说：“你回来了？”\n雨还在下。"
	if err := os.WriteFile(filepath.Join(runDir, "source.txt"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := `{"sourceText":"安岁推门。","sourceSentences":[],"scriptLines":[],"scriptText":"x"}`
	if err := os.WriteFile(filepath.Join(runDir, "adapted_script.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	const notVerbatim = "sourceText 必须从 source.txt 原样注入"
	_, before := execute(t, shell, "python3 scripts/validate_adapted_script.py adapted_script.json --source source.txt")
	if !strings.Contains(before, notVerbatim) {
		t.Fatalf("对照组：手抄的 sourceText 应被校验器指出\n%s", before)
	}
	if code, out := execute(t, shell, "embed-text adapted_script.json sourceText source.txt"); code != 0 {
		t.Fatalf("embed-text 失败: %s", out)
	}
	_, after := execute(t, shell, "python3 scripts/validate_adapted_script.py adapted_script.json --source source.txt")
	if strings.Contains(after, notVerbatim) {
		t.Fatalf("embed-text 之后 sourceText 应与 source.txt 一致\n%s", after)
	}
}
```

- [ ] **Step 2: 运行（已准备内置 Python）**

Run: `scripts/fetch-python.sh && cd server && go test ./internal/skillruntime/ -run Bundled -v -count=1`
Expected: 2 个测试 PASS（不是 SKIP）。

- [ ] **Step 3: macOS 打包验收**

Run: `pnpm build`，然后：

```bash
APP="src-tauri/target/release/bundle/macos/喵剧AI.app"
"$APP/Contents/MacOS/sidecar" --data-dir "$(mktemp -d)/Application Support/x" <<< "0123456789abcdef0123456789abcdef" 2>&1 | head -5
```

Expected: 输出中有 `内置 Python: …/喵剧AI.app/Contents/Resources/python/bin/python3` 和 `READY <port>`（看到后 Ctrl-C）。

再模拟“从网上下载”的隔离属性：`xattr -w com.apple.quarantine "0081;00000000;Safari;" "$APP"`，在访达里“打开”并按 E5 放行，进入画布用脚本节点跑一次“真人短剧风格 v3”，任务成功、日志里校验脚本有输出。

- [ ] **Step 4: Windows 打包验收（手动，Windows 10/11 x64 机器）**

Run（macOS 上）：`scripts/fetch-python.sh x86_64-pc-windows-msvc && scripts/package-windows.sh && scripts/fetch-python.sh`
Expected: 生成 `out/miaoju-windows-x64-<ver>.zip`，`unzip -l` 可见 `python/python.exe`、`python/vcruntime140.dll`、`python/Lib/json/__init__.py`。

在 Windows 机器上解压到带中文和空格的路径（例如 `C:\Users\张三\Desktop\喵剧 AI\`），运行 `miaoju.exe`，用脚本节点跑一次“小说真人短剧 v1-漫解”：
- 任务成功；
- 执行过程中**没有黑色控制台窗口闪现**；
- 数据目录 `runtime\skill-runs\<日期>\run-*\` 下有 `adapted_script.json`，其 `sourceText` 与 `source.txt` 一致。

- [ ] **Step 5: macOS 14 / 15 回归（D8）**

在 macOS 14 与 15 上各安装一次 dmg，重复 Step 3 的脚本节点运行。结果记入 `docs/superpowers/plans/p0-results.md` 同格式的验收记录（由执行者追加一行，不新建文件）。

- [ ] **Step 6: Commit**

```bash
git add server/internal/skillruntime/bundled_python_test.go
git commit -m "test(skillruntime): 用内置 Python 跑真实 Skill 校验脚本"
```

---

## P6 完成标准

- `cd server && go test ./... -count=1` 全部通过；在已运行 `scripts/fetch-python.sh` 的环境里 `Bundled*` 测试为 PASS 而非 SKIP。
- `grep -rn '"bash"\|exec.Command("sh"\|cmd.exe\|os.Environ()' server/internal server/cmd --include='*.go' | grep -v _test.go` 无输出。
- macOS dmg 与 Windows zip 都带内置 Python，脚本节点在两个平台各成功跑通一个普通风格和一个小说风格（Task 8 Step 3–5）。

## 待定问题（附推荐）

1. **是否在 macOS 上再套一层 `sandbox-exec`（禁网络、只允许写工作区）？** 推荐：第一期不做。现在只运行经审计的只读校验脚本（Task 2 测试守住），收益小；`sandbox-exec` 已被 Apple 标为废弃，Windows 也没有对等机制，两端行为会不一致。若以后 Skill 引入会写文件或联网的脚本，再评估。
2. **`embed-text` 是否接受？** 它不是“Skill 目录内脚本”，是 sidecar 内置命令，但只写工作区。推荐保留：不提供的话两个小说风格会因模型手抄长文本而反复校验失败（参考仓 run-1988439767 的教训）。备选是在每个小说 Skill 的 `scripts/` 里加一个 `embed_source.py`，但那要改 Skill 内容并改 SKILL.md 提示。
3. **mac 是否只发 arm64？** 构建脚本已支持 x86_64，但每个架构一份 dmg、各约多 25 MB。推荐先只发 arm64（macOS 14 的 Intel 机器很少），需要时再加 x86_64 构建。
4. **Tauri 资源目录映射写法**：`"resources/python/": "python/"` 是否按目录递归拷贝需在 Task 1 Step 5 实测；不行时按该步给出的两个备选改，Task 5 的 macOS 候选路径同步调整。
