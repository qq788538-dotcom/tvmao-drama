# Seedance 合规校验改走内置模型 seedance-asset 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 合规校验改为同步调用隐藏的内置模型 `seedance-asset`，拿到 `metadata.asset_id` 即通过，失败原样显示上游错误；删掉建组、轮询、重试和 `seedance_compliance_config`。

**Architecture:** 种子里新增模型行（Task 1）；`seedance` 包新增 `Registrar` 按模型配置同步调上游（Task 2，纯新增，不删旧代码以免并行时编译中断）；Task 3 在两者合入后简化 `library.SeedanceService`、改装配、删除旧客户端与 worker、更新文档。

**Tech Stack:** Go 1.26、net/http、`upstream.InterpolateTemplate`、`models.ResolveUpstream`。

**Spec:** `docs/superpowers/specs/2026-10-07-seedance-asset-model-design.md`

## Global Constraints

- 模型 `model_id` 恒为 `seedance-asset`；`hidden=1`；`base_path` = `/v1/images/generations`；请求模板 `{"model": "seedance-asset", "image": "{{image}}", "metadata": {"asset_type": "{{asset_type}}"}}`。
- `asset_type` 取值 `Image` / `Video` / `Audio`；图片也必须带。
- 成功 = HTTP 2xx 且 `metadata.asset_id` 非空；失败信息取上游原文（优先 `error.message`，否则响应体前 500 字），原样给用户。
- 上游地址与 Key 一律经 `models.ResolveUpstream(model, settings)`。
- 同步调用超时 300 秒。错误信息不得包含 API Key。
- 素材 URL 一律经现有 `signedURL`（对象存储发布）得到；未配置对象存储沿用现有引导文案。
- 种子只改 `seed/`，再跑 `scripts/sync-seed.sh`。`go get` 若需要用 `GOPROXY=https://goproxy.cn,direct`。
- 不写数据库迁移；`seedance_assets.version` 写入固定为 1；`users.seedance_group_*` 列保留不用。
- 注释、用户可见文案用中文。

## Review Focus

1. **上游返回 2xx 但 body 是错误**（newapi 有时 200 + `{"error":…}`）：必须判失败并显示原文。→ Task 2 `TestRegisterOKWithoutAssetIDFails`。
2. **错误体不是 JSON**（网关 502 HTML）：截断到 500 字显示，不 panic。→ Task 2 `TestRegisterNonJSONErrorTruncated`。
3. **校验失败后再点一次**：必须真的重新提交（不被 failed_permanent 挡住）。→ Task 3 `TestSubmitRetriesAfterFailure`。
4. **模型被禁用 / 种子里不存在**：入口显示"未配置"，Submit 返回 `ErrSeedanceDisabled`，不调上游。→ Task 2 `TestConfiguredFollowsModel`、Task 3 `TestSubmitDisabledWhenModelOff`。
5. **API Key 不出现在任何错误里**（上游把 Authorization 回显在错误体中）。→ Task 2 `TestRegisterErrorRedactsKey`。

---

### Task 1: 种子新增 seedance-asset 模型

**Files:** Modify `seed/model_configs.jsonl`、`seed/README.md`、`server/internal/builtin/seed_test.go`；由脚本同步 `server/internal/builtin/seeddata/model_configs.jsonl`。

**Interfaces:** Produces 内置模型行 `model_id=seedance-asset`（id 51）。

- [ ] **Step 1: 写失败测试**（追加到 `seed_test.go`）

```go
// 合规校验走隐藏的内置模型（seedance-asset 设计 §2）。
func TestSeedHasHiddenSeedanceAssetModel(t *testing.T) {
	s, err := LoadEmbeddedSeed()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.Models {
		if m.ModelID != "seedance-asset" {
			continue
		}
		if !m.Hidden || !m.Enabled || m.BasePath != "/v1/images/generations" {
			t.Fatalf("seedance-asset 配置不对: %+v", m)
		}
		var tpl map[string]any
		if err := json.Unmarshal(m.RequestTemplate, &tpl); err != nil {
			t.Fatal(err)
		}
		md, _ := tpl["metadata"].(map[string]any)
		if tpl["model"] != "seedance-asset" || tpl["image"] != "{{image}}" || md["asset_type"] != "{{asset_type}}" {
			t.Fatalf("模板不对: %s", m.RequestTemplate)
		}
		return
	}
	t.Fatal("种子里没有 seedance-asset")
}
```

