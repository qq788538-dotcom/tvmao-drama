# 画布桌面客户端 Spec

- 日期：2026-09-29
- 状态：**已定稿（2026-09-30）：64 项决策和实施阶段 P0–P8 全部确认；P7 挂起**
- 参考项目：`/Users/james/github/huabu_studio`（下称"参考仓"）
- 目标目录：`/Users/james/github/miaoju-desktop`

状态标记：`⬜ 待定` · `✅ 已确认` · `✏️ 已修改` · `❌ 否决`

---

## 0. 背景与目标

把参考仓的**自由画布**模块做成一个桌面客户端：数据全部存在本机，模型调用可配置，界面与参考仓保持一致。

参考仓现状（已核实）：
- 画布前端约 5.4 万行（`apps/web/src/modules/canvas`）。按依赖分三类：
  - A 纯前端：约 60%
  - B 调后端 API：约 30%
  - C 强依赖服务端：约 10%
- 后端 Go 1.25，依赖 MySQL + Redis + MinIO/TOS。Agent 基于 Eino（`internal/agent`，6.5k 行）。
- 后台"模型中心"（`apps/admin/src/routes/ModelCenterPage.tsx`）已经支持 JSON 导入导出，模型按"配置 + 通用引擎"的方式运行。

---

## 1. 技术选型

| ID | 决策项 | 候选 | 推荐 | 状态 |
|---|---|---|---|---|
| D1 | 桌面框架 | Tauri v2 / Wails v2 / Electron | Tauri v2（生态最成熟，主版本稳定） | ✅ 已确认 |
| D2 | 本地服务实现 | a) 裁剪参考仓 Go 后端，作为 sidecar 运行　b) 用 Rust 重写 | a（Agent 和生成引擎原样复用，Rust 只写薄壳） | ✅ 已确认 |
| D3 | 前端与本地服务的接口 | a) sidecar 在 127.0.0.1 上沿用 `/api/v1` REST + WS　b) 全部改成 Tauri invoke | a（88 处 api 调用基本不改） | ✅ 已确认 |
| D4 | 本地数据存储 | a) SQLite + 媒体文件目录　b) 每个项目一个文件夹（json + media）　c) WebView 的 IndexedDB | a | ✅ 已确认 |
| D5 | SQLite 驱动 | `modernc.org/sqlite`（纯 Go）/ `mattn/go-sqlite3`（cgo） | modernc（交叉编译不需要 cgo） | ✅ 已确认 |
| D6 | 目标平台 | macOS / Windows / Linux，可多选 | macOS + Windows（Linux WebKitGTK 对 WebCodecs 支持弱） | ✅ 已确认 |
| D7 | WebCodecs 风险兜底 | 2026-09-30 已在真实 Tauri v2 应用（macOS 26.2 / WKWebView）实测：WebCodecs 四个 API 齐全，H.264/HEVC/VP9/AAC/Opus 编解码均支持；用参考仓同款 mediabunny 跑抽帧、剪辑、裁剪、强制重编码、音频分离（无声 mp4 + WAV），通过 tauri:// 和 127.0.0.1 两种来源全部 PASS | 不再需要换 Electron；风险只剩旧版 macOS，见 D8 | ✅ 已确认 |
| D8 | macOS 最低支持版本 | WKWebView 版本随系统走，旧系统的 WebCodecs（尤其 AudioEncoder/AudioDecoder）可能不全，本机只能测 26.2 | 最低 macOS 14，上线前在 14/15 上各跑一次同一套测试；不通过就提高最低版本 | ✅ 已确认 |

---

## 2. 功能范围

### 2.1 保留

