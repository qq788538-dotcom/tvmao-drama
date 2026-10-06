# P3b + P3c 前端收尾（去计费 UI、节点菜单、未保存提示、协作残留清理）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删掉节点里的估价和积分 UI；把"保存到本地"节点放回添加节点菜单；媒体节点右键加"在访达中显示 / 在资源管理器中显示"；生成结果没保存到本地时，节点显示提示和重试按钮；清掉 P3a 留下的协作残留、运维弹窗和 `apiGuards` 放行名单。

**Architecture:** 只改 `web/`。数据存储和后端接口照搬参考仓和 P1b/P1c 已实现的 sidecar 接口，不新增后端接口。桌面端新增的前端逻辑放在 `web/src/desktop/`：`localMedia.ts` 负责判断本机媒体地址和调用 `POST /api/v1/media/reveal`，`unsavedLocal.tsx` 负责识别"未保存到本地"、调用 `POST /api/v1/tasks/{id}/repersist`、渲染角标。两者都挂在所有媒体节点共用的 `useMediaNodeMenu`，节点组件本身不改。删除类任务各带一个"源码守护"测试作为失败测试（参照 P3a 的 `noPublicHost.test.ts`），删完变绿。

**Tech Stack:** React 18.3 + Vite 5.4 + TypeScript 5.9 + vitest 2.1（jsdom 25）、AntD 5、@tanstack/react-query 5、zustand 4.5、lucide-react 0.460、axios 1、pnpm 9。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`。本计划实现第 8 节 P3 剩余部分：X3（积分和估价 UI）、F2（"保存到本地"放回菜单，"图片对比""预览"继续隐藏，见第 227 行决策记录）、S3（右键"在访达/资源管理器中显示"）、S5（"未保存到本地"和重试按钮），以及 X2、X4、F10/S10 在前端的残留清理。范围定义来自 `docs/superpowers/plans/2026-10-06-p3a-frontend-canvas-migration.md` 开头"为什么这样切 P3a"和"P3 其余子计划"两节。

**参考仓：** `/Users/james/github/huabu_studio`，commit `fea92ab`（只读）。本计划不再从参考仓复制文件。

**代码基线：** P3a 终审合入后的提交。写本计划时是分支 `p3a-t6` 的 `HEAD`（`d7052bd`），P3a 还在终审修复，行号可能继续漂移。**下文所有改动都用"锚点文本"定位，不用行号**；每个任务的 Step 0 先 `grep` 锚点，找不到就停下报告，不要猜。

**后端依据：**
- `POST /api/v1/media/reveal`，body `{"url": "<本地媒体 URL 或 media://key>"}`，成功 204；认不出的地址 400（"无法识别这个媒体"），文件不在 404（"文件不存在，可能已被删除"），打开失败 500（"无法打开所在文件夹"）。实现：`.worktrees/p1b-t12/server/internal/http/media_handlers.go` 的 `Reveal`，路由 `router.go` 的 `r.Post("/media/reveal", …)`。
- `POST /api/v1/tasks/{taskID}/repersist` → 200 `{"outputUrls": [已签名本地 URL…], "assetIds": [...]}`；404 任务不存在；409 没有需要保存的产物；502 "保存到本地失败：…"。成功时 sidecar 已经按结构改写源节点（`content` 换成本地地址、`history` 里的旧地址替换、`settings.assetId` 补上、删除 `settings.unsaved` 和 `settings.genTaskId`）。实现：`.worktrees/p1c-t9/server/internal/http/generation_repersist_handlers.go`、`server/internal/canvasgen/repersist_apply.go`；装配在 `cmd/sidecar/gen_wiring.go`。
- 节点约定：产物没能落本地时 sidecar 在快照里写 `settings.unsaved = true`、`settings.genTaskId = <任务 id>`（`canvasgen/service.go` 的 `OnGenerationResult`）。**注意：** 前端轮询的 `GET /canvas-tasks/{id}` 只返回 `status/outputUrls/assetIds`，不带 `unsaved`；个人编辑页也不连 WebSocket。所以页面开着时 store 里看不到这两个键，要等重新打开项目才会从快照读到。Task 7 的处理见"需要决定的问题"第 2 条。
- `GET /api/v1/canvas-tasks/by-node?projectId=&nodeId=` → `{"taskId": n, "status": …}`（`generation_routes.go`），用于节点上没有 `genTaskId` 时找回任务号。
- 本地媒体 URL 形如 `http://127.0.0.1:<端口>/media/<key>?token=…`，sidecar 认得任意一次启动发出的这种地址（`media_signer.go`）。

## Global Constraints

- 应用名 `喵剧AI`；目标平台 macOS 14+ 与 Windows x64。
- 只改 `web/` 内的文件，不改 `server/`、`src-tauri/`、`UPSTREAM.md`。
- 原则：数据存储和后端做法照搬参考仓，只调整 UI。不新增 sidecar 接口，不改 P1b/P1c 契约。
- 桌面端新增的前端模块只放在 `web/src/desktop/`（P3a 约定）；复制来的参考仓文件保持原路径。
- 前端不拼媒体 URL、不存 sidecar 端口（S3）；判断"是不是本机文件"只看地址形态，不比对端口。
- X3：节点和面板上不出现"积分""预计 X 积分""估算中""计算中…""预计 --"、⚡ 预扣数字、`priceNote` 单价说明。模型配置里的 `priceNote`、脚本候选里的 `credits` **类型字段保留**（sidecar 仍可能返回），只删显示。
- F2 文案：添加节点菜单"输出"组只放 `{ type: 'local-save', label: '保存到本地', icon: HardDrive, group: 'output', hint: '下载图片或视频' }`；"图片对比""预览"继续隐藏但仍注册。
- S3 文案：macOS 显示 `在访达中显示`，其他平台（Windows）显示 `在资源管理器中显示`。
- S5 文案：角标 `未保存到本地`，按钮 `重试`，成功提示 `已保存到本地`。
- 代码注释用中文；删掉功能后，引用它的注释就地改写或删除。
- `pnpm --dir web build`（`tsc --noEmit && vite build`）和 `pnpm --dir web test` 必须全部通过。测试单独跑，不要和 build 同时跑（P3a F-8：机器负载高时偶有超时）。

## Review Focus

1. **页面开着时生成结果下载失败**（没有重开项目，store 里没有 `settings.unsaved`）→ 节点也要立刻显示"未保存到本地"，点"重试"能通过 `/canvas-tasks/by-node` 找回任务号再调 repersist。（Task 7 `isUnsavedNode` 用例与"没有 genTaskId 时按节点找回任务号"用例）
2. **重试失败**（上游临时地址已过期 → 502、任务已被清理 → 404、已保存过 → 409）→ 把 sidecar 返回的中文原因提示出来，角标保留，可以再点；重试进行中按钮置灰，连点不会发第二个请求。（Task 7 "失败时提示原因"、"重试中按钮置灰"用例）
3. **内容还是上游外链时点"在访达中显示"** → 菜单项置灰并说明"还没有保存到本地"，不发请求；文件已被用户在访达里删掉 → 提示 sidecar 返回的"文件不存在，可能已被删除"。（Task 6 对应用例）
4. **Windows 上菜单文案**要是"在资源管理器中显示"（`navigator.platform` 为 `Win32`）。（Task 6 `fileManagerLabel` 与菜单用例）
5. **删掉在途登记后的拖拽落库**：拖动后松手、拖动中切走窗口（window blur）、多选拖动，都要照常 `patchCanvasNodePositions` 落点；文本节点失焦后内容照常保存。（Task 8 NodeShell "拖拽落库"一组用例；`textNodePersistence.test.tsx` 原样保留并须通过）

## 并发执行安排

```
第一批（全部并发）：
  P3b：Task 1 生图/视频/音频节点去估价 ‖ Task 2 去字幕/转高清/转白模去估价 ‖ Task 3 多角度/打光/资产生成/脚本生成去积分
        Task 5 "保存到本地"放回菜单 ‖ Task 6 媒体节点"在访达中显示"
  P3c：Task 8 协作残留清理 ‖ Task 9 运维弹窗与注释残留 ‖ Task 10 apiGuards 放行名单
第二批（各自依赖合入后开工）：
  Task 4 估价接口、合批器、priceNote 收尾   ← Task 1、2、3 已合入
  Task 7 "未保存到本地"提示与重试           ← Task 6 已合入
最后：Task 11 集成验收（串行）
```

- 每个任务在自己的 worktree 里做：`git worktree add .worktrees/p3bc-tN -b p3bc-tN <基线>`。第一批的基线是 P3a 终审合入后的提交（下文写作 `p3bc-base`）；Task 4 的基线是"Task 1、2、3 已合入"的集成提交；Task 7 的基线是"Task 6 已合入"的集成提交。
- 第一批合入顺序不限（文件互不重叠）；建议 Task 1 → 2 → 3 → 5 → 6 → 8 → 9 → 10，然后 Task 4、Task 7。
- **为什么 Task 4 必须等 Task 1–3：** Task 4 删除 `loadEstimate`、`estimateCostBatch`、`fetchCanvasPostprocessEstimate`、`priceNote.ts`、`useDebouncedValue.ts`，这些在 Task 1–3 合入前还有人用，独立 worktree 里 `tsc` 过不了。
- **为什么 Task 7 必须等 Task 6：** 两者都改 `useMediaNodeMenu.tsx` 和它的测试；Task 7 还要用 Task 6 新建的 `web/src/desktop/localMedia.ts` 里的 `isRemoteMediaUrl`。
- **共享文件归属（只有列出的任务能改，其他任务不碰）：**
  - `web/src/shared/api.ts`、`web/src/modules/canvas/editor/batchQueries.ts`、`web/src/shared/priceNote*`、`editor/hooks/useDebouncedValue.ts`、`editor/uploadPersistence.test.tsx`：只有 Task 4。
  - `editor/nodes/GenImageNode*`、`GenVideoNode*`、`GenAudioNode*`：只有 Task 1。
  - `editor/nodes/SubtitleEraseNode*`、`TieredVideoNode.tsx`、`VideoUpscaleNode.test.tsx`、`VideoWhiteModelNode.test.tsx`：只有 Task 2。
  - `editor/multiangle/MultiAnglePanel*`、`editor/lighting/LightPanel*`、`editor/nodes/script/AddAssetDialog*`、`editor/nodes/script/ScriptGenerator*`：只有 Task 3（`AddAssetDialog.test.tsx` 第二批由 Task 4 删一行 mock）。
  - `editor/registry.tsx`、`editor/registry.test.ts`：只有 Task 5。
  - `editor/nodes/useMediaNodeMenu.tsx`、`useMediaNodeMenu.test.tsx`：第一批 Task 6，第二批 Task 7。
  - `web/src/desktop/localMedia*`：只有 Task 6 创建，Task 7 只引用。`web/src/desktop/unsavedLocal*`：只有 Task 7。
  - `editor/store.ts`、`editor/NodeShell.tsx`、`NodeShell.test.tsx`、`editor/ConnectionLayer*`、`editor/nodes/TextNode.tsx`、`editor/persistence/canvasMutations.ts`、`canvasMutations.persistNode.test.ts`、`editor/interactionRegistry.ts`、`transientChannel*`、`presenceStore*`、`hooks/useNodeEditingBroadcast.ts` 及 Task 8 删除清单里的测试：只有 Task 8。
  - `editor/Sidebar.tsx`、`WorkflowImportModal.tsx`、`WorkflowExportModal.tsx`、`workflowSerializer*`、`shared/permissions.ts`、`editor/agent/AgentPanel.tsx`、`agent/MessageBubble.tsx`、`agent/types.ts`、`nodes/text/TextGenPanel.tsx`（后四个只改注释）：只有 Task 9。
  - `web/src/shared/apiGuards*`：只有 Task 10。
  - 守护测试各自独占：`desktop/noBillingUi.test.ts`（Task 4）、`desktop/noCollabResidue.test.ts`（Task 8）、`desktop/noOpsDialogs.test.ts`（Task 9）。
- **与其他阶段的潜在冲突：** P5（Agent）如果同时改 `agent/AgentPanel.tsx`、`agent/MessageBubble.tsx`、`agent/types.ts`，Task 9 只改其中几行注释，合并时以 P5 的代码为准、保留 Task 9 的注释改写即可。
- 在同一个工作区里并发时，每个任务只能 `git add` 自己 **Files** 里列出的路径。

---

## 已核实的事实（实现者不要再猜）

### F-1 估价/积分 UI 的分布（X3）

| 位置 | 估价来源 | 显示 |
|---|---|---|
| `GenImageNode.tsx` | `loadEstimate`（`batchQueries`）+ `selectedModel.priceNote` | 生成面板右侧药丸：`预计 X 积分` / `估算中...` / `预计 --` / priceNote |
| `GenVideoNode.tsx` | 同上 + `resolvePriceNote`（`shared/priceNote`） | 同上 |
| `GenAudioNode.tsx` | 同上 | 同上 |
| `SubtitleEraseNode.tsx` | `fetchCanvasPostprocessEstimate` | `预计 X 积分` / `计算中…` / `预计 --` |
| `TieredVideoNode.tsx`（转高清、转白模） | 同上（带 resolution） | 同上 |
| `multiangle/MultiAnglePanel.tsx` | `loadEstimate` + priceNote | 底栏 `⚡ 数字` |
| `lighting/LightPanel.tsx` | 同上 | 同上 |
| `nodes/script/AddAssetDialog.tsx` | `loadEstimate` | AI 生成 tab 底栏 `⚡ 数字` |
| `nodes/script/ScriptGenerator.tsx` | 风格接口返回的 `modelCandidates[].credits` | `⚡ 数字`（`credits > 0` 时；P6 的 sidecar 恒返回 0，但 UI 代码要删） |

- 涉及的测试：`GenImageNode.test.tsx`、`GenImageNode.presets.test.tsx`、`GenVideoNode.test.tsx`、`GenAudioNode.test.tsx`、`SubtitleEraseNode.test.tsx`、`VideoUpscaleNode.test.tsx`、`VideoWhiteModelNode.test.tsx`、`ScriptGenerator.test.tsx`、`AddAssetDialog.test.tsx`（新增用例）、`uploadPersistence.test.tsx`（只有一行 mock）、`shared/priceNote.test.ts`（整个删除）、`batchQueries.test.ts`（只测 `createBatchLoader`，不动）。
- 删完后 `useDebouncedValue`（`editor/hooks/useDebouncedValue.ts`）没有任何使用者：它只被上表 6 个估价查询用来防抖。
- `buildImageGenerationInputs` / `buildVideoGenerationInputs` / `buildAudioGenerationInputs` 在各节点的提交路径里还在用，**不要删 import**。
- `SubtitleEraseNode`、`TieredVideoNode` 的 `const projectId = currentProjectId();` 在提交时还要用，删估价时保留这一行。
- `MultiAnglePanel`、`LightPanel` 的 `schemaEntries`、`dynamicInputs` 在提交时还要用（`params: dynamicInputs`），只删 `estimateInputs` 之后的部分。
- 估价相关的 `api.ts` 导出：`estimateCost`、`EstimateItem`、`estimateCostBatch`、`CanvasPostprocessEstimateInput`、`CanvasPostprocessEstimate`、`fetchCanvasPostprocessEstimate`。除上表和测试外没有其他使用者（已 grep）。

### F-2 "保存到本地"节点（F2）

- `LocalSaveNode` 一直注册在 `registry.tsx` 的 `nodeComponents` 里，只是 `nodeMenuItems` 里那一行被注释掉了（连同 `HardDrive` 图标的 import）。放回菜单只需取消这行注释。
- 菜单分组标题由 `ContextMenu.tsx` 的 `GROUP_LABELS.output = '输出'` 提供，空分组会跳过标题，所以放回后"输出"标题自动出现。
- **已知问题（见"需要决定的问题"第 1 条）：** `LocalSaveNode` 和图片/视频/音频工具条的"下载"都用隐藏 `<a download target="_blank">`。按 wry 0.57 源码（`wkwebview/navigation.rs` 第 68–74 行），Tauri 没注册 `on_download` 时 macOS WebView 会直接取消下载导航；媒体地址和页面又不同源，`download` 属性可能被忽略、退化成开新窗口。因此在 Tauri 里点"下载"大概率没有反应。本计划只把节点放回菜单，不改下载实现。

### F-3 媒体节点右键菜单（S3、S5 的挂载点）

- `editor/nodes/useMediaNodeMenu.tsx` 是 10 个媒体节点共用的右键菜单和状态角标：`InputImageNode`、`AudioInputNode`、`VideoInputNode`、`GenImageNode`、`GenVideoNode`、`GenAudioNode`、`PreviewNode`、`SubtitleEraseNode`、`TieredVideoNode`（转高清、转白模）。返回 `{ onContextMenu, overlay, badge, state }`。
- `badge` 已经被所有使用者渲染（`seedanceBadgeWiring.test.ts` 守着：要么传 `NodeShell` 的 `titleExtra`，要么直接渲染 `menu.badge`），位置在节点名字那一行。所以"未保存到本地"角标并进 `badge` 就能出现在全部媒体节点上，节点组件不用改。
- 菜单项类型 `NodeMenuItem`（`nodes/NodeContextMenu.tsx`）：`{ key, label, onPick, disabled?, danger?, hint?, tip?, shortcut? }`；`hint` 渲染成按钮的 `title`；菜单项是 `<button>`，置灰时 `disabled`。现有顺序：合规校验（固定首项）→ `extraItems` → 复制节点/创建副本 → 保存到素材库 → 删除（固定末项）。新项"在访达中显示"插在"保存到素材库"和"删除"之间。
- 平台判断已有 `isMacLike()`（`nodes/standardNodeMenuItems.tsx`，`/Mac|iPhone|iPad/` 匹配 `navigator.platform || navigator.userAgent`）。测试里用 `Object.defineProperty(window.navigator, 'platform', { value, configurable: true })` 切平台（`CanvasContextMenu.test.tsx` 的 `setPlatform`）。jsdom 默认 `platform` 是空串、`userAgent` 里是 `darwin` 不含 `Mac`，所以不设平台时 `isMacLike()` 为 false。
- `AntApp.useApp()` 在 `<AntApp>` 外调用不报错，但 `message.error` 不可用；要断言提示文案的用例必须用 `<AntApp>` 包住。

### F-4 生成结果怎么回到页面（S5 的约束）

