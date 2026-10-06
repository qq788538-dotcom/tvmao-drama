# P3a 前端画布迁移（搬运、删减、接 sidecar、启动入口）Implementation Plan

> **实施后注（2026-10-06，正文未改）**
> - 最终审查修复轮（d7052bd）新增：401/加载失败不再卡 Loading、诊断报告不带 token、StrictMode 不重复建项目、生产包去掉测试钩子、sidecar 重启后 REST/Agent WS 刷新端口与 token。整合时 `DiagPage.tsx` 保留 P8 的 `SidecarGate` 写法并补"← 返回画布"链接；`sidecarWsUrl` 已无调用方。出处：p3a 账本、integration-report.md。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把参考仓的自由画布模块搬进 `web/`，删掉发布/分享/协作/登录/ChatPanel，让所有 REST、WebSocket 请求改走本机 sidecar，启动直接进画布（F9），字体本地化，参考仓 vitest 测试在 jsdom 下全部通过。

**Architecture:** 从参考仓 `fea92ab` 用 `git archive` 原样复制 `modules/canvas/**`、`shared/**`、`styles`、`types`、测试初始化文件和 Tailwind/PostCSS 配置，目录结构与 `@/` 别名不变。桌面端新增的代码集中放在 `web/src/desktop/`（启动流程、路由入口、主题、上一次项目记录）和 `web/src/shared/sidecarSession.ts`（同步读取本次 sidecar 的端口和 token）。`main.tsx` 先 `await getServerInfo()` 再渲染，axios 拦截器每个请求现读会话拼 baseURL 和 `Authorization`。画布页去掉编辑锁、观看模式和团队实时通道，持久化改用固定的本地 ownerToken。