| ID | 功能 | 说明 | 状态 |
|---|---|---|---|
| F1 | 画布编辑器核心 | 画布、连线、缩放平移、撤销重做、快捷键、小地图、右键菜单、自动整理 | ✅ 已确认 |
| F2 | 15 种节点 | 上传（图片/音频/视频）、文字、AI 绘图/视频/音频、图片对比、保存到本地、预览、去字幕、转高清、白模、脚本、3D 导演台 | ✏️ 已修改 |
| F3 | 节点工具条 | 图片：宫格切分、标注、旋转镜像、打光、多角度、全景、人像质感。视频：剪辑、裁剪、续写、重拍、音频分离。音频：截取、变速 | ✅ 已确认 |
| F4 | Agent 对话 | WS 通信 + Eino 运行时，包括 Skill、子 Agent、画布能力清单 | ✅ 已确认 |
| F5 | 生成历史面板 | 全局历史，支持 ZIP 下载和回填到画布 | ✅ 已确认 |
| F6 | 素材库 | 文件夹树、存入素材库、从素材库应用到画布 | ✅ 已确认 |
| F7 | AI 角色库 | 保留，走 newapi：1729 条角色文字数据内置，形象图在线加载并按需缓存（图片地址写进配置）；生成时的 asset://id 通过 newapi 背后的同一火山账号使用 | ✏️ 已修改 |
| F8 | 故事板视图 | `storyboard/*`，纯前端 | ✅ 已确认 |
| F9 | 启动页与项目入口 | 启动直接进画布：打开上一次的项目；没有记录或该项目已删除时，打开最近修改的项目；一个项目都没有时自动新建“未命名项目”并打开。画布左上角“返回”改名为“所有项目”，进入项目列表页（原样复用 CanvasProjectsPage），选中项目后回到画布 | ✏️ 已修改 |
| F10 | Skill JSON 导入导出 | 参考仓里只有超级管理员可见，桌面端是否对所有人开放 | ✏️ 已修改：隐藏 |

### 2.2 删除或关闭

| ID | 功能 | 默认处理 | 状态 |
|---|---|---|---|
| X1 | 发布工作流、分享链接、TV 秀投稿、封面库 | 删除 | ✅ 已确认 |
| X2 | 多人协作（presence、owner 编辑锁、团队） | 删除 | ✅ 已确认 |
| X3 | 积分计费和估价 UI | 删除；是否保留"价格说明"文本另行决定 | ✅ 已确认 |
| X4 | 登录、账户、修改密码、版本检查弹窗 | 删除，改为单个本地用户 | ✅ 已确认 |
| X5 | Seedance 2.0 合规校验 | 保留，生成前强制校验不动；合规调用改走 newapi provider（接口路径配置在 models.json 的 `features.seedanceCompliance`，接口由用户后续在 newapi 上补充）；未配置时入口提示“合规接口未配置” | ✏️ 已修改 |
| X6 | 脚本节点“剧本生成分镜”（Skill shell 执行） | 第一期就做：内置独立 Python 运行时 + 命令白名单（只允许执行 Skill 目录内脚本、只能写任务工作区）；需解决 Windows 没有 bash 的问题 | ✏️ 已修改 |
| X7 | ChatPanel（参考仓的死代码） | 不迁移 | ✅ 已确认 |

---

## 3. 模型中心

| ID | 决策项 | 推荐 | 状态 |
|---|---|---|---|
| M1 | 模型中心存储方式 | 照搬参考仓：SQLite 里建与 MySQL 同结构的 `model_configs` 表，JSON 列（request_template、polling_template、ui_schema、gen_control 等）存 TEXT，用 SQLite json 函数；JSON 文件只用于导入导出和首次安装的种子数据，格式兼容参考仓后台导出（version 1） | ✏️ 已修改 |
| M2 | 上游网关配置 | 照搬参考仓：全局一个 `base_domain` 存 settings 表，所有模型共用（对接 newapi）；不加 providers 层 | ✏️ 已修改 |
| M3 | API Key 存储 | 照搬参考仓：存 SQLite（settings 表）。风险：Key 明文随 data.db 一起被备份、拷贝 | ✏️ 已修改 |
| M4 | 引用模型 ID 的配置（后处理、预设、多角度、打光、脚本、Agent 大模型） | 照搬参考仓：settings 表每项存一个 JSON 值；Agent 大模型存 `agent_backbone_models` 表 | ✏️ 已修改 |
| M5 | 预设、多角度、打光的提示词正文 | 照搬参考仓在 sidecar 内拼接；正文存本地 SQLite，本地可见，设置页可直接编辑 | ✅ 已确认 |
| M6 | 前端写死的模型 ID（重拍、续写、isVideoEditModel） | 照搬参考仓，继续写死；模型中心未配置同名模型时入口提示“未配置” | ✏️ 已修改 |
| M7 | 导入导出 | 客户端不提供导入导出功能；参考仓后台的导出格式只在开发侧用于生成内置种子数据 | ✏️ 已修改 |
| M8 | 客户端内的配置页面 | 把参考仓后台的模型中心和各配置页（后处理、预设、多角度、打光、脚本、Agent 模型）搬进客户端“设置”，UI 调整为客户端风格；模型和配置全部内置，用户只能修改，不能新增或删除 | ✏️ 已修改 |
| M9 | 首次安装的种子数据 | 从参考仓生产后台导出（模型 + settings 配置），去掉 Key，编译进 sidecar 二进制（go:embed），首次启动导入 SQLite | ✅ 已确认 |
| M10 | 用户可修改的字段 | 只开放安全字段：启用/隐藏、显示名、排序、uiSchema 默认值、并发和重试，以及 settings 里的预设提示词、候选模型；请求模板、轮询模板、base_path、图片协议等技术字段只读。保存时后端校验；每个模型和每项配置提供“恢复默认”按钮 | ✅ 已确认 |
| M11 | 升级时内置数据的更新方式 | 按 modelId 合并：新模型自动加入，新版已去掉的模型移除；字段级别上，用户没改过的字段跟随新版，用户改过的保留用户值（表里记录被用户改过的字段） | ✅ 已确认 |

