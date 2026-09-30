# P0 工程骨架与启动握手 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建好 `src-tauri/` + `server/` + `web/` 三块骨架，让 Tauri 壳能拉起 Go sidecar、完成 token 握手，前端能带 token 调通 HTTP、WebSocket 和媒体 Range 请求，并在 macOS 与 Windows 上跑通同一套 WebCodecs 诊断。

**Architecture:** Tauri v2 的 Rust 薄壳启动 Go sidecar，通过 stdin 传入随机 token，sidecar 监听 `127.0.0.1` 随机端口并在 stdout 打印 `READY <port>`。前端通过 `server_info` 命令拿到端口和 token，之后所有请求直连 sidecar。sidecar 在 stdin 关闭时自行退出，避免父进程崩溃后残留。

**Tech Stack:** Tauri 2（tauri-plugin-shell、tauri-plugin-single-instance）、Go 1.25（chi v5、coder/websocket）、React 18 + Vite 5 + TypeScript 5 + vitest 2、mediabunny ^1.54.0、pnpm 9。

**Spec:** `docs/specs/2026-09-29-canvas-desktop-spec.md`（本计划实现第 8 节的 P0，并落地 D1–D3、D6–D8、SEC1–SEC4、S1、S11、E1、E2、E6 的骨架部分）

**后续计划（各自独立成文，本计划不含）:** P1 Go sidecar 业务迁移 · P2 内置数据 · P3 前端迁移 · P4 设置页 · P5 Agent · P6 脚本节点分镜 · P7 newapi 对接（挂起）· P8 打包发布。

## Global Constraints

- 应用名 `喵剧AI`，内部标识 `com.miaoju.desktop`（定后不可改，决定数据目录名）。
- 目标平台只有 macOS（最低 14.0）和 Windows x64；不做 Linux。
- 目录结构固定为 `src-tauri/`、`web/`、`server/`、`seed/`、`docs/`。
- `server/` 的 Go module 路径保持 `github.com/james/huabu_studio/apps/api`，内部包路径与参考仓 `apps/api/internal/*` 一致（后续从参考仓复制代码时不改 import）。
- `web/` 的 `@/` 别名指向 `web/src`，与参考仓 `apps/web` 一致。
- sidecar 只监听 `127.0.0.1`，端口由系统分配（`:0`）。
- 启动 token 只通过 stdin 传给 sidecar；不得出现在命令行参数或环境变量里。
- 除 CORS 预检（OPTIONS）外，所有 HTTP 和 WebSocket 请求都必须带 token：`Authorization: Bearer <token>` 或 URL 参数 `?token=<token>`；带了 `Origin` 头的请求，Origin 必须在白名单内。
- Origin 白名单：`tauri://localhost`（macOS）、`http://tauri.localhost`（Windows）、`http://localhost:5173`（开发）。
- 单实例、单窗口：再次启动只把已有窗口带到前台。
- 不做签名、公证、自动更新；macOS 用 ad-hoc 签名（`signingIdentity: "-"`）。
- 与参考仓不同步、独立演进；`UPSTREAM.md` 只记录复制时的来源 commit。
- 代码注释用中文，风格与参考仓一致。

## Review Focus

1. **Tauri 进程被强杀或崩溃** → sidecar 不能残留成孤儿进程占着数据目录。（Task 4 的 `TestExitsWhenStdinCloses` 钉住）
2. **Windows 下 stdin 写入的 token 带 `\r\n`** → 握手仍成功，token 比较不因尾部空白失败。（Task 4 的 `TestReadTokenTrimsCRLF` 钉住）
3. **本机其他网页向 `127.0.0.1:<port>` 发请求**（带 `Origin: https://evil.example`，即使猜中 token）→ 一律 403。（Task 2 的 `TestRejectsForeignOriginEvenWithToken` 钉住）
4. **`<img>`/`<video>` 直接加载媒体**（浏览器不带 Origin、也不能带 Authorization 头）→ 用 `?token=` 能通过，且支持 Range 返回 206。（Task 3 的 `TestDiagMediaRange` 钉住）
5. **sidecar 在打印 READY 之前退出**（二进制缺失、数据目录不可写）→ 窗口显示“本地服务启动失败”和原因，不是白屏或一直转圈。（Task 5 的 `parse_ready` 单测 + Task 6 的 `getServerInfo` 失败用例钉住）

---

## 文件结构

```
miaoju-desktop/
  .gitignore
  UPSTREAM.md                       来源 commit 记录
  package.json                      根脚本（dev / build / test）
  pnpm-workspace.yaml
  scripts/
    build-sidecar.sh                按目标平台编译 sidecar 到 src-tauri/binaries/
    package-windows.sh              交叉编译并打 Windows zip
  server/
    go.mod
    cmd/sidecar/main.go             启动握手：读 token、监听、打印 READY、stdin 关闭即退出
    cmd/sidecar/main_test.go
    internal/localauth/middleware.go      token + Origin 校验、CORS
    internal/localauth/middleware_test.go
    internal/http/router.go         路由装配（P1 起在这里挂业务路由）
    internal/http/diag.go           P0 诊断接口：ping、WS 回显、测试媒体上传与读取
    internal/http/router_test.go
  src-tauri/
    Cargo.toml
    build.rs
    tauri.conf.json
    capabilities/default.json
    icons/                          由 cargo tauri icon 生成
    binaries/                       sidecar 构建产物（gitignore）
    src/main.rs
    src/lib.rs                      sidecar 生命周期、server_info 命令、单实例
  web/
    package.json
    index.html
    vite.config.ts
    tsconfig.json
    public/logo.png                 从参考仓复制
    src/main.tsx
    src/shared/server.ts            取端口和 token、拼 URL
    src/shared/server.test.ts
    src/diag/checks.ts              连接检查与 WebCodecs 检查
    src/diag/DiagPage.tsx           诊断页
```

---

### Task 1: 仓库初始化与目录骨架

**Files:**
- Create: `.gitignore`
- Create: `UPSTREAM.md`
- Create: `package.json`
- Create: `pnpm-workspace.yaml`

**Interfaces:**
- Consumes: 无
- Produces: 根脚本 `pnpm dev`、`pnpm build`、`pnpm test:web`、`pnpm test:server`，后续任务往里填实际内容。

- [ ] **Step 1: 初始化 git 并确认在 main 分支**

```bash
cd /Users/james/github/miaoju-desktop
git init -b main
```

Expected: `Initialized empty Git repository in /Users/james/github/miaoju-desktop/.git/`

- [ ] **Step 2: 写 `.gitignore`**

```gitignore
node_modules/
dist/
.DS_Store
*.log

# Rust / Tauri
src-tauri/target/
src-tauri/binaries/
src-tauri/gen/

# Go
server/bin/

# 本地数据与打包产物
/out/
.claude/
```

- [ ] **Step 3: 写 `UPSTREAM.md`**

```markdown
# 上游来源

本仓从参考仓复制代码后**独立演进，不做同步**（spec E2）。这里只记录每次复制的来源，供追溯。

| 日期 | 参考仓 commit | 复制内容 | 备注 |
|---|---|---|---|
| 2026-09-30 | `fea92ab9ad58ac1aa6752855999c221fcb460d89` | `apps/web/public/logo.png` | P0 骨架，仅复制 logo |

参考仓：`/Users/james/github/huabu_studio`
```

- [ ] **Step 4: 写根 `package.json` 和 `pnpm-workspace.yaml`**

`package.json`：

```json
{
  "name": "miaoju-desktop",
  "private": true,
  "version": "0.1.0",
  "scripts": {
    "dev": "scripts/build-sidecar.sh && cargo tauri dev",
    "build": "scripts/build-sidecar.sh && cargo tauri build",
    "test:web": "pnpm --dir web test",
    "test:server": "cd server && go test ./...",
    "test:tauri": "cd src-tauri && cargo test"
  }
}
```

