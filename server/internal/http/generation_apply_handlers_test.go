package http

import (
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
)

type fakeApplyAssets struct {
	slot     *assets.AssetRecord
	inserted []assets.AssetRecord
}

func (f *fakeApplyAssets) FindByTaskSlot(int64, int) (*assets.AssetRecord, error) { return f.slot, nil }
func (f *fakeApplyAssets) ListForTask(int64) ([]assets.AssetRecord, error)        { return nil, nil }
func (f *fakeApplyAssets) Insert(rec assets.AssetRecord) (int64, error) {
	f.inserted = append(f.inserted, rec)
	return 99, nil
}

func TestApplyToCanvasInsertsReferenceRow(t *testing.T) {
	conn := dbtest.Open(t)
	res, _ := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	pid, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output) VALUES (1, 'm', 'succeeded', '{}', '{"outputUrls":["media://k"],"assetIds":[5]}')`)
	tid, _ := res.LastInsertId()
	fa := &fakeApplyAssets{slot: &assets.AssetRecord{ID: 5, ObjectKey: "k", URL: "media://k", MimeType: "video/mp4"}}
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Apply: NewGenerationApplyHandler(svc, fa, fakeSigner{}, conn)}

	rec := serve(t, routes, nethttp.MethodPost, "/api/v1/tasks/"+itoa(tid)+"/apply-to-canvas", `{"projectId":`+itoa(pid)+`,"outputIndex":0}`)
	if rec.Code != nethttp.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"kind":"video"`) || !strings.Contains(rec.Body.String(), fakeLocal+"k") {
		t.Fatalf("响应应带 kind 与签名 url: %s", rec.Body.String())
	}
	if len(fa.inserted) != 1 || fa.inserted[0].SourceKind != "generation_ref" || fa.inserted[0].SizeBytes != 0 || fa.inserted[0].SourceTaskID != 0 {
		t.Fatalf("应插一条 generation_ref 引用行（不带 source_task_id、不计配额）, got %+v", fa.inserted)
	}
}

func TestApplyToCanvasRejectsUnknownProject(t *testing.T) {
	conn := dbtest.Open(t)
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Apply: NewGenerationApplyHandler(svc, &fakeApplyAssets{}, nil, conn)}
	if rec := serve(t, routes, nethttp.MethodPost, "/api/v1/tasks/1/apply-to-canvas", `{"projectId":12345,"outputIndex":0}`); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}