- 生成节点（`GenImageNode` 等）靠轮询 `pollCanvasTaskUntilDone` 拿到 `outputUrls[0]` 后，本地 `updateNode(content=url)` + `updateNodeSettings(history/assetId/kind)`，再 `persistNodeById`。`persistNodeById` 对基线做 diff、只发变更字段（`persistence/canvasMutations.ts`），不会删掉 sidecar 在快照里写的 `unsaved/genTaskId`，但 store 里也没有这两个键。
- 重新打开项目时快照里带 `settings.unsaved/genTaskId`。
- `updateNodeSettings(id, { k: undefined })` 会让 diff 发 `k: null`（RFC 7386 删键），与 `GenImageNode` 清 `pendingGen` 的做法一致。
- 会写回生成结果的节点类型（canvasgen sink）：`gen-image`、`gen-video`、`gen-audio`、`subtitle-erase`、`video-upscale`、`video-whitemodel`。桌面端正常情况下这些节点的结果都是本机地址；只有下载失败才会留下 `http(s)://` 上游外链。

### F-5 协作残留（X2）的引用关系

- 页面已不注册瞬态通道、不写在线名册、不建页面级 WebSocket（P3a Task 2）。以下代码都不会再执行：
  - `editor/transientChannel.ts`（拖拽瞬态帧、"正在编辑"帧发送）、`editor/presenceStore.ts`（在线名册、"xx 正在编辑"）、`editor/hooks/useNodeEditingBroadcast.ts`（文本节点编辑广播 + 在途登记）。
  - `store.ts`：远端 op（`RemoteCanvasOp`、`applyRemoteOp`、`deferredRemoteOps`、`RemoteNodeStateSink`、`PendingLocalPatchProvider`、`onInteractionEnd` 监听）和瞬态坐标覆盖层（`transientPositions`、`applyTransientPositions`、`clearTransientPositions`、`resolveNodePosition`、`TRANSIENT_*`）。
  - `editor/interactionRegistry.ts`：只为"远端 op 对交互中的节点先挂起"服务；使用者只有 `store.ts`、`NodeShell.tsx`（拖拽登记）、`useNodeEditingBroadcast.ts`。
  - `persistence/canvasMutations.ts`：`setRemoteNodeStateSink(noteRemoteNodeState)`、`setPendingLocalPatchProvider(pendingLocalPatchFor)` 两处注册，以及"在飞登记"（`inFlightNodePatches`、`registerInFlightPatch`）——它只用于保护本端字段不被远端帧覆盖。
  - `remoteOpFrame.ts` 已在 P3a 删除，残留只有 `agent/types.ts` 第 176–181 行注释里的文字引用。
- 所有引用这些符号的文件（已全量 grep）：`store.ts`、`NodeShell.tsx`、`NodeShell.test.tsx`、`ConnectionLayer.tsx`、`ConnectionLayer.test.tsx`、`nodes/TextNode.tsx`、`interactionRegistry.ts`、`presenceStore.ts(+test)`、`transientChannel.ts(+test)`、`hooks/useNodeEditingBroadcast.ts`、`store.transient.test.ts`、`store.remoteOp.test.ts`、`concurrentEditSequences.test.ts`、`textNodeEditingPresence.test.tsx`、`persistence/canvasMutations.ts`、`canvasMutations.persistNode.test.ts`（3 个用例）、`agent/types.ts`（仅注释）。
- 不在本计划清理：`agent/agentClient.ts` 的 `sendTransient` 方法和 `agent/types.ts` 的 `canvas_transient/canvas_presence` 帧类型（Agent WebSocket 线格式，归 P5）；`store.load` 的 `preserveUiState` 选项（照搬参考仓，无副作用）；`director/desk` 下参考仓自带的孤儿文件。
- 导演台的 `InspectorControls.tsx` 有一个**同名但无关**的局部函数 `beginInteraction`（undo 批处理），守护测试不能按这个名字查。

### F-6 运维弹窗与 apiGuards（X4、F10、S10）

- `Sidebar.tsx` 里 `isSuperAdmin && (...)` 包着"导出画布为 Skill JSON""从 Skill JSON 导入"两个按钮和 `WorkflowExportModal`/`WorkflowImportModal`；`useIsSuperAdmin()`（`shared/permissions.ts`）在 P3a 已恒为 `false`。`workflowSerializer.ts` 只被这两个弹窗和自己的测试使用。删掉后 `Sidebar.tsx` 的 `useState`、`FileJson`、`Workflow` import 也不再使用。
- `canvasMutations.ts` 的 `seedCanvasBaselines` 注释提到 `WorkflowImportModal`——该文件归 Task 8，由 Task 8 改写这句注释。
- `shared/apiGuards.ts` 的 `ALLOWLIST` 三条（分享克隆、抢锁 `/canvas/owner/claim`、身份自愈 `/auth/switch-context`）对应的功能在桌面端都不存在；`isApiInReadOnlyMode()` 没有任何使用者。`setApiWritesBlocked` 由 `store.setReadOnly` 调用，保留。
- ChatPanel 注释残留：`agent/AgentPanel.tsx` 文件头第 3 行、`agent/MessageBubble.tsx` 文件头第 3 行和"复制按钮"两处、`nodes/text/TextGenPanel.tsx` 模型下拉一处。

---

## 文件结构

```
web/src/
  desktop/
    localMedia.ts(+test)          Task 6：本机媒体地址判定、文件管理器文案、POST /media/reveal
    unsavedLocal.tsx(+test)       Task 7：未保存判定、repersist 重试、角标
    noBillingUi.test.ts           Task 4：守护——估价代码不再出现
    noCollabResidue.test.ts       Task 8：守护——协作残留不再出现
    noOpsDialogs.test.ts          Task 9：守护——运维弹窗不再出现
  shared/
    api.ts                        Task 4：删估价接口，改 priceNote 注释
    priceNote.ts(+test)           Task 4：删除
    apiGuards.ts(+test)           Task 10：去掉放行名单和 isApiInReadOnlyMode
    permissions.ts                Task 9：删除
  modules/canvas/editor/
    batchQueries.ts               Task 4：删 loadEstimate
    hooks/useDebouncedValue.ts    Task 4：删除
    hooks/useNodeEditingBroadcast.ts  Task 8：删除
    nodes/Gen{Image,Video,Audio}Node.tsx(+tests)  Task 1
    nodes/SubtitleEraseNode.tsx、TieredVideoNode.tsx(+tests)  Task 2
    multiangle/MultiAnglePanel.tsx(+新测试)、lighting/LightPanel.tsx(+新测试)、
    nodes/script/AddAssetDialog.tsx、ScriptGenerator.tsx(+tests)  Task 3
    registry.tsx(+新测试)         Task 5
    nodes/useMediaNodeMenu.tsx(+test)  Task 6、Task 7
    store.ts、NodeShell.tsx、ConnectionLayer.tsx、nodes/TextNode.tsx、
    persistence/canvasMutations.ts、interactionRegistry.ts（删）、
    transientChannel.ts（删）、presenceStore.ts（删）  Task 8
    Sidebar.tsx、Workflow{Import,Export}Modal.tsx（删）、workflowSerializer.ts（删）  Task 9
```

---

### Task 1: AI 绘图 / AI 视频 / AI 音频节点去掉估价（X3）

**Files:**
- Modify: `web/src/modules/canvas/editor/nodes/GenImageNode.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/GenVideoNode.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/GenAudioNode.tsx`
- Test: `web/src/modules/canvas/editor/nodes/GenImageNode.test.tsx`、`GenImageNode.presets.test.tsx`、`GenVideoNode.test.tsx`、`GenAudioNode.test.tsx`

**Interfaces:**
- Consumes: 无。
- Produces: 三个节点及其测试不再 import `loadEstimate`（`../batchQueries`）、`resolvePriceNote`（`@/shared/priceNote`）、`useDebouncedValue`（Task 4 据此删除这些导出）。

