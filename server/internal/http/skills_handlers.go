// apps/api/internal/http/skills_handlers.go
package http

import (
	"net/http"
	"strings"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/agent"
)

// SkillsHandler 服务 /api/v1/skills 端点——内置的官方 Skill 目录（只读）。
type SkillsHandler struct {
	// Categories 给 web SkillPopover 提供动态分类字典（顺序+颜色）——分类曾写死
	// 在前端常量里，改成随接口下发。
	CategoriesRepo agent.SkillCategoriesRepo
	Repo           agent.SkillsRepo
	// signer 把后台上传的图标（icon_url 存 `media://key`）换成短时签名 URL；
	// 外链 icon_url 原样透传（Sign 对非本站媒体是 no-op）。
	signer skillIconSigner
}

// skillIconSigner 是 Skill 图标出口需要的两种签名（*MediaSigner 满足）。
type skillIconSigner interface {
	Sign(s string) string
	SignThumb(s string, maxWidth int) string
}

func NewSkillsHandler(repo agent.SkillsRepo, categories agent.SkillCategoriesRepo, signer skillIconSigner) *SkillsHandler {
	return &SkillsHandler{Repo: repo, CategoriesRepo: categories, signer: signer}
}

// Categories 返回有序分类字典（含颜色），SkillPopover 按它渲染分组与配色。
func (h *SkillsHandler) Categories(w http.ResponseWriter, _ *http.Request) {
	if h.CategoriesRepo == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []agent.SkillCategory{}})
		return
	}
	rows, err := h.CategoriesRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Featured 处理 `GET /api/v1/skills/featured`——首页故事灵感入口下方的首推行，
// **公开免鉴权**（匿名访客也要看到首页）。只回 chip 需要的字段：图标走
// SignThumb 出小缩略图（chip 里只有 20px），system_prompt_addon 清空——提示词
// 不该下发给匿名访客；画布侧发送只带 skill_id，服务端自己按 id 取 addon。
func (h *SkillsHandler) Featured(w http.ResponseWriter, _ *http.Request) {
	rows, err := h.Repo.ListFeatured()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agent.Skill, 0, len(rows))
	for _, s := range rows {
		s.IconURL = h.signer.SignThumb(s.IconURL, featuredIconThumbWidth)
		s.SystemPromptAddon = ""
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// featuredIconThumbWidth 是首推 chip 图标的缩略宽。chip 里图标只有 20px，
// 64 已够 3x 屏；上传原图可到 1MB，直接下发原图是白流量。
const featuredIconThumbWidth = 64

// List 处理 `GET /api/v1/skills?category=&q=`。返回公开 skill 加上调用方
// 自己的（对作者本人包含草稿）。category 和 q 是可选的客户端过滤，在 repo
// 取数之后应用——让 repo 签名保持精简，同时仍允许 UI 做自由文本 +
// category 范围筛选。
func (h *SkillsHandler) List(w http.ResponseWriter, r *http.Request) {
	uid := agentUserID(r)
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	rows, err := h.Repo.ListForUser(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agent.Skill, 0, len(rows))
	for _, s := range rows {
		if category != "" && !skillHasCategory(s, category) {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(s.Name), q) &&
			!strings.Contains(strings.ToLower(s.Description), q) {
			continue
		}
		s.IconURL = h.signer.Sign(s.IconURL)
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// skillHasCategory：多分类命中判断（Categories 为真源，兼容只有单值的旧行）。
func skillHasCategory(s agent.Skill, category string) bool {
	if s.Category == category {
		return true
	}
	for _, c := range s.Categories {
		if c == category {
			return true
		}
	}
	return false
}
