package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/library"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/workflows"
)

type taskProjectCleanup interface {
	DeleteForProject(projectID int64) error
}

type ProjectHandler struct {
	repo        projects.Repository
	signer      *MediaSigner
	storage     assets.Storage
	assetRepo   assets.Repository
	users       storageUserRepo
	taskCleanup taskProjectCleanup
	orphans     library.OrphansRepository
	db          *sql.DB
	// duplicator 与发布/克隆共用同一个（workflows.AssetDuplicator）：复制快照引用的
	// 对象、改写 URL、建 asset 行、计配额、失败时把已复制对象喂 GC 队列，这一整套
	// 没必要为「项目副本」再写一遍。
	duplicator *workflows.AssetDuplicator
}

func NewProjectHandler(
	repo projects.Repository,
	signer *MediaSigner,
	storage assets.Storage,
	assetRepo assets.Repository,
	users storageUserRepo,
	taskCleanup taskProjectCleanup,
	orphans library.OrphansRepository,
	db *sql.DB,
	duplicator *workflows.AssetDuplicator,
) *ProjectHandler {
	return &ProjectHandler{
		repo:        repo,
		signer:      signer,
		storage:     storage,
		assetRepo:   assetRepo,
		users:       users,
		taskCleanup: taskCleanup,
		orphans:     orphans,
		db:          db,
		duplicator:  duplicator,
	}
}

type createProjectBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var body createProjectBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	id, err := h.repo.Create(userID, body.Name, body.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	project, _ := h.repo.GetForUser(userID, id)
	writeJSON(w, http.StatusCreated, project)
}

// listPageSizeMax 挡住 ?limit=100000 这种把分页当全量用的调用——分页的意义就是
// 别让那个 LEFT JOIN assets 扫全表。
const listPageSizeMax = 100

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	// 不带 limit = 拉全量，与加分页之前逐字一致。首页「个人最近项目」区就是这么调的，
	// 本次不动它。
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit > listPageSizeMax {
		limit = listPageSizeMax
	}
	list, total, err := h.repo.ListForUser(userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []projects.Project{}
	}
	// 封面出口签名：repo 给的是 canonical media://（能熬过签名 TTL 的持久形态），
	// 这里换成 768w 缩略签名 URL。768 = 首页卡片渲染宽 ~300 CSS px 的 2x 屏覆盖，
	// 与画布缩略图规格一致（media_thumb_handlers.go 的规格论证）。
	for i := range list {
		if list[i].CoverURL != "" {
			list[i].CoverURL = h.signer.SignThumb(list[i].CoverURL, 768)
		}
	}
	// total 与分页无关，恒是该用户项目总数——前端据此判断还有没有下一页。
	// 老调用方（不传 limit）多收一个字段无害。
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "total": total})
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	p, err := h.repo.GetForUser(userID, projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 网页端只在打开项目时 GET 它（画布加载、启动时恢复上次项目）：顺手刷新 updated_at，
	// 列表按 updated_at 倒序即「最近打开在前」。失败不影响打开。CLI 走 /cli 路由，不触发。
	if err := h.repo.Touch(userID, projectID); err != nil {
		log.Printf("project %d touch updated_at: %v", projectID, err)
	}
	writeJSON(w, http.StatusOK, p)
}

type updateProjectBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// snapshotVersionResponse 是所有画布写入端点的统一回执：只回写后的版本号。
// 画布已经全量改走局部 mutation（canvas_mutation_handlers.go），整快照 PUT
// 那条乐观锁链路连同 baseVersion / 409 冲突体一起删了，只剩这个版本号回执。
type snapshotVersionResponse struct {
	Version int64 `json:"version"`
}

