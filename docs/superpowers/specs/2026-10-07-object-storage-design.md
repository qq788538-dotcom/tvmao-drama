# 用户自配对象存储（解决 S12）设计

日期：2026-10-07　状态：待用户审阅
关联：`docs/specs/2026-09-29-canvas-desktop-spec.md` 的 S12；事实依据 `docs/superpowers/plans/p1-research.md` 第 1 节。

## 1. 目标与已确认的决定

桌面端媒体都在本机磁盘，地址是 `http://127.0.0.1:<端口>/media/…`，上游拉取不到。本设计分两部分解决：

| # | 决定 | 来源 |
|---|---|---|
| D1 | **图片一律内嵌**：内置模型的 `image_input_format` 从 `url` 改为 `data_uri`，图片不再需要公网地址。哪个模型实测不支持，只把它的种子改回 `url`，代码不动 | 用户提出，2026-10-07 |
| D2 | 改成 `data_uri` 而不是裸 `base64`：模板大多把图片插进 `image_url.url` 这类字段，方舟和 OpenAI 兼容接口在这里认带前缀的 data URI | 用户确认 |
| D3 | **视频、音频走用户自己的对象存储**：只支持 **火山 TOS** 和 **阿里云 OSS**；用户在设置页填写并保存配置，存储和流量费用由用户自己承担 | 用户提出 |
| D4 | 技术路线 A：两个官方 SDK。TOS 用 `github.com/volcengine/ve-tos-golang-sdk/v2`（与参考仓 `apps/api/internal/assets/tos.go` 相同），OSS 用 `github.com/aliyun/alibabacloud-oss-go-sdk-v2` | 用户确认 |
| D5 | 桶保持私有，交给上游的是 **24 小时有效的预签名 GET URL**（沿用 `inputMediaURLExpiry`） | 用户确认 |
| D6 | 对象路径按内容哈希命名 `<前缀>/<sha256>.<扩展名>`，上传前先 HEAD 查询，已存在就跳过（同一个文件只上传一次） | 用户确认 |
| D7 | 程序不删除已上传的对象；设置页提示用户给桶配置生命周期规则（例如 7 天后自动删除） | 用户确认 |
| D8 | 配置存放在 settings 表，存法与 `upstream_api_key` 一致（数据库本身已加密）；SecretKey 只回显掩码 | 用户确认 |
| D9 | 未配置对象存储时行为与现在一致：调用上游前失败，报错信息改为引导去设置页配置 | 用户确认 |

不做：
- 不支持其他服务商（S3、R2、COS、MinIO）。
- 不做上传进度条。
- 不做"上传前压缩"。
- 不管理已上传对象（不提供列表，也不提供删除）。

## 2. 第一部分：图片改为 data_uri（只改种子数据）

修改 `seed/model_configs.jsonl`，再运行 `scripts/sync-seed.sh` 同步到 `server/internal/builtin/seeddata/`。种子哈希变化后，启动时会按现有合并逻辑（`builtin/apply.go`）更新已安装用户的库，没被用户改过的字段一律采用新值。

改动范围：`image_input_format = "url"` 且 `image_request_mode = "json"` 的全部模型，共 22 个（视频 16；图片 3：`z-image-rh`、seedream 1k/2k；音频 2；文本 1：`doubao-seed-2-0-lite`），全部改为 `data_uri`。

不改的模型：
- `gpt-image-2`（id 9，`openai_image_edits`）：走 multipart 文件上传，`url` 模式下已经是读本地字节（`image_inputs.go:169-178`），不需要公网地址。
- gemini 两个（id 19、20）：已经是 `base64`，模板按裸 base64 编写。
- `gpt-5.5`：已经是 `data_uri`。

风险：请求体变大。单张图受现有 `maxInputImageBytes` 限制；如果某个模型因为请求体过大或不认 data URI 而失败，就把这个模型的种子单独改回 `url`。改回后，这个模型的图片会走第二部分的对象存储上传（上传逻辑不区分媒体类型）。