并把 `TestSeedImagesAreInline` 的循环里加一行排除：`if m.ModelID == "seedance-asset" { continue } // 不经过 prepareImageInputs`。

> 加载函数和字段名（`LoadEmbeddedSeed`、`s.Models`、`Hidden`、`Enabled`、`BasePath`、`RequestTemplate`）以 `seed_test.go` 现有用法为准，类型不同（如 Hidden 是 int）就照实际改断言。

- [ ] **Step 2: 运行确认失败**：`cd server && go test ./internal/builtin/ -run SeedanceAsset` → FAIL「种子里没有 seedance-asset」。

- [ ] **Step 3: 追加种子行**：复制 `seed/model_configs.jsonl` 中 `z-image-rh`（id 48）那一行作为字段顺序模板，用 python 按原键顺序生成新行追加到文件末尾（保持与其他行一致的 `json.dumps(ensure_ascii=False)` 风格；先看一行现有内容确认分隔符是 `", "` 与 `": "`），字段值：

```
id=51, name="Seedance 合规校验", type="image", hidden=1, enabled=1, model_id="seedance-asset",
base_path="/v1/images/generations", ui_schema=null, admin_note="合规校验专用，勿在画布使用",
price_note="", sort_order=0, created_at/updated_at="2026-10-07 00:00:00.000000",
gen_control={"modules": {"canvas": {"maxRetries": 0, "retryIntervalSec": 0}}, "concurrency": 0},
billing_config={"items": [], "flatFee": 0, "freezeEstimate": 0}, polling_template=null,
request_template={"model": "seedance-asset", "image": "{{image}}", "metadata": {"asset_type": "{{asset_type}}"}},
image_input_format="url", image_request_mode="json"
```

然后 `scripts/sync-seed.sh`。`seed/README.md` 的 model_configs 行数 26→27，说明末尾追加「2026-10-07：新增隐藏模型 seedance-asset（id 51），合规校验专用」。

- [ ] **Step 4: 运行**：`cd server && go test ./internal/builtin/... ./internal/agent/agentseed/...` → PASS（若有断言模型总数的测试，按 27 更新）。

- [ ] **Step 5: 提交** `feat(seed): 新增隐藏内置模型 seedance-asset（合规校验）`。

---

### Task 2: seedance.Registrar（纯新增）

**Files:** Create `server/internal/seedance/registrar.go`、`server/internal/seedance/registrar_test.go`。不修改、不删除 `client.go`（Task 3 删）。

**Interfaces:**
- Produces:
  - `const ModelID = "seedance-asset"`
  - `type ModelLookup interface{ GetByModelID(modelID string) (*models.ModelConfig, error) }`
  - `type UpstreamError struct{ Status int; Message string }`（`Error()` 返回 Message）
  - `func NewRegistrar(m ModelLookup, settings models.UpstreamSettings, httpClient *http.Client) *Registrar`（httpClient 为 nil 时用 300 秒超时的默认 client）
  - `func (r *Registrar) Configured() bool`
  - `func (r *Registrar) Register(ctx context.Context, url, assetType string) (string, error)`
  - `var ErrModelUnavailable = errors.New("合规校验模型 seedance-asset 未启用或上游未配置")`

- [ ] **Step 1: 写 `registrar_test.go`**

```go
package seedance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

type fakeModels struct{ m *models.ModelConfig }

func (f fakeModels) GetByModelID(id string) (*models.ModelConfig, error) {
	if f.m == nil || f.m.ModelID != id {
		return nil, nil
	}
	return f.m, nil
}

type fakeSettings map[string]string

func (f fakeSettings) Get(k string) (string, error) { return f[k], nil }

const testKey = "sk-test-SECRET-123"

func assetModel(enabled bool) *models.ModelConfig {
	return &models.ModelConfig{ModelID: ModelID, Enabled: enabled, BasePath: "/v1/images/generations",
		RequestTemplate: json.RawMessage(`{"model":"seedance-asset","image":"{{image}}","metadata":{"asset_type":"{{asset_type}}"}}`)}
}

func newReg(t *testing.T, h http.HandlerFunc, enabled bool) *Registrar {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewRegistrar(fakeModels{assetModel(enabled)},
		fakeSettings{models.SettingBaseDomain: srv.URL, models.SettingUpstreamAPIKey: testKey}, nil)
}

func TestRegisterSendsTemplateAndReturnsAssetID(t *testing.T) {
	var got map[string]any
	var auth, path string
	r := newReg(t, func(w http.ResponseWriter, req *http.Request) {
		auth, path = req.Header.Get("Authorization"), req.URL.Path
		b, _ := io.ReadAll(req.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"created":1,"data":[{"url":"https://x"}],"metadata":{"asset_id":"asset-1","asset_type":"Video","group_id":"g"}}`))
	}, true)
	id, err := r.Register(context.Background(), "https://bucket/v.mp4", AssetTypeVideo)
	if err != nil || id != "asset-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	md, _ := got["metadata"].(map[string]any)
	if got["model"] != "seedance-asset" || got["image"] != "https://bucket/v.mp4" || md["asset_type"] != "Video" {
		t.Fatalf("请求体不对: %v", got)
	}
	if auth != "Bearer "+testKey || !strings.HasSuffix(path, "/v1/images/generations") {
		t.Fatalf("auth=%q path=%q", auth, path)
	}
}

