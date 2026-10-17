package http

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/events"
)

const sseHeartbeat = 25 * time.Second

// ProjectEvents 是 GET /api/v1/projects/{projectID}/events：推送 CLI 对该项目画布的写入。
// EventSource 带不了请求头，token 走 ?token=（localauth 已支持）。
func ProjectEvents(hub *events.Hub) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		fl, ok := w.(nethttp.Flusher)
		if !ok {
			writeError(w, nethttp.StatusInternalServerError, "streaming unsupported")
			return
		}
		ch, cancel := hub.Subscribe(projectIDFromContext(r.Context()))
		defer cancel()

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		w.WriteHeader(nethttp.StatusOK)
		_, _ = fmt.Fprint(w, ": ok\n\n")
		fl.Flush()

		tick := time.NewTicker(sseHeartbeat)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-hub.Done():
				return
			case <-tick.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				fl.Flush()
			case e, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(e)
				if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
					return
				}
				fl.Flush()
			}
		}
	}
}
