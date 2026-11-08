# 脚本拆分镜（F2）

用户在画布上放一个「脚本」节点（内部类型 `script`），两条路二选一：贴一段剧情、选风格和模型，让上游模型拆成分镜脚本；或者自己一行一行手写分镜表。分镜表定稿后可以「批量生视频」，但那一步只按分镜行铺视频生成节点，不提交生成。**只有「生成分镜脚本」这一步会真的花上游 token**；其余环节——建节点、读风格列表、写分镜行、铺视频节点、导入结果——都不计费。本功能图逐项标出哪一步计费，以及哪一步能绕开界面做。

## Sub-features

每一项后面是「成本 · 可达性」和代码依据（行号用 `sed -n` 核对过）。

- `script-styles` 读风格列表与候选模型（`GET /api/v1/canvas-script/styles`）：**零成本 · REST 可达 · CLI 无此命令**。
  - 路由 `server/internal/http/router.go:310`；处理 `server/internal/http/canvas_script_handlers.go:29-45`；风格/候选来自内置配置 `server/internal/canvasscript/service.go:189-209`；候选的 `credits` 桌面端恒为 0（`service.go:80-85`）。
  - 实测（T-0201，临时实例）：200，`styles` 8 项、`modelCandidates` 1 项。证据 `.ratchet/evidence/T-0201/f2/styles.json`。
- `script-node` 在画布上放一个脚本节点：**零成本 · REST 可达 · CLI 不可建**。
  - 前端类型注册 `web/src/modules/canvas/editor/registry.tsx:45`、侧栏入口 `registry.tsx:71`；类型名 `web/src/modules/canvas/editor/types.ts:15`。
  - REST：`POST /api/v1/projects/<id>/canvas/nodes`（`router.go:235` → `server/internal/http/canvas_mutation_handlers.go:159-169` → `server/internal/canvasstate/state.go:27-68`）。`UpsertNode` **不校验节点类型**，任何类型都能写进画布快照。
  - CLI：`script` 不在 CLI 可写类型里（`server/internal/cliapi/node_mapper.go:87-100`），也不在只读清单里（`server/internal/cliapi/node_build.go:99-104`），所以报的是 `INVALID_NODE_TYPE` 而不是 `NODE_READONLY_TYPE`（`node_build.go:78-90`）。实测 `miaoju node create --type script` → 退出码 2，stderr `INVALID_NODE_TYPE`。
- `script-rows` 手写/编辑分镜行（镜号、简述、时长、提示词、颜色标记）：**零成本 · 仅界面编辑（REST PATCH 可写，CLI 只读）**。
  - 界面：`web/src/modules/canvas/editor/nodes/script/ScriptPanel.tsx:60`（全屏面板）、`ScriptPanel.tsx:109` → `ShotTable.tsx:42`。
  - REST：`PATCH /api/v1/projects/<id>/canvas/nodes/<节点id>`（`router.go:237`，前端封装 `web/src/shared/api.ts:204-218`）。
  - CLI：`miaoju canvas get` 读出来的脚本节点是 `writable: false`（`node_mapper.go:125-130`），只能读。
  - 实测：PATCH 写 2 行 → `miaoju canvas get` 读回 2 行、`colorMark` 是 `red`。
- `script-assets` 给分镜里的角色/场景/物品配参考图：**零成本 · 仅界面**。
  - `ScriptPanel.tsx:114`（`AssetsPanel`）、`web/src/modules/canvas/editor/nodes/script/AssetsPanel.tsx`。配图本身是画布上的图片节点，CLI 侧属于 F1 的范围。
- `script-generator-open` 打开发电机面板（贴剧本、选风格、选模型、选最长秒数）：**零成本 · 仅界面**。
  - 入口按钮 `web/src/modules/canvas/editor/nodes/script/ScriptNode.tsx:150`（面板可见性 `ScriptNode.tsx:84`）；面板 `ScriptGenerator.tsx:75-261`；输入框 `ScriptGenerator.tsx:170`。
  - 打开面板只会触发一次 `GET /canvas-script/styles`（`ScriptGenerator.tsx:84-89`），没有写请求。
