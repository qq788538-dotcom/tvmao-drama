# 用户自配对象存储（TOS/OSS）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 图片改为 data_uri 内嵌；视频/音频上传到用户在设置页配置的 TOS 或 OSS 桶，以 24 小时预签名 URL 交给上游，解决 spec S12。

**Architecture:** 新包 `server/internal/objectstore` 负责配置读写、两个 SDK 适配器、按 sha256 去重的 `Publisher` 和连接测试 `Probe`。生成管线、Agent、Seedance 合规校验各自定义一个最小接口 `MediaPublisher{Configured() bool; Publish(ctx, key) (string, error)}`，由 `*objectstore.Publisher` 满足，在 sidecar 装配时注入。设置页 HTTP 接口 + 前端「对象存储」卡片负责配置。

**Tech Stack:** Go 1.26、`github.com/volcengine/ve-tos-golang-sdk/v2`、`github.com/aliyun/alibabacloud-oss-go-sdk-v2`、chi、React + antd + @tanstack/react-query、vitest。

**Spec:** `docs/superpowers/specs/2026-10-07-object-storage-design.md`

## Global Constraints

- 只支持 `provider` = `"tos"` | `"oss"`。
- 配置整体以 JSON 存在 settings 表键 `object_storage`；SecretKey 明文永不出 sidecar（GET 只回 `secretKeySet` + `secretKeyMasked`，复用 `maskAPIKey`）。
- 对象 key：`<prefix>/<sha256 hex>.<ext>`；prefix 默认 `miaoju-inputs`，去首尾 `/`。
- 预签名有效期 24 小时（`24 * time.Hour`）。
- 程序不删除已上传对象（`Delete` 只给连接测试用）。
- 未配置时报错文案统一为：`未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写`。
- 上传失败的报错前缀 `上传到对象存储失败：`，错误中不得出现 AccessKey / SecretKey 原文。
- 种子改动只改 `seed/`，再跑 `scripts/sync-seed.sh`；不手改 `server/internal/builtin/seeddata/`。
- 本机网络走代理，`go get` 必须带 `GOPROXY=https://goproxy.cn,direct`。
- 所有 Go 命令在 `server/` 下执行；前端命令在 `web/` 下执行。
- 用户可见文案、注释用中文，与周边代码一致。

## Review Focus

1. **配置保存后不重启即生效**：用户改完配置马上点生成，Publisher 必须用新配置（每次调用读 settings，配置变化重建 client）。→ Task 2 `TestPublisherRebuildsClientWhenConfigChanges`。
2. **同一视频被多个节点同时生成**：并发 Publish 同一 key 只上传一次。→ Task 2 `TestPublisherConcurrentSameKeyUploadsOnce`。
3. **SecretKey 留空保存**：用户只改 Bucket、不重输 SecretKey，原值必须保留；首次保存不带 SecretKey 必须 400。→ Task 6 `TestObjectStoragePutKeepsSecret`、`TestObjectStorageFirstSaveRequiresSecret`。
4. **Endpoint 带 `https://` 或末尾 `/`**：用户从控制台复制的地址要能直接用。→ Task 2 `TestNormalizeConfig`。
5. **data_uri 模型的图片不被上传、edits 模型的图片也不被上传**，而同一请求里的视频会被上传。→ Task 4 `TestSignInputMediaSkipsImagesForInlineFormats`。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| Create `server/internal/objectstore/config.go` | `Config`、`Normalize`、`Load`/`Save`/`Clear`（读写 settings） |
| Create `server/internal/objectstore/client.go` | `Client` 接口、`NewClient` 分发、`ErrNotConfigured` |
| Create `server/internal/objectstore/publisher.go` | `Publisher`：读本地对象 → sha256 → Exists → Put → Presign |
| Create `server/internal/objectstore/probe.go` | `Probe`：连接测试四步 |
| Create `server/internal/objectstore/tos.go` / `oss.go` | 两个 SDK 适配器 |
| Create `server/internal/objectstore/*_test.go` | 单测 + 环境变量门控的集成测试 |
| Modify `server/internal/generation/input_media.go`、`image_inputs.go`、`service.go`、`execution_plan.go` | 用 `MediaPublisher` 替换 `InputMediaSigner`，加 `skipImages` |
| Modify `server/internal/agent/media_llm.go`、`media_llm_local.go` | resolver 视频/音频走 Publisher |
| Modify `server/internal/library/seedance_service.go` | 回环地址时走 Publisher |
| Create `server/internal/http/settings_object_storage_handlers.go` (+test) | GET/PUT/DELETE/POST test |
| Modify `server/internal/http/settings_center.go` | `SettingsCenterDeps.ObjectStore` |
| Modify `server/cmd/sidecar/wire.go`、`main.go`、`agent_wiring.go` | 装配 |
| Modify `seed/model_configs.jsonl`、`server/internal/builtin/seed_test.go` | 图片改 data_uri |
| Create `web/src/modules/settings/components/ObjectStorageCard.tsx` (+test) | 前端卡片 |
| Modify `web/src/modules/settings/api.ts`、`pages/GeneralPage.tsx` | 接口函数、挂卡片 |

**依赖与并发**：Task 1、2、4、5、7 互不依赖，可并行（各自 worktree）。Task 3 依赖 Task 2 的 `Client` 接口；Task 6 依赖 Task 2 的 `Config`/`Load`/`Save`/`Probe`；Task 8 依赖全部。

---

### Task 1: 种子数据——图片改 data_uri

**Files:**
- Modify: `seed/model_configs.jsonl`
- Modify（由脚本生成）: `server/internal/builtin/seeddata/model_configs.jsonl`
- Test: `server/internal/builtin/seed_test.go`

**Interfaces:** 无（只改数据）。

- [ ] **Step 1: 写失败测试**，追加到 `server/internal/builtin/seed_test.go`：

```go
// 图片改为内嵌（对象存储设计 D1/D2）：除 edits 模式（multipart 读本地字节）和
// 模板按裸 base64 编写的模型外，不得再有 url。
func TestSeedImagesAreInline(t *testing.T) {
	s, err := LoadEmbeddedSeed()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Models {
		if m.ImageInputFormat != "url" {
			continue
		}
		if m.ImageRequestMode == "openai_image_edits" {
			continue
		}
		t.Errorf("模型 %s 仍是 image_input_format=url，应为 data_uri", m.ModelID)
	}
}
```

> 先看 `seed_test.go:11` 的 `TestLoadEmbeddedSeed`，确认加载函数与字段名（`LoadEmbeddedSeed`、`s.Models`、`ImageInputFormat`、`ImageRequestMode`），名字不同则照实际改。

- [ ] **Step 2: 运行确认失败**

Run: `cd server && go test ./internal/builtin/ -run TestSeedImagesAreInline`
Expected: FAIL，列出 22 个模型。

- [ ] **Step 3: 改种子**（在仓库根执行）：

```bash
python3 - <<'EOF'
import json
p='seed/model_configs.jsonl'
out=[]
for line in open(p, encoding='utf-8'):
    if not line.strip():
        out.append(line); continue
    m=json.loads(line)
    if m.get('image_input_format')=='url' and m.get('image_request_mode')=='json':
        line=line.replace('"image_input_format": "url"','"image_input_format": "data_uri"',1)
    out.append(line)
open(p,'w',encoding='utf-8').writelines(out)
EOF
grep -c '"image_input_format": "url"' seed/model_configs.jsonl   # 期望 1（gpt-image-2）
scripts/sync-seed.sh
```

用字符串替换而不是 `json.dumps`，保证其余字段的键顺序和格式逐字节不变（`key_order.go` 依赖原顺序）。

- [ ] **Step 4: 更新 `seed/README.md`**：在 `model_configs.jsonl` 行的说明末尾追加「2026-10-07：除 gpt-image-2（edits）与 gemini（base64）外，`image_input_format` 统一为 `data_uri`（对象存储设计 D1）」。

- [ ] **Step 5: 运行测试**

Run: `cd server && go test ./internal/builtin/...`
Expected: PASS（含 `TestEmbeddedSeedMatchesRepoSeed`）。

- [ ] **Step 6: 提交**

```bash
git add seed/ server/internal/builtin/
git commit -m "feat(seed): 内置模型参考图改为 data_uri 内嵌"
```

---

### Task 2: objectstore 核心（Config、Client 接口、Publisher、Probe）

**Files:**
- Create: `server/internal/objectstore/config.go`、`client.go`、`publisher.go`、`probe.go`
- Test: `server/internal/objectstore/config_test.go`、`publisher_test.go`、`probe_test.go`