func TestRegisterOKWithoutAssetIDFails(t *testing.T) {
	r := newReg(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"error":{"message":"素材不合规：检测到真人"}}`))
	}, true)
	_, err := r.Register(context.Background(), "https://b/a.png", AssetTypeImage)
	if err == nil || !strings.Contains(err.Error(), "素材不合规：检测到真人") {
		t.Fatalf("err=%v", err)
	}
}

func TestRegisterUpstreamErrorMessage(t *testing.T) {
	r := newReg(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"InvalidParameter: 图片宽高比超出范围"}}`))
	}, true)
	_, err := r.Register(context.Background(), "https://b/a.png", AssetTypeImage)
	var ue *UpstreamError
	if !errors.As(err, &ue) || ue.Status != 400 || ue.Message != "InvalidParameter: 图片宽高比超出范围" {
		t.Fatalf("err=%v", err)
	}
}

func TestRegisterNonJSONErrorTruncated(t *testing.T) {
	r := newReg(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("<html>" + strings.Repeat("x", 2000) + "</html>"))
	}, true)
	_, err := r.Register(context.Background(), "https://b/a.png", AssetTypeImage)
	if err == nil || len([]rune(err.Error())) > 520 || !strings.Contains(err.Error(), "<html>") {
		t.Fatalf("err len=%d: %v", len(err.Error()), err)
	}
}

func TestRegisterErrorRedactsKey(t *testing.T) {
	r := newReg(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid token Bearer ` + testKey + `"}}`))
	}, true)
	_, err := r.Register(context.Background(), "https://b/a.png", AssetTypeImage)
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("错误里不得出现 Key: %v", err)
	}
}

func TestConfiguredFollowsModel(t *testing.T) {
	called := false
	off := newReg(t, func(http.ResponseWriter, *http.Request) { called = true }, false)
	if off.Configured() {
		t.Fatal("模型禁用应未配置")
	}
	if _, err := off.Register(context.Background(), "https://b/a.png", AssetTypeImage); !errors.Is(err, ErrModelUnavailable) || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	missing := NewRegistrar(fakeModels{}, fakeSettings{models.SettingBaseDomain: "http://x", models.SettingUpstreamAPIKey: "k"}, nil)
	if missing.Configured() {
		t.Fatal("模型不存在应未配置")
	}
	noKey := NewRegistrar(fakeModels{assetModel(true)}, fakeSettings{models.SettingBaseDomain: "http://x"}, nil)
	if noKey.Configured() {
		t.Fatal("没有 Key 应未配置")
	}
	on := newReg(t, func(http.ResponseWriter, *http.Request) {}, true)
	if !on.Configured() {
		t.Fatal("启用且有上游应已配置")
	}
}
```

> `models.ModelConfig` 的字段名、`GetByModelID` 找不到时返回 `(nil, nil)` 还是 `(nil, err)`，以 `server/internal/models/repository.go:257` 为准；两种都当"不存在"处理。`models.UpstreamSettings` 的方法集以 `models/upstream.go` 为准。

- [ ] **Step 2: 运行确认失败**：`cd server && go test ./internal/seedance/ -run 'Register|Configured'` → 编译失败。

- [ ] **Step 3: 写 `registrar.go`**

```go
package seedance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

// ModelID 是合规校验专用的隐藏内置模型（seedance-asset 设计 §2）。
const ModelID = "seedance-asset"

const registerTimeout = 300 * time.Second

