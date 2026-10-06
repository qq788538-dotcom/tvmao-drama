package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/library"
)

// SeedanceHandler 暴露「画布素材（图片/视频/音频）的 Seedance 2.0 合规校验」。
// 校验状态挂在 object_key 上（见 0075），所以这里只做「asset id → object_key +
// 归属校验」的转换，剩下交给 service。
// seedanceKeyExtractor 是 assets.Storage 在这里用到的唯一方法（P1b 的 LocalStorage 认
// media:// 与任意端口的本地媒体 URL）。
type seedanceKeyExtractor interface {
	ExtractKey(urlOrKey string) (string, bool)
}

type SeedanceHandler struct {
	svc     *library.SeedanceService
	storage seedanceKeyExtractor
	db      *sql.DB
}

func NewSeedanceHandler(svc *library.SeedanceService, storage seedanceKeyExtractor, db *sql.DB) *SeedanceHandler {
	return &SeedanceHandler{svc: svc, storage: storage, db: db}
}

// objectKeyOfURL 把节点手里的媒体 URL 还原成 object_key，并确认这个对象属于该用户。
//
// 为什么以 URL 而不是 assetId 为入口：assetId 是内部 id，要靠前端在生成那一刻存进
// 节点 settings——只要那一刻没存上（历史节点、agent 产出的节点、从素材库应用过来的
// 节点），这个节点就永远用不了这些功能。而 URL 是节点始终持有的东西，object_key
// 又是图片在本系统里的真正身份，从 URL 还原它是确定性的。
func (h *SeedanceHandler) objectKeyOfURL(userID int64, mediaURL string) (string, error) {
	if h.storage == nil || mediaURL == "" {
		return "", errMediaNotResolvable
	}
	key, ok := h.storage.ExtractKey(mediaURL)
	if !ok || key == "" {
		return "", errMediaNotResolvable
	}
	// 归属校验：这个 object_key 得在该用户名下有 asset 或素材库记录。
	var n int
	if err := h.db.QueryRow(`
        SELECT (SELECT COUNT(*) FROM assets        WHERE user_id=? AND object_key=?)
             + (SELECT COUNT(*) FROM library_items WHERE user_id=? AND object_key=?)`,
		userID, key, userID, key).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "", errMediaNotYours
	}
	return key, nil
}

var (
	errMediaNotResolvable = errors.New("无法识别这个素材，请重新生成或上传")
	errMediaNotYours      = errors.New("media not yours")
)

type seedanceVerifyPayload struct {
	URL string `json:"url"`
	// Kind 是媒体类型（image/video/audio），决定上游按哪套规格校验。
	// 由前端给——节点自己知道它挂的是什么；这里不从 object_key 后缀猜（生成产物
	// 的 key 不保证带扩展名）。缺省视作 image，兼容老前端。
	Kind string `json:"kind"`
}

// Verify: POST /seedance/verify —— body {url, kind}。
// 幂等——已通过且版本没变的直接回原状态，不重复占用上游配额。
func (h *SeedanceHandler) Verify(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var input seedanceVerifyPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	assetType, ok := library.NormalizeAssetType(input.Kind)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported kind")
		return
	}
	key, err := h.objectKeyOfURL(userID, input.URL)
	switch {
	case errors.Is(err, errMediaNotResolvable):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, errMediaNotYours):
		writeError(w, http.StatusForbidden, "media not yours")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec, err := h.svc.Submit(r.Context(), userID, key, assetType)
	if err != nil {
		if errors.Is(err, library.ErrSeedanceDisabled) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		// 上游拒绝（含不合规）是用户可理解的失败，不是服务器故障——给 400 让前端
		// 直接把原因显示出来，而不是弹一个「服务器错误」。
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, rec)
}

type seedanceStatusPayload struct {
	URLs []string `json:"urls"`
}

// Status: POST /seedance/status —— body {urls}，回 {url: 状态}。
// 没校验过的 URL 不出现在结果里（前端按「无记录=未校验」处理）。
// 这里不做归属校验：Resolve 本身就只认自己的记录，别人的 key 查不出东西来。
func (h *SeedanceHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var input seedanceStatusPayload
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	out := map[string]library.SeedanceAsset{}
	if len(input.URLs) == 0 || h.storage == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	keyByURL := map[string]string{}
	keys := make([]string, 0, len(input.URLs))
	for _, u := range input.URLs {
		if k, ok := h.storage.ExtractKey(u); ok && k != "" {
			keyByURL[u] = k
			keys = append(keys, k)
		}
	}
	byKey, err := h.svc.Status(userID, keys)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 回给前端时用 URL 做键——节点手里就是 URL，不该让它知道 object_key。
	for u, k := range keyByURL {
		if a, ok := byKey[k]; ok {
			out[u] = a
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Config: GET /seedance/config —— 前端据此在「合规校验」入口提示「合规接口未配置」（spec X5）。
func (h *SeedanceHandler) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"configured": h.svc.Configured()})
}