> ⚠️ 2026-09-30 作废：以下 v2 草案（providers 层 + 单一 models.json）已被 M1–M8 的决定取代，数据存储改为照搬参考仓后台，仅保留作历史参考。

JSON 结构草案（v2，已作废）：

```jsonc
{
  "version": 2,
  "providers": {
    "gateway": { "baseUrl": "https://gw.example.com", "auth": { "type": "bearer", "keyRef": "gateway" } }
  },
  "models": [{
    "modelId": "seedance-lite", "type": "video", "name": "Seedance Lite", "provider": "gateway",
    "enabled": true, "hidden": false, "sortOrder": 1, "tags": [],
    "request":  { "method": "POST", "path": "/v1/video/generations", "headers": {}, "query": {},
                  "bodyType": "json", "imageInputFormat": "url", "imageRequestMode": "json",
                  "body": { "prompt": "{{prompt}}" } },
    "polling":  { "method": "GET", "path": "/v1/video/generations/{{task_id}}", "interval": 5, "maxWait": 600,
                  "statusField": "status", "completedValues": ["succeeded"], "failedValues": ["failed"],
                  "urlField": "video_url", "errorField": "error.message" },
    "uiSchema": { "duration": { "label": "时长", "options": [{ "value": 5, "label": "5s" }], "default": 5 } },
    "limits":   { "concurrency": 3, "inputLimits": {}, "inputDuration": null }
  }],
  "features": {
    "reshootModel": "", "continuationModel": "",
    "postprocess": {}, "genImagePresets": {}, "multiAngle": {}, "lightControl": {},
    "script": {}, "agent": { "planner": { "modelId": "", "maxTokens": 4096, "temperature": 0.7 } }
  }
}
```

---

## 4. 数据与存储

| ID | 决策项 | 推荐 | 状态 |
|---|---|---|---|
| S1 | 数据目录 | macOS `~/Library/Application Support/<app>/`，Windows `%APPDATA%\<app>\`；目录下放 `data.db`、`media/`、`thumbs/`、`config/`、`logs/` | ✅ 已确认 |
| S2 | 数据目录允许用户修改 | 是（在设置页修改，修改后迁移数据） | ✅ 已确认 |
| S3 | 媒体引用 | 照搬参考仓：节点里只存 `media://<key>`，由 sidecar 在返回数据时转成 `http://127.0.0.1:<端口>/…`；节点右键菜单新增“在访达/资源管理器中显示”，由 sidecar 换算成真实路径后打开所在文件夹 | ✏️ 已修改 |
| S4 | 媒体文件命名与去重 | 照搬参考仓的 key 布局（按年/月分目录），沿用 assets 表的 content_hash 去重 | ✏️ 已修改 |
| S5 | 生成结果下载 | 生成完成后立即下载到本地；下载失败自动重试 3 次，仍失败则保留上游地址，节点显示“未保存到本地”和重试按钮 | ✅ 已确认 |
| S6 | 画布快照 | 照搬参考仓：每个项目在 `workflow_snapshots` 表存一行 JSON + version，前端按节点级增量提交，sidecar 合并后写回 | ✅ 已确认 |
| S7 | 生成任务恢复 | 照搬参考仓的任务表和恢复逻辑（upstream_called_at、upstream_task_id、ResumeOrphanedTasks），去掉多实例租约；启动时扫描未完成任务，已发出的继续轮询，未发出的重新提交 | ✅ 已确认 |
| S8 | 缩略图 | 直接返回原图（与参考仓 MinIO 模式一致），第一期不生成缩略图 | ✏️ 已修改 |
| S9 | 媒体垃圾回收 | 照搬参考仓的孤儿回收：删除时记入 `orphan_objects` 表，7 天后由后台任务真正删除（配合撤销） | ✅ 已确认 |
| S10 | 项目导出、导入 | 不做（第一期和以后都不做）；备份靠拷贝整个数据目录 | ❌ 否决 |
| S11 | 多窗口 / 多实例 | 单实例、单窗口：再次启动时把已有窗口带到前台（tauri single-instance 插件） | ✅ 已确认 |

