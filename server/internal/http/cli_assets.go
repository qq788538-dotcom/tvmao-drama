package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/cliapi"
)

// cliAssetRepo 是 CLI 素材接口需要的 assets.Repository 子集。
type cliAssetRepo interface {
	ListForUser(userID int64, limit int) ([]assets.AssetRecord, error)
}

// CLIAssetHandler 是 CLI 的素材上传/查询/下载。
//
// 复用 AssetHandler 的 storeUpload：去重、视频时长探测、写对象、落库、配额记账
// 全部走同一份实现——那段逻辑有顺序敏感的不变量，复制第二份迟早会漂。
type CLIAssetHandler struct {
	assets *AssetHandler
	repo   cliAssetRepo
	signer *MediaSigner
}

func NewCLIAssetHandler(assetHandler *AssetHandler, repo cliAssetRepo, signer *MediaSigner) *CLIAssetHandler {
	return &CLIAssetHandler{assets: assetHandler, repo: repo, signer: signer}
}

// Upload POST /cli/projects/{projectID}/assets（multipart，字段名 file）
func (h *CLIAssetHandler) Upload(w http.ResponseWriter, r *http.Request) {
	projectID, err := cliProjectID(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	userID := userIDFromContext(r.Context())
	// 与网页上传同一上限：readUploadForm 会把整个文件读进内存，不封顶就是任意大小。
	if h.assets.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.assets.maxUploadBytes)
	}

	filename, contentType, body, err := readUploadForm(r)
	if err != nil {
		writeCLIError(w, r, mapUploadError(err))
		return
	}
	res, err := h.assets.storeUpload(r.Context(), userID, projectID, filename, contentType, body)
	if err != nil {
		writeCLIError(w, r, mapUploadError(err))
		return
	}
	status := http.StatusCreated
	if res.Deduped {
		status = http.StatusOK
	}
	writeCLIJSON(w, status, map[string]any{
		"id":       res.ID,
		"url":      h.signer.Sign(res.URL),
		"size":     res.Size,
		"mimeType": res.MimeType,
		"deduped":  res.Deduped,
	})
}

// Get GET /cli/assets/{assetID} —— 校验归属并返回元数据 + 新签名 URL。
func (h *CLIAssetHandler) Get(w http.ResponseWriter, r *http.Request) {
	rec, err := h.lookup(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	writeCLIJSON(w, http.StatusOK, map[string]any{
		"id":       rec.ID,
		"url":      h.signer.Sign(rec.URL),
		"size":     rec.SizeBytes,
		"mimeType": rec.MimeType,
	})
}

// Download GET /cli/assets/{assetID}/download —— 302 到短时签名 URL。
//
// 用重定向而不是由 API 流式转发：转发会让整个文件过一遍 API 进程内存，
// 而签名 URL 本来就是给客户端直连对象存储用的。
func (h *CLIAssetHandler) Download(w http.ResponseWriter, r *http.Request) {
	rec, err := h.lookup(r)
	if err != nil {
		writeCLIError(w, r, err)
		return
	}
	http.Redirect(w, r, h.signer.Sign(rec.URL), http.StatusFound)
}

// lookup 按 id 找素材并校验归属。
//
// 这里用「列当前用户的素材再过滤」而不是按 id 直查：assets.Repository 没有
// 带归属校验的单查方法，加一个属于改现有代码。列表上限 500 对 CLI 够用，
// 超出时会退化成找不到——真需要时再补 repo 方法，那是独立一次改动。
func (h *CLIAssetHandler) lookup(r *http.Request) (*assets.AssetRecord, error) {
	raw := chi.URLParam(r, "assetID")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, cliapi.Errf(cliapi.CodeAssetNotFound, "素材 %s 不存在", raw)
	}
	list, err := h.repo.ListForUser(userIDFromContext(r.Context()), 500)
	if err != nil {
		return nil, cliapi.New(cliapi.CodeInternal, "读取素材失败").WithCause(err)
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	// 「不存在」与「是别人的」返回同一个错误，避免 id 可枚举。
	return nil, cliapi.Errf(cliapi.CodeAssetNotFound, "素材 %d 不存在", id)
}

// mapUploadError 把 uploadError 的 HTTP 状态翻成 CLI 稳定错误码。
func mapUploadError(err error) error {
	var ue *uploadError
	if !errors.As(err, &ue) {
		return cliapi.New(cliapi.CodeInternal, err.Error())
	}
	switch ue.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		// 413 文件超限同属调用方问题：落 INTERNAL 会被 CLI 当成可重试，反复重传同一个大文件。
		return cliapi.New(cliapi.CodeInvalidArgument, ue.Message)
	case http.StatusBadGateway:
		return cliapi.New(cliapi.CodeUpstreamUnavailable, ue.Message)
	case http.StatusConflict:
		// 存储配额预检的 409：用户清理素材即可自解，归 INVALID_ARGUMENT（4xx、
		// 不可重试）。落 default 的 INTERNAL 会被 CLI 当服务端故障自动重试——
		// 空间不清理，重试多少次都是同一个结果。
		return cliapi.New(cliapi.CodeInvalidArgument, ue.Message)
	default:
		return cliapi.New(cliapi.CodeInternal, ue.Message)
	}
}