## 3. 第二部分：对象存储

### 3.1 新包 `server/internal/objectstore`

```go
// Config 是用户在设置页保存的配置，整体存在 settings 表的 object_storage 键里（JSON 格式）。
type Config struct {
    Provider  string `json:"provider"`  // "tos" | "oss"
    Endpoint  string `json:"endpoint"`  // 例：tos-cn-beijing.volces.com / oss-cn-hangzhou.aliyuncs.com
    Region    string `json:"region"`    // 例：cn-beijing / cn-hangzhou
    Bucket    string `json:"bucket"`
    Prefix    string `json:"prefix"`    // 默认 "miaoju-inputs"
    AccessKey string `json:"accessKey"`
    SecretKey string `json:"secretKey"`
}

// Client：对每个服务商 SDK 的最小封装，便于测试时替换。
type Client interface {
    Exists(ctx context.Context, key string) (bool, error)
    Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    Delete(ctx context.Context, key string) error // 只给"测试连接"使用
}

func NewClient(cfg Config) (Client, error) // 按 Provider 分发到 tosClient / ossClient

// Publisher：把本机媒体发布成上游能下载的 URL。
type Publisher struct { /* settings 仓库、本地存储、已缓存的 client */ }

var ErrNotConfigured = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")

// Publish：读取本地对象 → 计算 sha256 → 检查是否已存在 → 上传 → 返回预签名 URL。
func (p *Publisher) Publish(ctx context.Context, mediaKey string) (string, error)
func (p *Publisher) Configured() bool
```

要点：
- `tosClient` 的 client 构造和预签名照搬参考仓 `tos.go`（`NewClientV2` + `PreSignedURL`）。`ossClient` 用 OSS v2 SDK 的 `PutObject` / `HeadObject` / `Presign`。
- **每次调用都从 settings 读取配置**，用户保存后立即生效，无需重启。client 按配置内容缓存，配置变化时重新构造。
- 哈希需要先完整读一遍文件：先流式读取算出 sha256，再重新打开文件上传。本地磁盘读取开销可以忽略，内存占用也不随文件大小增长。
- 扩展名和 `Content-Type` 优先取本地对象 key 的扩展名，取不到时嗅探文件头。
- 同一个 key 的并发 Publish 用 `singleflight` 合并，避免同时上传两次。

### 3.2 接入三个位置

| 位置 | 现状 | 改后 |
|---|---|---|
| 生成管线 `generation.InputMediaSigner`（`input_media.go`） | 没有注入实现，签出的本机 URL 被 `ensureNoLocalMedia` 拦下 | 注入 `objectstore` 实现：遍历 inputs，遇到本机媒体就调用 `Publish` 换成预签名 URL。**跳过 `images` / `image` 字段**，条件是模型 `image_input_format ≠ url`，或请求方式是 edits（这些图片后续由 `prepareImageInputs` 读本地字节）。为此在接口里加一个参数：`SignInputMedia(ctx, inputs, opts SignOptions)`，其中 `SignOptions{SkipImages bool}`，由 `execution_plan.go` / `service.go` 按模型计算后传入 |
| Agent `LLMMediaResolver`（`media_llm_local.go`） | 视频、音频返回 `ErrMediaNeedsPublicURL` | 图片仍然转 data URI；视频、音频调用 `Publish`。未配置时返回新的引导文案 |
| Seedance 合规校验（`library/seedance_service.go` 的 `signedURL`） | 签出回环地址就报 `ErrLocalMediaNoPublicURL` | 遇到回环地址时改为调用 `Publish`；未配置时返回引导文案 |

未配置时：`InputMediaSigner` 实现保持现有行为（签出本机 URL）。`ensureNoLocalMedia` 仍在调用上游前拦截，`ErrLocalMediaUnreachable` 的文案改为与 `ErrNotConfigured` 一致的引导。已配置但上传失败时，报错格式为"上传到对象存储失败：<原因>"，原因里不能包含 AccessKey 或 SecretKey。

