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
node scripts/ratchet/browser-mode.mjs          # 后台跑；Linux/云端需要 Go 1.26、Node 22、pnpm install 过
```

就绪后 stdout 打一行 JSON，同时写到 `.ratchet/state/browser-mode.json`：

| 字段 | 用途 |
|---|---|
| `url` | 浏览器打开的地址（带 `ratchet_port` / `ratchet_token`，前端据此连 sidecar） |
| `endpointDir` | `export MIAOJU_ENDPOINT_DIR=<它>`，之后的 `miaoju` 都连这个临时 sidecar |
| `dataDir` | 临时数据目录，退出时自动删除 |

已经编译好 sidecar 时可以加 `--sidecar-bin <路径>` 省掉构建。端口 5173 被占用时用 `--web-port`。

## 2. Doctor：确认实例健康

```sh
export MIAOJU_ENDPOINT_DIR=$(python3 -c "import json;print(json.load(open('.ratchet/state/browser-mode.json'))['endpointDir'])")
miaoju project list          # 退出码 0 即 sidecar 可用；8 = 实例没起来
node scripts/ratchet/browser-smoke.mjs --task <任务号>   # 前端连上 sidecar、无页面错误
```

`miaoju` 不在 PATH 时：`(cd cli && go build -o /tmp/miaoju ./cmd/miaoju)`。

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

## 5. Cleanup

结束 `browser-mode.mjs`（Ctrl-C / `kill -INT <pid>`，pid 在 state 文件里）：它会关 sidecar、关 Vite、删临时数据目录。证据目录保留。

## 已知边界

- 浏览器验证模式只替代了“取端口和 token”；数据目录切换、系统对话框、打开外链等壳功能测不了，这部分要在 macOS 桌面端人工看。
- 内置 Python 运行时在验证实例里找不到，脚本节点任务会失败（sidecar 启动时有警告），F1 不受影响。
