package seedance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateAssetGroup(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Write([]byte(`{"Id":"group-abc"}`))
	}))
	defer srv.Close()

	c := NewClient(func() string { return srv.URL }, testPaths)
	id, err := c.CreateAssetGroup(context.Background(), "sk-test", "my-group", "desc")
	if err != nil {
		t.Fatalf("CreateAssetGroup: %v", err)
	}
	if id != "group-abc" {
		t.Fatalf("id = %q, want group-abc", id)
	}
	if gotPath != "/asset/CreateAssetGroup" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotKey)
	}
	if gotBody["Name"] != "my-group" || gotBody["Description"] != "desc" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestCreateAsset(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		if r.URL.Path != "/asset/CreateAsset" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"Id":"Asset-xyz"}`))
	}))
	defer srv.Close()

	c := NewClient(func() string { return srv.URL }, testPaths)
	id, err := c.CreateAsset(context.Background(), "sk-test", "group-abc", "https://img/x.png", "char-1", AssetTypeImage)
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	if id != "Asset-xyz" {
		t.Fatalf("id = %q, want Asset-xyz", id)
	}
	if gotBody["GroupId"] != "group-abc" || gotBody["URL"] != "https://img/x.png" ||
		gotBody["Name"] != "char-1" || gotBody["AssetType"] != "Image" {
		t.Errorf("body = %v", gotBody)
	}
}

// AssetType 透传：视频/音频与图片走同一端点，只有这一个字段不同，
// 传错就会被上游按图片规格校验（宽高比/像素数）莫名拒掉。
func TestCreateAssetPassesAssetType(t *testing.T) {
	for _, at := range []string{AssetTypeVideo, AssetTypeAudio} {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			w.Write([]byte(`{"Id":"Asset-xyz"}`))
		}))
		c := NewClient(func() string { return srv.URL }, testPaths)
		if _, err := c.CreateAsset(context.Background(), "sk-test", "group-abc", "https://m/x", "n1", at); err != nil {
			t.Fatalf("CreateAsset(%s): %v", at, err)
		}
		if gotBody["AssetType"] != at {
			t.Errorf("AssetType = %v, want %s", gotBody["AssetType"], at)
		}
		srv.Close()
	}
}

func TestGetAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/asset/GetAsset" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"Id":"Asset-xyz","Status":"Active"}`))
	}))
	defer srv.Close()

	c := NewClient(func() string { return srv.URL }, testPaths)
	st, err := c.GetAsset(context.Background(), "sk-test", "Asset-xyz")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if st != "Active" {
		t.Fatalf("status = %q, want Active", st)
	}
}

func TestNon2xxReturnsErrorWithBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	c := NewClient(func() string { return srv.URL }, testPaths)
	_, err := c.CreateAssetGroup(context.Background(), "sk-bad", "g", "")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !contains(err.Error(), "bad key") {
		t.Errorf("error should include response body, got: %v", err)
	}
}

// 生产实测形态：HTTP 200 但业务失败，Code 藏在 data 里、没有 Id。
// 错误必须携带可供上层分类的 Code（errors.As → *APIError）。
func TestCreateAssetBusinessErrorExposesCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":1,"data":{"Code":"InvalidParameter.AspectRatioTooSmall","Message":"Aspect ratio must be between 0.4 and 2.5.","Data":null},"error":null}`))
	}))
	defer srv.Close()

	c := NewClient(func() string { return srv.URL }, testPaths)
	_, err := c.CreateAsset(context.Background(), "sk-test", "group-abc", "https://img/x.png", "char-1", AssetTypeImage)
	if err == nil {
		t.Fatal("expected error when response has no Id")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "InvalidParameter.AspectRatioTooSmall" {
		t.Fatalf("Code = %q", apiErr.Code)
	}
	if !contains(err.Error(), "Aspect ratio") {
		t.Errorf("error should keep raw detail, got: %v", err)
	}
}

// provider 每次请求现读：域名在 settings 里被改后，无需重启即对下一次调用生效。
func TestBaseURLProviderReadPerRequest(t *testing.T) {
	var hits []string
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id":"g","Status":"Active"}`))
	}
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits = append(hits, "A"); handler(w, r) }))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits = append(hits, "B"); handler(w, r) }))
	defer srvB.Close()

	current := srvA.URL
	c := NewClient(func() string { return current }, testPaths)

	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	current = srvB.URL // 模拟管理端改了域名
	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if len(hits) != 2 || hits[0] != "A" || hits[1] != "B" {
		t.Fatalf("provider 未按每次请求现读: hits=%v", hits)
	}
}

// testPaths 是参考仓直连火山方舟时的三个路径，用来让移植来的用例保持原断言。
func testPaths() Paths {
	return Paths{CreateAssetGroup: "/asset/CreateAssetGroup", CreateAsset: "/asset/CreateAsset", GetAsset: "/asset/GetAsset"}
}

// 网关地址或路径为空：不发任何请求，直接报“合规接口未配置”。
func TestNotConfiguredWhenBaseOrPathEmpty(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	cases := []*Client{
		NewClient(func() string { return "" }, testPaths),
		NewClient(nil, testPaths),
		NewClient(func() string { return srv.URL }, func() Paths { return Paths{} }),
		NewClient(func() string { return srv.URL }, nil),
	}
	for i, c := range cases {
		if _, err := c.GetAsset(context.Background(), "sk", "id"); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("case %d: err = %v, want ErrNotConfigured", i, err)
		}
	}
	if hits != 0 {
		t.Fatalf("未配置时不应发请求，hits=%d", hits)
	}
}

// 路径与地址每次请求现读，并兼容末尾斜杠、缺前导斜杠。
func TestPathsAndBaseReadPerRequest(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Write([]byte(`{"Status":"Active"}`))
	}))
	defer srv.Close()
	p := Paths{CreateAssetGroup: "/g", CreateAsset: "/a", GetAsset: "newapi/seedance/get"}
	c := NewClient(func() string { return srv.URL + "/" }, func() Paths { return p })
	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatal(err)
	}
	p.GetAsset = "/v2/get"
	if _, err := c.GetAsset(context.Background(), "sk", "id"); err != nil {
		t.Fatal(err)
	}
	if len(gotPaths) != 2 || gotPaths[0] != "/newapi/seedance/get" || gotPaths[1] != "/v2/get" {
		t.Fatalf("paths = %v", gotPaths)
	}
}

func TestLoadComplianceConfig(t *testing.T) {
	get := func(v string) func(string) (string, error) {
		return func(k string) (string, error) {
			if k == SettingComplianceConfig {
				return v, nil
			}
			return "", nil
		}
	}
	c, err := LoadComplianceConfig(get(""))
	if err != nil || c.Complete() || c.EffectiveVersion() != 1 {
		t.Fatalf("空配置: %+v %v", c, err)
	}
	c, err = LoadComplianceConfig(get(`{"createAssetGroupPath":"/g","createAssetPath":"/a","getAssetPath":"/s","version":3}`))
	if err != nil || !c.Complete() || c.EffectiveVersion() != 3 || c.CreateAsset != "/a" {
		t.Fatalf("完整配置: %+v %v", c, err)
	}
	c, err = LoadComplianceConfig(get(`{"createAssetGroupPath":"/g","createAssetPath":" ","getAssetPath":"/s"}`))
	if err != nil || c.Complete() {
		t.Fatalf("空白路径应视为未配置: %+v %v", c, err)
	}
	if _, err := LoadComplianceConfig(get(`{broken`)); err == nil {
		t.Fatal("损坏的 JSON 应报错")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