---

## 5. 安全

| ID | 决策项 | 推荐 | 状态 |
|---|---|---|---|
| SEC1 | 本地服务只监听 `127.0.0.1`，端口随机 | 是 | ✅ 已确认 |
| SEC2 | 每次启动生成随机 token，所有请求校验 token 和 Origin（防止本机网页调用这个端口） | 是 | ✅ 已确认 |
| SEC3 | 启动 token 的传递 | Tauri 通过 stdin 把启动 token 传给 sidecar，不用命令行参数或环境变量（Key 已按 M3 存 SQLite，不经过 Tauri） | ✅ 已确认 |
| SEC4 | Tauri 权限与 CSP | capabilities 最小授权（取端口和 token、文件对话框、在访达中显示）；CSP 只放行 `127.0.0.1:<port>` 和 F7 角色库图片域名 | ✅ 已确认 |

---

## 6. Agent

| ID | 决策项 | 推荐 | 状态 |
|---|---|---|---|
| A1 | Eino 运行时原样复用；计费换成空实现 | 是 | ✅ 已确认 |
| A2 | Agent 使用的大模型 | 内置：照搬参考仓存 `agent_backbone_models` 表，调用地址和 Key 用全局 base_domain 和 Key。用户只能在内置的文本模型之间切换，其余参数（max_tokens、temperature、thinking 等）不可改 | ✏️ 已修改 |
| A3 | Skill、Skill 分类、Agent 工具目录的来源 | 从参考仓生产库导出，编译进 sidecar（go:embed），首次启动导入 SQLite，升级时随版本更新 | ✅ 已确认 |
| A4 | 用户新建或编辑 Skill | 不允许，只用内置 Skill，隐藏创建入口（SkillCreateModal 不启用） | ✏️ 已修改 |
| A5 | 画布能力清单一致性 | 照搬参考仓：`canvasManifest.json` 在 server 和 web 两处字节级一致，由测试检查，不一致则构建失败 | ✅ 已确认 |

---

## 7. 工程与发布

| ID | 决策项 | 推荐 | 状态 |
|---|---|---|---|
| E1 | 目录结构 | `src-tauri/`、`web/`、`server/`、`seed/`、`docs/`；web 和 server 内部保持与参考仓 apps/web、apps/api 相同的子目录和包路径（不再需要 `config/`） | ✅ 已确认 |
| E2 | 与参考仓的关系 | 不同步，独立演进：从参考仓复制一次后各自发展；只在文档里记录复制时的来源 commit，供追溯 | ✏️ 已修改 |
| E3 | 测试迁移 | Go 测试和前端 vitest 测试一起迁移，Go 测试改成在 SQLite 上运行；删掉已移除功能对应的测试 | ✅ 已确认 |
| E4 | 分发与升级 | macOS 直接发安装包（dmg），Windows 直接发 zip 包（解压即用）；不做自动更新，“设置”里显示当前版本号，新版由手动发给用户，覆盖安装后数据保留、内置模型按 M11 合并 | ✏️ 已修改 |
| E5 | 代码签名 | macOS 和 Windows 都不做开发者签名和公证。macOS 只保留构建时自动带的临时签名（ad-hoc，免费，Apple 芯片运行的最低要求）；随包附一份“首次打开如何放行”的说明（macOS 隐私与安全性放行 / Windows SmartScreen“仍要运行”） | ✏️ 已修改 |
| E8 | Windows zip 包的 WebView2 | 依赖系统自带的 WebView2；启动时检测不到则弹窗提示，并给出微软官方下载链接 | ✅ 已确认 |
| E6 | 应用名称和标识 | 应用名“喵剧AI”，内部标识 `com.miaoju.desktop`（决定数据目录名，定后不可改） | ✅ 已确认 |
| E7 | 字体 | Inter 和 JetBrains Mono 打包到本地，不再从 Google Fonts 在线加载 | ✅ 已确认 |