**Tech Stack:** React 18.3 + Vite 5.4 + TypeScript 5.9 + vitest 2.1（jsdom 25）、AntD 5、Tailwind 3.4.17、axios 1、@tanstack/react-query 5、react-router-dom 6、zustand 4.5、@fontsource/inter 与 @fontsource/jetbrains-mono 5.3、pnpm 9。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`。本计划实现第 8 节 P3 的一部分：F1、F2（保留现有菜单，"保存到本地"放回菜单不在本计划）、F3、F5、F6、F8、F9、X1、X2、X4、X7、E1、E2、E3、E7，以及 D3、S3、SEC2 在前端的落地。

**参考仓：** `/Users/james/github/huabu_studio`，commit `fea92ab9ad58ac1aa6752855999c221fcb460d89`，下文写作 `REF`。**注意：参考仓当前 HEAD 是 `4d77979`，比 `fea92ab` 多两个只改登录注册的提交（`AuthForm.tsx`、`shared/api.ts` 共 10 行）。一律用 `git -C $REF archive fea92ab…` 取文件，不要从工作区直接 `cp`。** 下文所有"参考仓第 N 行"都指 `fea92ab` 版本。

**为什么这样切 P3a（与建议范围的差异）：**
- **X3（积分和估价 UI）不在 P3a。** 估价代码分散在 9 个最大的节点组件（GenImageNode、GenVideoNode、GenAudioNode、SubtitleEraseNode、TieredVideoNode、MultiAnglePanel、LightPanel、AddAssetDialog、ScriptGenerator）、`batchQueries.ts` 和 12 个测试文件里，和本计划其他改动没有交集，单独成一个子计划更好审查。P3a 只去掉画布顶栏的"积分"余额显示。在 P3b 之前，估价标签会显示 sidecar 的返回值（计费是空实现）或估价失败的文案，不影响生成。
- **协作代码只删到页面层。** 画布页里的编辑锁、心跳、观看模式、团队实时通道、在线成员条全部删除，团队和锁相关的 API 也删掉。`transientChannel.ts`、`presenceStore.ts`、`useNodeEditingBroadcast.ts` 以及 `store.ts` 里的 `applyRemoteOp`/`applyTransientPositions` 保留不动：页面不再注册通道、不再写入名册以后，这些代码都不会执行（等同参考仓个人项目的行为）；要删就得改 NodeShell（633 行）、TextNode 和 NodeShell.test 里与拖拽在途登记写在一起的 200 行测试，风险大、收益只是删掉不会执行的代码。这部分清理放到 P3c。

**P3 其余子计划（各自成文，本计划不含）：**
- **P3b 节点菜单与计费 UI：** 删除 X3 估价和积分 UI（上面 9 个组件、`batchQueries.loadEstimate`、`shared/priceNote.ts` 及对应测试）；"保存到本地"节点放回右键菜单（F2）；节点右键"在访达/资源管理器中显示"（S3，需要 Tauri 命令或 sidecar 接口）；生成结果"未保存到本地"提示和重试按钮（S5）。
- **P3c 协作残留清理：** 删除 `transientChannel`、`presenceStore`、`useNodeEditingBroadcast`、`store` 的远端 op 和瞬态坐标、`apiGuards` 白名单里的分享和团队条目，以及不会再被调用的工作流 JSON 导入导出弹窗；同步改 NodeShell、TextNode 和相关测试。

## Global Constraints

- 应用名 `喵剧AI`，标识 `com.miaoju.desktop`；目标平台 macOS 14+ 与 Windows x64。
- `web/` 内部目录与参考仓 `apps/web` 一致：复制来的文件保持原路径（`src/modules/canvas/**`、`src/shared/**`、`src/styles/canvas.css`、`src/types/**`、`src/setupTests.ts`、`src/vite-env.d.ts`），`@/` 指向 `web/src`。桌面端新增的模块只放在 `web/src/desktop/` 和 `web/src/shared/sidecarSession.ts`，`web/src/App.tsx` 与参考仓同位置但内容重写。
- 与参考仓独立演进（E2），复制来源记在 `UPSTREAM.md`。
- 前端与 sidecar 的接口沿用参考仓的 `/api/v1/*` REST + WebSocket 契约（D3）。前端只从 `getServerInfo()`（`web/src/shared/server.ts`）取端口和 token，**任何地方都不要把拼好的地址存进画布或 localStorage**（S3）。
- 每个 REST 请求都带 `Authorization: Bearer <token>`；WebSocket 的 token 放在查询串里（SEC2，浏览器不能给 WS 握手设请求头）。
- 媒体地址由 sidecar 在响应里给出完整可用的地址，前端不拼媒体 URL、不写死任何公网主机（S3、S12 待定）。
- 没有登录、账户、团队（X4、X2）：不出现 401 跳登录、token 存储、身份切换。
- 字体 Inter 400/500/600/700 和 JetBrains Mono 400/500 打包进应用，不从 Google Fonts 加载（E7）。
- AntD 暗色主题的 token 与参考仓 `main.tsx` 完全一致。
- 设置页（M8、M10）不在本计划；P0 诊断页暂时挂在隐藏路由 `/diag`，P4 移入"设置 → 环境诊断"。
- 代码注释用中文；从参考仓复制的注释提到的行为如果已不成立，就地改写。
- `pnpm --dir web build`（`tsc --noEmit && vite build`）和 `pnpm --dir web test` 必须全部通过。
- 除 Task 3 改 `src-tauri/tauri.conf.json` 的一行 CSP 设置、Task 1 改 `UPSTREAM.md`、Task 6 改 `docs/superpowers/plans/p0-carryover.md` 以外，不改 `web/` 以外的任何文件。

## Review Focus

1. **在 `/canvas/12` 这样的深层路由上刷新窗口**（开发时 Cmd+R，或打包版重载）→ 页面和 3D 导演台的 `models/*.glb` 要能正常加载，不能因为相对路径解析到 `/canvas/assets/…` 而白屏。（Task 1 把 `base` 改成 `'/'`，Step 8 断言 `dist/index.html` 引用的是 `/assets/…`；Task 6 手动清单第 4 项在 Tauri 里实测）
2. **sidecar 每次启动端口和 token 都不同** → 不能把旧端口固化在 axios 实例或任何缓存里，新会话建立后下一个请求必须立刻用新地址。（Task 4 `sidecar 换了端口和 token…` 用例）
3. **上一次打开的项目已被删除，或 sidecar 的项目接口还没就绪（P1b 未合入时返回 404）** → 前者要退到最近修改的项目，后者要显示错误页并允许重试，**不能**因为一次失败就新建一个空项目。（Task 3 `resolveStartupProject` 的 6 个用例 + `StartupRedirect` 用例）
4. **本机媒体跨源**：页面来源是 `tauri://localhost`（Windows 是 `http://tauri.localhost`），媒体在 `http://127.0.0.1:<端口>`。不带 `crossOrigin` 加载的图片会污染 canvas，宫格切分、标注、旋转烘焙读像素时抛 SecurityError。（Task 4 `mediaCrossOrigin` 用例；Task 6 手动清单第 7 项）
5. **画布刚露出来用户就开始编辑** → 局部 mutation 持久化必须在画布渲染前就配好 ownerToken，否则结构性改动会被静默丢弃（参考仓靠"抢锁完成前不渲染"保证，桌面端去掉抢锁后要靠"setReady 之前配置"保证）。（Task 2 `不抢锁：直接用固定的本地 ownerToken…` 用例断言配置顺序；Task 6 手动清单第 5 项）

## 并发执行安排

```
Task 1（基础搬运，串行，必须最先完成并合入）
   ├─ 第一批（并发）：Task 2 画布页瘦身与删除 ‖ Task 3 启动外壳 ‖ Task 5 诊断页遗留修复
   └─ 第二批：Task 4 API 层接 sidecar（依赖 Task 2 已合入；可与仍在进行的 Task 3、Task 5 并发）
Task 6（集成验收，最后，串行）
```

- 每个并发任务在自己的 git worktree 里做（仓库已在 `.gitignore` 忽略 `.worktrees/`）：`git worktree add .worktrees/p3a-taskN -b p3a-taskN p3a-base`，其中 `p3a-base` 是 Task 1 合入后的提交。各任务完成后按 Task 2 → Task 3 → Task 5 → Task 4 的顺序合入集成分支（Task 4 的 worktree 必须从"Task 1 + Task 2 已合入"的提交开出）。
- **Task 4 为什么不能和 Task 2 并发：** Task 4 要删 `getToken`、`claimCanvasOwner` 等导出，而 Task 2 合入前画布页还在用它们。在 Task 4 自己的 worktree 里 `tsc` 和 `CanvasEditorPage.test` 都会失败，没法独立验证。
- **Task 5 合入前须先合入 Task 3：** Task 5 给诊断页加了 `<Link>`，需要路由上下文；路由由 Task 3 的 `App.tsx` 提供。Task 5 的测试自带 `MemoryRouter`，在自己的 worktree 里照样能通过。
- **共享文件归属（只有列出的任务能改，其他任务碰都不能碰）：**
  - `web/package.json`、`pnpm-lock.yaml`、`web/vite.config.ts`、`web/tsconfig.json`、`web/tailwind.config.js`、`web/postcss.config.js`、`UPSTREAM.md`：只有 Task 1。后续任务不加依赖：本计划用到的依赖（包括 @fontsource）都在 Task 1 里装好。
  - `web/src/shared/sidecarSession.ts`、`web/src/desktop/lastProject.ts`：只有 Task 1 创建，其他任务只引用。
  - `web/src/shared/api.ts`、`api.test.ts`、`identity*`、`permissions.ts`、`modules/canvas/editor/agent/AgentPanel.tsx`、`modules/canvas/editor/utils/mediaCrossOrigin*`：只有 Task 4。
  - `web/src/modules/canvas/CanvasEditorPage*`、`CanvasProjectsPage.tsx`，以及 Task 2 列出的删除清单：只有 Task 2。
  - `web/src/main.tsx`、`web/src/App.tsx`、`web/src/desktop/`（`lastProject*` 除外）、`web/src/styles/canvas.css`、`web/src/shared/noPublicHost.test.ts`、`src-tauri/tauri.conf.json`：只有 Task 3。
  - `web/src/diag/**`：只有 Task 5。
  - `docs/superpowers/plans/p0-carryover.md`：只有 Task 6。
- 在同一个工作区里并发时，每个任务只能 `git add` 自己 **Files** 里列出的路径。

---

## 已核实的事实（实现者不要再猜）

以下结论来自对参考仓 `fea92ab` 的阅读，以及在临时目录里把本计划的每个任务实际做了一遍（`tsc`、vitest、`vite build` 全部通过）。

### F-1 复制范围与传递依赖

- 从 `CanvasEditorPage.tsx` 和 `CanvasProjectsPage.tsx` 出发做 import 闭包，画布模块之外只依赖 `src/shared/` 下 10 个文件（`api.ts`、`apiGuards.ts`、`BrandLogo.tsx`、`frameCache.ts`、`gestureGuard.ts`、`identity.ts`、`permissions.ts`、`priceNote.ts`、`queryClient.ts`、`useVersionCheck.tsx`）和 `src/styles/canvas.css`。不依赖 `modules/auth`、`billing`、`home`、`team`、`shell` 等。（`identity.ts` 只被 `api.ts` 引用，`useVersionCheck.tsx` 只被画布页引用。）
- 画布测试额外只依赖 `src/setupTests.ts`（jest-dom、内存版 localStorage、matchMedia 桩）。
- 3D 导演台需要 `public/models/ue-mannequin-retopology.glb`（及其 license 文本），通过 `import.meta.env.BASE_URL + 'models/…'` 加载。
- `src/types/soundtouchjs.d.ts` 是 `soundtouchjs` 的类型声明。
- 因此 Task 1 复制：`src/modules/canvas`（392 个文件，含 153 个测试）、`src/shared` 全部 16 个文件、`src/styles`、`src/types`、`src/setupTests.ts`、`src/vite-env.d.ts`、`public/models`、`tailwind.config.js`、`postcss.config.js`。P0 的 `web/src/shared/server.ts`、`server.test.ts` 与参考仓的 shared 文件不重名。

### F-2 依赖（版本号照抄参考仓 `apps/web/package.json`；括号里是参考仓 lockfile 实际解析的版本）

画布代码实际 import 的外部包：`@react-three/drei ^9.122.0`(9.122.0)、`@react-three/fiber ^8.17.14`(8.18.0)、`@tanstack/react-query 5`(5.99.0)、`@tanstack/react-virtual ^3.14.9`、`@tiptap/extension-character-count ^2`、`@tiptap/extension-placeholder ^2`、`@tiptap/react ^2`、`@tiptap/starter-kit ^2`(2.27.2)、`antd 5`(5.29.3)、`axios 1`(1.15.0)、`dompurify ^3.4.0`、`file-saver ^2.0.5`、`jszip ^3.10.1`、`lucide-react 0.460`、`marked ^18.0.2`、`mediabunny ^1.54.0`、`nanoid 5`、`react ^18.3.1`、`react-dom ^18.3.1`、`react-router-dom 6`(6.30.3)、`soundtouchjs ^0.3.0`、`three ^0.184.0`、`three-stdlib ^2.30.0`(2.36.1)、`tiptap-markdown ^0.8.10`、`zustand 4.5`(4.5.7)；`dayjs 1.11.20` 由 `main.tsx` 使用。参考仓里的 `camera-controls` 没有被直接 import（是 drei 自带的依赖），不加。
开发依赖新增：`@testing-library/jest-dom ^6.6.3`、`@testing-library/react ^16.3.0`、`@testing-library/user-event ^14.5.2`、`@types/dompurify ^3.2.0`、`@types/file-saver ^2.0.7`、`@types/marked ^6.0.0`、`@types/three ^0.184.1`、`autoprefixer 10`、`jsdom ^25.0.1`、`postcss 8`、`tailwindcss 3.4.17`。
桌面端新增：`@fontsource/inter ^5.3.0`、`@fontsource/jetbrains-mono ^5.3.0`。P0 已有的 `@tauri-apps/api ^2`、`@types/node ^22` 保留。

### F-3 复制后 `tsc --noEmit` 的 8 个错误（参考仓构建只跑 `vite build` 不跑 tsc，这些错误一直存在）

| 文件:行 | 错误 | 修法（Task 1 Step 5） |
|---|---|---|
| `editor/nodes/LocalSaveNode.tsx:39` | NodeShell 没有 `title`/`icon` 属性 | 删第 39–40 行（NodeShell 本来就不渲染这两个属性），import 去掉 `HardDrive` |
| `editor/nodes/ImageCompareNode.tsx:52` | 同上 | 删第 52–53 行（`Split` 在第 91 行还在用） |
| `editor/nodes/script/scriptData.test.ts:37` | 类型断言不重叠 | `as Record<…>` 改为 `as unknown as Record<…>` |
| `editor/nodes/trim/videoTrim.test.ts:72` | spy 类型不兼容 | 声明改为 `{ mockRestore: () => void }` |
| `editor/nodes/trim/VideoTrimBar.test.tsx:99` | `settings` 可能为 undefined | `created!.settings?.kind` |
| `editor/nodes/WaveformPlayer.tsx:333` | `audioEdit.duration` 可能为 null | 改为 `audioEdit.duration!`（运行时行为不变） |
| `editor/PromptMentionEditor.test.tsx:4,8` | `ConnectedMediaRef` 重复 import | 删第 8 行 |

### F-4 画布页现在怎么接编辑锁、观看模式、团队和 WebSocket（参考仓 `CanvasEditorPage.tsx`）

- 第 310–329 行：`me()` 取积分余额；`getToken()` + `getAuthContexts()` 判断个人/团队身份。
- 第 476–517 行：快照就绪后决定编辑还是观看。团队身份用标签页指纹（`teamOwnerToken`）直接配置持久化；身份未知时先 `probeCanvasOwner`，如果有人持锁就进观看模式（`observeMode.shouldObserve`）；其余情况 `claimCanvasOwner` 拿 ownerToken 再 `configureCanvasPersistence` + `setCanvasOwnerToken`。抢锁完成前页面一直显示 Loading（第 859 行）。
- 第 522–550 行：观看模式 `setReadOnly(true)`；每 10 秒 `heartbeatCanvasOwner`；`onCanvasOwnerLost` 订阅失权。
- 第 595–793 行：实时通道。**只有观看模式或团队身份才会**用 `new AgentClient({ url: '/api/v1/agent/ws', token: getToken() })` 建一条页面级 WS：收到 `canvas_changed` 后按版本号用 `parseRemoteCanvasOp` + `applyRemoteOp` 本地合并，或防抖后整拉快照；收到 `canvas_transient` 和 `canvas_presence` 后写瞬态坐标和在线名册，并 `setTransientChannel` 把发送能力交给节点组件。**个人编辑者不建这条连接**，生成结果靠节点自己轮询任务（`useReconcileCanvasTask`、`pollCanvasTaskUntilDone`）回填。
- 渲染：`VersionUpdateGate`（版本检查弹窗）、logo 回首页、"返回"去 `/canvas`、积分、发布与分享浮层（`PublishSharePopover` → `PublishWorkflowModal`/`TvShowSubmitModal`）、团队在线成员条（`PresenceBar`）、观看横幅（"接管编辑"）、`AgentPanel jwt={getToken()}`、失权弹窗。
- **裁剪后（Task 2）：** 去掉上面所有身份、锁、观看、实时通道、积分、发布、版本检查相关代码。快照载入时依次执行 `load → seedCanvasBaselines → configureCanvasPersistence → setCanvasOwnerToken('local') → setReady(true)`；`saveLastProjectId(pid)`；logo 改成不可点击；"返回"改为"所有项目"（`LayoutGrid` 图标，仍然先 flush 再去 `/canvas`）；`AgentPanel jwt={sidecarToken()}`。页面不再建任何 WebSocket（与参考仓个人编辑者一致）。
- `canvasMutations` 在 ownerToken 为空时会**静默丢弃**结构性 op（第 195、435 行），所以必须给一个非空的固定值。**对 P1b 的契约要求：** sidecar 的 canvasOwner 校验恒通过，不要求先 claim，也不校验 ownerToken 的值；前端不再调用 `/canvas/owner/claim`、`/heartbeat`、`/canvas-owner/probe`。

### F-5 被删模块的引用点（kept 文件 → 被删文件）

| 被删文件 | 仍保留的文件里谁引用它 | 处理 |
|---|---|---|
| `share/SharePage.tsx`、`share/SharedCanvasViewer.tsx` | 只有参考仓 `App.tsx`（不复制） | 直接删 |
| `panels/PublishSharePopover.tsx`、`PublishWorkflowModal.tsx`、`TvShowSubmitModal.tsx` | `CanvasEditorPage.tsx`（以及 `CanvasEditorPage.test.tsx` 的 mock） | Task 2 重写页面和测试 |
| `panels/CoverGalleryModal.tsx` | 只有 `TvShowSubmitModal.tsx` | 直接删 |
| `panels/ChangePasswordModal.tsx`、`panels/ChatPanel.tsx`、`editor/Toolbar.tsx`、`modules/canvas/README.md` | 无（死代码，没有任何文件 import） | 直接删 |
| `editor/PresenceBar.tsx`、`teamOwnerToken.ts`、`observeMode.ts`、`remoteOpFrame.ts` | 只有 `CanvasEditorPage.tsx` | Task 2 页面重写后变成孤儿，连同测试一起删 |
| `shared/useVersionCheck.tsx` | 只有 `CanvasEditorPage.tsx` | Task 2 |
| `shared/identity.ts` | 只有 `shared/api.ts`（401 拦截里的团队身份自愈） | Task 4 |
| `shared/permissions.ts` 里的 `getToken` 依赖 | `editor/Sidebar.tsx` 用 `useIsSuperAdmin` 控制"工作流 JSON 导入导出"入口 | Task 4 把 `useIsSuperAdmin` 改成恒为 `false`（S10、F10：运维入口隐藏），Sidebar 不动 |
| `CanvasProjectsPage.tsx` 第 396–398 行"我的分享"按钮（跳 `/workflows/mine`） | — | Task 2 删掉这 3 行和用不到的 `Button` import |

### F-6 `shared/api.ts` 的改法（Task 4）

- 第 1–212 行（登录 token 的 localStorage 读写、`identity` import、401 → 跳 `/login`、团队身份自愈、drama 调试日志拦截器）整体换成新的头部：每个请求现读 `sidecarApiBaseUrl()` 和 `sidecarToken()`，保留只读模式的写入拦截。
- 删除只被已删除功能调用的块（行号都是原文件的行号，一条 `sed` 一次删完，地址不会错位）：223–253（`LoginResult`/`syncAuthUser`/`applyAuthSession`/`login`/`register`）、259–262（`changePassword`）、347–351（`CanvasOwnerClaim`）、419–460（`claimCanvasOwner`/`heartbeatCanvasOwner`/`CanvasOwnerProbe`/`probeCanvasOwner`）、1015–1076（`chatCompletion` 及类型、`getMyLedger`）、1108–1171（项目 Chat 会话，只有 ChatPanel 用）、1335–1469（分享工作流）、1651–1682（CLI/Flow 浏览器登录）、1732–2168（TV Show、首页 banner、团队、会员、支付）。
- 保留：`AuthUser`、`me()`（P1d 的 `/me` 仍会提供）、`CanvasOwnerLostError`/`isCanvasOwnerLostError`（`canvasMutations` 用）、所有画布、生成、素材库、Agent、Seedance、角色库、脚本节点接口，以及 X3 的估价接口（P3b 再删）。其余所有导出的函数名和签名不变。
- `apiGuards.ts` 原样保留（`store.setReadOnly` 依赖它）。
- 参考仓的 API 客户端只发 `Authorization` 和 `Content-Type` 两个请求头（含 multipart 上传），sidecar 现在的 `Access-Control-Allow-Headers: Authorization, Content-Type, Range` 已经够用（P0 遗留事项"核对 Allow-Headers"的结论）。

### F-7 其他直连地址

- WebSocket：`editor/agent/AgentPanel.tsx:268` 用相对地址 `'/api/v1/agent/ws'`（在 Tauri 里会解析成 `ws://tauri.localhost/…`）。改成 `sidecarWsUrl('/api/v1/agent/ws')`。`AgentClient` 自己会把 `project_id` 和 `token` 拼进查询串，所以 `sidecarWsUrl` 不带 token。
- `fetch(url)` 读媒体（HistoryPanel、VideoInputNode、WaveformPlayer、audioEdit、DirectorDeskNode）用的都是 sidecar 返回的媒体 URL，跨源由 sidecar 的 CORS 处理，不需要改。
- 画布模块里 12 处用 `window.location.pathname.split('/canvas/')` 或 `/\/canvas\/(\d+)/` 解析项目 id（GenImage/GenVideo/GenAudio 节点、NodeShell、DirectorDeskNode、useReconcileCanvasTask、ScriptGenerator、TextGenPanel、MultiAnglePanel、`api.currentProjectId`）。所以**必须用 BrowserRouter 并保留 `/canvas/:projectId` 路径**，不能用 HashRouter。
- 非测试源码里唯一的公网 URL 字面量是 `styles/canvas.css` 第 4 行的 Google Fonts `@import`（Task 3 删掉，并加一条守护测试）。
- `CanvasProjectsPage` 用 `<style>{CARD_CSS}</style>` 在运行时插入样式，AntD 是 CSS-in-JS，所以 CSP 的 `style-src 'unsafe-inline'` 必须保持有效。Task 3 给 `tauri.conf.json` 加 `"dangerousDisableAssetCspModification": ["style-src"]`，防止 Tauri 往 style-src 里注入 nonce 或 hash，导致 `'unsafe-inline'` 失效（P0 遗留事项）。

### F-8 测试：保留、删除与 api.ts 的 mock 方式

- 基线：复制后（Task 1）共 160 个测试文件、1502 个用例全部通过（参考仓画布 153 个、shared 6 个、P0 1 个）。
- 删除的测试（7 个）：`__tests__/PublishSharePopover.test.tsx`、`__tests__/TvShowSubmitModal.test.tsx`、`editor/PresenceBar.test.tsx`、`editor/teamOwnerToken.test.ts`、`editor/observeMode.test.ts`、`editor/remoteOpFrame.test.ts`、`shared/identity.test.ts`（Task 2、Task 4）；重写的测试：`CanvasEditorPage.test.tsx`（删掉观看、团队、瞬态三组共 950 行，新增桌面端一组）、`shared/api.test.ts`（删掉注册、401、团队自愈、旧 key 搬迁四组，新增 sidecar 一组；owner 相关用例改用仍然存在的接口）。其余画布测试除 F-3 的 3 处类型修正外原样保留（包括 `presenceStore.test.ts`、`transientChannel.test.ts`、`textNodeEditingPresence.test.tsx`、`store.remoteOp.test.ts`，因为被测模块保留，见上文切分说明）。
- **参考仓测试 mock `api.ts` 的方式：** 35 个测试文件用 `vi.mock('@/shared/api', async (importOriginal) => ({ ...actual, 某函数: vi.fn() }))` 局部替换函数；`pollCanvasTask.test.ts` 和 `api.test.ts` 用 `vi.spyOn(api, 'get'|'post'|…)`；`api.test.ts` 的拦截器用例替换 `api.defaults.adapter`。没有测试依赖 `baseURL` 的值，也没有测试依赖 `getToken`（除了被重写的两个文件）。因此 Task 4 改拦截器、删导出不会影响其他测试。会话未建立时 `sidecarApiBaseUrl()` 返回参考仓原来的 `/api/v1`，测试环境下的行为不变。
- 最终（Task 6）：162 个测试文件、1434 个用例全部通过（另有 8 个参考仓原本就跳过的用例）。
- 已知现象：整套测试在机器负载高时（例如同时跑 `vite build`）偶尔会有用例超时，单独重跑即可通过。验收时请单独运行测试。

---

## 文件结构

```
web/
  package.json                    Task 1：依赖（F-2）
  vite.config.ts                  Task 1：jsdom、setupFiles、assetsInclude、base '/'
  tsconfig.json                   Task 1：types、resolveJsonModule、esModuleInterop
  tailwind.config.js              Task 1：从参考仓复制
  postcss.config.js               Task 1：从参考仓复制
  public/models/                  Task 1：3D 导演台人偶模型（从参考仓复制）
  src/
    main.tsx                      Task 3：字体、全局样式、dayjs、手势拦截，然后 startApp()
    App.tsx                       Task 3：路由 / → 启动跳转，/canvas，/canvas/:projectId，/diag
    setupTests.ts、vite-env.d.ts  Task 1：从参考仓复制
    types/soundtouchjs.d.ts       Task 1：从参考仓复制
    styles/canvas.css             Task 1 复制；Task 3 删 Google Fonts 的 @import
    desktop/                      桌面端独有
      lastProject.ts(+test)       Task 1：上一次打开的项目（localStorage）
      theme.ts                    Task 3：AntD 暗色主题（取自参考仓 main.tsx）
      startApp.tsx(+test)         Task 3：先连 sidecar 再渲染；失败页
      resolveStartupProject.ts(+test)  Task 3：F9 选项目算法
      StartupRedirect.tsx(+test)  Task 3：根路由，跳进画布或显示错误
    shared/
      server.ts(+test)            P0，不动
      sidecarSession.ts(+test)    Task 1：同步读取本次会话的端口和 token
      api.ts / api.test.ts        Task 1 复制；Task 4 改头部、删死代码、重写测试
      permissions.ts              Task 4：useIsSuperAdmin 恒为 false
      noPublicHost.test.ts        Task 3：守护"不写死公网地址"
      apiGuards、BrandLogo、frameCache、gestureGuard、priceNote、queryClient  原样
    modules/canvas/**             Task 1 复制；Task 2 改画布页、删 X1/X2/X4/X7；Task 4 改 AgentPanel、mediaCrossOrigin
    diag/                         P0；Task 5 修遗留问题
src-tauri/tauri.conf.json         Task 3：CSP 不注入 style-src nonce
UPSTREAM.md                       Task 1：记录复制来源
```

---

### Task 1: 基础搬运（复制参考仓画布模块、依赖、构建配置、两个共享小模块）

**Files:**
- Create（复制）: `web/src/modules/canvas/**`、`web/src/shared/{api,apiGuards,BrandLogo,frameCache,gestureGuard,identity,permissions,priceNote,queryClient,useVersionCheck}.ts(x)` 及其测试、`web/src/styles/canvas.css`、`web/src/types/soundtouchjs.d.ts`、`web/src/setupTests.ts`、`web/src/vite-env.d.ts`、`web/public/models/*`、`web/tailwind.config.js`、`web/postcss.config.js`
- Modify: `web/package.json`、`web/vite.config.ts`、`web/tsconfig.json`、`pnpm-lock.yaml`、`UPSTREAM.md`
- Modify（修 tsc 错误）: `web/src/modules/canvas/editor/nodes/LocalSaveNode.tsx`、`ImageCompareNode.tsx`、`WaveformPlayer.tsx`、`script/scriptData.test.ts`、`trim/videoTrim.test.ts`、`trim/VideoTrimBar.test.tsx`、`web/src/modules/canvas/editor/PromptMentionEditor.test.tsx`
- Create: `web/src/shared/sidecarSession.ts`、`web/src/shared/sidecarSession.test.ts`、`web/src/desktop/lastProject.ts`、`web/src/desktop/lastProject.test.ts`

**Interfaces:**
- Consumes: P0 的 `web/src/shared/server.ts`：`ServerInfo { port: number; token: string }`、`buildHttpUrl(info, path)`。
- Produces:
  - `sidecarSession.ts`：`setSidecarSession(info: ServerInfo | null): void`、`getSidecarSession(): ServerInfo | null`、`sidecarToken(): string`（未建立时为 `''`）、`sidecarApiBaseUrl(): string`（未建立时为 `'/api/v1'`，否则 `http://127.0.0.1:<port>/api/v1`）、`sidecarWsUrl(path: string): string`（未建立时原样返回，否则 `ws://127.0.0.1:<port><path>`，不带 token）、`resetSidecarSessionForTest(): void`。
  - `lastProject.ts`：`loadLastProjectId(): number | null`、`saveLastProjectId(id: number): void`、`clearLastProjectId(): void`；存储键 `miaoju.lastProjectId`。
  - 参考仓画布模块、`shared/*` 原样可用（所有导出名与参考仓一致）。

- [ ] **Step 1: 从参考仓 `fea92ab` 导出文件并复制到 `web/`**

```bash
cd /Users/james/github/miaoju-desktop
REF=/Users/james/github/huabu_studio
TMP=$(mktemp -d)
git -C "$REF" archive fea92ab9ad58ac1aa6752855999c221fcb460d89 \
  apps/web/src/modules/canvas apps/web/src/shared apps/web/src/styles apps/web/src/types \
  apps/web/src/setupTests.ts apps/web/src/vite-env.d.ts apps/web/public/models \
  apps/web/tailwind.config.js apps/web/postcss.config.js | tar -x -C "$TMP"
mkdir -p web/src/modules
cp -R "$TMP/apps/web/src/modules/canvas" web/src/modules/
cp -R "$TMP/apps/web/src/shared/." web/src/shared/
cp -R "$TMP/apps/web/src/styles" "$TMP/apps/web/src/types" web/src/
cp "$TMP/apps/web/src/setupTests.ts" "$TMP/apps/web/src/vite-env.d.ts" web/src/
cp -R "$TMP/apps/web/public/models" web/public/
cp "$TMP/apps/web/tailwind.config.js" "$TMP/apps/web/postcss.config.js" web/
find web/src/modules/canvas -type f | wc -l
ls web/src/shared
```

Expected: 第一行输出 `392`；`web/src/shared` 下有参考仓的 16 个文件，再加 P0 的 `server.ts`、`server.test.ts`。

- [ ] **Step 2: 写 `web/package.json`（整个文件替换）**

```json
{
  "name": "@miaoju/web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "test": "vitest run"
  },
  "dependencies": {
    "@fontsource/inter": "^5.3.0",
    "@fontsource/jetbrains-mono": "^5.3.0",
    "@react-three/drei": "^9.122.0",
    "@react-three/fiber": "^8.17.14",
    "@tanstack/react-query": "5",
    "@tanstack/react-virtual": "^3.14.9",
    "@tauri-apps/api": "^2",
    "@tiptap/extension-character-count": "^2",
    "@tiptap/extension-placeholder": "^2",
    "@tiptap/react": "^2",
    "@tiptap/starter-kit": "^2",
    "antd": "5",
    "axios": "1",
    "dayjs": "1.11.20",
    "dompurify": "^3.4.0",
    "file-saver": "^2.0.5",
    "jszip": "^3.10.1",
    "lucide-react": "0.460",
    "marked": "^18.0.2",
    "mediabunny": "^1.54.0",
    "nanoid": "5",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "6",
    "soundtouchjs": "^0.3.0",
    "three": "^0.184.0",
    "three-stdlib": "^2.30.0",
    "tiptap-markdown": "^0.8.10",
    "zustand": "4.5"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.6.3",
    "@testing-library/react": "^16.3.0",
    "@testing-library/user-event": "^14.5.2",
    "@types/dompurify": "^3.2.0",
    "@types/file-saver": "^2.0.7",
    "@types/marked": "^6.0.0",
    "@types/node": "^22",
    "@types/react": "^18.3.12",
    "@types/react-dom": "^18.3.1",
    "@types/three": "^0.184.1",
    "@vitejs/plugin-react": "^4.3.4",
    "autoprefixer": "10",
    "jsdom": "^25.0.1",
    "postcss": "8",
    "tailwindcss": "3.4.17",
    "typescript": "^5.6.3",
    "vite": "^5.4.10",
    "vitest": "^2.1.3"
  }
}
```

- [ ] **Step 3: 写 `web/vite.config.ts` 和 `web/tsconfig.json`（整个文件替换）**

`web/vite.config.ts`：

```ts
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  // 3D 导演台（modules/canvas/editor/nodes/director/desk）的模型库 import.meta.glob
  // 需要把 fbx/obj 当资源模块处理——与参考仓 vite 配置一致。
  assetsInclude: ['**/*.fbx', '**/*.obj'],
  plugins: [react()],
  // 资源用站点根的绝对路径：画布路由是 /canvas/:id 这样的多级路径，相对路径 './'
  // 在深层路由下刷新时会把 ./assets、./models 解析成 /canvas/assets… 而 404。
  // Tauri 的 tauri://localhost 与 http://tauri.localhost 都以站点根提供 dist，
  // 找不到的路径回落到 index.html，所以绝对路径 + BrowserRouter 可用。
  base: '/',
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: { port: 5173, strictPort: true },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/setupTests.ts',
  },
});
```

`web/tsconfig.json`：

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true,
    "isolatedModules": true,
    "esModuleInterop": true,
    "resolveJsonModule": true,
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] },
    "types": ["vite/client", "vitest/globals", "node"]
  },
  "include": ["src", "vite.config.ts"]
}
```

- [ ] **Step 4: 安装依赖并确认 tsc 只剩 F-3 那 8 个错误**

```bash
cd /Users/james/github/miaoju-desktop
pnpm install
cd web && npx tsc --noEmit 2>&1 | grep -c "error TS"
```

Expected: 输出 `8`，文件和行号与 F-3 表格一致。

- [ ] **Step 5: 修掉这 8 个错误（命令里的行号都是刚复制完时的行号）**

```bash
cd /Users/james/github/miaoju-desktop/web
E=src/modules/canvas/editor
sed -i '' -e '39,40d' -e "1s/import { Download, HardDrive } from 'lucide-react';/import { Download } from 'lucide-react';/" $E/nodes/LocalSaveNode.tsx
sed -i '' '52,53d' $E/nodes/ImageCompareNode.tsx
sed -i '' '37s/(legacy.rows\[0\] as Record<string, unknown>)/(legacy.rows[0] as unknown as Record<string, unknown>)/' $E/nodes/script/scriptData.test.ts
sed -i '' '72s/let createElementSpy: ReturnType<typeof vi.spyOn>;/let createElementSpy: { mockRestore: () => void };/' $E/nodes/trim/videoTrim.test.ts
sed -i '' '99s/created!\.settings\.kind/created!.settings?.kind/' $E/nodes/trim/VideoTrimBar.test.tsx
sed -i '' '333s|/ audioEdit\.duration)|/ audioEdit.duration!)|' $E/nodes/WaveformPlayer.tsx
sed -i '' '8d' $E/PromptMentionEditor.test.tsx
npx tsc --noEmit 2>&1 | grep -c "error TS"
```

Expected: 输出 `0`。用 `git diff --stat` 确认这 7 个文件各只改了 1–2 行。被删的两行是 `title="保存到本地"` / `icon={<HardDrive …/>}` 和 `title="图片对比"` / `icon={<Split …/>}`；`PromptMentionEditor.test.tsx` 被删的是第二个 `import type { ConnectedMediaRef } from './types';`。

- [ ] **Step 6: 写 `sidecarSession` 和 `lastProject` 的测试（先写测试）**

`web/src/shared/sidecarSession.test.ts`：

```ts
import { afterEach, describe, expect, it } from 'vitest';
import {
  getSidecarSession,
  resetSidecarSessionForTest,
  setSidecarSession,
  sidecarApiBaseUrl,
  sidecarToken,
  sidecarWsUrl,
} from './sidecarSession';

describe('sidecarSession', () => {
  afterEach(() => resetSidecarSessionForTest());

  it('会话未建立时退回参考仓的相对路径、空 token', () => {
    expect(getSidecarSession()).toBeNull();
    expect(sidecarToken()).toBe('');
    expect(sidecarApiBaseUrl()).toBe('/api/v1');
    expect(sidecarWsUrl('/api/v1/agent/ws')).toBe('/api/v1/agent/ws');
  });

  it('建立会话后给出 127.0.0.1 上的 REST / WS 地址和 token', () => {
    setSidecarSession({ port: 51234, token: 'tok' });
    expect(sidecarToken()).toBe('tok');
    expect(sidecarApiBaseUrl()).toBe('http://127.0.0.1:51234/api/v1');
    // WS 地址不带 token：AgentClient 自己拼 ?project_id=&token=
    expect(sidecarWsUrl('/api/v1/agent/ws')).toBe('ws://127.0.0.1:51234/api/v1/agent/ws');
  });
});
```

`web/src/desktop/lastProject.test.ts`：

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clearLastProjectId, loadLastProjectId, saveLastProjectId } from './lastProject';

describe('lastProject', () => {
  beforeEach(() => localStorage.clear());

  it('没有记录时返回 null', () => {
    expect(loadLastProjectId()).toBeNull();
  });

  it('存了就能读回来；清掉后又是 null', () => {
    saveLastProjectId(42);
    expect(loadLastProjectId()).toBe(42);
    clearLastProjectId();
    expect(loadLastProjectId()).toBeNull();
  });

  it('非法值（被手改过的存储）当作没有记录', () => {
    localStorage.setItem('miaoju.lastProjectId', 'abc');
    expect(loadLastProjectId()).toBeNull();
    localStorage.setItem('miaoju.lastProjectId', '-3');
    expect(loadLastProjectId()).toBeNull();
  });

  it('不存非法 id', () => {
    saveLastProjectId(0);
    saveLastProjectId(Number.NaN);
    expect(loadLastProjectId()).toBeNull();
  });

  it('存储抛错时读返回 null、写不抛', () => {
    const getSpy = vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError');
    });
    const setSpy = vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceeded');
    });
    expect(loadLastProjectId()).toBeNull();
    expect(() => saveLastProjectId(7)).not.toThrow();
    getSpy.mockRestore();
    setSpy.mockRestore();
  });
});
```

Run: `pnpm --dir web exec vitest run src/shared/sidecarSession.test.ts src/desktop/lastProject.test.ts`
Expected: FAIL，报 `Failed to resolve import "./sidecarSession"` 和 `"./lastProject"`。

- [ ] **Step 7: 实现这两个模块**

`web/src/shared/sidecarSession.ts`：

```ts
// 本次启动的 sidecar 会话（端口 + token）的同步入口。
//
// getServerInfo()（./server.ts）是异步的，而 axios 拦截器、AgentClient 构造这些地方
// 需要同步拿到地址。所以 main.tsx 在渲染任何页面之前先 await 一次 getServerInfo()，
// 成功后调 setSidecarSession() 存进这里，之后全局同步读取。
//
// 只存在内存里：端口和 token 每次启动都会变，**不要**把这里拼出来的地址写进画布
// 或 localStorage（spec S3）。
import { buildHttpUrl, type ServerInfo } from './server';

let session: ServerInfo | null = null;

export function setSidecarSession(info: ServerInfo | null): void {
  session = info;
}

export function getSidecarSession(): ServerInfo | null {
  return session;
}

/** 本次启动的 token；会话未建立（单元测试、纯浏览器调试）时为空串。 */
export function sidecarToken(): string {
  return session?.token ?? '';
}

