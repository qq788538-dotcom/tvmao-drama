package http

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	nethttp "net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

type repersistGen interface {
	RepersistTask(ctx context.Context, userID, taskID int64) (*generation.RepersistResult, error)
}

type repersistCanvas interface {
	ApplyRepersist(ctx context.Context, sink json.RawMessage, reps []generation.RepersistReplacement) error
	RewriteMediaRef(ctx context.Context, userID int64, oldURL, newURL string) error
}

// repersistSigner 是 *MediaSigner 的最小子集（只用 Sign）。
type repersistSigner interface {
	Sign(s string) string
}

// GenerationRepersistHandler：节点「未保存到本地」的重试按钮。
type GenerationRepersistHandler struct {
	gen    repersistGen
	canvas repersistCanvas
	signer repersistSigner
}

func NewGenerationRepersistHandler(gen repersistGen, canvas repersistCanvas, signer repersistSigner) *GenerationRepersistHandler {
	return &GenerationRepersistHandler{gen: gen, canvas: canvas, signer: signer}
}

func registerRepersistRoute(r chi.Router, h *GenerationRepersistHandler) {
	if h == nil {
		return
	}
	r.Post("/api/v1/tasks/{taskID}/repersist", h.Repersist)
}

// Repersist POST /api/v1/tasks/{taskID}/repersist → {outputUrls(已签名), assetIds}
func (h *GenerationRepersistHandler) Repersist(w nethttp.ResponseWriter, r *nethttp.Request) {
	userID := userIDFromContext(r.Context())
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil || taskID <= 0 {
		writeError(w, nethttp.StatusBadRequest, "invalid task id")
		return
	}
	res, err := h.gen.RepersistTask(r.Context(), userID, taskID)
	if err != nil {
		switch {
		case errors.Is(err, generation.ErrNotFound):
			writeError(w, nethttp.StatusNotFound, "task not found")
		case errors.Is(err, generation.ErrNothingToRepersist):
			writeError(w, nethttp.StatusConflict, err.Error())
		default:
			writeError(w, nethttp.StatusBadGateway, "保存到本地失败："+err.Error())
		}
		return
	}
	if h.canvas != nil {
		// 源节点按结构回写（必须成功，否则节点一直显示未保存）；其他画布里的副本尽力按字节改写。
		if err := h.canvas.ApplyRepersist(r.Context(), res.Sink, res.Replacements); err != nil {
			writeError(w, nethttp.StatusInternalServerError, err.Error())
			return
		}
		for _, rep := range res.Replacements {
			if err := h.canvas.RewriteMediaRef(r.Context(), userID, rep.OldURL, rep.NewURL); err != nil {
				log.Printf("repersist: task=%d 改写其他画布副本失败 old=%s: %v", taskID, rep.OldURL, err)
			}
		}
	}
	urls := make([]string, len(res.OutputURLs))
	for i, u := range res.OutputURLs {
		urls[i] = u
		if h.signer != nil {
			urls[i] = h.signer.Sign(u)
		}
	}
	writeJSON(w, nethttp.StatusOK, map[string]any{"outputUrls": urls, "assetIds": res.AssetIDs})
}