`pnpm-workspace.yaml`：

```yaml
packages:
  - web
```

- [ ] **Step 5: 提交**

```bash
git add .gitignore UPSTREAM.md package.json pnpm-workspace.yaml docs seed
git commit -m "chore: 初始化仓库骨架，纳入 spec、计划与种子数据"
```

Expected: 提交成功，`git status` 干净（`.claude/` 被忽略）。

---

### Task 2: sidecar 鉴权中间件（token + Origin）

**Files:**
- Create: `server/go.mod`
- Create: `server/internal/localauth/middleware.go`
- Test: `server/internal/localauth/middleware_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `localauth.Config{Token string; AllowedOrigins []string}`
  - `localauth.Middleware(cfg Config) func(http.Handler) http.Handler`
  - 行为：OPTIONS 预检返回 204（Origin 不在白名单则 403）；有 `Origin` 且不在白名单 → 403；token 缺失或不匹配 → 401；token 取自 `Authorization: Bearer` 头，缺省时取 `?token=`。

- [ ] **Step 1: 建 Go module**

```bash
mkdir -p server/internal/localauth && cd server
cat > go.mod <<'EOF'
module github.com/james/huabu_studio/apps/api

go 1.25.0
EOF
```

- [ ] **Step 2: 写失败的测试**

`server/internal/localauth/middleware_test.go`：

```go
package localauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newHandler() http.Handler {
	cfg := Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}}
	return Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func do(t *testing.T, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	return rec
}

func TestRejectsMissingToken(t *testing.T) {
	if got := do(t, "GET", "/x", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", got)
	}
}

func TestRejectsWrongToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestAcceptsBearerToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{"Authorization": "Bearer " + testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

// <img>/<video> 发的请求带不了 Authorization 头，只能走 query。
func TestAcceptsQueryToken(t *testing.T) {
	if got := do(t, "GET", "/x?token="+testToken, nil).Code; got != http.StatusOK {
		t.Fatalf("code = %d, want 200", got)
	}
}

// 本机其他网页即使猜中 token 也必须被挡住。
func TestRejectsForeignOriginEvenWithToken(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{
		"Authorization": "Bearer " + testToken,
		"Origin":        "https://evil.example",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestAllowedOriginGetsCORSHeaders(t *testing.T) {
	rec := do(t, "GET", "/x", map[string]string{
		"Authorization": "Bearer " + testToken,
		"Origin":        "tauri://localhost",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Fatal("缺少 Expose-Headers，前端读不到 Content-Range")
	}
}

// 预检请求不带 token，白名单 Origin 要放行。
func TestPreflightAllowedOrigin(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{
		"Origin":                        "tauri://localhost",
		"Access-Control-Request-Method": "POST",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("预检缺少 Allow-Headers")
	}
}

func TestPreflightForeignOrigin(t *testing.T) {
	rec := do(t, "OPTIONS", "/x", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

// 空 token 配置是启动 bug，绝不能变成“任何请求都放行”。
func TestEmptyConfiguredTokenRejectsEverything(t *testing.T) {
	h := Middleware(Config{Token: ""})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x?token=", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/localauth/`
Expected: 编译失败，`undefined: Config`、`undefined: Middleware`

- [ ] **Step 4: 写实现**

`server/internal/localauth/middleware.go`：

```go
// Package localauth 是桌面端 sidecar 的本地鉴权：每次启动一个随机 token，
// 外加 Origin 白名单。sidecar 只监听 127.0.0.1，但本机浏览器里任意网页都能
// 向这个端口发请求，所以两道都不能省（spec SEC1、SEC2）。
package localauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

type Config struct {
	// Token 由 Tauri 壳每次启动生成，经 stdin 传入。为空时拒绝一切请求。
	Token string
	// AllowedOrigins 是允许的 Origin 全量白名单（完整匹配）。
	AllowedOrigins []string
}

func Middleware(cfg Config) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 带 Origin 的请求来自浏览器上下文；不在白名单的一律挡掉，哪怕 token 对。
			if origin := r.Header.Get("Origin"); origin != "" {
				if !allowed[origin] {
					http.Error(w, "origin not allowed", http.StatusForbidden)
					return
				}
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
			}
			// 预检请求浏览器不会带自定义头，token 校验留给随后的真实请求。
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if !tokenOK(r, cfg.Token) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenOK 先看 Authorization 头，没有再看 ?token=。后者给 <img>/<video> 和
// WebSocket 握手用——它们带不了自定义头。
func tokenOK(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
```

- [ ] **Step 5: 运行测试，确认通过**

Run: `cd server && go test ./internal/localauth/ -v`
Expected: 9 个测试全部 PASS

- [ ] **Step 6: 提交**

```bash
git add server/go.mod server/internal/localauth
git commit -m "feat(server): 本地鉴权中间件（token + Origin 白名单）"
```

---

### Task 3: sidecar 路由与 P0 诊断接口

**Files:**
- Create: `server/internal/http/router.go`
- Create: `server/internal/http/diag.go`
- Test: `server/internal/http/router_test.go`
- Modify: `server/go.mod`（新增依赖）

**Interfaces:**
- Consumes: `localauth.Config`、`localauth.Middleware`（Task 2）
- Produces:
  - `http.Deps{Auth localauth.Config; DataDir string; Version string}`（包名 `http`，导入路径 `github.com/james/huabu_studio/apps/api/internal/http`，与参考仓一致）
  - `http.NewRouter(d Deps) nethttp.Handler`
  - 路由（全部需要 token）：
    - `GET /api/v1/ping` → `{"ok":true,"version":"<Version>"}`
    - `GET /api/v1/agent/ws` → WebSocket，原样回显收到的每一帧（P5 会换成真正的 Agent hub）
    - `POST /api/v1/diag/upload`（multipart 字段 `file`）→ `{"name":"sample.<ext>"}`，文件存到 `<DataDir>/diag/`
    - `GET /api/v1/diag/media/{name}` → 返回上传的文件，支持 Range

- [ ] **Step 1: 加依赖**

```bash
cd server
go get github.com/go-chi/chi/v5@v5.2.1
go get github.com/coder/websocket@v1.8.14
```

- [ ] **Step 2: 写失败的测试**

`server/internal/http/router_test.go`：

```go
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewRouter(Deps{
		Auth:    localauth.Config{Token: testToken, AllowedOrigins: []string{"tauri://localhost"}},
		DataDir: t.TempDir(),
		Version: "test",
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPingRequiresToken(t *testing.T) {
	srv := newTestServer(t)
	resp, err := nethttp.Get(srv.URL + "/api/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", resp.StatusCode)
	}
}

func TestPing(t *testing.T) {
	srv := newTestServer(t)
	resp, err := nethttp.Get(srv.URL + "/api/v1/ping?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Version != "test" {
		t.Fatalf("body = %+v", body)
	}
}

// 预检请求打到只注册了 GET 的路径上，也必须拿到 204 而不是 405。
func TestPreflightOnGetOnlyRoute(t *testing.T) {
	srv := newTestServer(t)
	req, _ := nethttp.NewRequest("OPTIONS", srv.URL+"/api/v1/ping", nil)
	req.Header.Set("Origin", "tauri://localhost")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("code = %d, want 204", resp.StatusCode)
	}
}

func TestWebSocketEcho(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/agent/ws?token=" + testToken
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: nethttp.Header{"Origin": []string{"tauri://localhost"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("你好")); err != nil {
		t.Fatal(err)
	}
	_, got, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "你好" {
		t.Fatalf("echo = %q", got)
	}
}

func TestWebSocketRejectsWithoutToken(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/agent/ws"
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatal("没带 token 也连上了")
	}
	if resp == nil || resp.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("resp = %v, want 401", resp)
	}
}

func upload(t *testing.T, srv *httptest.Server, filename string, content []byte) *nethttp.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req, _ := nethttp.NewRequest("POST", srv.URL+"/api/v1/diag/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// <video> 拖动进度条靠 Range；没有 206 就只能从头播。
func TestDiagMediaRange(t *testing.T) {
	srv := newTestServer(t)
	content := []byte("0123456789abcdefghij")
	resp := upload(t, srv, "我的 视频.MP4", content)
	var out struct {
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK || out.Name != "sample.mp4" {
		t.Fatalf("upload: code=%d name=%q", resp.StatusCode, out.Name)
	}

	// 不带 Origin、不带 Authorization，只靠 ?token=（<video> 的请求形态）。
	req, _ := nethttp.NewRequest("GET", srv.URL+"/api/v1/diag/media/sample.mp4?token="+testToken, nil)
	req.Header.Set("Range", "bytes=5-9")
	got, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	if got.StatusCode != nethttp.StatusPartialContent {
		t.Fatalf("code = %d, want 206", got.StatusCode)
	}
	if cr := got.Header.Get("Content-Range"); cr != "bytes 5-9/20" {
		t.Fatalf("Content-Range = %q", cr)
	}
	body, _ := io.ReadAll(got.Body)
	if string(body) != "56789" {
		t.Fatalf("body = %q", body)
	}
}

func TestDiagUploadRejectsUnknownExtension(t *testing.T) {
	srv := newTestServer(t)
	resp := upload(t, srv, "evil.sh", []byte("#!/bin/sh"))
	resp.Body.Close()
	if resp.StatusCode != nethttp.StatusBadRequest {
		t.Fatalf("code = %d, want 400", resp.StatusCode)
	}
}

// 文件名只接受固定的 sample.<ext>，任何别的名字都当不存在。
func TestDiagMediaRejectsOtherNames(t *testing.T) {
	srv := newTestServer(t)
	for _, name := range []string{"..%2F..%2Fetc%2Fpasswd", "other.mp4", "sample.sh"} {
		resp, err := nethttp.Get(srv.URL + "/api/v1/diag/media/" + name + "?token=" + testToken)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != nethttp.StatusNotFound {
			t.Fatalf("%s: code = %d, want 404", name, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 3: 运行测试，确认失败**

Run: `cd server && go test ./internal/http/`
Expected: 编译失败，`undefined: NewRouter`、`undefined: Deps`

- [ ] **Step 4: 写路由**

`server/internal/http/router.go`：

```go
// Package http 装配 sidecar 的全部 HTTP 路由。包名和路径与参考仓
// apps/api/internal/http 一致，P1 起业务 handler 直接落在这个包里。
package http

import (
	"encoding/json"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// Deps 是 DI 容器，由 cmd/sidecar/main.go 填充。
type Deps struct {
	Auth    localauth.Config
	DataDir string
	Version string
}

func NewRouter(d Deps) nethttp.Handler {
	r := chi.NewRouter()
	// 鉴权必须挂在最外层：挂在 Group 里的话，预检请求（OPTIONS）打到只注册了
	// GET 的路径会先被 chi 判成 405，根本走不到中间件。
	r.Use(localauth.Middleware(d.Auth))

	r.Get("/api/v1/ping", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, map[string]any{"ok": true, "version": d.Version})
	})
	r.Get("/api/v1/agent/ws", wsEcho)

	diag := &diagHandler{dir: d.DataDir}
	r.Post("/api/v1/diag/upload", diag.upload)
	r.Get("/api/v1/diag/media/{name}", diag.media)
	return r
}

func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 5: 写诊断接口**

`server/internal/http/diag.go`：

```go
package http

import (
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// wsEcho 原样回显每一帧。P0 只用它验证 WKWebView / WebView2 能带 token 连上
// sidecar 的 WebSocket；P5 会把这条路由换成真正的 Agent hub。
func wsEcho(w nethttp.ResponseWriter, r *nethttp.Request) {
	// Origin 已由 localauth 校验过，这里不再让 websocket 库按同源规则二次拦截。
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	for {
		typ, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		if err := c.Write(r.Context(), typ, data); err != nil {
			return
		}
	}
}

// diagHandler 给诊断页用：上传一个测试媒体文件，再通过 127.0.0.1 读回来，
// 验证 Range 请求和 mediabunny 的 UrlSource。文件名固定为 sample.<ext>，
// 反复上传只会覆盖，不会堆积。
type diagHandler struct{ dir string }

const diagMaxBytes = 500 << 20

var diagNameRe = regexp.MustCompile(`^sample\.(mp4|mov|webm|mp3|wav|m4a)$`)

func (h *diagHandler) upload(w nethttp.ResponseWriter, r *nethttp.Request) {
	r.Body = nethttp.MaxBytesReader(w, r.Body, diagMaxBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		nethttp.Error(w, "缺少 file 字段或文件过大", nethttp.StatusBadRequest)
		return
	}
	defer file.Close()

	name := "sample" + strings.ToLower(filepath.Ext(header.Filename))
	if !diagNameRe.MatchString(name) {
		nethttp.Error(w, "不支持的文件类型", nethttp.StatusBadRequest)
		return
	}
	dir := filepath.Join(h.dir, "diag")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"name": name})
}

func (h *diagHandler) media(w nethttp.ResponseWriter, r *nethttp.Request) {
	name := chi.URLParam(r, "name")
	if !diagNameRe.MatchString(name) {
		nethttp.NotFound(w, r)
		return
	}
	// ServeFile 自带 Range / 206 / Content-Range 处理。
	nethttp.ServeFile(w, r, filepath.Join(h.dir, "diag", name))
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `cd server && go mod tidy && go test ./internal/http/ -v`
Expected: 8 个测试全部 PASS

- [ ] **Step 7: 提交**

```bash
git add server/go.mod server/go.sum server/internal/http
git commit -m "feat(server): 路由骨架与 P0 诊断接口（ping、WS 回显、媒体 Range）"
```

---

### Task 4: sidecar 启动握手

**Files:**
- Create: `server/cmd/sidecar/main.go`
- Test: `server/cmd/sidecar/main_test.go`

**Interfaces:**
- Consumes: `http.NewRouter`、`http.Deps`（Task 3）；`localauth.Config`（Task 2）
- Produces: 可执行文件 `sidecar`，启动协议如下（Task 5 的 Rust 代码依赖它）：
  1. 参数：`--data-dir <路径>`（必填），`--origins <逗号分隔>`（可选，默认三个白名单 Origin）
  2. 从 stdin 读第一行作为 token（去掉首尾空白，长度至少 32）
  3. 监听 `127.0.0.1:0`，就绪后向 stdout 打印一行 `READY <port>`
  4. 日志一律写 stderr
  5. stdin 读到 EOF（父进程退出或崩溃）时自行退出，退出码 0
  6. 启动失败时向 stderr 打印原因，退出码 1，**不打印 READY**

- [ ] **Step 1: 写失败的测试**

`server/cmd/sidecar/main_test.go`：

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestReadToken(t *testing.T) {
	got, err := readToken(bufio.NewReader(strings.NewReader(testToken + "\n")))
	if err != nil || got != testToken {
		t.Fatalf("got %q, %v", got, err)
	}
}

// Windows 管道写入的行尾是 \r\n，不能把 \r 算进 token。
func TestReadTokenTrimsCRLF(t *testing.T) {
	got, err := readToken(bufio.NewReader(strings.NewReader(testToken + "\r\n")))
	if err != nil || got != testToken {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadTokenRejectsShort(t *testing.T) {
	if _, err := readToken(bufio.NewReader(strings.NewReader("short\n"))); err == nil {
		t.Fatal("过短的 token 应当报错")
	}
}

func TestReadTokenRejectsEOF(t *testing.T) {
	if _, err := readToken(bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Fatal("stdin 直接关闭应当报错")
	}
}

// buildSidecar 把当前包编译成真实二进制，端到端测试握手协议。
func buildSidecar(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sidecar")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// startSidecar 启动进程、写入 token、等到 READY，返回进程、stdin 和端口。
func startSidecar(t *testing.T, bin, dataDir string) (*exec.Cmd, io.WriteCloser, string) {
	t.Helper()
	cmd := exec.Command(bin, "--data-dir", dataDir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	fmt.Fprintf(stdin, "%s\n", testToken)

	ready := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if port, ok := strings.CutPrefix(sc.Text(), "READY "); ok {
				ready <- strings.TrimSpace(port)
				return
			}
		}
		ready <- ""
	}()
	select {
	case port := <-ready:
		if port == "" {
			t.Fatal("sidecar 退出前没有打印 READY")
		}
		return cmd, stdin, port
	case <-time.After(10 * time.Second):
		t.Fatal("等待 READY 超时")
	}
	return nil, nil, ""
}

func TestHandshakeAndPing(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())

	resp, err := http.Get("http://127.0.0.1:" + port + "/api/v1/ping?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping code = %d", resp.StatusCode)
	}

	// 没带 token 必须 401——确认握手读到的 token 真的生效了。
	resp, err = http.Get("http://127.0.0.1:" + port + "/api/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token code = %d, want 401", resp.StatusCode)
	}
}

// Tauri 进程崩溃时 stdin 管道会被关闭；sidecar 必须跟着退出，不能变成孤儿。
func TestExitsWhenStdinCloses(t *testing.T) {
	bin := buildSidecar(t)
	cmd, stdin, _ := startSidecar(t, bin, t.TempDir())
	stdin.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("退出码非 0: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin 关闭 5 秒后 sidecar 仍未退出")
	}
}

// 数据目录建不出来时要明确失败，不能打印 READY 让壳误以为启动成功。
func TestFailsWithoutReadyWhenDataDirUnusable(t *testing.T) {
	bin := buildSidecar(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--data-dir", filepath.Join(blocker, "sub"))
	stdin, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(stdin, "%s\n", testToken)
	stdout, _ := io.ReadAll(out)
	err := cmd.Wait()
	if err == nil {
		t.Fatal("应当以非 0 退出")
	}
	if strings.Contains(string(stdout), "READY") {
		t.Fatalf("失败时不应打印 READY，stdout=%q", stdout)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `cd server && go test ./cmd/sidecar/`
Expected: 编译失败，`undefined: readToken`

- [ ] **Step 3: 写实现**

`server/cmd/sidecar/main.go`：

```go
// sidecar 是桌面端的本地服务进程，由 Tauri 壳拉起。
//
// 启动协议（壳与 sidecar 的唯一契约，改动要两边一起改）：
//  1. 壳通过 stdin 写入一行 token；
//  2. sidecar 监听 127.0.0.1 的随机端口，就绪后向 stdout 打印 "READY <port>"；
//  3. stdin 读到 EOF 说明壳已退出，sidecar 随之退出。
//
// token 走 stdin 而不是参数或环境变量：后两者同机其他进程读得到。
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	nethttp "net/http"
	"os"
	"strings"

	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/localauth"
)

// version 由构建脚本通过 -ldflags "-X main.version=…" 注入。
var version = "dev"

const defaultOrigins = "tauri://localhost,http://tauri.localhost,http://localhost:5173"

func main() {
	log.SetOutput(os.Stderr)
	if err := run(); err != nil {
		log.Printf("sidecar 启动失败: %v", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data-dir", "", "数据目录（必填）")
	origins := flag.String("origins", defaultOrigins, "允许的 Origin，逗号分隔")
	flag.Parse()
	if *dataDir == "" {
		return errors.New("缺少 --data-dir")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}

	stdin := bufio.NewReader(os.Stdin)
	token, err := readToken(stdin)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("监听端口: %w", err)
	}
	handler := apphttp.NewRouter(apphttp.Deps{
		Auth:    localauth.Config{Token: token, AllowedOrigins: splitOrigins(*origins)},
		DataDir: *dataDir,
		Version: version,
	})

	// 壳退出（含崩溃、被强杀）时 stdin 管道关闭，这里读到 EOF 就跟着退出。
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		log.Print("stdin 已关闭，sidecar 退出")
		os.Exit(0)
	}()

	fmt.Printf("READY %d\n", ln.Addr().(*net.TCPAddr).Port)
	return nethttp.Serve(ln, handler)
}

// readToken 读 stdin 第一行。TrimSpace 同时处理 Windows 的 \r\n。
func readToken(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	token := strings.TrimSpace(line)
	if token == "" && err != nil {
		return "", fmt.Errorf("读取 token: %w", err)
	}
	if len(token) < 32 {
		return "", errors.New("token 长度不足 32")
	}
	return token, nil
}

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `cd server && go test ./... -v`
Expected: 全部 PASS（`localauth` 9 个、`http` 8 个、`cmd/sidecar` 7 个）

- [ ] **Step 5: 提交**

```bash
git add server/cmd
git commit -m "feat(server): sidecar 启动握手（stdin token、READY 端口、stdin 关闭即退出）"
```

---

### Task 5: Tauri 壳（sidecar 生命周期、server_info、单实例）

**Files:**
- Create: `scripts/build-sidecar.sh`
- Create: `src-tauri/Cargo.toml`
- Create: `src-tauri/build.rs`
- Create: `src-tauri/tauri.conf.json`
- Create: `src-tauri/capabilities/default.json`
- Create: `src-tauri/src/main.rs`
- Create: `src-tauri/src/lib.rs`
- Create: `src-tauri/icons/*`（生成）
- Create: `web/public/logo.png`（从参考仓复制）

**Interfaces:**
- Consumes: sidecar 启动协议（Task 4）
- Produces:
  - Tauri 命令 `server_info` → 成功返回 `{ "port": number, "token": string }`；sidecar 没起来时返回错误字符串 `本地服务启动失败：<原因>`
  - `scripts/build-sidecar.sh [target-triple]` → 产出 `src-tauri/binaries/sidecar-<triple>[.exe]`，缺省用本机 triple
  - 数据目录：`app_data_dir()`，macOS 为 `~/Library/Application Support/com.miaoju.desktop/`，Windows 为 `%APPDATA%\com.miaoju.desktop\`

- [ ] **Step 1: 写 sidecar 构建脚本**

`scripts/build-sidecar.sh`：

```bash
#!/usr/bin/env bash
# 把 Go sidecar 编译到 src-tauri/binaries/sidecar-<target-triple>[.exe]。
# Tauri 的 externalBin 要求文件名带 target triple 后缀。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET="${1:-$(rustc -vV | sed -n 's/^host: //p')}"
case "$TARGET" in
  aarch64-apple-darwin)   GOOS=darwin  GOARCH=arm64 EXT="" ;;
  x86_64-apple-darwin)    GOOS=darwin  GOARCH=amd64 EXT="" ;;
  x86_64-pc-windows-msvc) GOOS=windows GOARCH=amd64 EXT=".exe" ;;
  *) echo "不支持的目标平台: $TARGET" >&2; exit 1 ;;
esac

VERSION="$(node -p "require('./package.json').version")"
mkdir -p src-tauri/binaries
( cd server && CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "../src-tauri/binaries/sidecar-$TARGET$EXT" ./cmd/sidecar )
echo "已生成 src-tauri/binaries/sidecar-$TARGET$EXT"
```

```bash
chmod +x scripts/build-sidecar.sh && scripts/build-sidecar.sh
```

Expected: 输出 `已生成 src-tauri/binaries/sidecar-aarch64-apple-darwin`（Intel Mac 上是 `x86_64-apple-darwin`）

- [ ] **Step 2: 写 `src-tauri/Cargo.toml` 和 `build.rs`**

`src-tauri/Cargo.toml`：

```toml
[package]
name = "miaoju-desktop"
version = "0.1.0"
edition = "2021"

[lib]
name = "miaoju_desktop_lib"
crate-type = ["staticlib", "cdylib", "rlib"]

[build-dependencies]
tauri-build = { version = "2", features = [] }

[dependencies]
tauri = { version = "2", features = [] }
tauri-plugin-shell = "2"
tauri-plugin-single-instance = "2"
serde = { version = "1", features = ["derive"] }
uuid = { version = "1", features = ["v4"] }
```

`src-tauri/build.rs`：

```rust
fn main() {
    tauri_build::build()
}
```

- [ ] **Step 3: 写 `tauri.conf.json` 和 capabilities**

`src-tauri/tauri.conf.json`：

```json
{
  "$schema": "https://schema.tauri.app/config/2",
  "productName": "喵剧AI",
  "mainBinaryName": "miaoju",
  "version": "0.1.0",
  "identifier": "com.miaoju.desktop",
  "build": {
    "frontendDist": "../web/dist",
    "devUrl": "http://localhost:5173",
    "beforeDevCommand": "pnpm --filter @miaoju/web dev",
    "beforeBuildCommand": "pnpm --filter @miaoju/web build"
  },
  "app": {
    "windows": [
      { "label": "main", "title": "喵剧AI", "width": 1440, "height": 900, "minWidth": 1024, "minHeight": 680 }
    ],
    "security": {
      "csp": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob: http://127.0.0.1:*; media-src 'self' blob: http://127.0.0.1:*; connect-src 'self' ipc: http://ipc.localhost http://127.0.0.1:* ws://127.0.0.1:*"
    }
  },
  "bundle": {
    "active": true,
    "targets": ["dmg"],
    "externalBin": ["binaries/sidecar"],
    "icon": ["icons/32x32.png", "icons/128x128.png", "icons/128x128@2x.png", "icons/icon.icns", "icons/icon.ico"],
    "macOS": { "minimumSystemVersion": "14.0", "signingIdentity": "-" }
  }
}
```

CSP 说明：sidecar 端口每次启动都不同，CSP 写不了具体端口，所以放行 `127.0.0.1:*`；真正的访问控制在 sidecar 的 token 校验上。

`src-tauri/capabilities/default.json`：

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "主窗口的最小权限：只有核心能力和自定义命令 server_info",
  "windows": ["main"],
  "permissions": ["core:default"]
}
```

前端不直接调用 shell 插件（sidecar 由 Rust 侧启动），所以这里不授予任何 `shell:*` 权限。

- [ ] **Step 4: 写失败的 Rust 单测**

`src-tauri/src/lib.rs`（先只放测试和函数签名占位的编译错误来源）：

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready_accepts_port_line() {
        assert_eq!(parse_ready("READY 54321"), Some(54321));
        assert_eq!(parse_ready("READY 54321\r\n"), Some(54321));
    }

    #[test]
    fn parse_ready_ignores_other_lines() {
        assert_eq!(parse_ready("listening..."), None);
        assert_eq!(parse_ready("READY"), None);
        assert_eq!(parse_ready("READY abc"), None);
        assert_eq!(parse_ready("READY 99999"), None); // 超出 u16
    }

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        let b = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b);
    }
}
```

- [ ] **Step 5: 运行测试，确认失败**

Run: `cd src-tauri && cargo test`
Expected: 编译失败，`cannot find function parse_ready`、`cannot find function new_token`

- [ ] **Step 6: 写实现**

`src-tauri/src/lib.rs`（完整文件，测试模块保留在末尾）：

```rust
//! Tauri 薄壳：拉起 Go sidecar、完成 token 握手、把端口和 token 交给前端。
//! 业务逻辑全部在 sidecar 里，这里不放任何业务代码。