**Interfaces:**
- Produces:
  - `type Config struct{ Provider, Endpoint, Region, Bucket, Prefix, AccessKey, SecretKey string }`（json tag 见下）
  - `func (c Config) Normalize() (Config, error)`
  - `const SettingKey = "object_storage"`
  - `type SettingsStore interface{ Get(string) (string, error); Set(string, string) error }`
  - `func Load(s SettingsStore) (Config, bool, error)`（bool=是否已配置）
  - `func Save(s SettingsStore, c Config) error`、`func Clear(s SettingsStore) error`
  - `type Client interface{ Exists; Put; PresignGet; Delete }`（签名见下）
  - `type ClientFactory func(Config) (Client, error)`
  - `var NewClient ClientFactory`（Task 3 在 `init()` 里填 tos/oss；本任务给默认实现返回"不支持的服务商"）
  - `var ErrNotConfigured`
  - `type ObjectReader interface{ GetObject(ctx context.Context, key string) (io.ReadCloser, error) }`
  - `func NewPublisher(s SettingsStore, objects ObjectReader, factory ClientFactory) *Publisher`
  - `func (p *Publisher) Configured() bool`
  - `func (p *Publisher) Publish(ctx context.Context, mediaKey string) (string, error)`
  - `type ProbeResult struct{ OK bool; Step, Message string }`（json: `ok`/`step`/`message`）
  - `func Probe(ctx context.Context, c Client, prefix string, get func(ctx context.Context, url string) ([]byte, error)) ProbeResult`
  - `func (p *Publisher) Probe(ctx context.Context, c Config) ProbeResult`（构造 client + 用 `http.DefaultClient` 下载）

- [ ] **Step 1: 写 `config_test.go`**

```go
package objectstore

import (
	"strings"
	"testing"
)

type memSettings map[string]string

func (m memSettings) Get(k string) (string, error) { return m[k], nil }
func (m memSettings) Set(k, v string) error       { m[k] = v; return nil }

func validCfg() Config {
	return Config{Provider: "tos", Endpoint: "tos-cn-beijing.volces.com", Region: "cn-beijing",
		Bucket: "b", AccessKey: "AK", SecretKey: "SK"}
}

func TestNormalizeConfig(t *testing.T) {
	c := validCfg()
	c.Endpoint = " https://tos-cn-beijing.volces.com/ "
	c.Prefix = "/a/b/"
	got, err := c.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "tos-cn-beijing.volces.com" || got.Prefix != "a/b" {
		t.Fatalf("got %+v", got)
	}
	c = validCfg()
	c.Prefix = ""
	if got, _ := c.Normalize(); got.Prefix != "miaoju-inputs" {
		t.Fatalf("默认前缀 got %q", got.Prefix)
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"provider":  func(c *Config) { c.Provider = "s3" },
		"endpoint":  func(c *Config) { c.Endpoint = "" },
		"region":    func(c *Config) { c.Region = "" },
		"bucket":    func(c *Config) { c.Bucket = "" },
		"accessKey": func(c *Config) { c.AccessKey = "" },
		"secretKey": func(c *Config) { c.SecretKey = "" },
		"space":     func(c *Config) { c.Bucket = "a b" },
		"long":      func(c *Config) { c.Bucket = strings.Repeat("a", 513) },
	}
	for name, mut := range cases {
		c := validCfg()
		mut(&c)
		if _, err := c.Normalize(); err == nil {
			t.Errorf("%s: 应报错", name)
		}
	}
}

func TestLoadSaveClear(t *testing.T) {
	s := memSettings{}
	if _, ok, err := Load(s); err != nil || ok {
		t.Fatalf("空库应未配置: ok=%v err=%v", ok, err)
	}
	if err := Save(s, validCfg()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Load(s)
	if err != nil || !ok || got.SecretKey != "SK" || got.Prefix != "miaoju-inputs" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if err := Clear(s); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Load(s); ok {
		t.Fatal("清除后应未配置")
	}
}
```

- [ ] **Step 2: 写 `config.go` 与 `client.go`**

`config.go`：

```go
// Package objectstore 把本机媒体上传到用户自己配置的对象存储（火山 TOS / 阿里云 OSS），
// 交给上游一个 24 小时有效的预签名地址（对象存储设计 docs/superpowers/specs/2026-10-07-object-storage-design.md）。
package objectstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// SettingKey：配置整体以 JSON 存在 settings 表的这个键里。
const SettingKey = "object_storage"

const defaultPrefix = "miaoju-inputs"

type Config struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// FieldError 标明哪个字段不合法，HTTP 层据此回 400 + field。
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Normalize 去空格、去 endpoint 的协议头和末尾斜杠、补默认前缀，并校验必填项。
func (c Config) Normalize() (Config, error) {
	trim := func(s string) string { return strings.TrimSpace(s) }
	c.Provider = strings.ToLower(trim(c.Provider))
	c.Endpoint = trim(c.Endpoint)
	c.Endpoint = strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	c.Region, c.Bucket, c.AccessKey, c.SecretKey = trim(c.Region), trim(c.Bucket), trim(c.AccessKey), trim(c.SecretKey)
	c.Prefix = strings.Trim(trim(c.Prefix), "/")
	if c.Prefix == "" {
		c.Prefix = defaultPrefix
	}
	if c.Provider != "tos" && c.Provider != "oss" {
		return c, &FieldError{"provider", "服务商只能是 TOS 或 OSS"}
	}
	fields := []struct{ name, label, v string }{
		{"endpoint", "Endpoint", c.Endpoint}, {"region", "Region", c.Region}, {"bucket", "Bucket", c.Bucket},
		{"prefix", "路径前缀", c.Prefix}, {"accessKey", "AccessKey", c.AccessKey}, {"secretKey", "SecretKey", c.SecretKey},
	}
	for _, f := range fields {
		if f.v == "" {
			return c, &FieldError{f.name, f.label + " 不能为空"}
		}
		if len(f.v) > 512 {
			return c, &FieldError{f.name, f.label + " 过长"}
		}
		if strings.IndexFunc(f.v, unicode.IsSpace) >= 0 {
			return c, &FieldError{f.name, f.label + " 不能包含空格"}
		}
	}
	return c, nil
}

type SettingsStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// Load 读配置；未保存过（或已清除）时 ok=false。
func Load(s SettingsStore) (Config, bool, error) {
	raw, err := s.Get(SettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Config{}, false, err
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, false, fmt.Errorf("对象存储配置损坏: %w", err)
	}
	return c, true, nil
}

// Save 校验后整体写入。
func Save(s SettingsStore, c Config) error {
	n, err := c.Normalize()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.Set(SettingKey, string(raw))
}

func Clear(s SettingsStore) error { return s.Set(SettingKey, "") }
```

`client.go`：

```go
package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrNotConfigured = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")

// Client 是对各服务商 SDK 的最小封装。
type Client interface {
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error // 只给连接测试用
}

type ClientFactory func(Config) (Client, error)

// providers 由 tos.go / oss.go 在 init() 里登记。
var providers = map[string]ClientFactory{}

// NewClient 按 Provider 分发。
func NewClient(c Config) (Client, error) {
	f, ok := providers[c.Provider]
	if !ok {
		return nil, fmt.Errorf("不支持的服务商 %q", c.Provider)
	}
	return f(c)
}
```

- [ ] **Step 3: 运行**：`cd server && go test ./internal/objectstore/ -run 'Normalize|LoadSave'` → PASS。

- [ ] **Step 4: 写 `publisher_test.go`**

```go
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClient struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    atomic.Int32
	putErr  error
	delay   time.Duration
}

func newFake() *fakeClient { return &fakeClient{objects: map[string][]byte{}} }

func (f *fakeClient) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok, nil
}
func (f *fakeClient) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	time.Sleep(f.delay)
	if f.putErr != nil {
		return f.putErr
	}
	b, _ := io.ReadAll(r)
	f.mu.Lock()
	f.objects[key] = b
	f.mu.Unlock()
	f.puts.Add(1)
	return nil
}
func (f *fakeClient) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	return "https://bucket.example.com/" + key + "?ttl=" + ttl.String(), nil
}
func (f *fakeClient) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	delete(f.objects, key)
	f.mu.Unlock()
	return nil
}

type memObjects map[string][]byte

func (m memObjects) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func setup(t *testing.T, fc *fakeClient) (*Publisher, memSettings, *atomic.Int32) {
	t.Helper()
	s := memSettings{}
	if err := Save(s, validCfg()); err != nil {
		t.Fatal(err)
	}
	builds := &atomic.Int32{}
	p := NewPublisher(s, memObjects{"u1/2026/10/v.mp4": []byte("video-bytes")}, func(Config) (Client, error) {
		builds.Add(1)
		return fc, nil
	})
	return p, s, builds
}

func TestPublisherUploadsByHashAndPresigns(t *testing.T) {
	fc := newFake()
	p, _, _ := setup(t, fc)
	url, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("video-bytes"))
	want := "miaoju-inputs/" + hex.EncodeToString(sum[:]) + ".mp4"
	if !strings.Contains(url, want) || !strings.Contains(url, "ttl=24h0m0s") {
		t.Fatalf("url=%s want key %s", url, want)
	}
	if _, err := p.Publish(context.Background(), "u1/2026/10/v.mp4"); err != nil {
		t.Fatal(err)
	}
	if fc.puts.Load() != 1 {
		t.Fatalf("已存在应跳过上传, puts=%d", fc.puts.Load())
	}
}

func TestPublisherConcurrentSameKeyUploadsOnce(t *testing.T) {
	fc := newFake()
	fc.delay = 50 * time.Millisecond
	p, _, _ := setup(t, fc)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Publish(context.Background(), "u1/2026/10/v.mp4"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fc.puts.Load() != 1 {
		t.Fatalf("并发同 key 应只传一次, puts=%d", fc.puts.Load())
	}
}

func TestPublisherNotConfigured(t *testing.T) {
	p := NewPublisher(memSettings{}, memObjects{}, func(Config) (Client, error) { return newFake(), nil })
	if p.Configured() {
		t.Fatal("空配置应未配置")
	}
	if _, err := p.Publish(context.Background(), "x.mp4"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestPublisherRebuildsClientWhenConfigChanges(t *testing.T) {
	fc := newFake()
	p, s, builds := setup(t, fc)
	ctx := context.Background()
	p.Publish(ctx, "u1/2026/10/v.mp4")
	p.Publish(ctx, "u1/2026/10/v.mp4")
	if builds.Load() != 1 {
		t.Fatalf("配置不变应复用 client, builds=%d", builds.Load())
	}
	c := validCfg()
	c.Bucket = "b2"
	Save(s, c)
	p.Publish(ctx, "u1/2026/10/v.mp4")
	if builds.Load() != 2 {
		t.Fatalf("配置变化应重建 client, builds=%d", builds.Load())
	}
}

func TestPublisherErrorHidesSecrets(t *testing.T) {
	fc := newFake()
	fc.putErr = errors.New("denied for AK/SK")
	p, _, _ := setup(t, fc)
	_, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err == nil || !strings.HasPrefix(err.Error(), "上传到对象存储失败：") {
		t.Fatalf("err=%v", err)
	}
	// validCfg 的 AccessKey=AK、SecretKey=SK；Publisher 必须把原文替换成 ***。
	if !strings.Contains(err.Error(), "denied for ***/***") {
		t.Fatalf("错误里不得出现密钥原文: %v", err)
	}
}
```

