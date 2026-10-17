package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/library"
)

type LibraryFolderHandler struct {
	folders library.FoldersRepository
	items   library.ItemsRepository
	orphans library.OrphansRepository
	users   storageUserRepo
	db      *sql.DB
}

// 注意：force=1 删除需要在事务里跑裸 SQL（UPDATE assets... + DELETE
// library_items...），所以我们直接在 *sql.Tx 上执行这些语句，而不走
// assets.Repository。这就是为什么这里没有注入 assets repo 依赖。
func NewLibraryFolderHandler(
	f library.FoldersRepository,
	items library.ItemsRepository,
	orphans library.OrphansRepository,
	users storageUserRepo,
	db *sql.DB,
) *LibraryFolderHandler {
	return &LibraryFolderHandler{
		folders: f,
		items:   items,
		orphans: orphans,
		users:   users,
		db:      db,
	}
}

func (h *LibraryFolderHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	list, err := h.folders.List(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

type folderCreatePayload struct {
	ParentID *int64 `json:"parentId"`
	Name     string `json:"name"`
}

func (h *LibraryFolderHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var input folderCreatePayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.Name == "" || len(input.Name) > 255 {
		writeError(w, http.StatusBadRequest, "name required, max 255")
		return
	}
	id, err := h.folders.Create(userID, input.ParentID, input.Name)
	if err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "parent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, _ := h.folders.Get(userID, id)
	writeJSON(w, http.StatusCreated, f)
}

type folderUpdatePayload struct {
	Name        *string `json:"name,omitempty"`
	ParentID    *int64  `json:"parentId,omitempty"`
	ClearParent bool    `json:"clearParent,omitempty"` // 显式想移到根目录用这个
}

func (h *LibraryFolderHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "folderID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid folder id")
		return
	}
	var input folderUpdatePayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 只为归属校验：目录不存在或不属于该用户时提前 404，别等到 Rename/Move 才发现。
	if _, err := h.folders.Get(userID, id); err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if input.Name != nil {
		if *input.Name == "" || len(*input.Name) > 255 {
			writeError(w, http.StatusBadRequest, "name required, max 255")
			return
		}
		if err := h.folders.Rename(userID, id, *input.Name); err != nil {
			if errors.Is(err, library.ErrFolderNotFound) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if input.ParentID != nil || input.ClearParent {
		var p *int64
		if !input.ClearParent {
			p = input.ParentID
		}
		if err := h.folders.Move(userID, id, p); err != nil {
			switch {
			case errors.Is(err, library.ErrFolderNotFound):
				writeError(w, http.StatusNotFound, "not found")
				return
			case errors.Is(err, library.ErrFolderCycle):
				writeError(w, http.StatusBadRequest, "cycle")
				return
			default:
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	updated, _ := h.folders.Get(userID, id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *LibraryFolderHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "folderID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid folder id")
		return
	}
	force := r.URL.Query().Get("force") == "1"
	// 只为归属校验：目录不存在或不属于该用户时提前 404，别等到 Rename/Move 才发现。
	if _, err := h.folders.Get(userID, id); err != nil {
		if errors.Is(err, library.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !force {
		if err := h.folders.DeleteIfEmpty(userID, id); err != nil {
			if errors.Is(err, library.ErrFolderNotFound) {
				writeError(w, http.StatusNotFound, "not found")
				return
			}
			if errors.Is(err, library.ErrFolderNotEmpty) {
				f, _ := h.folders.Get(userID, id)
				var itemCount int64
				var childCount int64
				if f != nil {
					itemCount = f.ItemCount
				}
				_ = h.db.QueryRow(`SELECT COUNT(*) FROM library_folders WHERE parent_id=? AND user_id=?`, id, userID).Scan(&childCount)
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":      "folder not empty",
					"itemCount":  itemCount,
					"childCount": childCount,
				})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// force=1：递归删除（items + 后代 folder）
	descIDs, err := h.folders.DescendantIDs(userID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := h.items.ListByFolderIDs(userID, descIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	for _, it := range items {
		if _, err := tx.Exec(`UPDATE assets SET library_item_id=NULL WHERE library_item_id=?`, it.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`DELETE FROM library_items WHERE id=? AND user_id=?`, it.ID, userID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := library.TryReclaim(library.ReclaimDeps{Querier: tx, Execer: tx, Orphans: h.orphans}, it.ObjectKey, userID, it.SizeBytes); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// 深度优先删子 folder（descIDs 来自 BFS，反向遍历以先删叶子）
	for i := len(descIDs) - 1; i >= 0; i-- {
		if _, err := tx.Exec(`DELETE FROM library_folders WHERE id=? AND user_id=?`, descIDs[i], userID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 给那些在这里失去最后一个引用的 object_key 扣减配额。
	if h.users != nil {
		var freed int64
		for _, it := range items {
			if it.SizeBytes == 0 {
				continue
			}
			referenced, err := library.IsStillReferenced(h.db, it.ObjectKey)
			if err != nil {
				continue
			}
			if !referenced {
				freed += it.SizeBytes
			}
		}
		if freed > 0 {
			_ = h.users.AddStorage(userID, -freed)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
