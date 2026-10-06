package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/directorlibrary"
)

// directorSigner 是 *MediaSigner 在导演台两个 handler 里用到的方法（P1b 提供实现）。
type directorSigner interface {
	Sign(string) string
	SignThumb(string, int) string
}

// directorMediaProtocol 与 assets.MediaProtocol 同值：导演台两张表只存裸 object_key，出图前补前缀再签名。
const directorMediaProtocol = "media://"

// DirectorLibraryHandler 是「3D 导演台·后台全局模型库」的用户端 HTTP 层：
// 只有一个只读目录接口。写路径（参考仓的 AdminDirectorLibraryHandler）桌面端不移植，
// 内置库为空。
type DirectorLibraryHandler struct {
	repo   directorlibrary.Repository
	signer directorSigner
}

func NewDirectorLibraryHandler(repo directorlibrary.Repository, signer directorSigner) *DirectorLibraryHandler {
	return &DirectorLibraryHandler{repo: repo, signer: signer}
}

// List GET /director/library：enabled 分类带各自 enabled 模型，都按 sort_order,id
// 排序。url/thumbUrl 每次现签——DB 只存 object_key；无缩略图时 thumbUrl 固定空串
// （前端契约按空串判断兜底占位图，不能省字段）。
func (h *DirectorLibraryHandler) List(w http.ResponseWriter, r *http.Request) {
	cats, err := h.repo.ListEnabledJoined()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		models := make([]map[string]any, 0, len(c.Models))
		for _, m := range c.Models {
			thumbURL := ""
			if m.ThumbObjectKey != "" {
				thumbURL = h.signer.SignThumb(directorMediaProtocol+m.ThumbObjectKey, 400)
			}
			models = append(models, map[string]any{
				"id":       m.ID,
				"name":     m.Name,
				"fileName": m.FileName,
				"url":      h.signer.Sign(directorMediaProtocol + m.ObjectKey),
				"thumbUrl": thumbURL,
			})
		}
		out = append(out, map[string]any{
			"id":     c.ID,
			"name":   c.Name,
			"models": models,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}
