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

## 棘轮 Ratchet：建在 pstack 上

Ratchet 不重写 pstack 已经有的能力。工作方法直接用土豆姐（poteto）开源的 [pstack](https://github.com/cursor/plugins/tree/main/pstack)（MIT），Ratchet 只补 pstack 没有、而 James 的红线需要的那一层。

pstack 在 `.claude/settings.json` 里按项目启用，并锁定到 cursor/plugins 的一个 commit（0.15.36）。第一次在本仓库打开 Claude Code 时，按提示信任这个 marketplace 即可。Claude Code 里 pstack 的命令要加前缀，比如 `/pstack:poteto-mode`；Cursor 名字怎么对应，见插件里的 `docs/harness/claude-code.md`。

| 要做的事 | 用什么 | 来源 |
|---|---|---|
| 任何要严谨的工程活（修 bug、做功能、重构、调研） | `/pstack:poteto-mode`，它会自己选 playbook | pstack |
| 主轴调度多个 worker、多个 PR | poteto-mode 的 orchestrate / autonomous-run playbook | pstack |
| 验证 skill 的生成与维护 | `/pstack:create-verification-skill`、`/pstack:maintain-verification-skill`（本仓库的产物是 `verify-tvmao`） | pstack |
| 把反复出现的错误变成机制、维护下面的规则表 | `/pstack:correct` | pstack |
| 复盘一次运行、提出改进 | `/pstack:reflect` | pstack |
| 刷一个指标 / 跑 eval | poteto-mode 的 hillclimb / eval playbook | pstack |
| 拦计费、合并、push main、密钥、红线文件 | 闸 hook `scripts/ratchet/hooks/pawl.py` + 权限 deny | Ratchet |
| 关卡 G1–G6 与批准 | `.ratchet/policy/gates.md`、`.ratchet/policy/approvals/<任务号>.md` | Ratchet |
| 派活的 kickoff 格式与预检 | `.claude/skills/ratchet-brief/SKILL.md`、`ratchet.py precheck-brief` | Ratchet（覆盖 orchestrate 要求的目标、证据、可写路径、禁止动作，再加关卡和成本上限） |
| worker 回传与证据核对 | `.claude/skills/ratchet-report/SKILL.md`、`ratchet.py check-report` | Ratchet |
| macOS WebView 驱动不了时的取证 | 浏览器验证模式（上一节） | Ratchet |

两者冲突时 Ratchet 的红线优先。例如 pstack 的流程里有“合并”“land the stack”，在这里一律交给闸：agent 不合并，合并由主轴按 James 的授权做。pstack 里 Cursor 专有的功能（Automations、Bugbot、cloud agent），在 Claude Code 里按它的 adapter 说明跳过。

- 主轴（coordinator）用 `RATCHET_ROLE=coordinator claude` 启动，只管项目、不写代码，只能写 `.ratchet/`。
- 每个 PR 要有一个不是作者、而且模型家族不同的 verifier（orchestrate 的要求）。
- `miaoju node run` 一提交就计费且不可取消，任何会话都不许跑（G5）。

## 规则 ↔ 由什么强制执行

`/pstack:correct` 和自改进 PR 维护这张表。只靠文字的规则要标出来，能变成 hook、lint 或测试的优先变。

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
