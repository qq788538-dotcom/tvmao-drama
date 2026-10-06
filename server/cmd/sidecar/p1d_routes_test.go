package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func p1dGet(t *testing.T, port, path string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+port+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%s: 不是 JSON: %s", path, raw)
	}
	return resp.StatusCode, body
}

// 新装的应用：P1d 的用户端接口全部可用，且“未配置”状态如实反映。
func TestP1dRoutesOnFreshInstall(t *testing.T) {
	bin := buildSidecar(t)
	_, _, port := startSidecar(t, bin, t.TempDir())

	for path, keys := range map[string][]string{
		"/api/v1/canvas/postprocess-config": {"subtitleRemoval", "upscaleModels", "whiteModelModel"},
		"/api/v1/canvas/gen-image-presets":  {"models", "presets"},
		"/api/v1/canvas/multi-angle":        {"models", "presets"},
		"/api/v1/canvas/light-control":      {"models", "presets"},
		"/api/v1/canvas/ai-characters":      {"items", "hasMore", "imageConfigured"},
		"/api/v1/seedance/config":           {"configured"},
		"/api/v1/director/library":          {"categories"},
		"/api/v1/director/models":           {"items"},
	} {
		code, body := p1dGet(t, port, path)
		if code != http.StatusOK {
			t.Fatalf("%s: code=%d body=%v", path, code, body)
		}
		for _, k := range keys {
			if _, ok := body[k]; !ok {
				t.Fatalf("%s: 缺字段 %s: %v", path, k, body)
			}
		}
	}
	if _, body := p1dGet(t, port, "/api/v1/seedance/config"); body["configured"] != false {
		t.Fatalf("新装应为未配置: %v", body)
	}
	if _, body := p1dGet(t, port, "/api/v1/canvas/ai-characters"); body["imageConfigured"] != false {
		t.Fatalf("新装形象图地址应为未配置: %v", body)
	}

	// 状态查询不依赖合规配置：未配置时也必须 200（前端每次打开画布都会批量查角标）。
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/api/v1/seedance/status", strings.NewReader(`{"urls":[]}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seedance/status code=%d", resp.StatusCode)
	}
}