use std::sync::Mutex;

use serde::Serialize;
use tauri::{Manager, RunEvent, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

#[derive(Clone, Serialize)]
pub struct ServerInfo {
    port: u16,
    token: String,
}

/// sidecar 的运行状态。`info` 为 Err 时保存启动失败原因，前端据此显示错误页。
struct Sidecar {
    info: Mutex<Result<ServerInfo, String>>,
    child: Mutex<Option<CommandChild>>,
}

#[tauri::command]
fn server_info(state: State<'_, Sidecar>) -> Result<ServerInfo, String> {
    state.info.lock().unwrap().clone()
}

/// 解析 sidecar 的就绪行 "READY <port>"。
pub fn parse_ready(line: &str) -> Option<u16> {
    line.trim().strip_prefix("READY ")?.trim().parse().ok()
}

/// 64 位十六进制随机 token（两个 UUIDv4，来自系统随机源）。
pub fn new_token() -> String {
    format!(
        "{}{}",
        uuid::Uuid::new_v4().simple(),
        uuid::Uuid::new_v4().simple()
    )
}

/// 启动 sidecar 并等到 READY。任何一步失败都返回可读的中文原因。
fn start_sidecar(app: &tauri::AppHandle) -> Result<(ServerInfo, CommandChild), String> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("无法确定数据目录：{e}"))?;
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("无法创建数据目录：{e}"))?;

    let token = new_token();
    let (mut rx, mut child) = app
        .shell()
        .sidecar("sidecar")
        .map_err(|e| format!("找不到 sidecar 程序：{e}"))?
        .args(["--data-dir", &data_dir.to_string_lossy()])
        .spawn()
        .map_err(|e| format!("无法启动 sidecar：{e}"))?;

    // token 只走 stdin：参数和环境变量同机其他进程读得到。
    child
        .write(format!("{token}\n").as_bytes())
        .map_err(|e| format!("无法向 sidecar 传递 token：{e}"))?;

    let mut stderr_tail = String::new();
    let port = tauri::async_runtime::block_on(async {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(line) => {
                    if let Some(port) = parse_ready(&String::from_utf8_lossy(&line)) {
                        return Some(port);
                    }
                }
                CommandEvent::Stderr(line) => {
                    let text = String::from_utf8_lossy(&line);
                    eprintln!("[sidecar] {}", text.trim_end());
                    stderr_tail = text.trim().to_string();
                }
                CommandEvent::Terminated(_) | CommandEvent::Error(_) => return None,
                _ => {}
            }
        }
        None
    });
    let Some(port) = port else {
        return Err(if stderr_tail.is_empty() {
            "sidecar 未就绪即退出".to_string()
        } else {
            stderr_tail
        });
    };

    // 握手完成后继续把 sidecar 的日志转到壳的 stderr。
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            if let CommandEvent::Stderr(line) = event {
                eprintln!("[sidecar] {}", String::from_utf8_lossy(&line).trim_end());
            }
        }
    });

    Ok((ServerInfo { port, token }, child))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // 单实例插件必须最先注册：第二次启动时只把已有窗口带到前台（spec S11）。
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let state = match start_sidecar(app.handle()) {
                Ok((info, child)) => Sidecar {
                    info: Mutex::new(Ok(info)),
                    child: Mutex::new(Some(child)),
                },
                // 启动失败不退出进程：让窗口照常打开，由前端显示失败原因。
                Err(reason) => Sidecar {
                    info: Mutex::new(Err(format!("本地服务启动失败：{reason}"))),
                    child: Mutex::new(None),
                },
            };
            app.manage(state);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![server_info])
        .build(tauri::generate_context!())
        .expect("构建 Tauri 应用失败")
        .run(|app, event| {
            // 正常退出时主动结束 sidecar；崩溃场景由 sidecar 自己靠 stdin EOF 兜底。
            if let RunEvent::Exit = event {
                if let Some(child) = app.state::<Sidecar>().child.lock().unwrap().take() {
                    let _ = child.kill();
                }
            }
        });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ready_accepts_port_line() {
        assert_eq!(parse_ready("READY 54321"), Some(54321));
        assert_eq!(parse_ready("READY 54321\r\n"), Some(54321));
    }

    #[test]
    fn parse_ready_ignores_other_lines() {
        assert_eq!(parse_ready("listening..."), None);
        assert_eq!(parse_ready("READY"), None);
        assert_eq!(parse_ready("READY abc"), None);
        assert_eq!(parse_ready("READY 99999"), None); // 超出 u16
    }

    #[test]
    fn new_token_is_long_and_unique() {
        let a = new_token();
        let b = new_token();
        assert_eq!(a.len(), 64);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b);
    }
}
```

`src-tauri/src/main.rs`：

```rust
// Windows release 构建不弹出控制台窗口。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    miaoju_desktop_lib::run()
}
```

- [ ] **Step 7: 生成图标**

P0 先用参考仓的 80×80 logo 生成图标（会偏糊）；P8 打包时换成 1024×1024 的正式图标。

```bash
mkdir -p web/public
cp /Users/james/github/huabu_studio/apps/web/public/logo.png web/public/logo.png
cd src-tauri && cargo tauri icon ../web/public/logo.png
```

Expected: `src-tauri/icons/` 下生成 `32x32.png`、`128x128.png`、`128x128@2x.png`、`icon.icns`、`icon.ico` 等文件。

- [ ] **Step 8: 运行 Rust 测试，确认通过**

`generate_context!` 在编译期要求 `frontendDist` 目录存在，先建一个空目录：

```bash
mkdir -p web/dist && cd src-tauri && cargo test
```

Expected: `test result: ok. 3 passed`

- [ ] **Step 9: 提交**

```bash
git add scripts/build-sidecar.sh src-tauri/Cargo.toml src-tauri/Cargo.lock src-tauri/build.rs \
  src-tauri/tauri.conf.json src-tauri/capabilities src-tauri/src src-tauri/icons web/public/logo.png