- [ ] **Step 5: 运行确认失败**：`go test ./internal/objectstore/ -run Publisher` → 编译失败（`NewPublisher` 未定义）。

- [ ] **Step 6: 写 `publisher.go`**

```go
package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// PresignTTL：交给上游的地址有效期（沿用 generation.inputMediaURLExpiry）。
const PresignTTL = 24 * time.Hour

// ObjectReader：本地存储（*assets.LocalStorage 满足）。
type ObjectReader interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

// Publisher 把本机媒体发布成上游能下载的地址。每次调用现读配置，保存后立即生效。
type Publisher struct {
	settings SettingsStore
	objects  ObjectReader
	factory  ClientFactory

	mu       sync.Mutex
	client   Client
	clientOf Config
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	url  string
	err  error
}

func NewPublisher(s SettingsStore, objects ObjectReader, factory ClientFactory) *Publisher {
	if factory == nil {
		factory = NewClient
	}
	return &Publisher{settings: s, objects: objects, factory: factory, inflight: map[string]*call{}}
}

func (p *Publisher) Configured() bool {
	_, ok, err := Load(p.settings)
	return err == nil && ok
}

func (p *Publisher) clientFor(c Config) (Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.clientOf == c {
		return p.client, nil
	}
	cl, err := p.factory(c)
	if err != nil {
		return nil, err
	}
	p.client, p.clientOf = cl, c
	return cl, nil
}

// Publish：读本地对象 → sha256 → 已存在则跳过上传 → 预签名。同一 mediaKey 并发调用合并为一次。
func (p *Publisher) Publish(ctx context.Context, mediaKey string) (string, error) {
	c, ok, err := Load(p.settings)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotConfigured
	}
	p.mu.Lock()
	if cl, ok := p.inflight[mediaKey]; ok {
		p.mu.Unlock()
		<-cl.done
		return cl.url, cl.err
	}
	cl := &call{done: make(chan struct{})}
	p.inflight[mediaKey] = cl
	p.mu.Unlock()

	cl.url, cl.err = p.publish(ctx, c, mediaKey)
	if cl.err != nil {
		cl.err = fmt.Errorf("上传到对象存储失败：%s", redact(cl.err.Error(), c))
	}
	close(cl.done)
	p.mu.Lock()
	delete(p.inflight, mediaKey)
	p.mu.Unlock()
	return cl.url, cl.err
}

func (p *Publisher) publish(ctx context.Context, c Config, mediaKey string) (string, error) {
	client, err := p.clientFor(c)
	if err != nil {
		return "", err
	}
	// 第一遍：流式算 sha256 并嗅探类型；第二遍：重新打开上传。内存不随文件大小增长。
	rc, err := p.objects.GetObject(ctx, mediaKey)
	if err != nil {
		return "", fmt.Errorf("读取本机文件: %w", err)
	}
	h := sha256.New()
	head := make([]byte, 512)
	n, _ := io.ReadFull(rc, head)
	h.Write(head[:n])
	rest, err := io.Copy(h, rc)
	rc.Close()
	if err != nil {
		return "", fmt.Errorf("读取本机文件: %w", err)
	}
	size := int64(n) + rest
	ext := strings.ToLower(path.Ext(mediaKey))
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = http.DetectContentType(head[:n])
	}
	key := c.Prefix + "/" + hex.EncodeToString(h.Sum(nil)) + ext

	exists, err := client.Exists(ctx, key)
	if err != nil {
		return "", err
	}
	if !exists {
		rc, err := p.objects.GetObject(ctx, mediaKey)
		if err != nil {
			return "", fmt.Errorf("读取本机文件: %w", err)
		}
		err = client.Put(ctx, key, rc, size, ctype)
		rc.Close()
		if err != nil {
			return "", err
		}
	}
	return client.PresignGet(ctx, key, PresignTTL)
}

// redact 把错误信息里的密钥原文替换掉（SDK 错误偶尔会带上请求头）。
func redact(msg string, c Config) string {
	for _, s := range []string{c.SecretKey, c.AccessKey} {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return msg
}
```

- [ ] **Step 7: 写 `probe_test.go` 与 `probe.go`**

```go
package objectstore

import (
	"context"
	"errors"
	"testing"
)

func TestProbeSteps(t *testing.T) {
	ctx := context.Background()
	fc := newFake()
	// 模拟公网下载：fake 里此刻只有刚上传的那一个测试文件，原样取回。
	ok := Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) {
		for _, v := range fc.objects {
			return v, nil
		}
		return nil, errors.New("none")
	})
	if !ok.OK || len(fc.objects) != 0 {
		t.Fatalf("成功时应删掉测试文件: %+v objects=%d", ok, len(fc.objects))
	}

	fc = newFake()
	fc.putErr = errors.New("AccessDenied")
	if r := Probe(ctx, fc, "p", nil); r.OK || r.Step != "upload" {
		t.Fatalf("got %+v", r)
	}

	fc = newFake()
	r := Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) { return nil, errors.New("403") })
	if r.OK || r.Step != "download" {
		t.Fatalf("got %+v", r)
	}

	fc = newFake()
	r = Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) { return []byte("wrong"), nil })
	if r.OK || r.Step != "download" {
		t.Fatalf("内容不一致应在 download 步失败: %+v", r)
	}
}
```

`probe.go`：

```go
package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProbeResult：Step 取 upload | presign | download | delete；OK 时 Step 为空。
type ProbeResult struct {
	OK      bool   `json:"ok"`
	Step    string `json:"step,omitempty"`
	Message string `json:"message"`
}

// Probe 上传一个小文件 → 预签名 → 用普通 HTTP 下载核对（证明上游能拉到）→ 删除。
func Probe(ctx context.Context, c Client, prefix string, get func(context.Context, string) ([]byte, error)) ProbeResult {
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	key := prefix + "/.miaoju-test-" + hex.EncodeToString(rnd) + ".txt"
	body := []byte("miaoju object storage probe " + hex.EncodeToString(rnd))
	if err := c.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		return ProbeResult{Step: "upload", Message: "上传失败：" + err.Error()}
	}
	defer func() { _ = c.Delete(context.WithoutCancel(ctx), key) }()
	url, err := c.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		return ProbeResult{Step: "presign", Message: "生成签名地址失败：" + err.Error()}
	}
	got, err := get(ctx, url)
	if err != nil {
		return ProbeResult{Step: "download", Message: "通过签名地址下载失败（上游将无法拉取）：" + err.Error()}
	}
	if !bytes.Equal(got, body) {
		return ProbeResult{Step: "download", Message: "下载到的内容与上传的不一致，请检查 Endpoint 是否为公网地址"}
	}
	if err := c.Delete(ctx, key); err != nil {
		return ProbeResult{Step: "delete", Message: "删除测试文件失败（请检查 AccessKey 是否有删除权限）：" + err.Error()}
	}
	return ProbeResult{OK: true, Message: "连接正常：上传、签名、公网下载、删除均成功"}
}

// HTTPGet 是 Probe 默认的下载实现。
func HTTPGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Probe 用给定配置（可未保存）做连接测试。错误信息中的密钥原文会被替换。
func (p *Publisher) Probe(ctx context.Context, c Config) ProbeResult {
	n, err := c.Normalize()
	if err != nil {
		return ProbeResult{Step: "config", Message: err.Error()}
	}
	cl, err := p.factory(n)
	if err != nil {
		return ProbeResult{Step: "config", Message: redact(err.Error(), n)}
	}
	r := Probe(ctx, cl, n.Prefix, HTTPGet)
	r.Message = redact(r.Message, n)
	return r
}
```