// ErrModelUnavailable：模型不存在、被禁用，或解析不出上游地址与 Key。
var ErrModelUnavailable = errors.New("合规校验模型 seedance-asset 未启用或上游未配置")

type ModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// UpstreamError 携带上游错误原文，界面原样显示（设计 A2）。
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string { return e.Message }

// Registrar 同步调用 seedance-asset，把公网 URL 注册为 Seedance 素材，返回 asset_id。
type Registrar struct {
	models   ModelLookup
	settings models.UpstreamSettings
	http     *http.Client
}

func NewRegistrar(m ModelLookup, settings models.UpstreamSettings, httpClient *http.Client) *Registrar {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: registerTimeout}
	}
	return &Registrar{models: m, settings: settings, http: httpClient}
}

// resolve 取模型与上游；任何一项缺失都返回 ErrModelUnavailable。
func (r *Registrar) resolve() (*models.ModelConfig, string, string, error) {
	m, err := r.models.GetByModelID(ModelID)
	if err != nil || m == nil || !m.Enabled {
		return nil, "", "", ErrModelUnavailable
	}
	base, key, err := models.ResolveUpstream(m, r.settings)
	if err != nil || strings.TrimSpace(base) == "" || strings.TrimSpace(key) == "" {
		return nil, "", "", ErrModelUnavailable
	}
	return m, strings.TrimRight(base, "/"), key, nil
}

func (r *Registrar) Configured() bool {
	_, _, _, err := r.resolve()
	return err == nil
}

