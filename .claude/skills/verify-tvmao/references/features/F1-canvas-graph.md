# F1 建图与连线

- **成本**：零成本（建节点、建连线、摆位都不会提交生成）
- **驱动**：CLI 可达 + 浏览器截图
- **一键跑**：`scripts/ratchet/verify-f1.sh <任务号>`（需要先起 `browser-mode.mjs`）

## 步骤

| # | 动作 | 命令 | 期望 |
|---|---|---|---|
| 1 | 建项目并绑定目录 | `miaoju project create --name …` → `miaoju project use <id>` | 退出码 0，返回项目 id |
| 2 | 选图片模型 | `miaoju model list` | 取 `kind=image` 且 `available=true` 的一个 `modelId` |
| 3 | 上传参考图并建输入节点 | `miaoju asset upload ./ref.png --create-node` | 返回 `node.type=image-input` |
| 4 | 建生成节点并接上参考图 | `miaoju node create --type image-generator --model <id> --param prompt=… --left <输入节点>` | 返回新节点和 1 条边 |
| 5 | 摆位 | `miaoju node move --set <输入>=0,0 --set <生成>=420,0` | 退出码 0 |
| 6 | 读回画布 | `miaoju canvas get` | 2 个节点、1 条边，方向 输入 → 生成 |
| 7 | 审计实际输入 | `miaoju node get <生成节点>` | `derivedInputs.images` 恰好 1 张，就是第 3 步上传的素材 |
| 8 | 浏览器看画布 | `node scripts/ratchet/browser-smoke.mjs --task <任务号> --path /canvas/<项目id>` | 截图里能看到参考图、连线、生成节点；无页面错误 |

## 不要做

- 不要 `miaoju node run <生成节点>`：会计费。
- `derivedInputs` 里是带 token 的媒体地址，比较时去掉查询参数再比路径。

## 上次验证

2026-11-07，Linux 容器（Go 1.26、Node 22、Chromium），`verify-f1.sh T-0006` 全部断言通过。关口 1：一个不知情的新会话只读本 skill 跑通 F1（T-0005），它提出的含糊处已改进 skill。
