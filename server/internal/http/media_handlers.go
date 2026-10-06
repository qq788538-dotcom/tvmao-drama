package http

import (
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

// mediaFileStore 把 object key 换算成本地文件路径；key 不合法时返回错误（*assets.LocalStorage 实现）。
type mediaFileStore interface {
	Path(key string) (string, error)
}

// mediaMimeStore 查对象登记过的 MIME。生成产物的 key 没有扩展名，只能靠它。
type mediaMimeStore interface {
	MimeByObjectKey(objectKey string) (string, error)
}

// MediaHandler 提供 sidecar 的本地媒体文件（spec S3），以及“在访达/资源管理器中显示”。
// URL 里的 token 由 localauth 校验（媒体专用 token 只放行 GET/HEAD /media/*）。
type MediaHandler struct {
	files  mediaFileStore
	signer *MediaSigner
	mimes  mediaMimeStore
	reveal func(path string) error
}

func NewMediaHandler(files mediaFileStore, signer *MediaSigner, mimes mediaMimeStore, reveal func(path string) error) *MediaHandler {
	return &MediaHandler{files: files, signer: signer, mimes: mimes, reveal: reveal}
}

// mediaCSP 让用户上传的 SVG/HTML 被当作页面直接打开时也跑不了脚本；嵌在 <img>/<video> 里时不生效。
const mediaCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// Serve 处理 GET/HEAD /media/<key>。key 非法、文件不存在、是目录，一律 404，不区分原因。
func (h *MediaHandler) Serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/media/")
	p, err := h.files.Path(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", h.contentType(key, f))
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", mediaCSP)
	// URL 带本次启动的 token，同一次启动内同一 key 的 URL 不变，可以放心缓存。
	hdr.Set("Cache-Control", "private, max-age=86400")
	// ServeContent 负责 Range / 206 / HEAD / If-Modified-Since；Content-Type 已设好，它不会再嗅探。
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// contentType 依次取：库里登记的 MIME → 扩展名 → 前 512 字节嗅探。
// application/octet-stream 等于没登记（上传时浏览器没给类型），继续往下找。
func (h *MediaHandler) contentType(key string, f *os.File) string {
	if h.mimes != nil {
		if m, err := h.mimes.MimeByObjectKey(key); err == nil && m != "" && m != "application/octet-stream" {
			return m
		}
	}
	if m := mime.TypeByExtension(strings.ToLower(path.Ext(key))); m != "" {
		return m
	}
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	_, _ = f.Seek(0, io.SeekStart)
	return http.DetectContentType(buf[:n])
}

type revealRequest struct {
	URL string `json:"url"`
}

// Reveal 处理 POST /api/v1/media/reveal：把节点里的媒体 URL 换算成本机真实路径，在访达 / 资源管理器
// 里选中它。由 sidecar 打开而不是把路径交给前端：Tauri 壳不必授予任何文件权限（spec SEC4），
// 真实路径也不出 sidecar。
func (h *MediaHandler) Reveal(w http.ResponseWriter, r *http.Request) {
	var body revealRequest
	if err := decodeJSON(r, &body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	key := h.signer.ObjectKey(body.URL)
	if key == "" {
		writeError(w, http.StatusBadRequest, "无法识别这个媒体")
		return
	}
	p, err := h.files.Path(key)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法识别这个媒体")
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeError(w, http.StatusNotFound, "文件不存在，可能已被删除")
		return
	}
	if h.reveal == nil {
		writeError(w, http.StatusNotImplemented, "当前系统不支持")
		return
	}
	if err := h.reveal(p); err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开所在文件夹")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SQLMediaMimeStore 按 object_key 查 MIME：先 assets（上传与生成产物），再 library_items（素材库上传）。
type SQLMediaMimeStore struct {
	db *sql.DB
}

func NewSQLMediaMimeStore(db *sql.DB) *SQLMediaMimeStore {
	return &SQLMediaMimeStore{db: db}
}

func (s *SQLMediaMimeStore) MimeByObjectKey(objectKey string) (string, error) {
	for _, q := range []string{
		`SELECT mime_type FROM assets WHERE object_key = ? ORDER BY id DESC LIMIT 1`,
		`SELECT mime_type FROM library_items WHERE object_key = ? ORDER BY id DESC LIMIT 1`,
	} {
		var m string
		err := s.db.QueryRow(q, objectKey).Scan(&m)
		if err == nil && m != "" && m != "application/octet-stream" {
			return m, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}
