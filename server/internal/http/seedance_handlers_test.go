package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/library"
	"github.com/james/huabu_studio/apps/api/internal/seedance"
)

type seedanceFakeExtractor struct{}

func (seedanceFakeExtractor) ExtractKey(u string) (string, bool) {
	const p = "media://"
	if strings.HasPrefix(u, p) && len(u) > len(p) {
		return u[len(p):], true
	}
	return "", false
}

type seedanceFakeClient struct {
	configured bool
	calls      int
}

func (c *seedanceFakeClient) Configured() bool { return c.configured }
func (c *seedanceFakeClient) Register(context.Context, string, string) (string, error) {
	c.calls++
	return "asset-new", nil
}

type seedanceFakeSigner func(string) string

func (f seedanceFakeSigner) Sign(s string) string { return f(s) }

type seedanceFakePublic map[string]string

func (p seedanceFakePublic) PublicAssetIDs(keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if id, ok := p[k]; ok {
			out[k] = id
		}
	}
	return out, nil
}

func seedancePublicSigner(s string) string {
	return "https://pub.example/" + strings.TrimPrefix(s, "media://")
}

func newSeedanceTestHandler(t *testing.T, configured bool, signer seedanceFakeSigner) (*SeedanceHandler, *library.SeedanceService, *sql.DB, *seedanceFakeClient) {
	t.Helper()
	conn := dbtest.Open(t)
	// library_items 由 P1b 的迁移 0002 建；本任务的工作区里可能还没有，归属查询需要这张表存在。
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS library_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, object_key TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	client := &seedanceFakeClient{configured: configured}
	svc := library.NewSeedanceService(library.NewSQLiteSeedanceAssetsRepository(conn), client, signer)
	return NewSeedanceHandler(svc, seedanceFakeExtractor{}, conn), svc, conn, client
}

func seedanceInsertAsset(t *testing.T, conn *sql.DB, userID int64, key string) {
	t.Helper()
	if _, err := conn.Exec(`INSERT INTO assets (user_id, object_key, source_kind, url, size_bytes, mime_type)
		VALUES (?, ?, 'upload', ?, 1, 'image/png')`, userID, key, "media://"+key); err != nil {
		t.Fatal(err)
	}
}

func seedanceDo(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/seedance/x", strings.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestSeedanceVerifyUnconfigured503(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, false, seedancePublicSigner)
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"image"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "合规校验未启用") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if client.calls != 0 {
		t.Fatal("未配置不应调上游")
	}
}

func TestSeedanceVerifyNotOwned403(t *testing.T) {
	h, _, _, _ := newSeedanceTestHandler(t, true, seedancePublicSigner)
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u9/other.png","kind":"image"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
}

func TestSeedanceVerifyUnresolvable400(t *testing.T) {
	h, _, _, _ := newSeedanceTestHandler(t, true, seedancePublicSigner)
	for _, body := range []string{`{"url":"https://elsewhere/x.png"}`, `{"url":"media://u1/a.png","kind":"pdf"}`, `{bad`} {
		if rec := seedanceDo(h.Verify, http.MethodPost, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: code=%d", body, rec.Code)
		}
	}
}

func TestSeedanceVerifyOwnedSubmits202(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, true, seedancePublicSigner)
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"video"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var got library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" || got.AssetType != seedance.AssetTypeVideo || client.calls != 1 {
		t.Fatalf("got %+v calls=%d", got, client.calls)
	}
}

func TestSeedanceVerifyLocalMediaRejected(t *testing.T) {
	h, _, conn, client := newSeedanceTestHandler(t, true, func(s string) string {
		return "http://127.0.0.1:5555/media/" + strings.TrimPrefix(s, "media://") + "?token=t"
	})
	seedanceInsertAsset(t, conn, 1, "u1/a.png")
	rec := seedanceDo(h.Verify, http.MethodPost, `{"url":"media://u1/a.png","kind":"image"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "对象存储") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	if client.calls != 0 {
		t.Fatal("本机地址不应发往上游")
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM seedance_assets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("不应留下校验记录: n=%d err=%v", n, err)
	}
}

func TestSeedanceStatusKeyedByURL(t *testing.T) {
	h, _, conn, _ := newSeedanceTestHandler(t, true, seedancePublicSigner)
	if _, err := conn.Exec(`INSERT INTO seedance_assets (object_key, user_id, asset_id, version, status, asset_type)
		VALUES ('u1/a.png', 1, 'asset-9', 1, 'active', 'Image')`); err != nil {
		t.Fatal(err)
	}
	rec := seedanceDo(h.Status, http.MethodPost, `{"urls":["media://u1/a.png","media://u1/never.png","https://x/y.png"]}`)
	var got map[string]library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["media://u1/a.png"].Status != "active" || *got["media://u1/a.png"].AssetID != "asset-9" {
		t.Fatalf("got %+v", got)
	}
}

// AI 角色图是平台预置、已过校验的公共资产：没有用户记录也应显示“已通过”。
func TestSeedanceStatusPublicFallback(t *testing.T) {
	h, svc, _, _ := newSeedanceTestHandler(t, true, seedancePublicSigner)
	svc.SetPublicAssets(seedanceFakePublic{"tapnow/sd-assets/asset-1.jpg": "asset-1"})
	rec := seedanceDo(h.Status, http.MethodPost, `{"urls":["media://tapnow/sd-assets/asset-1.jpg"]}`)
	var got map[string]library.SeedanceAsset
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	a := got["media://tapnow/sd-assets/asset-1.jpg"]
	if a.Status != "active" || a.AssetID == nil || *a.AssetID != "asset-1" {
		t.Fatalf("got %+v", got)
	}
}

func TestSeedanceConfigReportsConfigured(t *testing.T) {
	for _, c := range []struct {
		on   bool
		want bool
	}{{false, false}, {true, true}} {
		h, _, _, _ := newSeedanceTestHandler(t, c.on, seedancePublicSigner)
		rec := seedanceDo(h.Config, http.MethodGet, "")
		var body struct {
			Configured bool `json:"configured"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("code=%d err=%v", rec.Code, err)
		}
		if body.Configured != c.want {
			t.Fatalf("configured=%v want %v", body.Configured, c.want)
		}
	}
}
