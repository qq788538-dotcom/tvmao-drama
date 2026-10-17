package http

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/aicharacters"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

// 内置数据导入后，真实仓库经列表接口返回非空。
func TestAICharacterListNonEmptyAfterSeedImport(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := aicharacters.Import(conn)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Skip("未放入 AI 角色库数据包，见 seed/README.md")
	}
	h := NewAICharacterHandler(aicharacters.NewSQLiteRepository(conn), aiCharFakeSigner{})
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
