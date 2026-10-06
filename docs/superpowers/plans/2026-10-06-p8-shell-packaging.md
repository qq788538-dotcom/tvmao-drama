# P8 Tauri 壳加固与打包 Implementation Plan

> **实施后注（2026-10-06，正文未改）**
> - 实际决定：1024 图标为程序生成的占位图（正式稿到后重跑 `gen-icons.sh`）；全局 tauri-cli 升到 2.12.1；`sidecarGate.ts` 改名 `gateState.ts`（避免大小写不敏感文件系统冲突）；"崩溃 60 秒内只自动重启一次"；自定义数据目录不再强制 0700；T12（实机演练）交用户，见 `docs/manual-acceptance-checklist.md`。整合后 `ProcessHandle::shutdown` 先写 `SHUTDOWN` 行再关 stdin。出处：p8 账本、integration-report.md。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 P0 的 Tauri 薄壳加固成可发版的状态：sidecar 生命周期状态机（异步启动、崩溃发现与重启、重试命令、优雅退出）、数据目录可迁移（S2）、Windows WebView2 检测（E8）、版本号单一来源、正式图标，以及一键产出 `out/喵剧AI_<ver>_aarch64.dmg`、`out/喵剧AI_<ver>_x64.dmg`、`out/miaoju-windows-x64-<ver>.zip`（都带首次打开说明）和发版检查清单。

**Architecture:** 壳不再用 `tauri-plugin-shell`，改用 `tokio::process` 自己管 sidecar 进程（这样才能“先关 stdin、等待、再强杀”）。进程层 `sidecar.rs` 和状态机 `lifecycle.rs` 都不依赖 Tauri，单测用 `/bin/sh` 脚本扮演 sidecar；`lib.rs` 只做接线：命令 `server_info` / `sidecar_status` / `restart_sidecar` / `get_data_dir` / `set_data_dir`，事件 `sidecar-state`。前端提供 `SidecarGate` 组件统一处理“启动中 / 失败 + 重试 / 就绪（端口变了就重挂子树）”。打包脚本：macOS 用 Tauri 只出 `.app`（ad-hoc 签名），再用 `hdiutil` 自己做 dmg 以便放进说明文件；Windows 继续用 cargo-xwin 交叉编译 + `--no-bundle`，脚本按 `bundle.resources` 映射手动复制资源。

**Tech Stack:** Rust（Tauri 2.12、tokio process/sync、webview2-com 0.39、windows 0.62）、React 18 + vitest、bash + node + python3（打 zip）、hdiutil / codesign / cargo-xwin。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（D6、D8、E4、E5、E6、E7、E8、S1、S2、S11、SEC1–SEC4、第 9 节风险）；另读 `docs/superpowers/plans/p0-carryover.md` 的“带入 P8”和“带入 P1”前三条、`docs/superpowers/plans/p0-results.md`。

## Global Constraints

- 平台：macOS ≥ 14（`bundle.macOS.minimumSystemVersion = "14.0"`），Windows x64（D6、D8）。不做 Linux。
- 分发：macOS 发 dmg，Windows 发 zip（解压即用）；不做自动更新；“设置”里显示当前版本号（E4）。
- 签名：不做开发者签名和公证；macOS 只保留 ad-hoc（`signingIdentity: "-"`）；随包附“首次打开如何放行”说明（macOS 隐私与安全性放行 / Windows SmartScreen“仍要运行”）（E5）。
- 名称：应用名“喵剧AI”，标识 `com.miaoju.desktop`（定后不可改，决定数据目录名），主程序文件名 `miaoju`（E6）。
- WebView2：用系统自带运行时；启动时检测不到则弹窗提示并给出微软官方下载链接（E8）。
- 单实例单窗口：再次启动只把已有窗口带到前台（S11），`tauri-plugin-single-instance` 必须最先注册。
- 数据目录：默认 macOS `~/Library/Application Support/com.miaoju.desktop/`，Windows `%APPDATA%\com.miaoju.desktop\`（S1）；允许在设置页修改并迁移（S2）；目录权限仅当前用户（Unix 0o700）。
- 安全：sidecar 只监听 `127.0.0.1` 随机端口（SEC1）；每次启动随机 token + Origin 校验（SEC2）；token 只走 stdin，不进命令行参数和环境变量（SEC3）；capabilities 最小授权、严格 CSP（SEC4）。
- release 构建的 Origin 白名单不得包含 `http://localhost:5173`；只有 debug 构建由壳通过 `--origins` 传入。
- 所有面向用户的文案用中文；代码注释用中文。
- 脚本一律用 `bash scripts/…` 调用，不依赖可执行位。
- 字体打包（E7）由 P3a 在前端构建产物里完成，P8 不需要额外处理。

## Review Focus

1. 用户在压缩包预览里直接双击 `miaoju.exe`（没解压，或只拷走了 exe）→ 同目录没有 `sidecar.exe`；应显示“找不到 sidecar 程序……请先完整解压”的失败页，而不是白屏或闪退。（Task 6 `locate_sidecar_reports_unzip_hint`）
2. 自定义数据目录在外接盘上且盘没插，或 `location.json` 损坏 → 绝不能悄悄用一个空目录启动（用户会以为数据丢了）；应进入失败页并说明原因。（Task 3 `prepare_rejects_missing_custom_dir`、`resolve_rejects_corrupt_file`）
3. sidecar 还在启动时用户退出应用、或连点“重试” → 不残留 sidecar 进程、不死锁、最终只有一个进程在跑。（Task 5 `concurrent_restarts_end_ready`、`shutdown_while_starting_stops_process`）
4. sidecar 往 stderr 打出非 UTF-8 字节或海量日志 → 管道必须持续被读走（否则 sidecar 写满管道卡死），失败原因只带最后 20 行。（Task 2 `non_utf8_stderr_does_not_block`、`stderr_tail_is_bounded`）
5. 用户名或数据目录路径含中文和空格 → `--data-dir` 参数原样传给 sidecar。（Task 2 `args_with_spaces_and_cjk_pass_through`、Task 6 `sidecar_args_keep_cjk_and_spaces`）

---

## 关键决策（执行前请复核）

| # | 决策 | 理由 |
|---|---|---|
| K1 | 去掉 `tauri-plugin-shell`，用 `tokio::process` 自己拉起 sidecar | 插件的 `CommandChild` 无法“只关 stdin 但保留 kill 能力”，做不了优雅退出；同时少一个插件，SEC4 更小。Tauri 打包仍按 `externalBin` 把 sidecar 放在主程序同目录（dev：`target/debug/sidecar`；mac：`Contents/MacOS/sidecar`；Windows zip：exe 同目录）。 |
| K2 | 就绪后 sidecar 崩溃：自动重启一次；距上次自动重启不足 60 秒再崩 → 进入失败页，由用户点“重试” | 偶发崩溃用户无感；崩溃循环不会无限重启。用户点“重试”时清零计数。 |
| K3 | 每次（重新）拉起 sidecar 都生成新 token，端口也会变 | 与 SEC2“每次启动”一致；前端靠 `sidecar-state` 事件清缓存并重挂子树。 |
| K4 | 优雅退出：关 stdin → 最多等 3 秒 → 强杀；整个退出流程再加 6 秒上限 | 给 P1b 的 SQLite 收尾留时间；上限防止卡在“启动中”的锁上。壳被强杀时 sidecar 仍靠 stdin EOF 自行退出（P0 行为不变）。 |
| K5 | 数据目录位置存在 **默认数据目录** 下的 `location.json`（`{"version":1,"dataDir":"<绝对路径>"}`），只由壳读写 | 壳必须在启动 sidecar 前知道路径，不能放进 SQLite。P4 迁移完数据后调用 `set_data_dir` 再 `restart_sidecar`。 |
| K6 | Intel Mac 单独出 `x64` dmg，不做 universal | P6 要打包独立 Python 运行时，universal 需要 lipo 合并整套 Python，体积翻倍且易出错；两个 dmg 各自瘦身。文件名沿用 Tauri 的 `x64` 叫法。 |
| K7 | macOS dmg 由脚本用 `hdiutil` 生成（Tauri 只出 `.app`） | Tauri 的 dmg 配置不能往卷里放额外文件，首次打开说明必须在 dmg 里。 |
| K8 | WebView2 检测：建窗口前调用微软官方 API `GetAvailableCoreWebView2BrowserVersionString`；另外把 `Builder::build` 的错误改成弹窗（兜底） | 官方推荐检测方式，覆盖常青版和固定版；建窗失败后再提示的话 Tauri 已 panic，Windows 下无控制台，用户只看到闪退。 |
| K9 | 版本单一来源 = 根 `package.json`；`tauri.conf.json` 写 `"version": "../package.json"`；`Cargo.toml`、`web/package.json` 只是镜像，由 `bash scripts/version.sh set` 同步、`check` 校验；前端用 `@tauri-apps/api/app` 的 `getVersion()` | Tauri 原生支持 version 指向 package.json；`core:default` 已含 `core:app:allow-version`。 |
| K10 | CSP 继续 `127.0.0.1:*`，不收窄到具体端口 | 端口每次随机，CSP 是构建期静态值；真正的防线是 token + Origin（SEC2）。F7 图片域名由 P7 加。 |
| K11 | Windows zip 里的说明文件用 UTF-8 BOM + CRLF；zip 用 python3 `zipfile` 打（自动给中文文件名打 UTF-8 标记） | macOS 自带 `zip` 不保证 UTF-8 标记，资源管理器会显示乱码文件名。 |
| K12 | Private Network Access：先不加 `Access-Control-Allow-Private-Network` | `http://tauri.localhost` 与 `127.0.0.1` 同属本机地址，理论上不触发 PNA；以发版演练（Task 12）Windows 实测为准，若触发则由 Go 侧（P1b 的 localauth 负责人）加头。 |

## 跨计划接口声明（并行开发的约定）

**给 P1b（Go sidecar）：**
- 壳传给 sidecar 的参数只有 `--data-dir <绝对路径>` 和 `--origins <逗号分隔>`（两者 P0 已存在）。release：`tauri://localhost,http://tauri.localhost`；debug 额外加 `http://localhost:5173`。Go 的 `defaultOrigins` 可以去掉 `localhost:5173`，壳不依赖默认值。
- stdin 第一行是 token；之后 stdin EOF 的含义是“请优雅退出”，**sidecar 必须在 3 秒内退出**，否则被强杀。
- 壳在启动 sidecar 前已经用 0o700 创建数据目录；Go 侧 `MkdirAll` 也应改为 0o700（P1b 负责）。
- stderr 每行一条日志（壳会转发并保留最后 20 行作为失败原因）；stdout 只用于 `READY <port>`。

**给 P4（设置页）：**
- 版本号：`import { getVersion } from '@tauri-apps/api/app'`，无需新权限。
- 数据目录：Tauri 命令 `get_data_dir() -> { current: string, default: string, custom: boolean }`；`set_data_dir({ path: string | null }) -> 同上`（`null` = 恢复默认；只改“下次用哪个目录”，**不搬数据**；目标必须是已存在的绝对路径目录）。迁移流程：P4 的 sidecar 接口把数据拷到目标目录 → 前端 `invoke('set_data_dir', { path })` → `restartSidecar()`（见 `web/src/shared/server.ts`）。
- `location.json` 只由壳读写，P4 不要直接写文件。

**给 P3a（前端迁移）：**
- 稳定接口在 `web/src/shared/server.ts`：`getServerInfo()`、`getSidecarStatus()`、`restartSidecar()`、`onSidecarStatus(cb)`、类型 `SidecarStatus`、常量 `SIDECAR_STATE_EVENT = 'sidecar-state'`；以及组件 `web/src/shared/SidecarGate.tsx`：`<SidecarGate>{(info) => <App info={info} />}</SidecarGate>`。sidecar 重启后端口变化，`SidecarGate` 以端口为 key 重挂子树。P3a 移动诊断页时请保留 `SidecarGate` 包裹；搬家冲突在最终合并时解决。
- 规则不变：任何地方都不要把拼好的地址存起来，每次用 `getServerInfo()` 取。

**给 P6（Python 运行时等资源）：**
- 资源通过 `tauri.conf.json` 的 `bundle.resources` **映射形式** `{"<相对 src-tauri 的源路径>": "<资源目录内目标路径>"}` 声明（P6 自己加这一项；源路径不能用通配符，目录写成 `"resources/python/": "python/"` 这种）。
- 每个目标平台的资源准备脚本放到 `scripts/prebuild.d/<NN>-<名字>.sh`，参数为 target triple（`aarch64-apple-darwin` / `x86_64-apple-darwin` / `x86_64-pc-windows-msvc`），由 `bash scripts/prebuild.sh <triple>` 在每次打包前按文件名顺序调用。P8 不建 `prebuild.d/` 目录，P6 新建即可。
- 运行时定位：macOS 包内资源在 `Contents/Resources/<目标路径>`（即 sidecar 所在目录的 `../Resources/`）；Windows zip 和 dev 下资源在主程序/ sidecar 同目录。建议 Go 侧实现：`exeDir/../Resources` 存在则用它，否则用 `exeDir`。
- macOS：资源必须在 Tauri 签名前进包（走 `bundle.resources` 即可）；签名后再改包内容会让系统报“已损坏”。`release-macos.sh` 会做 `codesign --verify --deep --strict` 把关。

## 文件结构

| 文件 | 责任 | 任务 |
|---|---|---|
| `scripts/version.sh`（新） | 版本号校验与同步 | T1 |
| `src-tauri/tests/version.rs`（新） | 校验 Tauri 读到的版本 = Cargo 版本 | T1 |
| `src-tauri/src/sidecar.rs`（新） | 进程层：拉起、stdin 传 token、等 READY、stderr 尾部、退出通知、优雅关闭 | T2 |
| `src-tauri/src/data_dir.rs`（新） | 数据目录解析、`location.json` 读写、0o700 | T3 |
| `src-tauri/src/webview2.rs`、`src-tauri/src/native_dialog.rs`（新） | WebView2 检测、原生弹窗 | T4 |
| `src-tauri/src/lifecycle.rs`（新） | 状态机、重启策略、ServerInfo、token | T5 |
| `src-tauri/src/lib.rs` | 接线：命令、事件、setup、退出 | T6（T2–T5 各加一行 `pub mod`） |
| `src-tauri/Cargo.toml` | T2 tokio features；T3 serde_json；T4 Windows 依赖；T6 删 shell 插件 | 多任务，合并取并集 |
| `src-tauri/capabilities/default.json` | 权限描述更新 | T6 |
| `src-tauri/tauri.conf.json` | 只改 `version` 一行 | T1 |
| `web/src/shared/server.ts`、`server.test.ts` | 状态查询、重启、订阅 | T7 |
| `web/src/shared/sidecarGate.ts`、`sidecarGate.test.ts`、`SidecarGate.tsx`（新） | 门控状态机与组件 | T7 |
| `web/src/diag/DiagPage.tsx` | 改用 `SidecarGate` | T7 |
| `scripts/prebuild.sh`（新） | 打包前钩子入口 | T8（T9 写入同一内容） |
| `scripts/release-macos.sh`（新）、`packaging/首次打开说明-macOS.txt`（新） | macOS dmg | T8 |
| `package.json` | scripts 改 `bash scripts/…`、加发版命令 | T8 |
| `scripts/package-windows.sh`、`scripts/lib/list-resources.mjs`(+test)、`scripts/lib/make_zip.py`、`packaging/首次打开说明-Windows.txt` | Windows zip | T9 |
| `scripts/gen-icons.sh`（新）、`packaging/icon-1024.png`（设计稿）、`src-tauri/icons/*` | 正式图标 | T10 |
| `docs/release-checklist.md`（新） | 发版检查清单 | T11 |
| `docs/release-results/<ver>.md`（新） | 发版演练记录 | T12 |

## 并发执行安排

速度优先：每个任务在自己的 git worktree（`.worktrees/p8-tN`，从 main 拉分支 `p8/tN-<名字>`）里做，**冲突统一在最后合并时解决**。

| 批次 | 任务（可同时开工） | 依赖 |
|---|---|---|
| 第 1 批 | T1 版本单一来源 · T2 进程层 · T3 数据目录 · T4 WebView2 · T7 前端门控 · T8 macOS 打包 · T9 Windows 打包 · T10 图标（需设计稿） · T11 发版清单 | 无 |
| 第 2 批 | T5 生命周期状态机 | T2 合入 main |
| 第 3 批 | T6 壳接线 | T2、T3、T4、T5 合入；冒烟时需要 T7 |
| 第 4 批 | T12 发版演练（macOS 14/15/26、Windows 10/11） | 全部合入（含 P1b/P3a/P6 等已合入的部分） |

