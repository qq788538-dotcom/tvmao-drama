package http

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/mediaprobe"
)

// uniqueUploadFilename 给上传文件名加随机前缀，避免同用户同名不同内容文件互相覆盖
// （NewKey 仅按 u<id>/年/月/文件名 拼 key，不带唯一性；哈希去重只防同内容重复）。
func uniqueUploadFilename(filename string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return filename
	}
	return hex.EncodeToString(b) + "_" + filename
}

// assetMaxUploadBytes 是单个上传文件的上限。参考仓靠 nginx 的 body 上限挡；sidecar 前面没有
// nginx，而下面 readUploadForm 会把整个文件读进内存，必须在 handler 里限住。
const assetMaxUploadBytes int64 = 1 << 30

type AssetHandler struct {
	storage assets.Storage
	repo    assets.Repository
	users   storageUserRepo
	signer  *MediaSigner
	// probe 探测上传视频的时长（nil = 不探测）。默认 mediaprobe.ProbeDurationMsPortable：有 ffprobe 就数真实帧，
	// 没有就读 MP4 容器时长（桌面端不计费，spec X3）。记下 duration_ms 后，后处理就不用再下载探测；
	// 探测失败只是不记时长。
	probe func(ctx context.Context, data []byte) (int64, error)
	// maxUploadBytes 是单个文件的上限，默认 assetMaxUploadBytes；测试可调小。
	maxUploadBytes int64
}

func NewAssetHandler(storage assets.Storage, repo assets.Repository, users storageUserRepo, signer *MediaSigner) *AssetHandler {
	return &AssetHandler{storage: storage, repo: repo, users: users, signer: signer, maxUploadBytes: assetMaxUploadBytes, probe: mediaprobe.ProbeDurationMsPortable}
}

// uploadResult 是一次上传的结果。Deduped=true 表示命中同用户同项目的相同内容，
// 没有真正写对象存储。
type uploadResult struct {
	ID       int64
	URL      string // 未签名的存储 URL；调用方按各自契约决定怎么签
	Size     int64
	MimeType string
	Deduped  bool
}

// uploadError 让调用方能把失败映射到自己的错误契约（网页 writeError / CLI 错误码），
// 而不是被这一层的响应格式绑死。
type uploadError struct {
	Status  int
	Message string
}

func (e *uploadError) Error() string { return e.Message }

