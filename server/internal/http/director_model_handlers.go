package http

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/directormodels"
	"github.com/james/huabu_studio/apps/api/internal/library"
)

// DirectorModelHandler 是「3D 导演台·用户级云端模型库」的 HTTP 层：
// 上传 FBX/OBJ/GLB 模型、列表（签名 URL）、删除（带画布快照引用检查）。
// 模型是用户级（跨画布项目共享）资源，条数上限是常量
// directormodels.UserModelLimit（用户拍板硬编码，不走 settings）。
type DirectorModelHandler struct {
	svc     *directormodels.Service
	storage assets.Storage
	orphans library.OrphansRepository
	users   storageUserRepo
	signer  directorSigner
	db      *sql.DB
}

func NewDirectorModelHandler(
	svc *directormodels.Service,
	storage assets.Storage,
	orphans library.OrphansRepository,
	users storageUserRepo,
	signer directorSigner,
	db *sql.DB,
) *DirectorModelHandler {
	return &DirectorModelHandler{svc: svc, storage: storage, orphans: orphans, users: users, signer: signer, db: db}
}

// modelJSON 统一列表与上传响应里单个模型的形状（与前端契约对齐）。
// url 每次现签——DB 只存 object_key，客户端永远拿不到裸存储 URL。
func (h *DirectorModelHandler) modelJSON(m directormodels.Model, withCreatedAt bool) map[string]any {
	out := map[string]any{
		"id":        m.ID,
		"name":      m.Name,
		"fileName":  m.FileName,
		"url":       h.signer.Sign(directorMediaProtocol + m.ObjectKey),
		"sizeBytes": m.SizeBytes,
	}
	if withCreatedAt {
		out["createdAt"] = m.CreatedAt
	}
	return out
}

// directorModelMaxRequestBytes = 30MB 文件上限 + 2MB multipart 边界/头开销。
const directorModelMaxRequestBytes = directormodels.MaxFileSizeBytes + 2<<20

func (h *DirectorModelHandler) Upload(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())

	// 读表单之前先封顶：30MB 文件 + multipart 开销。ParseMultipartForm 超过内存
	// 阈值会落临时文件，io.ReadAll 只读到上限，因此内存占用有界。
	r.Body = http.MaxBytesReader(w, r.Body, directorModelMaxRequestBytes)

	filename, contentType, body, err := readUploadForm(r)
	if err != nil {
		var ue *uploadError
		if errors.As(err, &ue) {
			if ue.Status == http.StatusRequestEntityTooLarge {
				ue.Message = "模型文件不能超过 30MB"
			}
			writeError(w, ue.Status, ue.Message)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 扩展名 + 30MB 上限：静态校验先做，别为注定拒绝的请求去查 DB。
	if verr := directormodels.ValidateUpload(filename, int64(len(body))); verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	limit, ok, err := h.svc.CanCreate(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		// 契约固定形状 {"error":"limit_reached","limit":n}：前端按 error 字段
		// 分支提示并展示当前上限，所以不能走 writeError 的纯文案通道。
		writeJSON(w, http.StatusConflict, map[string]any{"error": "limit_reached", "limit": limit})
		return
	}

	// 存储配额预检（与素材/素材库上传同一道闸，TOCTOU 窗口同样可接受——每个
	// 请求都要过关卡，并发最多短暂超出一个文件的量）。注意与上面条数上限的
	// 409 区分：那个 body 是 {"error":"limit_reached",...}，这里是中文文案，
	// 前端按 error 字段的值分支提示，两种 409 不会混淆。
	used, quota, err := h.users.StorageUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if used+int64(len(body)) > quota {
		writeError(w, http.StatusConflict, "存储空间已满，请清理后重试")
		return
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}
	key := h.storage.NewKey(userID, uniqueUploadFilename(filename))
	stored, err := h.storage.PutObject(r.Context(), assets.PutObjectInput{
		Key: key, ContentType: contentType, Body: body,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	id, err := h.svc.Repo.Insert(directormodels.Model{
		UserID:    userID,
		Name:      name,
		FileName:  filename,
		ObjectKey: key,
		SizeBytes: stored.Size,
		MimeType:  contentType,
	})
	if err != nil {
		// 落库失败必须回滚对象：本表专属 key 不出现在 assets/library_items，
		// 现有基于表扫描的孤儿 GC 兜不住它，留下就是永久裸对象。
		if delErr := h.storage.DeleteObject(r.Context(), key); delErr != nil {
			log.Printf("director model upload: rollback DeleteObject %s failed: %v", key, delErr)
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 配额记账语义对齐素材上传（asset_handlers.storeUpload）：入账失败不回滚
	// 业务数据，只是账面少记。
	_ = h.users.AddStorage(userID, stored.Size)

	writeJSON(w, http.StatusCreated, h.modelJSON(directormodels.Model{
		ID: id, Name: name, FileName: filename, ObjectKey: key, SizeBytes: stored.Size,
	}, false))
}

func (h *DirectorModelHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	list, err := h.svc.Repo.ListByUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, m := range list {
		items = append(items, h.modelJSON(m, true))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *DirectorModelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "modelID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid model id")
		return
	}

	m, err := h.svc.Repo.GetByID(id)
	if err != nil {
		if errors.Is(err, directormodels.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 归属不符也报 404 而不是 403：不给探测别人模型 id 存在性的口子。
	if m.UserID != userID {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// 引用检查：模型被画布快照以 media://<object_key> 字符串引用，删了对象
	// 那些项目的 3D 导演台就断链。默认拦下并告知引用项目数，前端确认后带
	// force=1 强删。
	if r.URL.Query().Get("force") != "1" {
		n, err := h.svc.Repo.CountProjectsReferencing(userID, m.ObjectKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "referenced", "referencedProjects": n})
			return
		}
	}

	// 删行 + 标 orphan 必须同事务：只删行不标 orphan 就是永久裸对象，
	// 只标 orphan 不删行则 7 天后 GC 会删掉仍在目录里的模型的对象。
	// 不用 library.TryReclaim——它的引用检查查的是 assets ∪ library_items，
	// 本表的 object_key 不会出现在那两张表里，查了必然「无引用」，白跑一趟；
	// 直接 Mark 进孤儿管道（7 天宽限由现有 GC sweeper 收）。
	tx, err := h.db.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	if err := h.svc.Repo.Delete(tx, userID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.orphans.Mark(tx, m.ObjectKey, userID, m.SizeBytes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 配额扣减语义对齐素材库删除（library_item_handlers.Delete）：行删掉、对象
	// 进入孤儿管道即视为释放，commit 后扣账。素材库那边要先查 IsStillReferenced
	// 才扣（引用行不持有配额）；本表一行独占一个对象、无共享引用，直接扣。
	if m.SizeBytes > 0 {
		_ = h.users.AddStorage(userID, -m.SizeBytes)
	}
	w.WriteHeader(http.StatusNoContent)
}
