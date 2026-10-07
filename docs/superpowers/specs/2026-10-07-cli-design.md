# 桌面端命令行（CLI）设计

日期：2026-10-07　状态：已与用户确认（2026-10-07）
关联：`docs/specs/2026-09-29-canvas-desktop-spec.md`（X3、X4、E4、SEC1–SEC3、S2）；参考仓 `huabu_studio` 的 `docs/canvas-cli-design.md`、`docs/canvas-cli-usage.md`、`docs/superpowers/specs/2026-08-01-cli-agent-install-skill-design.md`。

## 1. 目标与已确认的决定

让用户和 AI 助手（Claude Code、Codex 等）在终端里操作本机桌面端的自由画布：查看画布、建节点、接线、运行、上传素材、做 Seedance 合规校验。总原则与整个项目一致：**照搬参考仓的 CLI 和 `cliapi` 后端，只改桌面端必须改的部分**。

| # | 决定 | 来源 |
|---|---|---|
| D1 | **CLI 只在应用打开时可用**。CLI 只是 sidecar 的一个 HTTP 客户端，不直接读写 SQLite（否则会和应用抢锁，生成任务也可能执行两次） | 用户确认，2026-10-07 |
| D2 | **用"地址文件"发现 sidecar**：sidecar 每次启动用随机端口，所以由壳在固定位置写 `cli-endpoint.json`（端口 + CLI 专用 token），CLI 每次执行时读取 | 用户确认，2026-10-07 |
| D3 | 地址文件放在**系统默认数据目录**（`location.json` 所在的目录），不能放在用户自定义的数据目录里：CLI 无从得知用户把数据目录改到了哪里（S2） | 本设计 |
| D4 | CLI token 与网页 token **双向隔离**：CLI token 只能访问 `/api/v1/cli/*`，网页 token 访问不了 `/api/v1/cli/*`（沿用参考仓 aud=cli 的隔离思路） | 照搬参考仓 |
| D5 | 删掉 `auth`、`upgrade` 子命令和 `/cli/releases/latest`：桌面端没有登录（X4），也不做自动更新（E4） | 本设计 |
| D6 | CLI 二进制随安装包一起发，版本号与应用相同 | 本设计 |
| D7 | 命令名为 **`miaoju`**：参考仓的 `tvmao` 连远程服务器，同一台机器上同名会互相覆盖 | 用户确认，2026-10-07 |
| D8 | 第 6 节"已打开的画布感知外部变更"与 CLI **同期做**：不做的话，AI 助手改完画布后用户看不到结果 | 用户确认，2026-10-07 |
| D9 | 应用没开时**不自动拉起**，只给出明确报错 | 用户确认，2026-10-07 |
| D10 | 本机连接使用明文 `http://127.0.0.1`，**不做 HTTPS**：流量不出本机。AI 助手能否访问本机由它自己的沙箱网络开关决定，与 http 还是 https 无关（见 3.5 节实测） | 用户确认，2026-10-07 |
| D11 | 设置页点"安装"时**同时把 skill 装进 Claude Code 和 Codex**（`~/.claude/skills/miaoju-cli`、`~/.codex/skills/miaoju-cli`），只装进主目录已存在的那个；**不覆盖用户自己的同名目录**。装完后直接在 AI 助手里说"用喵剧帮我……"即可（见 7.2 节） | 用户确认，2026-10-07 |
| D12 | **以按钮安装为主、提示词为辅**：卡片分"一键安装"（默认）和"让 AI 助手安装"**两个页签**，后者给出写着本机真实路径的提示词，并提前说明命令会写入工作区以外的目录、被沙箱拦截时请向用户申请权限（见 7.2 节） | 用户确认，2026-10-07 |

不做：
- 不移植 `tvmao-flow`（项目管理 CLI，`apps/flow-cli`）。
- 不移植 `model estimate` 和任何积分相关输出（X3）。
- 不支持远程访问：只连 `127.0.0.1`，不提供 `--server`，也不提供 `TVMAO_TOKEN` 这类远程凭据。
- 不做"应用没开时自动拉起应用"（D9）。

## 2. 整体结构

```
终端 / AI 助手
   │  miaoju node list …
   ▼
CLI 二进制（随安装包发布）
   │ 1. 读 <默认数据目录>/cli-endpoint.json → port + cliToken
   │ 2. HTTP  127.0.0.1:<port>/api/v1/cli/*   Authorization: Bearer <cliToken>
   ▼
sidecar（照搬 cliapi + cli_*.go handler）
   │ projects.MutateSnapshot / canvasstate / canvasgen / seedance
   ▼
SQLite  ──（第 6 节）外部变更事件──▶  已打开的画布刷新
```

## 3. sidecar 的发现与鉴权

