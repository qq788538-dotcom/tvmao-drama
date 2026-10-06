package http

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/aicharacters"
	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 内置数据导入后，真实仓库经列表接口返回非空。
func TestAICharacterListNonEmptyAfterSeedImport(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := aicharacters.Import(conn); err != nil {
		t.Fatal(err)
	}
	h := NewAICharacterHandler(aicharacters.NewSQLiteRepository(conn), aiCharFakeSigner{}, aiCharSettings(""))
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/canvas/ai-characters", nil))
	var body struct {
		Items   []aiCharacterDTO `json:"items"`
		HasMore bool             `json:"hasMore"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body.Items) != 50 || !body.HasMore {
		t.Fatalf("code=%d items=%d hasMore=%v", rec.Code, len(body.Items), body.HasMore)
	}
}