### 3.3 设置接口（`server/internal/http/settings_object_storage_handlers.go`）

按 `settings_general_handlers.go` 的写法注册到 `settingsRouteRegistrars`：

- `GET /settings/object-storage` → `{configured, provider, endpoint, region, bucket, prefix, accessKey, secretKeySet, secretKeyMasked}`。
- `PUT /settings/object-storage` → 严格解析请求体（`scDecodeStrict`），字段同 Config。`secretKey` 省略或为空时保留原值。校验规则：provider 只能是 tos 或 oss；endpoint、region、bucket、accessKey 不能为空；首次保存必须带 secretKey；字段去掉首尾空格，不能含空白字符，长度不超过 512；endpoint 去掉 `https://` 前缀和末尾的 `/`。
- `DELETE /settings/object-storage` → 清空配置，恢复为未配置状态。
- `POST /settings/object-storage/test` → 请求体与 PUT 相同（可以测试尚未保存的配置；secretKey 为空时用已保存的值）。依次执行：上传一个约 32 字节的文件 `<前缀>/.miaoju-test-<随机>.txt` → 生成预签名 URL → **用普通 HTTP GET 下载并核对内容**（证明公网能拉取）→ 删除。返回 `{ok, step, message}`，`step` 标明失败的环节（上传 / 签名 / 公网下载 / 删除），方便用户排查。

`object_storage` 不在种子数据里，也不受 builtin policy 约束，与 `upstream_api_key` 一样由专门的 handler 直接读写。

### 3.4 前端：配置页新增「对象存储」卡片

位置：`web/src/modules/settings/pages/GeneralPage.tsx`，放在"数据目录"卡片之前。新建组件 `components/ObjectStorageCard.tsx`，接口函数放在 `api.ts`。

- 服务商下拉框：火山引擎 TOS / 阿里云 OSS。切换时更新 Endpoint、Region 的占位示例（只是 placeholder，不自动填值）。
- 输入框：Endpoint、Region、Bucket、路径前缀（默认 `miaoju-inputs`）、AccessKey、SecretKey（密码框；已保存时占位显示掩码，留空表示不修改）。
- 按钮：「测试连接」「保存」「清除配置」（清除前弹框确认）。
- 卡片顶部用一行说明：「视频、音频参考素材会上传到你自己的对象存储，并用 24 小时有效的私有链接交给模型。建议给桶设置生命周期规则（如 7 天自动删除）以控制费用。」
- 状态标签：已配置 / 未配置。

## 4. 测试

- `objectstore`：用假的 Client 做单元测试，覆盖哈希命名、已存在时跳过、并发合并、未配置报错、配置变化后重建 client。真实 SDK 的集成测试需要设置环境变量 `MIAOJU_TEST_TOS_*` / `MIAOJU_TEST_OSS_*` 才运行，没设置时跳过。
- 生成管线：`SkipImages` 两种取值下都要测，确认 data_uri 模型的图片不会被上传、视频会被上传；未配置时仍然返回 `ErrLocalMediaUnreachable`。
- Agent 和合规校验：视频、音频分别测已配置和未配置两种情况。
- 设置接口：校验规则、SecretKey 保留和掩码、测试连接每一步的失败（用假 Client 注入）。
- 种子数据：`go test ./internal/builtin/` 校验副本一致；新增一条断言，确认除 edits 模式和 gemini 外不再有 `url`。
- 前端：`ObjectStorageCard` 的渲染、保存、测试结果展示。
- 人工验收：用真实 TOS 桶和 OSS 桶各跑一次 seedance 参考视频生成；另外各跑一次 data_uri 后的 seedream 参考图生成。结果补进 `docs/manual-acceptance-checklist.md`。

## 5. 收尾

- 主 spec 的 S12 改为 ✅，指向本文；同时在附录追加一行决策记录。
- `p1-research.md` 第 1 节的"待用户决定"改为指向本文。
