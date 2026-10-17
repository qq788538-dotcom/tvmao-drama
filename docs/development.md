# 开发指南

本文介绍如何搭建开发环境、运行测试和打包发布。

## 架构概览

喵剧AI 由四部分组成：

| 组件 | 目录 | 技术栈 | 职责 |
|---|---|---|---|
| 桌面壳 | `src-tauri/` | Rust、Tauri 2 | 创建窗口；启动、守护和重启 sidecar；数据目录迁移；原生保存对话框；安装 CLI |
| 前端 | `web/` | React 18、TypeScript、Vite、Ant Design、Tailwind、Zustand、TanStack Query | 画布编辑器、节点、Agent 面板、设置页 |
| 本地服务（sidecar） | `server/` | Go、chi、SQLite（ncruces/go-sqlite3）、Eino | REST + WebSocket 接口、生成队列、媒体存储、Agent、Skill 运行时 |
| 命令行工具 | `cli/` | Go | `miaoju` 命令，通过本机 sidecar 的 CLI 接口操作画布 |

运行时的关系：

1. 桌面壳启动时在 `127.0.0.1` 上为 sidecar 挑一个随机端口，生成一次性令牌，再拉起 sidecar 进程。
2. 前端从壳拿到端口和令牌，通过 `http://127.0.0.1:<端口>/api/v1` 和 WebSocket 访问 sidecar。sidecar 校验 Origin、Host 和令牌。
3. sidecar 把项目、设置存进数据目录下的 SQLite（`data.db`），媒体文件存在同一目录下，再按模型配置调用用户填写的上游网关。
4. CLI 从数据目录里的 `cli-endpoint.json` 读取端口和 CLI 令牌，访问 sidecar 的 `/api/v1/cli/` 接口（只认 Bearer 头）。

### 后端主要包

| 包 | 说明 |
|---|---|
| `cmd/sidecar` | 入口、依赖装配、优雅退出 |
| `internal/http` | 路由和处理器 |
| `internal/localauth` | 本机令牌、Origin / Host 校验 |
| `internal/db` | SQLite 打开与迁移 |
| `internal/generation` | 生成任务提交、排队、轮询、结果落盘、重试与退款 |
| `internal/upstream` | 按模板构造上游请求、解析响应 |
| `internal/models`、`internal/builtin` | 模型配置、内置种子数据合并 |
| `internal/agent` | Agent 循环、工具、Skill、子 Agent、画布能力清单 |
| `internal/skillruntime`、`internal/skillbundle` | 脚本节点的 Skill 执行（内置 Python + 受限命令） |
| `internal/canvasscript`、`internal/canvasgen` | 脚本节点任务、生成结果回写画布 |
| `internal/objectstore` | 阿里云 OSS / 火山引擎 TOS 上传与签名 |
| `internal/assets`、`internal/library` | 素材与素材库 |
| `internal/mediaprobe` | 读取音视频时长（MP4 解析，ffprobe 兜底） |

### 前端主要目录

| 目录 | 说明 |
|---|---|
| `web/src/modules/canvas/editor` | 画布编辑器、节点注册表（`registry.tsx`）、状态（`store.ts`） |
| `web/src/modules/canvas/editor/nodes` | 各类节点和媒体工具条 |
| `web/src/modules/canvas/editor/agent` | Agent 对话面板 |
| `web/src/modules/canvas/panels` | 生成历史、素材库、AI 角色库等侧边面板 |
| `web/src/modules/settings` | 设置弹框 |
| `web/src/shared` | API 客户端、sidecar 会话、通用组件 |
| `web/src/desktop` | 启动流程、本地媒体地址等桌面端专属逻辑 |

## 环境准备

开发和打包都在 macOS 上进行，Windows 版本由 macOS 交叉编译。

| 工具 | 版本 | 用途 |
|---|---|---|
| Node.js | 22 | 前端构建、脚本 |
| pnpm | 10 | 前端依赖 |
| Go | 1.26 | sidecar、CLI（也可以装较旧的 Go 并设置 `GOTOOLCHAIN=auto` 自动下载） |
| Rust | stable | 桌面壳 |
| tauri-cli | ≥ 2.12.1 | `cargo install tauri-cli --locked` |
| Python 3 | 3.x | 打包脚本 |
| cargo-xwin | 最新 | 仅交叉编译 Windows 时需要 |

