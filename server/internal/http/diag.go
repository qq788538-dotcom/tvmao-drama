package http

import (
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// wsEcho 原样回显每一帧。P0 只用它验证 WKWebView / WebView2 能带 token 连上
// sidecar 的 WebSocket；P5 会把这条路由换成真正的 Agent hub。
func wsEcho(w nethttp.ResponseWriter, r *nethttp.Request) {
	// Origin 已由 localauth 校验过，这里不再让 websocket 库按同源规则二次拦截。
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	for {
		typ, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		if err := c.Write(r.Context(), typ, data); err != nil {
			return
		}
	}
}

// diagHandler 给诊断页用：上传一个测试媒体文件，再通过 127.0.0.1 读回来，
// 验证 Range 请求和 mediabunny 的 UrlSource。文件名固定为 sample.<ext>，
// 反复上传只会覆盖，不会堆积。
type diagHandler struct{ dir string }

const diagMaxBytes = 500 << 20

var diagNameRe = regexp.MustCompile(`^sample\.(mp4|mov|webm|mp3|wav|m4a)$`)

func (h *diagHandler) upload(w nethttp.ResponseWriter, r *nethttp.Request) {
	r.Body = nethttp.MaxBytesReader(w, r.Body, diagMaxBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		nethttp.Error(w, "缺少 file 字段或文件过大", nethttp.StatusBadRequest)
		return
	}
	defer file.Close()

	name := "sample" + strings.ToLower(filepath.Ext(header.Filename))
	if !diagNameRe.MatchString(name) {
		nethttp.Error(w, "不支持的文件类型", nethttp.StatusBadRequest)
		return
	}
	dir := filepath.Join(h.dir, "diag")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]string{"name": name})
}

func (h *diagHandler) media(w nethttp.ResponseWriter, r *nethttp.Request) {
	name := chi.URLParam(r, "name")
	if !diagNameRe.MatchString(name) {
		nethttp.NotFound(w, r)
		return
	}
	// ServeFile 自带 Range / 206 / Content-Range 处理。
	nethttp.ServeFile(w, r, filepath.Join(h.dir, "diag", name))
}