### 3.1 启动协议改动（壳与 sidecar 两边一起改）

现有协议见 `server/cmd/sidecar/main.go` 文件头。改为：

1. 壳生成**两个** token，通过 stdin 写入**两行**：第 1 行是网页 token（不变），第 2 行是 CLI token。
2. sidecar 就绪后照旧打印 `READY <port>`。
3. 壳收到 `READY` 后写地址文件；sidecar 重启（`restart()`）后，用新端口和新 token 重写；应用正常退出时删除。

由壳生成 CLI token 并写文件，原因：
- 与 SEC3 一致：启动 token 都由壳生成，经 stdin 传入。
- 壳本来就负责 sidecar 的生命周期，重启和退出时都能同步更新文件；壳也知道默认数据目录（`src-tauri/src/data_dir.rs`），sidecar 只拿到实际的数据目录。

### 3.2 地址文件

路径：`<默认数据目录>/cli-endpoint.json`
- macOS：`~/Library/Application Support/com.miaoju.desktop/cli-endpoint.json`
- Windows：`%APPDATA%\com.miaoju.desktop\cli-endpoint.json`

```json
{ "version": 1, "port": 52817, "token": "<32 字节随机 hex>", "appVersion": "1.2.0" }
```

- 原子写入（先写 tmp，再 fsync 并改名），做法与 `write_location` 一致。
- macOS 上文件权限设为 `0600`。Windows 上目录默认就在用户 profile 下，只有当前用户能读，不额外设置 ACL。
- 应用崩溃时文件会残留，CLI 按 3.4 节处理，不需要壳在启动时清理（启动后会直接覆盖）。

### 3.3 鉴权中间件

`localauth.Config` 增加 `CLIToken`：
- 请求路径以 `/api/v1/cli/` 开头：只接受 `Authorization: Bearer <CLIToken>`。
- 其他路径：照旧只接受网页 token 和媒体 token，拒绝 CLI token。
- CLI 请求不带 `Origin`，不经过 Origin 白名单。如果带了 `Origin`，说明请求来自浏览器上下文，一律拒绝（防止本机网页借 CLI 接口发起请求）。

CLI 输出里的媒体 URL 照旧用媒体 token 签名，可以直接被 AI 助手下载查看。

### 3.4 CLI 侧的连接逻辑

| 情况 | CLI 的行为 |
|---|---|
| 地址文件不存在 | 报"喵剧未运行，请先打开应用"，退出码 `EXIT_APP_NOT_RUNNING` |
| 连接被拒绝 `ECONNREFUSED`（应用崩溃后文件残留） | 同上 |
| 权限被拒 `EPERM`（`Operation not permitted`，被 AI 助手的沙箱拦截了网络） | 报"网络被 AI 助手的沙箱拦截，请允许本命令访问网络后重试"，退出码 `EXIT_SANDBOX_BLOCKED`。必须与"应用未运行"区分开，否则 AI 助手会误以为应用没开 |
| 返回 401（端口被别的进程占用，或 token 过期） | 重读一次地址文件再重试一次；仍然 401 则同上 |
| `appVersion` 与 CLI 自身版本不一致 | stderr 警告一行，继续执行（覆盖安装后终端里可能残留旧链接） |

新增 `GET /api/v1/cli/ping`，返回 `{ "app": "miaoju-desktop", "version": "…" }`。CLI 先用它确认对端确实是本应用，再发业务请求。

### 3.5 AI 助手沙箱（2026-10-07 本机实测，codex-cli 0.159.3）

| 场景 | 结果 |
|---|---|
| 不进沙箱，`curl http://127.0.0.1:<port>/` | 200 |
| Codex 默认沙箱，访问 `http://127.0.0.1` | 连接失败 |
| Codex 默认沙箱，访问 `https://www.baidu.com` | 同样连接失败 |
| Codex `sandbox_mode="workspace-write"` + `sandbox_workspace_write.network_access=true`，访问 `http://127.0.0.1` | 200 |
| Codex 沙箱内读取 `~/Library/Application Support/com.miaoju.desktop` | 可读（地址文件能拿到） |

结论：Codex 默认关闭沙箱内的全部网络，HTTPS 也出不去，拦截与是否有证书无关；打开网络后，明文的本机 http 正常。Claude Code 默认不启用沙箱，Bash 命令直接执行；启用沙箱后也有网络白名单，需要放行本机地址（未实测）。CLI 读取本地图片上传不受沙箱影响，只有发 HTTP 请求这一步需要网络权限。

## 4. 后端：照搬 `cliapi`

从参考仓复制 `apps/api/internal/cliapi/`（约 2.3k 行）和 `apps/api/internal/http/cli_*.go`（约 3.9k 行），改 import 后接入。桌面端已经有对应的底座：`projects.MutateSnapshot`（`server/internal/projects/repository.go:258`）、`canvasstate`、`canvasinputs`、`canvasgen`、`seedance`。