// storeUpload 是上传的核心：去重 → 时长探测 → 写对象 → 落库 → 记账。
//
// 抽成独立方法是为了让网页与 CLI **共用同一份**。这段逻辑里有几个顺序敏感的
// 不变量（探测必须在 PutObject 之前、去重必须在探测之前），复制第二份迟早会漂。
// 网页 handler 的响应体逐字未变。
func (h *AssetHandler) storeUpload(ctx context.Context, userID, projectID int64, filename, contentType string, body []byte) (uploadResult, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// 按 (user, project) 维度去重。同一用户在同一项目里重新丢同一个文件
	//（典型场景：关掉对话框后又拖了一次）会返回已有的 asset row——不写 TOS，
	// 不额外扣额度。跨项目 / 跨用户去重需要 CAS 引用计数，否则会破坏
	// 项目存储记账和删除项目时的清理，所以这里不做。
	sum := sha256.Sum256(body)
	contentHash := hex.EncodeToString(sum[:])
	if existing, lookupErr := h.repo.FindByUserProjectHash(userID, projectID, contentHash); lookupErr == nil && existing != nil {
		return uploadResult{
			ID: existing.ID, URL: existing.URL, Size: existing.SizeBytes,
			MimeType: existing.MimeType, Deduped: true,
		}, nil
	} else if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return uploadResult{}, &uploadError{Status: http.StatusInternalServerError, Message: lookupErr.Error()}
	}

	// 配额预检必须在去重判定之后：dedup 命中复用已有对象、不新占存储，
	// 被配额拦下毫无道理（用户重复拖同一个文件反而看到「空间已满」）。
	// 又必须在 ffprobe/PutObject 之前：注定被拒的请求不该白花探测和存储写。
	// 这次读取与下面 AddStorage 之间存在 TOCTOU 窗口——可接受，口径同
	// library 上传闸：并发上传最多让用户超出几 MB，每个请求都要过这道关卡，
	// 不会无限超出。
	used, quota, qerr := h.users.StorageUsage(userID)
	if qerr != nil {
		return uploadResult{}, &uploadError{Status: http.StatusInternalServerError, Message: qerr.Error()}
	}
	if used+int64(len(body)) > quota {
		return uploadResult{}, &uploadError{Status: http.StatusConflict, Message: "存储空间已满，请清理后重试"}
	}

	// 时长探测改为尽力而为：探测失败只是不记时长（duration_ms 为 NULL），不再拒绝上传——
	// 参考仓拒绝无权威时长的视频是为了按秒计费不被篡改的元数据骗，桌面端不计费（spec X3）。
	// 探测仍放在 PutObject 之前，保持参考仓的顺序不变式。
	var durationMs int64
	if strings.HasPrefix(contentType, "video/") && h.probe != nil {
		if d, perr := h.probe(ctx, body); perr == nil {
			durationMs = d
		}
	}

	key := h.storage.NewKey(userID, uniqueUploadFilename(filename))
	stored, err := h.storage.PutObject(ctx, assets.PutObjectInput{
		Key:         key,
		ContentType: contentType,
		Body:        body,
	})
	if err != nil {
		return uploadResult{}, &uploadError{Status: http.StatusBadGateway, Message: err.Error()}
	}

	id, err := h.repo.Insert(assets.AssetRecord{
		UserID:      userID,
		ProjectID:   projectID,
		ObjectKey:   key,
		SourceKind:  "upload",
		URL:         stored.URL,
		SizeBytes:   stored.Size,
		MimeType:    contentType,
		ContentHash: contentHash,
		DurationMs:  durationMs,
	})
	if err != nil {
		return uploadResult{}, &uploadError{Status: http.StatusInternalServerError, Message: err.Error()}
	}
	_ = h.users.AddStorage(userID, stored.Size)

	return uploadResult{ID: id, URL: stored.URL, Size: stored.Size, MimeType: contentType}, nil
}

// readUploadForm 从 multipart 请求里取出文件。
func readUploadForm(r *http.Request) (filename, contentType string, body []byte, err error) {
	if perr := r.ParseMultipartForm(64 << 20); perr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(perr, &tooLarge) || strings.Contains(perr.Error(), "request body too large") {
			return "", "", nil, &uploadError{Status: http.StatusRequestEntityTooLarge, Message: "文件过大，单个文件不能超过 1GB"}
		}
		return "", "", nil, &uploadError{Status: http.StatusBadRequest, Message: "invalid multipart form"}
	}
	file, header, ferr := r.FormFile("file")
	if ferr != nil {
		return "", "", nil, &uploadError{Status: http.StatusBadRequest, Message: "file required"}
	}
	defer file.Close()

	blob, rerr := io.ReadAll(file)
	if rerr != nil {
		return "", "", nil, &uploadError{Status: http.StatusInternalServerError, Message: rerr.Error()}
	}
	return header.Filename, header.Header.Get("Content-Type"), blob, nil
}

func (h *AssetHandler) Upload(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if h.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadBytes)
	}

	filename, contentType, body, err := readUploadForm(r)
	if err != nil {
		var ue *uploadError
		if errors.As(err, &ue) {
			writeError(w, ue.Status, ue.Message)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// projectId 是可选的——存在于项目里的节点会带上它，这样删除项目时的
	// 清理逻辑能回收存储 + 删掉对象。
	var projectID int64
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		if v, perr := strconv.ParseInt(raw, 10, 64); perr == nil && v > 0 {
			projectID = v
		}
	}

	res, err := h.storeUpload(r.Context(), userID, projectID, filename, contentType, body)
	if err != nil {
		var ue *uploadError
		if errors.As(err, &ue) {
			writeError(w, ue.Status, ue.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if res.Deduped {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":       res.ID,
			"url":      h.signer.Sign(res.URL),
			"size":     res.Size,
			"mimeType": res.MimeType,
			"deduped":  true,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       res.ID,
		"url":      h.signer.Sign(res.URL),
		"size":     res.Size,
		"mimeType": res.MimeType,
	})
}

func (h *AssetHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	list, err := h.repo.ListForUser(userID, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range list {
		list[i].URL = h.signer.Sign(list[i].URL)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}
