# Seedance 合规校验改走内置模型 `seedance-asset` 设计

日期：2026-10-07　状态：待用户审阅
关联：主 spec `docs/specs/2026-09-29-canvas-desktop-spec.md` 的 X5、P7；对象存储设计 `2026-10-07-object-storage-design.md`。

## 1. 背景与已确认的决定

newapi 已补齐合规接口，形态是一个"图片生成"模型：

```
POST {base_domain}/v1/images/generations
Authorization: Bearer <api key>
{"model":"seedance-asset","image":"<公网 URL>","metadata":{"asset_type":"Video"}}

→ 200 {"created":…, "data":[{"url":"<方舟素材副本>"}],
       "metadata":{"asset_id":"asset-…","asset_type":"Video","group_id":"group-…"}}
→ 失败：直接返回错误信息
```

| # | 决定 | 来源 |
|---|---|---|
| A1 | 合规接口配置为**内置模型** `seedance-asset`，写在 `seed/model_configs.jsonl`，不再使用 `seedance_compliance_config` | 用户 |
| A2 | **同步**：响应里有 `metadata.asset_id` 即视为通过；失败时上游直接返回错误，**原样显示给用户** | 用户 Q1 |
| A3 | 图片、视频、音频都走这个模型，URL 一律放在 `image` 字段；`metadata.asset_type` 取 `Image` / `Video` / `Audio`。图片也带 `asset_type`（三类格式一致） | 用户 Q2、Q4 |
| A4 | 模型 `hidden=1`，不出现在画布的模型选择里 | 用户 Q3 |
| A5 | 素材先经对象存储发布成签名 URL（`MediaPublisher`）；未配置对象存储时返回现有的引导文案 | 用户 Q4 + 对象存储设计 |
| A6 | 合规校验**直接按模型配置同步调用上游**，不进生成队列（不产生生成任务、资产行、画布回写） | 用户确认 |
| A7 | 素材组由 newapi 管理：客户端删除建组、后台轮询、1 小时重试兜底，以及 `seedance_compliance_config` 配置项 | 由 A1、A2 推出 |

不做：
- 不迁移旧数据（旧流程从未启用，`seedance_assets` 里没有存量记录）。
- 不删除数据库列：`seedance_assets.version`、`users.seedance_group_*` 保留不用，避免为空跑写迁移。
- 不改前端交互：菜单、角标、提示文字保持原样。

## 2. 内置模型

在 `seed/model_configs.jsonl` 追加一行（id 51），其他字段参照现有隐藏模型：

```json
{"id": 51, "name": "Seedance 合规校验", "type": "image", "hidden": 1, "enabled": 1,
 "model_id": "seedance-asset", "base_path": "/v1/images/generations", "ui_schema": null,
 "request_template": {"model": "seedance-asset", "image": "{{image}}", "metadata": {"asset_type": "{{asset_type}}"}},
 "polling_template": null, "image_input_format": "url", "image_request_mode": "json",
 "billing_config": {"items": [], "flatFee": 0, "freezeEstimate": 0},
 "gen_control": {"modules": {"canvas": {"maxRetries": 0, "retryIntervalSec": 0}}, "concurrency": 0}, …}
```

- `image_input_format` 设为 `url`：这个模型不经过 `prepareImageInputs`，该字段不起作用。`TestSeedImagesAreInline` 要按 `model_id` 排除它。
- 上游地址与 Key 用 `models.ResolveUpstream(model, settings)` 解析：与其他模型一致，没有给这个模型单独配置上游时，用全局 base_domain + API Key。这一条取代 X5 原来"合规固定走全局上游"的说法。

## 3. 后端改造

### 3.1 `server/internal/seedance`（重写 client.go）

删除 `Paths`、`ComplianceConfig`、`LoadComplianceConfig`、`SettingComplianceConfig`、`CreateAssetGroup`、`CreateAsset`、`GetAsset`。新增：