| 参考仓 | 桌面端处理 |
|---|---|
| `CLIAuthMiddleware`（JWT，aud=cli） | 换成 3.3 节的 token 中间件，`userID` 固定为 1（X4） |
| `/cli/me`、`auth` 相关 handler | 删除 |
| `/cli/releases/latest`、`cli_install_handlers.go`、`cli_release_handlers.go` | 删除 |
| 项目、画布、节点、连线的读写 handler | 照搬 |
| `RunNode` / `GetRun` | 照搬。去掉 `billing` 的冻结和扣费，接入桌面端现有的生成入队 |
| `CLIAsset.Upload` | 改为调用桌面端本地素材存储（`server/internal/assets`），不再上传 TOS |
| `/nodes/{id}/compliance` | 接入桌面端 `seedance` 包 |
| `model_mapper.go` | 照搬，去掉价格字段 |
| 迁移 `0085_cli_idempotency_keys.sql` | 改写成 SQLite 迁移 |
| `MustWireCLI` 启动期校验 | 照搬 |

写入语义沿用参考仓：所有写操作经 `MutateSnapshot`，不推进 `last_client_version`；支持 `Idempotency-Key` 和 `If-Match: "snapshot-N"`。

## 5. CLI 二进制

从参考仓复制 `apps/cli/` 到本仓 `cli/`（独立 `go.mod`，与 `server/` 平级）。

| 参考仓 | 桌面端处理 |
|---|---|
| `internal/credential`、`internal/webauth`、`app/auth.go` | 删除 |
| `internal/upgrade`、`app/upgrade.go` | 删除 |
| `--server`、`TVMAO_SERVER`、`--token`、`TVMAO_TOKEN`、`main.defaultServer` | 删除，改为新增 `internal/endpoint`（读地址文件，见 3.4 节） |
| `internal/workspace`（目录绑定 `.tvmao/project.json`） | 照搬。绑定记录里原本的 `server` 字段去掉 |
| `internal/client`、`output`、`exitcode` | 照搬，`exitcode` 新增 `EXIT_APP_NOT_RUNNING`、`EXIT_SANDBOX_BLOCKED` |
| `project / canvas / node / edge / model / asset / run / compliance` | 照搬（`model estimate` 删除） |

命令名为 `miaoju`（D7），本机目录绑定文件相应改为 `.miaoju/project.json`。

## 6. 已打开的画布感知外部变更

**问题**：前端不合并远端 op（`web/src/modules/canvas/editor/agent/types.ts:147`，X2），也不轮询快照。CLI 改了画布后，已打开的画布要重新打开项目才会显示变化；用户在界面上操作一个已被 CLI 删除的节点时会报错。前端写入都是局部 mutation，**不会覆盖 CLI 的改动**，问题只是两边看到的画布不一致。

**方案**：
1. sidecar 新增进程内事件中心。`cliapi` 的写操作提交成功后发出 `{projectId, version, source: "cli"}`。只有 CLI 写入才发：生成回写、Agent 写入走现有链路，不受影响。
2. 新增 `GET /api/v1/projects/{id}/events`（SSE，网页 token）。前端打开项目时订阅，切换项目时断开重连。
3. 前端收到事件后，如果 `version` 大于本地已知版本，就等画布空闲（没有拖拽、没有正在编辑的输入框、没有未完成的本地 mutation），然后通过现有的快照加载路径重新拉取并替换 store。视口位置和当前选中项保留（选中的节点已被删除时清空选中）。
4. SSE 断开后自动重连；重连成功后比较一次版本号，补上断线期间漏掉的变更。

用 SSE 不复用 Agent 的 WebSocket，原因：Agent WS 绑定在线程上，没开 Agent 面板时没有连接。

## 7. 打包与安装

### 7.1 打包

- `scripts/` 里的构建脚本增加一步：按平台交叉编译 CLI，放到 `src-tauri/binaries/miaoju-cli-<target-triple>`，加入 `tauri.conf.json` 的 `externalBin`（与 sidecar 同样的做法）。安装包内的文件名为 `miaoju-cli`（主程序已占用 `miaoju`），安装到终端时命名为 `miaoju`。
- `ldflags` 注入 `main.version`，与应用版本一致。
- skill 目录 `cli/skill/` 作为 Tauri resource 打进安装包。

### 7.2 安装（设置 → 通用 → "命令行工具"卡片）

