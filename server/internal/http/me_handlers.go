package http

import (
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/users"
)

type meUserRepo interface {
	GetByID(id int64) (*users.User, error)
	ComputeStorageUsed(id int64) (int64, error)
}

// MeHandler 是 /me 的桌面版：没有登录、邀请码、JWT，
// 只回本地用户的资料（前端头像菜单、存储用量直接读它）。
type MeHandler struct {
	users meUserRepo
}

func NewMeHandler(users meUserRepo) *MeHandler { return &MeHandler{users: users} }

func (h *MeHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	user, err := h.users.GetByID(userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not found")
		return
	}
	// 存储用量从 assets + library_items 实时计算（权威），而不是用那个增量维护、会漂移的计数器。
	storageUsed := user.StorageUsedBytes
	if v, cerr := h.users.ComputeStorageUsed(userID); cerr == nil {
		storageUsed = v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                user.ID,
		"email":             user.Email,
		"role":              user.Role,
		"balanceCredits":    user.BalanceCredits,
		"storageQuotaBytes": user.StorageQuotaBytes,
		"storageUsedBytes":  storageUsed,
	})
}