/**
 * REST 接口的 baseURL。会话未建立时退回参考仓的相对路径 `/api/v1`，
 * 让不关心 sidecar 的单元测试保持原样。
 */
export function sidecarApiBaseUrl(): string {
  return session ? buildHttpUrl(session, '/api/v1') : '/api/v1';
}

/**
 * WebSocket 地址，**不带 token**：AgentClient 会自己把 token 和 project_id 拼进
 * 查询串（参考仓的约定），这里再拼一次就重复了。会话未建立时原样返回 path。
 */
export function sidecarWsUrl(path: string): string {
  return session ? `ws://127.0.0.1:${session.port}${path}` : path;
}

/** 仅供测试清状态。 */
export function resetSidecarSessionForTest(): void {
  session = null;
}
```

`web/src/desktop/lastProject.ts`：

```ts
// 「上一次打开的项目」记录（spec F9）。启动时据此直接进画布。
//
// 存 localStorage：只是一个提示，丢了也只是退回「最近修改的项目」。读写都包
// try/catch——存储不可用时当作没有记录，绝不让它挡住启动。
const KEY = 'miaoju.lastProjectId';

export function loadLastProjectId(): number | null {
  try {
    const raw = localStorage.getItem(KEY);
    const n = Number(raw);
    return raw && Number.isInteger(n) && n > 0 ? n : null;
  } catch {
    return null;
  }
}

export function saveLastProjectId(id: number): void {
  try {
    if (Number.isInteger(id) && id > 0) localStorage.setItem(KEY, String(id));
  } catch {
    /* 存储不可用：静默忽略 */
  }
}

export function clearLastProjectId(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* 同上 */
  }
}
```

Run: `pnpm --dir web exec vitest run src/shared/sidecarSession.test.ts src/desktop/lastProject.test.ts`
Expected: PASS（7 个用例）。

- [ ] **Step 8: 全量验证**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web test 2>&1 | tail -4
pnpm --dir web build 2>&1 | tail -2
grep -o 'src="/assets/[^"]*"' web/dist/index.html
```

Expected: `Test Files  162 passed (162)`、`Tests  1509 passed | 8 skipped`；构建成功（有 chunk 大于 500 kB 的警告，属正常）；`index.html` 里的脚本引用是 `/assets/index-….js`（绝对路径）。此时 `main.tsx` 仍是 P0 诊断页，应用照常可以运行。

- [ ] **Step 9: `UPSTREAM.md` 表格追加一行**

