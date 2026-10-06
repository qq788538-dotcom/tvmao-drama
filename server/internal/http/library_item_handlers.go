package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/library"
)

// libraryFormMemory 是 ParseMultipartForm 的内存阈值（不是上限）：超出部分落到系统临时文件，
// 之后从临时文件流式写入存储，不把整个文件读进内存。上限由 maxUploadBytes 控制。
const libraryFormMemory = 32 << 20

// sanitizeLibraryExt 只保留扩展名里的 ASCII 字母和数字：扩展名直接拼进 object key，
// 用户文件名里的空格、冒号、#、% 等会让 ValidKey 失败或和 media:// 冲突。
func sanitizeLibraryExt(ext string) string {
	var b strings.Builder
	for _, r := range strings.TrimPrefix(ext, ".") {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 16 {
		return ""
	}
	return "." + b.String()
}

func detectKind(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	default:
		return ""
	}
}

type LibraryItemHandler struct {
	items   library.ItemsRepository
	folders library.FoldersRepository
	orphans library.OrphansRepository
	storage assets.Storage
	users   storageUserRepo
	signer  *MediaSigner
	db      *sql.DB
	// maxUploadBytes 是单个上传的上限，默认与 /assets 上传相同（assetMaxUploadBytes）；测试可调小。
	maxUploadBytes int64
}

func NewLibraryItemHandler(
	items library.ItemsRepository,
	folders library.FoldersRepository,
	orphans library.OrphansRepository,
	storage assets.Storage,
	users storageUserRepo,
	signer *MediaSigner,
	db *sql.DB,
) *LibraryItemHandler {
	return &LibraryItemHandler{
		items:   items,
		folders: folders,
		orphans: orphans,
		storage: storage,
		users:   users,
		signer:  signer,
		db:      db,

		maxUploadBytes: assetMaxUploadBytes,
	}
}

func (h *LibraryItemHandler) signItem(it *library.Item) {
	if it == nil {
		return
	}
	it.URL = h.signer.Sign(it.URL)
}

func (h *LibraryItemHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	q := r.URL.Query()
	folderID, _ := strconv.ParseInt(q.Get("folder_id"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, total, err := h.items.List(library.ItemFilter{
		UserID: userID, FolderID: folderID, Search: q.Get("search"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range items {
		h.signItem(&items[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "total": total})
}

func (h *LibraryItemHandler) Upload(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	folderID, err := strconv.ParseInt(chi.URLParam(r, "folderID"), 10, 64)
	if err != nil || folderID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid folder id")
		return
	}

	if _, err := h.folders.Get(userID, folderID); err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	}
	if err := r.ParseMultipartForm(libraryFormMemory); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "文件过大，单个文件不能超过 1GB")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file required")
		return
	}
	defer file.Close()
	size := header.Size

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	kind := detectKind(contentType)
	if kind == "" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported mime: "+contentType)
		return
	}

	// 预检配额。这次读取和下面的 AddStorage 之间存在一个 TOCTOU 窗口——
	// 可接受：并发上传可能让用户超出几 MB，但不会无限超出，因为每个请求
	// 都要过这道关卡。
	var used, quota int64
	if err := h.db.QueryRow(`SELECT storage_used_bytes, storage_quota_bytes FROM users WHERE id=?`, userID).Scan(&used, &quota); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if used+size > quota {
		writeError(w, http.StatusConflict, "storage quota exceeded")
		return
	}

	ext := filepath.Ext(header.Filename)
	objectKey := "library/" + strconv.FormatInt(userID, 10) + "/" + uuid.NewString() + sanitizeLibraryExt(ext)

	// 从 multipart 的临时文件流式写入，不整份读进内存。
	stored, err := h.storage.PutObject(r.Context(), assets.PutObjectInput{
		Key: objectKey, ContentType: contentType, Reader: file, Size: size,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	size = stored.Size

	if err := h.users.AddStorage(userID, size); err != nil {
		if delErr := h.storage.DeleteObject(r.Context(), objectKey); delErr != nil {
			log.Printf("library upload: rollback DeleteObject %s failed: %v", objectKey, delErr)
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name := strings.TrimSuffix(header.Filename, ext)
	id, err := h.items.Insert(library.Item{
		UserID: userID, FolderID: folderID, ObjectKey: objectKey,
		URL: stored.URL, SizeBytes: stored.Size, MimeType: contentType,
		Kind: kind, Source: "upload", Name: name,
	})
	if err != nil {
		_ = h.users.AddStorage(userID, -size)
		if delErr := h.storage.DeleteObject(r.Context(), objectKey); delErr != nil {
			log.Printf("library upload: rollback DeleteObject %s failed: %v", objectKey, delErr)
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	item, _ := h.items.Get(userID, id)
	h.signItem(item)
	writeJSON(w, http.StatusCreated, item)
}

type itemUpdatePayload struct {
	Name     *string `json:"name,omitempty"`
	FolderID *int64  `json:"folderId,omitempty"`
}

func (h *LibraryItemHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "itemID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	var input itemUpdatePayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.Name != nil && len(*input.Name) > 255 {
		writeError(w, http.StatusBadRequest, "name too long")
		return
	}
	if input.FolderID != nil {
		// 只为归属校验：素材不存在或不属于该用户时提前 404。
		if _, err := h.items.Get(userID, id); err != nil {
			if errors.Is(err, library.ErrItemNotFound) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := h.items.Update(userID, id, input.Name, input.FolderID); err != nil {
		switch {
		case errors.Is(err, library.ErrItemNotFound):
			writeError(w, http.StatusNotFound, "not found")
			return
		case errors.Is(err, library.ErrFolderNotFound):
			writeError(w, http.StatusNotFound, "target folder not found")
			return
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	item, _ := h.items.Get(userID, id)
	h.signItem(item)
	writeJSON(w, http.StatusOK, item)
}

func (h *LibraryItemHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "itemID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}

	item, err := h.items.Get(userID, id)
	if err != nil {
		if errors.Is(err, library.ErrItemNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE assets SET library_item_id=NULL WHERE library_item_id=?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(`DELETE FROM library_items WHERE id=? AND user_id=?`, id, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := library.TryReclaim(library.ReclaimDeps{Querier: tx, Execer: tx, Orphans: h.orphans}, item.ObjectKey, userID, item.SizeBytes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 只有当 object_key 没有剩余引用、且被删的 row 是 size>0 的 canonical
	// row 时才扣减配额。引用 row（size_bytes=0）从来不持有那个 key 的配额。
	if item.SizeBytes > 0 {
		referenced, err := library.IsStillReferenced(h.db, item.ObjectKey)
		if err == nil && !referenced {
			_ = h.users.AddStorage(userID, -item.SizeBytes)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