---

## 8. 实施阶段

按第 1–7 节已确认的决策重排。各阶段已于 2026-09-30 确认；P7 挂起，等 newapi 接口补齐。

| 阶段 | 内容 | 预估 | 状态 |
|---|---|---|---|
| P0 | 验证原型。WebCodecs、mediabunny、127.0.0.1 Range 请求已在 2026-09-30 实测通过（见 D7）；剩余：sidecar 启动握手（stdin 传 token、stdout 回端口）、Agent WebSocket、Windows 上跑同一套测试 | 1 天 | ✅ 已确认 |
| P1 | Go sidecar：从参考仓复制画布相关包；MySQL → SQLite（modernc，合并成一份 schema，方言替换）；本地文件存储 + `media://` 转本地 URL；固定本地用户；计费空实现；去掉 Redis、owner 锁、多实例租约；任务恢复；孤儿回收；token + Origin 校验；sidecar 启动入口；Go 测试迁到 SQLite | 2–2.5 周 | ✅ 已确认 |
| P2 | 内置数据：从生产库导出模型、settings 配置、Skill、Agent 工具目录、AI 角色数据，去掉 Key，go:embed；首次启动导入；升级时按 modelId 字段级合并（M11）；“恢复默认” | 1 周 | ✅ 已确认 |
| P3 | 前端迁移：复制画布模块和 shared；删除协作、发布、分享、计费、登录、ChatPanel；启动直接进画布（F9）；“返回”改“所有项目”；“保存到本地”节点放回菜单；右键“在访达中显示”；下载失败提示与重试（S5）；字体本地化；vitest 测试迁移 | 1.5–2 周 | ✅ 已确认 |
| P4 | 设置页：把参考仓后台的模型中心和各配置页搬进客户端并调整 UI；只开放安全字段（M10）；base_domain 和 Key 填写；Agent 模型切换（A2）；数据目录修改与迁移（S2）；版本号显示 | 1–1.5 周 | ✅ 已确认 |
| P5 | Agent：Eino 运行时接入、WebSocket、内置 Skill；隐藏 Skill 创建和 Skill JSON 导入导出；能力清单一致性测试 | 0.5–1 周 | ✅ 已确认 |
| P6 | 脚本节点“剧本生成分镜”：内置独立 Python 运行时、命令白名单、任务工作区隔离、Windows 无 bash 的处理（X6） | 1–1.5 周 | ✅ 已确认 |
| P7 | Seedance 合规与 AI 角色库走 newapi（X5、F7）：客户端侧先按约定的接口形态实现，newapi 接口补齐后联调 | 0.5 周 + 联调 | ✅ 已确认 |
| P8 | Tauri 壳与打包：sidecar 生命周期、单实例、capabilities 和 CSP、WebView2 检测；macOS dmg（ad-hoc 签名）、Windows zip；首次打开放行说明 | 0.5–1 周 | ✅ 已确认 |

合计约 8–11 周（一个人全职）。比最初估计的 5–7 周多出的部分主要来自：X6 第一期就做（+1–1.5 周）、设置页搬迁（+1–1.5 周）、内置数据与升级合并（+0.5 周）、测试迁移。

---

## 9. 风险

| 风险 | 应对 |
|---|---|
| 旧版 macOS（14、15）的 WKWebView 音频编解码不全 | 上线前在 14 和 15 上各跑一次 WebCodecs 测试；不通过就把最低版本抬高（D8） |
| SQLite 方言替换有遗漏（全仓约 90 处 MySQL 专有语法） | Go 测试迁到 SQLite 上运行（E3） |
| 应用重启后生成任务重复提交、重复扣费 | 照搬参考仓的任务恢复逻辑（S7） |
| API Key 明文存在 data.db（M3） | 本地服务加随机 token 和 Origin 校验（SEC2）；提醒用户不要外传 data.db |
| 不做签名和公证（E5），用户首次打开被系统拦截 | 随包附放行说明；用户量变大后再考虑签名 |
| 不做缩略图（S8），画布图片多时卡顿 | 先观察；需要时在 sidecar 补本地缩略图 |
| 内置 Python 执行 LLM 生成的命令（X6） | 命令白名单 + 只允许写任务工作区；Windows 无 bash 需要单独方案 |
| X5、F7 依赖 newapi 上尚未存在的接口 | 接口形态先在 spec 里约定；未配置时入口提示“未配置”，不影响其他功能 |
| 模型和配置内置（M8、M9）且不做自动更新（E4），newapi 调整模型后用户要手动装新版 | 发版流程里固定一步“重新导出内置数据”；设置页显示版本号便于排查 |
| 不与参考仓同步（E2），参考仓后续的修复不会自动进入客户端 | 文档里记录复制时的来源 commit，需要时手动对比 |