> Probe 成功路径里 `defer Delete` 与显式 `Delete` 会删两次；第二次删除不存在的对象在 TOS/OSS 都返回成功（幂等），fake 也是。保持这样，好处是任何中途失败都会清理测试文件。

- [ ] **Step 8: 运行**：`cd server && go test -race ./internal/objectstore/` → PASS。

- [ ] **Step 9: 提交**

```bash
git add server/internal/objectstore
git commit -m "feat(objectstore): 配置读写、按哈希去重的 Publisher 与连接测试"
```

---

### Task 3: TOS / OSS SDK 适配器

**Files:**
- Create: `server/internal/objectstore/tos.go`、`oss.go`、`integration_test.go`
- Modify: `server/go.mod`、`server/go.sum`

**Interfaces:**
- Consumes: Task 2 的 `Client`、`Config`、`providers`。
- Produces: `providers["tos"]`、`providers["oss"]`（在 `init()` 中登记）。

- [ ] **Step 1: 加依赖**

```bash
cd server
GOPROXY=https://goproxy.cn,direct go get github.com/volcengine/ve-tos-golang-sdk/v2@v2.9.10 github.com/aliyun/alibabacloud-oss-go-sdk-v2@v1.6.0
```

- [ ] **Step 2: 写 `tos.go`**（client 构造与预签名照搬参考仓 `huabu_studio/apps/api/internal/assets/tos.go`）

```go
package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

func init() { providers["tos"] = newTOSClient }

type tosClient struct {
	cli    *tos.ClientV2
	bucket string
}

func newTOSClient(c Config) (Client, error) {
	cli, err := tos.NewClientV2(
		c.Endpoint,
		tos.WithRegion(c.Region),
		tos.WithCredentials(tos.NewStaticCredentials(c.AccessKey, c.SecretKey)),
	)
	if err != nil {
		return nil, fmt.Errorf("tos new client: %w", err)
	}
	return &tosClient{cli: cli, bucket: c.Bucket}, nil
}

func (t *tosClient) Exists(ctx context.Context, key string) (bool, error) {
	_, err := t.cli.HeadObjectV2(ctx, &tos.HeadObjectV2Input{Bucket: t.bucket, Key: key})
	if err == nil {
		return true, nil
	}
	if tos.StatusCode(err) == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("tos head object: %w", err)
}

func (t *tosClient) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := t.cli.PutObjectV2(ctx, &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket: t.bucket, Key: key, ContentType: contentType, ContentLength: size,
		},
		Content: r,
	})
	if err != nil {
		return fmt.Errorf("tos put object: %w", err)
	}
	return nil
}

func (t *tosClient) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	out, err := t.cli.PreSignedURL(&tos.PreSignedURLInput{
		HTTPMethod: enum.HttpMethodGet, Bucket: t.bucket, Key: key, Expires: int64(ttl.Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("tos presign: %w", err)
	}
	return out.SignedUrl, nil
}

func (t *tosClient) Delete(ctx context.Context, key string) error {
	if _, err := t.cli.DeleteObjectV2(ctx, &tos.DeleteObjectV2Input{Bucket: t.bucket, Key: key}); err != nil {
		return fmt.Errorf("tos delete object: %w", err)
	}
	return nil
}
```

> 字段名以参考仓 `tos.go:140-160` 为准（`out.SignedUrl`）；`go doc github.com/volcengine/ve-tos-golang-sdk/v2/tos PreSignedURLOutput` 可核对。

- [ ] **Step 3: 写 `oss.go`**

```go
package objectstore

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func init() { providers["oss"] = newOSSClient }

type ossClient struct {
	cli    *oss.Client
	bucket string
}

func newOSSClient(c Config) (Client, error) {
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey)).
		WithRegion(c.Region).
		WithEndpoint(c.Endpoint)
	return &ossClient{cli: oss.NewClient(cfg), bucket: c.Bucket}, nil
}

func (o *ossClient) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := o.cli.IsObjectExist(ctx, o.bucket, key)
	if err != nil {
		return false, fmt.Errorf("oss head object: %w", err)
	}
	return ok, nil
}

func (o *ossClient) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := o.cli.PutObject(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
		ContentType: oss.Ptr(contentType), ContentLength: oss.Ptr(size),
		Body: r,
	})
	if err != nil {
		return fmt.Errorf("oss put object: %w", err)
	}
	return nil
}

func (o *ossClient) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	res, err := o.cli.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key)},
		oss.PresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("oss presign: %w", err)
	}
	return res.URL, nil
}

func (o *ossClient) Delete(ctx context.Context, key string) error {
	if _, err := o.cli.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key)}); err != nil {
		return fmt.Errorf("oss delete object: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 写 `integration_test.go`**（无环境变量时跳过）

```go
package objectstore

import (
	"context"
	"os"
	"testing"
)