- [ ] **Step 0: 建 worktree、确认锚点**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t1 -b p3bc-t1 p3bc-base
cd .worktrees/p3bc-t1/web/src/modules/canvas/editor/nodes
grep -c "const estimateInputs = useMemo(" GenImageNode.tsx GenVideoNode.tsx GenAudioNode.tsx
grep -c "title={estimateTitle}" GenImageNode.tsx GenVideoNode.tsx GenAudioNode.tsx
```

Expected: 每个文件各输出 `1`。

- [ ] **Step 1: 写失败测试**

`GenImageNode.test.tsx`：把 `describe('GenImageNode lightbox', …)` 里下面 5 个用例整段删掉：`'未选中的节点不发起估价——预计积分标签本就随选中才显示'`、`'选中后才发起一次估价'`、`'priceNote 非空时只显示单价说明，不显示预扣/预计积分数字'`、`'无 priceNote 保持「预计 X 积分」原文案'`、`'does not re-estimate cost while only the prompt text changes'`。在原位置插入：

```tsx
  // spec X3：桌面端不计费。哪怕模型配置里带着 priceNote（单价说明），节点上也不出现
  // 任何估价/积分文案。
  it('选中后不显示估价或积分文案（X3）', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...imageModel, priceNote: '100积分/分钟' }]);
    useCanvasStore.setState({ selectedIds: new Set(['image-1']) });
    renderNode();

    expect((await screen.findAllByText('Image Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });
```

`GenVideoNode.test.tsx`：把 `describe('GenVideoNode cost estimate', () => {` 改为 `describe('GenVideoNode', () => {`；删掉其中 `'未选中的节点不发起估价——预计积分标签本就随选中才显示'` 和 `'does not re-estimate cost while only the prompt text changes'` 两个用例，在原位置插入：

```tsx
  it('选中后不显示估价或积分文案（X3）', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...videoModel, priceNote: '480p:70积分/秒|720p:150积分/秒' }]);
    renderNode();

    expect((await screen.findAllByText('Video Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });
```

`GenAudioNode.test.tsx`：在 `describe('GenAudioNode', () => {` 的 `beforeEach` 之后插入：

```tsx
  it('选中后不显示估价或积分文案（X3）', async () => {
    vi.mocked(listRuntimeModels).mockResolvedValue([{ ...audioModel, priceNote: '100积分/分钟' }, imageModel]);
    renderNode();

    expect((await screen.findAllByText('TTS Model', {}, { timeout: 3000 })).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 700));
    });
    expect(screen.queryByText(/积分|估算中|预计/)).toBeNull();
  });
```

如果 `GenAudioNode.test.tsx` 顶部的 `@testing-library/react` import 里没有 `act`，把它加上。

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/nodes/GenImageNode.test.tsx src/modules/canvas/editor/nodes/GenVideoNode.test.tsx src/modules/canvas/editor/nodes/GenAudioNode.test.tsx -t "X3"`
Expected: 3 个用例 FAIL，报错显示找到了含"积分"的元素（`100积分/分钟` 或 `70积分/秒…`）。

- [ ] **Step 3: 删 `GenImageNode.tsx` 的估价代码**

1. 删除这两行 import：

```tsx
import { useDebouncedValue } from '../hooks/useDebouncedValue';
```
```tsx
import { loadEstimate } from '../batchQueries';
```

2. 删除这一行（它只被估价的依赖数组用）：

```tsx
  const dynamicInputsFingerprint = JSON.stringify(dynamicInputs);
```

3. 删除从 `  const connectedImagesFingerprint = connectedImages.join('\n');` 开始、到 `    : '按当前模型和参数预估，实际以完成后结算为准';` 为止的整段（含首尾两行，共约 40 行：`estimateInputs`、`debouncedEstimateInputs`、`estimateQuery`、`priceNote`、`estimateLabel`、`estimateTitle`）。删完后，`const hasPrompt = …` 的下一段代码应是 `// 用户是否已经在上游连了媒体` 那段注释。

4. 删除生成面板里的估价药丸（保留它前面的 `<div className="flex-1" />`）：

```tsx
            {selectedModelId && (
              <span
                className="shrink-0 rounded-md bg-white/5 px-2 py-1 text-[11px] text-muted-foreground border border-white/10"
                title={estimateTitle}
              >
                {estimateLabel}
              </span>
            )}

```

- [ ] **Step 4: 删 `GenVideoNode.tsx` 的估价代码**

1. 删除三行 import：

```tsx
import { useDebouncedValue } from '../hooks/useDebouncedValue';
```
```tsx
import { loadEstimate } from '../batchQueries';
import { resolvePriceNote } from '@/shared/priceNote';
```

2. 删除 `  const dynamicInputsFingerprint = JSON.stringify(dynamicInputs);` 这一行。

3. 删除从 `  const estimateInputs = useMemo(` 开始、到 `    : '按当前模型和参数预估，实际以完成后结算为准';` 为止的整段（含首尾两行）。删完后，`const submitTip = …;` 的下一段代码应是 `// 用户是否已经在上游连了媒体` 那段注释。

4. 删除估价药丸（14 个空格缩进，保留前面的 `<div className="flex-1" />`）：

```tsx
              {selectedModelId && (
                <span
                  className="shrink-0 rounded-md bg-white/5 px-2 py-1 text-[11px] text-muted-foreground border border-white/10"
                  title={estimateTitle}
                >
                  {estimateLabel}
                </span>
              )}

```

- [ ] **Step 5: 删 `GenAudioNode.tsx` 的估价代码**

1. 删除两行 import：

```tsx
import { useDebouncedValue } from '../hooks/useDebouncedValue';
```
```tsx
import { loadEstimate } from '../batchQueries';
```

2. 删除 `  const dynamicInputsFingerprint = JSON.stringify(dynamicInputs);` 这一行。

3. 删除从 `  const connectedAudiosFingerprint = connectedAudios.join('\n');` 开始、到 `    : '按当前模型和参数预估，实际以完成后结算为准';` 为止的整段（含首尾两行）。删完后，`const hasPrompt = …;` 的下一行应是 `  const hasUpstreamMedia =`（中间留一个空行）。

4. 删除估价药丸（12 个空格缩进，与 Step 3 第 4 点的代码块完全相同）。

- [ ] **Step 6: 清理测试里的估价 mock**

在 `GenImageNode.test.tsx`、`GenImageNode.presets.test.tsx`、`GenVideoNode.test.tsx`、`GenAudioNode.test.tsx` 四个文件里：

1. `import { loadEstimate, loadSeedanceStatus } from '../batchQueries';` 改为 `import { loadSeedanceStatus } from '../batchQueries';`
2. `vi.mock('../batchQueries', () => ({ … }))` 里删掉 `  loadEstimate: vi.fn(),` 这一行（只留 `loadSeedanceStatus`）。
3. 删掉所有 `vi.mocked(loadEstimate).mockResolvedValue(…);` 行。
4. `GenImageNode.test.tsx` 里 `vi.mock('../batchQueries'…` 上方的注释"估价/合规状态都走合批器"改为"合规状态走合批器"；`GenVideoNode.test.tsx` 同理。

确认没有遗漏：

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t1/web/src/modules/canvas/editor/nodes
grep -n "loadEstimate\|resolvePriceNote\|useDebouncedValue\|estimateLabel\|estimateQuery\|dynamicInputsFingerprint" GenImageNode* GenVideoNode* GenAudioNode*
```

Expected: 无输出。

- [ ] **Step 7: 运行测试确认通过，并跑类型检查**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/nodes/GenImageNode.test.tsx src/modules/canvas/editor/nodes/GenImageNode.presets.test.tsx src/modules/canvas/editor/nodes/GenVideoNode.test.tsx src/modules/canvas/editor/nodes/GenAudioNode.test.tsx`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 无输出（退出码 0）。

- [ ] **Step 8: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t1
git add web/src/modules/canvas/editor/nodes/GenImageNode.tsx web/src/modules/canvas/editor/nodes/GenVideoNode.tsx web/src/modules/canvas/editor/nodes/GenAudioNode.tsx web/src/modules/canvas/editor/nodes/GenImageNode.test.tsx web/src/modules/canvas/editor/nodes/GenImageNode.presets.test.tsx web/src/modules/canvas/editor/nodes/GenVideoNode.test.tsx web/src/modules/canvas/editor/nodes/GenAudioNode.test.tsx
git commit -m "feat(web): AI 绘图/视频/音频节点去掉估价与积分文案（X3）"
```

---

### Task 2: 去字幕 / 转高清 / 转白模节点去掉估价（X3）

**Files:**
- Modify: `web/src/modules/canvas/editor/nodes/SubtitleEraseNode.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/TieredVideoNode.tsx`
- Test: `web/src/modules/canvas/editor/nodes/SubtitleEraseNode.test.tsx`、`VideoUpscaleNode.test.tsx`、`VideoWhiteModelNode.test.tsx`

**Interfaces:**
- Consumes: 无。
- Produces: 这些文件不再引用 `fetchCanvasPostprocessEstimate`、`loadEstimate`（Task 4 据此删除导出）。

- [ ] **Step 0: 建 worktree、确认锚点**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t2 -b p3bc-t2 p3bc-base
cd .worktrees/p3bc-t2/web/src/modules/canvas/editor/nodes
grep -c "const estimateQuery = useQuery({" SubtitleEraseNode.tsx TieredVideoNode.tsx
grep -c "{estimateLabel}" SubtitleEraseNode.tsx TieredVideoNode.tsx
```

Expected: 每个文件各输出 `1`。

- [ ] **Step 1: 写失败测试**

`SubtitleEraseNode.test.tsx`：

1. 删掉用例 `'E. 预估走后端权威时长——按 sourceNodeId 请求，不再经 /models/estimate 合批器'`、`'F. 回归：源视频容器 metadata 被篡改成 1 秒，显示的仍是后端算的真实预估'`、`'G. 预估未返回前显示「计算中…」，失败显示「预计 --」'` 三个用例，在原位置插入：

```tsx
  it('E. 不显示积分预估（X3）', async () => {
    seedStoreWithSource(undefined);
    renderNode();

    await screen.findByText('普通去字幕');
    await waitFor(() => expect(screen.getByRole('button', { name: '去字幕' })).not.toBeDisabled());
    expect(screen.queryByText(/积分|计算中|预计/)).toBeNull();
  });
```

`VideoUpscaleNode.test.tsx`：把用例 `'B. 预估积分来自后端（不从 <video>.duration 自己算）'` 整个替换为：

```tsx
  it('B. 不显示积分预估（X3）', async () => {
    seedStoreWithSource();
    renderNode();

    await screen.findByText('2K 超清');
    await waitFor(() => expect(screen.getByRole('button', { name: '转高清' })).not.toBeDisabled());
    expect(screen.queryByText(/积分|计算中|预计/)).toBeNull();
  });
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/nodes/SubtitleEraseNode.test.tsx src/modules/canvas/editor/nodes/VideoUpscaleNode.test.tsx -t "X3"`
Expected: 2 个用例 FAIL，找到了 `计算中…` 或 `预计 12 积分`。

- [ ] **Step 3: 删 `SubtitleEraseNode.tsx` 的估价代码**

1. `@/shared/api` 的 import 列表里删掉 `  fetchCanvasPostprocessEstimate,` 一行。

2. 把从 `  // 预估积分整份由后端算。**不要**从 <video>.duration 取时长再自己拼计费 inputs——` 开始、到 `      : \`预计 ${estimateQuery.data?.credits ?? 0} 积分\`;` 为止的整段（含首尾两行）替换为一行：

```tsx
  const projectId = currentProjectId();
```

3. 删除估价药丸（保留外层 `<div className="flex items-center gap-1.5">` 和后面的 `<div className="flex-1" />`）：

```tsx
                {modelId && (
                  <span
                    className="shrink-0 rounded-md bg-white/5 px-2 py-1 text-[11px] text-muted-foreground border border-white/10"
                    title="按当前模型和时长预估，实际以完成后结算为准"
                  >
                    {estimateLabel}
                  </span>
                )}
```

- [ ] **Step 4: 删 `TieredVideoNode.tsx` 的估价代码**

1. `@/shared/api` 的 import 列表里删掉 `  fetchCanvasPostprocessEstimate,` 一行。

2. 把从 `  // 预估整份由后端算（含按秒 × 档位）。**不要**从 <video>.duration 取时长自己拼 inputs——` 开始、到 `      : \`预计 ${estimateQuery.data?.credits ?? 0} 积分\`;` 为止的整段（含首尾两行）替换为一行：

```tsx
  const projectId = currentProjectId();
```

3. 删除估价药丸：

```tsx
                {configured && resolution && (
                  <span
                    className="shrink-0 rounded-md bg-white/5 px-2 py-1 text-[11px] text-muted-foreground border border-white/10"
                    title="按当前档位和视频真实时长预估，实际以完成后结算为准"
                  >
                    {estimateLabel}
                  </span>
                )}
```

4. 改两处注释：
   - `//   3. 预估要带 resolution（档位影响单价）。` 改为 `//   3. 提交要带 resolution（档位）。`
   - `  /** 是否显示模型下拉。只配一个模型也显示——让用户看得到扣的积分对应哪个模型。 */` 改为 `  /** 是否显示模型下拉。只配一个模型也显示——让用户看得到用的是哪个模型。 */`
   - `    // 默认第一个；后台删掉了当前选中的模型也回落第一个，避免拿白名单外的 modelId 去问价/提交。` 改为 `    // 默认第一个；后台删掉了当前选中的模型也回落第一个，避免拿白名单外的 modelId 去提交。`
   - `    // 换模型（或后台改了档位）后：新列表里有同值档位就保留——用户换模型多半是想比同一档的价；` 改为 `    // 换模型（或后台改了档位）后：新列表里有同值档位就保留——用户换模型多半还想要同一档；`

- [ ] **Step 5: 改其余测试里依赖估价的断言**

`SubtitleEraseNode.test.tsx`：
1. `@/shared/api` 的 import 删掉 `  fetchCanvasPostprocessEstimate,`；删掉 `import { loadEstimate } from '../batchQueries';`。
2. `vi.mock('@/shared/api', …)` 里删掉 `    fetchCanvasPostprocessEstimate: vi.fn(),`。
3. 删掉整段：

```tsx
// loadEstimate 仍被 mock，但只为断言「本节点再也不走这条路」——预估已改由后端算。
vi.mock('../batchQueries', () => ({
  loadEstimate: vi.fn().mockResolvedValue(3),
}));
```

4. `beforeEach` 里删掉 `vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({ credits: 3, durationMs: 3000 });` 和 `vi.mocked(loadEstimate).mockResolvedValue(3);` 两行。

`VideoUpscaleNode.test.tsx`：
1. import 和 `vi.mock` 里删掉 `fetchCanvasPostprocessEstimate`；`beforeEach` 删掉 `vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({ credits: 12, durationMs: 12000 });`。
2. 用例 A：删掉 `    // 默认第一档参与预估请求。` 和它下面整个 `await waitFor(() => expect(fetchCanvasPostprocessEstimate).toHaveBeenCalledWith({ … }));`（到 `);` 为止）。
3. 用例 C 整个替换为：

```tsx
  it('C. 换档位后提交用新档位', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 778, status: 'processing' });
    renderNode();

    await userEvent.click(await screen.findByText('4K 极清'));
    const btn = screen.getByRole('button', { name: '转高清' });
    await waitFor(() => expect(btn).not.toBeDisabled());
    await userEvent.click(btn);

    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-hd',
        resolution: '4k',
      }),
    );
  });
```

4. 用例 F：标题 `'F. 后台未配转高清模型 → 「未配置转高清模型」+ 按钮 disabled，且不问价'` 改为 `'F. 后台未配转高清模型 → 「未配置转高清模型」+ 按钮 disabled'`，删掉 `    expect(fetchCanvasPostprocessEstimate).not.toHaveBeenCalled();`。
5. 用例 I：标题里的 `按新模型重新预估、提交` 改为 `按新模型提交`；删掉 `await waitFor(() => expect(fetchCanvasPostprocessEstimate).toHaveBeenLastCalledWith({ … }));` 整段（提交断言保留）。
6. 用例 J 整个替换为：

```tsx
  it('J. 换模型且新模型没有当前档位 → 跳到新模型第一档', async () => {
    seedStoreWithSource();
    vi.mocked(submitCanvasUpscale).mockResolvedValue({ taskId: 779, status: 'processing' });
    renderNode();

    await screen.findByText('2K 超清'); // 默认 hd 的 2k，pro 没有 2k
    await pickModel('专业高清');
    expect(await screen.findByText('1080P')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: '转高清' }));
    await waitFor(() =>
      expect(submitCanvasUpscale).toHaveBeenCalledWith({
        projectId: 42,
        nodeId: 'up-1',
        sourceNodeId: 'src-1',
        modelId: 'upscale-pro',
        resolution: '1080p',
      }),
    );
  });
```

`VideoWhiteModelNode.test.tsx`：
1. import 和 `vi.mock` 里删掉 `fetchCanvasPostprocessEstimate`；`beforeEach` 删掉 `vi.mocked(fetchCanvasPostprocessEstimate).mockResolvedValue({ credits: 30, durationMs: 9000 });`。
2. 文件头注释 `// 转白模与转高清共用 TieredVideoNode，档位/预估/收起面板等共性由 VideoUpscaleNode.test.tsx 覆盖。` 改为 `// 转白模与转高清共用 TieredVideoNode，档位/收起面板等共性由 VideoUpscaleNode.test.tsx 覆盖。`
3. 第一个用例标题改为 `'档位来自 whiteModelResolutions（平铺列表）'`，删掉其中的 `await waitFor(() => expect(fetchCanvasPostprocessEstimate).toHaveBeenCalledWith({ … }));` 整段。
4. 第三个用例标题 `'只配了转高清、没配白模 → 「未配置白模模型」+ 按钮 disabled，且不问价'` 改为 `'只配了转高清、没配白模 → 「未配置白模模型」+ 按钮 disabled'`，删掉 `    expect(fetchCanvasPostprocessEstimate).not.toHaveBeenCalled();`。

确认没有遗漏：

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t2/web/src/modules/canvas/editor/nodes
grep -n "Estimate\|estimate" SubtitleEraseNode* TieredVideoNode.tsx VideoUpscaleNode.test.tsx VideoWhiteModelNode.test.tsx
```

Expected: 无输出。

- [ ] **Step 6: 运行测试确认通过，并跑类型检查**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/nodes/SubtitleEraseNode.test.tsx src/modules/canvas/editor/nodes/VideoUpscaleNode.test.tsx src/modules/canvas/editor/nodes/VideoWhiteModelNode.test.tsx`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 7: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t2
git add web/src/modules/canvas/editor/nodes/SubtitleEraseNode.tsx web/src/modules/canvas/editor/nodes/TieredVideoNode.tsx web/src/modules/canvas/editor/nodes/SubtitleEraseNode.test.tsx web/src/modules/canvas/editor/nodes/VideoUpscaleNode.test.tsx web/src/modules/canvas/editor/nodes/VideoWhiteModelNode.test.tsx
git commit -m "feat(web): 去字幕/转高清/转白模节点去掉积分预估（X3）"
```

---

### Task 3: 多角度 / 打光面板、资产 AI 生成、脚本生成去掉积分（X3）

**Files:**
- Modify: `web/src/modules/canvas/editor/multiangle/MultiAnglePanel.tsx`
- Modify: `web/src/modules/canvas/editor/lighting/LightPanel.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/script/AddAssetDialog.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/script/ScriptGenerator.tsx`
- Create: `web/src/modules/canvas/editor/multiangle/MultiAnglePanel.test.tsx`
- Create: `web/src/modules/canvas/editor/lighting/LightPanel.test.tsx`
- Test: `web/src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx`、`ScriptGenerator.test.tsx`

**Interfaces:**
- Consumes: 无。
- Produces: 这四个文件不再 import `loadEstimate`、`useDebouncedValue`。`AddAssetDialog.test.tsx` 的 `@/shared/api` mock 里留有一行 `estimateCostBatch`（红灯用），Task 4 删接口时一并删掉。

- [ ] **Step 0: 建 worktree、确认锚点**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t3 -b p3bc-t3 p3bc-base
cd .worktrees/p3bc-t3/web/src/modules/canvas/editor
grep -c "const creditsLabel = priceNote" multiangle/MultiAnglePanel.tsx lighting/LightPanel.tsx
grep -c "estimateQuery.data" nodes/script/AddAssetDialog.tsx
grep -c "credits > 0 &&" nodes/script/ScriptGenerator.tsx
```

Expected: 依次输出 `1`、`1`、`2`、`1`。

- [ ] **Step 1: 写失败测试**

新建 `web/src/modules/canvas/editor/multiangle/MultiAnglePanel.test.tsx`：

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { act, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchMultiAngleConfig, listRuntimeModels, type ModelConfig } from '@/shared/api';
import { useCanvasStore } from '../store';
import { MultiAnglePanel } from './MultiAnglePanel';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  fetchMultiAngleConfig: vi.fn(),
  listRuntimeModels: vi.fn(),
  submitCanvasTask: vi.fn(),
}));

const model: ModelConfig = {
  id: 1,
  type: 'image',
  name: '绘图模型A',
  modelId: 'img-a',
  basePath: '',
  priceNote: '100积分/张',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {},
};

describe('MultiAnglePanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchMultiAngleConfig).mockResolvedValue({ models: ['img-a'], presets: [] });
    vi.mocked(listRuntimeModels).mockResolvedValue([model]);
    useCanvasStore.setState({
      nodes: [{
        id: 'src', type: 'input-image', x: 0, y: 0, width: 260, height: 260,
        status: 'succeeded', content: 'http://127.0.0.1:5123/media/a.png', settings: {},
      }],
      connections: [],
      selectedIds: new Set<string>(),
      readOnly: false,
    });
  });

  // spec X3：底栏原来显示「⚡ 预扣积分」或 priceNote 计费说明，桌面端一律不显示。
  it('底栏不显示预扣积分或计费说明（X3）', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <AntApp>
          <MultiAnglePanel sourceNodeId="src" onClose={() => {}} />
        </AntApp>
      </QueryClientProvider>,
    );

    expect((await screen.findAllByText('绘图模型A')).length).toBeGreaterThan(0);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 500));
    });
    expect(screen.queryByText(/积分/)).toBeNull();
  });
});
```

新建 `web/src/modules/canvas/editor/lighting/LightPanel.test.tsx`：

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntApp } from 'antd';
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchLightControlConfig, listRuntimeModels, type ModelConfig } from '@/shared/api';
import { useCanvasStore } from '../store';
import { LightPanel } from './LightPanel';

vi.mock('@/shared/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/shared/api')>()),
  fetchLightControlConfig: vi.fn(),
  listRuntimeModels: vi.fn(),
  submitCanvasTask: vi.fn(),
  uploadAsset: vi.fn(),
}));

const model: ModelConfig = {
  id: 1,
  type: 'image',
  name: '绘图模型A',
  modelId: 'img-a',
  basePath: '',
  priceNote: '100积分/张',
  enabled: true,
  hidden: false,
  sortOrder: 1,
  requestTemplate: {},
  uiSchema: {},
};

describe('LightPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchLightControlConfig).mockResolvedValue({ models: ['img-a'], presets: [] });
    vi.mocked(listRuntimeModels).mockResolvedValue([model]);
    useCanvasStore.setState({
      nodes: [{
        id: 'src', type: 'input-image', x: 0, y: 0, width: 260, height: 260,
        status: 'succeeded', content: 'http://127.0.0.1:5123/media/a.png', settings: {},
      }],
      connections: [],
      selectedIds: new Set<string>(),
      readOnly: false,
    });
  });

  it('底栏不显示预扣积分或计费说明（X3）', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={queryClient}>
        <AntApp>
          <LightPanel sourceNodeId="src" onClose={() => {}} />
        </AntApp>
      </QueryClientProvider>,
    );

    expect(screen.getByText('重置参数')).toBeInTheDocument();
    await waitFor(() => expect(listRuntimeModels).toHaveBeenCalled());
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 500));
    });
    expect(screen.queryByText(/积分/)).toBeNull();
  });
});
```

`AddAssetDialog.test.tsx`：
1. 顶部 `vi.mock('@/shared/api', …)` 的对象里加一行（让旧代码在红灯阶段确实拿到一个估价数字）：

```tsx
  estimateCostBatch: vi.fn().mockResolvedValue({ total: 5, items: [5] }),
```

2. 在 `describe('AddAssetDialog', () => {` 里末尾加用例：

```tsx
  it('AI 生成 tab 不显示 ⚡ 积分估价（X3）', async () => {
    renderDialog();
    fireEvent.click(screen.getByTestId('add-asset-tab-ai'));
    fireEvent.change(screen.getByTestId('asset-gen-prompt'), { target: { value: '赛博朋克修理铺' } });
    await new Promise((resolve) => setTimeout(resolve, 600));
    expect(screen.queryByText(/⚡/)).toBeNull();
  });
```

`ScriptGenerator.test.tsx`：
1. 用例 `'空输入禁用提交；填剧本后提交任务并把 taskId 落 settings'` 里 `    expect(screen.getByText('30')).toBeInTheDocument();` 改为 `    expect(screen.queryByText('30')).toBeNull();`。
2. 用例 `'切换模型候选后 ⚡ 积分与提交 payload 的 model 同步更新'`：标题改为 `'切换模型候选后提交 payload 的 model 同步更新，且不显示积分'`；其中 `    expect(screen.getByText('30')).toBeInTheDocument();` 改为 `    expect(screen.queryByText('30')).toBeNull();`；`    await waitFor(() => expect(screen.getByText('50')).toBeInTheDocument());` 改为：

```tsx
    await waitFor(() => expect(modelSelector).toHaveTextContent('深度版'));
    expect(screen.queryByText('50')).toBeNull();
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/multiangle/MultiAnglePanel.test.tsx src/modules/canvas/editor/lighting/LightPanel.test.tsx src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx src/modules/canvas/editor/nodes/script/ScriptGenerator.test.tsx`
Expected: MultiAnglePanel、LightPanel 的 X3 用例 FAIL（找到 `100积分/张`）；AddAssetDialog 的 X3 用例 FAIL（找到 `⚡ 5`）；ScriptGenerator 两个用例 FAIL（找到 `30`）。

- [ ] **Step 3: 删 `MultiAnglePanel.tsx` 的积分代码**

1. `import { ArrowUp, RotateCcw, X, Zap } from 'lucide-react';` 改为 `import { ArrowUp, RotateCcw, X } from 'lucide-react';`。
2. 删除两行 import：`import { loadEstimate } from '../batchQueries';`、`import { useDebouncedValue } from '../hooks/useDebouncedValue';`。
3. `  // 模型显示名/uiSchema/priceNote 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。` 改为 `  // 模型显示名/uiSchema 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。`
4. 把从 `  // ── 预扣积分：与 AI 绘图节点同一套预估（同 buildDynamicInputs 默认值 +` 开始、到 `      : String(estimateQuery.data ?? 0);` 为止的整段替换为：

```tsx
  // ── 动态入参：与 AI 绘图节点同一套默认值（buildDynamicInputs），提交时用。──
  const schemaEntries = useMemo(
    () =>
      Object.entries(selectedModel?.uiSchema ?? {}).filter(([, v]) => v != null) as Array<
        [string, UISchemaField]
      >,
    [selectedModel],
  );
  const dynamicInputs = useMemo(() => buildDynamicInputs(schemaEntries, undefined), [schemaEntries]);
```

5. `      {/* 底部：重置 + 模型下拉（左）；⚡预扣积分 + 圆形↑提交（右，对标 liblib） */}` 改为 `      {/* 底部：重置 + 模型下拉（左）；圆形↑提交（右，对标 liblib） */}`。
6. 删除：

```tsx
        <span
          className="flex items-center gap-1 text-sm text-muted-foreground"
          title={priceNote ? '按实际结算，显示为计费说明' : '本次生成预扣的积分'}
        >
          <Zap size={14} />
          {creditsLabel}
        </span>
```

- [ ] **Step 4: 删 `LightPanel.tsx` 的积分代码**

1. `import { ArrowUp, Plus, RotateCcw, X, Zap } from 'lucide-react';` 改为 `import { ArrowUp, Plus, RotateCcw, X } from 'lucide-react';`。
2. 删除两行 import：`import { loadEstimate } from '../batchQueries';`、`import { useDebouncedValue } from '../hooks/useDebouncedValue';`。
3. `  // 模型 uiSchema/priceNote 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。` 改为 `  // 模型 uiSchema 从模型目录反查（与 GenImageNode 同 queryKey 共享缓存）。`
4. 把从 `  // ── 预扣积分：与 AI 绘图节点同一套预估（buildDynamicInputs 默认值 +` 开始、到 `      : String(estimateQuery.data ?? 0);` 为止的整段替换为与 Step 3 第 4 点完全相同的代码块。
5. `      {/* 底部：重置（左）；⚡预扣积分 + 圆形↑提交（右，对标 liblib） */}` 改为 `      {/* 底部：重置（左）；圆形↑提交（右，对标 liblib） */}`。
6. 删除与 Step 3 第 6 点完全相同的 `<span>…{creditsLabel}</span>` 代码块。

- [ ] **Step 5: 删 `AddAssetDialog.tsx` 的估价代码**

1. 删除两行 import：`import { loadEstimate } from '../../batchQueries';`、`import { useDebouncedValue } from '../../hooks/useDebouncedValue';`（`buildImageGenerationInputs` 提交时还在用，保留）。
2. 删除：

```tsx
  const debouncedPrompt = useDebouncedValue(prompt, 400);
  const estimateQuery = useQuery({
    queryKey: ['script-asset-gen-estimate', effectiveModelId, debouncedPrompt, dynamicInputs],
    enabled: !!effectiveModelId,
    queryFn: () => loadEstimate({
      modelId: effectiveModelId,
      inputs: buildImageGenerationInputs({ prompt: debouncedPrompt, fallbackPrompt: '', dynamicInputs, connectedImages: [] }),
    }),
  });

```

3. 删除：

```tsx
                {typeof estimateQuery.data === 'number' && (
                  <span className="text-xs text-muted-foreground">⚡ {estimateQuery.data}</span>
                )}
```