type getSnapshotResponse struct {
	Payload  json.RawMessage `json:"payload"`
	SyncedAt *time.Time      `json:"syncedAt"`
	Version  int64           `json:"version"`
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	var body updateProjectBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := h.repo.Update(userID, projectID, body.Name, body.Description); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, err := h.repo.GetForUser(userID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// Delete 软删项目：打 deleted_at 立即从列表隐藏、请求秒回；级联清资产/生成任务/存储 + 配额
// 回收由后台 ProjectCleaner worker 异步做（画布项目内文件可能很多，同步删会卡请求/超时）。
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if err := h.repo.Delete(userID, projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ProjectHandler) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if _, err := h.repo.GetForUser(userID, projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshot, err := h.repo.GetLatest(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 出口时把 `media://<key>` 条目换成新鲜的签名 URL。
	var syncedAt *time.Time
	var payload json.RawMessage
	version := int64(0)
	if snapshot != nil {
		signed := h.signer.SignJSON(snapshot.Payload)
		payload = signed
		syncedAt = &snapshot.SyncedAt
		version = snapshot.Version
	}
	writeJSON(w, http.StatusOK, getSnapshotResponse{
		Payload:  payload,
		SyncedAt: syncedAt,
		Version:  version,
	})
}

// ─── 封面 ────────────────────────────────────────────────────────────────────

type setCoverBody struct {
	// AssetID 指向该项目内的一条图片资产；<=0 表示清除手动封面、回落自动取图。
	// **刻意不收 URL**：前端手里的是短时签名 URL，存进库过几小时就是死链；由服务端
	// 从 asset 行取 canonical media:// 落库，顺带天然完成归属校验。
	AssetID int64 `json:"assetId"`
}

// projectImage 是选封面弹窗要的最小信息。url 出签名缩略图——弹窗里是网格缩略，
// 没必要拉原图。
type projectImage struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	MimeType  string `json:"mimeType"`
	CreatedAt string `json:"createdAt"`
}

// ListImages 列该项目内的图片资产，供「修改封面 → 从项目中选择」。
//
// 为什么不复用已有的资产列表端点：那个是「按 user 列最近 100 条」
// （AssetHandler.List），跨项目且有条数截断，喂不了这个弹窗。这里按 project 列，
// 归属校验由 ProjectFromURL + GetForUser 完成。
func (h *ProjectHandler) ListImages(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if _, err := h.repo.GetForUser(userID, projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := h.assetRepo.ListForProject(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]projectImage, 0, len(rows))
	for _, a := range rows {
		if !strings.HasPrefix(a.MimeType, "image/") {
			continue
		}
		out = append(out, projectImage{
			ID:        a.ID,
			URL:       h.signer.SignThumb(a.URL, 768),
			MimeType:  a.MimeType,
			CreatedAt: a.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// SetCover 手动指定封面。assetId<=0 清除（UI 眼下不给清除入口，但后端留着这条路：
// 它只是 SetCover(…, "") 一行，不写反而要多一个拒绝分支）。
func (h *ProjectHandler) SetCover(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	var body setCoverBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	coverURL := ""
	if body.AssetID > 0 {
		// 只在**该项目自己的**资产里找。跨项目/跨用户的 assetID 在这里查无此行，
		// 于是自动被拒——不需要另写一道归属校验。
		rows, err := h.assetRepo.ListForProject(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, a := range rows {
			if a.ID != body.AssetID {
				continue
			}
			if !strings.HasPrefix(a.MimeType, "image/") {
				writeError(w, http.StatusBadRequest, "封面必须是图片")
				return
			}
			coverURL = a.URL // canonical media://，不是签名 URL
			break
		}
		if coverURL == "" {
			writeError(w, http.StatusNotFound, "图片不存在")
			return
		}
	}

	if err := h.repo.SetCover(userID, projectID, coverURL); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coverUrl": h.signer.SignThumb(coverURL, 768)})
}

// ─── 副本 ────────────────────────────────────────────────────────────────────

// Duplicate 把整个项目复制一份（含快照引用的全部媒体对象）。
//
// 复制是**真复制**不是引用：两个项目各自独立编辑，共享对象的话删掉任一个都会把另一个
// 搞裂。所以副本会真实占用等量存储配额，这里先 EstimateBytes 预检、不够就 fail-fast，
// 免得 CopyObject 跑到一半才失败留下半个项目。
//
// 刻意**不加**每日次数/字节限流：分享克隆有那套是因为它复制的是别人的东西、有滥用面；
// 复制自己的项目，存储配额本身就是天然上限，再加一层只会误伤正常使用。
func (h *ProjectHandler) Duplicate(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())

	src, err := h.repo.GetForUser(userID, projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	snapshot, err := h.repo.GetLatest(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var payload json.RawMessage
	if snapshot != nil {
		payload = snapshot.Payload
	}

	if len(payload) > 0 {
		est, err := h.duplicator.EstimateBytes(payload)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		used, quota, err := h.users.StorageUsage(userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if quota > 0 && used+est > quota {
			writeError(w, http.StatusUnprocessableEntity, "存储空间不足，无法创建副本")
			return
		}
	}

	newID, err := h.repo.Create(userID, src.Name+" - 副本", src.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 空项目（从没保存过快照）没有对象要复制，跳过 duplicator 直接返回空副本。
	if len(payload) > 0 {
		dup, err := h.duplicator.Duplicate(r.Context(), userID, newID, "project_duplicate", payload)
		if err != nil {
			// 尽力回滚刚建的空壳项目；对象层的回收由 duplicator 自己的 GC 队列负责。
			_ = h.repo.Delete(userID, newID)
			writeError(w, http.StatusInternalServerError, "复制素材失败: "+err.Error())
			return
		}
		if _, err := h.repo.SaveAuthoritative(newID, dup.Snapshot); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"projectId": newID})
}
