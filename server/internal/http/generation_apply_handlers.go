package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
)

// generationApplyAssets 是 assets 仓库的窄子集（generation.AssetRecorder 也满足）。
type generationApplyAssets interface {
	FindByTaskSlot(taskID int64, outputIndex int) (*assets.AssetRecord, error)
	ListForTask(taskID int64) ([]assets.AssetRecord, error)
	Insert(rec assets.AssetRecord) (int64, error)
}

// GenerationApplyHandler 把生成历史里的某个产物落到指定画布项目——
// 「从生成历史选择」的确认动作。与素材库的 ApplyToCanvas 同一套引用语义:
// 插一条 source_kind='generation_ref'、size_bytes=0 的项目级 asset row,
// 引用产物的 object_key。有这条引用行,删除源项目/源任务时孤儿 GC 能看到
// 目标画布还在用这个对象,不会误删(与跨项目粘贴的 canvas_ref 同一条防线)。
type GenerationApplyHandler struct {
	service *generation.Service
	assets  generationApplyAssets
	signer  mediaRefSigner
	db      *sql.DB
}

func NewGenerationApplyHandler(service *generation.Service, ar generationApplyAssets, signer mediaRefSigner, db *sql.DB) *GenerationApplyHandler {
	return &GenerationApplyHandler{service: service, assets: ar, signer: signer, db: db}
}

type applyGenerationPayload struct {
	ProjectID   int64 `json:"projectId"`
	OutputIndex int   `json:"outputIndex"`
}

// resolveTaskAsset 找到该任务第 idx 个产物对应的 canonical asset 行。
// 三层兜底对应资产落库机制的三个年代:槽位列(output_index)是后来加的,
// 更早的行只有 assetIds 快照或裸的 source_task_id 关联。
func (h *GenerationApplyHandler) resolveTaskAsset(task *generation.Task, idx int) *assets.AssetRecord {
	// ① 槽位精确命中(persistOutputs 的 InsertSlotTx 写的行)。
	if rec, err := h.assets.FindByTaskSlot(task.ID, idx); err == nil && rec != nil {
		return rec
	}
	// ② output.assetIds[idx](与 outputUrls 同序;落库失败处为 0)。
	if len(task.Output) > 0 {
		var out struct {
			AssetIDs []int64 `json:"assetIds"`
		}
		if json.Unmarshal(task.Output, &out) == nil && idx < len(out.AssetIDs) && out.AssetIDs[idx] > 0 {
			if rows, err := h.assets.ListForTask(task.ID); err == nil {
				for i := range rows {
					if rows[i].ID == out.AssetIDs[idx] {
						return &rows[i]
					}
				}
			}
		}
	}
	// ③ 最老的行:既无槽位也无 assetIds,只能按 source_task_id 全取后以 id
	// 升序对位(插入顺序即产物顺序——persistOutputs 从来都是按序逐个插)。
	if rows, err := h.assets.ListForTask(task.ID); err == nil && idx < len(rows) {
		sort.Slice(rows, func(a, b int) bool { return rows[a].ID < rows[b].ID })
		return &rows[idx]
	}
	return nil
}

// ApplyToCanvas POST /tasks/{taskID}/apply-to-canvas {projectId, outputIndex}
func (h *GenerationApplyHandler) ApplyToCanvas(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	taskID, err := strconv.ParseInt(chi.URLParam(r, "taskID"), 10, 64)
	if err != nil || taskID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	var input applyGenerationPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if input.ProjectID <= 0 {
		writeError(w, http.StatusBadRequest, "projectId required")
		return
	}
	if input.OutputIndex < 0 {
		writeError(w, http.StatusBadRequest, "invalid outputIndex")
		return
	}

	// 目标项目归属校验(IDOR 防线,与素材库 ApplyToCanvas 同款)。
	var ownerUser int64
	if err := h.db.QueryRow(`SELECT user_id FROM projects WHERE id=?`, input.ProjectID).Scan(&ownerUser); err != nil {
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

	// 任务归属校验走 service.Get(带 user_id 条件)。
	task, err := h.service.Get(r.Context(), userID, taskID)
	if err != nil {
		if errors.Is(err, generation.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec := h.resolveTaskAsset(task, input.OutputIndex)
	if rec == nil {
		// 老任务产物可能从未落过 asset 行(persistOutputs 抓取失败时保留原始 URL)。
		writeError(w, http.StatusNotFound, "该产物没有资产记录，无法引用到画布")
		return
	}

	// 故意不带 SourceTaskID:任务删除路径是 `DELETE FROM assets WHERE
	// source_task_id=?`(generation.Delete),带上的话用户删掉这条历史记录时
	// 引用行会被连带删除,目标画布节点就失去了 GC 防线——与 canvas_ref 同款,
	// 引用行的生命周期跟随目标项目,不跟随源任务。
	assetID, err := h.assets.Insert(assets.AssetRecord{
		UserID:     userID,
		ProjectID:  input.ProjectID,
		ObjectKey:  rec.ObjectKey,
		SourceKind: "generation_ref",
		URL:        rec.URL,
		SizeBytes:  0, // 引用——配额由 canonical row 记账(同 library_ref/canvas_ref)
		MimeType:   rec.MimeType,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	kind := "image"
	switch {
	case strings.HasPrefix(rec.MimeType, "video/"):
		kind = "video"
	case strings.HasPrefix(rec.MimeType, "audio/"):
		kind = "audio"
	case strings.HasPrefix(rec.MimeType, "image/"):
		kind = "image"
	default:
		// 老行 mime 缺失/异常时退回任务级 output_kind(0103 回填过)。
		if task.OutputKind != "" {
			kind = task.OutputKind
		}
	}

	url := rec.URL
	if h.signer != nil {
		url = h.signer.Sign(url)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"assetId":  assetID,
		"url":      url,
		"kind":     kind,
		"mimeType": rec.MimeType,
	})
}