git commit -m "feat(tauri): 薄壳——sidecar 握手、server_info 命令、单实例"
```

---

### Task 6: 前端骨架与诊断页

**Files:**
- Create: `web/package.json`
- Create: `web/index.html`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json`
- Create: `web/src/main.tsx`
- Create: `web/src/shared/server.ts`
- Test: `web/src/shared/server.test.ts`
- Create: `web/src/diag/checks.ts`
- Create: `web/src/diag/DiagPage.tsx`

**Interfaces:**
- Consumes: Tauri 命令 `server_info`（Task 5）；sidecar 路由 `/api/v1/ping`、`/api/v1/agent/ws`、`/api/v1/diag/upload`、`/api/v1/diag/media/{name}`（Task 3）
- Produces（P3 迁移 `shared/api.ts` 时会用到）：
  - `interface ServerInfo { port: number; token: string }`
  - `getServerInfo(): Promise<ServerInfo>` —— 成功后缓存；失败不缓存，下次调用会重试
  - `buildHttpUrl(info: ServerInfo, path: string): string`
  - `buildMediaUrl(info: ServerInfo, path: string): string` —— 追加 `token` 查询参数
  - `buildWsUrl(info: ServerInfo, path: string): string` —— `ws://` 且追加 `token`
  - `authHeaders(info: ServerInfo): { Authorization: string }`

