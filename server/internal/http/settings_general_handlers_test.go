package http

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for in, want := range map[string]string{"": "", "short": "••••••••", "sk-12345678": "sk-••••••••5678"} {
		if got := maskAPIKey(in); got != want {
			t.Errorf("mask(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDataDirCheck(t *testing.T) {
	e := newSCEnv(t)
	target := filepath.Join(t.TempDir(), "新数据目录")
	code, body := e.do("POST", "/data-dir/check", map[string]any{"path": target})
	if code != 200 || !strings.Contains(body, "新数据目录") {
		t.Fatalf("%d %s", code, body)
	}
	nonEmpty := t.TempDir()
	os.WriteFile(filepath.Join(nonEmpty, "x.txt"), []byte("x"), 0o600)
	for _, p := range []string{"/tmp/a?b", "file:/tmp/x", "relative/dir", nonEmpty, e.dataDir, filepath.Join(e.dataDir, "sub")} {
		if code, body := e.do("POST", "/data-dir/check", map[string]any{"path": p}); code != 400 {
			t.Errorf("%q → %d %s", p, code, body)
		}
	}
}
