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