func (r *Registrar) Register(ctx context.Context, url, assetType string) (string, error) {
	m, base, key, err := r.resolve()
	if err != nil {
		return "", err
	}
	body, err := upstream.InterpolateTemplate(m.RequestTemplate, map[string]any{"image": url, "asset_type": assetType})
	if err != nil {
		return "", fmt.Errorf("合规校验请求模板错误: %w", err)
	}
	path := m.BasePath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := r.http.Do(req)
	if err != nil {
		return "", errors.New(redactKey("合规校验请求失败："+err.Error(), key))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out struct {
		Metadata struct {
			AssetID string `json:"asset_id"`
		} `json:"metadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 == 2 && out.Metadata.AssetID != "" {
		return out.Metadata.AssetID, nil
	}
	msg := ""
	if out.Error != nil {
		msg = strings.TrimSpace(out.Error.Message)
	}
	if msg == "" {
		msg = truncateRunes(strings.TrimSpace(string(raw)), 500)
	}
	if msg == "" {
		msg = fmt.Sprintf("上游未返回 asset_id（HTTP %d）", resp.StatusCode)
	} else if resp.StatusCode/100 == 2 && out.Error == nil {
		msg = "上游未返回 asset_id：" + msg
	}
	return "", &UpstreamError{Status: resp.StatusCode, Message: redactKey(msg, key)}
}

func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

> 检查 `seedance` 包导入 `upstream`、`models` 不构成循环（`go build ./...` 即可验证）；若 `upstream` 已 import `seedance`，改为在 registrar 内用 `json` 手工替换模板占位符并报告。

- [ ] **Step 4: 运行**：`cd server && go vet ./internal/seedance/ && go test ./internal/seedance/ && go build ./...` → PASS。

- [ ] **Step 5: 提交** `feat(seedance): 新增按内置模型同步注册素材的 Registrar`。

---

### Task 3: 简化 SeedanceService、改装配、删旧客户端、文档

**Files:**
- Modify: `server/internal/library/seedance_service.go`、`seedance_service_test.go`
- Delete: `server/internal/library/seedance_groups_repo.go`（及其测试中对应用例）
- Modify: `server/internal/seedance/client.go`（删掉旧 API，只保留 `AssetType*` 常量与 `NormalizeAssetType` 依赖的内容；可整体并入 `registrar.go` 后删文件）、`client_test.go`（删旧用例）
- Modify: `server/cmd/sidecar/wire.go:99-110`、`server/cmd/sidecar/main.go:199`
- Modify: `server/internal/http/seedance_handlers_test.go`（适配同步结果）
- Modify: `docs/specs/2026-09-29-canvas-desktop-spec.md`（X5、P7、变更记录）、`docs/manual-acceptance-checklist.md`

**Interfaces:**
- Consumes: Task 2 `seedance.NewRegistrar`、`(*Registrar).Configured/Register`、`*seedance.UpstreamError`、`seedance.ErrModelUnavailable`；Task 1 种子模型。
- Produces: `library.NewSeedanceService(repo SeedanceAssetsRepository, client seedanceAPI, signer urlSigner) *SeedanceService`，其中 `seedanceAPI = interface{ Configured() bool; Register(ctx context.Context, url, assetType string) (string, error) }`。

- [ ] **Step 1: 改 library 测试**：删除 `TestSubmitCreatesGroupAndAsset`、`TestSubmitReusesUserGroup`、`TestSubmitRecreatesGroupOnVersionBump`、所有 `TestReconcile*`、`TestSubmitNoKeyDisabled`、`TestSubmitDisabledWhenAnyPieceMissing`、`TestConfiguredWhenComplete`、`TestErrSeedanceDisabledMentionsUnconfigured`；fake client 改成实现新 `seedanceAPI`。新增：

```go
type fakeRegistrar struct {
	configured bool
	id         string
	err        error
	calls      int
	gotURL     string
	gotType    string
}

func (f *fakeRegistrar) Configured() bool { return f.configured }
func (f *fakeRegistrar) Register(_ context.Context, url, t string) (string, error) {
	f.calls++
	f.gotURL, f.gotType = url, t
	return f.id, f.err
}

func TestSubmitActiveOnSuccess(t *testing.T) {
	svc, repo, reg := newSyncSvc(t, &fakeRegistrar{configured: true, id: "asset-1"})
	rec, err := svc.Submit(context.Background(), 1, "u1/a.mp4", seedance.AssetTypeVideo)
	if err != nil || rec.Status != "active" || *rec.AssetID != "asset-1" || reg.gotType != "Video" {
		t.Fatalf("rec=%+v err=%v", rec, err)
	}
	got, _ := repo.Get("u1/a.mp4")
	if got.Status != "active" || *got.Version != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestSubmitFailureStoresUpstreamMessage(t *testing.T) {
	reg := &fakeRegistrar{configured: true, err: &seedance.UpstreamError{Status: 400, Message: "素材不合规：检测到真人"}}
	svc, repo, _ := newSyncSvc(t, reg)
	_, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	if err == nil || err.Error() != "素材不合规：检测到真人" {
		t.Fatalf("err=%v", err)
	}
	got, _ := repo.Get("u1/a.png")
	if got.Status != "failed_permanent" || *got.Error != "素材不合规：检测到真人" {
		t.Fatalf("got %+v", got)
	}
}

func TestSubmitRetriesAfterFailure(t *testing.T) {
	reg := &fakeRegistrar{configured: true, err: errors.New("boom")}
	svc, _, _ := newSyncSvc(t, reg)
	svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	reg.err, reg.id = nil, "asset-2"
	rec, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage)
	if err != nil || rec.Status != "active" || reg.calls != 2 {
		t.Fatalf("rec=%+v err=%v calls=%d", rec, err, reg.calls)
	}
}

func TestSubmitDisabledWhenModelOff(t *testing.T) {
	reg := &fakeRegistrar{configured: false}
	svc, _, _ := newSyncSvc(t, reg)
	if _, err := svc.Submit(context.Background(), 1, "u1/a.png", seedance.AssetTypeImage); !errors.Is(err, ErrSeedanceDisabled) || reg.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, reg.calls)
	}
	if svc.Configured() {
		t.Fatal("应未配置")
	}
}
```

`newSyncSvc` 照现有测试里构造服务的辅助函数写（同样的 repo、signer 返回公网 URL 的 stub），返回 `(svc, repo, reg)`。保留 `TestSubmitIdempotentWhenActive`、`TestSubmitRefusesLoopbackURL`、`TestSeedanceSignedURLPublishesLoopback`、所有 `TestResolve*`/`TestStatus*`/公共资产用例，按新构造函数改调用。

- [ ] **Step 2: 运行确认失败**：`cd server && go test ./internal/library/` → 编译失败。

- [ ] **Step 3: 改 `seedance_service.go`**
  - 结构体去掉 `groups`、`settings`、`now`；`seedanceAPI` 换成上面的接口；构造函数按 Produces 签名。
  - `ErrSeedanceDisabled = errors.New("合规校验未启用：内置模型 seedance-asset 未启用或上游地址、API Key 未配置")`。
  - `Configured()` 返回 `s.client.Configured()`。
  - `Submit`：

```go
func (s *SeedanceService) Submit(ctx context.Context, userID int64, objectKey, assetType string) (*SeedanceAsset, error) {
	if !s.client.Configured() {
		return nil, ErrSeedanceDisabled
	}
	existing, err := s.repo.Get(objectKey)
	if err != nil && !errors.Is(err, ErrSeedanceAssetNotFound) {
		return nil, err
	}
	if existing != nil && existing.UserID == userID && existing.Status == "active" &&
		existing.AssetID != nil && *existing.AssetID != "" {
		return existing, nil // 已通过 → 不重复占用上游配额
	}
	url, err := s.signedURL(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	// 同步注册（seedance-asset 设计 A2）：拿到 asset_id 即通过；失败原样记录上游原文。
	aid, err := s.client.Register(ctx, url, assetType)
	if err != nil {
		_ = s.repo.Upsert(SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetType: assetType, Status: seedanceStatusFailedPermanent})
		_ = s.repo.SetError(objectKey, seedanceStatusFailedPermanent, err.Error())
		return nil, err
	}
	version := 1
	rec := SeedanceAsset{ObjectKey: objectKey, UserID: userID, AssetID: &aid, Version: &version, AssetType: assetType, Status: "active"}
	if err := s.repo.Upsert(rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
```

  - 删除 `isPermanentSeedanceErr`、`config()`、`ensureGroup`、`RunWorker`、`ReconcileAll`、`reconcileOne`、`seedanceRetryWindow`、`seedanceGroupStore`、`settingsReader`（若无其他使用）以及顶部 `settingUpstreamAPIKey`/`settingBaseDomain` 常量与 X5 旧注释；`seedanceStatusFailedPermanent` 注释改为「校验失败（上游原文见 error）；用户可再次提交」。确认 `repo.Upsert` 会把旧的 error 清掉或 Submit 成功后 error 为空（必要时成功后 `SetError(objectKey, "active", "")` 不合适——看 repo 实现，Upsert 若不覆盖 error 列，就在 Upsert SQL 里一并写 `error = NULL, first_failed_at = NULL`）。
  - 删除 `seedance_groups_repo.go` 与其测试。

- [ ] **Step 4: 删旧客户端**：`server/internal/seedance/client.go` 只保留 `AssetTypeImage/Video/Audio` 常量（移到 `registrar.go` 后删除 `client.go`），删除 `client_test.go` 中旧 API 的用例（文件若因此为空则删除）。`grep -rn "seedance\.\(NewClient\|Paths\|LoadComplianceConfig\|APIError\|SettingComplianceConfig\)" server/` 必须为空。

- [ ] **Step 5: 改装配**
  - `wire.go`：

```go
	// Seedance 合规走隐藏内置模型 seedance-asset（seedance-asset 设计）：同步注册，拿到 asset_id 即通过。
	librarySeedanceSvc := library.NewSeedanceService(
		library.NewSQLiteSeedanceAssetsRepository(conn),
		seedance.NewRegistrar(modelsRepo, settingsRepo, nil),
		signer)
```

  - `main.go:199` 删除 `a.seedance.RunWorker(ctx)` 那行 worker。
  - 修 `server/internal/http/seedance_handlers_test.go` 与 `server/cmd/sidecar` 里所有受影响的测试（构造函数、`Verify` 返回 202 的断言保持，状态从 pending 改为 active）。

- [ ] **Step 6: 文档**：主 spec X5 行追加「**2026-10-07 变更**：改为隐藏内置模型 seedance-asset，同步返回 asset_id，见 `docs/superpowers/specs/2026-10-07-seedance-asset-model-design.md`」；P7 行状态追加「合规部分 2026-10-07 完成（AI 角色库仍不做）」；变更记录表追加 `| 2026-10-07 | X5 | ✏️ 合规改走内置模型 seedance-asset，同步 | 用户决定 |`。`docs/manual-acceptance-checklist.md` 追加一条：配置对象存储后对一张图、一段视频点「合规校验」→ 角标变已通过；用该素材跑 seedance 生成 → 上游请求里是 `asset://…`；故意用不合规素材 → 角标显示上游原文。

- [ ] **Step 7: 全量验证**：`cd server && go build ./... && go vet ./... && go test ./...`；`cd web && pnpm vitest run src/modules/canvas`（确认合规相关前端测试仍过）。

- [ ] **Step 8: 提交** `refactor(seedance): 合规校验改为同步调用 seedance-asset，删除建组、轮询与旧配置`。