// 真实桶集成测试：设置 MIAOJU_TEST_<TOS|OSS>_{ENDPOINT,REGION,BUCKET,AK,SK} 后运行。
func TestRealProviders(t *testing.T) {
	for _, p := range []string{"TOS", "OSS"} {
		t.Run(p, func(t *testing.T) {
			env := func(k string) string { return os.Getenv("MIAOJU_TEST_" + p + "_" + k) }
			if env("BUCKET") == "" {
				t.Skip("未设置 MIAOJU_TEST_" + p + "_BUCKET")
			}
			c, err := Config{Provider: map[string]string{"TOS": "tos", "OSS": "oss"}[p],
				Endpoint: env("ENDPOINT"), Region: env("REGION"), Bucket: env("BUCKET"),
				AccessKey: env("AK"), SecretKey: env("SK"), Prefix: "miaoju-it"}.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			cl, err := NewClient(c)
			if err != nil {
				t.Fatal(err)
			}
			if r := Probe(context.Background(), cl, c.Prefix, HTTPGet); !r.OK {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestProvidersRegistered(t *testing.T) {
	for _, p := range []string{"tos", "oss"} {
		if _, ok := providers[p]; !ok {
			t.Errorf("%s 未登记", p)
		}
	}
}
```

- [ ] **Step 5: 运行**：`cd server && go vet ./internal/objectstore/ && go test ./internal/objectstore/` → PASS（真实桶测试 SKIP）。

- [ ] **Step 6: 提交**

```bash
git add server/go.mod server/go.sum server/internal/objectstore
git commit -m "feat(objectstore): 火山 TOS 与阿里云 OSS 适配器"
```

---

### Task 4: 生成管线接入（替换 InputMediaSigner）

**Files:**
- Modify: `server/internal/generation/input_media.go`、`image_inputs.go:46-75`、`service.go:142-143,383`、`execution_plan.go:72-73`
- Test: `server/internal/generation/input_media_test.go`

**Interfaces:**
- Produces:
  - `type MediaPublisher interface{ Configured() bool; Publish(ctx context.Context, mediaKey string) (string, error) }`
  - `func (s *Service) SetMediaPublisher(p MediaPublisher)`
  - 删除 `InputMediaSigner`、`SetInputMediaSigner`、字段 `inputSigner`。
  - `signInputMediaReferences(ctx, inputs, skipImages bool)`

规则：本机媒体引用（`s.storage.ExtractKey` 认得）——若在顶层 `images` / `image` 字段且 `skipImages`，原样保留（`prepareImageInputs` 会经 `fetchInputImage → readLocalMedia` 直接读盘）；否则 publisher 已配置 → `Publish`；未配置 → 维持原逻辑 `storage.SignedURL`（随后由 `ensureNoLocalMedia` 拦截）。`skipImages = model.ImageInputFormat != url || model.ImageRequestMode == openai_image_edits`。

- [ ] **Step 1: 改测试**。把 `input_media_test.go` 里的 `publicSigner` 和 `TestPrepareExecutionInjectedSignerMakesURLReachable` 替换为：

```go
type fakePublisher struct {
	configured bool
	published  []string
}

func (f *fakePublisher) Configured() bool { return f.configured }
func (f *fakePublisher) Publish(_ context.Context, key string) (string, error) {
	f.published = append(f.published, key)
	return "https://bucket.example.com/" + key + "?sig", nil
}

func TestPrepareExecutionPublishesVideo(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"video":"{{video}}"}`)}, okSettings(), "sk")
	pub := &fakePublisher{configured: true}
	svc.SetMediaPublisher(pub)
	plan, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"video": "media://u1/v.mp4"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan.payload), "https://bucket.example.com/u1/v.mp4?sig") {
		t.Fatalf("视频应换成对象存储地址, got %s", plan.payload)
	}
}

func TestSignInputMediaSkipsImagesForInlineFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		mode   string
		skip   bool
	}{
		{"data_uri", models.ImageInputFormatDataURI, models.ImageRequestModeJSON, true},
		{"edits", models.ImageInputFormatURL, models.ImageRequestModeOpenAIImageEdits, true},
		{"url", models.ImageInputFormatURL, models.ImageRequestModeJSON, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipImagesFor(&models.ModelConfig{ImageInputFormat: tc.format, ImageRequestMode: tc.mode}); got != tc.skip {
				t.Fatalf("skipImagesFor=%v want %v", got, tc.skip)
			}
			svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true}, okSettings(), "sk")
			pub := &fakePublisher{configured: true}
			svc.SetMediaPublisher(pub)
			out, err := svc.signInputMediaReferences(context.Background(),
				map[string]any{"images": []any{"media://u1/a.png"}, "videos": []any{"media://u1/v.mp4"}}, tc.skip)
			if err != nil {
				t.Fatal(err)
			}
			wantPub := []string{"u1/v.mp4"}
			if !tc.skip {
				wantPub = []string{"u1/a.png", "u1/v.mp4"}
			}
			sort.Strings(pub.published)
			if !reflect.DeepEqual(pub.published, wantPub) {
				t.Fatalf("published=%v want %v", pub.published, wantPub)
			}
			if tc.skip && out["images"].([]any)[0] != "media://u1/a.png" {
				t.Fatalf("跳过的图片应原样保留, got %v", out["images"])
			}
		})
	}
}

func TestPrepareExecutionUnconfiguredStillFailsWithGuide(t *testing.T) {
	svc := planService(&models.ModelConfig{ModelID: "m", Enabled: true, ImageInputFormat: models.ImageInputFormatURL,
		RequestTemplate: json.RawMessage(`{"video":"{{video}}"}`)}, okSettings(), "sk")
	svc.SetMediaPublisher(&fakePublisher{configured: false})
	_, err := svc.prepareExecution(context.Background(), 1, "m",
		map[string]any{"video": "media://u1/v.mp4"}, "", "", nil)
	if !errors.Is(err, ErrLocalMediaUnreachable) || !strings.Contains(err.Error(), "设置 → 配置 → 对象存储") {
		t.Fatalf("err=%v", err)
	}
}
```

> `planService` 用的 storage 需能 `ExtractKey("media://u1/...")`；先读 `input_media_test.go` 顶部确认 fake storage 的行为，若只认 `media://` 前缀则无需调整。补齐 import：`errors`、`reflect`、`sort`。

- [ ] **Step 2: 运行确认失败**：`cd server && go test ./internal/generation/ -run 'Publish|SkipsImages|Unconfigured'` → 编译失败。

- [ ] **Step 3: 改 `input_media.go`**：用下面内容替换 `InputMediaSigner` 接口、`SetInputMediaSigner` 和 `ErrLocalMediaUnreachable`：

```go
// MediaPublisher 把本机媒体发布到用户配置的对象存储并返回上游可下载的地址
// （对象存储设计 §3.2；*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// SetMediaPublisher 注入对象存储发布器（setter 惯例同 SetTempDir）。nil = 参考仓原逻辑。
func (s *Service) SetMediaPublisher(p MediaPublisher) { s.publisher = p }

// skipImagesFor：图片会以内容本身发给上游（data_uri / base64 / edits multipart）时不必上传。
func skipImagesFor(m *models.ModelConfig) bool {
	return m != nil && (m.ImageInputFormat != models.ImageInputFormatURL || m.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits)
}

// ErrLocalMediaUnreachable：插值后的上游请求里仍有本机媒体地址（未配置对象存储）。
// 在调上游之前失败，不产生任何上游调用。
var ErrLocalMediaUnreachable = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")
```

`service.go:142-143` 字段改为：

```go
	// publisher 见 SetMediaPublisher（对象存储设计 §3.2）；nil 或未配置 = 参考仓原逻辑。
	publisher MediaPublisher
```

- [ ] **Step 4: 改 `image_inputs.go` 的 `signInputMediaReferences`**：

```go
func (s *Service) signInputMediaReferences(ctx context.Context, inputs map[string]any, skipImages bool) (map[string]any, error) {
	if s == nil || s.storage == nil || inputs == nil {
		return cloneInputs(inputs), nil
	}
	publish := s.publisher != nil && s.publisher.Configured()
	sign := func(ref string) (string, error) {
		key, ok := s.storage.ExtractKey(ref)
		if !ok || key == "" {
			return ref, nil
		}
		if publish {
			return s.publisher.Publish(ctx, key)
		}
		url, err := s.storage.SignedURL(ctx, key, inputMediaURLExpiry)
		if err != nil || url == "" {
			if err == nil {
				err = fmt.Errorf("empty signed url")
			}
			return "", fmt.Errorf("sign %q: %w", key, err)
		}
		return url, nil
	}
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		if skipImages && (k == "images" || k == "image") {
			// 图片随后由 prepareImageInputs 直接读盘内嵌，不上传。
			out[k] = cloneValue(v)
			continue
		}
		next, err := walkInputStrings(v, sign)
		if err != nil {
			return nil, err
		}
		out[k] = next
	}
	return out, nil
}
```

保留原函数上方「自由画布 HTTP 请求会在 GenerationHandler 签名…」那段注释。`cloneValue`：若 `cloneInputs` 已有按值深拷贝的辅助函数则复用；没有就用 `walkInputStrings(v, func(s string) (string, error) { return s, nil })` 代替（它返回新容器）。

- [ ] **Step 5: 改两个调用点**：
  - `execution_plan.go:73`：`s.signInputMediaReferences(ctx, inputs, skipImagesFor(model))`
  - `service.go:383`：`s.signInputMediaReferences(ctx, input.Inputs, skipImagesFor(model))`
  - 全仓 `grep -rn "signInputMediaReferences\|SetInputMediaSigner\|InputMediaSigner" server/` 修完其余引用（含测试）。

- [ ] **Step 6: 运行**：`cd server && go test ./internal/generation/...` → PASS。

- [ ] **Step 7: 提交**

```bash
git add server/internal/generation
git commit -m "feat(generation): 本机视频/音频经对象存储发布，内嵌图片不上传"
```

---

### Task 5: Agent 与 Seedance 合规校验接入

**Files:**
- Modify: `server/internal/agent/media_llm.go`、`media_llm_local.go:44-80`
- Modify: `server/internal/library/seedance_service.go:189-205,244,388`
- Test: `server/internal/agent/media_llm_local_test.go`、`server/internal/library/seedance_service_test.go`（文件名以现有为准，追加用例）

**Interfaces:**
- Produces:
  - `agent.MediaPublisher`（同 Task 4 的方法集）；`LocalMediaResolver.Publisher MediaPublisher` 字段
  - `library.MediaPublisher`（同方法集）；`func (s *SeedanceService) SetMediaPublisher(p MediaPublisher)`
  - `signedURL(ctx context.Context, objectKey string)`（加 ctx 参数）

- [ ] **Step 1: 写 Agent 测试**（追加到 resolver 现有测试文件）

```go
type fakePub struct{ configured bool }

func (f fakePub) Configured() bool { return f.configured }
func (f fakePub) Publish(_ context.Context, key string) (string, error) {
	return "https://bucket.example.com/" + key, nil
}

func TestResolverPublishesVideoAndAudio(t *testing.T) {
	r := &LocalMediaResolver{Keys: mediaKeys{}, Objects: memObjects{}, Publisher: fakePub{configured: true}}
	for _, kind := range []string{"video", "audio"} {
		got, err := r.ResolveForLLM(context.Background(), kind, "media://u1/x.mp4")
		if err != nil || got != "https://bucket.example.com/u1/x.mp4" {
			t.Fatalf("%s: got %q err=%v", kind, got, err)
		}
	}
}

func TestResolverVideoUnconfiguredGuides(t *testing.T) {
	r := &LocalMediaResolver{Keys: mediaKeys{}, Objects: memObjects{}, Publisher: fakePub{configured: false}}
	_, err := r.ResolveForLLM(context.Background(), "video", "media://u1/x.mp4")
	if !errors.Is(err, ErrMediaNeedsPublicURL) || !strings.Contains(err.Error(), "设置 → 配置 → 对象存储") {
		t.Fatalf("err=%v", err)
	}
}
```

> `mediaKeys` / `memObjects` 用该测试文件里已有的 fake（名字以实际为准）；它们须认 `media://u1/x.mp4` → `u1/x.mp4`。

- [ ] **Step 2: 改 Agent**

`media_llm.go`：

```go
// MediaPublisher 把本机媒体发布到用户配置的对象存储（*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// ErrMediaNeedsPublicURL：本机视频/音频需要对象存储才能交给上游，但用户还没配置。
var ErrMediaNeedsPublicURL = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")
```

`media_llm_local.go`：结构体加字段 `Publisher MediaPublisher // nil 或未配置：视频/音频报 ErrMediaNeedsPublicURL`，注释改为「本机视频 / 音频：经对象存储发布；未配置时返回 ErrMediaNeedsPublicURL」，并把

```go
	if kind != "image" && kind != "" {
		return "", ErrMediaNeedsPublicURL
	}
```

改为

```go
	if kind != "image" && kind != "" {
		if r.Publisher == nil || !r.Publisher.Configured() {
			return "", ErrMediaNeedsPublicURL
		}
		return r.Publisher.Publish(ctx, key)
	}
```

- [ ] **Step 3: 写 Seedance 测试**（追加；fake signer 返回 `http://127.0.0.1:1234/media/<key>`，构造方式参照该文件现有的 loopback 用例）

```go
type fakeSeedancePub struct{ configured bool }

func (f fakeSeedancePub) Configured() bool { return f.configured }
func (f fakeSeedancePub) Publish(_ context.Context, key string) (string, error) {
	return "https://bucket.example.com/" + key, nil
}

func TestSeedanceSignedURLPublishesLoopback(t *testing.T) {
	svc := newLoopbackSeedanceService(t) // 现有 loopback 用例的构造方式；没有就照它内联
	svc.SetMediaPublisher(fakeSeedancePub{configured: true})
	got, err := svc.signedURL(context.Background(), "u1/a.png")
	if err != nil || got != "https://bucket.example.com/u1/a.png" {
		t.Fatalf("got %q err=%v", got, err)
	}
	svc.SetMediaPublisher(fakeSeedancePub{configured: false})
	if _, err := svc.signedURL(context.Background(), "u1/a.png"); !errors.Is(err, ErrLocalMediaNoPublicURL) {
		t.Fatalf("未配置应报 ErrLocalMediaNoPublicURL, err=%v", err)
	}
}
```

- [ ] **Step 4: 改 `seedance_service.go`**

```go
// MediaPublisher 见对象存储设计 §3.2（*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// SetMediaPublisher 注入对象存储发布器；nil = 回环地址一律报错。
func (s *SeedanceService) SetMediaPublisher(p MediaPublisher) { s.publisher = p }

// ErrLocalMediaNoPublicURL：素材只能签出本机回环地址，且未配置对象存储。
var ErrLocalMediaNoPublicURL = errors.New("未配置对象存储：合规校验需要把素材上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")
```

`SeedanceService` 结构体加 `publisher MediaPublisher`。`signedURL` 改签名为 `signedURL(ctx context.Context, objectKey string)`，回环分支改为：

```go
	if isLoopbackURL(url) {
		if s.publisher == nil || !s.publisher.Configured() {
			return "", ErrLocalMediaNoPublicURL
		}
		return s.publisher.Publish(ctx, objectKey)
	}
```

两个调用点（`:244` 在 `Submit` 内、`:388` 在 `reconcileOne` 内）传入各自的 `ctx`。

- [ ] **Step 5: 运行**：`cd server && go test ./internal/agent/... ./internal/library/...` → PASS。

- [ ] **Step 6: 提交**

```bash
git add server/internal/agent server/internal/library
git commit -m "feat(agent,library): 视频/音频与合规素材经对象存储发布"
```

---

### Task 6: 设置接口

**Files:**
- Create: `server/internal/http/settings_object_storage_handlers.go`
- Create: `server/internal/http/settings_object_storage_handlers_test.go`
- Modify: `server/internal/http/settings_center.go:95-105`（加字段）

**Interfaces:**
- Consumes: `objectstore.Config/Load/Save/Clear/FieldError/ProbeResult`。
- Produces:
  - `SettingsCenterDeps.ObjectStore ObjectStoreProber`
  - `type ObjectStoreProber interface{ Probe(ctx context.Context, c objectstore.Config) objectstore.ProbeResult }`
  - 路由（相对 `/api/v1/settings`）：`GET/PUT/DELETE /object-storage`、`POST /object-storage/test`
  - 响应 `objectStorageView{configured, provider, endpoint, region, bucket, prefix, accessKey, secretKeySet, secretKeyMasked}`

- [ ] **Step 1: 写测试**

```go
package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/objectstore"
)

type fakeProber struct{ got objectstore.Config }

func (f *fakeProber) Probe(_ context.Context, c objectstore.Config) objectstore.ProbeResult {
	f.got = c
	return objectstore.ProbeResult{OK: true, Message: "ok"}
}

type osView struct {
	Configured      bool   `json:"configured"`
	Provider        string `json:"provider"`
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKey       string `json:"accessKey"`
	SecretKeySet    bool   `json:"secretKeySet"`
	SecretKeyMasked string `json:"secretKeyMasked"`
}

func osBody(over map[string]any) map[string]any {
	b := map[string]any{"provider": "oss", "endpoint": "https://oss-cn-hangzhou.aliyuncs.com/", "region": "cn-hangzhou",
		"bucket": "bkt", "prefix": "", "accessKey": "LTAIabcdef", "secretKey": "secret-1234567890"}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func TestObjectStorageGetEmpty(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/object-storage", nil)
	var v osView
	json.Unmarshal([]byte(body), &v)
	if code != 200 || v.Configured {
		t.Fatalf("%d %s", code, body)
	}
}

func TestObjectStoragePutNeverEchoesSecret(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("PUT", "/object-storage", osBody(nil))
	if code != 200 || strings.Contains(body, "secret-1234567890") {
		t.Fatalf("%d %s", code, body)
	}
	var v osView
	json.Unmarshal([]byte(body), &v)
	if !v.Configured || !v.SecretKeySet || v.Endpoint != "oss-cn-hangzhou.aliyuncs.com" || v.Prefix != "miaoju-inputs" || v.SecretKeyMasked == "" {
		t.Fatalf("%+v", v)
	}
}

func TestObjectStoragePutKeepsSecret(t *testing.T) {
	e := newSCEnv(t)
	e.do("PUT", "/object-storage", osBody(nil))
	code, body := e.do("PUT", "/object-storage", osBody(map[string]any{"bucket": "bkt2", "secretKey": ""}))
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	c, _, _ := objectstore.Load(e.settings)
	if c.Bucket != "bkt2" || c.SecretKey != "secret-1234567890" {
		t.Fatalf("%+v", c)
	}
}

func TestObjectStorageFirstSaveRequiresSecret(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("PUT", "/object-storage", osBody(map[string]any{"secretKey": ""}))
	if code != 400 || !strings.Contains(body, "secretKey") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestObjectStorageRejectsBadProviderAndUnknownField(t *testing.T) {
	e := newSCEnv(t)
	if code, _ := e.do("PUT", "/object-storage", osBody(map[string]any{"provider": "s3"})); code != 400 {
		t.Fatalf("provider=s3 应 400, got %d", code)
	}
	if code, _ := e.do("PUT", "/object-storage", osBody(map[string]any{"foo": 1})); code != 400 {
		t.Fatalf("未知字段应 400, got %d", code)
	}
}

func TestObjectStorageDelete(t *testing.T) {
	e := newSCEnv(t)
	e.do("PUT", "/object-storage", osBody(nil))
	code, body := e.do("DELETE", "/object-storage", nil)
	var v osView
	json.Unmarshal([]byte(body), &v)
	if code != 200 || v.Configured {
		t.Fatalf("%d %s", code, body)
	}
}

func TestObjectStorageTestUsesSavedSecret(t *testing.T) {
	e := newSCEnv(t)
	p := &fakeProber{}
	e.objectStore = p // 见 Step 2：scEnv 加字段并在 deps() 里传入
	e.rebuild()
	e.do("PUT", "/object-storage", osBody(nil))
	code, body := e.do("POST", "/object-storage/test", osBody(map[string]any{"secretKey": ""}))
	if code != 200 || !strings.Contains(body, `"ok":true`) || p.got.SecretKey != "secret-1234567890" {
		t.Fatalf("%d %s got=%+v", code, body, p.got)
	}
}
```

- [ ] **Step 2: 给 `scEnv` 加 `objectStore ObjectStoreProber` 字段**（`settings_center_env_test.go`），`deps()` 里传 `ObjectStore: e.objectStore`；新增 `rebuild()`：

```go
// rebuild 在改了 env 依赖后重新挂路由。
func (e *scEnv) rebuild() {
	r := chi.NewRouter()
	mountSettingsCenter(r, e.deps())
	e.handler = r
}
```

并让构造函数末尾改为调用 `e.rebuild()`。构造函数名以实际为准（测试里写的 `newSCEnv`，照实际替换）。

- [ ] **Step 3: 运行确认失败**：`cd server && go test ./internal/http/ -run ObjectStorage` → 编译失败。

- [ ] **Step 4: `settings_center.go` 的 `SettingsCenterDeps` 加字段**

```go
	// ObjectStore 为 nil 时不注册 /object-storage/test（其余对象存储接口只依赖 Settings）。
	ObjectStore ObjectStoreProber
```

- [ ] **Step 5: 写 handler**

```go
package http

import (
	"context"
	"errors"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/objectstore"
)

// ObjectStoreProber：*objectstore.Publisher 满足。
type ObjectStoreProber interface {
	Probe(ctx context.Context, c objectstore.Config) objectstore.ProbeResult
}

func init() {
	settingsRouteRegistrars = append(settingsRouteRegistrars, registerObjectStorageSettings)
}

func registerObjectStorageSettings(r chi.Router, d SettingsCenterDeps) {
	if d.Settings == nil {
		return
	}
	h := &objectStorageHandler{d: d}
	r.Get("/object-storage", h.get)
	r.Put("/object-storage", h.put)
	r.Delete("/object-storage", h.clear)
	if d.ObjectStore != nil {
		r.Post("/object-storage/test", h.test)
	}
}

type objectStorageHandler struct{ d SettingsCenterDeps }

type objectStorageView struct {
	Configured      bool   `json:"configured"`
	Provider        string `json:"provider"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	AccessKey       string `json:"accessKey"`
	SecretKeySet    bool   `json:"secretKeySet"`
	SecretKeyMasked string `json:"secretKeyMasked"`
}

func (h *objectStorageHandler) writeView(w nethttp.ResponseWriter) {
	c, ok, err := objectstore.Load(h.d.Settings)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, objectStorageView{
		Configured: ok, Provider: c.Provider, Endpoint: c.Endpoint, Region: c.Region,
		Bucket: c.Bucket, Prefix: c.Prefix, AccessKey: c.AccessKey,
		SecretKeySet: c.SecretKey != "", SecretKeyMasked: maskAPIKey(c.SecretKey),
	})
}

func (h *objectStorageHandler) get(w nethttp.ResponseWriter, _ *nethttp.Request) { h.writeView(w) }

// decode 解析请求体；secretKey 留空时沿用已保存的值（首次保存由 Normalize 报 secretKey 必填）。
func (h *objectStorageHandler) decode(w nethttp.ResponseWriter, r *nethttp.Request) (objectstore.Config, error) {
	var c objectstore.Config
	if err := scDecodeStrict(w, r, &c); err != nil {
		return c, err
	}
	if c.SecretKey == "" {
		if saved, ok, err := objectstore.Load(h.d.Settings); err != nil {
			return c, err
		} else if ok {
			c.SecretKey = saved.SecretKey
		}
	}
	return c, nil
}

func toValidation(err error) error {
	var fe *objectstore.FieldError
	if errors.As(err, &fe) {
		return scValidation(fe.Field, fe.Msg)
	}
	return err
}

func (h *objectStorageHandler) put(w nethttp.ResponseWriter, r *nethttp.Request) {
	c, err := h.decode(w, r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	if err := objectstore.Save(h.d.Settings, c); err != nil {
		scWriteError(w, toValidation(err))
		return
	}
	h.writeView(w)
}

func (h *objectStorageHandler) clear(w nethttp.ResponseWriter, _ *nethttp.Request) {
	if err := objectstore.Clear(h.d.Settings); err != nil {
		scWriteError(w, err)
		return
	}
	h.writeView(w)
}

func (h *objectStorageHandler) test(w nethttp.ResponseWriter, r *nethttp.Request) {
	c, err := h.decode(w, r)
	if err != nil {
		scWriteError(w, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, h.d.ObjectStore.Probe(r.Context(), c))
}
```

> 确认 `scDecodeStrict` 拒绝未知字段（`TestUpstream…` 里 `upstream_api_key` 字段返回 400，说明是 `DisallowUnknownFields`）。确认 `scWriteError` 对 `*SettingsValidationError` 回 400 且 body 含 `field`。

- [ ] **Step 6: 运行**：`cd server && go test ./internal/http/...` → PASS（含已有的 whitelist 测试；若 whitelist 测试枚举了全部路由，把新路由加进它的期望列表）。

- [ ] **Step 7: 提交**

```bash
git add server/internal/http
git commit -m "feat(settings): 对象存储配置接口与连接测试"
```

---

### Task 7: 前端「对象存储」卡片

**Files:**
- Modify: `web/src/modules/settings/api.ts`（追加）
- Create: `web/src/modules/settings/components/ObjectStorageCard.tsx`
- Create: `web/src/modules/settings/components/ObjectStorageCard.test.tsx`
- Modify: `web/src/modules/settings/pages/GeneralPage.tsx`（在「数据目录」卡片前插入 `<ObjectStorageCard />`）

**Interfaces:**
- Consumes: Task 6 的接口契约。
- Produces: `getObjectStorage`、`saveObjectStorage`、`clearObjectStorage`、`testObjectStorage`、类型 `ObjectStorageView`、`ObjectStorageInput`、`ProbeResult`。

- [ ] **Step 1: `api.ts` 追加**（同时把 `request` 的 method 联合类型加上 `'DELETE'`）

```ts
// ---- 对象存储（视频/音频参考素材上传到用户自己的桶）----
export type ObjectStorageProvider = 'tos' | 'oss';
export interface ObjectStorageView {
  configured: boolean;
  provider: ObjectStorageProvider | '';
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKey: string;
  secretKeySet: boolean;
  secretKeyMasked: string;
}
export interface ObjectStorageInput {
  provider: ObjectStorageProvider;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKey: string;
  /** 留空 = 沿用已保存的值 */
  secretKey: string;
}
export interface ProbeResult {
  ok: boolean;
  step?: 'config' | 'upload' | 'presign' | 'download' | 'delete';
  message: string;
}
export const getObjectStorage = () => request<ObjectStorageView>('GET', '/object-storage');
export const saveObjectStorage = (b: ObjectStorageInput) => request<ObjectStorageView>('PUT', '/object-storage', b);
export const clearObjectStorage = () => request<ObjectStorageView>('DELETE', '/object-storage');
export const testObjectStorage = (b: ObjectStorageInput) => request<ProbeResult>('POST', '/object-storage/test', b);
```

- [ ] **Step 2: 写测试 `ObjectStorageCard.test.tsx`**

```tsx
// @vitest-environment jsdom
import '../testing/domSetup';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App as AntApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const save = vi.fn();
const probe = vi.fn();
vi.mock('../api', async (orig) => ({
  ...(await orig<typeof import('../api')>()),
  getObjectStorage: async () => ({
    configured: true, provider: 'tos', endpoint: 'tos-cn-beijing.volces.com', region: 'cn-beijing',
    bucket: 'b', prefix: 'miaoju-inputs', accessKey: 'AK', secretKeySet: true, secretKeyMasked: '••••••••WXYZ',
  }),
  saveObjectStorage: (b: unknown) => save(b),
  testObjectStorage: (b: unknown) => probe(b),
  clearObjectStorage: vi.fn(),
}));

import { ObjectStorageCard } from './ObjectStorageCard';

function renderCard() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AntApp>
        <ObjectStorageCard />
      </AntApp>
    </QueryClientProvider>,
  );
}

describe('ObjectStorageCard', () => {
  beforeEach(() => {
    save.mockReset().mockResolvedValue({ configured: true });
    probe.mockReset();
  });

  it('回显已保存配置，SecretKey 只显示掩码占位', async () => {
    renderCard();
    expect(await screen.findByDisplayValue('tos-cn-beijing.volces.com')).toBeTruthy();
    expect(screen.getByText('已配置')).toBeTruthy();
    const secret = screen.getByLabelText('SecretKey') as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.placeholder).toContain('WXYZ');
  });

  it('不重输 SecretKey 保存时发送空字符串（后端沿用原值）', async () => {
    renderCard();
    await screen.findByDisplayValue('b');
    fireEvent.change(screen.getByLabelText('Bucket'), { target: { value: 'b2' } });
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][0]).toMatchObject({ provider: 'tos', bucket: 'b2', secretKey: '' });
  });

  it('测试失败时显示失败环节和原因', async () => {
    probe.mockResolvedValue({ ok: false, step: 'download', message: '通过签名地址下载失败：HTTP 403' });
    renderCard();
    await screen.findByDisplayValue('b');
    fireEvent.click(screen.getByRole('button', { name: '测试连接' }));
    expect(await screen.findByText(/HTTP 403/)).toBeTruthy();
  });
});
```

- [ ] **Step 3: 运行确认失败**：`cd web && pnpm vitest run src/modules/settings/components/ObjectStorageCard.test.tsx` → FAIL（组件不存在）。

- [ ] **Step 4: 写 `ObjectStorageCard.tsx`**

```tsx
import { useEffect, useState } from 'react';
import { Alert, App as AntApp, Button, Card, Form, Input, Modal, Select, Space, Tag, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  clearObjectStorage,
  errorText,
  getObjectStorage,
  saveObjectStorage,
  testObjectStorage,
  type ObjectStorageInput,
  type ObjectStorageProvider,
  type ProbeResult,
} from '../api';

