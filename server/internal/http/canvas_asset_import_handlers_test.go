package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// fakeCanvasAssetRefStore 是 canvasAssetRefStore 的手写替身。
// keys 里有的 object_key 视为「调用者名下存在 canonical 行」；
// existing 里有的视为「目标项目已有引用行」。
type fakeCanvasAssetRefStore struct {
	owner    int64
	ownerErr error
	keys     map[string]bool
	existing map[string]bool
}

func (f *fakeCanvasAssetRefStore) ProjectOwner(int64) (int64, error) {
	if f.ownerErr != nil {
		return 0, f.ownerErr
	}
	return f.owner, nil
}

func (f *fakeCanvasAssetRefStore) CanonicalByKey(_ int64, key string) (string, string, bool, error) {
	if !f.keys[key] {
		return "", "", false, nil
	}
	return "https://store.example/" + key, "image/png", true, nil
}

func (f *fakeCanvasAssetRefStore) ProjectHasKey(_ int64, key string) (bool, error) {
	return f.existing[key], nil
}

type fakeRefInserter struct {
	records []assets.AssetRecord
}

func (f *fakeRefInserter) Insert(rec assets.AssetRecord) (int64, error) {
	f.records = append(f.records, rec)
	return int64(len(f.records)), nil
}

func doImportRefs(t *testing.T, h *CanvasAssetImportHandler, userID, projectID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/projects/1/assets/import-refs", bytes.NewReader(raw))
	ctx := withUserID(req.Context(), userID)
	ctx = withProjectID(ctx, projectID)
	rec := httptest.NewRecorder()
	h.ImportRefs(rec, req.WithContext(ctx))
	return rec
}

func TestImportRefs_InsertsReferenceRows(t *testing.T) {
	store := &fakeCanvasAssetRefStore{
		owner:    7,
		keys:     map[string]bool{"u7/a.png": true, "u7/b.mp4": true},
		existing: map[string]bool{},
	}
	ins := &fakeRefInserter{}
	h := NewCanvasAssetImportHandler(store, ins)

	// 重复 key 去重；空白 key 忽略；未知 key 落 missing。
	rec := doImportRefs(t, h, 7, 42, map[string]any{
		"objectKeys": []string{"u7/a.png", "u7/a.png", " ", "u7/b.mp4", "gone/x.png"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var res importAssetRefsResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 {
		t.Fatalf("imported=%d want 2", res.Imported)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "gone/x.png" {
		t.Fatalf("missing=%v", res.Missing)
	}
	if len(ins.records) != 2 {
		t.Fatalf("inserted %d rows", len(ins.records))
	}
	got := ins.records[0]
	if got.UserID != 7 || got.ProjectID != 42 || got.SourceKind != "canvas_ref" || got.SizeBytes != 0 {
		t.Fatalf("bad record: %+v", got)
	}
	if got.URL == "" || got.MimeType == "" {
		t.Fatalf("引用行必须抄 canonical 的 url/mime: %+v", got)
	}
}

func TestImportRefs_IdempotentWhenProjectAlreadyHasKey(t *testing.T) {
	store := &fakeCanvasAssetRefStore{
		owner:    7,
		keys:     map[string]bool{"u7/a.png": true},
		existing: map[string]bool{"u7/a.png": true},
	}
	ins := &fakeRefInserter{}
	h := NewCanvasAssetImportHandler(store, ins)

	rec := doImportRefs(t, h, 7, 42, map[string]any{"objectKeys": []string{"u7/a.png"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var res importAssetRefsResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	// 已有引用=目标已达成，算 imported，但不再插行。
	if res.Imported != 1 || len(ins.records) != 0 {
		t.Fatalf("imported=%d rows=%d", res.Imported, len(ins.records))
	}
}

func TestImportRefs_RejectsForeignProject(t *testing.T) {
	store := &fakeCanvasAssetRefStore{owner: 99, keys: map[string]bool{"k": true}}
	h := NewCanvasAssetImportHandler(store, &fakeRefInserter{})
	rec := doImportRefs(t, h, 7, 42, map[string]any{"objectKeys": []string{"k"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
}

func TestImportRefs_ProjectNotFound(t *testing.T) {
	store := &fakeCanvasAssetRefStore{ownerErr: sql.ErrNoRows}
	h := NewCanvasAssetImportHandler(store, &fakeRefInserter{})
	rec := doImportRefs(t, h, 7, 42, map[string]any{"objectKeys": []string{"k"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rec.Code)
	}
}

func TestImportRefs_ValidatesPayload(t *testing.T) {
	store := &fakeCanvasAssetRefStore{owner: 7}
	h := NewCanvasAssetImportHandler(store, &fakeRefInserter{})

	if rec := doImportRefs(t, h, 7, 42, map[string]any{"objectKeys": []string{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty keys: code=%d want 400", rec.Code)
	}
	tooMany := make([]string, maxImportRefKeys+1)
	for i := range tooMany {
		tooMany[i] = "k" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i/100))
	}
	if rec := doImportRefs(t, h, 7, 42, map[string]any{"objectKeys": tooMany}); rec.Code != http.StatusBadRequest {
		t.Fatalf("too many keys: code=%d want 400", rec.Code)
	}
}