```go
const ModelID = "seedance-asset"

// Registrar 调用内置模型 seedance-asset，把一个公网 URL 注册为 Seedance 素材。
type Registrar struct { /* models 仓库、settings、*http.Client */ }

func NewRegistrar(models ModelLookup, settings models.UpstreamSettings, httpClient *http.Client) *Registrar

// Configured：模型存在且启用，并且解析得到上游地址与 Key。
func (r *Registrar) Configured() bool

// Register 同步注册，返回 asset_id。失败时返回 *UpstreamError，Message 为上游原文（优先取
// error.message，取不到就用响应体前 500 字），供界面原样显示。
func (r *Registrar) Register(ctx context.Context, url, assetType string) (assetID string, err error)
```

- 请求体：用 `upstream.InterpolateTemplate(model.RequestTemplate, {"image": url, "asset_type": assetType})` 生成，POST 到 `base + model.BasePath`。
- 成功的判定：HTTP 2xx，并且 `metadata.asset_id` 不为空；2xx 但没有 `asset_id`，也按失败处理，错误信息为「上游未返回 asset_id」加上响应原文。
- 超时 300 秒（与生成链路一致；视频注册可能较慢）。
- 错误信息中不能出现 API Key。

### 3.2 `server/internal/library/seedance_service.go`（简化）

- `seedanceAPI` 接口改为 `{ Configured() bool; Register(ctx, url, assetType string) (string, error) }`。
- `Submit`：已通过（`status=active` 且有 `asset_id`）就直接返回；否则 `signedURL`（对象存储发布）→ `Register`：
  - 成功：Upsert `status=active`、`asset_id`；
  - 失败：Upsert `status=failed_permanent`、`error=上游原文`，并返回错误。HTTP 层回 400，前端弹出原文。用户可以再点一次校验重试，因为 Submit 只跳过 active 状态。
- 删除：`RunWorker`、`ReconcileAll`、`reconcileOne`、`ensureGroup`、1 小时重试窗口、`config()` 和版本号逻辑（写入时 `version` 固定为 1）、`seedanceGroupStore` 依赖及其仓库文件 `seedance_groups_repo.go`。
- `Configured()` 改为 `client.Configured()`；`ErrSeedanceDisabled` 文案改为「合规校验模型 seedance-asset 未启用或上游未配置」。
- `Resolve`、`Status`、公共资产（AI 角色）逻辑保持不变。

### 3.3 装配

- `cmd/sidecar/wire.go`：`seedance.NewClient(...)` 换成 `seedance.NewRegistrar(modelsRepo, settingsRepo, &http.Client{Timeout: 300 * time.Second})`；`NewSeedanceService` 去掉 group store 参数。
- `cmd/sidecar/main.go:199`：删除 `a.seedance.RunWorker(ctx)` worker。

### 3.4 前端

只做一处核对：`useMediaNodeMenu.tsx:143` 对 `pending`/`failed` 每 5 秒轮询；新流程只会写入 `active` / `failed_permanent`，不会触发轮询，所以无需改动。`failed_permanent` 角标显示 `error` 原文（`:263`），符合 A2。

## 4. 测试

- `seedance`：用 `httptest.Server` 覆盖：请求体逐字段正确（model、image、metadata.asset_type）、Authorization 头、成功取到 asset_id、2xx 但没有 asset_id、4xx/5xx 时取到 `error.message` 原文、非 JSON 错误体截断、模型未启用或不存在时 `Configured()=false`、错误信息里没有 Key。
- `library`：Submit 成功写 active；失败写 failed_permanent 和原文；已 active 跳过；未配置对象存储时报引导文案；未配置模型时报 `ErrSeedanceDisabled`。删除轮询、重试、建组相关的旧测试。
- `http`：`TestSeedanceVerify*` 适配同步结果（已通过直接返回 active）。
- `builtin`：种子包含 `seedance-asset` 且 `hidden=1`；`TestSeedImagesAreInline` 排除它。
- `cmd/sidecar`：装配测试确认没有合规 worker，`Configured()` 跟随模型状态。
- 人工验收：配置好对象存储后，对一张图、一段视频分别点「合规校验」，角标变为已通过；再用这个素材跑一次 seedance 生成，请求里应该是 `asset://…`。

## 5. 文档收尾

- 主 spec：X5 行追加「2026-10-07：改为内置隐藏模型 seedance-asset，同步返回 asset_id，见本文」；P7 行状态改为「✅ 合规部分完成（AI 角色库仍不做）」；变更记录追加一行。
- `docs/manual-acceptance-checklist.md` 追加上面的人工验收步骤。