- **安装按钮**：在 `~/.miaoju/bin/` 下创建指向安装包内 CLI 的链接（Windows 上复制 `.exe`，因为普通用户不能创建符号链接），并把该目录写入 PATH：
  - macOS：在 `~/.zshrc`、`~/.bash_profile` 里追加带 marker 的一行，重复安装不会重复追加（照搬参考仓 `install.sh` 的做法）。
  - Windows：写用户级 `Path` 环境变量。
- **卸载按钮**：删除链接，并移除带 marker 的那一行或那条 PATH 项。
- **安装同时装 skill（D11）**：安装按钮还会把包内 skill 目录装进 AI 助手，卸载按钮一并删除：
  - 目标为 `~/.claude/skills/miaoju-cli`、`~/.codex/skills/miaoju-cli`；对应主目录（`~/.claude`、`~/.codex`）不存在就跳过，卡片显示"未检测到"。
  - macOS 建符号链接指向安装包里的 `cli-skill/`；Windows 递归复制，并在目录里写标记文件 `.miaoju-managed`（内容为应用版本号）。
  - **不覆盖用户同名目录**：只有符号链接（含悬空链接）或带 `.miaoju-managed` 的目录才算"我们装的"，可以替换和卸载；其他同名目录或文件一律不动，卡片显示"已存在同名目录，未覆盖"。
- **两个页签（D12）**：
  - "一键安装"（默认）：安装 / 重新安装 / 卸载按钮、命令位置、各 AI 助手的 skill 状态。
  - "让 AI 助手安装"：一段可复制的提示词，写的是本机真实路径（加引号，路径可能含空格和中文），让 Claude Code 或 Codex 自己执行与安装按钮等价的几条命令（macOS 用 shell，Windows 用 PowerShell）；末尾说明这些命令会写入工作区以外的目录，被沙箱拦截时请向用户申请权限。
  - 卡片顶部说明用途、使用前提（喵剧保持打开）和用法（终端 `miaoju --help`，或在 AI 助手新会话里直接说需求）。原来的"复制给 AI 助手：请阅读 …/SKILL.md"一节已被以上两项取代。
- **Codex 网络配置提示**（放在页签下方，两种安装方式都用得到）：卡片上附一段可复制的配置，并说明不加的话每次调用都要审批一次：
  ```toml
  # ~/.codex/config.toml
  sandbox_mode = "workspace-write"

  [sandbox_workspace_write]
  network_access = true
  ```
- 卡片上显示当前状态：未安装、已安装、已安装但版本不一致（覆盖安装后 Windows 上的复制件是旧版，需要提示重新安装）。

### 7.3 skill 内容

以参考仓 `apps/cli/skill/` 为底稿改写：删除 `auth.md` 和安装、更新章节；所有命令名替换成 `miaoju`。新增两条规则：
- 使用前先确认应用已打开；退出码为 `EXIT_APP_NOT_RUNNING` 时，请用户打开应用，不要重试。
- 退出码为 `EXIT_SANDBOX_BLOCKED` 时，按所用助手申请网络权限（Codex 申请提升权限或请用户按 7.2 节修改配置；Claude Code 请用户放行本机地址），然后重试同一条命令。

## 8. 测试

- `localauth`：CLI token 只能访问 `/cli/*`；网页 token 访问 `/cli/*` 被拒；带 `Origin` 的 CLI 请求被拒。
- 照搬参考仓 `cli_*_test.go` 和 `cliapi/*_test.go`，改为在 SQLite 上运行；删除 auth、release、install、billing 相关用例。
- 壳（Rust）：`READY` 后写地址文件；`restart()` 后文件内容更新；正常退出后文件被删除；写入是原子的。
- CLI：地址文件缺失、连接被拒、`EPERM` 映射为 `EXIT_SANDBOX_BLOCKED`、401 后重读一次、版本不一致警告。
- 手工：在 Codex 默认沙箱和打开网络两种配置下各跑一次 `miaoju project list`，确认退出码与提示正确。
- 端到端：启动 sidecar → CLI 建节点并接线 → 已订阅 SSE 的客户端收到事件，版本号正确。
- 前端 vitest：收到事件后，在空闲时重新加载；正在拖拽时推迟加载；选中的节点被删除后清空选中。

## 9. 待定项

无。原 Q1–Q3 已于 2026-10-07 按推荐确认，见 D7–D9。

## 10. 工作量估算（按子代理并行）

| 块 | 估算 |
|---|---|
| 启动协议、地址文件、鉴权隔离、沙箱错误区分（Go + Rust） | 1 天 |
| `cliapi` 与 handler 移植（含 run、素材、合规适配） | 2 天 |
| CLI 二进制裁剪 + endpoint 包 | 1 天 |
| 打包、设置页卡片、skill 改写 | 1 天 |
| 外部变更事件（SSE + 前端空闲刷新） | 1–2 天，"空闲时刷新且不打断操作"最没把握 |

合计约一周。