const EXAMPLES: Record<ObjectStorageProvider, { endpoint: string; region: string }> = {
  tos: { endpoint: '例：tos-cn-beijing.volces.com', region: '例：cn-beijing' },
  oss: { endpoint: '例：oss-cn-hangzhou.aliyuncs.com', region: '例：cn-hangzhou' },
};

const STEP_LABEL: Record<string, string> = {
  config: '配置', upload: '上传', presign: '签名', download: '公网下载', delete: '删除',
};

/** 视频、音频参考素材上传到用户自己的对象存储（对象存储设计 §3.4）。 */
export function ObjectStorageCard() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['settings-object-storage'], queryFn: getObjectStorage });
  const [form] = Form.useForm<ObjectStorageInput>();
  const provider = (Form.useWatch('provider', form) ?? 'tos') as ObjectStorageProvider;
  const [probe, setProbe] = useState<ProbeResult | null>(null);

  useEffect(() => {
    const v = q.data;
    if (!v) return;
    form.setFieldsValue({
      provider: (v.provider || 'tos') as ObjectStorageProvider,
      endpoint: v.endpoint, region: v.region, bucket: v.bucket,
      prefix: v.prefix || 'miaoju-inputs', accessKey: v.accessKey, secretKey: '',
    });
  }, [q.data, form]);

  const values = (): ObjectStorageInput => ({ prefix: '', secretKey: '', ...form.getFieldsValue() });

  const save = useMutation({
    mutationFn: () => saveObjectStorage(values()),
    onSuccess: (v) => {
      qc.setQueryData(['settings-object-storage'], v);
      message.success('对象存储配置已保存');
    },
    onError: (e) => message.error(errorText(e)),
  });
  const test = useMutation({
    mutationFn: () => testObjectStorage(values()),
    onMutate: () => setProbe(null),
    onSuccess: setProbe,
    onError: (e) => setProbe({ ok: false, message: errorText(e) }),
  });
  const clear = useMutation({
    mutationFn: clearObjectStorage,
    onSuccess: (v) => {
      qc.setQueryData(['settings-object-storage'], v);
      form.resetFields();
      message.success('已清除对象存储配置');
    },
    onError: (e) => message.error(errorText(e)),
  });

  const confirmClear = () =>
    Modal.confirm({
      title: '清除对象存储配置',
      content: '清除后，带本机视频或音频参考素材的生成将无法提交。已上传到桶里的文件不会被删除。',
      okText: '清除',
      okButtonProps: { danger: true },
      onOk: () => clear.mutateAsync(),
    });

  const v = q.data;
  return (
    <Card
      title="对象存储"
      extra={v?.configured ? <Tag color="green">已配置</Tag> : <Tag>未配置</Tag>}
    >
      <Typography.Paragraph type="secondary">
        视频、音频参考素材会上传到你自己的对象存储，并用 24 小时有效的私有链接交给模型。建议给桶设置生命周期规则（如 7 天自动删除）以控制费用。
      </Typography.Paragraph>
      <Form form={form} layout="vertical" initialValues={{ provider: 'tos', prefix: 'miaoju-inputs' }}>
        <Form.Item label="服务商" name="provider">
          <Select
            options={[
              { value: 'tos', label: '火山引擎 TOS' },
              { value: 'oss', label: '阿里云 OSS' },
            ]}
          />
        </Form.Item>
        <Form.Item label="Endpoint" name="endpoint">
          <Input placeholder={EXAMPLES[provider].endpoint} />
        </Form.Item>
        <Form.Item label="Region" name="region">
          <Input placeholder={EXAMPLES[provider].region} />
        </Form.Item>
        <Form.Item label="Bucket" name="bucket">
          <Input />
        </Form.Item>
        <Form.Item label="路径前缀" name="prefix">
          <Input placeholder="miaoju-inputs" />
        </Form.Item>
        <Form.Item label="AccessKey" name="accessKey">
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item label="SecretKey" name="secretKey">
          <Input.Password
            autoComplete="new-password"
            placeholder={v?.secretKeySet ? `已保存 ${v.secretKeyMasked}，留空表示不修改` : ''}
          />
        </Form.Item>
      </Form>
      {probe && (
        <Alert
          style={{ marginBottom: 12 }}
          showIcon
          type={probe.ok ? 'success' : 'error'}
          message={probe.ok ? probe.message : `${probe.step ? `【${STEP_LABEL[probe.step] ?? probe.step}】` : ''}${probe.message}`}
        />
      )}
      <Space>
        <Button onClick={() => test.mutate()} loading={test.isPending}>测试连接</Button>
        <Button type="primary" onClick={() => save.mutate()} loading={save.isPending}>保存</Button>
        {v?.configured && <Button danger onClick={confirmClear}>清除配置</Button>}
      </Space>
    </Card>
  );
}
```

- [ ] **Step 5: `GeneralPage.tsx`** 加 `import { ObjectStorageCard } from '../components/ObjectStorageCard';`，在 `<Card title="数据目录" …>` 之前插入 `<ObjectStorageCard />`。`GeneralPage.test.tsx` 的 `vi.mock('../api')` 追加 `getObjectStorage: async () => ({ configured: false, provider: '', endpoint: '', region: '', bucket: '', prefix: '', accessKey: '', secretKeySet: false, secretKeyMasked: '' })`。

- [ ] **Step 6: 运行**：`cd web && pnpm vitest run src/modules/settings && pnpm tsc --noEmit -p .` → PASS。

- [ ] **Step 7: 提交**

```bash
git add web/src/modules/settings
git commit -m "feat(web): 设置页新增对象存储配置卡片"
```

---

### Task 8: sidecar 装配 + 文档收尾

**Files:**
- Modify: `server/cmd/sidecar/wire.go`（构造 Publisher、注入生成与合规、设置接口）
- Modify: `server/cmd/sidecar/agent_wiring.go`（`agentWiringDeps.Publisher`、传给 resolver）
- Modify: `server/cmd/sidecar/main.go:153-160`（传 `Publisher: a.publisher`）
- Test: `server/cmd/sidecar/gen_wiring_test.go` 或新建 `objectstore_wiring_test.go`
- Modify: `docs/specs/2026-09-29-canvas-desktop-spec.md`（S12 行 + 附录）、`docs/superpowers/plans/p1-research.md` 第 1 节、`docs/manual-acceptance-checklist.md`

**Interfaces:**
- Consumes: Task 2–6 全部。
- Produces: `app.publisher *objectstore.Publisher`。

- [ ] **Step 1: 写装配测试**（新建 `server/cmd/sidecar/objectstore_wiring_test.go`；`wire` 的调用方式照 `gen_wiring_test.go` 现有用例）

```go
package main