- `script-submit` 点「生成分镜脚本」提交任务（`POST /api/v1/canvas-script-tasks`）：**计费（G5）· REST 可达但禁止 · verified-unreachable**。
  - 前端 `ScriptGenerator.tsx:136`（`submitCanvasScriptTask`，`api.ts:1104-1112`）→ 路由 `router.go:311` → 处理 `canvas_script_handlers.go:57-80` → 落任务 `canvasscript/service.go:225-280` → worker `service.go:375-459` `execute` → `service.go:416` `models.ResolveUpstream` 解析上游地址与 Key → `service.go:431` `s.skills.Run` 真正调上游模型，按 token 计费。
  - 缺的前提：**G5 批准**。本任务不执行、不探测、不绕过闸（`scripts/ratchet/hooks/pawl.py:371-374` 会拦带写意图的 `/api/v1/canvas-script-tasks`）。
  - 注意：临时验证实例没有内置 Python 运行时（sidecar 启动警告），生成任务最终会失败；但**失败前已经调过上游模型**，所以「跑不通」不等于「不花钱」。
- `script-poll` 轮询任务状态（`GET /api/v1/canvas-script-tasks/<taskID>`）：**零成本（只读）· REST 可达 · CLI 无此命令**。
  - 路由 `router.go:312`、处理 `canvas_script_handlers.go:82-106`；前端轮询 `ScriptGenerator.tsx:39`（3 秒一次，`ScriptGenerator.tsx:21`）。
  - 但一个真实的 taskID 只能由付费的 `script-submit` 产生，所以本项只在代码里标出，不进一键脚本。实测 `GET /canvas-script-tasks/999999` → 404 `{"error":"任务不存在"}`，不改任何状态。
- `script-import` 任务成功后把 candidate.json 导入分镜表与资产：**零成本 · 仅界面**。
  - `ScriptGenerator.tsx:39-60`（轮询拿到 `succeeded` 后调用 import）；映射唯一一份在 `importCandidate.ts:53`；`script/importCandidate.test.ts` 覆盖映射规则。没有对应的服务端接口。
- `script-batch-video` 右键「批量生成视频」→「确认并创建视频生成器组」：**零成本 · 仅界面 · CLI 无对应命令**。
  - 菜单 `ScriptNode.tsx:94-101`；弹层 `BatchVideoDialog.tsx:99-107`（`handleConfirm` 只调 `spawnVideoNodes`）；铺节点实现 `batchVideo.ts:51-98`——只有 `store.addNode('gen-video')` + `addConnection`，**不提交任何生成**（`batchVideo.ts:8-9` 的注释即这条铁律）。
  - 名字里带「生成」但实际零成本，容易误判成计费项，别被名字骗。
  - 等价的 CLI 路径是逐个 `miaoju node create --type video-generator ...`（属于 F1 的范围），不是这个入口。
- `script-upstream-text` 把上游文字节点的内容拼进剧本一起提交：**零成本 · 仅界面建文字节点（CLI 不可）· 连线 REST 可达**。
  - `ScriptGenerator.tsx:96-99`（只认 `text-node`）、`ScriptGenerator.tsx:124`（拼在输入框文本前面）。
  - `text-node` 在 CLI 只读清单里（`node_build.go:100`）；连线可用 `POST /api/v1/projects/<id>/canvas/connections`（`router.go:239`）。
- `script-style-config` 「脚本设置」里的风格/候选模型配置：**零成本 · 无写接口（写死内置）**。
  - `server/internal/http/settings_center_refs.go:13-19`；`server/cmd/sidecar/settings_center_wiring_test.go:41` 断言 `PUT /api/v1/settings/configs/canvas_script_config` 是 404/405。用户改不了风格清单，只能读。

**成本小结**：11 项 sub-feature 里只有 `script-submit` 一项计费（G5，未执行）；其余 10 项都是零成本。可达性上，风格列表与节点读写有 REST 入口，CLI 完全没有脚本节点的写能力（读 `canvas get` 可以）。