```markdown
| 2026-10-06 | `fea92ab9ad58ac1aa6752855999c221fcb460d89` | `apps/web/src/modules/canvas/**`、`src/shared/**`、`src/styles`、`src/types`、`src/setupTests.ts`、`src/vite-env.d.ts`、`public/models/`、`tailwind.config.js`、`postcss.config.js` | P3a 前端画布迁移；参考仓 HEAD 已到 `4d77979`，用 `git archive fea92ab` 取文件 |
```

- [ ] **Step 10: 提交**

```bash
git add web/package.json web/vite.config.ts web/tsconfig.json web/tailwind.config.js web/postcss.config.js \
  web/public/models web/src/modules web/src/shared web/src/styles web/src/types web/src/setupTests.ts \
  web/src/vite-env.d.ts web/src/desktop pnpm-lock.yaml UPSTREAM.md
git commit -m "feat(web): 从参考仓 fea92ab 复制画布模块与 shared，补 sidecar 会话与上一次项目记录"
git tag p3a-base
```

---

### Task 2: 画布页瘦身，删除发布、协作、账户、ChatPanel（X1、X2、X4、X7，F9 的画布侧）

**Files:**
- Modify（整体替换）: `web/src/modules/canvas/CanvasEditorPage.tsx`
- Modify（整体替换）: `web/src/modules/canvas/CanvasEditorPage.test.tsx`
- Modify: `web/src/modules/canvas/CanvasProjectsPage.tsx`（删第 396–398 行、antd import 里的 `Button,`）
- Delete: `web/src/modules/canvas/share/SharePage.tsx`、`share/SharedCanvasViewer.tsx`、`panels/PublishSharePopover.tsx`、`panels/PublishWorkflowModal.tsx`、`panels/TvShowSubmitModal.tsx`、`panels/CoverGalleryModal.tsx`、`panels/ChangePasswordModal.tsx`、`panels/ChatPanel.tsx`、`__tests__/PublishSharePopover.test.tsx`、`__tests__/TvShowSubmitModal.test.tsx`、`editor/PresenceBar.tsx`、`editor/PresenceBar.test.tsx`、`editor/teamOwnerToken.ts`、`editor/teamOwnerToken.test.ts`、`editor/observeMode.ts`、`editor/observeMode.test.ts`、`editor/remoteOpFrame.ts`、`editor/remoteOpFrame.test.ts`、`editor/Toolbar.tsx`、`README.md`（以上都在 `web/src/modules/canvas/` 下）、`web/src/shared/useVersionCheck.tsx`

**Interfaces:**
- Consumes: Task 1 的 `sidecarToken()`（`@/shared/sidecarSession`）、`saveLastProjectId()`（`@/desktop/lastProject`）；`canvasMutations` 的 `configureCanvasPersistence`、`setCanvasOwnerToken`、`flushCanvasMutations`、`resetCanvasPersistence`、`seedCanvasBaselines`、`persistNodeById`（参考仓原样）。
- Produces:
  - `CanvasEditorPage`（路由 `/canvas/:projectId`）不再引用 `getToken`、`me`、`claimCanvasOwner`、`heartbeatCanvasOwner`、`probeCanvasOwner`、`getAuthContexts`、`isCanvasOwnerLostError`。Task 4 依赖这一点才能删这些导出。
  - `export const LOCAL_OWNER_TOKEN = 'local'`（画布持久化用的固定 ownerToken，见 F-4）。
  - 顶栏按钮"所有项目"：先 `flushCanvasMutations()`，再跳 `/canvas`（Task 3 把 `/canvas` 路由到 `CanvasProjectsPage`）。
  - 快照载入成功后调用 `saveLastProjectId(pid)`。

- [ ] **Step 1: 用新测试整体替换 `CanvasEditorPage.test.tsx`**

整个文件替换成下面的内容。前三组用例沿用参考仓（访问保护、就地改名、视图切换），只删掉抢锁和余额相关的桩；参考仓的观看模式、团队并发编辑、瞬态层三组（原第 503–1440 行）删除；最后一组是新增的桌面端用例。

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { AxiosHeaders, type AxiosError, type InternalAxiosRequestConfig } from 'axios';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CanvasEditorPage, LOCAL_OWNER_TOKEN } from './CanvasEditorPage';
import { resetSidecarSessionForTest, setSidecarSession } from '@/shared/sidecarSession';
import { loadLastProjectId } from '@/desktop/lastProject';

const messageError = vi.fn();
const loadMock = vi.fn();

const getSnapshot = vi.fn();
const getProject = vi.fn();
const updateProject = vi.fn();

// 桌面端页面自己不建 WebSocket（没有观看模式、没有团队实时通道）；构造器一旦被
// 调用，就说明还残留着实时通道代码。
const agentClientCtor = vi.fn();
vi.mock('@/modules/canvas/editor/agent/agentClient', () => ({
  AgentClient: class {
    constructor(opts: unknown) { agentClientCtor(opts); }
    onFrame() {}
    onOpen() {}
    onClose() {}
    connect() {}
    close() {}
  },
}));

const configureCanvasPersistence = vi.fn();
const setCanvasOwnerToken = vi.fn();
const flushCanvasMutations = vi.fn(async () => undefined);
const resetCanvasPersistence = vi.fn();
const seedCanvasBaselines = vi.fn();
const loadLocalView = vi.fn(() => ({ x: 7, y: 8, zoom: 1.2 }));
// 真实 antd 的 App.useApp() 每次调用返回同一个 message API 对象（走 context，
// 引用稳定）。这里必须复刻这个稳定性：页面的快照 effect 把 message 放进依赖数组，
// mock 每次渲染都 new 一个新对象的话，任何无关的 state 更新都会让它重跑。
const messageApi = { error: messageError, success: vi.fn() };

vi.mock('antd', async () => {
  const actual = await vi.importActual<typeof import('antd')>('antd');
  return {
    ...actual,
    App: Object.assign(
      ({ children }: { children: React.ReactNode }) => <>{children}</>,
      { useApp: () => ({ message: messageApi }) },
    ),
  };
});

vi.mock('@/modules/canvas/editor/CanvasEditor', () => ({
  CanvasEditor: ({ projectId }: { projectId: number }) => <div data-testid="canvas-editor">editor-{projectId}</div>,
}));

vi.mock('@/modules/canvas/editor/Sidebar', () => ({
  Sidebar: () => <div>sidebar</div>,
}));

vi.mock('@/modules/canvas/editor/Lightbox', () => ({
  Lightbox: () => null,
}));

vi.mock('@/modules/canvas/editor/Minimap', () => ({
  // 用可见标记代替 null，这样能断言故事板模式下它被门控隐藏了
  // （回归②：Minimap 曾漏了 view === 'canvas' 门控，浮在故事板内容之上）。
  Minimap: () => <div data-testid="minimap" />,
}));

vi.mock('@/modules/canvas/editor/store', () => {
  // nodes:[] 与真实 store 的初始值一致（永远不是 undefined）。
  const state = () => ({ load: loadMock, nodes: [], view: { x: 0, y: 0, zoom: 1 } });
  return {
    useCanvasStore: Object.assign(
      (selector: (s: ReturnType<typeof state>) => unknown) => selector(state()),
      { getState: () => state() },
    ),
  };
});

vi.mock('@/modules/canvas/editor/persistence/canvasMutations', () => ({
  configureCanvasPersistence: (...args: Parameters<typeof configureCanvasPersistence>) => configureCanvasPersistence(...args),
  setCanvasOwnerToken: (...args: Parameters<typeof setCanvasOwnerToken>) => setCanvasOwnerToken(...args),
  flushCanvasMutations: (...args: Parameters<typeof flushCanvasMutations>) => flushCanvasMutations(...args),
  resetCanvasPersistence: (...args: Parameters<typeof resetCanvasPersistence>) => resetCanvasPersistence(...args),
  seedCanvasBaselines: (...args: Parameters<typeof seedCanvasBaselines>) => seedCanvasBaselines(...args),
  persistNodeById: vi.fn(),
}));

vi.mock('@/modules/canvas/editor/persistence/localView', () => ({
  loadLocalView: (...args: Parameters<typeof loadLocalView>) => loadLocalView(...args),
}));

vi.mock('@/modules/canvas/panels/HistoryPanel', () => ({
  // 同 Minimap：用可见标记代替 null，好断言抽屉面板容器随 view 门控。
  HistoryPanel: () => <div data-testid="history-panel" />,
}));

vi.mock('@/modules/canvas/editor/agent/AgentPanel', () => ({
  // 页面级测试只关心「面板开没开、页面把宽度和 token 传下来没有」——sentinel 把
  // 这几个 props 暴露出来即可，真实面板的 WS/消息渲染与此无关。
  AgentPanel: ({ open, width, jwt }: { open: boolean; width: number; jwt: string }) =>
    open ? <div data-testid="agent-panel" data-width={width} data-jwt={jwt} /> : null,
  loadAgentPanelWidth: () => 480,
  saveAgentPanelWidth: () => {},
}));

// 落点固定在原点，本文件不测排布。claimAgentSlot 必须一起 mock：
// 漏一个导出，真跑到落点分支时是 "not a function" 而不是位置断言失败。
vi.mock('@/modules/canvas/editor/agent/dropPoint', () => ({
  agentDropPointFor: () => ({ x: 0, y: 0 }),
  claimAgentSlot: () => 0,
}));

vi.mock('@/modules/canvas/panels/LibraryPanel', () => ({
  LibraryPanel: () => null,
}));

vi.mock('@/modules/canvas/storyboard/StoryboardView', () => ({
  // 真实 StoryboardView 会拉 store.nodes 分组渲染——这里只关心它挂载/卸载
  // 是否跟着 view 走，用 sentinel 代替，避免把虚拟列表等无关实现细节
  // 拖进这个页面级测试。
  StoryboardView: () => <div data-testid="storyboard-view" />,
}));

vi.mock('@/shared/api', async () => {
  const actual = await vi.importActual<typeof import('@/shared/api')>('@/shared/api');
  return {
    ...actual,
    getProject: (...args: Parameters<typeof getProject>) => getProject(...args),
    updateProject: (...args: Parameters<typeof updateProject>) => updateProject(...args),
    getSnapshot: (...args: Parameters<typeof getSnapshot>) => getSnapshot(...args),
    // 模型目录与本文件无关；不 mock 的话会真发 XHR，jsdom 报一串连接错误噪声。
    listRuntimeModels: vi.fn(async () => []),
  };
});

function makeAxiosError(status: number): AxiosError {
  return {
    isAxiosError: true,
    name: 'AxiosError',
    message: `Request failed with status code ${status}`,
    toJSON: () => ({}),
    config: { headers: new AxiosHeaders() } as InternalAxiosRequestConfig,
    response: {
      status,
      statusText: status === 404 ? 'Not Found' : 'Forbidden',
      headers: {},
      config: { headers: new AxiosHeaders() } as InternalAxiosRequestConfig,
      data: {},
    },
  } as AxiosError;
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
}

function renderPage(queryClient = createTestQueryClient()) {

  const result = render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <MemoryRouter initialEntries={['/canvas/14']}>
          <Routes>
            <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
            <Route path="/canvas" element={<div>canvas-projects-page</div>} />
          </Routes>
        </MemoryRouter>
      </AntApp>
    </QueryClientProvider>,
  );
  return { ...result, queryClient };
}

describe('CanvasEditorPage access guard', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    loadMock.mockReset();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    resetCanvasPersistence.mockClear();
    loadLocalView.mockClear();
  });

  it('不抢锁：直接用固定的本地 ownerToken 配好持久化，并用本地 view 替代服务端 view 加载画布', async () => {
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 999, y: 999, zoom: 2 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });

    renderPage();

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(configureCanvasPersistence).toHaveBeenCalledWith(expect.objectContaining({ projectID: 14 }));
    expect(setCanvasOwnerToken).toHaveBeenCalledWith(LOCAL_OWNER_TOKEN);
    // 先 configure 再设 token——反过来会被 configure 里的兜底 reset 抹掉。
    expect(configureCanvasPersistence.mock.invocationCallOrder[0]).toBeLessThan(
      setCanvasOwnerToken.mock.invocationCallOrder[0],
    );
    expect(loadMock).toHaveBeenCalledWith({ nodes: [], connections: [], view: { x: 7, y: 8, zoom: 1.2 } });
  });

  it('snapshot 返回 404 时不渲染编辑器并跳回 /canvas', async () => {
    getSnapshot.mockRejectedValue(makeAxiosError(404));

    renderPage();

    await waitFor(() => {
      expect(screen.getByText('canvas-projects-page')).toBeInTheDocument();
    });

    expect(screen.queryByTestId('canvas-editor')).not.toBeInTheDocument();
    expect(messageError).toHaveBeenCalledWith('该画布不存在或你无权访问');
  });

  it('保存成功更新 snapshot 缓存时不重新 load 当前画布', async () => {
    getSnapshot.mockResolvedValue({
      payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200 }], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    const { queryClient } = renderPage();

    await waitFor(() => expect(loadMock).toHaveBeenCalledTimes(1));

    queryClient.setQueryData(['snapshot', 14], {
      payload: { nodes: [{ id: 'n1', type: 'gen-image', x: 0, y: 0, width: 240, height: 200, settings: { prompt: 'new' } }], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 2,
      syncedAt: '2026-07-15T00:01:00.000Z',
    });

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(loadMock).toHaveBeenCalledTimes(1);
  });
});

describe('CanvasEditorPage 顶栏项目名就地改名', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project', description: '原描述' });
    updateProject.mockReset();
    updateProject.mockResolvedValue({ id: 14, name: '新名字', description: '原描述' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
  });

  async function startEditing() {
    renderPage();
    await waitFor(() => expect(screen.getByText('Secret Project')).toBeInTheDocument());
    fireEvent.click(screen.getByText('Secret Project'));
    return screen.getByLabelText('项目名称') as HTMLInputElement;
  }

  it('点击项目名 → 输入新名 → 回车提交，请求体带回原 description（PUT 整行覆盖，不带会清空描述）', async () => {
    const input = await startEditing();
    expect(input.value).toBe('Secret Project');

    fireEvent.change(input, { target: { value: '  新名字  ' } });
    fireEvent.blur(input);

    // 提交的是 trim 后的名字 + 原 description
    await waitFor(() =>
      expect(updateProject).toHaveBeenCalledWith(14, { name: '新名字', description: '原描述' }),
    );
    // 输入框退场，回到展示态
    expect(screen.queryByLabelText('项目名称')).not.toBeInTheDocument();
  });

  it('清空名字提交：不发请求，还原旧名', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });

  it('名字没改动就失焦：不发请求', async () => {
    const input = await startEditing();

    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });

  // 点画布不会触发 blur：CanvasEditor 在 mousedown 里 preventDefault（平移/框选
  // 起手式），把浏览器「点别处 → 原输入框失焦」的默认行为掐掉了。所以这条不许
  // 用 fireEvent.blur 模拟——直接对 document 派发 pointerdown，走的是编辑态挂在
  // capture 阶段的收口监听，这才是「点画布保存」的真实路径。
  it('点画布（只有 pointerdown、没有 blur）也要提交', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '点画布保存' } });
    fireEvent.pointerDown(document.body);

    await waitFor(() =>
      expect(updateProject).toHaveBeenCalledWith(14, { name: '点画布保存', description: '原描述' }),
    );
    expect(screen.queryByLabelText('项目名称')).not.toBeInTheDocument();
  });

  it('pointerdown 提交后浏览器补发 blur：同一次编辑只发一次请求', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '只提交一次' } });
    // 点顶栏其他按钮时两条收口路径都会走到：先 document pointerdown，
    // 浏览器随后把焦点挪走触发 blur。
    fireEvent.pointerDown(document.body);
    fireEvent.blur(input);

    await waitFor(() => expect(updateProject).toHaveBeenCalledTimes(1));
    expect(updateProject).toHaveBeenCalledWith(14, { name: '只提交一次', description: '原描述' });
  });

  it('Esc 放弃编辑：即使已输入新名也不提交', async () => {
    const input = await startEditing();

    fireEvent.change(input, { target: { value: '改了但反悔' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    fireEvent.blur(input);

    expect(updateProject).not.toHaveBeenCalled();
    expect(screen.getByText('Secret Project')).toBeInTheDocument();
  });
});

describe('CanvasEditorPage 视图切换（工作流 / 故事板）', () => {
  beforeEach(() => {
    messageError.mockReset();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [], view: { x: 0, y: 0, zoom: 1 } },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    configureCanvasPersistence.mockClear();
    setCanvasOwnerToken.mockClear();
    resetCanvasPersistence.mockClear();
    loadLocalView.mockClear();
  });

  // 视图偏好记在 localStorage（全局一个键，所有画布共用）。这条测的是真正的
  // 验收点：**下次进来直接渲染成上次选的那个**——只测 load/save 存取不够，
  // 那证明不了页面初始化真的用上了它（比如误写成 useEffect 事后切）。
  it('上次选了故事板：再进画布直接渲染故事板，不先闪一帧画布', async () => {
    window.localStorage.setItem('tvmao:canvas:viewMode', 'storyboard');
    renderPage();

    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    // 首次渲染就是故事板：画布的工具面板一次都没出现过
    expect(screen.getByTestId('storyboard-view')).toBeInTheDocument();
    expect(screen.queryByText('sidebar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();

    window.localStorage.clear();
  });

  it('切换视图会写回 localStorage', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));
    expect(window.localStorage.getItem('tvmao:canvas:viewMode')).toBe('storyboard');

    fireEvent.click(screen.getByRole('button', { name: '工作流' }));
    expect(window.localStorage.getItem('tvmao:canvas:viewMode')).toBe('canvas');

    window.localStorage.clear();
  });

  it('切到故事板：覆盖层挂 data-storyboard-open，画布工具面板（Sidebar/抽屉容器/Minimap）隐藏；切回画布：全部恢复', async () => {
    const { container } = renderPage();

    // 先等画布就绪（owner 接管完成），默认是画布视图。
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('minimap')).toBeInTheDocument();
    expect(screen.getByTestId('history-panel')).toBeInTheDocument();
    expect(screen.getByTestId('shortcuts-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('storyboard-view')).not.toBeInTheDocument();
    expect(container.querySelector('[data-storyboard-open]')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));

    // 故事板出现，画布的工具面板全部退场。
    expect(screen.getByTestId('storyboard-view')).toBeInTheDocument();
    expect(screen.queryByText('sidebar')).not.toBeInTheDocument();
    expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();
    expect(screen.queryByTestId('history-panel')).not.toBeInTheDocument();
    // 回归③：ShortcutsHint 是 fixed + z-40，比故事板覆盖层的 z-30 还高，
    // 漏了门控就会浮在故事板内容之上，展示已被停用的画布快捷键。
    expect(screen.queryByTestId('shortcuts-strip')).not.toBeInTheDocument();
    // 覆盖层必须带这个标记——CanvasEditor 的全局键盘监听靠它停用，
    // 否则被盖住的画布节点会被 Delete/Cmd+Z 静默改掉（回归①）。
    expect(container.querySelector('[data-storyboard-open]')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '工作流' }));

    // 切回画布：画布本身全程没卸载（canvas-editor 一直在），工具面板恢复。
    expect(screen.getByTestId('canvas-editor')).toBeInTheDocument();
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('minimap')).toBeInTheDocument();
    expect(screen.getByTestId('history-panel')).toBeInTheDocument();
    expect(screen.getByTestId('shortcuts-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('storyboard-view')).not.toBeInTheDocument();
    expect(container.querySelector('[data-storyboard-open]')).not.toBeInTheDocument();
  });

  it('切到故事板自动打开 Agent 面板，覆盖层按面板宽度向左让位', async () => {
    const { container } = renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    // 画布视图下不会自动开。
    expect(screen.queryByTestId('agent-panel')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '故事板' }));

    // 面板自动打开，页面把宽度真源传给了它。
    const panel = screen.getByTestId('agent-panel');
    expect(panel.getAttribute('data-width')).toBe('480');
    // 覆盖层用 paddingRight 给面板让位——故事板内容被挤压，不是被盖住。
    const overlay = container.querySelector('[data-storyboard-open]') as HTMLElement;
    expect(overlay.style.paddingRight).toBe('480px');

    // 手动关掉后不会被强制重开（自动打开只在切入瞬间触发一次）。
    // 这里没渲染关闭按钮，直接走「切出去再切回来会再开」的对偶断言。
    fireEvent.click(screen.getByRole('button', { name: '工作流' }));
    fireEvent.click(screen.getByRole('button', { name: '故事板' }));
    expect(screen.getByTestId('agent-panel')).toBeInTheDocument();

    // 视图偏好是**全局**键，不清的话本文件后面所有用例都会在故事板模式下跑。
    window.localStorage.clear();
  });
});

describe('CanvasEditorPage 桌面端（spec F9、X1–X4）', () => {
  beforeEach(() => {
    messageError.mockReset();
    localStorage.clear();
    getProject.mockResolvedValue({ id: 14, name: 'Secret Project' });
    getSnapshot.mockReset();
    getSnapshot.mockResolvedValue({
      payload: { nodes: [], connections: [] },
      version: 1,
      syncedAt: '2026-07-15T00:00:00.000Z',
    });
    loadMock.mockReset();
    flushCanvasMutations.mockClear();
    configureCanvasPersistence.mockClear();
    agentClientCtor.mockClear();
  });
  afterEach(() => {
    resetSidecarSessionForTest();
    localStorage.clear();
  });

  it('「所有项目」先 flush 未提交的编辑，再进项目列表页', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: '所有项目' }));

    await waitFor(() => expect(screen.getByText('canvas-projects-page')).toBeInTheDocument());
    expect(flushCanvasMutations).toHaveBeenCalledTimes(1);
  });

  it('画布载入成功后记为「上一次打开的项目」', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    await waitFor(() => expect(loadLastProjectId()).toBe(14));
  });

  it('快照 404（项目已删除）时不记为上一次打开的项目', async () => {
    getSnapshot.mockRejectedValue(makeAxiosError(404));
    renderPage();
    await waitFor(() => expect(screen.getByText('canvas-projects-page')).toBeInTheDocument());
    expect(loadLastProjectId()).toBeNull();
  });

  it('顶栏没有积分、没有发布/分享入口、没有回首页入口', async () => {
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());

    expect(screen.queryByText(/积分/)).not.toBeInTheDocument();
    expect(screen.queryByTitle('发布与分享')).not.toBeInTheDocument();
    expect(screen.queryByTitle('回到首页')).not.toBeInTheDocument();
    expect(screen.queryByText('返回')).not.toBeInTheDocument();
  });

  it('页面本身不建 WebSocket；Agent 面板拿到的是本次启动的 sidecar token', async () => {
    setSidecarSession({ port: 50001, token: 'sidecar-tok' });
    renderPage();
    await waitFor(() => expect(screen.getByTestId('canvas-editor')).toBeInTheDocument());
    expect(agentClientCtor).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: /Agent/ }));
    expect(screen.getByTestId('agent-panel').getAttribute('data-jwt')).toBe('sidecar-tok');
  });

  it('局部 mutation 保存出错时提示「保存失败」', async () => {
    renderPage();
    await waitFor(() => expect(configureCanvasPersistence).toHaveBeenCalled());
    const { onError } = configureCanvasPersistence.mock.calls[0][0] as { onError: (err: unknown) => void };
    onError(new Error('boom'));
    expect(messageError).toHaveBeenCalledWith('保存失败');
  });
});
```

- [ ] **Step 2: 运行，确认失败**

Run: `pnpm --dir web exec vitest run src/modules/canvas/CanvasEditorPage.test.tsx`
Expected: FAIL。旧页面没有导出 `LOCAL_OWNER_TOKEN`，没有"所有项目"按钮，还会调用测试里已经不再 mock 的 `claimCanvasOwner` 和 `getAuthContexts`。

- [ ] **Step 3: 整体替换 `CanvasEditorPage.tsx`**

下面是参考仓文件按 F-4 裁剪后的完整内容。除标了"桌面端"的注释和代码外，其余行和注释都逐字来自参考仓。

```tsx
import axios, { type AxiosError } from 'axios';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { App as AntApp, Tooltip } from 'antd';
import { BotMessageSquare, LayoutGrid, LayoutPanelLeft, Save, Waypoints } from 'lucide-react';
import { BrandLogo } from '@/shared/BrandLogo';
import { CanvasEditor } from '@/modules/canvas/editor/CanvasEditor';
import { StoryboardView } from '@/modules/canvas/storyboard/StoryboardView';
import { Sidebar } from '@/modules/canvas/editor/Sidebar';
import { Lightbox } from '@/modules/canvas/editor/Lightbox';
import { Minimap } from '@/modules/canvas/editor/Minimap';
import { ShortcutsHint } from '@/modules/canvas/editor/ShortcutsHint';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  persistNodeById,
  resetCanvasPersistence,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from '@/modules/canvas/editor/persistence/canvasMutations';
import { loadLocalView } from '@/modules/canvas/editor/persistence/localView';
import {
  type CanvasViewMode,
  loadCanvasViewMode,
  saveCanvasViewMode,
} from '@/modules/canvas/editor/persistence/localCanvasView';
import {
  type Snapshot,
  getProject,
  getSnapshot,
  listRuntimeModels,
  updateProject,
} from '@/shared/api';
import { sidecarToken } from '@/shared/sidecarSession';
import { saveLastProjectId } from '@/desktop/lastProject';
import type { CanvasSnapshot } from '@/modules/canvas/editor/types';
import { HistoryPanel } from '@/modules/canvas/panels/HistoryPanel';
import { AgentPanel, loadAgentPanelWidth, saveAgentPanelWidth } from '@/modules/canvas/editor/agent/AgentPanel';
import { useAgentHandoff } from '@/modules/canvas/editor/agent/agentHandoff';
import { agentDropPointFor, claimAgentSlot } from '@/modules/canvas/editor/agent/dropPoint';
import { applyCanvasOpArtifact, isCanvasOpArtifact } from '@/modules/canvas/editor/agent/canvasOps';
import { LibraryPanel } from '@/modules/canvas/panels/LibraryPanel';
import '../../styles/canvas.css';

// 桌面端单用户、单窗口（spec X2、S11）：没有画布编辑锁，也就没有 claim / 心跳 /
// 观看模式。但 canvasMutations 仍要求一个非空 ownerToken 才会发结构性 mutation
// （空 token 时整批静默丢弃），所以给一个固定值。sidecar 的 owner 校验恒通过，
// 只把它当普通字段收下。
export const LOCAL_OWNER_TOKEN = 'local';

export function CanvasEditorPage() {
  const navigate = useNavigate();
  const { projectId } = useParams<{ projectId: string }>();
  const pid = Number(projectId);
  const load = useCanvasStore((s) => s.load);
  // 故事板是盖在画布上的覆盖层，不是路由：切过去时 CanvasEditor 不卸载，
  // 视口、选中态、正在跑的生成任务全部原样活着，切回来还在原地。
  //
  // 初值从 localStorage 惰性读取（全局一个值，所有画布共用）。这里必须是
  // useState 的初始化函数而不是 useEffect——事后 setView 会先渲染一帧画布
  // 再跳到故事板，用户看到闪一下，那一帧的画布还真挂载了一次。
  const [view, setViewState] = useState<CanvasViewMode>(loadCanvasViewMode);
  const setView = useCallback((next: CanvasViewMode) => {
    setViewState(next);
    saveCanvasViewMode(next);
  }, []);
  const [ready, setReady] = useState(false);
  const [lastSyncedAt, setLastSyncedAt] = useState<string | null>(null);
  const [activeTool, setActiveTool] = useState<'select' | 'history'>('select');
  const [historyOpen, setHistoryOpen] = useState(false);
  // 首页「故事灵感」入口带着交接单进来时，面板必须随页面一起挂载并滑入——
  // 交接单的消费方是 AgentPanel（见 agentHandoff.ts），面板不挂载它就永远发不
  // 出去。用惰性初始值而不是 effect：effect 会先渲染一帧关闭态再打开，面板
  // 闪一下才滑入。这里只 peek 不 take，取走的动作留给 AgentPanel。
  const [chatOpen, setChatOpen] = useState(() => useAgentHandoff.getState().peek(pid) !== null);
  // AgentPanel 首次打开前不挂载：挂载即建 WS、拉 threads 并渲染首个会话的
  // 全部历史消息（消息里的图片会整批下载）。用户没打开过对话就不该发生这些。
  // 打开过一次后保持挂载——保留 WS 连接和进行中的 agent 运行状态，关闭面板
  // 只是滑出屏幕。
  const [agentEverOpened, setAgentEverOpened] = useState(false);
  useEffect(() => {
    if (chatOpen) setAgentEverOpened(true);
  }, [chatOpen]);
  // Agent 面板宽度的真源在这里而不是 AgentPanel 内部：故事板覆盖层要按同一个
  // 宽度让位（挤压布局），拖拽调宽时两者必须同帧更新，不能各存一份。
  const [agentPanelWidth, setAgentPanelWidth] = useState<number>(loadAgentPanelWidth);
  useEffect(() => {
    saveAgentPanelWidth(agentPanelWidth);
  }, [agentPanelWidth]);
  // 进入故事板模式自动打开 Agent 面板（产品语义：故事板 = 对话驱动的视图）。
  // 只在切入瞬间触发——用户在故事板里手动关掉后不会被强制重开，下次切入再开。
  // useLayoutEffect 而非 useEffect：覆盖层的 paddingRight 由 chatOpen 驱动，
  // paint 后才 set 的话会先闪一帧全宽故事板再收窄。
  useLayoutEffect(() => {
    if (view === 'storyboard') setChatOpen(true);
  }, [view]);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const accessDeniedHandledRef = useRef(false);
  const loadedProjectRef = useRef<number | null>(null);
  // 按 action 维度的落点账本，记录同一 action 已占用的网格格子号；让一次
  // action 产生的多个节点按序号排进 5 列网格，对齐且互不重叠。
  // 模型目录：agent 切模型时要按新模型的 uiSchema 重置比例/分辨率/时长，与
  // 用户在节点工具条上手动切模型走同一条路（canvasOps 的 withDynamicFieldReset
  // 复用 UI 那个 modelSettingDefaults）。queryKey 与 GenImage/GenVideo 节点相同，
  // react-query 缓存共享——这里不会多发一次请求。
  const runtimeModelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });
  const resolveUiSchema = useCallback(
    (modelId: string) => runtimeModelsQuery.data?.find((m) => m.modelId === modelId)?.uiSchema ?? undefined,
    [runtimeModelsQuery.data],
  );

  const agentDropSlotsRef = useRef<Record<string, Set<number>>>({});
  const slotsForAction = useCallback((actionId: string) => {
    const existing = agentDropSlotsRef.current[actionId];
    if (existing) return existing;
    const fresh = new Set<number>();
    agentDropSlotsRef.current[actionId] = fresh;
    return fresh;
  }, []);

  // 把 AgentPanel 的产出物作为预览节点拉到画布上，锚定在视口中心
  // 附近（往左偏一点，避开 420px 宽的抽屉）。
  const onAgentArtifact = useCallback(
    ({
      thread_id,
      action_id,
      artifact,
    }: {
      thread_id: string;
      action_id: string;
      artifact: { artifact_type?: string; artifact_id: string; url?: string; name?: string; width?: number; height?: number; prompt?: string; text?: string; payload?: unknown };
    }) => {
      // 画布操作五件套（增/改/删节点、连/断连线，后端 tools_canvas_ops.go）
      // 不走下面「落新产物节点 + 排布」的逻辑——它们操作的是画布现有结构，
      // 由 canvasOps 执行器调 store 现有动作完成（undo/持久化/连线清理全复用），
      // 不改 status、不触发任何生成。
      if (isCanvasOpArtifact(artifact.artifact_type)) {
        const state = useCanvasStore.getState();
        applyCanvasOpArtifact({
          store: state,
          artifact,
          agentMeta: {
            threadId: thread_id,
            actionId: action_id,
            artifactId: artifact.artifact_id,
            agentName: 'planner',
          },
          // 未带坐标的 create 与其他 agent 产物共享同一个 action 内的网格账本，
          // 按名字里的序号排位；惰性调用——带显式坐标的操作不消耗格子。
          nextDropPoint: (name) => {
            const slot = claimAgentSlot(slotsForAction(action_id), name);
            const anchorX = (window.innerWidth / 2 - state.view.x - 210) / state.view.zoom;
            const anchorY = (window.innerHeight / 2 - state.view.y) / state.view.zoom;
            return agentDropPointFor({ anchorX, anchorY }, slot);
          },
          persistNode: persistNodeById,
          resolveUiSchema,
        });
        return;
      }

      // text_info（create_text_node 工具产出的剧本/文案）没有对象存储 URL，
      // 正文随帧携带——必须在 url 守卫之前分流，否则整类产物被静默丢弃。
      const isTextInfo = artifact.artifact_type === 'text_info';
      // gen_*_node_info（create_gen_node 工具）同理无 URL：铺生成节点预填提示词，
      // 不执行生成、不扣积分（铁律：生成必须用户逐节点手动点）。
      const genNodeType =
        artifact.artifact_type === 'gen_image_node_info'
          ? 'gen-image'
          : artifact.artifact_type === 'gen_video_node_info'
            ? 'gen-video'
            : null;
      if (genNodeType ? !artifact.prompt : isTextInfo ? !artifact.text : !artifact.url) return;
      const state = useCanvasStore.getState();
      // 落点按 artifact.name 里的序号排进网格——agent 的产物是并发生成、乱序
      // 到达的（第13段可能先于第1段回来），只靠到达序排会得到一片乱码顺序。
      const slot = claimAgentSlot(slotsForAction(action_id), artifact.name);

      const cw = window.innerWidth;
      const ch = window.innerHeight;
      const anchorX = (cw / 2 - state.view.x - 210) / state.view.zoom;
      const anchorY = (ch / 2 - state.view.y) / state.view.zoom;
      const { x, y } = agentDropPointFor({ anchorX, anchorY }, slot);

      const agentMeta = {
        threadId: thread_id,
        actionId: action_id,
        artifactId: artifact.artifact_id,
        agentName: 'planner',
      };

      if (genNodeType) {
        // 只建节点 + 预填提示词，状态留初始值（不置 succeeded——节点还没产出）。
        // 字段名以浏览器为准：gen-image 存 settings.prompt，gen-video 存
        // settings.videoPrompt（GenVideoNode 读写的就是它，CLI 曾在这栽过跟头）。
        const id = state.addNode(genNodeType, { x, y });
        state.updateNodeSettings(id, {
          ...(genNodeType === 'gen-video'
            ? { videoPrompt: artifact.prompt }
            : { prompt: artifact.prompt }),
          ...(artifact.name ? { name: artifact.name } : {}),
          agentMeta,
        });
        persistNodeById(id);
        return;
      }

      if (isTextInfo) {
        // 落成现有文字节点（TextNode 读写 settings.text，组件零改动）。
        // 尺寸给比默认（280×200）宽高一档——剧本/文案通常是长文，默认框
        // 只能看到开头几行。
        const id = state.addNode('text-node', { x, y });
        state.updateNode(id, { status: 'succeeded', width: 320, height: 360 });
        state.updateNodeSettings(id, {
          text: artifact.text,
          name: artifact.name,
          agentMeta,
        });
        // 与下面 preview 路径同一条持久化路：addNode 只 upsert 了空壳，补一次完整节点。
        persistNodeById(id);
        return;
      }

      const w = 320;
      const h = artifact.width && artifact.height
        ? Math.round((artifact.height / artifact.width) * w)
        : 320;

      const id = state.addNode('preview', { x, y });
      state.updateNode(id, { content: artifact.url, status: 'succeeded', width: w, height: h });
      state.updateNodeSettings(id, {
        // 【M4】必须按 artifact_type 写 settings.kind：签名 URL 通常没有扩展名，
        // PreviewNode（渲染/灯箱）和 canvasSummary（@ 候选归类）判媒体类型只能
        // 靠它，不写的话视频/音频产物整条链都被当图片处理（<img> 渲染直接破图）。
        kind:
          artifact.artifact_type === 'video_info'
            ? 'video'
            : artifact.artifact_type === 'audio_info'
              ? 'audio'
              : 'image',
        agentMeta,
      });
      // agent 产出物落到画布是前端建节点，addNode 只 upsert 了空壳，补一次完整节点。
      persistNodeById(id);
    },
    [],
  );

  const projectQuery = useQuery({
    queryKey: ['project', pid],
    queryFn: () => getProject(pid),
    enabled: Number.isFinite(pid) && pid > 0,
  });

  const snapshotQuery = useQuery<Snapshot, AxiosError>({
    queryKey: ['snapshot', pid],
    queryFn: () => getSnapshot(pid),
    enabled: Number.isFinite(pid) && pid > 0,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: (failureCount, error) => {
      const status = error.response?.status;
      if (status === 403 || status === 404) return false;
      return failureCount < 1;
    },
  });

  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();

  // 顶栏项目名就地改名：null = 展示态，字符串 = 编辑中的草稿。
  // Esc 走「先标记再 blur」而不是直接卸载 input——统一从 onBlur 单出口收尾，
  // 避免「卸载是否补发 focusout」这种浏览器差异造成放弃/提交二义。
  const [nameDraft, setNameDraft] = useState<string | null>(null);
  const renameEscapedRef = useRef(false);
  const nameInputRef = useRef<HTMLInputElement | null>(null);
  // 一次编辑只允许收口一次：点画布提交走 document pointerdown，点普通控件
  // 提交走 onBlur，两条路可能对同一次编辑先后各触发一次（pointerdown 之后
  // 浏览器紧接着把焦点挪走），不加闩会重复发一次改名请求。
  const renameClosedRef = useRef(false);

  // 提交改名。空名（含纯空格）或没改动 → 静默还原旧名，不发请求。
  // PUT /projects/{id} 是整行覆盖（name+description 一起写），必须回传原
  // description，否则就地改名会顺手把列表页里填的项目描述清空。
  const commitRename = useCallback(
    (raw: string) => {
      if (renameClosedRef.current) return;
      renameClosedRef.current = true;
      setNameDraft(null);
      const project = projectQuery.data;
      const next = raw.trim();
      if (!project || !next || next === project.name) return;
      updateProject(project.id, { name: next, description: project.description ?? '' })
        .then(() => queryClient.invalidateQueries({ queryKey: ['project', pid] }))
        .catch(() => message.error('重命名失败'));
    },
    [projectQuery.data, queryClient, pid, message],
  );

  // 点到输入框外面 = 提交。不能只靠 onBlur：画布为了平移/框选在 mousedown 里
  // preventDefault（CanvasEditor.tsx），把浏览器「点别处 → 原输入框失焦」这个
  // 默认行为一并掐掉了，点画布时 blur 根本不会发生。preventDefault 吞不掉事件
  // 本身，capture 阶段的 pointerdown 一定先于画布拿到，在这里主动收口。
  const renameEditing = nameDraft !== null;
  useEffect(() => {
    if (!renameEditing) return;
    const onPointerDownOutside = (e: PointerEvent) => {
      const input = nameInputRef.current;
      if (!input || input.contains(e.target as Node)) return;
      commitRename(input.value);
    };
    document.addEventListener('pointerdown', onPointerDownOutside, true);
    return () => document.removeEventListener('pointerdown', onPointerDownOutside, true);
  }, [renameEditing, commitRename]);
  const snapshotStatus = axios.isAxiosError(snapshotQuery.error)
    ? snapshotQuery.error.response?.status
    : undefined;

  useEffect(() => {
    if (snapshotStatus !== 403 && snapshotStatus !== 404) {
      accessDeniedHandledRef.current = false;
      return;
    }
    if (accessDeniedHandledRef.current) return;
    accessDeniedHandledRef.current = true;
    message.error('该画布不存在或你无权访问');
    navigate('/canvas', { replace: true });
  }, [message, navigate, snapshotStatus]);

  useEffect(() => {
    if (!snapshotQuery.data) return;
    setLastSyncedAt(snapshotQuery.data.syncedAt ?? null);

    // snapshotQuery.data 在保存后不再被写回 query-cache（新模型下局部 mutation
    // 不 setQueryData），所以正常编辑期间这个 effect 不会重跑。仍保留守卫：
    // 只有首次进入、切换项目，或失权刷新显式 setReady(false) 后的 refetch，才
    // 重新初始化画布 store；否则会清空 selectedIds/undoStack，把正在输入的
    // AI绘图/AI视频 prompt 面板隐藏掉。
    if (ready && loadedProjectRef.current === pid) {
      return;
    }

    const raw = snapshotQuery.data.payload as Partial<CanvasSnapshot> | null | undefined;
    const nodes = Array.isArray(raw?.nodes) ? raw!.nodes : [];
    // view（pan/zoom）不再是服务端内容，改用当前浏览器按项目存的 localStorage。
    // 换电脑只丢视角，画布数据（节点/连线/内容）仍从服务端 JSON 加载。
    load({
      nodes,
      connections: Array.isArray(raw?.connections) ? raw!.connections : [],
      view: loadLocalView(pid),
    });
    // 播种字段级 diff 的基线。**只能喂服务端快照**（这里正是），本地导入的 JSON
    // 不能播——那些节点服务端没有，有了基线就会对不存在的节点发 PatchNode。
    // 不分个人/团队一起播：基线是 persistNodeById 的 diff 依据，与身份无关；
    // 不播只是退化成「每次发整节点 upsert」，白丢字段级 LWW。
    seedCanvasBaselines(nodes);
    // 配置局部 mutation 持久化（版本回写只用来刷新「已保存」时间戳），并且必须在
    // setReady(true) 之前做完：画布一露出来用户就能改，这时持久化还没配好的话，
    // 结构性 op 会因为没有 ownerToken 被静默丢弃。
    // 顺序要紧：先 configure 再设 token。configureCanvasPersistence 在换项目时会兜底
    // resetCanvasPersistence()，而那会把 ownerToken 清空——先设 token 就等于交给
    // reset 抹掉。
    configureCanvasPersistence({
      projectID: pid,
      onVersion: () => setLastSyncedAt(new Date().toISOString()),
      onError: () => message.error('保存失败'),
    });
    setCanvasOwnerToken(LOCAL_OWNER_TOKEN);
    loadedProjectRef.current = pid;
    setReady(true);
  }, [snapshotQuery.data, load, pid, ready, message]);

  // 记住「上一次打开的项目」，下次启动直接进它（spec F9）。只在快照真的载入
  // 之后记：打不开的项目（已删除 / 不存在）不该被当成上一次。
  useEffect(() => {
    if (ready) saveLastProjectId(pid);
  }, [ready, pid]);

  // 离开画布时清干净持久化运行态（owner-token、心跳队列、debounce 计时器、
  // 事件监听）。切项目由 configureCanvasPersistence 内部处理，这里兜卸载。
  useEffect(() => () => resetCanvasPersistence(), []);

  // Ctrl/Cmd + S 不再触发保存（画布即时落库，没有「保存」这个动作了），但仍然
  // 拦掉它：不拦的话浏览器会弹出「另存为网页」对话框——在一个网页应用里按 Cmd+S
  // 蹦出保存文件的窗口，比什么都不做更让人困惑。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // 应用内跳转（如「返回」）。新模型下用户动作已即时落库，不再有「未保存」
  // 概念；只有 1.5s debounce 中的 prompt 可能还没提交，所以离开前先 flush
  // 一次队列再跳转，避免丢掉最后一次输入。
  const requestNavigate = useCallback((to: string) => {
    void flushCanvasMutations().catch(() => undefined).finally(() => navigate(to));
  }, [navigate]);

  if (!Number.isFinite(pid) || pid <= 0) {
    return <div className="p-8 text-white">无效的项目 ID</div>;
  }

  if (snapshotQuery.isPending || snapshotStatus === 401 || snapshotStatus === 403 || snapshotStatus === 404) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  if (snapshotQuery.isError) {
    return <div className="p-8 text-white">加载画布失败，请稍后重试</div>;
  }

  if (!ready) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  return (
    <div className="canvas-root">
      {/* 第 0 层：全宽画布——铺满整个视口，让点状背景延伸到浮动顶栏和
          侧边栏后面（TapNow 的设计）。 */}
      <CanvasEditor projectId={pid} />

      {/* 第 0.5 层：故事板覆盖层。盖住画布和它的工具面板，但让开顶栏（z-40）。
          画布本身不卸载——切换成本接近零。
          data-storyboard-open 是画布快捷键的停用标记（CanvasEditor 的 onKey/
          空格抓手见到它直接返回）——画布被完全盖住但没卸载，selectedIds 仍在
          store 里，不停用的话在故事板视图按 Delete/Cmd+Z 会静默删掉/撤销画布
          上看不见的节点。与 3D 导演台的 data-director-desk-open 同一套机制。 */}
      {view === 'storyboard' && (
        <div
          data-storyboard-open
          // 页面底色比面板(#262626)暗一档：三个分区面板浮在这层暗底上，
          // 面板之间的间隙露出它，那道暗缝就是模块的分隔。两层同色的话
          // 模块之间会糊成一片（试过，只剩一条看不见的 1px 线）。
          // 铺满整个视口(inset-0)而不是从 top-14 开始：浮动顶栏不是满宽实心的
          // (top-3 left-4 right-4)，让开那 56px 的话，顶栏没盖到的地方会直接
          // 透出底下还活着的画布——节点和连线从顶栏两侧漏出来。覆盖层 z-30 <
          // 顶栏 z-40，铺满也不会挡住顶栏交互；故事板内容自己用 pt-14 让位。
          className="absolute inset-0 z-30 bg-[#141414] overflow-hidden"
          // Agent 面板开着时故事板内容向左让位（挤压而非被盖住）。让位用
          // paddingRight 而不是收窄覆盖层本身：背景层保持全宽压住底下的画布，
          // 面板 200ms 滑入动画期间右侧露出的是这层自己的暗底，不是画布节点。
          // 不加 transition——拖拽调宽时 padding 和面板宽度同帧变化才跟手，
          // 挂了过渡反而会在两者之间裂出缝。
          style={{ paddingRight: chatOpen ? agentPanelWidth : 0 }}
        >
          <StoryboardView />
        </div>
      )}

      {/* 第 1 层：抽屉面板，定位在 68px 宽的侧边栏间隙右侧，浮动布局，
          上下留白对称，这样顶栏不会盖住面板标题，下方的视觉间距也和
          上方对齐。故事板模式下它们是画布的工具，不该露出来。 */}
      {view === 'canvas' && (
        <div className="absolute top-14 bottom-14 left-[68px] flex gap-2 z-30">
          <HistoryPanel open={historyOpen} projectId={pid} onClose={() => setHistoryOpen(false)} />
          <LibraryPanel open={libraryOpen} projectId={pid} onClose={() => setLibraryOpen(false)} />
        </div>
      )}

      {/* 第 2 层：浮动的胶囊形侧边栏。同上，故事板模式下隐藏。 */}
      {view === 'canvas' && (
        <Sidebar
          projectId={pid}
          activeTool={activeTool}
          onToolChange={setActiveTool}
          historyOpen={historyOpen}
          onToggleHistory={() => setHistoryOpen((v) => !v)}
          libraryOpen={libraryOpen}
          onToggleLibrary={() => setLibraryOpen((v) => !v)}
        />
      )}

      {/* 第 3 层：浮动顶栏——左侧 logo 组没有背景（TapNow 设计）；右侧组
          是一个毛玻璃胶囊，跟侧边栏的表面风格一致。这里用内联样式来
          绕开 preflight: false。 */}
      <div className="absolute top-3 left-4 right-4 flex items-center gap-3 z-40 pointer-events-none">
        <div className="flex items-center gap-2 pointer-events-auto">
          <div className="flex items-center gap-2">
            <BrandLogo size={28} />
            <div className="text-foreground text-sm font-semibold tracking-tight">
              喵剧AI
            </div>
          </div>
          {/* 「所有项目」：进项目列表页，选中项目后回到画布（spec F9）。离开前先 flush。 */}
          <button
            className="flex items-center gap-1.5 px-2 py-1 text-sidebar-foreground hover:text-foreground text-xs rounded bg-transparent border-none cursor-pointer"
            onClick={() => requestNavigate('/canvas')}
          >
            <LayoutGrid size={14} />
            所有项目
          </button>
          <div className="h-5 w-px bg-accent" />
          {nameDraft !== null ? (
            <input
              ref={nameInputRef}
              aria-label="项目名称"
              autoFocus
              value={nameDraft}
              onFocus={(e) => e.currentTarget.select()}
              onChange={(e) => setNameDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur();
                if (e.key === 'Escape') {
                  renameEscapedRef.current = true;
                  e.currentTarget.blur();
                }
              }}
              onBlur={(e) => {
                if (renameEscapedRef.current) {
                  renameEscapedRef.current = false;
                  renameClosedRef.current = true;
                  setNameDraft(null);
                  return;
                }
                commitRename(e.currentTarget.value);
              }}
              className="text-foreground text-sm bg-transparent rounded px-1 py-0.5 w-[220px] outline-none"
              style={{ borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(255, 255, 255, 0.25)' }}
            />
          ) : (
            <button
              type="button"
              className="text-sidebar-foreground hover:text-foreground text-sm truncate max-w-[280px] bg-transparent border-none cursor-pointer px-0 text-left"
              title={projectQuery.data ? `${projectQuery.data.name}（点击重命名）` : ''}
              onClick={() => {
                // 项目还没加载出来时占位显示「项目 #id」，此时没有可编辑的旧名，不进编辑态。
                if (!projectQuery.data) return;
                renameClosedRef.current = false;
                setNameDraft(projectQuery.data.name);
              }}
            >
              {projectQuery.data?.name ?? `项目 #${pid}`}
            </button>
          )}
          <div className="h-5 w-px bg-accent" />
          {/* 视图切换：只放图标，名字走 hover tooltip。顶栏左侧寸土寸金——
              项目名本来就可能很长，两个按钮各带两个汉字会把它挤到省略号。 */}
          <div className="flex items-center gap-0.5 p-0.5 rounded-lg" style={{ backgroundColor: 'rgba(28, 28, 28, 0.95)' }}>
            <Tooltip title="工作流" placement="bottom">
              <button
                type="button"
                aria-label="工作流"
                aria-pressed={view === 'canvas'}
                onClick={() => setView('canvas')}
                className={`flex items-center justify-center w-7 h-7 rounded-md border-none cursor-pointer ${
                  view === 'canvas' ? 'bg-accent text-foreground' : 'bg-transparent text-sidebar-foreground'
                }`}
              >
                <Waypoints size={15} />
              </button>
            </Tooltip>
            <Tooltip title="故事板" placement="bottom">
              <button
                type="button"
                aria-label="故事板"
                aria-pressed={view === 'storyboard'}
                onClick={() => setView('storyboard')}
                className={`flex items-center justify-center w-7 h-7 rounded-md border-none cursor-pointer ${
                  view === 'storyboard' ? 'bg-accent text-foreground' : 'bg-transparent text-sidebar-foreground'
                }`}
              >
                <LayoutPanelLeft size={15} />
              </button>
            </Tooltip>
          </div>
        </div>

        <div className="flex-1" />

        <div
          className="flex items-center gap-3 px-4 py-2 rounded-xl text-sm text-sidebar-foreground shadow-xl shadow-black/40 pointer-events-auto"
          style={{
            backgroundColor: 'rgba(28, 28, 28, 0.95)',
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <span className="flex items-center gap-1">
            <Save size={14} />
            {lastSyncedAt
              ? `已保存 ${new Date(lastSyncedAt).toLocaleTimeString()}`
              : '尚未保存'}
          </span>
        </div>

        {/* Agent 入口——对标上游 Tapnow：顶栏最右侧的独立按钮（原来是左侧
            dock 里的「AI 对话」图标）。顶栏在故事板视图也在，但 AgentPanel
            自己是 fixed z-[900]，盖得过故事板覆盖层（z-30），两个视图都能用。 */}
        <button
          type="button"
          onClick={() => setChatOpen((v) => !v)}
          className={`flex items-center gap-1.5 px-4 py-2 rounded-xl text-sm font-semibold text-foreground cursor-pointer shadow-xl shadow-black/40 pointer-events-auto transition-colors ${
            chatOpen ? 'bg-accent' : 'bg-[rgba(28,28,28,0.95)] hover:bg-accent'
          }`}
          style={{
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <BotMessageSquare size={16} />
          Agent
        </button>
      </div>

      {agentEverOpened && (
        <AgentPanel
          projectId={pid}
          jwt={sidecarToken()}
          open={chatOpen}
          width={agentPanelWidth}
          onWidthChange={setAgentPanelWidth}
          onClose={() => setChatOpen(false)}
          onArtifact={onAgentArtifact}
        />
      )}

      <Lightbox />
      {/* 小地图是画布的工具，同第 1/2 层一样在故事板模式下隐藏——它自己
          fixed + z-30，DOM 顺序又在覆盖层之后，不隐藏会浮在故事板内容上。 */}
      {view === 'canvas' && <Minimap />}

      {/* 快捷键提示同 Minimap 一个 bug 家族：自己 fixed + z-40（比故事板覆盖层
          的 z-30 还高），DOM 顺序又在覆盖层之后，不隐藏就会浮在故事板内容
          上方，宣传一堆已经被 data-storyboard-open 停用的画布快捷键。 */}
      {view === 'canvas' && <ShortcutsHint />}
    </div>
  );
}
```

- [ ] **Step 4: 删除 X1、X2、X4、X7 的文件，去掉项目列表页的"我的分享"按钮**

```bash
cd /Users/james/github/miaoju-desktop/web/src
git rm -q modules/canvas/share/SharePage.tsx modules/canvas/share/SharedCanvasViewer.tsx \
  modules/canvas/panels/PublishSharePopover.tsx modules/canvas/panels/PublishWorkflowModal.tsx \
  modules/canvas/panels/TvShowSubmitModal.tsx modules/canvas/panels/CoverGalleryModal.tsx \
  modules/canvas/panels/ChangePasswordModal.tsx modules/canvas/panels/ChatPanel.tsx \
  modules/canvas/__tests__/PublishSharePopover.test.tsx modules/canvas/__tests__/TvShowSubmitModal.test.tsx \
  modules/canvas/editor/PresenceBar.tsx modules/canvas/editor/PresenceBar.test.tsx \
  modules/canvas/editor/teamOwnerToken.ts modules/canvas/editor/teamOwnerToken.test.ts \
  modules/canvas/editor/observeMode.ts modules/canvas/editor/observeMode.test.ts \
  modules/canvas/editor/remoteOpFrame.ts modules/canvas/editor/remoteOpFrame.test.ts \
  modules/canvas/editor/Toolbar.tsx modules/canvas/README.md shared/useVersionCheck.tsx
sed -n 396,398p modules/canvas/CanvasProjectsPage.tsx
```

Expected: 打印的 3 行依次是 `{/* 创建入口只剩网格首卡，…`、`入口并存会让人犹豫点哪个。 */}`、`<Button onClick={() => navigate('/workflows/mine')}>我的分享</Button>`。确认无误后执行：

```bash
sed -i '' '396,398d' modules/canvas/CanvasProjectsPage.tsx
sed -i '' '6{/^  Button,$/d;}' modules/canvas/CanvasProjectsPage.tsx
grep -c "Button\|workflows" modules/canvas/CanvasProjectsPage.tsx
```

Expected: 输出 `0`。

- [ ] **Step 5: 运行页面测试**

Run: `pnpm --dir web exec vitest run src/modules/canvas/CanvasEditorPage.test.tsx src/modules/canvas/__tests__/CanvasProjectsPage.test.tsx`
Expected: PASS（页面 19 个用例，列表页用例全部通过）。

- [ ] **Step 6: 全量验证**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | tail -2
pnpm --dir web test 2>&1 | tail -4
grep -rn "PublishWorkflowModal\|TvShowSubmitModal\|PresenceBar\|useVersionCheck\|teamOwnerToken\|observeMode\|remoteOpFrame\|ChatPanel\|ChangePasswordModal" web/src --include=*.ts --include=*.tsx
```

Expected: 构建成功；`Test Files  156 passed (156)`（162 − 6 个被删测试），全部通过；最后的 grep 没有输出。

- [ ] **Step 7: 提交**

```bash
git add -A web/src/modules/canvas web/src/shared/useVersionCheck.tsx
git commit -m "feat(web): 画布页去掉编辑锁/观看/团队实时通道/积分/发布，返回改为所有项目；删除分享、协作、账户、ChatPanel"
```

---

### Task 3: 启动外壳（路由、F9 启动进画布、字体本地化、AntD 主题、CSP）

**Files:**
- Modify（整体替换）: `web/src/main.tsx`
- Create: `web/src/App.tsx`、`web/src/desktop/theme.ts`、`web/src/desktop/startApp.tsx`、`web/src/desktop/startApp.test.tsx`、`web/src/desktop/resolveStartupProject.ts`、`web/src/desktop/resolveStartupProject.test.ts`、`web/src/desktop/StartupRedirect.tsx`、`web/src/desktop/StartupRedirect.test.tsx`、`web/src/shared/noPublicHost.test.ts`
- Modify: `web/src/styles/canvas.css`（第 1–4 行）
- Modify: `src-tauri/tauri.conf.json`（`app.security`）

**Interfaces:**
- Consumes: Task 1 的 `setSidecarSession`、`loadLastProjectId`、`clearLastProjectId`；P0 的 `getServerInfo(): Promise<ServerInfo>`；参考仓 `api.ts` 的 `getProject(id): Promise<Project>`、`listProjectsPage(limit, offset): Promise<{ items: Project[]; total: number }>`（sidecar 按 `updated_at DESC, id DESC` 排序，见 `server/internal/projects/repository.go:124`）、`createProject(name, description): Promise<Project>`；组件 `CanvasProjectsPage`、`CanvasEditorPage`、`DiagPage`（只按名字引用，不依赖 Task 2、Task 5 的改动）。
- Produces:
  - 路由：`/` → `StartupRedirect`、`/canvas` → `CanvasProjectsPage`、`/canvas/:projectId` → `CanvasEditorPage`、`/diag` → `DiagPage`、其他路径 → `/`。
  - `resolveStartupProject(deps?: StartupDeps): Promise<number>`、`UNTITLED_PROJECT_NAME = '未命名项目'`。
  - `startApp(container: HTMLElement, loadServerInfo?: () => Promise<ServerInfo>): Promise<Root>`：会话建立后才渲染路由。
  - `darkTheme: ThemeConfig`。

- [ ] **Step 1: 写测试（先写测试）**

`web/src/desktop/resolveStartupProject.test.ts`：

```ts
import { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project } from '@/shared/api';
import { loadLastProjectId, saveLastProjectId } from './lastProject';
import { resolveStartupProject, UNTITLED_PROJECT_NAME, type StartupDeps } from './resolveStartupProject';

function project(id: number): Project {
  return { id, userId: 1, name: `p${id}`, description: '', createdAt: '', updatedAt: '' };
}

function httpError(status: number): AxiosError {
  const config = { headers: new AxiosHeaders() } as InternalAxiosRequestConfig;
  return new AxiosError(`status ${status}`, String(status), config, undefined, {
    status,
    statusText: '',
    headers: {},
    config,
    data: {},
  });
}

function makeDeps(over: Partial<StartupDeps> = {}) {
  return {
    getProject: vi.fn(async (id: number) => project(id)),
    listProjectsPage: vi.fn(async () => ({ items: [] as Project[], total: 0 })),
    createProject: vi.fn(async () => project(99)),
    ...over,
  };
}

describe('resolveStartupProject', () => {
  beforeEach(() => localStorage.clear());

  it('上一次打开的项目还在：直接打开它，不查列表', async () => {
    saveLastProjectId(7);
    const deps = makeDeps();

    expect(await resolveStartupProject(deps)).toBe(7);
    expect(deps.getProject).toHaveBeenCalledWith(7);
    expect(deps.listProjectsPage).not.toHaveBeenCalled();
  });

  it('上一次的项目已删除（404）：清掉记录，打开最近修改的项目', async () => {
    saveLastProjectId(7);
    const deps = makeDeps({
      getProject: vi.fn(async () => {
        throw httpError(404);
      }),
      listProjectsPage: vi.fn(async () => ({ items: [project(12), project(3)], total: 2 })),
    });

    expect(await resolveStartupProject(deps)).toBe(12);
    expect(deps.listProjectsPage).toHaveBeenCalledWith(1, 0);
    expect(loadLastProjectId()).toBeNull();
  });

  it('没有记录：打开最近修改的项目（列表第一项）', async () => {
    const deps = makeDeps({
      listProjectsPage: vi.fn(async () => ({ items: [project(5)], total: 9 })),
    });

    expect(await resolveStartupProject(deps)).toBe(5);
    expect(deps.getProject).not.toHaveBeenCalled();
    expect(deps.createProject).not.toHaveBeenCalled();
  });

  it('一个项目都没有：新建「未命名项目」并打开', async () => {
    const deps = makeDeps();

    expect(await resolveStartupProject(deps)).toBe(99);
    expect(deps.createProject).toHaveBeenCalledWith(UNTITLED_PROJECT_NAME, '');
    expect(UNTITLED_PROJECT_NAME).toBe('未命名项目');
  });

  it('查上一次的项目遇到非 404 错误（sidecar 故障）：原样抛出，不新建项目', async () => {
    saveLastProjectId(7);
    const deps = makeDeps({
      getProject: vi.fn(async () => {
        throw httpError(500);
      }),
    });

    await expect(resolveStartupProject(deps)).rejects.toBeInstanceOf(AxiosError);
    expect(deps.createProject).not.toHaveBeenCalled();
    expect(loadLastProjectId()).toBe(7);
  });

  it('列表接口失败：原样抛出，不新建项目', async () => {
    const deps = makeDeps({
      listProjectsPage: vi.fn(async () => {
        throw httpError(404);
      }),
    });

    await expect(resolveStartupProject(deps)).rejects.toBeInstanceOf(AxiosError);
    expect(deps.createProject).not.toHaveBeenCalled();
  });
});
```

`web/src/desktop/StartupRedirect.test.tsx`：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const resolveStartupProject = vi.fn();
vi.mock('./resolveStartupProject', () => ({
  resolveStartupProject: () => resolveStartupProject(),
}));

import { StartupRedirect } from './StartupRedirect';

function renderAt() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <Routes>
        <Route path="/" element={<StartupRedirect />} />
        <Route path="/canvas/:projectId" element={<div>canvas-page</div>} />
        <Route path="/diag" element={<div>diag-page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('StartupRedirect', () => {
  beforeEach(() => resolveStartupProject.mockReset());

  it('算出项目后直接进画布', async () => {
    resolveStartupProject.mockResolvedValue(8);
    renderAt();
    expect(await screen.findByText('canvas-page')).toBeInTheDocument();
  });

  it('失败时显示原因，可重试，也能进环境诊断', async () => {
    resolveStartupProject.mockRejectedValueOnce(new Error('Request failed with status code 404'));
    resolveStartupProject.mockResolvedValueOnce(3);
    renderAt();

    expect(await screen.findByText('无法打开项目')).toBeInTheDocument();
    expect(screen.getByText('Request failed with status code 404')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '环境诊断' })).toHaveAttribute('href', '/diag');

    fireEvent.click(screen.getByRole('button', { name: /重\s*试/ }));
    await waitFor(() => expect(screen.getByText('canvas-page')).toBeInTheDocument());
    expect(resolveStartupProject).toHaveBeenCalledTimes(2);
  });
});
```

`web/src/desktop/startApp.test.tsx`：

```tsx
import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Root } from 'react-dom/client';
import { getSidecarSession, resetSidecarSessionForTest } from '@/shared/sidecarSession';

// App 本身（画布页、项目列表）不在本文件的测试范围，用占位替代。
vi.mock('@/App', () => ({ App: () => <div>app-routes</div> }));

import { startApp } from './startApp';

describe('startApp', () => {
  let root: Root | null = null;
  afterEach(() => {
    act(() => root?.unmount());
    root = null;
    document.body.innerHTML = '';
    resetSidecarSessionForTest();
  });

  function container() {
    const el = document.createElement('div');
    document.body.appendChild(el);
    return el;
  }

  it('拿到端口和 token 后写入会话，再渲染应用路由', async () => {
    let resolve!: (v: { port: number; token: string }) => void;
    const pending = new Promise<{ port: number; token: string }>((r) => { resolve = r; });

    let started!: Promise<Root>;
    act(() => {
      started = startApp(container(), () => pending);
    });
    // 会话建立之前只显示「正在连接」，不渲染任何页面（axios 此时还不知道地址）。
    expect(await screen.findByText('正在连接本地服务…')).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();

    await act(async () => {
      resolve({ port: 50001, token: 't' });
      root = await started;
    });
    await waitFor(() => expect(screen.getByText('app-routes')).toBeInTheDocument());
    expect(getSidecarSession()).toEqual({ port: 50001, token: 't' });
  });

  it('sidecar 启动失败：显示原因，不渲染应用、不写会话', async () => {
    await act(async () => {
      root = await startApp(container(), () => Promise.reject(new Error('sidecar exited: code 1')));
    });
    expect(screen.getByText('本地服务未启动')).toBeInTheDocument();
    expect(screen.getByText(/sidecar exited: code 1/)).toBeInTheDocument();
    expect(screen.queryByText('app-routes')).not.toBeInTheDocument();
    expect(getSidecarSession()).toBeNull();
  });
});
```

`web/src/shared/noPublicHost.test.ts`：

```ts
// 守护：前端源码里不得写死任何公网地址（spec S12 未定、E7 字体本地化、SEC4 CSP）。
// 媒体和接口地址一律由 sidecar 会话给出；需要外链的功能（如 F7 角色库图片）把
// 地址写进配置，而不是写进前端代码。
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');
// 被引号/括号直接包住的 http(s) 字面量；注释里的说明性 URL 不算。
const URL_LITERAL = /['"`(]https?:\/\/(?!127\.0\.0\.1[:/]|localhost[:/]|www\.w3\.org\/)[^'"`)\s]+/g;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|css)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

describe('前端源码不写死公网地址', () => {
  it('src 下非测试文件没有公网 URL 字面量', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const text = readFileSync(file, 'utf8');
      for (const m of text.matchAll(URL_LITERAL)) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
```

- [ ] **Step 2: 运行，确认失败**

Run: `pnpm --dir web exec vitest run src/desktop src/shared/noPublicHost.test.ts`
Expected: FAIL。三个 desktop 测试报找不到 `./resolveStartupProject`、`./StartupRedirect`、`./startApp`；`noPublicHost` 报出 `styles/canvas.css: 'https://fonts.googleapis.com/css2?…`。`lastProject.test.ts` 照常通过。

- [ ] **Step 3: 实现启动逻辑、路由和主题**

`web/src/desktop/resolveStartupProject.ts`：

```ts
// 启动时决定打开哪个项目（spec F9）：
//   1. 上一次打开的项目（还存在的话）；
//   2. 否则最近修改的项目（列表接口按 updated_at 倒序）；
//   3. 一个项目都没有时，新建「未命名项目」。
import axios from 'axios';
import { createProject, getProject, listProjectsPage } from '@/shared/api';
import { clearLastProjectId, loadLastProjectId } from './lastProject';

export interface StartupDeps {
  getProject: typeof getProject;
  listProjectsPage: typeof listProjectsPage;
  createProject: typeof createProject;
}

const defaultDeps: StartupDeps = { getProject, listProjectsPage, createProject };

export const UNTITLED_PROJECT_NAME = '未命名项目';

function isNotFound(err: unknown): boolean {
  return axios.isAxiosError(err) && err.response?.status === 404;
}

/**
 * 返回要打开的项目 id。只有「上一次的项目已不存在（404）」会被吞掉并退到下一步；
 * 其它错误（sidecar 没起来、网络、500）原样抛出，由调用方显示错误页——不能因为
 * 一次暂时性失败就去新建一个空项目。
 */
export async function resolveStartupProject(deps: StartupDeps = defaultDeps): Promise<number> {
  const last = loadLastProjectId();
  if (last !== null) {
    try {
      const project = await deps.getProject(last);
      return project.id;
    } catch (err) {
      if (!isNotFound(err)) throw err;
      clearLastProjectId();
    }
  }
  const { items } = await deps.listProjectsPage(1, 0);
  if (items.length > 0) return items[0].id;
  const created = await deps.createProject(UNTITLED_PROJECT_NAME, '');
  return created.id;
}
```

`web/src/desktop/StartupRedirect.tsx`：

```tsx
import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, Result, Spin } from 'antd';
import { resolveStartupProject } from './resolveStartupProject';

// 根路由 `/`：不渲染任何首页，算出要打开的项目后直接跳进画布（spec F9）。
export function StartupRedirect() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  const retry = useCallback(() => {
    setError(null);
    setAttempt((n) => n + 1);
  }, []);

  useEffect(() => {
    let cancelled = false;
    resolveStartupProject()
      .then((id) => {
        if (!cancelled) navigate(`/canvas/${id}`, { replace: true });
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [navigate, attempt]);

  if (error) {
    return (
      <Result
        status="error"
        title="无法打开项目"
        subTitle={error}
        extra={[
          <Button key="retry" type="primary" onClick={retry}>
            重试
          </Button>,
          <Link key="diag" to="/diag">
            环境诊断
          </Link>,
        ]}
      />
    );
  }
  return (
    <div className="flex h-screen items-center justify-center">
      <Spin tip="正在打开项目…">
        <div className="w-24 h-24" />
      </Spin>
    </div>
  );
}
```

`web/src/desktop/theme.ts`：

```ts
import { theme } from 'antd';
import type { ThemeConfig } from 'antd';

// Ant Design 暗色主题——原样取自参考仓 apps/web/src/main.tsx。这些值对齐
// app.tapnow.ai 的 design token，让 AntD 的弹窗、下拉、tooltip、表格跟画布
// 共用同一套配色。
export const darkTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: '#1fa2dc',
    colorBgBase: '#000000',
    colorBgContainer: '#1f1f1f',
    colorBgElevated: '#262626',
    colorBgLayout: '#000000',
    colorBorder: 'rgba(255,255,255,0.15)',
    colorBorderSecondary: 'rgba(255,255,255,0.10)',
    colorText: '#f5f5f5',
    colorTextSecondary: '#cfcece',
    colorTextTertiary: '#7a7a7a',
    borderRadius: 12,
    fontFamily: 'Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  },
  components: {
    Card: { colorBgContainer: '#1f1f1f' },
    Modal: { contentBg: '#1f1f1f', headerBg: '#1f1f1f' },
    Layout: {
      bodyBg: '#000000',
      headerBg: '#1c1c1c',
      siderBg: '#1c1c1c',
    },
    Menu: {
      darkItemBg: '#1c1c1c',
      darkSubMenuItemBg: '#1c1c1c',
      darkItemSelectedBg: '#262626',
    },
    Popover: { colorBgElevated: '#262626' },
    Tooltip: { colorBgSpotlight: '#262626' },
  },
};
```

`web/src/desktop/startApp.tsx`：

```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp, ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { App } from '@/App';
import { queryClient } from '@/shared/queryClient';
import { getServerInfo, type ServerInfo } from '@/shared/server';
import { setSidecarSession } from '@/shared/sidecarSession';
import { darkTheme } from './theme';

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <ConfigProvider locale={zhCN} theme={darkTheme}>
          <AntApp>{children}</AntApp>
        </ConfigProvider>
      </QueryClientProvider>
    </React.StrictMode>
  );
}

export function SidecarConnecting() {
  return <div style={{ padding: 24, color: '#cfcece' }}>正在连接本地服务…</div>;
}

// sidecar 没起来：显示原因，而不是白屏（壳不会自动重启 sidecar，P8 才做重启）。
export function SidecarFailed({ error }: { error: string }) {
  return (
    <div style={{ padding: 24, color: '#f5f5f5' }}>
      <h2>本地服务未启动</h2>
      <pre style={{ whiteSpace: 'pre-wrap', color: '#f88' }}>{error}</pre>
      <p>请退出并重新打开应用。</p>
    </div>
  );
}

/**
 * 启动流程：先拿到本次 sidecar 的端口和 token 并写进会话，之后才渲染任何页面——
 * axios 拦截器和 AgentClient 都是同步读会话的。
 */
export async function startApp(
  container: HTMLElement,
  loadServerInfo: () => Promise<ServerInfo> = getServerInfo,
): Promise<ReactDOM.Root> {
  const root = ReactDOM.createRoot(container);
  root.render(<Shell><SidecarConnecting /></Shell>);
  let info: ServerInfo;
  try {
    info = await loadServerInfo();
  } catch (err) {
    root.render(<Shell><SidecarFailed error={String(err)} /></Shell>);
    return root;
  }
  setSidecarSession(info);
  root.render(
    <Shell>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </Shell>,
  );
  return root;
}
```

`web/src/App.tsx`：

```tsx
import { Navigate, Route, Routes } from 'react-router-dom';
import { CanvasProjectsPage } from '@/modules/canvas/CanvasProjectsPage';
import { CanvasEditorPage } from '@/modules/canvas/CanvasEditorPage';
import { DiagPage } from '@/diag/DiagPage';
import { StartupRedirect } from '@/desktop/StartupRedirect';

// 桌面端路由（spec F9）：
//   /                  启动入口，直接跳进画布（上一次 → 最近修改 → 新建）
//   /canvas            所有项目（原样复用参考仓的 CanvasProjectsPage）
//   /canvas/:projectId 画布编辑器
//   /diag              环境诊断（P0 诊断页，隐藏入口；P4 移入「设置 → 环境诊断」）
// 路径与参考仓保持一致：画布模块里有十余处直接从 location.pathname 解析 /canvas/:id。
export function App() {
  return (
    <Routes>
      <Route path="/" element={<StartupRedirect />} />
      <Route path="/canvas" element={<CanvasProjectsPage />} />
      <Route path="/canvas/:projectId" element={<CanvasEditorPage />} />
      <Route path="/diag" element={<DiagPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
```

- [ ] **Step 4: 字体本地化：删掉 `canvas.css` 的 Google Fonts，改写 `main.tsx`**

把 `web/src/styles/canvas.css` 的第 1–4 行：

```css
/* Inter is TapNow's default sans family. Loaded once globally so node
   prompt textareas, panel labels, and the right-click menu all share the
   same family without per-component import. */
@import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');
```

替换为：

```css
/* Inter 是 TapNow 的默认无衬线字体。桌面端字体随应用打包（spec E7）：
   Inter 400/500/600/700 与 JetBrains Mono 400/500 由 main.tsx 从
   @fontsource 引入，不再从 Google Fonts 在线加载。 */
```

`web/src/main.tsx` 整个文件替换为：

```tsx
// 字体随应用打包（spec E7）：不再从 Google Fonts 在线加载。
import '@fontsource/inter/400.css';
import '@fontsource/inter/500.css';
import '@fontsource/inter/600.css';
import '@fontsource/inter/700.css';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/500.css';
import 'antd/dist/reset.css';
import './styles/canvas.css';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { installGestureGuards } from '@/shared/gestureGuard';
import { startApp } from '@/desktop/startApp';

// AntD 的 ConfigProvider locale 只汉化组件自身文案；DatePicker 的星期 / 月份名来自
// dayjs，需单独把 dayjs 全局 locale 设成中文（与参考仓 main.tsx 一致）。
dayjs.locale('zh-cn');

// 以下三项原样沿用参考仓 main.tsx：测试钩子、清理过期的抽帧缓存、拦截触控板手势。
(window as any).__CANVAS_STORE_FOR_TEST__ = useCanvasStore.getState();
import('@/shared/frameCache').then((m) => m.purgeExpired()).catch(() => {});
installGestureGuards();

void startApp(document.getElementById('root')!);
```

- [ ] **Step 5: CSP：禁止 Tauri 往 style-src 注入 nonce**

在 `src-tauri/tauri.conf.json` 的 `app.security` 对象里，`"csp"` 那一行后面加一个字段，结果是：

```json
    "security": {
      "csp": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob: http://127.0.0.1:*; media-src 'self' blob: http://127.0.0.1:*; connect-src 'self' ipc: http://ipc.localhost http://127.0.0.1:* ws://127.0.0.1:*; worker-src 'self' blob:",
      "dangerousDisableAssetCspModification": ["style-src"]
    }
```

原因：AntD（CSS-in-JS）和 `CanvasProjectsPage` 的 `<style>` 都在运行时插入内联样式，依赖 `'unsafe-inline'`。只要 style-src 里出现 nonce 或 hash，浏览器就会忽略 `'unsafe-inline'`。script-src 仍由 Tauri 管理。

- [ ] **Step 6: 运行测试**

Run: `pnpm --dir web exec vitest run src/desktop src/shared/noPublicHost.test.ts`
Expected: PASS（lastProject 5、resolveStartupProject 6、StartupRedirect 2、startApp 2、noPublicHost 1）。

- [ ] **Step 7: 全量验证**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | grep -E "built in|error"
ls web/dist/assets | grep -c "woff2"
grep -l "googleapis" web/dist/assets/*.css; echo "exit=$?"
pnpm --dir web test 2>&1 | tail -4
bash scripts/build-sidecar.sh && (cd src-tauri && cargo build 2>&1 | tail -1)
```

Expected: 构建成功；woff2 文件数大于 0（Inter 和 JetBrains Mono 各个字重、各个子集）；`grep -l` 没有输出，`exit=1`；测试全部通过；`cargo build` 成功（证明 conf 字段合法）。

- [ ] **Step 8: 提交**

```bash
git add web/src/main.tsx web/src/App.tsx web/src/desktop web/src/styles/canvas.css web/src/shared/noPublicHost.test.ts src-tauri/tauri.conf.json
git commit -m "feat(web): 启动直接进画布（上次项目→最近修改→新建），路由与诊断隐藏入口，字体本地化，AntD 暗色主题"
```

---

### Task 4: API 层接 sidecar（axios、WebSocket、媒体 crossOrigin、删除登录/团队/发布接口）

**前置：** Task 2 已合入（从"Task 1 + Task 2"的提交开 worktree）。

**Files:**
- Modify: `web/src/shared/api.ts`（换头部，按 F-6 删除死代码块）
- Modify（整体替换）: `web/src/shared/api.test.ts`
- Modify（整体替换）: `web/src/shared/permissions.ts`
- Delete: `web/src/shared/identity.ts`、`web/src/shared/identity.test.ts`
- Modify: `web/src/modules/canvas/editor/agent/AgentPanel.tsx`（第 54 行后加 1 行 import，第 268 行改 1 处）
- Modify: `web/src/modules/canvas/editor/utils/mediaCrossOrigin.ts`
- Create: `web/src/modules/canvas/editor/utils/mediaCrossOrigin.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `sidecarApiBaseUrl()`、`sidecarToken()`、`sidecarWsUrl(path)`、`setSidecarSession`、`resetSidecarSessionForTest`；`apiGuards.isApiWriteAllowed`、`setApiWritesBlocked`（参考仓原样）。
- Produces:
  - `api`（axios 实例）：每个请求 `baseURL = sidecarApiBaseUrl()`；有 token 时加 `Authorization: Bearer <token>`；401 原样 reject，不跳转；只读模式下写请求 reject 一个 `{ code: 'READ_ONLY' }` 错误。
  - `api.ts` 不再导出 `getToken`、`setToken`、`clearAuthSession`、`login`、`register`、`changePassword`、`syncAuthUser`、`applyAuthSession`、`LoginResult`、`__resetIdentityRecoveryForTest`、`claimCanvasOwner`、`heartbeatCanvasOwner`、`probeCanvasOwner`、`CanvasOwnerClaim`、`CanvasOwnerProbe`、`getAuthContexts`、`switchContext`、团队、会员、支付、分享工作流、TV Show、banner、CLI/Flow 登录、项目 Chat 会话、`chatCompletion`、`getMyLedger` 等接口（完整范围见 F-6）。其余导出不变。
  - `useIsSuperAdmin(): boolean` 恒为 `false`。
  - `mediaCrossOrigin(url)` 对 `http://127.0.0.1:<端口>/…` 返回 `'anonymous'`。

- [ ] **Step 1: 写测试（先写测试）**

`web/src/shared/api.test.ts` 整个文件替换为下面的内容。新增"接 sidecar"一组；"canvas mutations"和"去字幕"两组沿用参考仓，但去掉了 `claimCanvasOwner`、`heartbeatCanvasOwner` 的调用，`CANVAS_OWNER_EXPIRED` 用例改用 `upsertCanvasConnection` 触发。

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { InternalAxiosRequestConfig } from 'axios';
import {
  api,
  CanvasOwnerLostError,
  deleteCanvasConnection,
  deleteCanvasNodes,
  fetchCanvasPostprocessConfig,
  getProject,
  patchCanvasNode,
  patchCanvasNodePositions,
  submitCanvasDesub,
  upsertCanvasConnection,
  upsertCanvasNode,
} from './api';
import { setApiWritesBlocked } from './apiGuards';
import { resetSidecarSessionForTest, setSidecarSession } from './sidecarSession';

// 桌面端：请求一律发往本次启动的 sidecar，带 Bearer token；没有登录态，401 不跳转。
describe('shared/api 接 sidecar', () => {
  const realAdapter = api.defaults.adapter;
  let seen: InternalAxiosRequestConfig[] = [];
  let status = 200;

  beforeEach(() => {
    vi.restoreAllMocks();
    seen = [];
    status = 200;
    api.defaults.adapter = async (config) => {
      seen.push(config);
      if (status >= 400) {
        return Promise.reject(
          Object.assign(new Error(`Request failed with status code ${status}`), {
            isAxiosError: true,
            config,
            response: { status, statusText: '', headers: {}, config, data: { error: 'x' } },
          }),
        );
      }
      return { data: { id: 3, name: 'p' }, status, statusText: 'OK', headers: {}, config };
    };
  });

  afterEach(() => {
    api.defaults.adapter = realAdapter;
    resetSidecarSessionForTest();
    setApiWritesBlocked(false);
  });

  it('baseURL 指向 127.0.0.1:<端口>/api/v1，并带 Authorization: Bearer <token>', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });

    const project = await getProject(3);

    expect(project).toEqual({ id: 3, name: 'p' });
    expect(seen).toHaveLength(1);
    expect(seen[0].baseURL).toBe('http://127.0.0.1:50123/api/v1');
    expect(seen[0].url).toBe('/projects/3');
    expect(seen[0].headers.Authorization).toBe('Bearer tok-1');
  });

  it('sidecar 换了端口和 token（重新建会话）后，下一个请求立刻用新的', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });
    await getProject(3);
    setSidecarSession({ port: 50999, token: 'tok-2' });
    await getProject(3);

    expect(seen[1].baseURL).toBe('http://127.0.0.1:50999/api/v1');
    expect(seen[1].headers.Authorization).toBe('Bearer tok-2');
  });

  it('401 原样抛给调用方，不跳登录页', async () => {
    setSidecarSession({ port: 50123, token: 'stale' });
    status = 401;
    const before = window.location.href;

    const err = await getProject(3).catch((e: unknown) => e);

    expect((err as { response?: { status?: number } }).response?.status).toBe(401);
    expect(window.location.href).toBe(before);
  });

  it('只读模式下写请求被本地挡下，不发到 sidecar', async () => {
    setSidecarSession({ port: 50123, token: 'tok-1' });
    setApiWritesBlocked(true);

    const err = await upsertCanvasNode(14, 'local', { id: 'n1' }).catch((e: unknown) => e);

    expect((err as { code?: string }).code).toBe('READ_ONLY');
    expect(seen).toHaveLength(0);
  });
});

describe('shared/api canvas mutations', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('calls canvas mutation endpoints with exact payloads', async () => {
    const node = { id: 'n1', type: 'image', x: 10 };
    const patch = { data: { prompt: 'new' } };
    const nodes = [{ id: 'n1', x: 1, y: 2, width: 300, height: 200 }];
    const connection = { id: 'e1', from: 'n1', to: 'n2' };

    const postSpy = vi.spyOn(api, 'post')
      .mockResolvedValueOnce({ data: { version: 1 } })
      .mockResolvedValueOnce({ data: { version: 2 } });
    const patchSpy = vi.spyOn(api, 'patch')
      .mockResolvedValueOnce({ data: { version: 3 } })
      .mockResolvedValueOnce({ data: { version: 4 } });
    const deleteSpy = vi.spyOn(api, 'delete')
      .mockResolvedValueOnce({ data: { version: 5 } })
      .mockResolvedValueOnce({ data: { version: 6 } });

    await upsertCanvasNode(14, 'tok', node);
    await patchCanvasNode(14, 'tok', 'n/1 space', patch);
    await deleteCanvasNodes(14, 'tok', ['n1']);
    await patchCanvasNodePositions(14, 'tok', nodes);
    await upsertCanvasConnection(14, 'tok', connection);
    await deleteCanvasConnection(14, 'tok', 'e1');

    expect(postSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes', { ownerToken: 'tok', node });
    expect(patchSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes/n%2F1%20space', { ownerToken: 'tok', patch });
    expect(deleteSpy).toHaveBeenNthCalledWith(1, '/projects/14/canvas/nodes', {
      data: { ownerToken: 'tok', nodeIds: ['n1'] },
    });
    expect(patchSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/nodes/positions', { ownerToken: 'tok', nodes });
    expect(postSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/connections', { ownerToken: 'tok', connection });
    expect(deleteSpy).toHaveBeenNthCalledWith(2, '/projects/14/canvas/connections/e1', {
      data: { ownerToken: 'tok' },
    });
  });

  it('maps CANVAS_OWNER_LOST conflicts to CanvasOwnerLostError', async () => {
    vi.spyOn(api, 'patch').mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'CANVAS_OWNER_LOST', error: 'owner lost' },
      },
    });

    const error = await patchCanvasNode(14, 'tok', 'n1', {}).catch((err: unknown) => err);

    expect(error).toBeInstanceOf(CanvasOwnerLostError);
    expect(error).toMatchObject({
      code: 'CANVAS_OWNER_LOST',
      message: 'owner lost',
    });
  });

  it('maps CANVAS_OWNER_EXPIRED conflicts to CanvasOwnerLostError', async () => {
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'CANVAS_OWNER_EXPIRED', error: 'owner expired' },
      },
    });

    const error = await upsertCanvasConnection(14, 'tok', { id: 'e1', from: 'n1', to: 'n2' }).catch((err: unknown) => err);

    expect(error).toBeInstanceOf(CanvasOwnerLostError);
    expect(error).toMatchObject({
      code: 'CANVAS_OWNER_EXPIRED',
      message: 'owner expired',
    });
  });

  it('rethrows 409 conflicts with other codes unchanged', async () => {
    const originalError = {
      isAxiosError: true,
      response: {
        status: 409,
        data: { code: 'SNAPSHOT_VERSION_CONFLICT', error: 'conflict' },
      },
    };
    vi.spyOn(api, 'delete').mockRejectedValue(originalError);

    await expect(deleteCanvasNodes(14, 'tok', ['n1'])).rejects.toBe(originalError);
  });

  it('rethrows non-409 errors unchanged', async () => {
    const originalError = {
      isAxiosError: true,
      response: {
        status: 500,
        data: { error: 'server error' },
      },
    };
    vi.spyOn(api, 'delete').mockRejectedValue(originalError);

    await expect(deleteCanvasConnection(14, 'tok', 'e1')).rejects.toBe(originalError);
  });
});

describe('shared/api canvas postprocess (去字幕)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('fetchCanvasPostprocessConfig 调 GET /canvas/postprocess-config 并透传返回', async () => {
    const getSpy = vi.spyOn(api, 'get').mockResolvedValueOnce({
      data: {
        subtitleRemoval: [{ modelId: 'm', label: 'L', subtitle: 's', isTimeLimited: false }],
      },
    });

    const cfg = await fetchCanvasPostprocessConfig();

    expect(getSpy).toHaveBeenCalledWith('/canvas/postprocess-config');
    expect(cfg.subtitleRemoval).toHaveLength(1);
    expect(cfg.subtitleRemoval[0]).toMatchObject({ modelId: 'm', label: 'L', subtitle: 's', isTimeLimited: false });
  });

  it('submitCanvasDesub 调 POST /canvas-tasks/desub 带完整 payload 并透传返回', async () => {
    const postSpy = vi.spyOn(api, 'post').mockResolvedValueOnce({ data: { taskId: 7, status: 'processing' } });

    const result = await submitCanvasDesub({ projectId: 1, nodeId: 'n2', sourceNodeId: 'n1', modelId: 'm' });

    expect(postSpy).toHaveBeenCalledWith('/canvas-tasks/desub', {
      projectId: 1,
      nodeId: 'n2',
      sourceNodeId: 'n1',
      modelId: 'm',
    });
    expect(result).toEqual({ taskId: 7, status: 'processing' });
  });
});

// 登录态 key 从 tapnow.* 改名到 tvmao.*。搬迁是为了让改名上线时在线用户不掉线，
```

`web/src/modules/canvas/editor/utils/mediaCrossOrigin.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import { mediaCrossOrigin } from './mediaCrossOrigin';

describe('mediaCrossOrigin', () => {
  it('本机 sidecar 媒体按 CORS 模式加载（否则读像素会污染 canvas）', () => {
    expect(mediaCrossOrigin('http://127.0.0.1:51234/media/u1/2026/10/a.png?token=t')).toBe('anonymous');
  });

  it('参考仓的对象存储签名 URL 照旧', () => {
    expect(mediaCrossOrigin('https://b.tos.example/a.png?X-Tos-Signature=1')).toBe('anonymous');
    expect(mediaCrossOrigin('https://s3.example/a.png?X-Amz-Signature=1')).toBe('anonymous');
  });

  it('上游裸 URL、data:/blob:、空值不加 crossOrigin', () => {
    expect(mediaCrossOrigin('https://cdn.example/a.png')).toBeUndefined();
    expect(mediaCrossOrigin('data:image/png;base64,AAAA')).toBeUndefined();
    expect(mediaCrossOrigin('blob:tauri://localhost/123')).toBeUndefined();
    expect(mediaCrossOrigin('http://127.0.0.1.evil.example/a.png')).toBeUndefined();
    expect(mediaCrossOrigin(null)).toBeUndefined();
  });
});
```

- [ ] **Step 2: 运行，确认失败**

Run: `pnpm --dir web exec vitest run src/shared/api.test.ts src/modules/canvas/editor/utils/mediaCrossOrigin.test.ts`
Expected: FAIL。`baseURL` 仍是 `/api/v1`，没有 `Authorization` 头；`mediaCrossOrigin` 对本机地址返回 `undefined`。

- [ ] **Step 3: 改 `api.ts`：先按原行号一次删掉所有死代码块（包括旧头部第 1–212 行），再在开头写入新头部**

```bash
cd /Users/james/github/miaoju-desktop/web
wc -l src/shared/api.ts
sed -i '' -e '1,212d' -e '223,253d' -e '259,262d' -e '347,351d' -e '419,460d' -e '1015,1076d' \
  -e '1108,1171d' -e '1335,1469d' -e '1651,1682d' -e '1732,2168d' src/shared/api.ts
wc -l src/shared/api.ts
head -3 src/shared/api.ts
```

Expected: 第一次 `wc` 输出 `2168`（文件与参考仓一致，行号可用），第二次输出 `1144`；文件开头是一个空行，接着是 `export interface AuthUser {`。

然后把下面的内容写进 `src/shared/api.head.ts`，再拼到 `api.ts` 前面：

```ts
import axios, { AxiosInstance } from 'axios';
import { isApiWriteAllowed } from './apiGuards';
import { sidecarApiBaseUrl, sidecarToken } from './sidecarSession';

// 桌面端：所有 REST 请求直连本机 sidecar（spec D3）。地址和 token 来自本次启动的
// 会话（./sidecarSession，main.tsx 在渲染任何页面之前写入）。端口每次启动都不同，
// 所以 baseURL 在每个请求里现读，不在模块加载时固化。
//
// 与参考仓的差异：没有登录（spec X4），所以没有 localStorage 里的登录 token、没有
// 「401 → 清会话跳 /login」、没有团队身份自愈。401 只可能是 token 对不上（sidecar
// 换了一次而页面没重载），原样抛给调用方。
export const api: AxiosInstance = axios.create({
  headers: { 'Content-Type': 'application/json' },
});

api.interceptors.request.use((config) => {
  config.baseURL = sidecarApiBaseUrl();
  const token = sidecarToken();
  if (token && config.headers) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  if (!isApiWriteAllowed(config.method, config.url)) {
    // 直接硬失败，让调用方节点的 try/catch 抛出错误提示，而不是默默
    // 吞掉这次点击。viewer 是只读模式——那些本会改动状态的按钮会得到
    // 类似 409 的行为。
    return Promise.reject(
      Object.assign(new Error('画布处于只读模式'), { code: 'READ_ONLY' }),
    );
  }
  return config;
});
```

```bash
cat src/shared/api.head.ts src/shared/api.ts > src/shared/api.ts.new
mv src/shared/api.ts.new src/shared/api.ts
rm src/shared/api.head.ts
npx tsc --noEmit 2>&1 | grep "error TS" | cut -d'(' -f1 | sort -u
```

Expected: 只剩 `src/shared/identity.ts` 和 `src/shared/permissions.ts` 两个文件报错（它们引用了已删除的 `getToken`、`setToken`、`switchContext`、`clearAuthSession`），下一步处理。

- [ ] **Step 4: 删 `identity`，重写 `permissions.ts`**

```bash
cd /Users/james/github/miaoju-desktop/web
git rm -q src/shared/identity.ts src/shared/identity.test.ts
```

`web/src/shared/permissions.ts` 整个文件替换为：

```ts
// 运维专用 UI（画布侧边栏 → 工作流 JSON 导入导出）的权限门控。
//
// 参考仓按「当前用户是不是超管」放行；桌面端固定单个本地用户（spec X4），
// 这些运维入口一律隐藏（spec S10 不做项目导入导出、F10 隐藏 JSON 导入导出）。
export function useIsSuperAdmin(): boolean {
  return false;
}
```

- [ ] **Step 5: Agent WebSocket 改连 sidecar**

```bash
cd /Users/james/github/miaoju-desktop/web
F=src/modules/canvas/editor/agent/AgentPanel.tsx
sed -n 54p $F
sed -n 268p $F
```

Expected: 第 54 行是 `import type { Artifact, ContentBlock, Mention } from './types';`；第 268 行是 `    const c = new AgentClient({ url: '/api/v1/agent/ws', token: jwt, projectId });`。确认后执行：

```bash
sed -i '' "268s|url: '/api/v1/agent/ws'|url: sidecarWsUrl('/api/v1/agent/ws')|" $F
sed -i '' "54a\\
import { sidecarWsUrl } from '@/shared/sidecarSession';
" $F
sed -n 55p $F; sed -n 269p $F
```

Expected: 第 55 行是新加的 import；第 269 行是 `    const c = new AgentClient({ url: sidecarWsUrl('/api/v1/agent/ws'), token: jwt, projectId });`。`jwt` 由画布页传入 `sidecarToken()`（Task 2）。

- [ ] **Step 6: 本机媒体按 CORS 模式加载**

把 `web/src/modules/canvas/editor/utils/mediaCrossOrigin.ts` 末尾的函数：

```ts
export function mediaCrossOrigin(url: string | null | undefined): 'anonymous' | undefined {
  if (!url) return undefined;
  return url.includes('X-Tos-') || url.includes('X-Amz-') ? 'anonymous' : undefined;
}
```

替换为：

```ts
//
// 桌面端：媒体由本机 sidecar（http://127.0.0.1:<端口>/…）提供，页面来源却是
// tauri://localhost（Windows 是 http://tauri.localhost），两者跨源。不带
// crossOrigin 加载的图片会污染 canvas，宫格切分/标注/旋转烘焙读像素时直接抛
// SecurityError。sidecar 对白名单 Origin 回 Access-Control-Allow-Origin，所以
// 本机媒体一律按 CORS 模式加载。
const SIDECAR_MEDIA = /^http:\/\/127\.0\.0\.1:\d+\//;

export function mediaCrossOrigin(url: string | null | undefined): 'anonymous' | undefined {
  if (!url) return undefined;
  if (SIDECAR_MEDIA.test(url)) return 'anonymous';
  return url.includes('X-Tos-') || url.includes('X-Amz-') ? 'anonymous' : undefined;
}
```

- [ ] **Step 7: 运行测试**

Run: `pnpm --dir web exec vitest run src/shared src/modules/canvas/editor/utils/mediaCrossOrigin.test.ts`
Expected: PASS（`api.test.ts` 11 个用例、`mediaCrossOrigin` 3 个，其余 shared 测试照旧通过）。

- [ ] **Step 8: 全量验证**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | tail -2
pnpm --dir web test 2>&1 | tail -4
grep -rn "getToken\|claimCanvasOwner\|heartbeatCanvasOwner\|getAuthContexts\|/auth/\|'/api/v1/agent/ws'" web/src --include=*.ts --include=*.tsx | grep -v "sidecarWsUrl('/api/v1/agent/ws')"
```

Expected: 构建成功；测试全部通过；grep 只匹配到 `apiGuards.ts` 白名单里的 `/auth/switch-context` 和 `canvas/owner/claim` 两条正则，以及 `apiGuards.test.ts`、`diag/checks.ts` 里的相关字符串（白名单留给 P3c 清理；`diag/checks.ts` 用的是 `buildWsUrl`，不是相对地址）。

- [ ] **Step 9: 提交**

```bash
git add -A web/src/shared web/src/modules/canvas/editor/agent/AgentPanel.tsx web/src/modules/canvas/editor/utils/mediaCrossOrigin.ts web/src/modules/canvas/editor/utils/mediaCrossOrigin.test.ts
git commit -m "feat(web): axios 与 Agent WS 改连本机 sidecar（Bearer token），去掉 401 跳登录与登录/团队/发布接口，本机媒体按 CORS 加载"
```

---

### Task 5: 诊断页遗留修复（P0 遗留事项中归 P3 的 checks.ts 四条）

**Files:**
- Modify: `web/src/diag/checks.ts`
- Modify（整体替换）: `web/src/diag/DiagPage.tsx`
- Create: `web/src/diag/checks.test.ts`、`web/src/diag/DiagPage.test.tsx`

**Interfaces:**
- Consumes: P0 的 `getServerInfo`、`buildMediaUrl`；`react-router-dom` 的 `Link`（路由由 Task 3 提供）。
- Produces: `diagMediaPath(name: string): string`；诊断页顶部的"← 返回画布"链接（指向 `/`）。

- [ ] **Step 1: 写测试**

`web/src/diag/checks.test.ts`：

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectionChecks, diagMediaPath } from './checks';

describe('diagMediaPath', () => {
  it('文件名做 URL 编码（空格、中文、#、?）', () => {
    expect(diagMediaPath('样片 1#?.mp4')).toBe('/api/v1/diag/media/%E6%A0%B7%E7%89%87%201%23%3F.mp4');
  });
});

describe('connectionChecks WebSocket 回显', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('服务端在回显前关闭连接：立刻判失败并带上 close code', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"ok":true,"version":"t"}', { status: 401 })));
    class ClosingSocket {
      onopen: (() => void) | null = null;
      onmessage: ((ev: { data: unknown }) => void) | null = null;
      onerror: (() => void) | null = null;
      onclose: ((ev: { code: number }) => void) | null = null;
      constructor() {
        setTimeout(() => this.onclose?.({ code: 1008 }), 0);
      }
      send() {}
      close() {}
    }
    vi.stubGlobal('WebSocket', ClosingSocket);

    const results = await connectionChecks({ port: 1, token: 't' });
    const ws = results.find((r) => r.name === 'WebSocket 回显')!;

    expect(ws.ok).toBe(false);
    expect(ws.detail).toContain('1008');
    expect(ws.ms).toBeLessThan(4000);
  });
});
```

`web/src/diag/DiagPage.test.tsx`（桩函数用普通函数而不是 `vi.fn`：vitest 2.1 下 `vi.fn` 包装的 async 实现抛错时，即使被测代码已经 catch，用例也会被判失败）：

```tsx
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/shared/server', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/server')>()),
  getServerInfo: vi.fn(async () => ({ port: 50001, token: 't' })),
}));

// 用普通函数而不是 vi.fn 承载桩行为：让「检查框架抛错」这条用例的 rejection
// 只经过被测代码一条路径。
let connectionChecksImpl: () => Promise<unknown[]> = async () => [];
vi.mock('./checks', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./checks')>()),
  connectionChecks: () => connectionChecksImpl(),
  webCodecsChecks: vi.fn(async () => []),
}));

import { DiagPage } from './DiagPage';

function renderPage() {
  return render(
    <MemoryRouter>
      <DiagPage />
    </MemoryRouter>,
  );
}

describe('DiagPage', () => {
  beforeEach(() => {
    connectionChecksImpl = async () => [];
  });

  it('检查框架本身抛错时显示失败项，而不是静默', async () => {
    connectionChecksImpl = async () => {
      throw new Error('boom');
    };
    renderPage();
    fireEvent.click(await screen.findByText('1. 运行连接与编解码检查'));
    expect(await screen.findByText(/FAIL 运行检查 — Error: boom/)).toBeInTheDocument();
  });

  it('剪贴板写入被拒时提示手动复制', async () => {
    connectionChecksImpl = async () => [{ name: 'x', ok: true, detail: 'ok', ms: 1 }];
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn(async () => { throw new Error('NotAllowedError'); }) },
    });
    renderPage();
    fireEvent.click(await screen.findByText('1. 运行连接与编解码检查'));
    await waitFor(() => expect(screen.getByText('复制结果')).not.toBeDisabled());
    fireEvent.click(screen.getByText('复制结果'));
    expect(await screen.findByText(/复制失败/)).toBeInTheDocument();
  });
});
```

Run: `pnpm --dir web exec vitest run src/diag`
Expected: FAIL：`diagMediaPath` 不存在；WebSocket 用例要等满 5 秒超时，且错误信息里没有 `1008`；DiagPage 两个用例找不到"FAIL 运行检查"和"复制失败"。

- [ ] **Step 2: 改 `checks.ts`**

在 `ws.onerror = () => { … };` 这段之后（第 68 行 `};` 之后）插入：

```ts
        // 服务端在回显之前就关了连接（例如 Origin 被拒）：立刻判失败，不必干等 5 秒。
        // 已经 resolve 过的话（onmessage 里主动 close 也会触发这里），reject 是空操作。
        ws.onclose = (ev) => {
          clearTimeout(timer);
          reject(new Error(`连接在回显前关闭（code ${ev.code}）`));
        };
```

在 `/** 用参考仓画布同样的 mediabunny 调用方式，对 sidecar 返回的媒体跑一遍。 */` 这行之前插入：

```ts
/** 诊断媒体的接口路径。文件名来自 sidecar，可能含空格或中文，必须编码。 */
export function diagMediaPath(name: string): string {
  return `/api/v1/diag/media/${encodeURIComponent(name)}`;
}

```

并把 `mediaChecks` 里的

```ts
  const url = buildMediaUrl(info, `/api/v1/diag/media/${name}`);
```

改为

```ts
  const url = buildMediaUrl(info, diagMediaPath(name));
```

- [ ] **Step 3: 整体替换 `DiagPage.tsx`**

改动：`runBasic` 补 catch；复制结果处理剪贴板被拒；页面可滚动（canvas.css 全局样式下）；加"返回画布"链接。

```tsx
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { getServerInfo, type ServerInfo } from '@/shared/server';
import {
  connectionChecks,
  formatReport,
  mediaChecks,
  uploadSample,
  webCodecsChecks,
  type CheckResult,
} from './checks';

export function DiagPage() {
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [results, setResults] = useState<CheckResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [copyStatus, setCopyStatus] = useState('');

  useEffect(() => {
    getServerInfo().then(setInfo, (e) => setStartError(String(e)));
  }, []);

  // sidecar 没起来：显示原因和提示，而不是白屏（壳不会自动重启 sidecar，重试无意义）。
  if (startError) {
    return (
      <div style={{ padding: 24 }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88' }}>{startError}</pre>
        <p>请退出并重新打开应用。</p>
      </div>
    );
  }
  if (!info) return <div style={{ padding: 24 }}>正在连接本地服务…</div>;

  const runBasic = async () => {
    setBusy(true);
    try {
      setResults([...(await connectionChecks(info)), ...(await webCodecsChecks())]);
    } catch (e) {
      // 单项检查各自捕获异常；走到这里说明检查框架本身出错，也要显示出来而不是静默。
      setResults([{ name: '运行检查', ok: false, detail: String(e), ms: 0 }]);
    } finally {
      setBusy(false);
    }
  };

  const runMedia = async (file: File) => {
    setBusy(true);
    try {
      const name = await uploadSample(info, file);
      const media = await mediaChecks(info, name);
      setResults((prev) => [...prev, ...media]);
    } catch (e) {
      setResults((prev) => [...prev, { name: '上传测试视频', ok: false, detail: String(e), ms: 0 }]);
    } finally {
      setBusy(false);
    }
  };

  const report = formatReport(results);
  const failed = results.filter((r) => !r.ok).length;

  return (
    <div style={{ padding: 24, fontFamily: 'system-ui, sans-serif', height: '100vh', overflow: 'auto', boxSizing: 'border-box' }}>
      <Link to="/">← 返回画布</Link>
      <h2>喵剧AI · 环境诊断</h2>
      <p>本地服务端口：{info.port}</p>
      <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
        <button disabled={busy} onClick={runBasic}>
          1. 运行连接与编解码检查
        </button>
        <label>
          2. 选择一个带声音的 mp4 测试视频：
          <input
            type="file"
            accept=".mp4,.mov,.webm"
            disabled={busy}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void runMedia(file);
            }}
          />
        </label>
        <button
          disabled={results.length === 0}
          onClick={() => {
            navigator.clipboard.writeText(report).then(
              () => setCopyStatus('已复制'),
              (e: unknown) => setCopyStatus(`复制失败：${String(e)}，请手动选中下方文本复制`),
            );
          }}
        >
          复制结果
        </button>
        {copyStatus && <span>{copyStatus}</span>}
      </div>
      {results.length > 0 && (
        <p style={{ color: failed ? '#f88' : '#8f8' }}>
          {failed ? `${failed} 项失败` : `全部 ${results.length} 项通过`}
        </p>
      )}
      <pre style={{ whiteSpace: 'pre-wrap', userSelect: 'text' }}>{report}</pre>
    </div>
  );
}
```

- [ ] **Step 4: 运行测试**

Run: `pnpm --dir web exec vitest run src/diag`
Expected: PASS（4 个用例）。

- [ ] **Step 5: 验证并提交**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | tail -1
git add web/src/diag
git commit -m "fix(web): 诊断页媒体名编码、WS 提前关闭立即判失败、检查异常与剪贴板失败可见，加返回画布入口"
```

---

### Task 6: 集成验收与文档

**Files:**
- Modify: `docs/superpowers/plans/p0-carryover.md`

**Interfaces:**
- Consumes: Task 1–5 的全部产出（按 Task 2 → 3 → 5 → 4 的顺序合入同一分支）。
- Produces: 可在 Tauri 里运行的 P3a 前端；更新后的遗留事项清单。

- [ ] **Step 1: 合入并全量验证（测试单独跑，不要和 build 同时跑）**

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | grep -E "built in|error"
pnpm --dir web test 2>&1 | tail -4
pnpm test:server
pnpm test:tauri
```

Expected: 构建成功；`Test Files  162 passed (162)`、`Tests  1434 passed | 8 skipped (1442)`；Go 和 Rust 测试不受影响，照常通过。

- [ ] **Step 2: Tauri 手动验收（`pnpm dev`；第 4、9 项还要用 `pnpm build` 产出的 dmg 再测一次）**

按 sidecar 进度分三档，标明每一档**预期**能用和**预期**不能用的部分。

**A. 现在就能验（P1b 未合入，sidecar 只有 P0 的 ping/diag/agent 回显）：**
1. 启动后先显示"正在连接本地服务…"，随即显示"无法打开项目"错误页（`/api/v1/projects` 返回 404），有"重试"和"环境诊断"两个入口。**不会**新建项目。
2. 点"环境诊断"进 `/diag`，连接 3 项 + 编解码 8 项全部 PASS，选测试视频后 6 项媒体检查全部 PASS（与 P0 结果一致）；"← 返回画布"回到错误页。
3. 打开 Web Inspector（开发构建右键 → 检查元素），控制台没有 CSP 报错，没有 `fonts.googleapis.com` 请求；Network 里字体是本地的 woff2；文字是 Inter（与 P0 的系统字体对比可见差异）。

**B. P1b 合入后（项目、快照、局部 mutation、上传、媒体、缩略图接口）：**
4. 首次启动：自动新建"未命名项目"并直接进入画布。在画布上按 Cmd+R 刷新：仍停在这个项目，没有白屏（深层路由回落到 index.html，资源是绝对路径）。
5. 加一个文字节点并输入内容、加一个上传节点并上传图片、拖动节点、连线。等顶栏显示"已保存 …"后退出应用再打开：直接回到这个项目，内容和位置都在。
6. 点"所有项目"进入项目列表：能新建、重命名、复制、删除项目；点某个项目回到它的画布。删除"上一次打开的项目"后重启，打开的是最近修改的项目。
7. 对上传的图片做宫格切分（2×2）、标注保存、旋转：都能产出新节点，控制台没有 `SecurityError`/tainted canvas 报错（mediaCrossOrigin + sidecar CORS）。
8. 切到故事板视图再切回；小地图、快捷键提示、素材库面板能打开（素材库要等 P1b 的 library 接口）。
9. 3D 导演台节点能打开，人偶模型能加载（`/models/ue-mannequin-retopology.glb`），截图回传能生成新节点。

**C. P1c 合入后（生成链路）：** AI 绘图、AI 视频、去字幕、转高清的提交、轮询和结果回填；生成历史面板（`/tasks`）。在 P3b 之前，估价标签会显示 0 或估价失败的文案，属预期。
**P5 之后：** Agent 面板能连上（`ws://127.0.0.1:<端口>/api/v1/agent/ws?project_id=…&token=…`）并对话；在 P5 之前，打开 Agent 面板会连到 P0 的回显占位，不能正常对话，属预期。

- [ ] **Step 3: 更新 `docs/superpowers/plans/p0-carryover.md` 的"带入 P3"一节**

把这一节替换为：

```markdown
## 带入 P3（前端迁移，诊断页移入“设置 → 环境诊断”）

- ~~`checks.ts`：媒体名未 `encodeURIComponent`；`runBasic` 无 catch；剪贴板写入被拒未处理；WebSocket 未处理 `onclose`。~~ P3a Task 5 已修。
- ~~P3 接入参考仓 API 客户端时核对 Allow-Headers 是否够用。~~ P3a 核实：参考仓客户端只发 `Authorization`、`Content-Type`，现有 `Authorization, Content-Type, Range` 够用。
- ~~CSP：若 `index.html` 加入内联 `<style>`，Tauri 可能注入 style nonce 使 `'unsafe-inline'` 失效。~~ P3a Task 3 已在 `tauri.conf.json` 设 `dangerousDisableAssetCspModification: ["style-src"]`。
- `diag.go`：`dst.Close()` 错误被忽略、拷贝失败留半截文件；超 500MB 返回 400 而非 413；500 响应回显 `err.Error()`；注释“只覆盖不堆积”只对同扩展名成立。（Go 侧，转 P4 搬诊断页时一并处理）
- 诊断路由和 WebSocket 回显路由届时替换或收进设置页。（P3a 暂挂隐藏路由 `/diag`；转 P4/P5）
```

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/plans/p0-carryover.md
git commit -m "docs: P3a 完成后更新 P0 遗留事项"
```

---

## 自查记录

- **spec 覆盖：** F1/F3/F5/F6/F8：画布模块原样复制（Task 1），测试全部保留。F2：节点原样保留，"保存到本地"放回菜单在 P3b。F9：Task 2（所有项目、记住项目）+ Task 3（启动算法、路由）。X1/X2/X4/X7：Task 2、Task 4（X2 的惰性残留见切分说明，P3c 清理）。X3：仅顶栏积分（Task 2），其余在 P3b。E7：Task 3。D3/SEC2：Task 4 + Task 1 的 sidecarSession。S3：前端不拼媒体地址（F-7），"在访达中显示"在 P3b。F10/S10：`useIsSuperAdmin` 恒为 false（Task 4）。M8/M10：不在本计划。
- **类型一致：** `LOCAL_OWNER_TOKEN`、`sidecarToken`、`sidecarApiBaseUrl`、`sidecarWsUrl`、`saveLastProjectId`、`loadLastProjectId`、`clearLastProjectId`、`resolveStartupProject`、`UNTITLED_PROJECT_NAME`、`startApp`、`diagMediaPath` 在定义处和使用处名字、签名一致。
- **已在临时目录实测：** 每个任务的代码都按本计划的顺序实际做过一遍，`tsc --noEmit`、vitest、`vite build` 全部通过，Task 2/3/4/5 的分支合并没有冲突。
