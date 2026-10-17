package http

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
)

// 列表接口的封面契约：repo 给出 canonical media://，出口必须是签名后的缩略 URL，
// 绝不能把 canonical 形态漏给前端（前端拿它没法展示，也违反「客户端永远拿不到
// 裸对象存储 URL」的铁律）。无封面项目 coverUrl 字段整个省略（omitempty）。
type stubCoverProjectRepo struct {
	projects.Repository // 只实现 ListForUser，其余方法调用即 panic——测试里不该被碰
	list                []projects.Project
}

func (s stubCoverProjectRepo) ListForUser(userID int64, limit, offset int) ([]projects.Project, int, error) {
	return s.list, len(s.list), nil
}

type stubThumbStorage struct {
	assets.Storage
}

func (stubThumbStorage) ExtractKey(s string) (string, bool) {
	if strings.HasPrefix(s, "media://") {
		return strings.TrimPrefix(s, "media://"), true
	}
	return "", false
}

func (stubThumbStorage) SignedThumbURL(_ context.Context, key string, _ time.Duration, maxWidth int) (string, error) {
	return "https://signed.example/" + key + "?w=768", nil
}

func TestProjectListSignsCoverURL(t *testing.T) {
	h := &ProjectHandler{
		repo: stubCoverProjectRepo{list: []projects.Project{
			{ID: 1, Name: "有封面", CoverURL: "media://gen/first.png"},
			{ID: 2, Name: "无封面"},
		}},
		signer: NewMediaSigner(stubThumbStorage{}),
	}
	req := httptest.NewRequest("GET", "/api/v1/projects", nil)
	req = req.WithContext(withUserID(req.Context(), 7))
	rec := httptest.NewRecorder()
	h.List(rec, req)

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got := body.Data[0]["coverUrl"]; got != "https://signed.example/gen/first.png?w=768" {
		t.Fatalf("coverUrl 未签名: %v", got)
	}
	if _, ok := body.Data[1]["coverUrl"]; ok {
		t.Fatalf("无封面项目不应出现 coverUrl 字段: %v", body.Data[1])
	}
}