## How to get to it (user POV)

- 桌面端：打开一个项目进入画布，从左侧工具栏（`generate` 组）添加「脚本」节点（`registry.tsx:71`）。卡片空态给两条入口：「剧本生成分镜脚本」（打开发电机，填剧情、选风格、选模型、选最长秒数，点右上角箭头提交）和「自己编写分镜脚本」（打开全屏面板，直接编辑分镜表与资产配图）。右键卡片有「批量生成视频」。
- 浏览器验证模式：`/canvas/<项目id>`，画面和桌面端一致（见 SKILL.md 第 1 节）。T-0201 的截图 `.ratchet/evidence/T-0201/f2/smoke.png` 里能看到脚本卡片、中央的四横线占位图形、「确认分镜 / 准备资产」步骤圈和「打开脚本节点 →」按钮。
- 终端：**不能建脚本节点**，`miaoju node --help` 的可写类型只有 image-input / audio-input / video-input / image-generator / video-generator。能做的只有读：`miaoju canvas get` 和 `miaoju node get <节点id>`（`writable: false`）。要建节点只能走 sidecar 的画布 REST（见下）。

## Driving it with miaoju + sidecar REST + browser-smoke

Preconditions:

- 下面的 `${MIAOJU_BIN:-miaoju}`：按 SKILL.md 第 2 节构建到 `/tmp/miaoju` 并设了 `MIAOJU_BIN` 时用它，否则用 PATH 里的 `miaoju`。
- 已按 SKILL.md 起好验证实例，`MIAOJU_ENDPOINT_DIR` 指向它，doctor 通过。
- 当前目录是一个空的临时目录（`project use` 会把项目绑定到当前目录）。
- 浏览器取证要 Playwright：`node scripts/ratchet/browser-smoke.mjs` 从 `NODE_PATH` 或仓库里解析 playwright（见 Gotchas 的版本坑）。
- sidecar REST 用的 token 是 `.ratchet/state/browser-mode.json` 里 `url` 的 `ratchet_token`（**不是** `endpointDir/cli-endpoint.json` 里的 token，那个只过 `/api/v1/cli/*`，打用户侧接口会 401）。

一键跑：`scripts/ratchet/verify-f2.sh <任务号>`。它只做零成本部分，下面是它做的每一步，手动跑时照抄：

- **建项目。** `${MIAOJU_BIN:-miaoju} project create --name "F2 验证 <任务号>"`，然后 `project use <id>`。退出码 0。
- **读风格列表。** `GET /api/v1/canvas-script/styles`（Authorization: Bearer `<ratchet_token>`）。断言 `styles` 非空、每项 `name` 非空、候选模型带 `modelId`/`displayName`/`credits`。
- **确认 CLI 建不了脚本节点。** `${MIAOJU_BIN:-miaoju} node create --type script`，预期**退出码 2** 且 stderr 含 `INVALID_NODE_TYPE`。这是被测行为，不是脚本出错。
- **抢编辑权再写画布。** `POST /api/v1/projects/<id>/canvas/owner/claim`（body `{}`）返回 `ownerToken`；之后的画布写请求都带它。**必须在打开浏览器之前写完**：页面一打开就会自己去 claim。
- **建脚本节点。** `POST /api/v1/projects/<id>/canvas/nodes`，body `{"ownerToken": <token>, "node": {"id": "f2-script-1", "type": "script", "x":120, "y":120, "width":500, "height":280, "settings": {...}}}`。返回 `{"version": n}`。
- **写分镜行。** `PATCH /api/v1/projects/<id>/canvas/nodes/f2-script-1`，body `{"ownerToken": <token>, "patch": {"settings": {"rows": [2 行]}}}`。
- **读回。** `${MIAOJU_BIN:-miaoju} canvas get`。断言恰好 1 个节点、`type == "script"`、`writable == false`、`params.rows` 是刚写的 2 行。
- **浏览器取证。** `node scripts/ratchet/browser-smoke.mjs --task <任务号> --sub f2 --path /canvas/<项目id> --wait-selector '[data-node-id]' --expect-count 1`。`smoke.json` 里 `ok: true`、`waitFor.count` 是 1；`smoke.png` 能看到脚本卡片。