- [ ] **Step 6: 删 `ScriptGenerator.tsx` 的积分显示**

1. `import { ArrowUp, Loader2, Zap } from 'lucide-react';` 改为 `import { ArrowUp, Loader2 } from 'lucide-react';`。
2. 删除 `  const credits = selectedModel?.credits ?? 0;` 这一行。
3. `        {/* 选项只给模型名:右侧 ⚡ 已显示所选模型的积分,再挂到每个选项后面是重复信息。 */}` 改为 `        {/* 选项只给模型名。 */}`。
4. 删除：

```tsx
            {credits > 0 && (
              <span className="flex items-center gap-0.5 text-xs text-muted-foreground" title="预冻结积分，成功结算失败退回">
                <Zap size={12} />
                {credits}
              </span>
            )}
```

确认没有遗漏：

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t3/web/src/modules/canvas/editor
grep -n "loadEstimate\|useDebouncedValue\|creditsLabel\|estimateQuery\|Zap\|credits" multiangle/MultiAnglePanel.tsx lighting/LightPanel.tsx nodes/script/AddAssetDialog.tsx nodes/script/ScriptGenerator.tsx
```

Expected: 无输出。

- [ ] **Step 7: 运行测试确认通过，并跑类型检查**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/multiangle src/modules/canvas/editor/lighting src/modules/canvas/editor/nodes/script`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 8: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t3
git add web/src/modules/canvas/editor/multiangle web/src/modules/canvas/editor/lighting web/src/modules/canvas/editor/nodes/script/AddAssetDialog.tsx web/src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx web/src/modules/canvas/editor/nodes/script/ScriptGenerator.tsx web/src/modules/canvas/editor/nodes/script/ScriptGenerator.test.tsx
git commit -m "feat(web): 多角度/打光面板、资产 AI 生成、脚本生成去掉积分显示（X3）"
```

---

### Task 4: 估价接口、合批器、priceNote 收尾（X3，第二批）

**Files:**
- Create: `web/src/desktop/noBillingUi.test.ts`
- Modify: `web/src/shared/api.ts`
- Modify: `web/src/modules/canvas/editor/batchQueries.ts`
- Delete: `web/src/shared/priceNote.ts`、`web/src/shared/priceNote.test.ts`、`web/src/modules/canvas/editor/hooks/useDebouncedValue.ts`
- Modify（删一行 mock）: `web/src/modules/canvas/editor/uploadPersistence.test.tsx`、`web/src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx`

**Interfaces:**
- Consumes: Task 1、2、3 已合入（上述导出没有使用者）。
- Produces: `api.ts` 不再导出 `estimateCost`、`EstimateItem`、`estimateCostBatch`、`CanvasPostprocessEstimateInput`、`CanvasPostprocessEstimate`、`fetchCanvasPostprocessEstimate`；`batchQueries.ts` 只导出 `createBatchLoader`、`loadSeedanceStatus`。`ModelConfig.priceNote`、`CanvasScriptModelCandidate.credits` 类型字段保留。

- [ ] **Step 0: 建 worktree（基线 = Task 1、2、3 已合入的集成提交）、确认前置**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t4 -b p3bc-t4 <Task1-3 已合入的集成提交>
cd .worktrees/p3bc-t4/web/src
grep -rln "loadEstimate\|useDebouncedValue\|resolvePriceNote\|fetchCanvasPostprocessEstimate" --include="*.ts" --include="*.tsx" . | grep -v "\.test\."
```

Expected: 只列出 `shared/api.ts`、`shared/priceNote.ts`、`modules/canvas/editor/batchQueries.ts`、`modules/canvas/editor/hooks/useDebouncedValue.ts` 四个文件。多出别的文件说明 Task 1–3 没合全，停下。

- [ ] **Step 1: 写失败的守护测试**

新建 `web/src/desktop/noBillingUi.test.ts`：

```ts
// 守护：spec X3 删除了积分计费和估价 UI。估价接口、估价合批器、单价说明解析都已删除，
// 以后从参考仓同步代码时如果把它们带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = ['shared/priceNote.ts', 'modules/canvas/editor/hooks/useDebouncedValue.ts'];

const FORBIDDEN =
  /\b(loadEstimate|estimateCost|estimateCostBatch|EstimateItem|fetchCanvasPostprocessEstimate|CanvasPostprocessEstimate\w*|resolvePriceNote|useDebouncedValue)\b|['"]@\/shared\/priceNote['"]/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

// 去掉注释再查：说明性文字里提到旧名字不算残留。
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

describe('估价与积分代码已删除（X3）', () => {
  it('相关文件不存在', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用估价接口、合批器和单价说明', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/desktop/noBillingUi.test.ts`
Expected: 2 个用例 FAIL；第一个列出两个文件，第二个列出 `shared/api.ts`、`batchQueries.ts` 等。

- [ ] **Step 3: 删 `api.ts` 的估价接口**

1. 删除从 `// estimateCost 预计扣费：后端按模型计费配置(billingConfig)对这组 inputs 算预冻结积分，` 开始、到 `estimateCostBatch` 函数结束（`  return { total: data.total ?? 0, items: data.items ?? [] };` 下一行的 `}`）为止的整段，以及它后面多出的一个空行。
2. 删除从 `// ---------- 后处理预估积分 ----------` 开始、到 `fetchCanvasPostprocessEstimate` 函数结束（`  return data;` 下一行的 `}`）为止的整段，以及它后面多出的一个空行。删完后 `getCanvasTask` 函数前面应只有一个空行。
3. `ModelConfig` 里 `priceNote` 上方两行注释：

```ts
  // 按上游响应实际用量结算的模型的单价说明（如「100积分/分钟」）。这类模型
  // estimate 给的是预冻结上限而非实价——非空时把「预计 X 积分」改成「预扣 X 积分 · 文案，按实际结算」。
```

替换为：

```ts
  // 参考仓的单价说明。桌面端不计费（spec X3），界面不显示；字段保留只为兼容 sidecar 返回。
```

4. `CanvasScriptModelCandidate` 里 `credits` 上方的注释 `  /** 该模型一次执行的预冻结积分（后端按模型中心计费实时算，与扣费同源） */` 替换为 `  /** 参考仓的预冻结积分。桌面端 sidecar 恒为 0、界面不显示（spec X3），字段保留只为兼容。 */`。
5. 脚本节点小节注释 `// 任务恒异步：提交冻结积分 → 轮询 → succeeded 拿 candidate.json 原文自行导入。` 改为 `// 任务恒异步：提交 → 轮询 → succeeded 拿 candidate.json 原文自行导入。`

- [ ] **Step 4: 删 `batchQueries.ts` 的估价合批**

1. 文件开头的 import 替换为：

```ts
import { fetchSeedanceStatus, type SeedanceAssetState } from '@/shared/api';
```

2. 头部注释第一句 `// 画布合批加载器。大画布上每个节点各自的估价 / 合规状态查询本来是「一个节点一个` 改为 `// 画布合批加载器。大画布上每个节点各自的合规状态查询本来是「一个节点一个`。
3. 删除从 `// modelId + inputs 指纹：同模型同 inputs 的节点共用一次估算。` 开始、到 `loadEstimate` 定义结束（`  { keyOf: estimateKey },` 下一行的 `);`）为止的整段，以及它后面多出的一个空行。

- [ ] **Step 5: 删除文件、清理测试里的残留 mock**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t4
git rm web/src/shared/priceNote.ts web/src/shared/priceNote.test.ts web/src/modules/canvas/editor/hooks/useDebouncedValue.ts
```

- `web/src/modules/canvas/editor/uploadPersistence.test.tsx`：`vi.mock('./batchQueries', () => ({ … }))` 里删掉 `  loadEstimate: vi.fn(),` 一行。
- `web/src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx`：`vi.mock('@/shared/api', …)` 里删掉 `  estimateCostBatch: vi.fn().mockResolvedValue({ total: 5, items: [5] }),` 一行。

确认测试里也没有残留：

```bash
grep -rn "loadEstimate\|estimateCost\|fetchCanvasPostprocessEstimate\|priceNote'" web/src --include="*.test.ts" --include="*.test.tsx"
```

Expected: 无输出（测试里的 `priceNote: '100积分/…'` 是模型字段，带引号的模块路径 `priceNote'` 不应出现）。

- [ ] **Step 6: 运行测试确认通过，跑类型检查和全量测试**

Run: `pnpm --dir web exec vitest run src/desktop/noBillingUi.test.ts`
Expected: PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

Run: `pnpm --dir web test 2>&1 | tail -4`
Expected: `Test Files  N passed (N)`，没有 failed。

- [ ] **Step 7: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t4
git add web/src/desktop/noBillingUi.test.ts web/src/shared/api.ts web/src/modules/canvas/editor/batchQueries.ts web/src/modules/canvas/editor/uploadPersistence.test.tsx web/src/modules/canvas/editor/nodes/script/AddAssetDialog.test.tsx
git commit -m "refactor(web): 删除估价接口、估价合批器、priceNote 解析与 useDebouncedValue，加 X3 守护测试"
```

---

### Task 5: "保存到本地"放回添加节点菜单（F2）

**Files:**
- Modify: `web/src/modules/canvas/editor/registry.tsx`
- Create: `web/src/modules/canvas/editor/registry.test.ts`

**Interfaces:**
- Consumes: 无。
- Produces: `nodeMenuItems` 含 `{ type: 'local-save', label: '保存到本地', icon: HardDrive, group: 'output', hint: '下载图片或视频' }`。

- [ ] **Step 0: 建 worktree**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t5 -b p3bc-t5 p3bc-base
grep -n "// { type: 'local-save'" .worktrees/p3bc-t5/web/src/modules/canvas/editor/registry.tsx
```

Expected: 输出 1 行。

- [ ] **Step 1: 写失败测试**

新建 `web/src/modules/canvas/editor/registry.test.ts`：

```ts
import { describe, expect, it } from 'vitest';
import { nodeComponents, nodeMenuItems } from './registry';

// spec F2（2026-09-30 决策）：「保存到本地」放回右键菜单，「图片对比」「预览」继续隐藏。
describe('添加节点菜单（F2）', () => {
  it('「保存到本地」在菜单里，归「输出」组', () => {
    expect(nodeMenuItems.find((m) => m.type === 'local-save')).toMatchObject({
      label: '保存到本地',
      group: 'output',
      hint: '下载图片或视频',
    });
  });

  it('「图片对比」「预览」不在菜单里，但仍注册（存量项目里的这两种节点照常渲染）', () => {
    const types = nodeMenuItems.map((m) => m.type);
    expect(types).not.toContain('image-compare');
    expect(types).not.toContain('preview');
    expect(nodeComponents['image-compare']).toBeTruthy();
    expect(nodeComponents.preview).toBeTruthy();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/registry.test.ts`
Expected: 第一个用例 FAIL（`toMatchObject` 收到 `undefined`），第二个 PASS。

- [ ] **Step 3: 放回菜单项**

`registry.tsx` 的 lucide import 整段替换为：

```tsx
import {
  AudioLines,
  Camera,
  Clapperboard,
  Eye,
  HardDrive,
  Sparkles,
  // Split 只被下面隐藏的「图片对比」菜单项引用，恢复时一起放开。
  // Split,
  Type,
  Upload,
  Video,
} from 'lucide-react';
```

`nodeMenuItems` 末尾那段注释和三行注释掉的菜单项整段替换为：

```tsx
  // 「输出」组：「保存到本地」按 spec F2 放回菜单；「图片对比」「预览」按用户要求继续
  // 隐藏——只是不出现在菜单里，不是移除：仍注册在 nodeComponents，存量项目里的这些
  // 节点照常渲染。要恢复显示，取消对应行的注释即可。
  // { type: 'image-compare', label: '图片对比', icon: Split, group: 'output', hint: '滑动对比两张图片' },
  { type: 'local-save', label: '保存到本地', icon: HardDrive, group: 'output', hint: '下载图片或视频' },
  // { type: 'preview', label: '预览', icon: Eye, group: 'output', hint: '查看图片或视频' },
```

- [ ] **Step 4: 运行测试确认通过**

Run: `pnpm --dir web exec vitest run src/modules/canvas/editor/registry.test.ts`
Expected: 2 个用例 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 5: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t5
git add web/src/modules/canvas/editor/registry.tsx web/src/modules/canvas/editor/registry.test.ts
git commit -m "feat(web): 「保存到本地」节点放回添加节点菜单（F2）"
```

---

### Task 6: 媒体节点右键"在访达中显示 / 在资源管理器中显示"（S3）

**Files:**
- Create: `web/src/desktop/localMedia.ts`
- Create: `web/src/desktop/localMedia.test.ts`
- Modify: `web/src/modules/canvas/editor/nodes/useMediaNodeMenu.tsx`
- Test: `web/src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx`

**Interfaces:**
- Consumes: `api`（`@/shared/api`，axios 实例，baseURL 由拦截器设为 sidecar 的 `/api/v1`）；`isMacLike()`（`@/modules/canvas/editor/nodes/standardNodeMenuItems`）。
- Produces（`web/src/desktop/localMedia.ts`）：
  - `isLocalMediaUrl(url: string | undefined | null): boolean`
  - `isRemoteMediaUrl(url: string | undefined | null): boolean`（Task 7 用）
  - `fileManagerLabel(): string`
  - `revealInFileManager(url: string): Promise<void>`
  - `revealErrorMessage(err: unknown): string`
  - 媒体菜单新增 `key: 'reveal'` 菜单项，位于"保存到素材库"之后、"删除"之前。

- [ ] **Step 0: 建 worktree、确认锚点**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t6 -b p3bc-t6 p3bc-base
cd .worktrees/p3bc-t6/web/src/modules/canvas/editor/nodes
grep -n "key: 'save'," useMediaNodeMenu.tsx
grep -n "export function isMacLike" standardNodeMenuItems.tsx
```

Expected: 各输出 1 行。

- [ ] **Step 1: 写失败测试**

新建 `web/src/desktop/localMedia.test.ts`：

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/shared/api';
import {
  fileManagerLabel,
  isLocalMediaUrl,
  isRemoteMediaUrl,
  revealErrorMessage,
  revealInFileManager,
} from './localMedia';

function setPlatform(value: string) {
  Object.defineProperty(window.navigator, 'platform', { value, configurable: true });
}

afterEach(() => {
  setPlatform('');
  vi.restoreAllMocks();
});

describe('本机媒体地址判定', () => {
  it('sidecar 本地媒体地址和 media:// 都算本机文件，端口不限', () => {
    expect(isLocalMediaUrl('http://127.0.0.1:5123/media/u/1/a.png?token=t')).toBe(true);
    expect(isLocalMediaUrl('http://127.0.0.1:61000/media/k')).toBe(true);
    expect(isLocalMediaUrl('media://u/1/a.png')).toBe(true);
  });

  it('外链、接口地址、blob、空值都不算', () => {
    expect(isLocalMediaUrl('https://cdn.example.com/a.png')).toBe(false);
    expect(isLocalMediaUrl('http://127.0.0.1:5123/api/v1/projects')).toBe(false);
    expect(isLocalMediaUrl('blob:http://localhost/1')).toBe(false);
    expect(isLocalMediaUrl(undefined)).toBe(false);
    expect(isLocalMediaUrl('')).toBe(false);
  });

  it('isRemoteMediaUrl 只认 http(s) 外链', () => {
    expect(isRemoteMediaUrl('https://cdn.example.com/a.png')).toBe(true);
    expect(isRemoteMediaUrl('HTTP://cdn.example.com/a.png')).toBe(true);
    expect(isRemoteMediaUrl('http://127.0.0.1:5123/media/k')).toBe(false);
    expect(isRemoteMediaUrl('media://k')).toBe(false);
    expect(isRemoteMediaUrl('data:image/png;base64,AAAA')).toBe(false);
    expect(isRemoteMediaUrl(undefined)).toBe(false);
  });
});

describe('fileManagerLabel', () => {
  it('macOS 叫「在访达中显示」', () => {
    setPlatform('MacIntel');
    expect(fileManagerLabel()).toBe('在访达中显示');
  });

  it('Windows 叫「在资源管理器中显示」', () => {
    setPlatform('Win32');
    expect(fileManagerLabel()).toBe('在资源管理器中显示');
  });
});

describe('revealInFileManager', () => {
  it('把媒体地址原样 POST 给 /media/reveal', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ status: 204, data: '' });
    await revealInFileManager('http://127.0.0.1:5123/media/k?token=t');
    expect(post).toHaveBeenCalledWith('/media/reveal', { url: 'http://127.0.0.1:5123/media/k?token=t' });
  });

  it('revealErrorMessage 优先取 sidecar 返回的中文原因', () => {
    expect(revealErrorMessage({ response: { data: { error: '文件不存在，可能已被删除' } } })).toBe(
      '文件不存在，可能已被删除',
    );
    expect(revealErrorMessage(new Error('Network Error'))).toBe('Network Error');
    expect(revealErrorMessage(undefined)).toBe('无法打开所在文件夹');
  });
});
```

`useMediaNodeMenu.test.tsx`：

1. 在文件顶部 `import { useCanvasStore } from '../store';` 下面加：

```tsx
import { App as AntApp } from 'antd';
```

2. 在 `vi.mock('../batchQueries', …)` 那段下面加：

```tsx
// 「在访达中显示」最终由 sidecar 打开文件夹；这里只验证菜单把哪个地址交了出去。
const revealMock = vi.fn();
vi.mock('@/desktop/localMedia', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/desktop/localMedia')>()),
  revealInFileManager: (...args: unknown[]) => revealMock(...args),
}));

function setPlatform(value: string) {
  Object.defineProperty(window.navigator, 'platform', { value, configurable: true });
}