**共享文件归属与合并规则：**
- `src-tauri/src/lib.rs`：T6 拥有。T2/T3/T4/T5 只在文件顶部加自己的 `pub mod xxx;` 一行（按字母序），合并时取并集。
- `src-tauri/Cargo.toml`：各任务只改自己那几行，合并取并集；`Cargo.lock` 冲突时任取一边，然后在 `src-tauri/` 跑 `cargo check && cargo xwin check --target x86_64-pc-windows-msvc` 重新生成。
- `src-tauri/tauri.conf.json`：P8 内只有 T1 改（`version` 一行）。P6 加 `bundle.resources`、P7 改 CSP，合并取并集。
- `package.json`：T8 拥有 `scripts` 块；T9 不改它。
- `scripts/prebuild.sh`：T8 与 T9 都按本计划写入**逐字节相同**的内容，合并无冲突。
- `web/src/shared/server.ts`、`web/src/diag/DiagPage.tsx`：T7 改；若 P3a 已搬家，以 P3a 的目录结构为准，保留 T7 的导出和 `SidecarGate` 包裹。
- `packaging/`：T8（macOS 说明）、T9（Windows 说明）、T10（图标稿）各管各的文件。

---

### Task 1: 版本号单一来源

**Files:**
- Create: `scripts/version.sh`
- Create: `src-tauri/tests/version.rs`
- Modify: `src-tauri/tauri.conf.json`（第 5 行 `"version": "0.1.0"`）

**Interfaces:**
- Consumes: 无
- Produces: `bash scripts/version.sh check`（一致则输出 `版本一致：<ver>` 并退出 0，否则退出 1）；`bash scripts/version.sh set <x.y.z>`。T8、T9 的发版脚本第一步调用 `check`。

- [ ] **Step 1: 写失败的测试**

`src-tauri/tests/version.rs`：

```rust
//! 版本号单一来源：tauri.conf.json 的 version 指向根 package.json，
//! Cargo.toml 的版本由 scripts/version.sh 同步。这里确认 Tauri 实际读到的版本与 Cargo 版本一致。

#[test]
fn tauri_version_matches_cargo_version() {
    let ctx: tauri::Context<tauri::Wry> = tauri::generate_context!();
    assert_eq!(
        ctx.package_info().version.to_string(),
        env!("CARGO_PKG_VERSION")
    );
}

#[test]
fn tauri_conf_points_version_at_package_json() {
    let conf = include_str!("../tauri.conf.json");
    assert!(
        conf.contains(r#""version": "../package.json""#),
        "tauri.conf.json 的 version 必须是 \"../package.json\""
    );
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd src-tauri && cargo test --test version`
Expected: `tauri_conf_points_version_at_package_json` FAIL（第一个测试此时会通过，因为两处都是 0.1.0）。

- [ ] **Step 3: 改 tauri.conf.json**

把 `"version": "0.1.0",` 改成：

```json
  "version": "../package.json",
```

- [ ] **Step 4: 写 `scripts/version.sh`**

```bash
#!/usr/bin/env bash
# 版本号单一来源：根 package.json 的 version。
# - tauri.conf.json 直接引用它（"version": "../package.json"）；
# - build-sidecar.sh 从它注入 sidecar 的 main.version；
# - 前端用 @tauri-apps/api/app 的 getVersion() 读取（来自 tauri.conf.json）。
# Cargo.toml 和 web/package.json 的 version 只是镜像，由本脚本同步：
#   bash scripts/version.sh check        # 不一致则失败（发版脚本第一步）
#   bash scripts/version.sh set 0.2.0    # 同时改三处并校验
set -euo pipefail
cd "$(dirname "$0")/.."

root_v()  { node -p "require('./package.json').version"; }
web_v()   { node -p "require('./web/package.json').version"; }
conf_v()  { node -p "require('./src-tauri/tauri.conf.json').version"; }
cargo_v() { sed -n 's/^version = "\(.*\)"$/\1/p' src-tauri/Cargo.toml | head -1; }

set_json_version() {
  node -e '
    const fs = require("fs");
    const [file, v] = process.argv.slice(1);
    const j = JSON.parse(fs.readFileSync(file, "utf8"));
    j.version = v;
    fs.writeFileSync(file, JSON.stringify(j, null, 2) + "\n");
  ' "$1" "$2"
}

case "${1:-}" in
  check)
    R="$(root_v)"; ok=1
    [ "$(conf_v)" = "../package.json" ] || { echo "tauri.conf.json 的 version 应为 \"../package.json\"，实际：$(conf_v)" >&2; ok=0; }
    [ "$(web_v)" = "$R" ] || { echo "web/package.json 版本 $(web_v) 与根 package.json $R 不一致" >&2; ok=0; }
    [ "$(cargo_v)" = "$R" ] || { echo "src-tauri/Cargo.toml 版本 $(cargo_v) 与根 package.json $R 不一致" >&2; ok=0; }
    if [ "$ok" = 1 ]; then echo "版本一致：$R"; else exit 1; fi
    ;;
  set)
    V="${2:?用法: bash scripts/version.sh set <x.y.z>}"
    [[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "版本号必须是 x.y.z 三段数字：$V" >&2; exit 1; }
    set_json_version package.json "$V"
    set_json_version web/package.json "$V"
    # 只改 [package] 段的第一行 version（依赖写成 { version = ... }，不以 version 开头）
    sed -i '' "1,/^version = /s/^version = \".*\"$/version = \"$V\"/" src-tauri/Cargo.toml
    # 刷新 Cargo.lock 里本包的版本
    ( cd src-tauri && cargo metadata --format-version 1 --offline >/dev/null )
    bash scripts/version.sh check
    ;;
  *)
    echo "用法: bash scripts/version.sh check | set <x.y.z>" >&2
    exit 1
    ;;
esac
```

- [ ] **Step 5: 验证**

Run: `bash scripts/version.sh check`
Expected: `版本一致：0.1.0`

Run: `bash scripts/version.sh set 0.1.1 && cd src-tauri && cargo test --test version && cd .. && bash scripts/version.sh set 0.1.0`
Expected: 两次 `版本一致`，`cargo test` 2 passed；最后回到 0.1.0。

Run: `bash scripts/version.sh set 1.0`
Expected: 退出码 1，输出“版本号必须是 x.y.z 三段数字：1.0”。

Run: `git diff --stat`
Expected: 只有 `src-tauri/tauri.conf.json`（以及新文件）；`package.json`、`web/package.json`、`Cargo.toml`、`Cargo.lock` 回到原值无差异。

- [ ] **Step 6: Commit**

```bash
git add scripts/version.sh src-tauri/tests/version.rs src-tauri/tauri.conf.json
git commit -m "build: 版本号以根 package.json 为唯一来源，加 version.sh 校验与同步"
```

---

### Task 2: sidecar 进程层（拉起、握手、stderr 尾部、优雅关闭）

**Files:**
- Create: `src-tauri/src/sidecar.rs`
- Modify: `src-tauri/src/lib.rs`（只在第 3 行后加 `pub mod sidecar;`）
- Modify: `src-tauri/Cargo.toml`（tokio features）

**Interfaces:**
- Consumes: 无
- Produces（T5、T6 使用）：
  - `pub struct LaunchSpec { pub program: PathBuf, pub args: Vec<String> }`
  - `pub struct ExitInfo { pub code: Option<i32>, pub stderr_tail: String }`（`Clone, Debug, PartialEq`）
  - `pub struct Launched { pub port: u16, pub handle: ProcessHandle }`
  - `pub async fn launch(spec: &LaunchSpec, token: &str, ready_timeout: Duration) -> Result<Launched, String>`
  - `impl ProcessHandle { pub fn exited(&self) -> watch::Receiver<Option<ExitInfo>>; pub async fn shutdown(self, grace: Duration) -> ExitInfo }`；`ProcessHandle` 被 drop 时强杀进程。
  - `pub fn parse_ready(line: &str) -> Option<u16>`
  - `pub struct StderrTail`、`pub const STDERR_TAIL_LINES: usize = 20`

- [ ] **Step 1: 加依赖和模块声明**

`src-tauri/Cargo.toml` 把 tokio 一行改成：

```toml
tokio = { version = "1", features = ["time", "process", "io-util", "sync", "macros", "rt"] }
```

`src-tauri/src/lib.rs` 在两行文件注释之后加：

```rust
pub mod sidecar;
```

- [ ] **Step 2: 写失败的测试**

新建 `src-tauri/src/sidecar.rs`，先只放测试模块和空壳类型（下一步实现）。测试（全部 `#[cfg(unix)]`，用 `/bin/sh -c` 扮演 sidecar）：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready_accepts_port_line() {
        assert_eq!(parse_ready("READY 54321"), Some(54321));
        assert_eq!(parse_ready("READY 54321\r\n"), Some(54321));
    }

    #[test]
    fn parse_ready_ignores_other_lines() {
        assert_eq!(parse_ready("listening..."), None);
        assert_eq!(parse_ready("READY"), None);
        assert_eq!(parse_ready("READY abc"), None);
        assert_eq!(parse_ready("READY 99999"), None);
    }

    #[test]
    fn stderr_tail_is_bounded() {
        let mut t = StderrTail::new(3);
        for i in 0..10 {
            t.push(&format!("line{i}\n"));
        }
        t.push("   ");
        assert_eq!(t.joined(), "line7\nline8\nline9");
    }
}

#[cfg(all(test, unix))]
mod process_tests {
    use super::*;
    use std::time::Instant;

    const TOKEN: &str = "0123456789abcdef0123456789abcdef";

    fn sh(script: &str) -> LaunchSpec {
        LaunchSpec {
            program: "/bin/sh".into(),
            args: vec!["-c".into(), script.into()],
        }
    }