- [ ] **Step 1: 写 `web/package.json` 并安装依赖**

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
    "@tauri-apps/api": "^2",
    "mediabunny": "^1.54.0",
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@types/node": "^22",
    "@types/react": "^18.3.12",
    "@types/react-dom": "^18.3.1",
    "@vitejs/plugin-react": "^4.3.4",
    "typescript": "^5.6.3",
    "vite": "^5.4.10",
    "vitest": "^2.1.3"
  }
}
```

```bash
pnpm install
```

Expected: 安装成功，生成根目录 `pnpm-lock.yaml`。

- [ ] **Step 2: 写构建配置**

`web/vite.config.ts`：

```ts
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { fileURLToPath } from 'node:url';

export default defineConfig({
  plugins: [react()],
  // 打包后由 tauri:// 协议加载，资源必须用相对路径。
  base: './',
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: { port: 5173, strictPort: true },
  test: { environment: 'node' },
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
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] }
  },
  "include": ["src", "vite.config.ts"]
}
```

`web/index.html`：

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>喵剧AI</title>
  </head>
  <body style="margin: 0; background: #000; color: #eee">
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: 写失败的测试**

`web/src/shared/server.test.ts`：

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest';

const invoke = vi.fn();
vi.mock('@tauri-apps/api/core', () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  getServerInfo,
  resetServerInfoForTest,
} from './server';

const info = { port: 54321, token: 'a+b/c=' };

beforeEach(() => {
  invoke.mockReset();
  resetServerInfoForTest();
});

describe('URL 构造', () => {
  it('HTTP 地址指向 127.0.0.1 的 sidecar 端口', () => {
    expect(buildHttpUrl(info, '/api/v1/ping')).toBe('http://127.0.0.1:54321/api/v1/ping');
  });

  it('媒体地址追加经过编码的 token', () => {
    expect(buildMediaUrl(info, '/api/v1/diag/media/sample.mp4')).toBe(
      'http://127.0.0.1:54321/api/v1/diag/media/sample.mp4?token=a%2Bb%2Fc%3D',
    );
  });

  it('路径已有查询参数时用 & 拼接 token', () => {
    expect(buildMediaUrl(info, '/m?w=400')).toBe('http://127.0.0.1:54321/m?w=400&token=a%2Bb%2Fc%3D');
  });

  it('WebSocket 地址用 ws:// 并带 token', () => {
    expect(buildWsUrl(info, '/api/v1/agent/ws')).toBe(
      'ws://127.0.0.1:54321/api/v1/agent/ws?token=a%2Bb%2Fc%3D',
    );
  });

  it('请求头用 Bearer', () => {
    expect(authHeaders(info)).toEqual({ Authorization: 'Bearer a+b/c=' });
  });
});

describe('getServerInfo', () => {
  it('成功后缓存，只调用一次 server_info', async () => {
    invoke.mockResolvedValue(info);
    expect(await getServerInfo()).toEqual(info);
    expect(await getServerInfo()).toEqual(info);
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(invoke).toHaveBeenCalledWith('server_info');
  });

  // sidecar 没起来时 server_info 会 reject；失败不能被缓存，否则用户点“重试”永远失败。
  it('失败时抛出原因且不缓存，下次调用会重试', async () => {
    invoke.mockRejectedValueOnce('本地服务启动失败：找不到 sidecar 程序');
    await expect(getServerInfo()).rejects.toBe('本地服务启动失败：找不到 sidecar 程序');
    invoke.mockResolvedValueOnce(info);
    expect(await getServerInfo()).toEqual(info);
    expect(invoke).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 4: 运行测试，确认失败**

Run: `pnpm test:web`
Expected: FAIL，`Failed to resolve import "./server"`

- [ ] **Step 5: 写实现**

`web/src/shared/server.ts`：

```ts
// 前端与本地 sidecar 之间的唯一入口：端口和 token 只从这里取。
// sidecar 的端口每次启动都不同，所以**任何地方都不要把拼好的地址存起来**
// ——存进画布的话，重启后全部失效（spec S3）。
import { invoke } from '@tauri-apps/api/core';

export interface ServerInfo {
  port: number;
  token: string;
}

let cached: Promise<ServerInfo> | null = null;

/** 取 sidecar 的端口和 token。成功后缓存；失败不缓存，下次调用会重试。 */
export function getServerInfo(): Promise<ServerInfo> {
  if (!cached) {
    const pending = invoke<ServerInfo>('server_info');
    cached = pending;
    pending.catch(() => {
      if (cached === pending) cached = null;
    });
  }
  return cached;
}

export function buildHttpUrl(info: ServerInfo, path: string): string {
  return `http://127.0.0.1:${info.port}${path}`;
}

function withToken(url: string, token: string): string {
  return `${url}${url.includes('?') ? '&' : '?'}token=${encodeURIComponent(token)}`;
}

/** <img>/<video> 和 mediabunny 用的地址：它们带不了 Authorization 头，token 放查询参数。 */
export function buildMediaUrl(info: ServerInfo, path: string): string {
  return withToken(buildHttpUrl(info, path), info.token);
}

