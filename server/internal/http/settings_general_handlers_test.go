package http

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

type generalResp struct {
	Version            string `json:"version"`
	DataDir            string `json:"dataDir"`
	BaseDomain         string `json:"baseDomain"`
	BaseDomainModified bool   `json:"baseDomainModified"`
	APIKeySet          bool   `json:"apiKeySet"`
	APIKeyMasked       string `json:"apiKeyMasked"`
}

func TestGeneralGetReturnsVersionAndDataDir(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("GET", "/general", nil)
	g := decodeTo[generalResp](t, body)
	if code != 200 || g.Version != "9.9.9" || g.DataDir != e.dataDir || g.BaseDomain != "http://115.191.35.118:3000" || g.APIKeySet || g.BaseDomainModified {
		t.Fatalf("%d %s", code, body)
	}
}

func TestUpstreamResponsesNeverContainPlainKey(t *testing.T) {
	e := newSCEnv(t)
	const key = "sk-abcdef1234567890WXYZ"
	code, body := e.do("PUT", "/upstream", map[string]any{"apiKey": "  " + key + " "})
	if code != 200 || strings.Contains(body, "abcdef1234567890") {
		t.Fatalf("PUT 响应泄露明文或失败: %d %s", code, body)
	}
	g := decodeTo[generalResp](t, body)
	if !g.APIKeySet || g.APIKeyMasked != "sk-••••••••WXYZ" {
		t.Fatalf("掩码不对: %+v", g)
	}
	_, body = e.do("GET", "/general", nil)
	if strings.Contains(body, "abcdef1234567890") {
		t.Fatalf("GET 泄露明文: %s", body)
	}
	if stored, _ := e.settings.Get("upstream_api_key"); stored != key {
		t.Fatalf("库里应存去掉首尾空白的明文，got %q", stored)
	}
}

func TestUpstreamClearKey(t *testing.T) {
	e := newSCEnv(t)
	e.do("PUT", "/upstream", map[string]any{"apiKey": "sk-123456789"})
	code, body := e.do("PUT", "/upstream", map[string]any{"clearApiKey": true})
	if g := decodeTo[generalResp](t, body); code != 200 || g.APIKeySet || g.APIKeyMasked != "" {
		t.Fatalf("%d %s", code, body)
	}
	code, _ = e.do("PUT", "/upstream", map[string]any{"clearApiKey": true, "apiKey": "sk-x"})
	if code != 400 {
		t.Fatalf("同时提交应 400，got %d", code)
	}
}

func TestUpstreamRejectsBadInput(t *testing.T) {
	e := newSCEnv(t)
	for _, body := range []any{
		map[string]any{},
		map[string]any{"baseDomain": "ftp://x"},
		map[string]any{"baseDomain": "not a url"},
		map[string]any{"baseDomain": "http://h/?a=1"},
		map[string]any{"apiKey": ""},
		map[string]any{"apiKey": "sk 有空格"},
		map[string]any{"apiKey": strings.Repeat("a", 513)},
		map[string]any{"upstream_api_key": "sk-x"},
	} {
		if code, resp := e.do("PUT", "/upstream", body); code != 400 {
			t.Errorf("%v → %d %s", body, code, resp)
		}
	}
}

func TestUpstreamBaseDomainGoesThroughEditServiceAndRestores(t *testing.T) {
	e := newSCEnv(t)
	code, body := e.do("PUT", "/upstream", map[string]any{"baseDomain": " https://gw.example.com/ "})
	g := decodeTo[generalResp](t, body)
	if code != 200 || g.BaseDomain != "https://gw.example.com" || !g.BaseDomainModified {
		t.Fatalf("%d %s", code, body)
	}
	code, body = e.do("POST", "/upstream/base-domain/restore-default", nil)
	g = decodeTo[generalResp](t, body)
	if code != 200 || g.BaseDomain != "http://115.191.35.118:3000" || g.BaseDomainModified {
		t.Fatalf("%d %s", code, body)
	}
}

func TestUpstreamSameBaseDomainIsNotAnEdit(t *testing.T) {
	e := newSCEnv(t)
	_, body := e.do("PUT", "/upstream", map[string]any{"baseDomain": "http://115.191.35.118:3000/"})
	if g := decodeTo[generalResp](t, body); g.BaseDomainModified {
		t.Fatal("值没变不应记为用户修改")
	}
}

func TestMaskAPIKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"short":           "••••••••",
		"sk-12345678":     "••••••••",        // 11 位：不足 12 位全部遮住
		"sk-123456789":    "sk-••••••••6789", // 12 位：露后 4 位
		"sk-1234567890":   "sk-••••••••7890",
		"abcdefghijklmno": "••••••••lmno", // 不以 sk- 开头时不输出前缀，也不露前几位
	} {
		if got := maskAPIKey(in); got != want {
			t.Errorf("mask(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDataDirCheck(t *testing.T) {
	e := newSCEnv(t)
	// 返回所选文件夹下的专用子目录；所选文件夹本身可以非空（例如 ~/Documents）。
	picked := filepath.Join(t.TempDir(), "我的 文档")
	os.MkdirAll(picked, 0o755)
	os.WriteFile(filepath.Join(picked, "x.txt"), []byte("x"), 0o600)
	code, body := e.do("POST", "/data-dir/check", map[string]any{"path": picked})
	want, _ := json.Marshal(map[string]string{"path": filepath.Join(picked, datadir.DedicatedSubdir), "state": "empty"})
	if code != 200 || strings.TrimSpace(body) != string(want) {
		t.Fatalf("%d %s", code, body)
	}
	// 已有喵剧数据（含有效 data.db）→ state=existing，前端提供“直接使用该目录（不迁移）”
	had := t.TempDir()
	hadSub := filepath.Join(had, datadir.DedicatedSubdir)
	os.MkdirAll(hadSub, 0o700)
	os.WriteFile(filepath.Join(hadSub, "data.db"), []byte("SQLite format 3\x00rest"), 0o600)
	code, body = e.do("POST", "/data-dir/check", map[string]any{"path": had})
	want, _ = json.Marshal(map[string]string{"path": hadSub, "state": "existing"})
	if code != 200 || strings.TrimSpace(body) != string(want) {
		t.Fatalf("existing: %d %s", code, body)
	}
	// 专用子目录已存在且非空 → 拒绝
	taken := t.TempDir()
	os.MkdirAll(filepath.Join(taken, datadir.DedicatedSubdir), 0o700)
	os.WriteFile(filepath.Join(taken, datadir.DedicatedSubdir, "x.txt"), []byte("x"), 0o600)
	for _, p := range []string{"", "/tmp/a?b", "file:/tmp/x", "relative/dir", taken, filepath.Join(t.TempDir(), "不存在"), e.dataDir, filepath.Join(e.dataDir, "sub")} {
		if code, body := e.do("POST", "/data-dir/check", map[string]any{"path": p}); code != 400 {
			t.Errorf("%q → %d %s", p, code, body)
		}
	}
}
