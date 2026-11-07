# CLAUDE.md

给在本仓库工作的 Claude Code 会话（棘轮 Ratchet 的主轴、worker、verifier）看的项目规则。人类贡献者的约定见 [CONTRIBUTING.md](CONTRIBUTING.md)，开发环境见 [docs/development.md](docs/development.md)。

## 基本约定

- 一个 PR 只做一件事；提交信息用 `<类型>(<范围>): <说明>`，类型见 CONTRIBUTING。
- 跟着周围的代码写，注释以中文为主。
- 修 bug 先写一个能复现问题的失败测试。
- 只开 draft PR，合并权只在 James。

## 本地检查（与 CI 一致，按改动路径跑）

| 改动路径 | 命令 |
|---|---|
| `server/` | `gofmt -l .`、`go vet ./...`、`go test -race ./...`（在 `server/` 下） |
| `cli/` | 同上，在 `cli/` 下 |
| `web/` | `npx tsc --noEmit`、`npx vitest run`、`npx vite build`（在 `web/` 下，先 `pnpm install --frozen-lockfile`） |
| `src-tauri/` | `cargo test --locked` |
| `scripts/` | `bash scripts/version.sh check`、`node --test scripts/lib/*.test.mjs` |

没跑的检查不要写"通过"；CI 没出结果写「CI 待核」。

## 验证（verify-tvmao）

验证改动或功能时按 `.claude/skills/verify-tvmao/SKILL.md` 做：临时验证实例 + CLI/浏览器驱动 + 至少三类证据。F1（建图与连线）可以一键跑：`scripts/ratchet/verify-f1.sh <任务号>`。

## 浏览器验证模式（取证用）

macOS 的 Tauri WebView 驱动不了，验证 UI 时走浏览器验证模式（只在开发构建生效）：

```sh
node scripts/ratchet/browser-mode.mjs            # 临时数据目录起 sidecar + Vite，打印带 ratchet_port/ratchet_token 的地址
node scripts/ratchet/browser-smoke.mjs --task T-0001   # 另开终端：打开页面、截图、记 console，证据进 .ratchet/evidence/T-0001/
```

- `miaoju` 连这个临时 sidecar：`MIAOJU_ENDPOINT_DIR=<就绪 JSON 里的 endpointDir> miaoju …`。**不设这个变量时 miaoju 连的是你正在用的喵剧**。
- 永远用临时数据目录，不要传日常使用的数据目录给 `--data-dir`。
- 只连 sidecar 的端口和 token；数据目录、对话框等壳命令在这个模式下不可用。
- 冒烟取证不点任何生成按钮（不计费）。

## 三套 skill 不要混

| 位置 | 是什么 | 能不能改 |
|---|---|---|
| `cli/skill/` | `miaoju` CLI 手册（给外部 agent 用） | 不改，另立任务并由 James 批 |
| `seed/skills.jsonl` 等 seed 数据 | 应用内官方 Skill（sidecar 执行） | 不改，另立任务并由 James 批 |
| `.claude/skills/` | 棘轮的开发 skill | 可以通过自改进 PR 提议，James 合并 |

## 棘轮 Ratchet

- 主轴（coordinator）用 `RATCHET_ROLE=coordinator claude` 启动，只管项目、不写代码，只能写 `.ratchet/`。
- 派活前写任务书并预检：`.claude/skills/ratchet-brief/SKILL.md`。
- worker 完成后写回传书：`.claude/skills/ratchet-report/SKILL.md`。
- 关卡 G1–G6 见 `.ratchet/policy/gates.md`；批准只认 `.ratchet/policy/approvals/<任务号>.md`。
- `miaoju node run` 一提交就计费且不可取消，任何会话都不许跑（G5）。

## 规则 ↔ 由什么强制执行

`/correct` 和自改进 PR 维护这张表。只靠文字的规则要标出来，能变成 hook、lint 或测试的优先变。

| 规则 | 强制手段 |
|---|---|
| 不跑 `miaoju node run` | 闸 hook（`scripts/ratchet/hooks/pawl.py`）+ 权限 deny |
| 不合并 PR、不 push main、不 force push | 闸 hook + 权限 deny + main 分支保护（管理员也受限；5 项 CI 必过且分支须与 main 同步） |
| 不读写 `.env` 与密钥 | 闸 hook + 权限 deny |
| 红线文件只有 James 能改 | 闸 hook + 权限 deny |
| 主轴不写代码 | 闸 hook（`RATCHET_ROLE=coordinator`） |
| 任务书 9 项齐全、关卡有批准 | `ratchet.py precheck-brief` |
| 回传书五项齐全、证据存在 | `ratchet.py check-report` |
| 一个 PR 只做一件事 | **仅文字**（评审把关） |
| 修 bug 先写失败测试 | **仅文字**（verifier 读 diff 把关） |
| 不改 `cli/skill/`、seed 数据 | **仅文字**（候选：加进闸的受保护路径） |