/** 浏览器没法给 WebSocket 握手设请求头，token 同样放查询参数。 */
export function buildWsUrl(info: ServerInfo, path: string): string {
  return withToken(`ws://127.0.0.1:${info.port}${path}`, info.token);
}

export function authHeaders(info: ServerInfo): { Authorization: string } {
  return { Authorization: `Bearer ${info.token}` };
}

/** 仅供测试清缓存。 */
export function resetServerInfoForTest(): void {
  cached = null;
}
```

- [ ] **Step 6: 运行测试，确认通过**

Run: `pnpm test:web`
Expected: 7 个测试全部 PASS

- [ ] **Step 7: 写诊断检查**

`web/src/diag/checks.ts`：

```ts
// 诊断检查：验证「壳 → sidecar → WebView」整条链路，以及参考仓画布依赖的
// WebCodecs / mediabunny 能力在当前系统 WebView 里是否可用。
// 每个检查互不依赖、各自捕获异常，一项失败不影响其余项。
import {
  authHeaders,
  buildHttpUrl,
  buildMediaUrl,
  buildWsUrl,
  type ServerInfo,
} from '@/shared/server';

export interface CheckResult {
  name: string;
  ok: boolean;
  detail: string;
  ms: number;
}

async function run(name: string, fn: () => Promise<string>): Promise<CheckResult> {
  const start = performance.now();
  try {
    const detail = await fn();
    return { name, ok: true, detail, ms: Math.round(performance.now() - start) };
  } catch (e) {
    const detail = e instanceof Error ? e.message : String(e);
    return { name, ok: false, detail, ms: Math.round(performance.now() - start) };
  }
}

export async function connectionChecks(info: ServerInfo): Promise<CheckResult[]> {
  const out: CheckResult[] = [];

  out.push(
    await run('HTTP ping（带 token）', async () => {
      const resp = await fetch(buildHttpUrl(info, '/api/v1/ping'), { headers: authHeaders(info) });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const body = (await resp.json()) as { ok: boolean; version: string };
      return `version=${body.version}`;
    }),
  );

  out.push(
    await run('HTTP ping（不带 token 应被拒绝）', async () => {
      const resp = await fetch(buildHttpUrl(info, '/api/v1/ping'));
      if (resp.status !== 401) throw new Error(`期望 401，实际 ${resp.status}`);
      return '401';
    }),
  );

  out.push(
    await run('WebSocket 回显', () => {
      return new Promise<string>((resolve, reject) => {
        const ws = new WebSocket(buildWsUrl(info, '/api/v1/agent/ws'));
        const timer = setTimeout(() => {
          ws.close();
          reject(new Error('5 秒内未收到回显'));
        }, 5000);
        ws.onopen = () => ws.send('你好');
        ws.onmessage = (ev) => {
          clearTimeout(timer);
          ws.close();
          if (ev.data === '你好') resolve('回显一致');
          else reject(new Error(`回显不一致：${String(ev.data)}`));
        };
        ws.onerror = () => {
          clearTimeout(timer);
          reject(new Error('连接失败'));
        };
      });
    }),
  );

  return out;
}

type CodecCtor = { isConfigSupported(config: unknown): Promise<{ supported?: boolean }> };

async function supported(ctor: CodecCtor | undefined, config: unknown): Promise<string> {
  if (!ctor) throw new Error('API 不存在');
  const result = await ctor.isConfigSupported(config);
  if (!result.supported) throw new Error('不支持');
  return '支持';
}

export async function webCodecsChecks(): Promise<CheckResult[]> {
  const g = globalThis as Record<string, unknown>;
  const out: CheckResult[] = [];

  for (const api of ['VideoEncoder', 'VideoDecoder', 'AudioEncoder', 'AudioDecoder']) {
    out.push(
      await run(`API ${api}`, async () => {
        if (typeof g[api] === 'undefined') throw new Error('不存在');
        return '存在';
      }),
    );
  }

  const VE = g.VideoEncoder as CodecCtor | undefined;
  const VD = g.VideoDecoder as CodecCtor | undefined;
  const AE = g.AudioEncoder as CodecCtor | undefined;
  const AD = g.AudioDecoder as CodecCtor | undefined;

  out.push(
    await run('编码 H.264 1080p', () =>
      supported(VE, { codec: 'avc1.640028', width: 1920, height: 1080, bitrate: 8e6, framerate: 30 }),
    ),
  );
  out.push(await run('解码 H.264', () => supported(VD, { codec: 'avc1.640028' })));
  out.push(
    await run('编码 AAC', () =>
      supported(AE, { codec: 'mp4a.40.2', sampleRate: 48000, numberOfChannels: 2, bitrate: 128000 }),
    ),
  );
  out.push(
    await run('解码 AAC', () =>
      supported(AD, { codec: 'mp4a.40.2', sampleRate: 48000, numberOfChannels: 2 }),
    ),
  );
  return out;
}

/** 上传一个测试视频到 sidecar，返回可读回的文件名。 */
export async function uploadSample(info: ServerInfo, file: File): Promise<string> {
  const form = new FormData();
  form.append('file', file);
  const resp = await fetch(buildHttpUrl(info, '/api/v1/diag/upload'), {
    method: 'POST',
    headers: authHeaders(info),
    body: form,
  });
  if (!resp.ok) throw new Error(`上传失败：HTTP ${resp.status} ${await resp.text()}`);
  return ((await resp.json()) as { name: string }).name;
}

/** 用参考仓画布同样的 mediabunny 调用方式，对 sidecar 返回的媒体跑一遍。 */
export async function mediaChecks(info: ServerInfo, name: string): Promise<CheckResult[]> {
  const mb = await import('mediabunny');
  const url = buildMediaUrl(info, `/api/v1/diag/media/${name}`);
  const out: CheckResult[] = [];

  const convert = async (options: Record<string, unknown>, wav = false): Promise<string> => {
    const input = new mb.Input({ formats: mb.ALL_FORMATS, source: new mb.UrlSource(url) });
    const target = new mb.BufferTarget();
    const format = wav ? new mb.WavOutputFormat() : new mb.Mp4OutputFormat({ fastStart: 'in-memory' });
    const output = new mb.Output({ format, target });
    const conversion = await mb.Conversion.init({ input, output, ...options, showWarnings: false });
    if (!conversion.isValid) throw new Error('转换配置无效（有轨道被丢弃）');
    await conversion.execute();
    if (!target.buffer) throw new Error('转码未产出数据');
    return `输出 ${target.buffer.byteLength} 字节`;
  };

  out.push(
    await run('Range 请求返回 206', async () => {
      const resp = await fetch(url, { headers: { Range: 'bytes=0-99' } });
      if (resp.status !== 206) throw new Error(`期望 206，实际 ${resp.status}`);
      return resp.headers.get('Content-Range') ?? '206';
    }),
  );

  let duration = 0;
  out.push(
    await run('抽缩略图条（10 帧）', async () => {
      const input = new mb.Input({ formats: mb.ALL_FORMATS, source: new mb.UrlSource(url) });
      const track = await input.getPrimaryVideoTrack();
      if (!track) throw new Error('没有视频轨');
      duration = await input.computeDuration();
      const sink = new mb.CanvasSink(track, { width: 96, height: 56, fit: 'cover' });
      const stamps = Array.from({ length: 10 }, (_, i) => ((i + 0.5) / 10) * duration);
      let frames = 0;
      for await (const wrapped of sink.canvasesAtTimestamps(stamps)) if (wrapped) frames++;
      if (frames < 10) throw new Error(`只解出 ${frames}/10 帧`);
      return `时长 ${duration.toFixed(2)} 秒`;
    }),
  );

  const end = Math.max(1, Math.min(duration * 0.6, 5));
  out.push(await run('剪辑（截取前段）', () => convert({ trim: { start: 0.3, end } })));
  out.push(
    await run('强制重编码 H.264', () =>
      convert({ video: { forceTranscode: true, codec: 'avc' }, trim: { start: 0, end: Math.min(end, 2) } }),
    ),
  );
  out.push(await run('音频分离：无声 mp4', () => convert({ audio: { discard: true } })));
  out.push(await run('音频分离：WAV', () => convert({ video: { discard: true } }, true)));
  return out;
}

