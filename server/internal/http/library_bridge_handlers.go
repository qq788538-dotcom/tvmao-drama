package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/library"
)

type LibraryBridgeHandler struct {
	items   library.ItemsRepository
	folders library.FoldersRepository
	assets  assets.Repository
	signer  *MediaSigner
	db      *sql.DB
}

func NewLibraryBridgeHandler(
	items library.ItemsRepository,
	folders library.FoldersRepository,
	ar assets.Repository,
	signer *MediaSigner,
	db *sql.DB,
) *LibraryBridgeHandler {
	return &LibraryBridgeHandler{items: items, folders: folders, assets: ar, signer: signer, db: db}
}

type saveToLibraryPayload struct {
	FolderID int64  `json:"folderId"`
	Name     string `json:"name,omitempty"`
}

// SaveToLibrary 把一个已有项目 asset 的引用复制进用户的素材库（相同的
// object_key，库 row 上 size_bytes=0，避免对用户存储配额重复计数——
// 项目 asset row 仍然是那个 object_key 的 canonical「size>0」记录）。
// 同时给该 asset 盖上 library_item_id，让 UI 能显示这个 asset 已被关联。
func (h *LibraryBridgeHandler) SaveToLibrary(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil || projectID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	assetID, err := strconv.ParseInt(chi.URLParam(r, "assetID"), 10, 64)
	if err != nil || assetID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid asset id")
		return
	}

	var input saveToLibraryPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.FolderID <= 0 {
		writeError(w, http.StatusBadRequest, "folderId required")
		return
	}
	if _, err := h.folders.Get(userID, input.FolderID); err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 校验 asset 归属和项目范围。
	var (
		ownerUser    int64
		ownerProject sql.NullInt64
		objectKey    string
		mimeType     string
		url          string
	)
	err = h.db.QueryRow(`SELECT user_id, project_id, object_key, mime_type, url FROM assets WHERE id=?`, assetID).
		Scan(&ownerUser, &ownerProject, &objectKey, &mimeType, &url)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "asset not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ownerUser != userID {
		writeError(w, http.StatusForbidden, "asset not yours")
		return
	}
	if ownerProject.Valid && ownerProject.Int64 != projectID {
		writeError(w, http.StatusBadRequest, "asset belongs to a different project")
		return
	}

	kind := detectKind(mimeType)
	if kind == "" {
		writeError(w, http.StatusUnsupportedMediaType, "kind not supported")
		return
	}

	name := input.Name
	if name == "" {
		name = "asset_" + strconv.FormatInt(assetID, 10)
	}

	libID, err := h.items.Insert(library.Item{
		UserID:    userID,
		FolderID:  input.FolderID,
		ObjectKey: objectKey,
		URL:       url,
		SizeBytes: 0, // 引用 row——不对用户配额重复计数
		MimeType:  mimeType,
		Kind:      kind,
		Source:    "from_project",
		Name:      name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 给该 asset 盖上 library_item_id。如果这条 UPDATE 失败，library_items
	// row 已经存在了；客户端会拿到 500 并可能重试，从而创建第二条指向同一
	// object_key 的 size_bytes=0 库 row。这两条 row 对配额（size=0）和 GC
	//（tryReclaim 会认为这个 key 仍被引用）都无害，但第一条会变成孤儿
	//（没有 asset 指向它）。相比给跨两个 repository 的操作加事务，这是可接受
	// 的取舍。这条单行 PK 更新极不可能独立于上面的 INSERT 失败。
	if _, err := h.db.Exec(`UPDATE assets SET library_item_id=? WHERE id=?`, libID, assetID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, _ := h.items.Get(userID, libID)
	if item != nil {
		item.URL = h.signer.Sign(item.URL)
	}
	writeJSON(w, http.StatusCreated, item)
}

type saveFromURLPayload struct {
	URL      string `json:"url"`
	FolderID int64  `json:"folderId"`
	Name     string `json:"name,omitempty"`
}

// SaveFromURL: POST /library/save-from-url —— 按媒体 URL 把画布上的东西存进素材库。
//
// 为什么另开一条以 URL 为入口的路：SaveToLibrary 需要路径里的 assetID，而那个 id
// 得靠前端在生成那一刻存进节点 settings。历史节点、agent 产出的节点、从素材库应用
// 回来的节点都没存过，于是永远点不了「保存到素材库」。URL 则是节点始终持有的东西，
// 而 object_key 是图片在本系统里的真正身份——从 URL 还原它是确定性的。
func (h *LibraryBridgeHandler) SaveFromURL(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var input saveFromURLPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.FolderID <= 0 {
		writeError(w, http.StatusBadRequest, "folderId required")
		return
	}
	if _, err := h.folders.Get(userID, input.FolderID); err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	objectKey := h.signer.ObjectKey(input.URL)
	if objectKey == "" {
		writeError(w, http.StatusBadRequest, "无法识别这个媒体，请重新生成或上传")
		return
	}

	// 取该用户名下这个 object_key 的最近一条 asset：既做归属校验，也拿到
	// mime/url 这些落库要用的字段。
	var (
		assetID  int64
		mimeType string
		url      string
	)
	err := h.db.QueryRow(
		`SELECT id, mime_type, url FROM assets WHERE user_id=? AND object_key=? ORDER BY id DESC LIMIT 1`,
		userID, objectKey).Scan(&assetID, &mimeType, &url)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	kind := detectKind(mimeType)
	if kind == "" {
		writeError(w, http.StatusUnsupportedMediaType, "kind not supported")
		return
	}
	name := input.Name
	if name == "" {
		name = "asset_" + strconv.FormatInt(assetID, 10)
	}

	libID, err := h.items.Insert(library.Item{
		UserID:    userID,
		FolderID:  input.FolderID,
		ObjectKey: objectKey,
		URL:       url,
		SizeBytes: 0, // 引用 row——不对用户配额重复计数
		MimeType:  mimeType,
		Kind:      kind,
		Source:    "from_project",
		Name:      name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 盖 library_item_id 的失败取舍同 SaveToLibrary，见那边的注释。
	if _, err := h.db.Exec(`UPDATE assets SET library_item_id=? WHERE id=?`, libID, assetID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	item, _ := h.items.Get(userID, libID)
	if item != nil {
		item.URL = h.signer.Sign(item.URL)
	}
	writeJSON(w, http.StatusCreated, item)
}

type applyToCanvasPayload struct {
	ProjectID int64 `json:"projectId"`
}

// ApplyToCanvas 插入一条项目级的 asset row，引用库 item 的 object_key。
// 这条新 asset row 的 size_bytes=0、source_kind='library_ref'，所以不计入
// 配额，删除路径也能把它识别成引用（只要库 item 或任何其他项目 asset 还
// 引用着这个底层 TOS object，它就会保留）。
func (h *LibraryBridgeHandler) ApplyToCanvas(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemID"), 10, 64)
	if err != nil || itemID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}

	var input applyToCanvasPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.ProjectID <= 0 {
		writeError(w, http.StatusBadRequest, "projectId required")
		return
	}

	// 校验项目归属。
	var ownerUser int64
	if err := h.db.QueryRow(`SELECT user_id FROM projects WHERE id=? AND deleted_at IS NULL`, input.ProjectID).Scan(&ownerUser); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ownerUser != userID {
		writeError(w, http.StatusForbidden, "project not yours")
		return
	}

	item, err := h.items.Get(userID, itemID)
	if err != nil {
		if errors.Is(err, library.ErrItemNotFound) {
			writeError(w, http.StatusNotFound, "library item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	assetID, err := h.assets.Insert(assets.AssetRecord{
		UserID:        userID,
		ProjectID:     input.ProjectID,
		ObjectKey:     item.ObjectKey,
		SourceKind:    "library_ref",
		URL:           item.URL,
		SizeBytes:     0, // 引用——配额由 canonical row 记账
		MimeType:      item.MimeType,
		LibraryItemID: item.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"assetId":    assetID,
		"url":        h.signer.Sign(item.URL),
		"kind":       item.Kind,
		"mimeType":   item.MimeType,
		"width":      item.Width,
		"height":     item.Height,
		"durationMs": item.DurationMs,
	})
}
