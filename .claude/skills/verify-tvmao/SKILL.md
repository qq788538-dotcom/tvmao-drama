---
name: verify-tvmao
description: 在喵剧（tvmao-drama）上验证一项改动或一个功能时使用：起一个临时的验证实例（sidecar + 浏览器验证模式），用 miaoju CLI 和 Playwright 驱动，留下至少三类证据，绝不触发计费生成。棘轮的 verifier 和 worker 自测都用它。
---

# verify-tvmao：真跑应用、留证据

验收看证据，不看自述。这份 skill 告诉你怎么在不碰用户数据、不花钱的前提下把喵剧跑起来、操作它、拿到证据。

## 0. 红线（先读）

- **绝不跑 `miaoju node run`、`miaoju run`、界面上的“生成”按钮**。一提交就计费且不可取消（关卡 G5），闸会拦，但你也不要试。
- **永远用临时验证实例**。`miaoju` 不设 `MIAOJU_ENDPOINT_DIR` 时连的是用户正在用的喵剧，会改到他的真实数据。
- 证据放 `.ratchet/evidence/<任务号>/`（不入库）。cleanup 时不要删它。

## 1. Launch：起验证实例

```sh
node scripts/ratchet/browser-mode.mjs > /tmp/ratchet-browser.out &   # 后台跑
```

环境：Node 22、`pnpm install` 过；Go 要 1.26。本机 Go 更老时保持 `GOTOOLCHAIN=auto`（默认值），go 会按 go.mod 自动下载 1.26，需要网络；`GOTOOLCHAIN=local` 会构建失败。

**等就绪**：启动时它先删掉旧的 `.ratchet/state/browser-mode.json`，就绪后才重新写。所以等这个文件出现即可（首次要构建 sidecar，给 3 分钟）：

```sh
for i in $(seq 180); do [ -f .ratchet/state/browser-mode.json ] && break; sleep 1; done
```

超时没出现就看 `/tmp/ratchet-browser.out` 的报错。就绪后 stdout 打一行 JSON，state 文件内容相同，另多一个 `cliToken`（CLI 用，不要贴进证据）：

| 字段 | 用途 |
|---|---|
| `url` | 浏览器打开的地址（带 `ratchet_port` / `ratchet_token`，前端据此连 sidecar） |
| `endpointDir` | `export MIAOJU_ENDPOINT_DIR=<它>`，之后的 `miaoju` 都连这个临时 sidecar |
| `dataDir` | 临时数据目录，退出时自动删除 |
| `pid` | browser-mode 自己的进程号，收尾时用 |

已经编译好 sidecar 时可以加 `--sidecar-bin <路径>` 省掉构建。端口 5173 被占用时用 `--web-port`。

## 2. Doctor：确认实例健康

```sh
export MIAOJU_ENDPOINT_DIR=$(python3 -c "import json;print(json.load(open('.ratchet/state/browser-mode.json'))['endpointDir'])")
(cd cli && go build -o /tmp/miaoju ./cmd/miaoju) && export MIAOJU_BIN=/tmp/miaoju   # PATH 里已有 miaoju 可跳过
"${MIAOJU_BIN:-miaoju}" project list                                   # 退出码 0 即 sidecar 可用；8 = 实例没起来
node scripts/ratchet/browser-smoke.mjs --task <任务号> --sub doctor     # 前端连上 sidecar、无页面错误
```

`verify-*.sh` 脚本认 `MIAOJU_BIN`，没设时先找 PATH，再自己构建一份到证据目录。

冒烟打开 `/` 时前端会自动建一个默认项目，所以实例不再是空的。断言要按自己建的项目算，不要假设“实例里只有我的数据”。

## 3. Drive：怎么操作

按优先级：

1. **`miaoju` CLI**：项目、节点、连线、模型、素材。输出 JSON，最稳定。手册在 `cli/skill/`（只读参考，不要改）。
2. **sidecar REST**：`/api/v1/...`，CLI 没覆盖的接口。地址和 token 在 `.ratchet/state/browser-mode.json`。
3. **浏览器**：Playwright 打开 `url`，用于 UI-only 的行为和截图。参考 `scripts/ratchet/browser-smoke.mjs` 的写法。

每个功能的具体步骤在 feature map 里：

| 编号 | 功能 | 文件 | 成本 | 驱动方式 |
|---|---|---|---|---|
| F1 | 建图与连线 | [references/features/F1-canvas-graph.md](references/features/F1-canvas-graph.md) | 零成本 | CLI + 浏览器 |

（F2 脚本拆分镜、F3 模型配置、F4 素材库、F5 Agent 面板待补。）

## 4. Evidence：至少三类

| 类别 | 例子 |
|---|---|
| 命令与退出码 | `commands.log`，每行 `<命令> exit=<码>` |
| 结构化输出 | `canvas.json`、`node get` 的 `derivedInputs` |
| 截图 / 录屏 | `smoke.png` |
| 日志 | `console.log`（浏览器 console）、sidecar stderr |
| 测试输出 | 本地检查命令的输出 |

断言要写在证据里（“断言通过：…”），不要只放截图让人猜。

同一任务跑多次冒烟时用 `--sub` 分子目录（`doctor/`、`f1/`…），否则后一次会覆盖前一次。

冒烟的 `ok` 只看页面错误（pageerror）和是否连上 sidecar。`console.log` 里的 `[error]` 行要读一遍：已知无害的有 antd `destroyOnClose` 废弃警告；其他新出现的 error 写进回传书的「未解决风险」。

截图里的视觉期望（feature map 里写的“能看到…”）脚本断言不了，要自己打开 `smoke.png` 看，并在回传书里写看到了什么。

## 5. Cleanup

结束 `browser-mode.mjs`（Ctrl-C / `kill -INT <pid>`，pid 在 state 文件里）：它会关 sidecar、关 Vite、删临时数据目录和 state 文件。证据目录保留。`/tmp/miaoju` 可以留着下次用。

有任务书（`.ratchet/briefs/<任务号>.md`）时，收尾之后按 ratchet-report skill 写回传书并跑 `check-report`；没有任务书（临时自查）就只停实例。

## 已知边界

- 浏览器验证模式只替代了“取端口和 token”；数据目录切换、系统对话框、打开外链等壳功能测不了，这部分要在 macOS 桌面端人工看。
- 内置 Python 运行时在验证实例里找不到，脚本节点任务会失败（sidecar 启动时有警告），F1 不受影响。