---

## 附录：确认记录

| 日期 | ID | 结论 | 备注 |
|---|---|---|---|
| 2026-09-30 | D1 | ✅ Tauri v2 | |
| 2026-09-30 | D2 | ✅ 裁剪 Go 后端做 sidecar | |
| 2026-09-30 | D3 | ✅ 127.0.0.1 REST + WS | |
| 2026-09-30 | D4 | ✅ SQLite + 媒体目录 | |
| 2026-09-30 | D5 | ✅ modernc.org/sqlite | |
| 2026-09-30 | D6 | ✅ macOS + Windows | 不做 Linux |
| 2026-09-30 | D7 | ✅ 坐定 Tauri，不需要兜底 | 实测通过，见 D7 行 |
| 2026-09-30 | D8 | ✅ 最低 macOS 14 | 上线前在 14/15 上实测，不通过就抬高 |
| 2026-09-30 | F1 | ✅ 编辑器核心全部保留 | |
| 2026-09-30 | F2 | ✏️ 15 种节点全部保留；“保存到本地”放回右键菜单，“图片对比”“预览”继续隐藏 | |
| 2026-09-30 | F3 | ✅ 节点工具条全部保留 | |
| 2026-09-30 | F4 | ✅ Agent 完整保留（对话、Skill、子 Agent、能力清单） | |
| 2026-09-30 | F5 | ✅ 生成历史面板保留 | |
| 2026-09-30 | F6 | ✅ 素材库保留，跨项目共享 | |
| 2026-09-30 | F7 | ❌ AI 角色库第一期不做 | asset:// 绑定平台火山账号；与 X5 同一依赖 |
| 2026-09-30 | F8 | ✅ 故事板保留 | |
| 2026-09-30 | F9 | ✏️ 启动直接进画布（上次项目 → 最近修改 → 自动新建）；“返回”改为“所有项目”进入列表页 | |
| 2026-09-30 | F10 | ✏️ Skill JSON 导入导出隐藏，只用内置 Skill | 未采纳推荐（开放） |
| 2026-09-30 | X1 | ✅ 发布、分享、TV 秀、封面库全部删除 | |
| 2026-09-30 | X2 | ✅ 多人协作删除（含 owner 锁、心跳） | |
| 2026-09-30 | X3 | ✅ 积分、估价、价格说明全部删除；models.json 的 pricing 字段保留不用 | |
| 2026-09-30 | X4 | ✅ 删除登录，固定单个本地用户（userID=1） | |
| 2026-09-30 | X5 | ✏️ 保留，走 newapi（用户后续补接口） | 未采纳推荐（关闭） |
| 2026-09-30 | F7 | ✏️ 改回保留，走 newapi | 覆盖此前“第一期不做” |
| 2026-09-30 | X6 | ✏️ 第一期就做：内置 Python + 白名单 | 未采纳推荐；总工期 +1–1.5 周 |
| 2026-09-30 | X7 | ✅ ChatPanel 不迁移 | |
| 2026-09-30 | M1 | ✏️ 模型存 SQLite（与参考仓 MySQL 同结构，JSON 列），JSON 文件只做导入导出和种子 | 替代原“单一 models.json” |
| 2026-09-30 | M2 | ✏️ 照搬参考仓，全局 base_domain，不加 providers 层 | 模型中心整体原则：存储与参考仓后台一致，只调整 UI |
| 2026-09-30 | M3 | ✏️ Key 照搬参考仓存 SQLite | 已提示明文风险 |
| 2026-09-30 | M4 | ✏️ 配置存 settings 表、agent_backbone_models 表 | |
| 2026-09-30 | M7 | ✏️ 沿用参考仓导出格式，不做转换 | |
| 2026-09-30 | M8 | ✏️ 后台配置页搬进客户端“设置”，UI 调整，支持直接配置 | |
| 2026-09-30 | M5 | ✅ 提示词本地可见、可编辑 | |
| 2026-09-30 | M6 | ✏️ 照搬参考仓，继续写死 | |
| 2026-09-30 | M9 | ✅ 生产后台导出，去 Key，go:embed 内置 | |
| 2026-09-30 | M7 | ✏️ 客户端不做导入导出 | |
| 2026-09-30 | M8 | ✏️ 模型和配置全部内置，用户只能修改，不能增删 | |
| 2026-09-30 | M10 | ✅ 只开放安全字段，技术字段只读，提供恢复默认 | 新增项 |
| 2026-09-30 | M11 | ✅ 升级按 modelId 字段级合并 | 新增项 |
| 2026-09-30 | S1 | ✅ 系统应用数据目录 | |
| 2026-09-30 | S2 | ✅ 允许在设置页修改数据目录，自动迁移 | |
| 2026-09-30 | S4 | ✏️ 照搬参考仓 key 布局 + content_hash 去重 | 撤回“文件名用 hash” |
| 2026-09-30 | S3 | ✏️ media:// + 右键“在访达/资源管理器中显示” | 曾选绝对路径，因与 S2 冲突改回 |
| 2026-09-30 | S5 | ✅ 立即下载，失败重试 3 次后提示并可手动重试 | |
| 2026-09-30 | S6 | ✅ 快照照搬参考仓 | |
| 2026-09-30 | S7 | ✅ 任务恢复照搬参考仓，去掉多实例租约 | |
| 2026-09-30 | S8 | ✏️ 直接返回原图，不生成缩略图 | 未采纳推荐；图多时可能卡顿，后续可再加 |
| 2026-09-30 | S9 | ✅ 照搬参考仓延迟回收（7 天） | |
| 2026-09-30 | S10 | ❌ 项目导出导入永远不做 | 未采纳推荐（第二期补） |
| 2026-09-30 | S11 | ✅ 单实例单窗口 | |
| 2026-09-30 | SEC1 | ✅ 只监听 127.0.0.1，端口随机 | |
| 2026-09-30 | SEC2 | ✅ 每次启动随机 token + Origin 校验 | |
| 2026-09-30 | SEC3 | ✅ 启动 token 走 stdin | |
| 2026-09-30 | SEC4 | ✅ 最小授权 + 严格 CSP | |
| 2026-09-30 | A1 | ✅ Eino 运行时原样复制，计费空实现 | |
| 2026-09-30 | A2 | ✏️ 内置，用户只能切换模型，其他参数不可改 | |
| 2026-09-30 | A3 | ✅ Skill 和工具目录从生产库导出内置 | |
| 2026-09-30 | A4 | ✏️ 不允许用户新建或编辑 Skill | |
| 2026-09-30 | A5 | ✅ 能力清单两份字节级一致，测试检查 | |
| 2026-09-30 | E1 | ✅ 目录结构 src-tauri / web / server / seed / docs | |
| 2026-09-30 | E2 | ✏️ 不同步，独立演进 | 未采纳推荐 |
| 2026-09-30 | E3 | ✅ 测试一起迁移，Go 测试跑 SQLite | |
| 2026-09-30 | E4 | ✏️ mac 发 dmg、Windows 发 zip；不做自动更新 | |
| 2026-09-30 | E5 | ✏️ 不做签名和公证（macOS 仅 ad-hoc），附首次打开说明 | 未采纳推荐 |
| 2026-09-30 | E8 | ✅ 用系统自带 WebView2，缺失时提示 | 新增项 |
| 2026-09-30 | E6 | ✅ 喵剧AI / com.miaoju.desktop | |
| 2026-09-30 | E7 | ✅ 字体打包到本地 | |
| 2026-09-30 | P2 | 种子数据已从生产库只读导出到 `seed/`（7 个 JSONL，见 seed/README.md），已扫描不含密钥 | 3D 导演台内置模型库在生产库为空 |
| 2026-09-30 | P7 | newapi 接口（Seedance 合规、AI 角色库图片）后续再补，P7 挂起 | |
| 2026-09-30 | P0–P8 | ✅ 实施阶段确认 | P7 挂起 |
