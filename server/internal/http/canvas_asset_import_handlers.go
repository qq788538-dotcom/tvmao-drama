package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

// 画布「复制节点」跨项目粘贴的资产登记。
//
// 粘贴只是把节点 JSON（内含 media://<object_key> 引用）落进目标项目的快照，
// 文件本体不动。但 GC 的引用计数按 assets ∪ library_items 的 object_key 算
// （见 library/reclaim.go）——目标项目没有自己的 asset 行的话，源项目一删、
// 它名下的行被清，对象就会被判孤儿回收，粘过来的节点变死链。
// 所以跨项目粘贴后前端调这里，给目标项目插引用行（source_kind='canvas_ref'、
// size_bytes=0 不计配额）——与素材库 ApplyToCanvas 的 'library_ref' 同一套
// 引用行机制，不物理复制文件。

// canvasAssetRefStore 是 ImportRefs 需要的最小查询集。窄接口：测试用手写
// fake，不用为三条 SQL 拖一个 sqlmock 进来。
type canvasAssetRefStore interface {
	// ProjectOwner 返回项目属主 user_id；项目不存在返回 sql.ErrNoRows。
	ProjectOwner(projectID int64) (int64, error)
	// CanonicalByKey 找调用者名下该 object_key 的任一 asset 行，抄它的
	// url/mime 给引用行。found=false 表示这个 key 不属于调用者（或压根不存在）
	// ——归属校验与取数是同一条查询。
	CanonicalByKey(userID int64, objectKey string) (url string, mime string, found bool, err error)
	// ProjectHasKey 判断目标项目是否已有该 object_key 的行——重复粘贴幂等，
	// 不堆重复引用行。
	ProjectHasKey(projectID int64, objectKey string) (bool, error)
}

type assetRefInserter interface {
	Insert(assets.AssetRecord) (int64, error)
}

type CanvasAssetImportHandler struct {
	store    canvasAssetRefStore
	inserter assetRefInserter
}

func NewCanvasAssetImportHandler(store canvasAssetRefStore, inserter assetRefInserter) *CanvasAssetImportHandler {
	return &CanvasAssetImportHandler{store: store, inserter: inserter}
}

// 一次粘贴的媒体数量级是个位数到几十（节点 content + history）；200 远超正常
// 使用，纯粹挡恶意灌接口。
const maxImportRefKeys = 200

type importAssetRefsPayload struct {
	ObjectKeys []string `json:"objectKeys"`
}

type importAssetRefsResult struct {
	// Imported 含「已存在引用」的幂等命中——语义是"这些 key 在目标项目已有着落"。
	Imported int `json:"imported"`
	// Missing 是调用者名下查无 asset 行的 key（老到没有记账行/别人的资源）。
	// 前端只提示不阻塞：节点已粘上，退化为「源项目别删」的旧行为。
	Missing []string `json:"missing,omitempty"`
}

func (h *CanvasAssetImportHandler) ImportRefs(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())

	var input importAssetRefsPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(input.ObjectKeys) == 0 {
		writeError(w, http.StatusBadRequest, "objectKeys required")
		return
	}
	if len(input.ObjectKeys) > maxImportRefKeys {
		writeError(w, http.StatusBadRequest, "too many object keys")
		return
	}

	owner, err := h.store.ProjectOwner(projectID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if owner != userID {
		writeError(w, http.StatusForbidden, "project not yours")
		return
	}

	seen := make(map[string]bool, len(input.ObjectKeys))
	var res importAssetRefsResult
	for _, key := range input.ObjectKeys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true

		url, mime, found, err := h.store.CanonicalByKey(userID, key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			res.Missing = append(res.Missing, key)
			continue
		}
		has, err := h.store.ProjectHasKey(projectID, key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if has {
			res.Imported++
			continue
		}
		if _, err := h.inserter.Insert(assets.AssetRecord{
			UserID:     userID,
			ProjectID:  projectID,
			ObjectKey:  key,
			SourceKind: "canvas_ref",
			URL:        url,
			SizeBytes:  0, // 引用——配额由 canonical row 记账（同 library_ref）
			MimeType:   mime,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		res.Imported++
	}
	writeJSON(w, http.StatusOK, res)
}

// SQLCanvasAssetRefStore 是 canvasAssetRefStore 的 MySQL 实现，main.go 注入。
type SQLCanvasAssetRefStore struct {
	db *sql.DB
}

func NewSQLCanvasAssetRefStore(db *sql.DB) *SQLCanvasAssetRefStore {
	return &SQLCanvasAssetRefStore{db: db}
}

func (s *SQLCanvasAssetRefStore) ProjectOwner(projectID int64) (int64, error) {
	var owner int64
	err := s.db.QueryRow(`SELECT user_id FROM projects WHERE id=?`, projectID).Scan(&owner)
	return owner, err
}

func (s *SQLCanvasAssetRefStore) CanonicalByKey(userID int64, objectKey string) (string, string, bool, error) {
	var url, mime string
	err := s.db.QueryRow(
		`SELECT url, mime_type FROM assets WHERE object_key=? AND user_id=? ORDER BY id LIMIT 1`,
		objectKey, userID,
	).Scan(&url, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return url, mime, true, nil
}

func (s *SQLCanvasAssetRefStore) ProjectHasKey(projectID int64, objectKey string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM assets WHERE object_key=? AND project_id=?)`,
		objectKey, projectID,
	).Scan(&exists)
	return exists, err
}
