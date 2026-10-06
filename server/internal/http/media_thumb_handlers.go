package http

import (
	"encoding/json"
	"net/http"
)

// thumbBatchMax 限制单次批量换取的 URL 数。画布节点数在数百量级即已卡顿，
// 500 足够一次画布加载全量换完，同时防止该端点被当成放大器打。
const thumbBatchMax = 500

// thumbMaxWidthCap 是允许请求的最大缩略宽。超过这个宽度缩略图失去意义
// （不如直接用原图），也避免把任意 resize 参数透传给对象存储。
const thumbMaxWidthCap = 2048

// thumbDefaultWidth 是调用方不指定时的缺省缩略宽。画布图片节点默认渲染
// 宽约 240-360 CSS px。400 刚好覆盖 1x 屏最大节点宽（200 在 2x 屏明显发虚，
// 768 体积×4 清晰度边际收益小），且与 drama 侧 SignThumb(400) 规格一致。
const thumbDefaultWidth = 400

// MediaThumbs 返回 POST /media/thumbs 的 handler：把画布持有的原图签名 URL
// 批量换成按宽缩放的缩略图签名 URL，key 为请求原文，便于前端按 URL 建缓存。
//
// 仅供展示层使用——生成 inputs、Lightbox、下载仍然走原图 URL；缩略图的
// x-tos-process 参数必须参与预签名，前端无法自行拼接，所以需要这个端点。
// 非我方 bucket 的 URL（外部图、data:）原样返回；MinIO 后端无图片处理，
// SignThumb 会退回原图签名，行为自动降级。
func MediaThumbs(signer *MediaSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URLs     []string `json:"urls"`
			MaxWidth int      `json:"maxWidth"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if len(body.URLs) > thumbBatchMax {
			writeError(w, http.StatusBadRequest, "too many urls")
			return
		}
		width := body.MaxWidth
		if width <= 0 {
			width = thumbDefaultWidth
		}
		if width > thumbMaxWidthCap {
			width = thumbMaxWidthCap
		}
		thumbs := make(map[string]string, len(body.URLs))
		for _, u := range body.URLs {
			if u == "" {
				continue
			}
			thumbs[u] = signer.SignThumb(u, width)
		}
		writeJSON(w, http.StatusOK, map[string]any{"thumbs": thumbs})
	}
}
