package http

import (
	"net/http"
	"strconv"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/aicharacters"
)

// aiCharacterSigner 是 *MediaSigner 在这里用到的两个方法。
type aiCharacterSigner interface {
	Sign(string) string
	SignThumb(string, int) string
}

// AICharacterHandler 暴露自由画布的「AI 角色库」列表。只读——这批角色是平台
// 预置数据（内置种子导入），没有任何写入口。形象图经本地 /media 提供，本地没有时
// 由 aicharacters.ImageCache 从 sidecar 内置资源复制过去。
type AICharacterHandler struct {
	repo   aicharacters.Repository
	signer aiCharacterSigner
}

func NewAICharacterHandler(repo aicharacters.Repository, signer aiCharacterSigner) *AICharacterHandler {
	return &AICharacterHandler{repo: repo, signer: signer}
}

// aiCharacterListLimit 是单页上限，也是前端滚动加载的批大小。
const aiCharacterListLimit = 50

type aiCharacterDTO struct {
	AssetID     string `json:"assetId"`
	Age         int    `json:"age"`
	Gender      string `json:"gender"`
	Occupation  string `json:"occupation"`
	Description string `json:"description"`
	AgeGroup    string `json:"ageGroup"`
	// ImageURL 是签名后的本地 URL——详情弹框展示、「应用到画布」的节点内容都用它
	//（节点持久化时会被 canonical 化回 media:// 形式）。ThumbURL 给网格卡片用；
	// 桌面端不生成缩略图，与 ImageURL 相同。
	ImageURL string `json:"imageUrl"`
	ThumbURL string `json:"thumbUrl"`
}

// List: GET /canvas/ai-characters?search=&gender=&ageGroup=&offset=
// 无效的过滤值当作不过滤——过滤枚举将来若扩充，老前端传新值也不至于 400。
func (h *AICharacterHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := aicharacters.ListParams{
		Search: q.Get("search"),
		Offset: 0,
		// 多取一条探测还有没有下一页，响应里只回 limit 条。
		Limit: aiCharacterListLimit + 1,
	}
	if g := q.Get("gender"); g == "男" || g == "女" {
		p.Gender = g
	}
	if a := q.Get("ageGroup"); a == "青年" || a == "中年" || a == "老年" {
		p.AgeGroup = a
	}
	if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		p.Offset = off
	}

	list, err := h.repo.List(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasMore := len(list) > aiCharacterListLimit
	if hasMore {
		list = list[:aiCharacterListLimit]
	}
	items := make([]aiCharacterDTO, 0, len(list))
	for _, c := range list {
		items = append(items, aiCharacterDTO{
			AssetID:     c.AssetID,
			Age:         c.Age,
			Gender:      c.Gender,
			Occupation:  c.Occupation,
			Description: c.Description,
			AgeGroup:    c.AgeGroup,
			ImageURL:    h.signer.Sign(c.ImageURL),
			ThumbURL:    h.signer.SignThumb(c.ImageURL, 400),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"hasMore": hasMore,
		// 形象图已内置，恒为 true；字段保留是为了前端「图片未配置」提示的契约不变。
		"imageConfigured": true,
	})
}
