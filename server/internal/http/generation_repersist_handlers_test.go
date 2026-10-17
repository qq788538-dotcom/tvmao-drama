package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

type fakeRepersistGen struct {
	res *generation.RepersistResult
	err error
}

func (f fakeRepersistGen) RepersistTask(_ context.Context, userID, taskID int64) (*generation.RepersistResult, error) {
	if userID != 1 || taskID != 7 {
		return nil, generation.ErrNotFound
	}
	return f.res, f.err
}

type fakeRepersistCanvas struct {
	applied  int
	rewrites [][2]string
}

func (f *fakeRepersistCanvas) ApplyRepersist(context.Context, json.RawMessage, []generation.RepersistReplacement) error {
	f.applied++
	return nil
}

func (f *fakeRepersistCanvas) RewriteMediaRef(_ context.Context, _ int64, oldURL, newURL string) error {
	f.rewrites = append(f.rewrites, [2]string{oldURL, newURL})
	return nil
}

type prefixSigner struct{}

func (prefixSigner) Sign(s string) string { return "signed:" + s }

func doRepersist(t *testing.T, h *GenerationRepersistHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	registerRepersistRoute(r, h)
	req := httptest.NewRequest(nethttp.MethodPost, "/api/v1/tasks/"+id+"/repersist", nil)
	req = req.WithContext(withUserID(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRepersistHandlerAppliesToCanvas(t *testing.T) {
	canvas := &fakeRepersistCanvas{}
	h := NewGenerationRepersistHandler(fakeRepersistGen{res: &generation.RepersistResult{
		Sink:         json.RawMessage(`{"kind":"canvas_node","projectId":1,"nodeId":"n"}`),
		OutputURLs:   []string{"media://k"},
		AssetIDs:     []int64{5},
		Replacements: []generation.RepersistReplacement{{Index: 0, OldURL: "https://x/a.png", NewURL: "media://k", AssetID: 5}},
	}}, canvas, prefixSigner{})
	rec := doRepersist(t, h, "7")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"signed:media://k"`) {
		t.Fatalf("返回的 outputUrls 应已签名: %s", rec.Body.String())
	}
	if canvas.applied != 1 || len(canvas.rewrites) != 1 || canvas.rewrites[0][1] != "media://k" {
		t.Fatalf("应回写源节点并尽力改写其他副本, applied=%d rewrites=%v", canvas.applied, canvas.rewrites)
	}
}

func TestRepersistHandlerErrorMapping(t *testing.T) {
	cases := []struct {
		id   string
		err  error
		want int
	}{
		{"8", nil, nethttp.StatusNotFound},
		{"7", generation.ErrNothingToRepersist, nethttp.StatusConflict},
		{"7", errors.New("fetch 404"), nethttp.StatusBadGateway},
		{"abc", nil, nethttp.StatusBadRequest},
	}
	for _, c := range cases {
		h := NewGenerationRepersistHandler(fakeRepersistGen{err: c.err}, &fakeRepersistCanvas{}, nil)
		if rec := doRepersist(t, h, c.id); rec.Code != c.want {
			t.Fatalf("id=%s err=%v got %d want %d", c.id, c.err, rec.Code, c.want)
		}
	}
}