function renderProbeInApp(props: { url?: string; kind: 'image' | 'video' | 'audio'; nodeId: string }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <AntApp>
        <Probe {...props} />
      </AntApp>
    </QueryClientProvider>,
  );
}
```

3. 文件末尾加：

```tsx
describe('useMediaNodeMenu 在访达中显示（S3）', () => {
  const LOCAL = 'http://127.0.0.1:5123/media/u/1/a.png?token=t';

  beforeEach(() => {
    revealMock.mockReset();
    revealMock.mockResolvedValue(undefined);
    setPlatform('MacIntel');
    useCanvasStore.setState({
      nodes: [],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
  });

  afterEach(() => setPlatform(''));

  it('本机文件：macOS 显示「在访达中显示」，点击把节点的媒体地址交给 sidecar 并收起菜单', async () => {
    renderProbeInApp({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    await userEvent.click(screen.getByText('在访达中显示'));
    expect(revealMock).toHaveBeenCalledWith(LOCAL);
    expect(screen.queryByText('在访达中显示')).toBeNull();
  });

  it('Windows 上叫「在资源管理器中显示」', () => {
    setPlatform('Win32');
    renderProbe({ url: LOCAL, kind: 'video', nodeId: 'n-1' });
    openMenu();
    expect(screen.getByText('在资源管理器中显示')).toBeInTheDocument();
  });

  it('内容还是上游外链（未保存到本地）：置灰并说明原因，不发请求', async () => {
    renderProbe({ url: 'https://upstream.example.com/out.png', kind: 'image', nodeId: 'n-1' });
    openMenu();
    const item = screen.getByText('在访达中显示').closest('button')!;
    expect(item).toBeDisabled();
    expect(item.getAttribute('title')).toContain('还没有保存到本地');
    await userEvent.click(item);
    expect(revealMock).not.toHaveBeenCalled();
  });

  it('没有内容时置灰', () => {
    renderProbe({ url: undefined, kind: 'image', nodeId: 'n-1' });
    openMenu();
    expect(screen.getByText('在访达中显示').closest('button')!).toBeDisabled();
  });

  it('sidecar 报错时把原因提示给用户', async () => {
    revealMock.mockRejectedValue({ response: { data: { error: '文件不存在，可能已被删除' } } });
    renderProbeInApp({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    await userEvent.click(screen.getByText('在访达中显示'));
    expect(await screen.findByText('文件不存在，可能已被删除')).toBeInTheDocument();
  });

  it('「在访达中显示」排在「保存到素材库」之后、「删除」之前', () => {
    renderProbe({ url: LOCAL, kind: 'image', nodeId: 'n-1' });
    openMenu();
    const labels = screen.getAllByRole('button').map((b) => b.textContent ?? '');
    const save = labels.findIndex((t) => t.includes('保存到素材库'));
    const reveal = labels.findIndex((t) => t.includes('在访达中显示'));
    const del = labels.findIndex((t) => t.includes('删除'));
    expect(save).toBeGreaterThanOrEqual(0);
    expect(reveal).toBe(save + 1);
    expect(del).toBe(reveal + 1);
  });
});
```

如果顶部 `vitest` 的 import 里没有 `afterEach`，把它加上。

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/desktop/localMedia.test.ts src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx`
Expected: `localMedia.test.ts` 因 `Failed to resolve import "./localMedia"` 整体 FAIL；`useMediaNodeMenu.test.tsx` 同样因 `@/desktop/localMedia` 解析失败而 FAIL。

- [ ] **Step 3: 写 `web/src/desktop/localMedia.ts`**

```ts
// 本机媒体（sidecar 数据目录里的文件）的小工具：判断一个媒体地址是不是本机文件，
// 以及节点右键「在访达中显示 / 在资源管理器中显示」（spec S3）。
//
// 真实路径只在 sidecar 里换算：前端把节点上的媒体地址原样交给 POST /media/reveal，
// sidecar 解析出 object key、找到文件、调系统文件管理器选中它（P1b Task 8 的设计决定：
// 壳不加权限，WebView 拿不到打开任意路径的能力）。
import { api } from '@/shared/api';
import { isMacLike } from '@/modules/canvas/editor/nodes/standardNodeMenuItems';

// sidecar 出口的本地媒体地址：http://127.0.0.1:<端口>/media/<key>?token=…（P1b MediaSigner）。
// 端口每次启动都不同，sidecar 认得任意一次启动发出的这种地址，所以这里不比对端口。
const LOCAL_MEDIA_URL = /^http:\/\/127\.0\.0\.1:\d+\/media\//;

export function isLocalMediaUrl(url: string | undefined | null): boolean {
  if (!url) return false;
  return url.startsWith('media://') || LOCAL_MEDIA_URL.test(url);
}

/** 上游外链。生成结果下载到本机失败（重试 3 次）时，节点里留下的就是它（spec S5）。 */
export function isRemoteMediaUrl(url: string | undefined | null): boolean {
  if (!url) return false;
  return /^https?:\/\//i.test(url) && !isLocalMediaUrl(url);
}

/** 菜单文案按平台切换：macOS 叫访达，Windows 叫资源管理器。每次现算，测试才能切平台。 */
export function fileManagerLabel(): string {
  return isMacLike() ? '在访达中显示' : '在资源管理器中显示';
}

export async function revealInFileManager(url: string): Promise<void> {
  await api.post('/media/reveal', { url });
}

export function revealErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string } | undefined;
  return e?.response?.data?.error || e?.message || '无法打开所在文件夹';
}
```

- [ ] **Step 4: 在 `useMediaNodeMenu.tsx` 加菜单项**

1. 在 `import { SEEDANCE_REQUIREMENTS, checkSeedanceDuration } from './seedanceRules';` 下面加：

```tsx
import {
  fileManagerLabel,
  isLocalMediaUrl,
  revealErrorMessage,
  revealInFileManager,
} from '@/desktop/localMedia';
```

2. 在 `const handleSave = async (folderId: number, name: string) => {` 之前插入：

```tsx
  // 桌面端（spec S3）：本机文件在系统文件管理器里定位出来。内容还是上游外链（生成结果
  // 没保存到本地）时没有本机文件可定位，直接置灰。
  const canReveal = isLocalMediaUrl(url);
  const reveal = async () => {
    if (!url) return;
    try {
      await revealInFileManager(url);
    } catch (e) {
      message.error(revealErrorMessage(e));
    }
  };

```

3. 在 `items` 数组里，`key: 'save'` 那一项（以 `      hint: canAct ? undefined : '还没有内容可保存',` 和 `    },` 结束）之后、`    // 删除固定放最后一项` 注释之前插入：

```tsx
    {
      key: 'reveal',
      label: fileManagerLabel(),
      onPick: () => {
        setMenuPos(null);
        void reveal();
      },
      disabled: !canReveal,
      hint: !url ? '还没有内容' : canReveal ? undefined : '还没有保存到本地，无法在文件夹中显示',
    },
```

- [ ] **Step 5: 运行测试确认通过**

Run: `pnpm --dir web exec vitest run src/desktop/localMedia.test.ts src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx src/modules/canvas/editor/nodes/seedanceBadgeWiring.test.ts`
Expected: 全部 PASS（原有"「删除」是菜单最后一项""合规校验固定是菜单首项"等用例照样通过）。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 6: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t6
git add web/src/desktop/localMedia.ts web/src/desktop/localMedia.test.ts web/src/modules/canvas/editor/nodes/useMediaNodeMenu.tsx web/src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx
git commit -m "feat(web): 媒体节点右键「在访达中显示 / 在资源管理器中显示」（S3）"
```

---

### Task 7: 生成结果"未保存到本地"提示与重试（S5，第二批）

**Files:**
- Create: `web/src/desktop/unsavedLocal.tsx`
- Create: `web/src/desktop/unsavedLocal.test.tsx`
- Modify: `web/src/modules/canvas/editor/nodes/useMediaNodeMenu.tsx`
- Test: `web/src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx`

**Interfaces:**
- Consumes: Task 6 的 `isRemoteMediaUrl(url)`（`@/desktop/localMedia`）；`api`、`currentProjectId(): number | undefined`（`@/shared/api`）；`useCanvasStore`（`updateNode`、`updateNodeSettings`）；`persistNodeById(nodeID: string): void`（`persistence/canvasMutations`）。
- Produces（`web/src/desktop/unsavedLocal.tsx`）：
  - `isUnsavedNode(node: CanvasNode | undefined): boolean`
  - `replaceHistoryUrl(history: unknown, oldUrl: string, newUrl: string): unknown`
  - `interface RepersistResult { outputUrls: string[]; assetIds: number[] }`
  - `repersistTask(taskId: number): Promise<RepersistResult>`
  - `retrySaveToLocal(nodeId: string): Promise<void>`（失败时 throw，错误对象原样上抛）
  - `retryErrorMessage(err: unknown): string`
  - `useUnsavedLocalBadge(nodeId: string): ReactNode | null`
  - `useMediaNodeMenu` 返回的 `badge` 里多出"未保存到本地"角标（与合规角标并排）。

- [ ] **Step 0: 建 worktree（基线 = Task 6 已合入的集成提交）、确认前置**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t7 -b p3bc-t7 <Task6 已合入的集成提交>
grep -n "export function isRemoteMediaUrl" .worktrees/p3bc-t7/web/src/desktop/localMedia.ts
grep -n "const badge = !state ? null" .worktrees/p3bc-t7/web/src/modules/canvas/editor/nodes/useMediaNodeMenu.tsx
```

Expected: 各输出 1 行。

- [ ] **Step 1: 写失败测试**

新建 `web/src/desktop/unsavedLocal.test.tsx`：

```tsx
import { App as AntApp } from 'antd';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { CanvasNode } from '@/modules/canvas/editor/types';
import {
  isUnsavedNode,
  replaceHistoryUrl,
  retryErrorMessage,
  retrySaveToLocal,
  useUnsavedLocalBadge,
} from './unsavedLocal';

vi.mock('@/modules/canvas/editor/persistence/canvasMutations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/modules/canvas/editor/persistence/canvasMutations')>()),
  persistNodeById: vi.fn(),
}));

const REMOTE = 'https://upstream.example.com/out/1.png?sig=a&exp=1';
const LOCAL = 'http://127.0.0.1:5123/media/gen/9.png?token=t';

function genNode(patch: Partial<CanvasNode> = {}): CanvasNode {
  return {
    id: 'g1',
    type: 'gen-image',
    x: 0,
    y: 0,
    width: 280,
    height: 280,
    status: 'succeeded',
    content: REMOTE,
    settings: {},
    ...patch,
  };
}

function setNodes(nodes: CanvasNode[]) {
  useCanvasStore.setState({ nodes, connections: [], selectedIds: new Set<string>(), readOnly: false });
}

beforeEach(() => {
  vi.clearAllMocks();
  window.history.pushState({}, '', '/canvas/14');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('isUnsavedNode', () => {
  it('快照里带 settings.unsaved 的节点算未保存', () => {
    expect(isUnsavedNode(genNode({ content: LOCAL, settings: { unsaved: true, genTaskId: 3 } }))).toBe(true);
  });

  // 页面开着时生成结果靠轮询回填，store 里没有 unsaved 键：按「生成类节点 + 成功 + 外链」认定。
  it('生成类节点成功但内容是上游外链，也算未保存', () => {
    for (const type of ['gen-image', 'gen-video', 'gen-audio', 'subtitle-erase', 'video-upscale', 'video-whitemodel'] as const) {
      expect(isUnsavedNode(genNode({ type }))).toBe(true);
    }
  });

  it('本机地址、非生成类节点、未完成的节点、空节点都不算', () => {
    expect(isUnsavedNode(genNode({ content: LOCAL }))).toBe(false);
    expect(isUnsavedNode(genNode({ type: 'input-image' }))).toBe(false);
    expect(isUnsavedNode(genNode({ status: 'generating' }))).toBe(false);
    expect(isUnsavedNode(genNode({ content: undefined }))).toBe(false);
    expect(isUnsavedNode(undefined)).toBe(false);
  });
});

describe('replaceHistoryUrl', () => {
  it('字符串条目和对象条目里的旧地址都换成新地址，其余原样', () => {
    expect(
      replaceHistoryUrl([REMOTE, { url: REMOTE, prompt: 'p' }, 'media://old', { url: 'media://x' }], REMOTE, LOCAL),
    ).toEqual([LOCAL, { url: LOCAL, prompt: 'p' }, 'media://old', { url: 'media://x' }]);
  });

  it('不是数组时原样返回', () => {
    expect(replaceHistoryUrl(undefined, REMOTE, LOCAL)).toBeUndefined();
  });
});

describe('retrySaveToLocal', () => {
  it('有 genTaskId：调 repersist，把节点内容、历史、assetId 换成本地并清掉标记，再补一次持久化', async () => {
    setNodes([genNode({ settings: { unsaved: true, genTaskId: 42, history: [{ url: REMOTE, prompt: 'p' }, 'media://old'] } })]);
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [9] } });

    await retrySaveToLocal('g1');

    expect(post).toHaveBeenCalledWith('/tasks/42/repersist');
    const n = useCanvasStore.getState().nodes[0];
    expect(n.content).toBe(LOCAL);
    expect(n.settings).toMatchObject({ assetId: 9, history: [{ url: LOCAL, prompt: 'p' }, 'media://old'] });
    expect(n.settings?.unsaved).toBeUndefined();
    expect(n.settings?.genTaskId).toBeUndefined();
    expect(persistNodeById).toHaveBeenCalledWith('g1');
  });

  it('没有 genTaskId（页面开着时出的结果）：先按节点找回任务号', async () => {
    setNodes([genNode()]);
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: { taskId: 77, status: 'succeeded' } });
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [] } });

    await retrySaveToLocal('g1');

    expect(get).toHaveBeenCalledWith('/canvas-tasks/by-node', { params: { projectId: 14, nodeId: 'g1' } });
    expect(post).toHaveBeenCalledWith('/tasks/77/repersist');
    expect(useCanvasStore.getState().nodes[0].content).toBe(LOCAL);
  });

  it('找不到任务号时报错，不调 repersist', async () => {
    setNodes([genNode()]);
    vi.spyOn(api, 'get').mockResolvedValue({ data: {} });
    const post = vi.spyOn(api, 'post');

    await expect(retrySaveToLocal('g1')).rejects.toThrow('找不到这次生成的任务记录，无法重试');
    expect(post).not.toHaveBeenCalled();
  });

  it('等待期间节点已重新生成：只替换历史，不动当前内容和标记', async () => {
    setNodes([genNode({ settings: { genTaskId: 42, history: [REMOTE] } })]);
    vi.spyOn(api, 'post').mockImplementation(async () => {
      setNodes([genNode({ content: 'media://newer', settings: { genTaskId: 50, history: [REMOTE, 'media://newer'] } })]);
      return { data: { outputUrls: [LOCAL], assetIds: [9] } };
    });

    await retrySaveToLocal('g1');

    const n = useCanvasStore.getState().nodes[0];
    expect(n.content).toBe('media://newer');
    expect(n.settings).toMatchObject({ genTaskId: 50, history: [LOCAL, 'media://newer'] });
    expect(n.settings?.assetId).toBeUndefined();
  });

  it('retryErrorMessage 优先取 sidecar 返回的原因', () => {
    expect(retryErrorMessage({ response: { data: { error: '保存到本地失败：timeout' } } })).toBe('保存到本地失败：timeout');
    expect(retryErrorMessage(new Error('找不到这次生成的任务记录，无法重试'))).toBe('找不到这次生成的任务记录，无法重试');
    expect(retryErrorMessage(undefined)).toBe('保存到本地失败');
  });
});

function BadgeProbe({ nodeId }: { nodeId: string }) {
  return <div>{useUnsavedLocalBadge(nodeId)}</div>;
}

function renderBadge() {
  render(
    <AntApp>
      <BadgeProbe nodeId="g1" />
    </AntApp>,
  );
}

describe('useUnsavedLocalBadge', () => {
  it('未保存时显示「未保存到本地」和「重试」', () => {
    setNodes([genNode()]);
    renderBadge();
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('本机文件不显示角标', () => {
    setNodes([genNode({ content: LOCAL })]);
    renderBadge();
    expect(screen.queryByText('未保存到本地')).toBeNull();
  });

  it('重试成功：提示「已保存到本地」，角标消失', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    vi.spyOn(api, 'post').mockResolvedValue({ data: { outputUrls: [LOCAL], assetIds: [9] } });
    renderBadge();

    await userEvent.click(screen.getByRole('button', { name: '重试' }));

    expect(await screen.findByText('已保存到本地')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('未保存到本地')).toBeNull());
  });

  it('重试失败：提示 sidecar 的原因，角标保留、可以再点', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    vi.spyOn(api, 'post').mockRejectedValue({ response: { data: { error: '保存到本地失败：上游地址已过期' } } });
    renderBadge();

    await userEvent.click(screen.getByRole('button', { name: '重试' }));

    expect(await screen.findByText('保存到本地失败：上游地址已过期')).toBeInTheDocument();
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: '重试' })).not.toBeDisabled());
  });

  it('重试进行中按钮置灰，连点只发一次请求', async () => {
    setNodes([genNode({ settings: { genTaskId: 42 } })]);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let finish: (v: any) => void = () => {};
    const post = vi.spyOn(api, 'post').mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    renderBadge();

    const btn = screen.getByRole('button', { name: '重试' });
    await userEvent.click(btn);
    await waitFor(() => expect(btn).toBeDisabled());
    await userEvent.click(btn);
    expect(post).toHaveBeenCalledTimes(1);

    finish({ data: { outputUrls: [LOCAL], assetIds: [] } });
    await waitFor(() => expect(screen.queryByText('未保存到本地')).toBeNull());
  });
});
```

`useMediaNodeMenu.test.tsx`：
1. `Probe` 组件里把

```tsx
    <div data-testid="probe" onContextMenu={menu.onContextMenu}>
      probe
      {menu.overlay}
    </div>
```

改为

```tsx
    <div data-testid="probe" onContextMenu={menu.onContextMenu}>
      probe
      {menu.badge}
      {menu.overlay}
    </div>
```

2. 文件末尾加：

```tsx
describe('useMediaNodeMenu 「未保存到本地」角标（S5）', () => {
  it('生成结果还是上游外链时，badge 里有「未保存到本地」和「重试」', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'g1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
        status: 'succeeded', content: 'https://upstream.example.com/1.png', settings: {},
      }],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
    renderProbe({ url: 'https://upstream.example.com/1.png', kind: 'image', nodeId: 'g1' });
    expect(screen.getByText('未保存到本地')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
  });

  it('本机文件不挂这个角标', () => {
    useCanvasStore.setState({
      nodes: [{
        id: 'g1', type: 'gen-image', x: 0, y: 0, width: 280, height: 280,
        status: 'succeeded', content: 'http://127.0.0.1:5123/media/k', settings: {},
      }],
      connections: [],
      selectedIds: new Set(),
      view: { x: 0, y: 0, zoom: 1 },
      lightbox: null,
    });
    renderProbe({ url: 'http://127.0.0.1:5123/media/k', kind: 'image', nodeId: 'g1' });
    expect(screen.queryByText('未保存到本地')).toBeNull();
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/desktop/unsavedLocal.test.tsx src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx`
Expected: `unsavedLocal.test.tsx` 因 `Failed to resolve import "./unsavedLocal"` FAIL；`useMediaNodeMenu.test.tsx` 新增的第一个用例 FAIL（找不到"未保存到本地"）。

- [ ] **Step 3: 写 `web/src/desktop/unsavedLocal.tsx`**

```tsx
// spec S5：生成结果下载到本机失败（sidecar 自动重试 3 次仍失败）时，节点里留的是上游
// 外链。这里负责认出这种节点、挂「未保存到本地」角标和「重试」按钮。
//
// 重试走 sidecar 的 POST /tasks/{id}/repersist（P1c Task 5）。成功时 sidecar 已经把快照里
// 的节点改成本地地址；页面这边把同样的改动落到 store，再补一次局部持久化——与生成结果
// 回填同一套做法（服务端权威写回，前端补齐自己的状态）。
import { useState, type ReactNode } from 'react';
import { App as AntApp } from 'antd';
import { CloudOff, Loader2 } from 'lucide-react';
import { api, currentProjectId } from '@/shared/api';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { persistNodeById } from '@/modules/canvas/editor/persistence/canvasMutations';
import type { CanvasNode, NodeType } from '@/modules/canvas/editor/types';
import { isRemoteMediaUrl } from './localMedia';

// 产物由 sidecar 生成管线写回的节点类型（canvasgen 的 sink 只写这几类）。
const GENERATED_TYPES: ReadonlySet<NodeType> = new Set<NodeType>([
  'gen-image',
  'gen-video',
  'gen-audio',
  'subtitle-erase',
  'video-upscale',
  'video-whitemodel',
]);

export function isUnsavedNode(node: CanvasNode | undefined): boolean {
  if (!node) return false;
  if (node.settings?.unsaved === true) return true;
  // 页面开着时生成结果靠轮询任务回填，任务状态里没有 unsaved（它只写进快照），要等重新
  // 打开项目才读得到。所以对生成类节点按「成功了但内容是外链」认定：桌面端生成结果正常
  // 都是本机地址，只有下载失败才会留下外链。
  return GENERATED_TYPES.has(node.type) && node.status === 'succeeded' && isRemoteMediaUrl(node.content);
}

/** 与 sidecar 的 ApplyRepersist 同一规则：history 里字符串条目和 {url} 条目都换。 */
export function replaceHistoryUrl(history: unknown, oldUrl: string, newUrl: string): unknown {
  if (!Array.isArray(history)) return history;
  return history.map((entry) => {
    if (entry === oldUrl) return newUrl;
    if (entry && typeof entry === 'object' && (entry as { url?: unknown }).url === oldUrl) {
      return { ...(entry as Record<string, unknown>), url: newUrl };
    }
    return entry;
  });
}

export interface RepersistResult {
  outputUrls: string[];
  assetIds: number[];
}

export async function repersistTask(taskId: number): Promise<RepersistResult> {
  const { data } = await api.post<Partial<RepersistResult>>(`/tasks/${taskId}/repersist`);
  return { outputUrls: data?.outputUrls ?? [], assetIds: data?.assetIds ?? [] };
}

// 快照里有 genTaskId 就用它；没有（页面开着时出的结果）就按节点找回最近一次任务。
async function resolveTaskId(node: CanvasNode): Promise<number | null> {
  const recorded = Number(node.settings?.genTaskId);
  if (Number.isInteger(recorded) && recorded > 0) return recorded;
  const projectId = currentProjectId();
  if (!projectId) return null;
  const { data } = await api.get<{ taskId?: number }>('/canvas-tasks/by-node', {
    params: { projectId, nodeId: node.id },
  });
  return data?.taskId && data.taskId > 0 ? data.taskId : null;
}

export async function retrySaveToLocal(nodeId: string): Promise<void> {
  const node = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
  if (!node?.content) return;
  const oldUrl = node.content;
  const taskId = await resolveTaskId(node);
  if (!taskId) throw new Error('找不到这次生成的任务记录，无法重试');
  const { outputUrls, assetIds } = await repersistTask(taskId);
  const newUrl = outputUrls[0];
  if (!newUrl) throw new Error('保存到本地失败：sidecar 没有返回本地地址');

  // 等待期间节点可能被删、被重新生成，按最新状态落。
  const latest = useCanvasStore.getState().nodes.find((n) => n.id === nodeId);
  if (!latest) return;
  const store = useCanvasStore.getState();
  const history = latest.settings?.history;
  if (latest.content !== oldUrl) {
    // 已经重新生成过：当前内容和标记属于新任务，只把历史里那一版换成本地地址。
    if (Array.isArray(history)) {
      store.updateNodeSettings(nodeId, { history: replaceHistoryUrl(history, oldUrl, newUrl) });
      persistNodeById(nodeId);
    }
    return;
  }
  const settingsPatch: Record<string, unknown> = { unsaved: undefined, genTaskId: undefined };
  if (assetIds[0]) settingsPatch.assetId = assetIds[0];
  if (Array.isArray(history)) settingsPatch.history = replaceHistoryUrl(history, oldUrl, newUrl);
  store.updateNodeSettings(nodeId, settingsPatch);
  store.updateNode(nodeId, { content: newUrl });
  persistNodeById(nodeId);
}

export function retryErrorMessage(err: unknown): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string } | undefined;
  return e?.response?.data?.error || e?.message || '保存到本地失败';
}

/** 节点名字行上的「未保存到本地」角标；节点已在本机时返回 null。 */
export function useUnsavedLocalBadge(nodeId: string): ReactNode | null {
  const unsaved = useCanvasStore((s) => isUnsavedNode(s.nodes.find((n) => n.id === nodeId)));
  const readOnly = useCanvasStore((s) => s.readOnly);
  const { message } = AntApp.useApp();
  const [retrying, setRetrying] = useState(false);
  if (!unsaved) return null;

  const retry = async () => {
    if (retrying) return;
    setRetrying(true);
    try {
      await retrySaveToLocal(nodeId);
      message.success('已保存到本地');
    } catch (err) {
      message.error(retryErrorMessage(err));
    } finally {
      setRetrying(false);
    }
  };

  return (
    <span
      data-unsaved-local={nodeId}
      className="shrink-0 flex items-center gap-1 px-1.5 py-0.5 rounded bg-amber-600/90 text-white text-[11px] leading-none font-medium"
      title="生成结果没能下载到本机，现在用的是上游的临时地址，过期后会打不开"
    >
      <CloudOff size={11} />
      未保存到本地
      <button
        type="button"
        disabled={retrying || readOnly}
        className="ml-0.5 px-1 py-0.5 rounded bg-white/20 hover:bg-white/30 text-white text-[11px] leading-none border-none cursor-pointer disabled:cursor-default disabled:opacity-60"
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation();
          void retry();
        }}
      >
        {retrying ? <Loader2 size={11} className="animate-spin" aria-label="重试" /> : '重试'}
      </button>
    </span>
  );
}
```

- [ ] **Step 4: 把角标并进 `useMediaNodeMenu` 的 `badge`**

1. 在 Task 6 加的 `@/desktop/localMedia` import 下面加：

```tsx
import { useUnsavedLocalBadge } from '@/desktop/unsavedLocal';
```

2. 在 `const durationCheck = checkSeedanceDuration(kind, duration);` 下面加一行：

```tsx
  const unsavedBadge = useUnsavedLocalBadge(nodeId);
```

3. 把

```tsx
  // 状态角标。没校验过的不显示任何东西——绝大多数素材都属于这一类，全都挂标记
  // 只会变成噪音。
  const badge = !state ? null : state.status === 'active' ? (
```

改为

```tsx
  // 合规状态角标。没校验过的不显示任何东西——绝大多数素材都属于这一类，全都挂标记
  // 只会变成噪音。
  const seedanceBadge = !state ? null : state.status === 'active' ? (
```

4. 在这个三元表达式结束（`  );`）之后、`  const overlay = (` 之前插入：

```tsx
  // 「未保存到本地」（spec S5）和合规角标互不相干，同时出现时并排显示，未保存排前面。
  const badge = unsavedBadge || seedanceBadge ? (
    <>
      {unsavedBadge}
      {seedanceBadge}
    </>
  ) : null;

```

- [ ] **Step 5: 运行测试确认通过**

Run: `pnpm --dir web exec vitest run src/desktop src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx src/modules/canvas/editor/nodes/seedanceBadgeWiring.test.ts`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

Run: `pnpm --dir web test 2>&1 | tail -4`
Expected: 没有 failed（生成节点的测试会渲染 badge；夹具里结果是 `https://example.test/…` 外链的节点会多出这个角标，原有断言不受影响——如有用例因多出"重试"按钮而按 role 找按钮失败，把该用例的按钮查询改成按 `name` 精确匹配，不要改实现）。

- [ ] **Step 6: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t7
git add web/src/desktop/unsavedLocal.tsx web/src/desktop/unsavedLocal.test.tsx web/src/modules/canvas/editor/nodes/useMediaNodeMenu.tsx web/src/modules/canvas/editor/nodes/useMediaNodeMenu.test.tsx
git commit -m "feat(web): 生成结果未保存到本地时节点显示提示与重试按钮（S5）"
```

---

### Task 8: 协作残留清理——瞬态通道、在线名册、远端 op、在途登记（X2）

**Files:**
- Create: `web/src/desktop/noCollabResidue.test.ts`
- Delete: `web/src/modules/canvas/editor/transientChannel.ts`、`transientChannel.test.ts`、`presenceStore.ts`、`presenceStore.test.ts`、`interactionRegistry.ts`、`hooks/useNodeEditingBroadcast.ts`、`store.transient.test.ts`、`store.remoteOp.test.ts`、`concurrentEditSequences.test.ts`、`textNodeEditingPresence.test.tsx`
- Modify: `web/src/modules/canvas/editor/store.ts`、`NodeShell.tsx`、`NodeShell.test.tsx`、`ConnectionLayer.tsx`、`ConnectionLayer.test.tsx`、`nodes/TextNode.tsx`、`persistence/canvasMutations.ts`、`persistence/canvasMutations.persistNode.test.ts`

**Interfaces:**
- Consumes: 无。
- Produces: `store.ts` 不再导出 `RemoteNodeGeometry`、`RemoteCanvasOp`、`RemoteNodeStateOp`、`RemoteNodeStateSink`、`setRemoteNodeStateSink`、`PendingLocalPatchProvider`、`setPendingLocalPatchProvider`、`clearDeferredRemoteOps`、`TRANSIENT_POSITION_TTL_MS`、`TRANSIENT_SUPPRESS_MS`、`TransientPosition`、`clearTransientPositions`、`resolveNodePosition`；`CanvasState` 去掉 `transientPositions`、`applyRemoteOp`、`applyTransientPositions`。其余导出（`setCanvasOpSink`、`CanvasOp`、`DEFAULT_SIZES`、`useCanvasStore` 的其它 action）不变。`canvasMutations.ts` 的全部导出名和签名不变。

- [ ] **Step 0: 建 worktree、确认引用面没有变**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t8 -b p3bc-t8 p3bc-base
cd .worktrees/p3bc-t8/web/src
grep -rln "clearDeferredRemoteOps\|setPendingLocalPatchProvider\|setRemoteNodeStateSink\|RemoteNodeStateOp\|RemoteCanvasOp\|applyRemoteOp\|transientPositions\|applyTransientPositions\|clearTransientPositions\|resolveNodePosition\|TRANSIENT_\|interactionRegistry\|presenceStore\|transientChannel\|useNodeEditingBroadcast\|NODE_EDITING_HEARTBEAT_MS" . | sort
```

Expected: 正好是 F-5 列出的 19 个文件。多出文件说明 P3a 之后又有人引用，停下报告。

- [ ] **Step 1: 写失败的守护测试**

新建 `web/src/desktop/noCollabResidue.test.ts`：

```ts
// 守护：spec X2 删除了多人协作。瞬态通道、在线名册、远端 op 合并、在途登记都已删除；
// 以后从参考仓同步代码时如果把它们带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = [
  'modules/canvas/editor/transientChannel.ts',
  'modules/canvas/editor/presenceStore.ts',
  'modules/canvas/editor/interactionRegistry.ts',
  'modules/canvas/editor/hooks/useNodeEditingBroadcast.ts',
];

// 注意不按 beginInteraction 查：导演台 InspectorControls 有个同名但无关的局部函数。
const FORBIDDEN =
  /\b(applyRemoteOp|applyTransientPositions|transientPositions|clearTransientPositions|resolveNodePosition|RemoteCanvasOp|RemoteNodeStateOp|setRemoteNodeStateSink|setPendingLocalPatchProvider|deferredRemoteOps|clearDeferredRemoteOps|registerInFlightPatch|inFlightNodePatches|useCanvasPresenceStore|sendDragTransient|sendNodeEditingTransient|setTransientChannel)\b/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

// 去掉注释再查：说明性文字里提到旧名字不算残留。
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

describe('协作残留已清理（X2）', () => {
  it('协作专用模块已删除', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用瞬态通道、在线名册、远端 op 和在途登记', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/desktop/noCollabResidue.test.ts`
Expected: 2 个用例 FAIL，列出 4 个文件和 `store.ts`、`NodeShell.tsx`、`ConnectionLayer.tsx`、`canvasMutations.ts`、`TextNode.tsx` 等命中。

- [ ] **Step 3: 删除协作专用模块和只测它们的测试**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t8/web/src/modules/canvas/editor
git rm transientChannel.ts transientChannel.test.ts presenceStore.ts presenceStore.test.ts interactionRegistry.ts hooks/useNodeEditingBroadcast.ts store.transient.test.ts store.remoteOp.test.ts concurrentEditSequences.test.ts textNodeEditingPresence.test.tsx
```

- [ ] **Step 4: 改 `store.ts`**

1. 删除这两行 import（删完后 `applyMergePatch`、`isPlainRecord` 在 store 里已无使用者）：

```ts
import { isNodeInteracting, onInteractionEnd } from './interactionRegistry';
import { applyMergePatch, isPlainRecord } from './mergePatch';
```

2. 删除从 `// ── 远端 op（团队并发编辑）────` 这一行开始、到 `onInteractionEnd((nodeId) => {` 那段监听的结束 `});` 为止的整段（包含：`RemoteNodeGeometry`、`RemoteCanvasOp`、`RemoteNodeStateOp`、`RemoteNodeStateSink`/`setRemoteNodeStateSink`、`PendingLocalPatchProvider`/`setPendingLocalPatchProvider`、`DeferredRemoteNodeOp`、`deferredRemoteOps`、`clearDeferredRemoteOps`、`deferRemoteNode`、`mergeDeferredPatch`、`stripLocallyOwnedFields`、`stripAgainstLocal`、`overlayLocalPending`、整个"瞬态坐标覆盖层"小节、`cloneRemoteValue`、`geometryPatchRecord`、`onInteractionEnd(...)` 监听）。删完后，`function emitCanvasOp(op: CanvasOp): void { … }` 之后隔一个空行就是 `function sameStructuralNode(a: CanvasNode, b: CanvasNode): boolean {`。

3. `interface CanvasState` 里删除：

```ts
  /** 瞬态坐标覆盖层（nodeId → 坐标 + 过期时刻）。与 nodes 平行的一层，**永不
   *  合并进 nodes**，见 applyTransientPositions 与 resolveNodePosition。 */
  transientPositions: Record<string, TransientPosition>;
```

以及：

```ts
  /** 合并一条**远端**（队友经服务端广播回来的）op。见 RemoteCanvasOp 的说明。 */
  applyRemoteOp: (op: RemoteCanvasOp) => void;
  /** 落地一批**瞬态**坐标（队友拖拽途中的实时位置）。见实现处的三条不变量。 */
  applyTransientPositions: (positions: Array<{ id: string; x: number; y: number }>) => void;
```

4. `load` 的文档注释里把

```ts
   * undo 栈跨整拉保留是有意的，且与 applyRemoteOp 的既有语义一致：远端改动本来
   * 就不进 undo 栈，撤销撤的一直是「我自己做过的那几步」。
```

改为

```ts
   * undo 栈跨整拉保留是有意的：撤销撤的一直是「我自己做过的那几步」。
```

5. `create<CanvasState>(…)` 的初始值里删除 `  transientPositions: {},` 一行。

6. `load: (snapshot, options) => {` 里删除开头这 5 行：

```ts
    // 整快照重载已经包含了所有远端改动，挂起的旧 op 再落一遍就是拿旧帧覆盖新状态。
    clearDeferredRemoteOps();
    // 覆盖层同理：快照里的坐标是权威的，留着旧覆盖条目会让节点继续画在别人
    // 上一轮拖到一半的位置上（切项目时甚至画到同 id 的别的节点上）。
    clearTransientPositions();
```

7. 删除从 `  // 远端 op 合并。三条不变量：` 开始、到 `applyTransientPositions` 实现结束（`    scheduleTransientSweep();` 下一行的 `  },`）为止的整段，以及它后面多出的一个空行。删完后 `load` 的 `},` 之后隔一个空行就是 `  addNode: (type, worldPosition) => {`。

8. `deleteNodes` 里删除：

```ts
    // 本端删掉的节点，其挂起的远端 op 一并作废——不然交互结束时那条挂起的
    // upsert 会把已删节点凭空复活。
    for (const id of ids) deferredRemoteOps.delete(id);
```

- [ ] **Step 5: 改 `NodeShell.tsx`**

1. import 部分：

```tsx
import { resolveNodePosition, useCanvasStore } from './store';
import { beginInteraction, endInteraction } from './interactionRegistry';
import { resetDragTransientThrottle, sendDragTransient, shouldSendDragTransient } from './transientChannel';
import { nodeEditingLabel, useCanvasPresenceStore } from './presenceStore';
```

替换为：

```tsx
import { useCanvasStore } from './store';
```

2. 删除：

```tsx
  // 队友拖拽途中的瞬态坐标（覆盖层，见 store 的 applyTransientPositions）。
  // 渲染取位置的唯一入口就是这里：覆盖层与 node.x/y 是两层，瞬态永远进不了
  // node 本体——那一层是 persistNodeById 的 diff 面。
  const transientPosition = useCanvasStore((s) => s.transientPositions[node.id]);
  const position = resolveNodePosition(node, transientPosition);

  // 队友正在编辑本节点时的角标文案（null=没人）。协作在场信息不在画布 store 里，
  // 见 presenceStore 的说明。
  const remoteEditingLabel = useCanvasPresenceStore((s) => nodeEditingLabel(s, node.id));

```

3. 删除 `dragInteractionRef` 整段：从 `  // 本次拖拽登记了在途状态的节点（拖多选时是一整批）。` 开始、到 `  }, []);`（`releaseDragInteractions` 的 `useCallback` 结束）为止。

4. 卸载清理：

```tsx
  useEffect(() => () => {
    detachWindowListeners();
    releaseDragInteractions();
  }, [detachWindowListeners, releaseDragInteractions]);
```

替换为：

```tsx
  useEffect(() => () => {
    detachWindowListeners();
  }, [detachWindowListeners]);
```

5. `onMouseDown` 里删除：

```tsx
    // 登记在途：拖拽期间队友对这些节点的远端几何/内容更新一律挂起到松手
    // （否则节点会在用户手底下跳位）；同时也让本端收到的远端**瞬态**坐标对
    // 这些节点直接忽略（store 的 applyTransientPositions）。
    {
      const sel = useCanvasStore.getState().selectedIds;
      const dragging = sel.has(node.id) ? Array.from(sel) : [node.id];
      releaseDragInteractions();
      for (const id of dragging) beginInteraction(id, 'drag');
      dragInteractionRef.current = dragging;
    }
    // 前沿节流：让本次拖拽的第一帧立刻发出去，队友那边不会先愣 200ms。
    resetDragTransientThrottle();
```

6. `handleMove` 里删除从 `      // 瞬态拖拽流：节流后发**绝对**坐标（丢帧无需补偿，下一帧直接覆盖）。` 开始、到 `if (shouldSendDragTransient()) { … }` 结束（`      }`）为止的整段。删完后 `moveNodes(targets, dx, dy);` 下一行就是 `    };`。

7. `handleUp` 整个替换为：

```tsx
    const handleUp = () => {
      const committed = dragRef.current?.committed;
      dragRef.current = null;
      detachWindowListeners();
      // 拖动过程中只改本地，松手后一次性 PATCH 受影响节点的坐标。没真正
      // 移动（committed=false，只是点选）就不发请求。
      if (!committed) return;
      const pid = currentProjectId();
      const token = getCanvasOwnerToken();
      if (!pid || !token) return;
      const state = useCanvasStore.getState();
      const targets = state.selectedIds.has(node.id) ? Array.from(state.selectedIds) : [node.id];
      const patches = targets
        .map((id) => state.nodes.find((n) => n.id === id))
        .filter((n): n is CanvasNode => Boolean(n))
        .map((n) => geometryPatchFromNode(n));
      persistNodePositions(pid, token, patches);
    };
```

8. 紧接着的注释

```tsx
    // 第三条退出路径：window blur。切标签页/切应用时浏览器**不会**补一个
    // mouseup——没有它，拖拽状态会一直挂着：在途登记不解除（该节点上挂起的
    // 远端 op 永久卡住）、瞬态还在发、切回来一动鼠标节点就接着跟手飘。
    // 按 mouseup 同样处理并**照常落库**：用户切窗前看到的就是这个位置，
    // 丢掉等于回滚了他刚做的移动。
```

替换为：

```tsx
    // 第三条退出路径：window blur。切标签页/切应用时浏览器**不会**补一个
    // mouseup——没有它，切回来一动鼠标节点就接着跟手飘。按 mouseup 同样处理并
    // **照常落库**：用户切窗前看到的就是这个位置，丢掉等于回滚了他刚做的移动。
```

9. `onMouseDown` 的依赖数组 `[node.id, isSelected, selectNode, commit, moveNodes, storyboardMode, releaseDragInteractions, detachWindowListeners]` 改为 `[node.id, isSelected, selectNode, commit, moveNodes, storyboardMode, detachWindowListeners]`。

10. `onResizeMouseDown` 的 `handleUp` 里

```tsx
      // 同拖拽走 persistNodePositions：尺寸在飞期间归本端所有，远端旧坐标/尺寸
      // 不该顶掉刚松手的结果；成功后顺带推进基线。
```

改为

```tsx
      // 同拖拽走 persistNodePositions：成功后顺带推进基线。
```

11. 渲染里 `{ left: position.x, top: position.y, width: node.width, height: node.height, zIndex: isSelected ? 30 : 1 }` 改为 `{ left: node.x, top: node.y, width: node.width, height: node.height, zIndex: isSelected ? 30 : 1 }`。

12. 删除名字 chip 行里的"正在编辑"角标：

```tsx
        {/* 「xx 正在编辑」角标。**只提示，不阻止**（拍板）：字段级 LWW 之下
            后提交者胜，硬锁住节点反而会把人挡在自己想改的东西外面。挂在名字
            chip 这一行而不是节点内部——内容区是各节点自己的地盘，塞进去会跟
            提示词面板/工具条打架。 */}
        {remoteEditingLabel && !storyboardMode && (
          <span
            data-remote-editing={node.id}
            className="ml-1 px-1.5 py-0.5 rounded text-[10px] leading-none text-primary truncate max-w-[160px]"
            style={{ backgroundColor: 'rgba(28, 28, 28, 0.95)' }}
          >
            {remoteEditingLabel}
          </span>
        )}
```

- [ ] **Step 6: 改 `ConnectionLayer.tsx`、`TextNode.tsx`**

`ConnectionLayer.tsx`：
1. `import { resolveNodePosition, useCanvasStore } from './store';` 改为 `import { useCanvasStore } from './store';`。
2. 删除：

```tsx
  // 队友拖拽途中的瞬态坐标覆盖层。连线必须跟着一起走，否则队友拖一个有连线的
  // 节点时，节点动而线不动——看上去像连线断了。**只读不写**：坐标只在这里参与
  // 画路径，绝不回写进 nodes（那会让瞬态中间值进持久化 diff，正是 T4 H1 的坑）。
  // 个人项目这张表恒为初始空对象，引用不变 → 不产生任何多余渲染。
  const transientPositions = useCanvasStore((s) => s.transientPositions);
```

3. 把

```tsx
        const fromAt = resolveNodePosition(from, transientPositions[from.id]);
        const toAt = resolveNodePosition(to, transientPositions[to.id]);
        const startX = fromAt.x + from.width;
        const startY = fromAt.y + from.height / 2;
        const endX = toAt.x;
        const endY = toAt.y + to.height / 2;
```

改为

```tsx
        const startX = from.x + from.width;
        const startY = from.y + from.height / 2;
        const endX = to.x;
        const endY = to.y + to.height / 2;
```

`nodes/TextNode.tsx`：
1. 删除 `import { useNodeEditingBroadcast } from '../hooks/useNodeEditingBroadcast';`。
2. 删除：

```tsx
  // 广播「我正在编辑这个节点」+ 本地登记在途（队友的远端 patch 挂起到编辑结束，
  // 否则光标处的文字会被换掉）。放在早退之前——hook 不能有条件地调用。
  // 展开弹层也算编辑态：内容改的是同一个 settings.text，队友该看到同样的提示。
  useNodeEditingBroadcast(id, editing && !readOnly);

```

- [ ] **Step 7: 改 `persistence/canvasMutations.ts`**

1. 从 `'../store'` 的 import 整段替换为：

```ts
import { setCanvasOpSink, useCanvasStore, type CanvasOp } from '../store';
```

2. 基线说明里

```ts
// 谁推进基线（三个入口，都代表「服务端此刻就是这个状态」）：
//   1. 整快照加载后 seedCanvasBaselines()；
//   2. 本端 mutation 发送成功（upsert 发的是整节点，patch 按同一套递归合并规则并进去）；
//   3. 远端 op 落地（store 的 RemoteNodeStateSink 回吐）。
```

改为

```ts
// 谁推进基线（两个入口，都代表「服务端此刻就是这个状态」）：
//   1. 整快照加载后 seedCanvasBaselines()；
//   2. 本端 mutation 发送成功（upsert 发的是整节点，patch 按同一套递归合并规则并进去）。
```

3. 删除从 `// 「已经决定要发、但还没 settle」的 patch（per-node，可能排队多条）。` 开始、到 `registerInFlightPatch` 函数结束（`  };` 下一行的 `}`）为止的整段，以及它后面多出的一个空行。

4. `configureCanvasPersistence` 里删除：

```ts
  // 远端 op 落地时同步基线——服务端已经做了那一步。
  setRemoteNodeStateSink(noteRemoteNodeState);
  // 挂起的远端 patch 落地前，store 要问「这个节点还有哪些本地字段没发出去」。
  setPendingLocalPatchProvider(pendingLocalPatchFor);
```

5. 删除 `pendingLocalPatchFor` 函数及其上方注释（从 `// 该节点「本端还占着的字段」= 在飞的 patch（已发未 settle）∪ 队列里等 debounce 的` 到该函数结束的 `}`）。

6. 删除 `noteRemoteNodeState` 函数及其上方注释（从 `// 远端 op 落地时推进基线。收到的是**服务端做的那一步动作**（整节点或 patch），` 到该函数结束的 `}`）。

7. `seedCanvasBaselines` 的注释里 `不能拿本地构造的节点（例如 WorkflowImportModal 导入的 JSON）来播——那些节点` 改为 `不能拿本地构造的节点（例如本地新建、尚未落库的节点）来播——那些节点`。

8. `sendNodePatch` 里删除：

```ts
  // 登记在入队**之前**：从这一刻起这些字段就归本端所有，直到 settle。排队等待
  // 期间同样算在飞——队列是串行的，前面一个慢请求能把窗口拉得很长。
  const release = registerInFlightPatch(nodeID, patch);
```

并把它下面 `void runNodeMutation(context, async () => { … }).finally(release);` 的 `.finally(release);` 改为 `;`（即 `  });`）。

9. `persistNodePositions` 的文档注释整段替换为：

```ts
/**
 * 落库一批节点几何（拖拽松手 / 自动排布）。走这里而不是直接
 * `enqueueCanvasMutation(patchCanvasNodePositions(...))`：成功后要推进基线——几何是
 * 节点字段，不推的话随后的 persistNodeById 会把 x/y/w/h 当成变更再发一遍。
 */
```

函数体整个替换为：

```ts
  if (patches.length === 0) return;
  const context = captureMutationContext();
  void runNodeMutation(context, async () => {
    const result = await patchCanvasNodePositions(projectID, token, patches);
    if (isMutationContextCurrent(context)) {
      for (const patch of patches) mergePatchIntoBaseline(patch.id, geometryRecord(patch));
    }
    return result;
  });
```

10. `flushNodePatch` 里把

```ts
  // 摘出 pending 的同一步就登记为在飞——中间**一个 tick 都不能空窗**：失焦冲刷
  // 与随后的 endInteraction 之间没有 await，空窗期挂起的远端帧就会覆盖刚打的字。
  const release = registerInFlightPatch(nodeID, patch.patch);
  try {
    await enqueueCanvasMutationInContext(async () => {
      if (patch.projectID == null) {
        return Promise.resolve();
      }
      const result = await patch.patchNode(patch.projectID, patch.ownerToken, nodeID, patch.patch);
      // debounce 落库的 patch 同样推进基线：不推的话基线一直落后于服务端，
      // persistNodeById 之后会把这些字段的旧值再算成「变更」发一遍——真正的风险
      // 是队友在这中间改过同一个字段，那次重发就是拿我的旧值盖掉他的新值。
      if (isMutationContextCurrent(patch)) mergePatchIntoBaseline(nodeID, patch.patch);
      return result;
    }, patch);
  } finally {
    release();
  }
```

替换为

```ts
  await enqueueCanvasMutationInContext(async () => {
    if (patch.projectID == null) {
      return Promise.resolve();
    }
    const result = await patch.patchNode(patch.projectID, patch.ownerToken, nodeID, patch.patch);
    // debounce 落库的 patch 同样推进基线：不推的话基线一直落后于服务端，
    // persistNodeById 之后会把这些字段的旧值再算成「变更」发一遍。
    if (isMutationContextCurrent(patch)) mergePatchIntoBaseline(nodeID, patch.patch);
    return result;
  }, patch);
```

11. `resetCanvasPersistence` 里删除 `  setRemoteNodeStateSink(null);`、`  setPendingLocalPatchProvider(null);`、`  inFlightNodePatches = new Map();` 三行。

- [ ] **Step 8: 改测试**

`persistence/canvasMutations.persistNode.test.ts`：删除 `'本地有未发出的编辑时，远端 patch 落地不会吞掉它'`（连同它上方 `// H1：…` 两行注释）、`'远端 op 落地后基线跟着推进——本地 diff 不会把队友的字段改回去'`、`'远端删除后基线一并清除，同 id 节点重新出现时走 upsert'` 三个用例。

`ConnectionLayer.test.tsx`：
1. `beforeEach` 的 `setState` 里删除 `      transientPositions: {},`。
2. 删除最后一个用例 `'连线跟随远端瞬态坐标，而 nodes 本身不被改写'`（连同上方两行注释）。
3. `import { act, fireEvent, render } from '@testing-library/react';` 改为 `import { fireEvent, render } from '@testing-library/react';`。

`NodeShell.test.tsx`：
1. import 部分删除 `import { isNodeInteracting, resetInteractionRegistry } from './interactionRegistry';`、`import { resetCanvasPresence, useCanvasPresenceStore } from './presenceStore';`、`import { setTransientChannel, type OutgoingTransientFrame } from './transientChannel';`；`import { clearTransientPositions, useCanvasStore } from './store';` 改为 `import { useCanvasStore } from './store';`；`canvasMutations` 的 import 只保留 `setCanvasOwnerToken`。
2. 删除 `describe('NodeShell 瞬态坐标覆盖层', …)` 和 `describe('NodeShell 「正在编辑」角标', …)` 两整组。
3. `describe('NodeShell 拖拽瞬态流（团队并发编辑）', …)` 整组替换为：

```tsx
describe('NodeShell 拖拽落库', () => {
  function renderDraggable(id: string) {
    const node = makeNode(id, 0);
    useCanvasStore.setState({ nodes: [node], selectedIds: new Set<string>(), view: { x: 0, y: 0, zoom: 1 } });
    const view = render(
      <NodeShell node={node}>
        <div>body</div>
      </NodeShell>,
    );
    const shell = view.container.querySelector(`[data-node-id="${id}"]`) as HTMLElement;
    return { ...view, shell };
  }

  beforeEach(() => {
    vi.useFakeTimers();
    window.history.pushState({}, '', '/canvas/14');
    setCanvasOwnerToken('owner-token');
    vi.mocked(patchCanvasNodePositions).mockClear();
  });

  afterEach(() => {
    setCanvasOwnerToken('');
    vi.useRealTimers();
  });

  it('拖动后松手：一次 PATCH 落点坐标', async () => {
    const { shell } = renderDraggable('drag-up');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 40, clientY: 30 });
    fireEvent.mouseUp(window);

    expect(useCanvasStore.getState().nodes[0]).toMatchObject({ x: 40, y: 30 });
    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-up', x: 40, y: 30 }),
    ]);
  });

  it('只点选没移动：不发 PATCH', async () => {
    const { shell } = renderDraggable('drag-click-only');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseUp(window);
    await vi.advanceTimersByTimeAsync(50);
    expect(patchCanvasNodePositions).not.toHaveBeenCalled();
  });

  // 切标签页/切应用时浏览器**不会**补 mouseup：按松手处理并照常落库。
  it('window blur 视同松手：照常落库，之后的 mousemove 不再改坐标', async () => {
    const { shell } = renderDraggable('drag-blur');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });

    fireEvent.blur(window);

    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    expect(vi.mocked(patchCanvasNodePositions).mock.calls[0][2]).toEqual([
      expect.objectContaining({ id: 'drag-blur' }),
    ]);
    const x = useCanvasStore.getState().nodes[0].x;
    fireEvent.mouseMove(window, { clientX: 90, clientY: 90 });
    expect(useCanvasStore.getState().nodes[0].x).toBe(x);
  });

  it('卸载摘掉 window 监听：之后的 mousemove 不再改节点坐标', () => {
    const { shell, unmount } = renderDraggable('drag-detach');
    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    const movedTo = useCanvasStore.getState().nodes.find((n) => n.id === 'drag-detach')!.x;

    unmount();
    fireEvent.mouseMove(window, { clientX: 400, clientY: 400 });
    expect(useCanvasStore.getState().nodes.find((n) => n.id === 'drag-detach')!.x).toBe(movedTo);
  });

  it('多选拖拽：整批节点一起移动、一起落库', async () => {
    const a = makeNode('multi-a', 0);
    const b = makeNode('multi-b', 400);
    useCanvasStore.setState({ nodes: [a, b], selectedIds: new Set([a.id, b.id]), view: { x: 0, y: 0, zoom: 1 } });
    const { container } = render(
      <NodeShell node={a}>
        <div>body-a</div>
      </NodeShell>,
    );
    const shell = container.querySelector('[data-node-id="multi-a"]') as HTMLElement;

    fireEvent.mouseDown(shell, { button: 0, clientX: 0, clientY: 0 });
    fireEvent.mouseMove(window, { clientX: 10, clientY: 10 });
    fireEvent.mouseUp(window);

    await vi.waitFor(() => expect(patchCanvasNodePositions).toHaveBeenCalledTimes(1));
    const ids = vi.mocked(patchCanvasNodePositions).mock.calls[0][2].map((p: { id: string }) => p.id).sort();
    expect(ids).toEqual(['multi-a', 'multi-b']);
  });
});
```

4. 文件里如果 `act`、`screen` 已不再使用，从 `@testing-library/react` 的 import 里去掉（以 `grep -n "act(\|screen\." NodeShell.test.tsx` 确认）。

- [ ] **Step 9: 运行测试确认通过，跑类型检查和全量测试**

Run: `pnpm --dir web exec vitest run src/desktop/noCollabResidue.test.ts src/modules/canvas/editor/NodeShell.test.tsx src/modules/canvas/editor/ConnectionLayer.test.tsx src/modules/canvas/editor/persistence src/modules/canvas/editor/store.test.ts src/modules/canvas/editor/textNodePersistence.test.tsx`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

Run: `pnpm --dir web test 2>&1 | tail -4`
Expected: 没有 failed。

- [ ] **Step 10: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t8
git add -A web/src/modules/canvas/editor web/src/desktop/noCollabResidue.test.ts
git status --short   # 确认只有 Files 里列出的路径
git commit -m "refactor(web): 删除协作残留——瞬态通道、在线名册、远端 op 合并与在途登记（X2）"
```

---

### Task 9: 运维弹窗死代码与注释残留（F10、S10、X7）

**Files:**
- Create: `web/src/desktop/noOpsDialogs.test.ts`
- Delete: `web/src/modules/canvas/editor/WorkflowImportModal.tsx`、`WorkflowExportModal.tsx`、`workflowSerializer.ts`、`workflowSerializer.test.ts`、`web/src/shared/permissions.ts`
- Modify: `web/src/modules/canvas/editor/Sidebar.tsx`
- Modify（只改注释）: `web/src/modules/canvas/editor/agent/AgentPanel.tsx`、`agent/MessageBubble.tsx`、`agent/types.ts`、`nodes/text/TextGenPanel.tsx`

**Interfaces:**
- Consumes: 无。
- Produces: `Sidebar` 的 props 和渲染（除去管理员按钮）不变。

- [ ] **Step 0: 建 worktree、确认锚点**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t9 -b p3bc-t9 p3bc-base
cd .worktrees/p3bc-t9/web/src
grep -rn "useIsSuperAdmin\|WorkflowImportModal\|WorkflowExportModal\|workflowSerializer" . | grep -v "\.test\." | grep -v "^./modules/canvas/editor/Workflow\|^./modules/canvas/editor/workflowSerializer.ts\|^./shared/permissions.ts"
```

Expected: 只有 `modules/canvas/editor/Sidebar.tsx`（4 处）和 `modules/canvas/editor/persistence/canvasMutations.ts`（1 处注释，归 Task 8，本任务不改）。

- [ ] **Step 1: 写失败的守护测试**

新建 `web/src/desktop/noOpsDialogs.test.ts`：

```ts
// 守护：桌面端隐藏运维入口（spec F10 隐藏 Skill JSON 导入导出、S10 不做项目导入导出），
// 相关弹窗和超管门控已删除；以后从参考仓同步代码时如果带回来，这里会失败。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const SRC = join(__dirname, '..');

const REMOVED_FILES = [
  'modules/canvas/editor/WorkflowImportModal.tsx',
  'modules/canvas/editor/WorkflowExportModal.tsx',
  'modules/canvas/editor/workflowSerializer.ts',
  'shared/permissions.ts',
];

const FORBIDDEN = /\b(useIsSuperAdmin|WorkflowImportModal|WorkflowExportModal|compileToSkill|decodeFromSkill)\b/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
  }
  return out;
}

// 去掉注释再查：说明性文字里提到旧名字不算残留。
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

describe('运维弹窗已删除（F10、S10）', () => {
  it('弹窗、序列化器、超管门控文件不存在', () => {
    expect(REMOVED_FILES.filter((f) => existsSync(join(SRC, f)))).toEqual([]);
  });

  it('非测试源码不再引用它们', () => {
    const hits: string[] = [];
    for (const file of walk(SRC)) {
      const code = stripComments(readFileSync(file, 'utf8'));
      const m = code.match(FORBIDDEN);
      if (m) hits.push(`${relative(SRC, file)}: ${m[0]}`);
    }
    expect(hits).toEqual([]);
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/desktop/noOpsDialogs.test.ts`
Expected: 2 个用例 FAIL，列出 4 个文件和 `Sidebar.tsx` 等命中。

- [ ] **Step 3: 删文件、改 `Sidebar.tsx`**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t9/web/src
git rm modules/canvas/editor/WorkflowImportModal.tsx modules/canvas/editor/WorkflowExportModal.tsx modules/canvas/editor/workflowSerializer.ts modules/canvas/editor/workflowSerializer.test.ts shared/permissions.ts
```

`Sidebar.tsx`：
1. `import { ReactNode, useState } from 'react';` 改为 `import { ReactNode } from 'react';`。
2. lucide import 里删掉 `  FileJson,` 和 `  Workflow,` 两行。
3. 删除三行 import：

```tsx
import { useIsSuperAdmin } from '@/shared/permissions';
import { WorkflowExportModal } from './WorkflowExportModal';
import { WorkflowImportModal } from './WorkflowImportModal';
```

4. 删除：

```tsx
  const isSuperAdmin = useIsSuperAdmin();
  const [exportOpen, setExportOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
```

5. 删除从 `        {/* 超级管理员运维工具——对其他人不可见。把 canvas 编译成 Skill JSON` 开始的整段注释和紧跟的 `{isSuperAdmin && ( <> … </> )}`（到 `        )}` 为止）。
6. 删除药丸外面那段：

```tsx
      {isSuperAdmin && (
        <>
          <WorkflowExportModal open={exportOpen} onClose={() => setExportOpen(false)} />
          <WorkflowImportModal open={importOpen} onClose={() => setImportOpen(false)} />
        </>
      )}
```

- [ ] **Step 4: 改注释残留**

`agent/AgentPanel.tsx` 文件头：

```tsx
// 承载 agent 对话的右侧抽屉。视觉结构是 apps/web/src/panels/ChatPanel.tsx 的
// 1:1 移植（抽屉外壳、会话下拉、消息区、附件预览、输入栏）——唯一的区别在数据层：
```

改为

```tsx
// 承载 agent 对话的右侧抽屉（抽屉外壳、会话下拉、消息区、附件预览、输入栏）。
// 视觉结构源自参考仓已删除的 ChatPanel（桌面端不迁移，spec X7），数据层的区别：
```

`agent/MessageBubble.tsx`：
- 文件头 `// 气泡外壳视觉上跟 apps/web/src/panels/ChatPanel.tsx 的 MessageBubble 对齐` 改为 `// 气泡外壳视觉沿用参考仓 ChatPanel 的 MessageBubble（桌面端未迁移 ChatPanel，spec X7）`。
- `  // 跟 ChatPanel 一样只复制可见正文。` 改为 `  // 只复制可见正文。`
- `            跟 ChatPanel.MessageBubble 一致。 */}` 改为 `            与参考仓 ChatPanel 的气泡一致。 */}`

`nodes/text/TextGenPanel.tsx`：`  // 与 ChatPanel 的模型下拉同一口径：/models 里 type==='text' 的行。` 改为 `  // 模型下拉的口径：/models 里 type==='text' 的行。`

`agent/types.ts`：
- `/** 线格式，**不做校验**——运行时校验在 transientChannel 的` 和下一行 ` *  parseTransientDragPositions / presenceStore 的 parsePresenceMembers。 */` 两行替换为 `/** 线格式。桌面端前端不再消费瞬态帧（协作已删除，spec X2），类型保留只为与 Agent WebSocket 线格式对齐。 */`
- `CanvasOpFramePayload` 上方注释里

```ts
 * （snake_case 线格式）。**这里只描述线上形状，不做校验**：字段可信度由
 * `parseRemoteCanvasOp`（editor/remoteOpFrame.ts）在运行时把关，翻译成 store 的
 * `RemoteCanvasOp` 后才进画布——kind 是会增长的枚举，认不出来就退回整拉，
 * 不能让一个新 kind 把老客户端搞崩。
```

替换为

```ts
 * （snake_case 线格式）。**这里只描述线上形状**：桌面端个人编辑页不建页面级
 * WebSocket，也不做远端 op 合并（spec X2），类型保留只为与线格式对齐。
```

- [ ] **Step 5: 运行测试确认通过，跑类型检查**

Run: `pnpm --dir web exec vitest run src/desktop/noOpsDialogs.test.ts src/modules/canvas/editor/autoArrangePersistence.test.tsx src/modules/canvas/CanvasEditorPage.test.tsx`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 6: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t9
git add -A web/src/modules/canvas/editor/Sidebar.tsx web/src/modules/canvas/editor/WorkflowImportModal.tsx web/src/modules/canvas/editor/WorkflowExportModal.tsx web/src/modules/canvas/editor/workflowSerializer.ts web/src/modules/canvas/editor/workflowSerializer.test.ts web/src/shared/permissions.ts web/src/modules/canvas/editor/agent/AgentPanel.tsx web/src/modules/canvas/editor/agent/MessageBubble.tsx web/src/modules/canvas/editor/agent/types.ts web/src/modules/canvas/editor/nodes/text/TextGenPanel.tsx web/src/desktop/noOpsDialogs.test.ts
git commit -m "refactor(web): 删除 Skill JSON 导入导出弹窗与超管门控，清理 ChatPanel/协作注释残留"
```

---

### Task 10: apiGuards 放行名单清理（X2、X4）

**Files:**
- Modify: `web/src/shared/apiGuards.ts`
- Test: `web/src/shared/apiGuards.test.ts`

**Interfaces:**
- Consumes: 无。
- Produces: `setApiWritesBlocked(v: boolean): void`、`isApiWriteAllowed(method, url): boolean` 签名不变；删除 `isApiInReadOnlyMode`（无使用者）。只读态下所有写方法一律拦截。

- [ ] **Step 0: 建 worktree、确认无使用者**

```bash
cd /Users/james/github/miaoju-desktop
git worktree add .worktrees/p3bc-t10 -b p3bc-t10 p3bc-base
grep -rn "isApiInReadOnlyMode" .worktrees/p3bc-t10/web/src
```

Expected: 只有 `shared/apiGuards.ts` 里的定义一处。

- [ ] **Step 1: 写失败测试**

`apiGuards.test.ts` 删除这三个用例：`'放行「接管编辑」的抢锁请求，否则观看者永远接管不了'`（连同上方两行注释）、`'放行身份自愈的换发请求，否则只读态下自愈第一步就发不出去'`（连同上方两行注释）、`'分享 viewer 的克隆仍在放行名单里（既有行为）'`。在原位置插入：

```ts
  // 桌面端没有分享、团队观看、登录身份（spec X2、X4），参考仓为这些功能开的口子
  // 全部去掉：只读态下写请求一律拦住。
  it('没有放行名单：只读态下抢锁、身份换发、分享克隆都被拦住', () => {
    setApiWritesBlocked(true);
    for (const url of ['/projects/14/canvas/owner/claim', '/auth/switch-context', '/workflows/by-token/abc/clone']) {
      expect(isApiWriteAllowed('post', url)).toBe(false);
    }
  });
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir web exec vitest run src/shared/apiGuards.test.ts`
Expected: 新用例 FAIL（`/projects/14/canvas/owner/claim` 返回 true）。

- [ ] **Step 3: 重写 `apiGuards.ts`**

整个文件替换为：

```ts
// 全局写入拦截标记，由 axios 请求 interceptor 消费。
//
// 为什么单独拆一个模块而不是从 canvas store 读：api.ts 比 canvas store
// 层级更低，不应该依赖它。相反，是 canvas store 的 `setReadOnly` action
// 调这里的 `setApiWritesBlocked`。api.ts 和 canvas/store.ts 都依赖这个
// lib 模块——绝不会反过来。
//
// 与参考仓的差异：参考仓为分享克隆、团队观看「接管编辑」、团队身份自愈开了放行名单；
// 桌面端没有这些功能（spec X2、X4），只读态下写请求一律拦住。
let writesBlocked = false;

const WRITE_METHODS = new Set(['post', 'put', 'patch', 'delete']);

export function setApiWritesBlocked(v: boolean): void {
  writesBlocked = v;
}

export function isApiWriteAllowed(method: string | undefined, url: string | undefined): boolean {
  void url;
  if (!writesBlocked) return true;
  return !WRITE_METHODS.has((method ?? 'get').toLowerCase());
}
```

（保留 `url` 参数是为了不改 `api.ts` 的调用点；`void url;` 只为说明它有意不用。）

- [ ] **Step 4: 运行测试确认通过**

Run: `pnpm --dir web exec vitest run src/shared/apiGuards.test.ts src/shared/api.test.ts`
Expected: 全部 PASS。

Run: `pnpm --dir web exec tsc --noEmit`
Expected: 退出码 0。

- [ ] **Step 5: 提交**

```bash
cd /Users/james/github/miaoju-desktop/.worktrees/p3bc-t10
git add web/src/shared/apiGuards.ts web/src/shared/apiGuards.test.ts
git commit -m "refactor(web): apiGuards 去掉分享/抢锁/身份自愈放行名单与无用的 isApiInReadOnlyMode"
```

---

### Task 11: 集成验收

**Files:** 无代码改动（只合并和验证）。

**Interfaces:**
- Consumes: Task 1–10 全部产出。
- Produces: 可在 Tauri 里运行的 P3b + P3c 前端。

- [ ] **Step 1: 合入并全量验证**

按 Task 1 → 2 → 3 → 5 → 6 → 8 → 9 → 10 → 4 → 7 的顺序合入同一集成分支（Task 4、Task 7 的分支本来就从集成分支开出，合入是快进或无冲突合并）。然后：

```bash
cd /Users/james/github/miaoju-desktop
pnpm --dir web build 2>&1 | grep -E "built in|error"
pnpm --dir web test 2>&1 | tail -4
grep -rn "积分" web/src --include="*.tsx" | grep -v "\.test\." | grep -E "['\"\`>][^'\"\`<]*积分"
```

Expected: 构建成功；测试 `Test Files  N passed (N)`，没有 failed。最后一条 grep 粗查界面字符串里的"积分"：如有命中，逐条确认都在注释里（例如"零积分"一类说明），界面上不能再有。把测试文件数和用例数记进合入提交说明。

- [ ] **Step 2: Tauri 手动验收（`pnpm dev`；需要 P1b + P1c 已合入的 sidecar）**

1. **X3：** 打开一个项目，分别选中 AI 绘图、AI 视频、AI 音频节点，打开去字幕、转高清、转白模节点，在图片节点工具条打开多角度、打光面板，在脚本节点打开"添加资产 → AI 生成"和剧本生成框：全都看不到"积分""预计""估算中""计算中…"和 ⚡ 数字；生成按钮照常可点、能提交。
2. **F2：** 双击画布空白处（或右键 → 添加节点），菜单出现"输出"组，里面只有"保存到本地"；点它能建出节点，连上一个图片节点后列表里出现该图片。（点"下载"在 Tauri 里预期没有反应，见"需要决定的问题"第 1 条。）
3. **S3（macOS）：** 对上传的图片、生成成功的图片/视频/音频节点右键，菜单在"保存到素材库"下面有"在访达中显示"；点击后访达打开数据目录并选中该文件。在访达里把文件删掉后再点，提示"文件不存在，可能已被删除"。
4. **S3（Windows，有条件时）：** 同上，菜单文案是"在资源管理器中显示"，资源管理器打开并选中文件。
5. **S5：** 提交一次 AI 绘图，等 sidecar 开始下载结果时断网（关 Wi-Fi），直到 sidecar 日志出现下载重试 3 次失败；节点显示图片（上游地址）并在名字行显示"未保存到本地 [重试]"。右键菜单里"在访达中显示"置灰。恢复网络后点"重试"：提示"已保存到本地"，角标消失，"在访达中显示"可用。退出应用再打开：节点仍是本地文件，没有角标。另测一次：断网状态下直接退出应用再打开，角标仍在（来自快照里的 `settings.unsaved`），重试照常可用。
6. **X2：** 拖动节点、多选拖动、拖动中切到别的应用再切回：松手位置都能保存（退出重开后位置不变）；文本节点输入后点空白处，重开后内容在。
7. **F10/S10：** 左侧药丸栏没有 Skill JSON 导入导出按钮。

- [ ] **Step 3: 清理 worktree（确认全部合入后）**

```bash
cd /Users/james/github/miaoju-desktop
for n in 1 2 3 4 5 6 7 8 9 10; do git worktree remove .worktrees/p3bc-t$n; done
```

---

## 需要决定的问题（执行前请确认）

1. **Tauri 里下载不可用。** "保存到本地"节点、图片/视频/音频工具条的"下载"、生成历史的 ZIP 下载都依赖浏览器下载；按 wry 0.57 源码，Tauri 没注册 `on_download` 时 macOS WebView 会取消下载（F-2），节点放回菜单后点"下载"大概率没反应。**推荐：** 本计划只按 spec F2 把节点放回菜单，下载问题另开一个小任务放进 P8：壳层给主窗口注册 `on_download`（存到"下载"文件夹、重名自动加序号、完成后在访达中选中），前端把 `<a download>` 统一改成先取 blob 再触发下载以绕开跨源限制。**理由：** 一次修好全部 6 处下载，也不违背"照搬参考仓只改 UI"；改壳要动窗口创建方式（目前窗口写在 `tauri.conf.json`），归 P8 更合适。替代方案：把 `LocalSaveNode` 的按钮改成"在访达中显示"（纯前端、现在就能用），但这改变了节点含义，其余 5 处下载也还是坏的。

2. **页面开着时怎么知道"未保存到本地"。** sidecar 只把 `unsaved/genTaskId` 写进快照，轮询的任务状态里没有（F-4）。**推荐（本计划已按此写）：** 前端对生成类节点按"成功 + 内容是 http(s) 外链"认定未保存；节点上没有 `genTaskId` 时，用现成的 `GET /canvas-tasks/by-node` 找回任务号。**理由：** 不改 P1c 契约、不加后端代码；桌面端生成结果正常都是本机地址，误判面很小（已列入 Review Focus 第 1 条）。替代方案：让 P1c 的 `/canvas-tasks/{id}` 多返回 `unsaved` 字段，判断更直接，但要改后端和它的测试。

3. **Task 8 会改持久化层。** 删远端 op 时要连带删掉 `canvasMutations.ts` 的"在飞登记"和两处 store 注册（F-5），这是画布保存的核心文件。**推荐：** 照常执行，但 Task 8 单独派一个审查子代理，重点核对拖拽落库、文本失焦保存、生成回填这三条路径（Review Focus 第 5 条），并跑一遍手动验收第 6 项。**理由：** 这些代码在桌面端永远不会执行，留着会误导后续维护，且现有持久化测试（`canvasMutations.test.ts`、`textNodePersistence.test.tsx`、`uploadPersistence.test.tsx`）都保留并要求通过。保守替代：只删瞬态通道和在线名册（NodeShell、ConnectionLayer、TextNode、store 的覆盖层），`applyRemoteOp`、在途登记和 `canvasMutations` 不动。

4. **计费类型字段是否一起删。** `ModelConfig.priceNote` 和脚本候选的 `credits` 只删显示、类型保留（Global Constraints）。**推荐：** 保留。**理由：** sidecar（P2a 内置模型、P6 脚本节点）仍会返回这两个字段；删掉类型会让测试夹具的对象字面量过不了类型检查，还要协调后端，收益只是少两行类型定义。

5. **"未保存到本地"的入口位置。** **推荐（本计划已按此写）：** 角标和"重试"按钮放在节点名字那一行，与 Seedance 合规角标同一位置；右键菜单不再加"重试保存"项。**理由：** spec S5 要求"节点显示提示和重试按钮"；名字行在所有 10 种媒体节点上已有统一挂载点（`useMediaNodeMenu` 的 `badge`），不用改节点组件，也不会挡住内容区。

---

## 自查记录

- **spec 覆盖：** X3 → Task 1（3 个生成节点）、Task 2（3 个后处理节点）、Task 3（2 个面板 + 资产生成 + 脚本生成）、Task 4（接口、合批器、priceNote、守护）。F2 → Task 5。S3 → Task 6。S5 → Task 7。X2 → Task 8（瞬态、名册、远端 op、在途登记）、Task 10（抢锁放行）。X4 → Task 10（身份自愈放行）。F10/S10 → Task 9。X7（ChatPanel）→ Task 9 注释。用户点名的 9 个大组件（GenImageNode、GenVideoNode、GenAudioNode、SubtitleEraseNode、TieredVideoNode、MultiAnglePanel、LightPanel、AddAssetDialog、ScriptGenerator）全部覆盖；P3a 计划里说的 12 个测试文件对应 F-1 列出的测试。
- **类型一致：** `isLocalMediaUrl`、`isRemoteMediaUrl`、`fileManagerLabel`、`revealInFileManager`、`revealErrorMessage`（Task 6 定义，Task 7 用 `isRemoteMediaUrl`）；`isUnsavedNode`、`replaceHistoryUrl`、`RepersistResult`、`repersistTask`、`retrySaveToLocal`、`retryErrorMessage`、`useUnsavedLocalBadge`（Task 7 定义并在测试和 `useMediaNodeMenu` 中使用）名字与签名一致。
- **未在临时目录实测：** 本计划按只读阅读写成，没有实际执行改动。各任务 Step 0 用 grep 校验锚点、Step 末尾跑 `tsc` 和相关测试，执行时如果锚点或用例行为与本文不符，以实际代码为准并在任务报告里说明。