export function formatReport(results: CheckResult[]): string {
  const lines = [`UA: ${navigator.userAgent}`];
  for (const r of results) lines.push(`${r.ok ? 'PASS' : 'FAIL'} ${r.name} — ${r.detail} (${r.ms}ms)`);
  return lines.join('\n');
}
```

- [ ] **Step 8: 写诊断页和入口**

`web/src/diag/DiagPage.tsx`：

```tsx
import { useEffect, useState } from 'react';
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

  const connect = () => {
    setStartError(null);
    getServerInfo().then(setInfo, (e) => setStartError(String(e)));
  };
  useEffect(connect, []);

  // sidecar 没起来：显示原因和重试，而不是白屏。
  if (startError) {
    return (
      <div style={{ padding: 24 }}>
        <h2>本地服务未启动</h2>
        <pre style={{ whiteSpace: 'pre-wrap', color: '#f88' }}>{startError}</pre>
        <button onClick={connect}>重试</button>
      </div>
    );
  }
  if (!info) return <div style={{ padding: 24 }}>正在连接本地服务…</div>;

  const runBasic = async () => {
    setBusy(true);
    try {
      setResults([...(await connectionChecks(info)), ...(await webCodecsChecks())]);
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
    <div style={{ padding: 24, fontFamily: 'system-ui, sans-serif' }}>
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
        <button disabled={results.length === 0} onClick={() => void navigator.clipboard.writeText(report)}>
          复制结果
        </button>
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

`web/src/main.tsx`：

```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { DiagPage } from './diag/DiagPage';

// P0 阶段入口只有诊断页；P3 迁移画布后，诊断页移到「设置 → 环境诊断」。
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <DiagPage />
  </React.StrictMode>,
);
```

- [ ] **Step 9: 类型检查与构建**

Run: `pnpm --dir web build`
Expected: `tsc --noEmit` 无错误，`vite build` 生成 `web/dist/index.html`

- [ ] **Step 10: 在 macOS 上实际运行**

```bash
pnpm dev
```

在打开的窗口里：
1. 点「运行连接与编解码检查」。Expected: 3 项连接检查 + 8 项编解码检查全部 PASS。
2. 选择一个带声音的 mp4。Expected: 「Range 请求返回 206」「抽缩略图条」「剪辑」「强制重编码 H.264」「音频分离：无声 mp4」「音频分离：WAV」6 项全部 PASS。
3. 再启动一次应用（`open` 同一个 app，或再跑一次二进制）。Expected: 不出现第二个窗口，已有窗口被带到前台。
4. 关闭窗口后执行 `pgrep -fl sidecar`。Expected: 没有残留的 sidecar 进程。
5. 重新 `pnpm dev`，然后在另一个终端 `kill -9` Tauri 主进程（`pgrep -f 'target/debug/miaoju'`），5 秒后执行 `pgrep -fl sidecar`。Expected: sidecar 已自行退出。

把「复制结果」的文本贴到 `docs/superpowers/plans/p0-results.md` 的 macOS 小节。

- [ ] **Step 11: 提交**

```bash
git add web pnpm-lock.yaml docs/superpowers/plans/p0-results.md
git commit -m "feat(web): 前端骨架、sidecar 连接封装与环境诊断页"
```

---

### Task 7: Windows 打包与验证

**Files:**
- Create: `scripts/package-windows.sh`
- Modify: `docs/superpowers/plans/p0-results.md`（追加 Windows 小节）

**Interfaces:**
- Consumes: `scripts/build-sidecar.sh`（Task 5）、`web/` 构建（Task 6）
- Produces: `out/miaoju-windows-x64-<version>.zip`，内含 `miaoju.exe` 和 `sidecar.exe`（解压即用，spec E4）

- [ ] **Step 1: 写打包脚本**

`scripts/package-windows.sh`：

```bash
#!/usr/bin/env bash
# 在 macOS 上交叉编译 Windows x64 版本并打成 zip（解压即用，不含安装程序）。
# 依赖：rustup target x86_64-pc-windows-msvc、cargo-xwin。
set -euo pipefail
cd "$(dirname "$0")/.."

TARGET=x86_64-pc-windows-msvc
VERSION="$(node -p "require('./package.json').version")"

scripts/build-sidecar.sh "$TARGET"
cargo tauri build --runner cargo-xwin --target "$TARGET" --no-bundle

REL="src-tauri/target/$TARGET/release"
for f in miaoju.exe sidecar.exe; do
  [ -f "$REL/$f" ] || { echo "缺少构建产物 $REL/$f" >&2; exit 1; }
done

STAGE="out/miaoju-windows-x64-$VERSION"
rm -rf "$STAGE" "$STAGE.zip"
mkdir -p "$STAGE"
cp "$REL/miaoju.exe" "$REL/sidecar.exe" "$STAGE/"
( cd out && zip -r "miaoju-windows-x64-$VERSION.zip" "miaoju-windows-x64-$VERSION" )
echo "已生成 $STAGE.zip"
```

- [ ] **Step 2: 运行打包脚本**

```bash
chmod +x scripts/package-windows.sh && scripts/package-windows.sh
```

Expected: 输出 `已生成 out/miaoju-windows-x64-0.1.0.zip`，`unzip -l` 能看到 `miaoju.exe` 和 `sidecar.exe`。

如果脚本在 `缺少构建产物 …/sidecar.exe` 处失败：说明 `--no-bundle` 没有把 externalBin 复制到 release 目录。在脚本里 `for f in …` 循环之前加一行
`cp "src-tauri/binaries/sidecar-$TARGET.exe" "$REL/sidecar.exe"` 后重跑。

如果 `cargo tauri build` 本身因交叉编译工具链失败：改为在一台 Windows 机器上执行 `scripts/build-sidecar.sh x86_64-pc-windows-msvc`（Git Bash）和 `cargo tauri build --no-bundle`，再手动把两个 exe 打成 zip。把实际采用的方式记到 `p0-results.md`，P8 打包计划据此确定 Windows 构建环境。

- [ ] **Step 3: 在 Windows 10 或 11 机器上验证**

把 zip 拷到 Windows 机器，解压后双击 `miaoju.exe`（SmartScreen 拦截时点「更多信息 → 仍要运行」）。然后重复 Task 6 Step 10 的第 1、2、3 步，外加：

- 关闭窗口后打开任务管理器。Expected: 没有残留的 `sidecar.exe`。
- 在任务管理器里直接结束 `miaoju.exe`，5 秒后查看。Expected: `sidecar.exe` 已自行退出。
- 确认 `%APPDATA%\com.miaoju.desktop\diag\sample.mp4` 存在。

把「复制结果」的文本贴到 `docs/superpowers/plans/p0-results.md` 的 Windows 小节，并写明 Windows 版本号。

- [ ] **Step 4: 提交**

```bash
git add scripts/package-windows.sh docs/superpowers/plans/p0-results.md
git commit -m "build: Windows zip 打包脚本与 P0 验证结果"
```

---

## P0 完成标准

- `pnpm test:server`、`pnpm test:web`、`pnpm test:tauri` 全部通过。
- macOS 和 Windows 上诊断页所有检查项 PASS，结果记录在 `docs/superpowers/plans/p0-results.md`。
- 两个平台上，正常关闭和强杀主进程后都没有残留的 sidecar 进程。
- 重复启动应用只有一个窗口。

任一平台的媒体检查有 FAIL：停下来，把失败项和 UA 报给用户，再决定是调整最低系统版本（spec D8）还是改方案，不要带着失败项进入 P1。