付费路径（`script-submit`、`miaoju run`、界面上的生成按钮）一律不跑，也不写别的写法绕闸去跑。要验它只能等 G5 批准后人工点一次，或者对着 `service.go:375-459` 读代码。

## Gotchas

- **「批量生成视频」不计费。** 它只按分镜行铺 `gen-video` 节点（`batchVideo.ts:51-98`），不提交生成。真正计费的只有「生成分镜脚本」那个箭头按钮（`ScriptGenerator.tsx:136`）；批量生视频铺出来的 `gen-video` 节点，要用户自己逐个点生成才会花钱。
- **CLI 建不了脚本节点，报错不是 `NODE_READONLY_TYPE`。** `script` 既不在可写表（`node_mapper.go:87-100`）也不在只读清单（`node_build.go:99-104`），所以是 `INVALID_NODE_TYPE`（退出码 2）。想验证「CLI 能不能建」就照抄 `node create --type script`，别拿内部名 `script-node` 之类去猜（同样报 `INVALID_NODE_TYPE`）。
- **两种 token 别搞混。** `endpointDir/cli-endpoint.json` 的 token 只过 `/api/v1/cli/*`；用它打 `/api/v1/canvas-script/styles` 会 401。用户侧接口要用 state 文件 `url` 里的 `ratchet_token`。token 不要写进证据、不要打印。
- **画布写入要先 claim，而且要在开浏览器之前。** `mutate` 在个人身份下会 `owner.Require`（`canvas_mutation_handlers.go:245-251`），但 `writeCanvasOwnerError` 不认的错误会被放过、继续写——所以空 `ownerToken` 有时也能写成功，靠这个巧合很脆。正确做法是先 `POST /canvas/owner/claim` 拿 token。浏览器页面一打开就会抢锁，抢到之后再从脚本写就会 `CANVAS_OWNER_LOST`。
- **Playwright 版本和本机缓存的 Chromium 对不上。** `npm i --prefix /tmp/t2-pw playwright` 装到 1.65 要 chromium-1254，本机缓存只有 1228，冒烟直接报 `Executable doesn't exist ... chrome-headless-shell`。补一次 `node_modules/.bin/playwright install chromium`（不用 `npx`），再 `NODE_PATH=/tmp/t2-pw/node_modules node scripts/ratchet/browser-smoke.mjs ...`。
- **临时实例没有内置 Python，脚本节点任务必失败。** sidecar 启动会警告「找不到内置 Python 运行时」。这不代表生成路径零成本：`execute` 先 `ResolveUpstream`（`service.go:416`）、再 `skills.Run`（`service.go:431`）调上游，之后才轮到 Python 那一步。所以就算任务最终失败，上游 token 也已经花掉了。
- **风格 key 是 `"name version"` 全串。** 同一个 `name` 有多个 `version`（现场是「真人短剧风格」v3 / v2-1.8 / v2 / v1-1.8倍漫解），`service.go:52-59` 的 `Key()` 把两者拼起来。断言只能按 key 集合比，不能按 `name` 去重。
- **画布挂着 EventSource 长连接**（`/api/v1/projects/<id>/events`，`server/internal/http/router.go:224`），网络永远不会空闲。自己写 Playwright 时不要用 `waitUntil: 'networkidle'`：macOS 上会一直等到超时。用 `domcontentloaded` 再等 `[data-node-id]` 的数量对上。
- **`.sh` 里 `$VAR` 紧跟中文会吞掉变量名**（bash 3.2 + `set -u`，经验 #36、#37）。写成 `${VAR}`。
- 上次验证：2026-11-08，macOS 26.7.1（arm64，系统 bash 3.2.57、Node 22、Go 1.26.5、Playwright Chromium），`verify-f2.sh T-0201` 全部断言通过；计费调用 0 次。Linux 待复验。