```sh
git clone <仓库地址>
cd <仓库目录>
pnpm install
```

## 本地开发

```sh
pnpm dev
```

这条命令依次：

1. `scripts/build-sidecar.sh`：编译 Go sidecar 到 `src-tauri/binaries/sidecar-<target-triple>`，并下载内置 Python（脚本节点要用）。离线开发可以设 `MIAOJU_SKIP_PYTHON=1` 跳过。
2. `scripts/build-cli.sh`：编译 CLI 到 `src-tauri/binaries/miaoju-cli-<target-triple>`，复制 Skill 文档。
3. `cargo tauri dev`：启动 Vite 开发服务器和桌面壳。

只改前端时，Vite 会热更新；改了 Go 代码需要重新运行 `pnpm dev`。

### 只调试 sidecar

```sh
cd server
go run ./cmd/sidecar --help
```

## 测试

```sh
pnpm test:server    # cd server && go test ./...
pnpm test:web       # vitest
pnpm test:tauri     # cd src-tauri && cargo test
pnpm test:scripts   # node --test scripts/lib/*.test.mjs
cd cli && go test ./...
```

前端类型检查：`cd web && npx tsc --noEmit`。

在 Linux 上跑 `cargo test` 需要先安装 `libwebkit2gtk-4.1-dev libgtk-3-dev librsvg2-dev libsoup-3.0-dev`，并为 `externalBin` 和资源放占位文件，做法见 `.github/workflows/ci.yml`。

部分测试依赖本机工具，缺少时会自动跳过：

- `ffmpeg` / `ffprobe`：媒体时长探测测试
- `python3`：Skill 受限 shell 测试
- `MIAOJU_TEST_<OSS|TOS>_BUCKET` 等环境变量：对象存储集成测试

## 内置数据

`seed/` 下的 JSONL 是首次安装导入的模型和配置，`server/internal/builtin/seeddata/` 是编进 sidecar 的副本。修改内置模型后运行：

```sh
bash scripts/sync-seed.sh
```

升级时按模型 ID 合并：新模型自动加入，已移除的模型删除，用户改过的字段保留。详见 [seed/README.md](../seed/README.md)。

## 版本号

根目录 `package.json` 的 `version` 是唯一来源，`web/package.json` 和 `src-tauri/Cargo.toml` 是镜像：

```sh
bash scripts/version.sh check          # 检查三处一致
bash scripts/version.sh set 1.1.0      # 同时修改三处
```

## 打包发布

```sh
pnpm release:mac    # out/喵剧AI_<版本>_aarch64.dmg 和 _x64.dmg
pnpm release:win    # out/miaoju-windows-x64-<版本>.zip（macOS 上交叉编译）
pnpm release        # 两者都打
```

不要直接运行 `cargo tauri build`，那样得不到带《首次打开说明》的 dmg 和 Windows zip。发版前的完整检查项见 [发版检查清单](release-checklist.md)。

应用图标的源图是 `packaging/icon-1024.png`，替换后运行 `bash scripts/gen-icons.sh` 重新生成 `src-tauri/icons/`。

安装包使用 ad-hoc 签名，没有经过苹果公证，也没有 Windows 代码签名证书，用户首次打开需要手动放行。

## 持续集成

`.github/workflows/ci.yml` 在每个 Pull Request 和推送到 `main` 时运行：Go 格式检查、`go vet`（含 Windows / macOS 交叉检查）和带竞态检测的测试，前端类型检查、单测和构建，脚本测试和版本号一致性检查，以及 Tauri 壳的 `cargo test`。

## 安全约定

- sidecar 只监听 `127.0.0.1`，所有请求都要带令牌，并校验 Origin 和 Host。
- 前端没有直接读写磁盘的权限。保存文件一律由 Rust 侧弹出系统对话框，写入路径只能来自用户的选择。
- Tauri 权限保持最小集合，`src-tauri/tests/capabilities.rs` 会检查。
- API Key 不写日志，接口只返回掩码。
