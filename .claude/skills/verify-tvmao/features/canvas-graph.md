# 建图与连线（F1）

用户在画布上放一张参考图，再放一个图片生成节点，用连线把参考图接给它。保存后重新打开画布，节点和连线都还在，生成节点实际会拿到这张参考图。整个过程不提交生成，零成本。

## Sub-features

- `graph-upload` 上传参考图，自动生成一个图片输入节点。
- `graph-generator` 建一个图片生成节点并选好模型。
- `graph-connect` 把参考图连到生成节点，方向是 参考图 → 生成节点。
- `graph-layout` 摆放节点位置，刷新后位置不变。
- `graph-inputs` 生成节点的实际输入（derivedInputs）正好是连进来的那张图。

## How to get to it (user POV)

- 桌面端：打开一个项目进入画布，把图片拖进画布，或者用工具栏添加图片节点和图片生成节点，从输出口拖线到输入口。
- 终端：`miaoju asset upload <图> --create-node`、`miaoju node create --type image-generator --left <输入节点>`。
- 浏览器验证模式：`/canvas/<项目id>`，看到的画面和桌面端一样（见 SKILL.md 第 1 节）。

## Driving it with miaoju + browser-smoke

Preconditions:

- 下面的 `${MIAOJU_BIN:-miaoju}`：按 SKILL.md 第 2 节构建到 `/tmp/miaoju` 并设了 `MIAOJU_BIN` 时用它，否则用 PATH 里的 `miaoju`。

- 已按 SKILL.md 起好验证实例，`MIAOJU_ENDPOINT_DIR` 指向它，doctor 通过。
- 当前目录是一个空的临时目录（`project use` 会把项目绑定到当前目录）。

一键跑：`scripts/ratchet/verify-f1.sh <任务号>`。下面是它做的每一步，手动跑时照抄：

- **建项目。** `${MIAOJU_BIN:-miaoju} project create --name "F1 验证 <任务号>"`，然后 `${MIAOJU_BIN:-miaoju} project use <id>`。退出码 0，返回项目 id。
- **选模型。** `${MIAOJU_BIN:-miaoju} model list`。取 `kind=image` 且 `available=true` 的一个 `modelId`。只读，不运行。
- **上传参考图。** `${MIAOJU_BIN:-miaoju} asset upload ./ref.png --create-node`。返回的 `node.type` 是 `image-input`。
- **建生成节点并连线。** `${MIAOJU_BIN:-miaoju} node create --type image-generator --model <modelId> --param prompt="…" --left <输入节点>`。返回新节点和 1 条边。
- **摆位。** `${MIAOJU_BIN:-miaoju} node move --set <输入>=0,0 --set <生成>=420,0`。退出码 0。
- **读回画布。** `${MIAOJU_BIN:-miaoju} canvas get`。2 个节点、1 条边，`fromNodeId` 是输入节点，`toNodeId` 是生成节点。
- **审计实际输入。** `${MIAOJU_BIN:-miaoju} node get <生成节点>`。`derivedInputs.images` 恰好 1 张，去掉查询参数后的路径等于上传素材的路径。
- **浏览器取证。** `node scripts/ratchet/browser-smoke.mjs --task <任务号> --sub f1 --path /canvas/<项目id> --wait-selector '[data-node-id]' --expect-count 2`。`smoke.json` 里 `ok: true`、`waitFor.count` 是 2；打开 `smoke.png` 能看到参考图、连线、生成节点。

## Gotchas

- 绝不要 `miaoju run <生成节点>` 或 `miaoju node run <生成节点>`，也不要点界面上的生成按钮：一提交就计费，而且取消不了（G5）。
- `derivedInputs` 里是带 token 的媒体地址，比较时去掉 `?` 之后的部分再比 `/media/` 后的路径。
- 冒烟打开 `/` 时会自动建一个默认项目，所以 F1 的项目 id 不一定是 1。断言要按自己建的项目来，不要按 id 猜。
- 输入节点摆在 (0,0) 时，有一部分会被左上角顶栏挡住，看截图时别误判成没渲染。
- 画布挂着 EventSource 长连接（`/api/v1/projects/<id>/events`），网络不会空闲。自己写 Playwright 时不要用 `waitUntil: 'networkidle'`：macOS 上会一直等到超时。用 `domcontentloaded`，再等 `[data-node-id]` 的数量对上。
- 上次验证：2026-11-07，Linux 容器（Go 1.26、Node 22、Chromium），`verify-f1.sh T-0007` 全部断言通过。2026-11-08 macOS 26.7.1 上 CLI 段全过，浏览器段卡在 networkidle，已改成等节点数，macOS 复验待补。关口 1：一个不知情的新会话只读本 skill 跑通了 F1（T-0005）。