import "testing"

func TestWireInjectsObjectStorePublisher(t *testing.T) {
	a := newTestApp(t) // 以 gen_wiring_test.go / wire 测试里现有的构造辅助为准
	if a.publisher == nil {
		t.Fatal("wire 应构造对象存储 Publisher")
	}
	if a.deps.SettingsCenter.ObjectStore == nil {
		t.Fatal("设置接口应拿到 ObjectStore（连接测试）")
	}
	if a.publisher.Configured() {
		t.Fatal("新库默认未配置")
	}
}
```

- [ ] **Step 2: 改 `wire.go`**：在 `signer := apphttp.NewMediaSigner(storage)` 之后加

```go
	// 对象存储（用户自配 TOS/OSS）：本机视频/音频经它交给上游（对象存储设计 §3.2）。
	publisher := objectstore.NewPublisher(settings.NewSQLiteRepository(conn), storage, nil)
```

`wireGeneration(...)` 返回后加 `genW.Gen.SetMediaPublisher(publisher)`；`librarySeedanceSvc` 构造后加 `librarySeedanceSvc.SetMediaPublisher(publisher)`；`deps.SettingsCenter` 里加 `ObjectStore: publisher,`；`app` 结构体加 `publisher *objectstore.Publisher` 并在 return 时赋值。

- [ ] **Step 3: 改 Agent 装配**：`agentWiringDeps` 加 `Publisher agent.MediaPublisher`；`resolver := &agent.LocalMediaResolver{Keys: d.Keys, Objects: objects, Publisher: d.Publisher}`；`main.go` 的 `wireAgent(ctx, agentWiringDeps{…})` 加 `Publisher: a.publisher,`。

- [ ] **Step 4: 运行全量**

```bash
cd server && go build ./... && go vet ./... && go test ./...
cd ../web && pnpm vitest run && pnpm tsc --noEmit -p .
```

Expected: 全部 PASS。

- [ ] **Step 5: 文档**
  - 主 spec S12 行状态改为 `✅ 已确认（2026-10-07）：图片改 data_uri 内嵌；视频/音频上传到用户自配的 TOS/OSS，见 docs/superpowers/specs/2026-10-07-object-storage-design.md`；变更记录表追加 `| 2026-10-07 | S12 | ✅ 用户自配对象存储 + 图片 data_uri | 用户决定 |`。
  - `p1-research.md` 第 1 节末尾的「**待用户决定**」改为「已决定（2026-10-07）：见 `docs/superpowers/specs/2026-10-07-object-storage-design.md`」。
  - `docs/manual-acceptance-checklist.md` 追加一节「对象存储」：
    1. 设置 → 配置 → 对象存储，填 TOS 配置，点「测试连接」显示连接正常；
    2. 用本机视频作参考跑一次 doubao-seedance 生成，成功，桶里出现 `miaoju-inputs/<哈希>.mp4`；
    3. 同一视频再生成一次，桶里不新增对象；
    4. 换成 OSS 配置重复 1–2；
    5. 用本机图片作参考跑一次 seedream，成功且桶里没有新增图片；
    6. 清除配置后再跑步骤 2，报「未配置对象存储…」。

- [ ] **Step 6: 提交**

```bash
git add server/cmd docs
git commit -m "feat(sidecar): 装配对象存储 Publisher；S12 定稿"
```
