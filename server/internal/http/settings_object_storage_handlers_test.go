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