    #[tokio::test]
    async fn launch_returns_port_and_token_reaches_stdin() {
        let spec = sh(r#"read t; [ "$t" = "0123456789abcdef0123456789abcdef" ] || exit 3; echo "READY 5555"; cat >/dev/null"#);
        let l = launch(&spec, TOKEN, Duration::from_secs(5)).await.unwrap();
        assert_eq!(l.port, 5555);
        let start = Instant::now();
        let info = l.handle.shutdown(Duration::from_secs(3)).await;
        assert_eq!(info.code, Some(0));
        assert!(start.elapsed() < Duration::from_secs(2), "关 stdin 后应自行退出");
    }

    #[tokio::test]
    async fn args_with_spaces_and_cjk_pass_through() {
        let spec = LaunchSpec {
            program: "/bin/sh".into(),
            args: vec![
                "-c".into(),
                r#"read t; [ "$1" = "/Users/张 三/数据 目录" ] || exit 4; echo "READY 1"; cat >/dev/null"#.into(),
                "sh".into(),
                "/Users/张 三/数据 目录".into(),
            ],
        };
        let l = launch(&spec, TOKEN, Duration::from_secs(5)).await.unwrap();
        assert_eq!(l.port, 1);
        l.handle.shutdown(Duration::from_secs(2)).await;
    }

    #[tokio::test]
    async fn exit_before_ready_reports_multiline_stderr() {
        let spec = sh(r#"read t; echo first >&2; echo second >&2; exit 1"#);
        let err = launch(&spec, TOKEN, Duration::from_secs(5)).await.err().unwrap();
        assert!(err.contains("未就绪即退出"), "{err}");
        assert!(err.contains("first") && err.contains("second"), "{err}");
    }

    #[tokio::test]
    async fn exit_before_ready_without_stderr_has_plain_message() {
        let spec = sh("read t; exit 1");
        let err = launch(&spec, TOKEN, Duration::from_secs(5)).await.err().unwrap();
        assert_eq!(err, "sidecar 未就绪即退出");
    }

    #[tokio::test]
    async fn silent_sidecar_times_out_and_is_killed() {
        let spec = sh("read t; exec sleep 30");
        let start = Instant::now();
        let err = launch(&spec, TOKEN, Duration::from_millis(200)).await.err().unwrap();
        assert!(err.contains("启动超时"), "{err}");
        assert!(start.elapsed() < Duration::from_secs(3));
    }

    #[tokio::test]
    async fn shutdown_escalates_to_kill() {
        let spec = sh(r#"read t; echo "READY 1"; trap '' TERM; exec sleep 30"#);
        let l = launch(&spec, TOKEN, Duration::from_secs(5)).await.unwrap();
        let start = Instant::now();
        let info = l.handle.shutdown(Duration::from_millis(300)).await;
        assert_eq!(info.code, None, "被强杀，没有退出码");
        assert!(start.elapsed() < Duration::from_secs(3));
    }

    #[tokio::test]
    async fn crash_after_ready_is_reported_with_tail() {
        let spec = sh(r#"read t; echo "READY 7"; echo fatal-1 >&2; echo fatal-2 >&2; exit 7"#);
        let l = launch(&spec, TOKEN, Duration::from_secs(5)).await.unwrap();
        let mut rx = l.handle.exited();
        let info = tokio::time::timeout(Duration::from_secs(3), rx.wait_for(|v| v.is_some()))
            .await
            .unwrap()
            .unwrap()
            .clone()
            .unwrap();
        assert_eq!(info.code, Some(7));
        assert!(info.stderr_tail.contains("fatal-1") && info.stderr_tail.contains("fatal-2"));
    }

    #[tokio::test]
    async fn dropping_handle_kills_process() {
        let spec = sh(r#"read t; echo "READY 1"; exec sleep 30"#);
        let l = launch(&spec, TOKEN, Duration::from_secs(5)).await.unwrap();
        let mut rx = l.handle.exited();
        drop(l);
        tokio::time::timeout(Duration::from_secs(3), rx.wait_for(|v| v.is_some()))
            .await
            .expect("drop 后进程应被结束")
            .unwrap();
    }

    #[tokio::test]
    async fn non_utf8_stderr_does_not_block() {
        // 非 UTF-8 字节 + 大量日志：读取不能中断，否则 sidecar 写满管道会卡住
        let spec = sh(r#"read t; printf '\377\376\n' >&2; i=0; while [ $i -lt 2000 ]; do echo "log line $i" >&2; i=$((i+1)); done; echo "READY 9"; echo last >&2; exit 2"#);
        let l = launch(&spec, TOKEN, Duration::from_secs(10)).await.unwrap();
        assert_eq!(l.port, 9);
        let mut rx = l.handle.exited();
        let info = tokio::time::timeout(Duration::from_secs(5), rx.wait_for(|v| v.is_some()))
            .await
            .unwrap()
            .unwrap()
            .clone()
            .unwrap();
        assert_eq!(info.stderr_tail.lines().count(), STDERR_TAIL_LINES);
        assert!(info.stderr_tail.ends_with("last"));
    }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd src-tauri && cargo test sidecar::`
Expected: 编译失败（`launch`、`LaunchSpec` 等未定义）。

- [ ] **Step 4: 实现**

在 `src-tauri/src/sidecar.rs` 测试模块之前写：

```rust
//! sidecar 进程层：拉起进程、stdin 传 token、等 "READY <port>"、转发并保留 stderr 尾部、
//! 通知进程退出、优雅关闭（先关 stdin，等待，再强杀）。
//! 不依赖 Tauri，单测直接用 /bin/sh 脚本扮演 sidecar。
//!
//! 启动协议见 server/cmd/sidecar/main.go 顶部注释：stdin 第一行是 token；
//! 就绪后 stdout 打印 "READY <port>"；stdin EOF 表示壳要求退出。

use std::collections::VecDeque;
use std::path::PathBuf;
use std::process::Stdio;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tokio::io::{AsyncBufRead, AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::process::{Child, ChildStderr, ChildStdin, Command};
use tokio::sync::{oneshot, watch};
use tokio::task::JoinHandle;

/// 失败原因里保留的 stderr 行数。
pub const STDERR_TAIL_LINES: usize = 20;

/// 只保留 stderr 的最后若干行（空行跳过）。
#[derive(Debug)]
pub struct StderrTail {
    lines: VecDeque<String>,
    cap: usize,
}

impl StderrTail {
    pub fn new(cap: usize) -> Self {
        Self { lines: VecDeque::with_capacity(cap), cap }
    }

    pub fn push(&mut self, line: &str) {
        let line = line.trim_end();
        if line.trim().is_empty() {
            return;
        }
        if self.lines.len() == self.cap {
            self.lines.pop_front();
        }
        self.lines.push_back(line.to_string());
    }

    pub fn joined(&self) -> String {
        self.lines.iter().cloned().collect::<Vec<_>>().join("\n")
    }
}

/// 解析 sidecar 的就绪行 "READY <port>"。
pub fn parse_ready(line: &str) -> Option<u16> {
    line.trim().strip_prefix("READY ")?.trim().parse().ok()
}

#[derive(Debug, Clone)]
pub struct LaunchSpec {
    pub program: PathBuf,
    pub args: Vec<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct ExitInfo {
    /// 被信号结束（含强杀）时为 None。
    pub code: Option<i32>,
    pub stderr_tail: String,
}

pub struct Launched {
    pub port: u16,
    pub handle: ProcessHandle,
}

/// 运行中的 sidecar。drop 时强杀进程（等价于 kill_on_drop）。
pub struct ProcessHandle {
    stdin: Option<ChildStdin>,
    kill: Option<oneshot::Sender<()>>,
    exited: watch::Receiver<Option<ExitInfo>>,
}

impl ProcessHandle {
    /// 退出通知：值从 None 变成 Some 表示进程已退出。
    pub fn exited(&self) -> watch::Receiver<Option<ExitInfo>> {
        self.exited.clone()
    }

    async fn wait_exit_for(&self, d: Duration) -> Option<ExitInfo> {
        let mut rx = self.exited.clone();
        tokio::time::timeout(d, async move {
            rx.wait_for(|v| v.is_some()).await.ok().and_then(|v| (*v).clone())
        })
        .await
        .ok()
        .flatten()
    }

    async fn kill_now(&mut self) -> ExitInfo {
        self.stdin.take();
        if let Some(k) = self.kill.take() {
            let _ = k.send(());
        }
        self.wait_exit_for(Duration::from_secs(5)).await.unwrap_or(ExitInfo {
            code: None,
            stderr_tail: String::new(),
        })
    }

    /// 优雅关闭：先关 stdin（sidecar 约定读到 EOF 即收尾退出），最多等 grace，仍未退出再强杀。
    pub async fn shutdown(mut self, grace: Duration) -> ExitInfo {
        self.stdin.take();
        if let Some(info) = self.wait_exit_for(grace).await {
            return info;
        }
        eprintln!("[sidecar] {:.1} 秒内未退出，强制结束", grace.as_secs_f32());
        self.kill_now().await
    }
}

fn with_tail(msg: String, tail: &str) -> String {
    if tail.is_empty() {
        msg
    } else {
        format!("{msg}：\n{tail}")
    }
}

/// 按字节读一行再做有损 UTF-8 转换：非 UTF-8 输出不能让读取中断（否则管道写满会卡住 sidecar）。
async fn next_line_lossy<R: AsyncBufRead + Unpin>(r: &mut R, buf: &mut Vec<u8>) -> Option<String> {
    buf.clear();
    match r.read_until(b'\n', buf).await {
        Ok(0) | Err(_) => None,
        Ok(_) => Some(String::from_utf8_lossy(buf).into_owned()),
    }
}

async fn pump_stderr(stderr: ChildStderr, tail: Arc<Mutex<StderrTail>>) {
    let mut r = BufReader::new(stderr);
    let mut buf = Vec::new();
    while let Some(line) = next_line_lossy(&mut r, &mut buf).await {
        eprintln!("[sidecar] {}", line.trim_end());
        tail.lock().unwrap().push(&line);
    }
}

async fn wait_exit(
    mut child: Child,
    kill_rx: oneshot::Receiver<()>,
    stderr_task: JoinHandle<()>,
    tail: Arc<Mutex<StderrTail>>,
    tx: watch::Sender<Option<ExitInfo>>,
) {
    let status = tokio::select! {
        s = child.wait() => s,
        // 收到强杀请求，或 ProcessHandle 被 drop（发送端关闭）
        _ = kill_rx => {
            let _ = child.start_kill();
            child.wait().await
        }
    };
    // 进程退出后 stderr 管道关闭；等读完再取尾部（孙进程占着管道时最多等 1 秒）
    let _ = tokio::time::timeout(Duration::from_secs(1), stderr_task).await;
    let code = status.ok().and_then(|s| s.code());
    let stderr_tail = tail.lock().unwrap().joined();
    let _ = tx.send(Some(ExitInfo { code, stderr_tail }));
}

/// 拉起 sidecar 并等到 READY。任何一步失败都返回可读的中文原因（带 stderr 最后几行）。
pub async fn launch(spec: &LaunchSpec, token: &str, ready_timeout: Duration) -> Result<Launched, String> {
    let mut cmd = Command::new(&spec.program);
    cmd.args(&spec.args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW：不弹控制台窗口
    let mut child = cmd
        .spawn()
        .map_err(|e| format!("无法启动 sidecar（{}）：{e}", spec.program.display()))?;

    let mut stdin = child.stdin.take().expect("stdin 已设为 piped");
    let stdout = child.stdout.take().expect("stdout 已设为 piped");
    let stderr = child.stderr.take().expect("stderr 已设为 piped");

    let tail = Arc::new(Mutex::new(StderrTail::new(STDERR_TAIL_LINES)));
    let stderr_task = tokio::spawn(pump_stderr(stderr, tail.clone()));
    let (exited_tx, exited_rx) = watch::channel(None);
    let (kill_tx, kill_rx) = oneshot::channel();
    tokio::spawn(wait_exit(child, kill_rx, stderr_task, tail, exited_tx));
    let mut handle = ProcessHandle { stdin: None, kill: Some(kill_tx), exited: exited_rx };

    // token 只走 stdin（SEC3）：参数和环境变量同机其他进程读得到。
    let wrote = async {
        stdin.write_all(format!("{token}\n").as_bytes()).await?;
        stdin.flush().await?;
        Ok::<(), std::io::Error>(())
    }
    .await;
    if let Err(e) = wrote {
        let info = handle.kill_now().await;
        return Err(with_tail(format!("无法向 sidecar 传递 token：{e}"), &info.stderr_tail));
    }
    handle.stdin = Some(stdin);

    let mut out = BufReader::new(stdout);
    let ready = tokio::time::timeout(ready_timeout, async {
        let mut buf = Vec::new();
        while let Some(line) = next_line_lossy(&mut out, &mut buf).await {
            if let Some(port) = parse_ready(&line) {
                return Some(port);
            }
        }
        None
    })
    .await;

    match ready {
        Ok(Some(port)) => {
            // 之后继续读走 stdout，避免管道写满阻塞 sidecar
            tokio::spawn(async move {
                let mut buf = Vec::new();
                while next_line_lossy(&mut out, &mut buf).await.is_some() {}
            });
            Ok(Launched { port, handle })
        }
        Ok(None) => {
            let info = handle.kill_now().await;
            Err(with_tail("sidecar 未就绪即退出".to_string(), &info.stderr_tail))
        }
        Err(_) => {
            let info = handle.kill_now().await;
            Err(with_tail(
                format!("sidecar 启动超时（{} 秒内未就绪）", ready_timeout.as_secs()),
                &info.stderr_tail,
            ))
        }
    }
}
```

注意：`lib.rs` 里 P0 的 `parse_ready` 及其测试暂时保留（T6 删除），两份同名函数分属不同模块，不冲突。

- [ ] **Step 5: 运行确认通过**

Run: `cd src-tauri && cargo test sidecar:: && cargo build`
Expected: `sidecar::tests` 3 个 + `sidecar::process_tests` 9 个全部 PASS；`cargo build` 无新警告（`pub` 项不会报 dead_code）。

- [ ] **Step 6: Windows 交叉编译检查**

Run: `cd src-tauri && cargo xwin check --target x86_64-pc-windows-msvc`
Expected: 通过（确认 `creation_flags` 在 Windows 下可用）。

- [ ] **Step 7: Commit**

```bash
git add src-tauri/src/sidecar.rs src-tauri/src/lib.rs src-tauri/Cargo.toml src-tauri/Cargo.lock
git commit -m "feat(tauri): sidecar 进程层改用 tokio::process，支持 stderr 尾部 20 行、退出通知和优雅关闭"
```

---

### Task 3: 数据目录解析、location.json、0o700

**Files:**
- Create: `src-tauri/src/data_dir.rs`
- Modify: `src-tauri/src/lib.rs`（顶部加 `pub mod data_dir;`）
- Modify: `src-tauri/Cargo.toml`（`[dependencies]` 加 `serde_json = "1"`）

**Interfaces:**
- Consumes: 无
- Produces（T6 使用；P4 通过 T6 的命令间接使用）：
  - `pub const LOCATION_FILE: &str = "location.json"`
  - `pub struct DataDirInfo { pub current: PathBuf, pub default: PathBuf, pub custom: bool }`（`Serialize` 为 camelCase：`{current, default, custom}`）
  - `pub fn resolve(default_dir: &Path) -> Result<DataDirInfo, String>`
  - `pub fn prepare(info: &DataDirInfo) -> Result<(), String>`
  - `pub fn ensure_private_dir(dir: &Path) -> Result<(), String>`
  - `pub fn write_location(default_dir: &Path, target: Option<&Path>) -> Result<DataDirInfo, String>`

- [ ] **Step 1: 写失败的测试**

`src-tauri/src/data_dir.rs` 末尾：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    fn tmp() -> PathBuf {
        let d = std::env::temp_dir().join(format!("miaoju-test-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn missing_file_means_default() {
        let d = tmp();
        let info = resolve(&d).unwrap();
        assert_eq!(info, DataDirInfo { current: d.clone(), default: d, custom: false });
    }

    #[test]
    fn valid_file_points_to_custom_dir() {
        let d = tmp();
        let target = tmp();
        std::fs::write(
            d.join(LOCATION_FILE),
            format!(r#"{{"version":1,"dataDir":{}}}"#, serde_json::to_string(&target).unwrap()),
        )
        .unwrap();
        let info = resolve(&d).unwrap();
        assert_eq!(info.current, target);
        assert!(info.custom);
    }

    #[test]
    fn custom_equal_to_default_is_not_custom() {
        let d = tmp();
        write_location(&d, Some(&d)).unwrap();
        assert!(!resolve(&d).unwrap().custom);
        assert!(!d.join(LOCATION_FILE).exists());
    }

    #[test]
    fn resolve_rejects_corrupt_file() {
        let d = tmp();
        std::fs::write(d.join(LOCATION_FILE), "{not json").unwrap();
        let err = resolve(&d).unwrap_err();
        assert!(err.contains("location.json"), "{err}");
    }

    #[test]
    fn resolve_rejects_relative_path() {
        let d = tmp();
        std::fs::write(d.join(LOCATION_FILE), r#"{"version":1,"dataDir":"relative/dir"}"#).unwrap();
        assert!(resolve(&d).unwrap_err().contains("绝对路径"));
    }

    #[test]
    fn write_then_reset_location() {
        let d = tmp();
        let target = tmp();
        let info = write_location(&d, Some(&target)).unwrap();
        assert_eq!(info.current, target);
        assert!(info.custom);
        let info = write_location(&d, None).unwrap();
        assert_eq!(info.current, d);
        assert!(!d.join(LOCATION_FILE).exists());
    }

    #[test]
    fn write_location_rejects_relative_and_missing() {
        let d = tmp();
        assert!(write_location(&d, Some(Path::new("rel"))).unwrap_err().contains("绝对路径"));
        let missing = d.join("nope");
        assert!(write_location(&d, Some(&missing)).unwrap_err().contains("不存在"));
    }

    #[test]
    fn prepare_rejects_missing_custom_dir() {
        let d = tmp();
        let gone = d.join("外接盘 未连接");
        let info = DataDirInfo { current: gone.clone(), default: d, custom: true };
        let err = prepare(&info).unwrap_err();
        assert!(err.contains("数据目录不可用"), "{err}");
        assert!(!gone.exists(), "不能替用户新建空目录");
    }

    #[test]
    fn prepare_creates_default_dir() {
        let d = tmp().join("新目录");
        let info = DataDirInfo { current: d.clone(), default: d.clone(), custom: false };
        prepare(&info).unwrap();
        assert!(d.is_dir());
    }

    #[cfg(unix)]
    #[test]
    fn ensure_private_dir_sets_0700() {
        use std::os::unix::fs::PermissionsExt;
        let d = tmp();
        std::fs::set_permissions(&d, std::fs::Permissions::from_mode(0o755)).unwrap();
        ensure_private_dir(&d).unwrap();
        assert_eq!(std::fs::metadata(&d).unwrap().permissions().mode() & 0o777, 0o700);
        let nested = d.join("a/b");
        ensure_private_dir(&nested).unwrap();
        assert_eq!(std::fs::metadata(&nested).unwrap().permissions().mode() & 0o777, 0o700);
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd src-tauri && cargo test data_dir::`
Expected: 编译失败（函数未定义）。

- [ ] **Step 3: 实现**

`src-tauri/src/data_dir.rs` 测试模块之前：

```rust
//! 数据目录（spec S1、S2）。默认在系统应用数据目录；用户在设置里改过则从 location.json 读取。
//! location.json 永远放在默认数据目录里、和数据库分开：壳在启动 sidecar 之前就要知道数据在哪。
//! 这个文件只由壳读写（Tauri 命令 get_data_dir / set_data_dir），P4 不要直接写。

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

pub const LOCATION_FILE: &str = "location.json";

#[derive(Debug, Serialize, Deserialize)]
struct LocationFile {
    version: u32,
    #[serde(rename = "dataDir")]
    data_dir: PathBuf,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DataDirInfo {
    /// 实际使用的数据目录。
    pub current: PathBuf,
    /// 系统默认数据目录（location.json 所在处）。
    pub default: PathBuf,
    /// 是否为用户自定义的目录。
    pub custom: bool,
}

fn default_info(default_dir: &Path) -> DataDirInfo {
    DataDirInfo { current: default_dir.into(), default: default_dir.into(), custom: false }
}

/// 读取 location.json。文件不存在 → 默认目录；文件损坏或路径非法 → 报错（绝不悄悄回退到默认目录，
/// 否则用户会看到一个空的应用，以为数据丢了）。
pub fn resolve(default_dir: &Path) -> Result<DataDirInfo, String> {
    let file = default_dir.join(LOCATION_FILE);
    let text = match std::fs::read_to_string(&file) {
        Ok(t) => t,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(default_info(default_dir)),
        Err(e) => return Err(format!("无法读取数据目录配置 {}：{e}", file.display())),
    };
    let loc: LocationFile = serde_json::from_str(&text).map_err(|e| {
        format!("数据目录配置文件 {} 已损坏（{e}）。删除该文件即可恢复使用默认数据目录。", file.display())
    })?;
    if loc.version != 1 {
        return Err(format!("数据目录配置文件 {} 的版本 {} 不受支持", file.display(), loc.version));
    }
    if !loc.data_dir.is_absolute() {
        return Err(format!("数据目录配置文件 {} 里的路径必须是绝对路径：{}", file.display(), loc.data_dir.display()));
    }
    if loc.data_dir == default_dir {
        return Ok(default_info(default_dir));
    }
    Ok(DataDirInfo { current: loc.data_dir, default: default_dir.into(), custom: true })
}

/// 创建目录并把权限设为仅当前用户可读写（Unix 0o700；Windows 的 %APPDATA% 本身按用户隔离）。
pub fn ensure_private_dir(dir: &Path) -> Result<(), String> {
    std::fs::create_dir_all(dir).map_err(|e| format!("无法创建数据目录 {}：{e}", dir.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("无法设置数据目录权限 {}：{e}", dir.display()))?;
    }
    Ok(())
}

/// 启动 sidecar 前调用。自定义目录不存在时报错，不替用户新建空目录（外接盘没插的情况）。
pub fn prepare(info: &DataDirInfo) -> Result<(), String> {
    if info.custom && !info.current.is_dir() {
        return Err(format!(
            "数据目录不可用：{}。如果它在外接磁盘上，请先连接磁盘再点“重试”。",
            info.current.display()
        ));
    }
    ensure_private_dir(&info.current)
}

/// 记录“下次用哪个数据目录”。只写配置，不搬数据（搬数据由 P4 完成）。target 为 None 或等于默认目录时删除配置。
pub fn write_location(default_dir: &Path, target: Option<&Path>) -> Result<DataDirInfo, String> {
    ensure_private_dir(default_dir)?;
    let file = default_dir.join(LOCATION_FILE);
    match target {
        Some(t) if t != default_dir => {
            if !t.is_absolute() {
                return Err(format!("数据目录必须是绝对路径：{}", t.display()));
            }
            if !t.is_dir() {
                return Err(format!("目标目录不存在：{}", t.display()));
            }
            let json = serde_json::to_string_pretty(&LocationFile { version: 1, data_dir: t.into() })
                .map_err(|e| format!("无法生成数据目录配置：{e}"))?;
            let tmp = default_dir.join(format!("{LOCATION_FILE}.tmp"));
            std::fs::write(&tmp, json).map_err(|e| format!("无法写入 {}：{e}", tmp.display()))?;
            std::fs::rename(&tmp, &file).map_err(|e| format!("无法写入 {}：{e}", file.display()))?;
        }
        _ => match std::fs::remove_file(&file) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(format!("无法删除 {}：{e}", file.display())),
        },
    }
    resolve(default_dir)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd src-tauri && cargo test data_dir::`
Expected: 10 个测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add src-tauri/src/data_dir.rs src-tauri/src/lib.rs src-tauri/Cargo.toml src-tauri/Cargo.lock
git commit -m "feat(tauri): 数据目录解析与 location.json（S2），目录权限 0700"
```

---

### Task 4: Windows WebView2 检测与原生弹窗

**Files:**
- Create: `src-tauri/src/webview2.rs`
- Create: `src-tauri/src/native_dialog.rs`
- Modify: `src-tauri/src/lib.rs`（顶部加 `pub mod native_dialog;` 和 `pub mod webview2;`）
- Modify: `src-tauri/Cargo.toml`（加 Windows 专用依赖）

**Interfaces:**
- Consumes: 无
- Produces（T6 使用）：
  - `webview2::WEBVIEW2_DOWNLOAD_URL`、`webview2::missing_message() -> String`、`webview2::is_usable_version(Option<&str>) -> bool`
  - `#[cfg(windows)] webview2::ensure_or_exit()`：检测不到则弹窗（确定 = 打开下载链接）后 `exit(1)`；debug 构建可用环境变量 `MIAOJU_FAKE_NO_WEBVIEW2=1` 模拟缺失
  - `native_dialog::show_error(title: &str, msg: &str)`（Windows 弹窗；其他平台写 stderr）

- [ ] **Step 1: 加依赖**

`src-tauri/Cargo.toml` 末尾追加（版本与 `Cargo.lock` 里 wry 已用的一致，避免重复编译）：

```toml
[target.'cfg(windows)'.dependencies]
webview2-com = "0.39"
windows = { version = "0.62", features = ["Win32_Foundation", "Win32_System_Com", "Win32_UI_WindowsAndMessaging"] }
```

- [ ] **Step 2: 写失败的测试**

`src-tauri/src/webview2.rs` 末尾：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn usable_version_rules() {
        assert!(is_usable_version(Some("129.0.2792.65")));
        assert!(!is_usable_version(None));
        assert!(!is_usable_version(Some("")));
        assert!(!is_usable_version(Some("  ")));
        assert!(!is_usable_version(Some("0.0.0.0")));
    }

    #[test]
    fn message_contains_official_link() {
        let m = missing_message();
        assert!(m.contains(WEBVIEW2_DOWNLOAD_URL));
        assert!(WEBVIEW2_DOWNLOAD_URL.starts_with("https://go.microsoft.com/"));
        assert!(m.contains("WebView2"));
    }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `cd src-tauri && cargo test webview2::`
Expected: 编译失败。

- [ ] **Step 4: 实现 `native_dialog.rs`**

```rust
//! 不依赖 Tauri 的原生弹窗：用在 Tauri 还没起来（WebView2 缺失）或起不来（build 失败）的时候。
//! Windows release 构建没有控制台，不弹窗用户只会看到闪退。

#[cfg(windows)]
fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0)).collect()
}

#[cfg(windows)]
fn message_box(
    title: &str,
    msg: &str,
    style: windows::Win32::UI::WindowsAndMessaging::MESSAGEBOX_STYLE,
) -> windows::Win32::UI::WindowsAndMessaging::MESSAGEBOX_RESULT {
    use windows::core::PCWSTR;
    use windows::Win32::UI::WindowsAndMessaging::MessageBoxW;
    let (t, m) = (wide(title), wide(msg));
    unsafe { MessageBoxW(None, PCWSTR(m.as_ptr()), PCWSTR(t.as_ptr()), style) }
}

/// 显示错误提示（Windows 弹窗；其他平台写 stderr）。
pub fn show_error(title: &str, msg: &str) {
    #[cfg(windows)]
    {
        use windows::Win32::UI::WindowsAndMessaging::{MB_ICONERROR, MB_OK};
        message_box(title, msg, MB_OK | MB_ICONERROR);
    }
    #[cfg(not(windows))]
    eprintln!("{title}：{msg}");
}

/// “确定 / 取消”询问，确定返回 true。
#[cfg(windows)]
pub fn confirm(title: &str, msg: &str) -> bool {
    use windows::Win32::UI::WindowsAndMessaging::{IDOK, MB_ICONWARNING, MB_OKCANCEL};
    message_box(title, msg, MB_OKCANCEL | MB_ICONWARNING) == IDOK
}

/// 用默认浏览器打开链接。
#[cfg(windows)]
pub fn open_url(url: &str) {
    let _ = std::process::Command::new("explorer.exe").arg(url).spawn();
}
```

- [ ] **Step 5: 实现 `webview2.rs`**

在测试模块之前：

```rust
//! Windows：建窗口前检测系统 WebView2 运行时（spec E8）。检测不到就弹窗给出微软官方下载链接，然后退出。
//! 检测用微软推荐的 GetAvailableCoreWebView2BrowserVersionString，常青版和固定版都能识别。

/// 微软官方“常青版引导程序”下载链接。
pub const WEBVIEW2_DOWNLOAD_URL: &str = "https://go.microsoft.com/fwlink/p/?LinkId=2124703";
/// 微软官方 WebView2 说明页。
pub const WEBVIEW2_INFO_URL: &str = "https://developer.microsoft.com/zh-cn/microsoft-edge/webview2/";

pub fn missing_message() -> String {
    format!(
        "喵剧AI 需要 Microsoft Edge WebView2 运行时，但这台电脑上没有检测到。\n\n\
         点击“确定”下载微软官方安装程序，安装完成后重新打开喵剧AI。\n\n\
         下载地址：{WEBVIEW2_DOWNLOAD_URL}\n说明页面：{WEBVIEW2_INFO_URL}"
    )
}

/// 版本字符串可用：非空且不是 "0.0.0.0"。
pub fn is_usable_version(v: Option<&str>) -> bool {
    matches!(v.map(str::trim), Some(s) if !s.is_empty() && s != "0.0.0.0")
}

#[cfg(windows)]
pub fn installed_version() -> Option<String> {
    use webview2_com::Microsoft::Web::WebView2::Win32::GetAvailableCoreWebView2BrowserVersionString;
    use windows::core::{PCWSTR, PWSTR};
    use windows::Win32::System::Com::CoTaskMemFree;

    let mut out = PWSTR::null();
    unsafe {
        GetAvailableCoreWebView2BrowserVersionString(PCWSTR::null(), &mut out).ok()?;
        if out.is_null() {
            return None;
        }
        let s = out.to_string().ok();
        CoTaskMemFree(Some(out.0 as *const _));
        s
    }
}

/// 检测不到 WebView2 就弹窗并退出进程。必须在创建 Tauri 应用之前调用。
#[cfg(windows)]
pub fn ensure_or_exit() {
    let fake_missing = cfg!(debug_assertions) && std::env::var_os("MIAOJU_FAKE_NO_WEBVIEW2").is_some();
    let version = if fake_missing { None } else { installed_version() };
    if is_usable_version(version.as_deref()) {
        return;
    }
    if crate::native_dialog::confirm("缺少 WebView2 运行时", &missing_message()) {
        crate::native_dialog::open_url(WEBVIEW2_DOWNLOAD_URL);
    }
    std::process::exit(1);
}
```

- [ ] **Step 6: 运行确认通过 + Windows 编译**

Run: `cd src-tauri && cargo test webview2:: && cargo xwin check --target x86_64-pc-windows-msvc`
Expected: 2 个测试 PASS；Windows 检查通过。若 `windows` 0.62 的签名与上面不符（例如 `MessageBoxW` 第一个参数类型、`CoTaskMemFree` 参数），按编译器提示调整调用写法，但不改函数对外签名。

- [ ] **Step 7: Commit**

```bash
git add src-tauri/src/webview2.rs src-tauri/src/native_dialog.rs src-tauri/src/lib.rs src-tauri/Cargo.toml src-tauri/Cargo.lock
git commit -m "feat(tauri): Windows 启动前检测 WebView2，缺失时弹窗给出官方下载链接（E8）"
```

---

### Task 5: sidecar 生命周期状态机

**前置：** T2 已合入 main，从合入后的 main 拉分支。

**Files:**
- Create: `src-tauri/src/lifecycle.rs`
- Modify: `src-tauri/src/lib.rs`（顶部加 `pub mod lifecycle;`）

**Interfaces:**
- Consumes（T2）：`sidecar::{launch, LaunchSpec, ExitInfo, ProcessHandle}`，签名见 Task 2。
- Produces（T6 使用）：
  - `pub struct ServerInfo { pub port: u16, pub token: String }`（`Serialize, Clone, Debug, PartialEq`，序列化为 `{port, token}`，与前端 `ServerInfo` 一致）
  - `pub enum SidecarStatus { Starting, Ready, Failed { reason: String } }`，序列化为 `{"status":"starting"}` / `{"status":"ready"}` / `{"status":"failed","reason":"…"}`（不含 token）
  - `pub type SpecFactory = Arc<dyn Fn() -> Result<LaunchSpec, String> + Send + Sync>`
  - `pub type Emitter = Arc<dyn Fn(SidecarStatus) + Send + Sync>`
  - `pub struct Timeouts { pub ready: Duration, pub grace: Duration, pub crash_window: Duration }`，`Default` = 20s / 3s / 60s
  - `SidecarManager::new(make_spec, emit, timeouts) -> Arc<SidecarManager>`；`status(&self) -> SidecarStatus`；`async wait_info(&self) -> Result<ServerInfo, String>`；`async start(self: &Arc<Self>) -> Result<ServerInfo, String>`；`async restart(self: &Arc<Self>) -> Result<ServerInfo, String>`；`async shutdown(&self)`
  - `pub fn new_token() -> String`（64 位十六进制）

- [ ] **Step 1: 写失败的测试**

`src-tauri/src/lifecycle.rs` 末尾：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, new_token());
    }

    #[test]
    fn restart_policy_restarts_once_per_window() {
        let w = Duration::from_secs(60);
        let t0 = Instant::now();
        let mut p = RestartPolicy::default();
        assert_eq!(p.on_crash(t0, w), CrashAction::Restart);
        assert_eq!(p.on_crash(t0 + Duration::from_secs(10), w), CrashAction::GiveUp);
        assert_eq!(p.on_crash(t0 + Duration::from_secs(61), w), CrashAction::Restart);
        p.reset();
        assert_eq!(p.on_crash(t0 + Duration::from_secs(62), w), CrashAction::Restart);
    }

    #[test]
    fn status_serializes_without_token() {
        assert_eq!(serde_json::to_string(&SidecarStatus::Starting).unwrap(), r#"{"status":"starting"}"#);
        assert_eq!(serde_json::to_string(&SidecarStatus::Ready).unwrap(), r#"{"status":"ready"}"#);
        assert_eq!(
            serde_json::to_string(&SidecarStatus::Failed { reason: "x".into() }).unwrap(),
            r#"{"status":"failed","reason":"x"}"#
        );
    }
}

#[cfg(all(test, unix))]
mod manager_tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    const SERVE_4001: &str = r#"read t; echo "READY 4001"; cat >/dev/null"#;
    const SERVE_4002: &str = r#"read t; echo "READY 4002"; cat >/dev/null"#;
    const CRASH_4003: &str = r#"read t; echo "READY 4003"; sleep 0.3; echo boom >&2; exit 9"#;

    fn sh(script: &str) -> LaunchSpec {
        LaunchSpec { program: "/bin/sh".into(), args: vec!["-c".into(), script.into()] }
    }

    fn fast() -> Timeouts {
        Timeouts {
            ready: Duration::from_secs(5),
            grace: Duration::from_millis(500),
            crash_window: Duration::from_secs(60),
        }
    }

    /// 按调用次数依次返回脚本，超出后一直用最后一个。
    fn scripted(scripts: Vec<&'static str>) -> (SpecFactory, Arc<AtomicUsize>) {
        let n = Arc::new(AtomicUsize::new(0));
        let n2 = n.clone();
        let f: SpecFactory = Arc::new(move || {
            let i = n2.fetch_add(1, Ordering::SeqCst);
            Ok(sh(scripts[i.min(scripts.len() - 1)]))
        });
        (f, n)
    }

    fn recorder() -> (Emitter, Arc<std::sync::Mutex<Vec<SidecarStatus>>>) {
        let log = Arc::new(std::sync::Mutex::new(Vec::new()));
        let log2 = log.clone();
        (Arc::new(move |s| log2.lock().unwrap().push(s)), log)
    }

    async fn wait_until(m: &SidecarManager, pred: impl Fn(&SidecarStatus) -> bool) {
        for _ in 0..250 {
            if pred(&m.status()) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        panic!("等待状态超时，当前：{:?}", m.status());
    }

    #[tokio::test]
    async fn start_success_sets_ready_and_emits() {
        let (f, _) = scripted(vec![SERVE_4001]);
        let (emit, log) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        assert_eq!(m.status(), SidecarStatus::Starting);
        let info = m.start().await.unwrap();
        assert_eq!(info.port, 4001);
        assert_eq!(info.token.len(), 64);
        assert_eq!(m.status(), SidecarStatus::Ready);
        assert_eq!(m.wait_info().await.unwrap(), info);
        assert_eq!(*log.lock().unwrap(), vec![SidecarStatus::Starting, SidecarStatus::Ready]);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn start_failure_then_restart_recovers() {
        let (f, _) = scripted(vec![r#"read t; echo bad-config >&2; exit 1"#, SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let err = m.start().await.unwrap_err();
        assert!(err.starts_with("本地服务启动失败") && err.contains("bad-config"), "{err}");
        assert!(matches!(m.status(), SidecarStatus::Failed { reason } if reason.contains("bad-config")));
        assert!(m.wait_info().await.is_err());
        assert_eq!(m.restart().await.unwrap().port, 4001);
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn spec_factory_error_becomes_failed() {
        let f: SpecFactory = Arc::new(|| Err("数据目录不可用：/Volumes/外接盘".to_string()));
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let err = m.start().await.unwrap_err();
        assert!(err.contains("数据目录不可用"), "{err}");
    }

    #[tokio::test]
    async fn crash_after_ready_restarts_once() {
        let (f, n) = scripted(vec![CRASH_4003, SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        assert_eq!(m.start().await.unwrap().port, 4003);
        for _ in 0..250 {
            if n.load(Ordering::SeqCst) == 2 && m.status() == SidecarStatus::Ready {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        assert_eq!(n.load(Ordering::SeqCst), 2);
        assert_eq!(m.wait_info().await.unwrap().port, 4001);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn second_crash_within_window_gives_up() {
        let (f, n) = scripted(vec![CRASH_4003, CRASH_4003]);
        let (emit, log) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        wait_until(&m, |s| matches!(s, SidecarStatus::Failed { .. })).await;
        assert_eq!(n.load(Ordering::SeqCst), 2);
        let SidecarStatus::Failed { reason } = m.status() else { unreachable!() };
        assert!(reason.contains("意外退出") && reason.contains("9") && reason.contains("boom"), "{reason}");
        let log = log.lock().unwrap();
        assert_eq!(log.first(), Some(&SidecarStatus::Starting));
        assert!(matches!(log.last(), Some(SidecarStatus::Failed { .. })));
    }

    #[tokio::test]
    async fn shutdown_does_not_trigger_restart() {
        let (f, n) = scripted(vec![SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        m.shutdown().await;
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 1);
        assert!(m.restart().await.is_err(), "退出中不再拉起");
    }

    #[tokio::test]
    async fn wait_info_blocks_while_starting() {
        let (f, _) = scripted(vec![r#"read t; sleep 0.3; echo "READY 4001"; cat >/dev/null"#]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let m2 = m.clone();
        tokio::spawn(async move { m2.start().await });
        assert_eq!(m.wait_info().await.unwrap().port, 4001);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn restart_replaces_running_process_without_auto_restart() {
        let (f, n) = scripted(vec![SERVE_4001, SERVE_4002]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let a = m.start().await.unwrap();
        let b = m.restart().await.unwrap();
        assert_eq!(b.port, 4002);
        assert_ne!(a.token, b.token, "每次拉起都换 token");
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 2, "旧进程被主动关闭不算崩溃");
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn concurrent_restarts_end_ready() {
        let (f, n) = scripted(vec![SERVE_4001]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        m.start().await.unwrap();
        let (r1, r2) = tokio::join!(m.restart(), m.restart());
        assert!(r1.is_ok() && r2.is_ok());
        tokio::time::sleep(Duration::from_millis(300)).await;
        assert_eq!(n.load(Ordering::SeqCst), 3);
        assert_eq!(m.status(), SidecarStatus::Ready);
        m.shutdown().await;
    }

    #[tokio::test]
    async fn shutdown_while_starting_stops_process() {
        let (f, _) = scripted(vec![r#"read t; sleep 0.3; echo "READY 4001"; cat >/dev/null"#]);
        let (emit, _) = recorder();
        let m = SidecarManager::new(f, emit, fast());
        let m2 = m.clone();
        let starting = tokio::spawn(async move { m2.start().await });
        tokio::time::sleep(Duration::from_millis(50)).await;
        tokio::time::timeout(Duration::from_secs(3), m.shutdown()).await.expect("shutdown 不应卡死");
        let _ = starting.await;
        assert!(m.inner.lock().await.handle.is_none(), "退出后不应留有运行中的进程");
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd src-tauri && cargo test lifecycle::`
Expected: 编译失败。

- [ ] **Step 3: 实现**

`src-tauri/src/lifecycle.rs` 测试模块之前：

```rust
//! sidecar 生命周期状态机：启动中 → 就绪 / 失败。
//! - 启动在后台进行，不阻塞主线程；前端 server_info 会等到“不再是启动中”。
//! - 就绪后 sidecar 崩溃：自动重启一次；距上次自动重启不到 crash_window 又崩 → 失败，由用户点“重试”。
//! - restart()：前端“重试”或 P4 切换数据目录后调用；清零自动重启计数。
//! - shutdown()：应用退出时调用，之后不再拉起。
//! 不依赖 Tauri：状态变化通过 Emitter 回调发出（lib.rs 里转成 `sidecar-state` 事件）。

use std::sync::Arc;
use std::time::{Duration, Instant};

use serde::Serialize;
use tokio::sync::{watch, Mutex};

use crate::sidecar::{launch, ExitInfo, LaunchSpec, ProcessHandle};

#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ServerInfo {
    pub port: u16,
    pub token: String,
}

/// 发给前端的状态（事件载荷和 sidecar_status 命令返回值）。故意不含 token。
#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(tag = "status", rename_all = "lowercase")]
pub enum SidecarStatus {
    Starting,
    Ready,
    Failed { reason: String },
}

#[derive(Clone, Debug, PartialEq)]
enum Phase {
    Starting,
    Ready(ServerInfo),
    Failed(String),
}

impl Phase {
    fn status(&self) -> SidecarStatus {
        match self {
            Phase::Starting => SidecarStatus::Starting,
            Phase::Ready(_) => SidecarStatus::Ready,
            Phase::Failed(r) => SidecarStatus::Failed { reason: r.clone() },
        }
    }
}

pub type SpecFactory = Arc<dyn Fn() -> Result<LaunchSpec, String> + Send + Sync>;
pub type Emitter = Arc<dyn Fn(SidecarStatus) + Send + Sync>;

#[derive(Clone, Copy, Debug)]
pub struct Timeouts {
    /// 等 READY 的最长时间。
    pub ready: Duration,
    /// 关 stdin 后等 sidecar 自行退出的时间，超时强杀。
    pub grace: Duration,
    /// 两次自动重启的最小间隔，间隔内再崩就放弃。
    pub crash_window: Duration,
}

impl Default for Timeouts {
    fn default() -> Self {
        Self {
            ready: Duration::from_secs(20),
            grace: Duration::from_secs(3),
            crash_window: Duration::from_secs(60),
        }
    }
}

#[derive(Debug, PartialEq)]
pub enum CrashAction {
    Restart,
    GiveUp,
}

/// 就绪后崩溃的处理策略：距上次自动重启超过 window（或从未自动重启）就重启，否则放弃。
#[derive(Debug, Default)]
pub struct RestartPolicy {
    last_auto_restart: Option<Instant>,
}

impl RestartPolicy {
    pub fn on_crash(&mut self, now: Instant, window: Duration) -> CrashAction {
        match self.last_auto_restart {
            Some(t) if now.duration_since(t) < window => CrashAction::GiveUp,
            _ => {
                self.last_auto_restart = Some(now);
                CrashAction::Restart
            }
        }
    }

    /// 用户手动重试时清零。
    pub fn reset(&mut self) {
        self.last_auto_restart = None;
    }
}

/// 64 位十六进制随机 token（两个 UUIDv4，来自系统随机源）。
pub fn new_token() -> String {
    format!("{}{}", uuid::Uuid::new_v4().simple(), uuid::Uuid::new_v4().simple())
}

fn crash_reason(info: &ExitInfo) -> String {
    let code = match info.code {
        Some(c) => format!("退出码 {c}"),
        None => "被系统结束".to_string(),
    };
    if info.stderr_tail.is_empty() {
        format!("本地服务意外退出（{code}）")
    } else {
        format!("本地服务意外退出（{code}）：\n{}", info.stderr_tail)
    }
}

struct Inner {
    handle: Option<ProcessHandle>,
    /// 每次拉起 +1；退出通知带着自己的代号，不匹配说明已被新进程取代，忽略。
    generation: u64,
    policy: RestartPolicy,
    closing: bool,
}

pub struct SidecarManager {
    phase: watch::Sender<Phase>,
    inner: Mutex<Inner>,
    make_spec: SpecFactory,
    emit: Emitter,
    timeouts: Timeouts,
}

impl SidecarManager {
    pub fn new(make_spec: SpecFactory, emit: Emitter, timeouts: Timeouts) -> Arc<Self> {
        Arc::new(Self {
            phase: watch::Sender::new(Phase::Starting),
            inner: Mutex::new(Inner {
                handle: None,
                generation: 0,
                policy: RestartPolicy::default(),
                closing: false,
            }),
            make_spec,
            emit,
            timeouts,
        })
    }

    pub fn status(&self) -> SidecarStatus {
        self.phase.borrow().status()
    }

    fn set_phase(&self, p: Phase) {
        let s = p.status();
        self.phase.send_replace(p);
        (self.emit)(s);
    }

    /// 等到不再是“启动中”，返回端口和 token，或失败原因。
    pub async fn wait_info(&self) -> Result<ServerInfo, String> {
        let mut rx = self.phase.subscribe();
        let phase = rx
            .wait_for(|p| *p != Phase::Starting)
            .await
            .map_err(|_| "本地服务已关闭".to_string())?
            .clone();
        match phase {
            Phase::Ready(info) => Ok(info),
            Phase::Failed(reason) => Err(reason),
            Phase::Starting => unreachable!("wait_for 已排除"),
        }
    }

    /// 首次启动。lib.rs 在 setup 里用 tauri::async_runtime::spawn 调用，不阻塞主线程。
    pub async fn start(self: &Arc<Self>) -> Result<ServerInfo, String> {
        let mut inner = self.inner.lock().await;
        self.start_locked(&mut inner).await
    }

    /// 前端“重试” / 切换数据目录后调用：清零自动重启计数，关掉旧进程再拉起。
    pub async fn restart(self: &Arc<Self>) -> Result<ServerInfo, String> {
        let mut inner = self.inner.lock().await;
        inner.policy.reset();
        self.start_locked(&mut inner).await
    }

    /// 应用退出：不再自动重启，优雅关闭当前进程。
    pub async fn shutdown(&self) {
        let mut inner = self.inner.lock().await;
        inner.closing = true;
        if let Some(h) = inner.handle.take() {
            h.shutdown(self.timeouts.grace).await;
        }
    }

    async fn start_locked(self: &Arc<Self>, inner: &mut Inner) -> Result<ServerInfo, String> {
        if inner.closing {
            return Err("应用正在退出".to_string());
        }
        if let Some(old) = inner.handle.take() {
            old.shutdown(self.timeouts.grace).await;
        }
        inner.generation += 1;
        let generation = inner.generation;
        self.set_phase(Phase::Starting);

        let launched = async {
            let spec = (self.make_spec)()?;
            let token = new_token();
            let l = launch(&spec, &token, self.timeouts.ready).await?;
            Ok::<_, String>((ServerInfo { port: l.port, token }, l.handle))
        }
        .await;

        match launched {
            Ok((info, handle)) => {
                self.watch_crash(generation, handle.exited());
                inner.handle = Some(handle);
                self.set_phase(Phase::Ready(info.clone()));
                Ok(info)
            }
            Err(reason) => {
                let reason = format!("本地服务启动失败：{reason}");
                self.set_phase(Phase::Failed(reason.clone()));
                Err(reason)
            }
        }
    }

    fn watch_crash(self: &Arc<Self>, generation: u64, mut exited: watch::Receiver<Option<ExitInfo>>) {
        let me = Arc::clone(self);
        tokio::spawn(async move {
            let info = match exited.wait_for(|v| v.is_some()).await {
                Ok(v) => (*v).clone().expect("wait_for 已保证 Some"),
                Err(_) => return,
            };
            me.on_exit(generation, info).await;
        });
    }

    async fn on_exit(self: &Arc<Self>, generation: u64, info: ExitInfo) {
        let mut inner = self.inner.lock().await;
        // 主动关闭（退出 / 重启）或已被新进程取代：不算崩溃
        if inner.closing || inner.generation != generation {
            return;
        }
        inner.handle = None;
        let reason = crash_reason(&info);
        eprintln!("[sidecar] {reason}");
        match inner.policy.on_crash(Instant::now(), self.timeouts.crash_window) {
            CrashAction::Restart => {
                let _ = self.start_locked(&mut inner).await;
            }
            CrashAction::GiveUp => self.set_phase(Phase::Failed(reason)),
        }
    }
}
```

说明：`restart` 持锁期间关闭旧进程，旧进程的退出通知会在 `on_exit` 里等锁；拿到锁时 `generation` 已变，直接忽略，不会误判为崩溃。

- [ ] **Step 4: 运行确认通过**

Run: `cd src-tauri && cargo test lifecycle::`
Expected: 3 + 10 个测试 PASS。再跑 3 遍确认不抖：`for i in 1 2 3; do cargo test lifecycle:: -q || break; done`。

- [ ] **Step 5: Commit**

```bash
git add src-tauri/src/lifecycle.rs src-tauri/src/lib.rs
git commit -m "feat(tauri): sidecar 生命周期状态机（启动中/就绪/失败、崩溃自动重启一次、重试、优雅退出）"
```

---

### Task 6: 壳接线（命令、事件、数据目录、Origin、退出、WebView2）

**前置：** T2、T3、T4、T5 已合入 main。

**Files:**
- Modify: `src-tauri/src/lib.rs`（整体重写，见下）
- Modify: `src-tauri/Cargo.toml`（删除 `tauri-plugin-shell = "2"`）
- Modify: `src-tauri/capabilities/default.json`（description）

**Interfaces:**
- Consumes：T2 `sidecar::LaunchSpec`；T3 `data_dir::{resolve, prepare, write_location, DataDirInfo}`；T4 `webview2::ensure_or_exit`、`native_dialog::show_error`；T5 `lifecycle::{SidecarManager, ServerInfo, SidecarStatus, SpecFactory, Emitter, Timeouts}`
- Produces（前端 / P4 使用）：
  - 命令 `server_info() -> ServerInfo`（启动中会等待；失败时 reject 原因字符串）
  - 命令 `sidecar_status() -> SidecarStatus`（立即返回）
  - 命令 `restart_sidecar() -> ServerInfo`
  - 命令 `get_data_dir() -> DataDirInfo`、`set_data_dir(path: string | null) -> DataDirInfo`
  - 事件 `sidecar-state`，载荷 `SidecarStatus`
  - `pub const SIDECAR_STATE_EVENT: &str = "sidecar-state"`

- [ ] **Step 1: 写失败的测试**

把 `lib.rs` 的测试模块整体替换为：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_args_exclude_dev_origin() {
        let args = sidecar_args(Path::new("/data"), false);
        assert_eq!(
            args,
            vec!["--data-dir", "/data", "--origins", "tauri://localhost,http://tauri.localhost"]
        );
    }

    #[test]
    fn debug_args_include_dev_origin() {
        let args = sidecar_args(Path::new("/data"), true);
        assert_eq!(args[3], "tauri://localhost,http://tauri.localhost,http://localhost:5173");
    }

    #[test]
    fn sidecar_args_keep_cjk_and_spaces() {
        let args = sidecar_args(Path::new("/Users/张 三/Library/Application Support/com.miaoju.desktop"), false);
        assert_eq!(args[1], "/Users/张 三/Library/Application Support/com.miaoju.desktop");
    }

    #[test]
    fn locate_sidecar_reports_unzip_hint() {
        let dir = std::env::temp_dir().join(format!("miaoju-test-{}", uuid::Uuid::new_v4().simple()));
        std::fs::create_dir_all(&dir).unwrap();
        let err = locate_sidecar(&dir).unwrap_err();
        assert!(err.contains("找不到 sidecar 程序") && err.contains("完整解压"), "{err}");
        let name = format!("sidecar{}", std::env::consts::EXE_SUFFIX);
        std::fs::write(dir.join(&name), b"").unwrap();
        assert_eq!(locate_sidecar(&dir).unwrap(), dir.join(name));
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd src-tauri && cargo test --lib tests::`
Expected: 编译失败（`sidecar_args`、`locate_sidecar` 未定义）。

- [ ] **Step 3: 重写 lib.rs（测试模块之前的全部内容）**

```rust
//! Tauri 薄壳：管理 Go sidecar 的生命周期、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

pub mod data_dir;
pub mod lifecycle;
pub mod native_dialog;
pub mod sidecar;
pub mod webview2;

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use tauri::{Emitter as TauriEmitter, Manager, RunEvent, State};

use lifecycle::{Emitter, ServerInfo, SidecarManager, SidecarStatus, SpecFactory, Timeouts};

/// sidecar 状态变化事件名，与 web/src/shared/server.ts 的 SIDECAR_STATE_EVENT 一致。
pub const SIDECAR_STATE_EVENT: &str = "sidecar-state";

const RELEASE_ORIGINS: &str = "tauri://localhost,http://tauri.localhost";
/// Vite 开发服务器，只在 debug 构建放行。
const DEV_ORIGIN: &str = "http://localhost:5173";

/// 传给 sidecar 的参数。token 不在这里：它只走 stdin（SEC3）。
pub fn sidecar_args(data_dir: &Path, debug: bool) -> Vec<String> {
    let mut origins = RELEASE_ORIGINS.to_string();
    if debug {
        origins.push(',');
        origins.push_str(DEV_ORIGIN);
    }
    vec![
        "--data-dir".into(),
        data_dir.to_string_lossy().into_owned(),
        "--origins".into(),
        origins,
    ]
}

/// Tauri 把 externalBin 放在主程序同目录（dev：target/debug；macOS：Contents/MacOS；Windows zip：exe 同目录）。
pub fn locate_sidecar(exe_dir: &Path) -> Result<PathBuf, String> {
    let path = exe_dir.join(format!("sidecar{}", std::env::consts::EXE_SUFFIX));
    if path.is_file() {
        Ok(path)
    } else {
        Err(format!(
            "找不到 sidecar 程序：{}。如果是 Windows 版，请先把 zip 完整解压到一个文件夹，再运行其中的 miaoju.exe。",
            path.display()
        ))
    }
}

fn sidecar_program() -> Result<PathBuf, String> {
    let exe = tauri::utils::platform::current_exe().map_err(|e| format!("无法定位主程序：{e}"))?;
    let dir = exe.parent().ok_or_else(|| "无法定位主程序所在目录".to_string())?;
    locate_sidecar(dir)
}

fn default_data_dir(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    app.path().app_data_dir().map_err(|e| format!("无法确定数据目录：{e}"))
}

#[tauri::command]
async fn server_info(m: State<'_, Arc<SidecarManager>>) -> Result<ServerInfo, String> {
    m.wait_info().await
}

#[tauri::command]
fn sidecar_status(m: State<'_, Arc<SidecarManager>>) -> SidecarStatus {
    m.status()
}

#[tauri::command]
async fn restart_sidecar(m: State<'_, Arc<SidecarManager>>) -> Result<ServerInfo, String> {
    m.inner().restart().await
}

#[tauri::command]
fn get_data_dir(app: tauri::AppHandle) -> Result<data_dir::DataDirInfo, String> {
    data_dir::resolve(&default_data_dir(&app)?)
}

/// 只改“下次启动用哪个数据目录”，不搬数据。P4 迁移完数据后调用，再调 restart_sidecar 生效。
/// path 为 null 表示恢复默认目录。
#[tauri::command]
fn set_data_dir(app: tauri::AppHandle, path: Option<String>) -> Result<data_dir::DataDirInfo, String> {
    let default = default_data_dir(&app)?;
    data_dir::write_location(&default, path.as_deref().map(Path::new))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // E8：建窗口前检测 WebView2，缺失则弹窗并退出。
    #[cfg(windows)]
    webview2::ensure_or_exit();

    let built = tauri::Builder::default()
        // 单实例插件必须最先注册：第二次启动时只把已有窗口带到前台（spec S11）。
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .setup(|app| {
            let handle = app.handle().clone();

            let spec_app = handle.clone();
            let make_spec: SpecFactory = Arc::new(move || {
                let info = data_dir::resolve(&default_data_dir(&spec_app)?)?;
                data_dir::prepare(&info)?;
                Ok(sidecar::LaunchSpec {
                    program: sidecar_program()?,
                    args: sidecar_args(&info.current, cfg!(debug_assertions)),
                })
            });

            let emit_app = handle.clone();
            let emit: Emitter = Arc::new(move |status| {
                let _ = emit_app.emit(SIDECAR_STATE_EVENT, status);
            });

            let manager = SidecarManager::new(make_spec, emit, Timeouts::default());
            app.manage(manager.clone());
            // 异步启动：窗口立即出现，前端显示“正在启动本地服务…”。
            tauri::async_runtime::spawn(async move {
                let _ = manager.start().await;
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            server_info,
            sidecar_status,
            restart_sidecar,
            get_data_dir,
            set_data_dir
        ])
        .build(tauri::generate_context!());

    let app = match built {
        Ok(app) => app,
        Err(e) => {
            // 兜底：Windows release 没有控制台，必须弹窗，否则用户只看到闪退。
            native_dialog::show_error("喵剧AI 启动失败", &format!("{e}"));
            std::process::exit(1);
        }
    };

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            let manager = app.state::<Arc<SidecarManager>>().inner().clone();
            // 关 stdin → 等 sidecar 收尾（最多 3 秒）→ 强杀。整体再加上限，避免卡在“启动中”的锁上；
            // 即便超时，壳退出后 stdin 管道关闭，sidecar 也会自行退出。
            let _ = tauri::async_runtime::block_on(async move {
                tokio::time::timeout(Duration::from_secs(6), manager.shutdown()).await
            });
        }
    });
}
```

同时删除旧代码：`Sidecar` 结构体、旧 `server_info`、`parse_ready`、`new_token`、`READY_TIMEOUT`、`wait_ready`、`start_sidecar` 以及它们的测试（已分别迁到 `sidecar.rs`、`lifecycle.rs`）。

- [ ] **Step 4: 删除 shell 插件依赖、更新 capability 描述**

`src-tauri/Cargo.toml` 删除 `tauri-plugin-shell = "2"` 一行。

`src-tauri/capabilities/default.json`：

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "主窗口的最小权限：core:default（含事件监听、getVersion）。自定义命令 server_info / sidecar_status / restart_sidecar / get_data_dir / set_data_dir 由本应用注册，默认只对本应用窗口开放。文件对话框、在访达中显示等权限由后续阶段按需添加（SEC4）。",
  "windows": ["main"],
  "permissions": ["core:default"]
}
```

- [ ] **Step 5: 运行全部测试与编译**

Run: `cd src-tauri && cargo test && cargo build && cargo xwin check --target x86_64-pc-windows-msvc`
Expected: 全部测试 PASS（lib 4 个 + sidecar 12 个 + data_dir 10 个 + webview2 2 个 + lifecycle 13 个 + tests/version 2 个）；`cargo build` 无警告；`grep -rn plugin_shell src-tauri/src` 无输出。

- [ ] **Step 6: 冒烟（需要 T7 已合入以看到重试按钮；没有 T7 时只核对日志和进程）**

Run: `pnpm dev`（另开终端执行下面的检查）

1. 窗口立即出现，先显示“正在启动本地服务…”，随后进入诊断页。
2. `ps -o args= -p "$(pgrep -f 'target/debug/sidecar')"` → 含 `--origins tauri://localhost,http://tauri.localhost,http://localhost:5173`，不含 token。
3. `stat -f %Lp "$HOME/Library/Application Support/com.miaoju.desktop"` → `700`。
4. `pkill -9 -f target/debug/sidecar` → 终端出现“本地服务意外退出”，几秒内诊断页自动恢复；`pgrep -f target/debug/sidecar` 是新 pid。
5. 60 秒内再 `pkill -9` 两次 → 进入失败页，原因含“意外退出”；点“重试” → 恢复。
6. `echo '{"version":1,"dataDir":"/Volumes/不存在的盘"}' > "$HOME/Library/Application Support/com.miaoju.desktop/location.json"`，在失败页点“重试” → 原因“数据目录不可用：/Volumes/不存在的盘…”；`rm` 该文件后点“重试” → 恢复。
7. Cmd+Q 退出 → 4 秒内 `pgrep -f target/debug/sidecar` 无输出。

- [ ] **Step 7: Commit**

```bash
git add src-tauri/src/lib.rs src-tauri/Cargo.toml src-tauri/Cargo.lock src-tauri/capabilities/default.json
git commit -m "feat(tauri): 接入生命周期状态机、数据目录命令、debug 才放行开发源、优雅退出与 WebView2 检测"
```

---

### Task 7: 前端 sidecar 门控（启动中 / 失败 + 重试 / 就绪）

**Files:**
- Modify: `web/src/shared/server.ts`
- Modify: `web/src/shared/server.test.ts`
- Create: `web/src/shared/sidecarGate.ts`
- Create: `web/src/shared/sidecarGate.test.ts`
- Create: `web/src/shared/SidecarGate.tsx`
- Modify: `web/src/diag/DiagPage.tsx`

**Interfaces:**
- Consumes（T6 的命令和事件，按本计划约定编码，无需等 T6）：`server_info`、`sidecar_status`、`restart_sidecar`、事件 `sidecar-state`（载荷 `{status:'starting'|'ready'} | {status:'failed', reason}`）
- Produces（P3a、P4 使用）：`getSidecarStatus(): Promise<SidecarStatus>`、`restartSidecar(): Promise<ServerInfo>`、`onSidecarStatus(cb): Promise<UnlistenFn>`、`SIDECAR_STATE_EVENT`、`type SidecarStatus`、组件 `SidecarGate`

- [ ] **Step 1: 写失败的测试（server.ts）**

`web/src/shared/server.test.ts` 顶部的 mock 改为：

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

let eventHandler: ((e: { payload: unknown }) => void) | null = null;
const unlisten = vi.fn();
vi.mock('@tauri-apps/api/event', () => ({
  listen: (name: string, handler: (e: { payload: unknown }) => void) => {
    expect(name).toBe('sidecar-state');
    eventHandler = handler;
    return Promise.resolve(unlisten);
  },
}));

import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  getServerInfo,
  getSidecarStatus,
  onSidecarStatus,
  resetServerInfoForTest,
  restartSidecar,
} from './server';
```

`beforeEach` 里加 `eventHandler = null;`。文件末尾追加：

```ts
describe('sidecar 状态', () => {
  it('getSidecarStatus 调用 sidecar_status', async () => {
    invoke.mockResolvedValue({ status: 'ready' });
    expect(await getSidecarStatus()).toEqual({ status: 'ready' });
    expect(invoke).toHaveBeenCalledWith('sidecar_status');
  });

  it('restartSidecar 调用 restart_sidecar，并把新端口写入缓存', async () => {
    invoke.mockResolvedValueOnce(info);
    await getServerInfo();
    const fresh = { port: 60000, token: 'new' };
    invoke.mockResolvedValueOnce(fresh);
    expect(await restartSidecar()).toEqual(fresh);
    expect(invoke).toHaveBeenLastCalledWith('restart_sidecar');
    expect(await getServerInfo()).toEqual(fresh);
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('restartSidecar 失败不缓存', async () => {
    invoke.mockRejectedValueOnce('本地服务启动失败：x');
    await expect(restartSidecar()).rejects.toBe('本地服务启动失败：x');
    invoke.mockResolvedValueOnce(info);
    expect(await getServerInfo()).toEqual(info);
  });

  it('收到状态事件时回调并清掉端口缓存', async () => {
    invoke.mockResolvedValue(info);
    await getServerInfo();
    const seen: unknown[] = [];
    const un = await onSidecarStatus((s) => seen.push(s));
    eventHandler!({ payload: { status: 'starting' } });
    expect(seen).toEqual([{ status: 'starting' }]);
    await getServerInfo();
    expect(invoke).toHaveBeenCalledTimes(2); // 缓存已清，重新调用 server_info
    un();
    expect(unlisten).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: 写失败的测试（门控状态机）**

`web/src/shared/sidecarGate.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import { gateReducer, initialGateState } from './sidecarGate';

const info = { port: 4001, token: 't' };

describe('gateReducer', () => {
  it('初始为启动中，epoch 0', () => {
    expect(initialGateState).toEqual({ epoch: 0, view: { kind: 'starting' } });
  });

  it('同一 epoch 的 info 进入就绪', () => {
    expect(gateReducer(initialGateState, { type: 'info', epoch: 0, info })).toEqual({
      epoch: 0,
      view: { kind: 'ready', info },
    });
  });

  it('过期 epoch 的 info / error 被忽略', () => {
    const s = gateReducer(initialGateState, { type: 'status', status: { status: 'starting' } });
    expect(s.epoch).toBe(1);
    expect(gateReducer(s, { type: 'info', epoch: 0, info })).toBe(s);
    expect(gateReducer(s, { type: 'error', epoch: 0, reason: 'x' })).toBe(s);
  });

  it('failed 事件进入失败页', () => {
    const s = gateReducer(initialGateState, { type: 'status', status: { status: 'failed', reason: '崩了' } });
    expect(s).toEqual({ epoch: 1, view: { kind: 'failed', reason: '崩了' } });
  });

  it('ready 事件不带 token：回到启动中，等组件取到 info', () => {
    const ready = gateReducer(initialGateState, { type: 'info', epoch: 0, info });
    const s = gateReducer(ready, { type: 'status', status: { status: 'ready' } });
    expect(s).toEqual({ epoch: 1, view: { kind: 'starting' } });
  });

  it('同一 epoch 的 error 进入失败页', () => {
    expect(gateReducer(initialGateState, { type: 'error', epoch: 0, reason: '启动超时' })).toEqual({
      epoch: 0,
      view: { kind: 'failed', reason: '启动超时' },
    });
  });
});
```

- [ ] **Step 3: 运行确认失败**

Run: `pnpm --dir web test`
Expected: 新测试 FAIL（导出不存在）。

- [ ] **Step 4: 实现 server.ts**

在 `server.ts` 中：import 区加 `import { listen, type UnlistenFn } from '@tauri-apps/api/event';`，把 `getServerInfo` 改为复用 `track`，并追加新导出：

```ts
let cached: Promise<ServerInfo> | null = null;

/** 记入缓存；失败时清掉，下次调用会重试。 */
function track(pending: Promise<ServerInfo>): Promise<ServerInfo> {
  cached = pending;
  pending.catch(() => {
    if (cached === pending) cached = null;
  });
  return pending;
}

/** 取 sidecar 的端口和 token。sidecar 启动中时会等到就绪或失败。成功后缓存；失败不缓存。 */
export function getServerInfo(): Promise<ServerInfo> {
  return cached ?? track(invoke<ServerInfo>('server_info'));
}

/** sidecar 状态，与 src-tauri/src/lifecycle.rs 的 SidecarStatus 一致（不含 token）。 */
export type SidecarStatus =
  | { status: 'starting' }
  | { status: 'ready' }
  | { status: 'failed'; reason: string };

/** 壳广播 sidecar 状态变化的事件名，与 src-tauri/src/lib.rs 的 SIDECAR_STATE_EVENT 一致。 */
export const SIDECAR_STATE_EVENT = 'sidecar-state';

export function getSidecarStatus(): Promise<SidecarStatus> {
  return invoke<SidecarStatus>('sidecar_status');
}

/** 重启 sidecar（失败页“重试”、切换数据目录后）。端口和 token 都会变，缓存随之更新。 */
export function restartSidecar(): Promise<ServerInfo> {
  return track(invoke<ServerInfo>('restart_sidecar'));
}

/** 订阅 sidecar 状态变化。任何变化都清掉端口缓存：重启后端口和 token 都会变。 */
export function onSidecarStatus(cb: (s: SidecarStatus) => void): Promise<UnlistenFn> {
  return listen<SidecarStatus>(SIDECAR_STATE_EVENT, (e) => {
    cached = null;
    cb(e.payload);
  });
}
```

（删掉原 `getServerInfo` 里内联的缓存逻辑，其余函数不变。）

- [ ] **Step 5: 实现 `sidecarGate.ts`**

```ts
// SidecarGate 的纯状态机。epoch 在每次收到状态事件时 +1；
// 异步取回的 info / error 带着发起时的 epoch，过期的结果直接丢弃（防止重启后用到旧端口）。
import type { ServerInfo, SidecarStatus } from './server';

export type GateView =
  | { kind: 'starting' }
  | { kind: 'ready'; info: ServerInfo }
  | { kind: 'failed'; reason: string };

export interface GateState {
  epoch: number;
  view: GateView;
}

export type GateEvent =
  | { type: 'status'; status: SidecarStatus }
  | { type: 'info'; epoch: number; info: ServerInfo }
  | { type: 'error'; epoch: number; reason: string };

export const initialGateState: GateState = { epoch: 0, view: { kind: 'starting' } };

export function gateReducer(s: GateState, e: GateEvent): GateState {
  switch (e.type) {
    case 'status':
      if (e.status.status === 'failed') {
        return { epoch: s.epoch + 1, view: { kind: 'failed', reason: e.status.reason } };
      }
      // starting 和 ready 都先进入“启动中”：ready 事件不带 token，由组件随后调用 getServerInfo 取回
      return { epoch: s.epoch + 1, view: { kind: 'starting' } };
    case 'info':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'ready', info: e.info } } : s;
    case 'error':
      return e.epoch === s.epoch ? { epoch: s.epoch, view: { kind: 'failed', reason: e.reason } } : s;
  }
}
```

- [ ] **Step 6: 实现 `SidecarGate.tsx`**

```tsx
// 所有依赖本地服务的界面都包在 SidecarGate 里：
// 启动中显示等待；失败显示原因和“重试”；就绪后把 info 交给子组件。
// sidecar 重启后端口会变，子树以端口为 key 重新挂载，避免继续用旧地址。
import { Fragment, useEffect, useReducer, type ReactNode } from 'react';
import { getServerInfo, onSidecarStatus, restartSidecar, type ServerInfo } from './server';
import { gateReducer, initialGateState } from './sidecarGate';

export function SidecarGate({ children }: { children: (info: ServerInfo) => ReactNode }) {
  const [state, dispatch] = useReducer(gateReducer, initialGateState);

  useEffect(() => {
    let unlisten: (() => void) | undefined;
    let disposed = false;
    void onSidecarStatus((status) => dispatch({ type: 'status', status })).then((u) => {
      if (disposed) u();
      else unlisten = u;
    });
    return () => {
      disposed = true;
      unlisten?.();
    };
  }, []);

  useEffect(() => {
    if (state.view.kind !== 'starting') return;
    const epoch = state.epoch;
    getServerInfo().then(
      (info) => dispatch({ type: 'info', epoch, info }),
      (e) => dispatch({ type: 'error', epoch, reason: String(e) }),
    );
  }, [state.epoch, state.view.kind]);

  const retry = () => {
    // 先发起重启（写入缓存），再切到“启动中”让组件等待新的 info
    void restartSidecar().catch(() => undefined);
    dispatch({ type: 'status', status: { status: 'starting' } });
  };

  if (state.view.kind === 'starting') {
    return <div style={{ padding: 24 }}>正在启动本地服务…</div>;
  }
  if (state.view.kind === 'failed') {
    return (
      <div style={{ padding: 24 }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88', userSelect: 'text' }}>{state.view.reason}</pre>
        <button onClick={retry}>重试</button>
      </div>
    );
  }
  return <Fragment key={state.view.info.port}>{children(state.view.info)}</Fragment>;
}
```

- [ ] **Step 7: DiagPage 改用 SidecarGate**

`web/src/diag/DiagPage.tsx`：删除 `info` / `startError` 两个 state、`getServerInfo` 的 `useEffect` 以及“本地服务未启动”和“正在连接本地服务…”两段早返回；把剩余主体改名为 `DiagBody({ info }: { info: ServerInfo })`，并新增：

```tsx
import { SidecarGate } from '@/shared/SidecarGate';
import type { ServerInfo } from '@/shared/server';

export function DiagPage() {
  return <SidecarGate>{(info) => <DiagBody info={info} />}</SidecarGate>;
}
```

`DiagBody` 的函数体就是原 `DiagPage` 中 `const runBasic = …` 起到末尾 `return (…)` 的部分，`useState`（`results`、`busy`）移到 `DiagBody` 顶部。

- [ ] **Step 8: 运行确认通过**

Run: `pnpm --dir web test && pnpm --dir web build`
Expected: 全部测试 PASS（原 7 个 + server 新增 4 个 + gate 6 个）；build 通过（`tsc --noEmit` 无错误）。

- [ ] **Step 9: Commit**

```bash
git add web/src/shared web/src/diag/DiagPage.tsx
git commit -m "feat(web): SidecarGate 统一处理本地服务启动中/失败重试/重启后重挂"
```

---

### Task 8: macOS 发布（aarch64 + x64 dmg，附首次打开说明）

**Files:**
- Create: `scripts/prebuild.sh`
- Create: `scripts/release-macos.sh`
- Create: `packaging/首次打开说明-macOS.txt`
- Modify: `package.json`（scripts）

**Interfaces:**
- Consumes：T1 `bash scripts/version.sh check`（未合入时该步会因脚本不存在而失败，可先注释该行本地验证，合并前恢复）；P6 的 `scripts/prebuild.d/*.sh`（可不存在）
- Produces：`bash scripts/prebuild.sh <triple>`；`bash scripts/release-macos.sh [aarch64|x64|all]` → `out/喵剧AI_<ver>_aarch64.dmg`、`out/喵剧AI_<ver>_x64.dmg`

- [ ] **Step 1: 写 `scripts/prebuild.sh`（T9 写入逐字节相同的内容）**

```bash
#!/usr/bin/env bash
# 打包前准备：编译对应平台的 sidecar，再按文件名顺序执行 scripts/prebuild.d/*.sh <target-triple>
# （其他阶段的资源准备钩子，例如 P6 的 Python 运行时）。
# 用法：bash scripts/prebuild.sh <target-triple>
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:?用法: bash scripts/prebuild.sh <target-triple>}"
bash scripts/build-sidecar.sh "$TARGET"

shopt -s nullglob
for hook in scripts/prebuild.d/*.sh; do
  echo "==> $hook $TARGET"
  bash "$hook" "$TARGET"
done
```

- [ ] **Step 2: 写说明文件 `packaging/首次打开说明-macOS.txt`**

```text
喵剧AI 首次打开说明（macOS）

喵剧AI 没有购买苹果开发者证书，第一次打开时 macOS 会拦截，这是正常现象。
按下面的步骤放行一次，以后就能正常打开。

一、选对安装包
  Apple 芯片（M1、M2、M3、M4……）用文件名带 aarch64 的安装包；
  Intel 芯片用文件名带 x64 的安装包。
  不确定的话：点屏幕左上角苹果菜单 →“关于本机”，看“芯片”或“处理器”一栏。

二、安装
  把窗口里的“喵剧AI”拖到右边的“Applications（应用程序）”文件夹。

三、第一次打开
  1. 在“应用程序”里双击“喵剧AI”。系统提示“无法打开”或“Apple 无法验证……”，点“完成”或“取消”。
  2. 打开“系统设置”→“隐私与安全性”，滚动到最下面的“安全性”部分，
     能看到“已阻止‘喵剧AI’以保护 Mac”，点右边的“仍要打开”，输入开机密码确认。
  3. 再次双击“喵剧AI”，在弹出的对话框里点“仍要打开”。

四、如果提示“‘喵剧AI’已损坏，无法打开”
  打开“终端”（在“应用程序 → 实用工具”里），粘贴下面这一行并回车，按提示输入开机密码：
    sudo xattr -dr com.apple.quarantine /Applications/喵剧AI.app
  然后重新打开喵剧AI。

五、升级到新版本
  直接把新版“喵剧AI”拖进“应用程序”并选择“替换”，数据不会丢失
  （数据保存在 ~/Library/Application Support/com.miaoju.desktop）。
  替换后第一次打开可能需要再按第三步放行一次。

系统要求：macOS 14 或更高版本。
```

- [ ] **Step 3: 写 `scripts/release-macos.sh`**

```bash
#!/usr/bin/env bash
# 构建 macOS 发布包：out/喵剧AI_<版本>_aarch64.dmg（Apple 芯片）和 out/喵剧AI_<版本>_x64.dmg（Intel）。
# 不做开发者签名和公证（spec E5）：Tauri 按 signingIdentity "-" 做 ad-hoc 签名；dmg 内附《首次打开说明.txt》。
# Tauri 的 dmg 配置不能往卷里放额外文件，所以 Tauri 只出 .app，dmg 由 hdiutil 生成。
# 用法：bash scripts/release-macos.sh [aarch64|x64|all]（默认 all）
set -euo pipefail
cd "$(dirname "$0")/.."

WHICH="${1:-all}"
PRODUCT="喵剧AI"
bash scripts/version.sh check
VERSION="$(node -p "require('./package.json').version")"
mkdir -p out

verify_dmg() {
  local dmg="$1" lipo_arch="$2" mnt
  mnt="$(mktemp -d)"
  hdiutil attach -nobrowse -readonly -mountpoint "$mnt" "$dmg" >/dev/null
  local ok=1
  [ -d "$mnt/$PRODUCT.app" ] || { echo "dmg 里缺少 $PRODUCT.app" >&2; ok=0; }
  [ -L "$mnt/Applications" ] || { echo "dmg 里缺少 Applications 快捷方式" >&2; ok=0; }
  [ -f "$mnt/首次打开说明.txt" ] || { echo "dmg 里缺少 首次打开说明.txt" >&2; ok=0; }
  codesign --verify --deep --strict "$mnt/$PRODUCT.app" || ok=0
  for bin in miaoju sidecar; do
    [ "$(lipo -archs "$mnt/$PRODUCT.app/Contents/MacOS/$bin")" = "$lipo_arch" ] \
      || { echo "$bin 架构不是 $lipo_arch" >&2; ok=0; }
  done
  hdiutil detach "$mnt" >/dev/null
  rmdir "$mnt"
  [ "$ok" = 1 ]
}

build_one() {
  local arch="$1" target lipo_arch
  case "$arch" in
    aarch64) target=aarch64-apple-darwin; lipo_arch=arm64 ;;
    x64)     target=x86_64-apple-darwin;  lipo_arch=x86_64 ;;
    *) echo "未知架构：$arch（可选 aarch64 / x64 / all）" >&2; exit 1 ;;
  esac

  bash scripts/prebuild.sh "$target"
  cargo tauri build --target "$target" --bundles app

  local app="src-tauri/target/$target/release/bundle/macos/$PRODUCT.app"
  [ -d "$app" ] || { echo "缺少构建产物 $app" >&2; exit 1; }
  # 签名必须完整且是 ad-hoc；资源要在 Tauri 签名前进包，签名后改动包内容会让 macOS 报“已损坏”
  codesign --verify --deep --strict "$app"
  codesign -dv "$app" 2>&1 | grep -q 'Signature=adhoc' || { echo "$app 不是 ad-hoc 签名" >&2; exit 1; }
  [ "$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$app/Contents/Info.plist")" = "14.0" ] \
    || { echo "LSMinimumSystemVersion 不是 14.0" >&2; exit 1; }
  [ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")" = "$VERSION" ] \
    || { echo "包内版本号与 package.json 不一致" >&2; exit 1; }

  local stage="out/dmg-stage-$arch" dmg="out/${PRODUCT}_${VERSION}_${arch}.dmg"
  rm -rf "$stage" "$dmg"
  mkdir -p "$stage"
  ditto "$app" "$stage/$PRODUCT.app"
  ln -s /Applications "$stage/Applications"
  cp "packaging/首次打开说明-macOS.txt" "$stage/首次打开说明.txt"
  hdiutil create -volname "$PRODUCT $VERSION" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg" >/dev/null
  rm -rf "$stage"

  verify_dmg "$dmg" "$lipo_arch"
  echo "已生成 $dmg（$(du -h "$dmg" | cut -f1)）"
}

case "$WHICH" in
  all) build_one aarch64; build_one x64 ;;
  aarch64|x64) build_one "$WHICH" ;;
  *) echo "用法: bash scripts/release-macos.sh [aarch64|x64|all]" >&2; exit 1 ;;
esac
```

- [ ] **Step 4: 改 `package.json` 的 scripts**

```json
  "scripts": {
    "dev": "bash scripts/build-sidecar.sh && cargo tauri dev",
    "build": "bash scripts/release-macos.sh aarch64",
    "release:mac": "bash scripts/release-macos.sh all",
    "release:win": "bash scripts/package-windows.sh",
    "release": "bash scripts/release-macos.sh all && bash scripts/package-windows.sh",
    "version:check": "bash scripts/version.sh check",
    "test:web": "pnpm --dir web test",
    "test:server": "cd server && go test ./...",
    "test:tauri": "cd src-tauri && cargo test",
    "test:scripts": "node --test scripts/lib/*.test.mjs"
  }
```

- [ ] **Step 5: 运行并验证**

Run: `bash scripts/release-macos.sh all`
Expected: 结尾两行 `已生成 out/喵剧AI_0.1.0_aarch64.dmg（…）`、`已生成 out/喵剧AI_0.1.0_x64.dmg（…）`；脚本内的 `verify_dmg` 全部通过（无报错即通过）。

Run: `bash scripts/release-macos.sh foo; echo "exit=$?"`
Expected: 输出“未知架构”或用法提示，`exit=1`。

本机打开 aarch64 dmg，把 app 拖到“应用程序”，用 `xattr -w com.apple.quarantine "0081;$(printf %x $(date +%s));Safari;" /Applications/喵剧AI.app` 模拟下载隔离后双击，确认拦截流程与说明第三步一致（系统设置里出现“仍要打开”）。

- [ ] **Step 6: Commit**

```bash
git add scripts/prebuild.sh scripts/release-macos.sh "packaging/首次打开说明-macOS.txt" package.json
git commit -m "build(macos): 发布脚本产出 aarch64/x64 两个 dmg（ad-hoc 签名、内附首次打开说明）"
```

---

### Task 9: Windows zip（资源复制、首次打开说明、STATIC_VCRUNTIME）

**Files:**
- Modify: `scripts/package-windows.sh`
- Create: `scripts/prebuild.sh`（与 Task 8 Step 1 **逐字节相同**；若 T8 已合入则跳过）
- Create: `scripts/lib/list-resources.mjs`
- Create: `scripts/lib/list-resources.test.mjs`
- Create: `scripts/lib/make_zip.py`
- Create: `packaging/首次打开说明-Windows.txt`

**Interfaces:**
- Consumes：T1 `bash scripts/version.sh check`；`tauri.conf.json` 的 `bundle.resources`（P6 添加，映射形式）
- Produces：`out/miaoju-windows-x64-<ver>.zip`，顶层目录 `miaoju-windows-x64-<ver>/`，内含 `miaoju.exe`、`sidecar.exe`、`首次打开说明.txt`、以及 `bundle.resources` 的每个目标路径

- [ ] **Step 1: 写失败的测试**

`scripts/lib/list-resources.test.mjs`：

```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { listResources } from './list-resources.mjs';

test('没有 resources 返回空', () => {
  assert.deepEqual(listResources({ bundle: {} }), []);
});

test('映射形式按 [src, dest] 输出', () => {
  const conf = { bundle: { resources: { 'resources/python/': 'python/', 'resources/a.txt': 'cfg/a.txt' } } };
  assert.deepEqual(listResources(conf), [
    ['resources/python/', 'python/'],
    ['resources/a.txt', 'cfg/a.txt'],
  ]);
});

test('数组形式报错：zip 打包需要明确目标路径', () => {
  assert.throws(() => listResources({ bundle: { resources: ['a/*'] } }), /映射形式/);
});

test('通配符、绝对路径、.. 报错', () => {
  assert.throws(() => listResources({ bundle: { resources: { 'a/*': 'a/' } } }), /通配符/);
  assert.throws(() => listResources({ bundle: { resources: { a: '/abs' } } }), /目标路径无效/);
  assert.throws(() => listResources({ bundle: { resources: { a: '../up' } } }), /目标路径无效/);
});
```

- [ ] **Step 2: 运行确认失败**

Run: `node --test scripts/lib/list-resources.test.mjs`
Expected: FAIL（模块不存在）。

- [ ] **Step 3: 实现 `scripts/lib/list-resources.mjs`**

```js
// 输出 tauri.conf.json 里 bundle.resources 的 {源: 目标} 映射，每行 "源\t目标"。
// Windows zip 用 --no-bundle 构建，Tauri 不会复制资源；package-windows.sh 按这里的输出手动复制到 exe 同目录
// （Windows 未打包时 Tauri 的 resource_dir() 就是 exe 所在目录）。
// 用法：node scripts/lib/list-resources.mjs src-tauri/tauri.conf.json
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function listResources(conf) {
  const r = conf?.bundle?.resources;
  if (r == null) return [];
  if (Array.isArray(r)) {
    throw new Error('bundle.resources 请用 {"源路径": "目标路径"} 映射形式：Windows zip 打包需要明确的目标路径');
  }
  return Object.entries(r).map(([src, dest]) => {
    if (src.includes('*')) throw new Error(`bundle.resources 的源路径不支持通配符：${src}`);
    if (
      typeof dest !== 'string' ||
      dest === '' ||
      dest.startsWith('/') ||
      /^[A-Za-z]:/.test(dest) ||
      dest.split(/[\\/]/).includes('..')
    ) {
      throw new Error(`bundle.resources 目标路径无效：${src} → ${dest}`);
    }
    return [src, dest];
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const conf = JSON.parse(readFileSync(process.argv[2], 'utf8'));
  for (const [src, dest] of listResources(conf)) console.log(`${src}\t${dest}`);
}
```

- [ ] **Step 4: 运行确认通过**

Run: `node --test scripts/lib/list-resources.test.mjs && node scripts/lib/list-resources.mjs src-tauri/tauri.conf.json`
Expected: 4 个测试 PASS；第二条命令无输出（当前没有 resources）。

- [ ] **Step 5: 实现 `scripts/lib/make_zip.py`**

```python
"""把目录打成 zip，zip 内保留顶层目录名。
用 python 的 zipfile：文件名含中文时会自动设置 UTF-8 标记，Windows 资源管理器解压才不会乱码。
用法：python3 scripts/lib/make_zip.py <目录> <输出.zip>
"""
import os
import sys
import zipfile


def main() -> None:
    src, out = sys.argv[1], sys.argv[2]
    base = os.path.dirname(os.path.abspath(src))
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for root, dirs, files in os.walk(src):
            dirs.sort()
            for name in sorted(files):
                path = os.path.join(root, name)
                z.write(path, os.path.relpath(path, base))


if __name__ == "__main__":
    main()
```

- [ ] **Step 6: 写说明文件 `packaging/首次打开说明-Windows.txt`**

```text
喵剧AI 首次打开说明（Windows）

一、先解压，再运行
  右键这个 zip 文件 →“全部解压缩”，打开解压出来的文件夹，双击 miaoju.exe。
  不要在压缩包里直接双击运行，也不要只把 miaoju.exe 单独拷走——
  它需要同一个文件夹里的 sidecar.exe 等文件，否则会提示“找不到 sidecar 程序”。

二、如果出现蓝色窗口“Windows 已保护你的电脑”
  喵剧AI 没有购买代码签名证书，Windows SmartScreen 会拦截，这是正常现象。
  点“更多信息”，再点“仍要运行”。之后打开就不会再提示。

三、如果提示缺少 WebView2 运行时
  点“确定”会打开微软官方下载链接，安装完成后重新打开喵剧AI。
  也可以手动下载：https://go.microsoft.com/fwlink/p/?LinkId=2124703
  Windows 11 和较新的 Windows 10 已经自带，一般不会遇到。

四、如果杀毒软件拦截 sidecar.exe
  请把它加入信任。它是喵剧AI 的本地服务，只在本机 127.0.0.1 上工作。

五、升级到新版本
  关闭喵剧AI，删掉旧的文件夹，解压新版本即可。
  数据保存在 %APPDATA%\com.miaoju.desktop，不会丢失。

系统要求：Windows 10 / 11 64 位。
```

- [ ] **Step 7: 改写 `scripts/package-windows.sh`**

```bash
#!/usr/bin/env bash
# 在 macOS 上交叉编译 Windows x64 版本并打成 zip（解压即用，不含安装程序，spec E4）。
# 依赖：rustup target x86_64-pc-windows-msvc、cargo-xwin、node、python3。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET=x86_64-pc-windows-msvc
bash scripts/version.sh check
VERSION="$(node -p "require('./package.json').version")"

bash scripts/prebuild.sh "$TARGET"
cargo tauri build --runner cargo-xwin --target "$TARGET" --no-bundle

REL="src-tauri/target/$TARGET/release"
for f in miaoju.exe sidecar.exe; do
  [ -f "$REL/$f" ] || { echo "缺少构建产物 $REL/$f" >&2; exit 1; }
done

NAME="miaoju-windows-x64-$VERSION"
STAGE="out/$NAME"
ZIP="out/$NAME.zip"
rm -rf "$STAGE" "$ZIP"
mkdir -p "$STAGE"
cp "$REL/miaoju.exe" "$REL/sidecar.exe" "$STAGE/"

# --no-bundle 不处理 bundle.resources：按 {源: 目标} 映射复制到 exe 同目录
while IFS=$'\t' read -r src dest; do
  [ -n "$src" ] || continue
  if [ -d "src-tauri/$src" ]; then
    mkdir -p "$STAGE/$dest"
    cp -R "src-tauri/$src/." "$STAGE/$dest/"
  else
    mkdir -p "$(dirname "$STAGE/$dest")"
    cp "src-tauri/$src" "$STAGE/$dest"
  fi
done < <(node scripts/lib/list-resources.mjs src-tauri/tauri.conf.json)

# 说明文件：UTF-8 BOM + CRLF，老版本记事本打开也不乱码
{ printf '\xEF\xBB\xBF'; sed 's/$/\r/' "packaging/首次打开说明-Windows.txt"; } > "$STAGE/首次打开说明.txt"

python3 scripts/lib/make_zip.py "$STAGE" "$ZIP"
echo "已生成 $ZIP（$(du -h "$ZIP" | cut -f1)）"
```

- [ ] **Step 8: 处理 STATIC_VCRUNTIME 弃用警告**

原因（已核对源码）：`tauri-cli 2.11.5` 在 Windows 目标构建时设置环境变量 `STATIC_VCRUNTIME=true`，而 `tauri-build 2.7.0` 读到该变量就打印弃用警告（静态链接本身照常生效，`build.windows.staticVCRuntime` 默认就是 `true`）。

Run: `cargo install tauri-cli --locked` 后执行 `grep -rn 'set_var("STATIC_VCRUNTIME"' ~/.cargo/registry/src/*/tauri-cli-*/src`，看最新 CLI 版本对应的源码。
- 若最新 CLI 已不再设置该变量：保持升级后的 CLI，在 `docs/release-checklist.md` 的“工具版本”里记下 `cargo tauri --version` 的输出。
- 若仍设置：不做改动，在 `docs/release-checklist.md` 的“已知无害警告”里记一行“`STATIC_VCRUNTIME is deprecated` 来自 tauri-cli，静态链接照常生效”。（T11 负责该文件；T9 在 PR 描述里写明结论，合并时补进清单。）

- [ ] **Step 9: 运行并验证**

Run: `bash scripts/package-windows.sh`
Expected: 结尾 `已生成 out/miaoju-windows-x64-0.1.0.zip（…）`。

Run:
```bash
unzip -l out/miaoju-windows-x64-0.1.0.zip
python3 - <<'EOF'
import zipfile
z = zipfile.ZipFile("out/miaoju-windows-x64-0.1.0.zip")
names = {i.filename: i for i in z.infolist()}
top = "miaoju-windows-x64-0.1.0/"
for n in ("miaoju.exe", "sidecar.exe", "首次打开说明.txt"):
    assert top + n in names, n
info = names[top + "首次打开说明.txt"]
assert info.flag_bits & 0x800, "中文文件名缺少 UTF-8 标记"
data = z.read(info)
assert data.startswith(b"\xef\xbb\xbf") and b"\r\n" in data
print("zip 检查通过")
EOF
```
Expected: 列表含三项；输出 `zip 检查通过`。

- [ ] **Step 10: Commit**

```bash
git add scripts/package-windows.sh scripts/prebuild.sh scripts/lib "packaging/首次打开说明-Windows.txt"
git commit -m "build(windows): zip 内附首次打开说明、按 bundle.resources 复制资源、中文文件名带 UTF-8 标记"
```

---

### Task 10: 1024×1024 正式图标

**前置：** 需要用户/设计提供 1024×1024 PNG（正方形、带透明或满底、无圆角——macOS 会自行加圆角遮罩之外的部分请按 Apple 图标模板留边）。拿到前本任务挂起，不阻塞其他任务。

**Files:**
- Create: `packaging/icon-1024.png`（设计稿原图）
- Create: `scripts/gen-icons.sh`
- Modify: `src-tauri/icons/*`（重新生成）

**Interfaces:**
- Consumes: 无
- Produces：`bash scripts/gen-icons.sh [源图]`；`tauri.conf.json` 的 `bundle.icon` 列表不变（文件名不变）

- [ ] **Step 1: 写 `scripts/gen-icons.sh`**

```bash
#!/usr/bin/env bash
# 从 1024×1024 设计稿生成 Tauri 全套图标（src-tauri/icons/）。
# 用法：bash scripts/gen-icons.sh [源图，默认 packaging/icon-1024.png]
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${1:-packaging/icon-1024.png}"
[ -f "$SRC" ] || { echo "找不到图标源文件：$SRC" >&2; exit 1; }
W="$(sips -g pixelWidth "$SRC" | awk '/pixelWidth/{print $2}')"
H="$(sips -g pixelHeight "$SRC" | awk '/pixelHeight/{print $2}')"
[ "$W" = 1024 ] && [ "$H" = 1024 ] || { echo "图标源必须是 1024×1024，实际 ${W}×${H}" >&2; exit 1; }

cargo tauri icon "$SRC" -o src-tauri/icons
echo "图标已生成到 src-tauri/icons/"
```

- [ ] **Step 2: 验证尺寸校验会拦住小图**

Run: `bash scripts/gen-icons.sh web/public/logo.png; echo "exit=$?"`
Expected: `图标源必须是 1024×1024，实际 80×80`，`exit=1`。

- [ ] **Step 3: 放入设计稿并生成**

Run: `cp <设计稿路径> packaging/icon-1024.png && bash scripts/gen-icons.sh`
Expected: `图标已生成到 src-tauri/icons/`。

- [ ] **Step 4: 验证产物**

Run:
```bash
sips -g pixelWidth src-tauri/icons/icon.png
D="$(mktemp -d)"; iconutil -c iconset src-tauri/icons/icon.icns -o "$D/i.iconset" && ls "$D/i.iconset" | grep 512x512@2x
```
Expected: `icon.png` 宽 512 或更大；iconset 里有 `icon_512x512@2x.png`（即 1024 像素层）。

- [ ] **Step 5: Commit**

```bash
git add packaging/icon-1024.png scripts/gen-icons.sh src-tauri/icons
git commit -m "build: 用 1024×1024 设计稿重新生成应用图标"
```

---

### Task 11: 发版检查清单

**Files:**
- Create: `docs/release-checklist.md`

**Interfaces:**
- Consumes：T1/T8/T9 的脚本名（按本计划约定）
- Produces：每次发版照着执行的清单；结果记录到 `docs/release-results/<ver>.md`

- [ ] **Step 1: 写 `docs/release-checklist.md`**

````markdown
# 喵剧AI 发版检查清单

每次发版复制下面的“结果记录模板”到 `docs/release-results/<版本>.md`，逐项打勾并粘贴诊断结果。

## 1. 工具版本（记录到结果文件）

- `cargo tauri --version`、`rustc --version`、`go version`、`node --version`、`cargo xwin --version`
- 已知无害警告：（T9 Step 8 的结论写在这里）

## 2. 发版前

- [ ] 内置数据已重新导出（spec 第 9 节：模型和配置内置、不自动更新）
- [ ] `bash scripts/version.sh set <新版本>`，然后 `bash scripts/version.sh check` 输出“版本一致”
- [ ] `pnpm test:server && pnpm test:web && pnpm test:tauri && pnpm test:scripts` 全部通过
- [ ] `cd src-tauri && cargo xwin check --target x86_64-pc-windows-msvc` 通过

## 3. 构建

- [ ] `pnpm release` 产出：
  - `out/喵剧AI_<ver>_aarch64.dmg`
  - `out/喵剧AI_<ver>_x64.dmg`
  - `out/miaoju-windows-x64-<ver>.zip`
- [ ] 记录三个文件的大小和 `shasum -a 256`

## 4. 实机矩阵（D8：上线前 macOS 14 / 15 各跑一次；不通过就抬高最低版本）

| 机器 | 安装包 | 必须 |
|---|---|---|
| macOS 14（Apple 芯片） | aarch64.dmg | 是 |
| macOS 15（Apple 芯片） | aarch64.dmg | 是 |
| macOS 26（Apple 芯片，开发机） | aarch64.dmg | 是 |
| macOS 14 或 15（Intel） | x64.dmg | 是（没有实机时用借的机器，不能用 Rosetta 代替） |
| Windows 11 x64 | zip | 是 |
| Windows 10 22H2 x64 | zip | 是 |

虚拟机（UTM 等）可用于先行排查；虚拟机里 WebCodecs 编码失败可能是没有硬件编码器，必须回到实机复测后才能下结论。

### 每台机器都做

- [ ] 从“下载”目录打开（保留隔离属性），严格按包内《首次打开说明.txt》操作能放行；说明与实际界面不符就改说明
- [ ] 打开“环境诊断”（P3 之后在“设置 → 环境诊断”），点“运行连接与编解码检查”，再选一个带声音的 mp4：17 项全部 PASS，“复制结果”粘贴到结果文件
- [ ] “设置”里显示的版本号 = 本次版本
- [ ] 再次打开应用：只有一个窗口（S11）
- [ ] 退出应用后无残留：macOS `pgrep -fl sidecar` 无输出；Windows 任务管理器里无 `sidecar.exe`
- [ ] 强制结束主程序（活动监视器 / 任务管理器结束 `miaoju`）后 5 秒内 sidecar 消失
- [ ] 强制结束 `sidecar` 一次：界面自动恢复；60 秒内再结束一次：出现失败页，点“重试”恢复
- [ ] 覆盖安装上一个版本：项目、设置、媒体都还在

### 仅 macOS

- [ ] `codesign -dv /Applications/喵剧AI.app 2>&1 | grep Signature` 输出 `Signature=adhoc`
- [ ] `stat -f %Lp ~/Library/Application\ Support/com.miaoju.desktop` 输出 `700`

### 仅 Windows

- [ ] 在压缩包预览里直接双击 `miaoju.exe`：失败页提示“请先把 zip 完整解压”
- [ ] 解压后首次运行出现 SmartScreen，“更多信息 → 仍要运行”可放行
- [ ] 资源管理器里 `首次打开说明.txt` 文件名不乱码，记事本打开不乱码
- [ ] `%APPDATA%\com.miaoju.desktop\` 存在
- [ ] WebView2 缺失提示：在没有 WebView2 的环境（如 Windows Server / LTSC 虚拟机）运行，弹窗出现、点“确定”打开微软下载页；没有这样的环境时，用 debug 构建设 `MIAOJU_FAKE_NO_WEBVIEW2=1` 验证弹窗
- [ ] 若诊断页 HTTP 检查因 CORS 预检失败且控制台提到 Private Network Access：记录下来，交 Go 侧加 `Access-Control-Allow-Private-Network: true` 后重新出包

## 5. D8 判定

- macOS 14 或 15 上任一 WebCodecs 项（尤其 AudioEncoder/AudioDecoder、AAC）失败：把 `src-tauri/tauri.conf.json` 的 `bundle.macOS.minimumSystemVersion` 和 `scripts/release-macos.sh` 的校验值、两份说明里的“系统要求”一起提高到下一个通过的版本，重新出包。

## 6. 发出

- [ ] 三个安装包 + sha256 发给用户；附一句：首次打开请看包内《首次打开说明.txt》

## 结果记录模板

```
# <版本> 发版结果（<日期>）
工具版本：
sha256：
## <机器 1：系统版本 / 芯片>
诊断结果：
（粘贴）
勾选项：
```
````

- [ ] **Step 2: Commit**

```bash
git add docs/release-checklist.md
git commit -m "docs: 发版检查清单（含 macOS 14/15 与 Windows 实机诊断矩阵）"
```

---

### Task 12: 发版演练

**前置：** T1–T11 全部合入，并已合入当时能合入的其他阶段（至少 P1b；P3a、P6 若已完成一并带上）。

**Files:**
- Create: `docs/release-results/0.1.0.md`（或演练时的版本号）
- Modify: `docs/superpowers/plans/p0-results.md`（补 Windows 一节）

- [ ] **Step 1: 合并后全量检查**

Run: `bash scripts/version.sh check && pnpm test:server && pnpm test:web && pnpm test:tauri && pnpm test:scripts && (cd src-tauri && cargo xwin check --target x86_64-pc-windows-msvc)`
Expected: 全部通过。

- [ ] **Step 2: 出包**

Run: `pnpm release`
Expected: `out/` 下三个安装包，脚本内校验全部通过。

- [ ] **Step 3: 按 `docs/release-checklist.md` 第 4 节逐台机器执行**

把每台机器的诊断“复制结果”和勾选结果写入 `docs/release-results/0.1.0.md`；Windows 的结果同时补进 `docs/superpowers/plans/p0-results.md` 的“Windows（zip）”一节。

Expected: 所有“必须”机器 17/17 PASS，勾选项全部通过；任何失败都按清单第 4、5 节处理并在结果文件里记录处理方式。

- [ ] **Step 4: Commit**

```bash
git add docs/release-results docs/superpowers/plans/p0-results.md
git commit -m "docs: P8 发版演练结果（macOS 14/15/26、Intel、Windows 10/11）"
```

---

## 自查记录

- Spec 覆盖：D6/D8 → T8（两种芯片 dmg、最低 14.0 校验）、T11/T12（14/15 实测与判定）；E4 → T8/T9（dmg + zip，无自动更新），版本显示接口 → K9 + 给 P4 的声明；E5 → T8/T9 说明文件、ad-hoc 校验；E6 → 全局约束、脚本里的产品名；E7 → 无需 P8 处理（P3a）；E8 → T4 + T6；S1/S2 → T3 + T6 命令 + 给 P4 的声明；S11 → T6 保留插件顺序、T11 实测；SEC1–3 → T2（stdin token）、T6（release 去掉开发源）；SEC4 → T6 去掉 shell 插件、K10 说明 CSP；P0 遗留 P8 节全部条目 → T1、T2、T5、T6、T7、T9、T10、K12。
- 类型一致：`ServerInfo {port, token}`、`SidecarStatus` 三态与前端 `SidecarStatus` 一致；事件名 `sidecar-state` 在 `lib.rs` 与 `server.ts` 一致；`DataDirInfo {current, default, custom}` 与给 P4 的声明一致。

## 执行方式

推荐 **Subagent-driven**：第 1 批 9 个任务彼此独立、文件几乎不重叠，适合并行子代理 + 每任务独立审查；T5/T6 是唯一的强依赖链，生命周期出错会让整个应用“本地服务未启动”，值得单独审一遍。
