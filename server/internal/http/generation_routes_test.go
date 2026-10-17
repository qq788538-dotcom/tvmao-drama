package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// fakeSigner：media://k → http://127.0.0.1:9/media/k，Canonicalize 反之。
type fakeSigner struct{}

const fakeLocal = "http://127.0.0.1:9/media/"

func (fakeSigner) Sign(s string) string {
	if strings.HasPrefix(s, "media://") {
		return fakeLocal + strings.TrimPrefix(s, "media://")
	}
	return s
}

func (f fakeSigner) SignJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(string(raw), "media://", fakeLocal))
}

func (fakeSigner) Canonicalize(s string) string {
	if strings.HasPrefix(s, fakeLocal) {
		return "media://" + strings.TrimPrefix(s, fakeLocal)
	}
	return s
}

type captureGenClient struct{ got generation.SubmitInput }

func (c *captureGenClient) Submit(_ context.Context, in generation.SubmitInput) (*generation.SubmitResult, error) {
	c.got = in
	return &generation.SubmitResult{TaskID: 11, Status: "queued"}, nil
}
func (c *captureGenClient) Get(context.Context, int64, int64) (*generation.Task, error) {
	return &generation.Task{Status: "succeeded", Output: json.RawMessage(`{"outputUrls":["media://k"]}`)}, nil
}
func (c *captureGenClient) LatestByCanvasNode(context.Context, int64, int64, string) (*generation.Task, error) {
	return nil, generation.ErrNotFound
}

func serve(t *testing.T, routes GenerationRoutes, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	registerGenerationRoutes(r, routes)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// 签名 URL 带本次启动的端口和 token：入库前必须还原成 media://，否则排队任务跨重启后取不到参考图。
func TestCanvasTaskSubmitCanonicalizesSignedURLs(t *testing.T) {
	gen := &captureGenClient{}
	routes := GenerationRoutes{CanvasGen: NewCanvasGenHandler(canvasgen.NewService(gen, nil), fakeSigner{})}
	rec := serve(t, routes, nethttp.MethodPost, "/api/v1/canvas-tasks",
		`{"projectId":1,"nodeId":"n1","modelId":"m","inputs":{"prompt":"p","images":["`+fakeLocal+`u1/a"]}}`)
	if rec.Code != nethttp.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	imgs := gen.got.Inputs["images"].([]any)
	if imgs[0] != "media://u1/a" {
		t.Fatalf("入库前应还原为 media://, got %v", imgs[0])
	}
	if gen.got.UserID != 1 {
		t.Fatalf("userID 应取自上下文, got %d", gen.got.UserID)
	}
}

func TestCanvasTaskStatusSignsOutputs(t *testing.T) {
	routes := GenerationRoutes{CanvasGen: NewCanvasGenHandler(canvasgen.NewService(&captureGenClient{}, nil), fakeSigner{})}
	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/canvas-tasks/11", "")
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), fakeLocal+"k") {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTasksListAndDelete(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, output, output_kind)
		VALUES (1, 'm', 'succeeded', '{"inputs":{"images":["media://in"]}}', '{"outputUrls":["media://out"]}', 'image')`)
	if err != nil {
		t.Fatal(err)
	}
	doneID, _ := res.LastInsertId()
	res, _ = conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input) VALUES (1, 'm', 'running', '{}')`)
	runningID, _ := res.LastInsertId()
	svc := generation.NewService(generation.NewSQLiteRepository(conn), nil, nil, nil, nil, nil, nil, nil, nil, nil, conn)
	routes := GenerationRoutes{Generation: NewGenerationHandler(svc, fakeSigner{})}

	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/tasks?kind=image", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fakeLocal+"out") || !strings.Contains(rec.Body.String(), fakeLocal+"in") {
		t.Fatalf("input/output 里的媒体引用都应签名: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("kind=image 只应命中 1 条: %s", rec.Body.String())
	}

	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/"+itoa(runningID), ""); rec.Code != nethttp.StatusConflict {
		t.Fatalf("进行中的任务删除应 409, got %d", rec.Code)
	}
	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/"+itoa(doneID), ""); rec.Code != nethttp.StatusNoContent {
		t.Fatalf("终态任务删除应 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, routes, nethttp.MethodDelete, "/api/v1/tasks/999", ""); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("不存在应 404, got %d", rec.Code)
	}
}

func TestModelsListHidesNotesAndEstimatesAreZero(t *testing.T) {
	conn := dbtest.Open(t)
	repo := models.NewSQLiteRepository(conn)
	if _, err := repo.Create(models.ModelConfig{Type: "image", Name: "n", ModelID: "m1", BasePath: "/p", Enabled: true,
		AdminNote: "内部备注", PriceNote: "100积分/张", RequestTemplate: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	routes := GenerationRoutes{Models: NewRuntimeModelHandler(repo)}
	rec := serve(t, routes, nethttp.MethodGet, "/api/v1/models", "")
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), `"modelId":"m1"`) {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "内部备注") || strings.Contains(rec.Body.String(), "100积分") {
		t.Fatalf("不得返回备注与价格说明: %s", rec.Body.String())
	}
	if rec := serve(t, routes, nethttp.MethodPost, "/api/v1/models/estimate", `{"modelId":"m1","inputs":{}}`); !strings.Contains(rec.Body.String(), `"credits":0`) {
		t.Fatalf("估价恒为 0: %s", rec.Body.String())
	}
	rec = serve(t, routes, nethttp.MethodPost, "/api/v1/models/estimate-batch", `{"items":[{"modelId":"m1"},{"modelId":"x"}]}`)
	if !strings.Contains(rec.Body.String(), `"total":0`) || !strings.Contains(rec.Body.String(), `"items":[0,0]`) {
		t.Fatalf("批量估价恒为 0: %s", rec.Body.String())
	}
}

func TestRoutesSkipNilHandlers(t *testing.T) {
	if rec := serve(t, GenerationRoutes{}, nethttp.MethodGet, "/api/v1/models", ""); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("未装配的路由不应注册, got %d", rec.Code)
	}
}
