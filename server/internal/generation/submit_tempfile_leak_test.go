package generation_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/generation"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

type editsModelLookup struct{}

func (editsModelLookup) GetByModelID(modelID string) (*models.ModelConfig, error) {
	raw, _ := json.Marshal(map[string]any{"flatFee": 10})
	return &models.ModelConfig{
		ModelID:          modelID,
		Enabled:          true,
		BillingConfigRaw: raw,
		ImageRequestMode: models.ImageRequestModeOpenAIImageEdits,
		ImageInputFormat: models.ImageInputFormatURL,
		BasePath:         "/v1/images/generations",
		RequestTemplate:  json.RawMessage(`{"prompt":"{{prompt}}","image":"{{images}}"}`),
		UpstreamBaseURL:  "http://127.0.0.1:1",
		UpstreamAPIKey:   "sk-test",
	}, nil
}

// 直连提交在参考图已落盘之后、交给 callAndComplete 之前失败（这里是冻结失败）时，
// 参考图临时文件必须被删掉——曾经只有 ensureNoLocalMedia 失败这一条出口会清，
// 建行/冻结/提交失败都把文件永久留在临时目录里（只在下次启动按 1h 时效清）。
func TestSubmitDirect_FreezeFailureRemovesSpooledImages(t *testing.T) {
	conn := dbtest.Open(t)
	svc := generation.NewService(
		generation.NewSQLiteRepository(conn), editsModelLookup{}, nil, nil,
		txTestBilling{freezeErr: billing.ErrInsufficientBalance, freezes: &[]int64{}},
		nil, nil, nil, nil, nil, conn,
	)
	dir := t.TempDir()
	svc.SetTempDir(dir)

	img := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))
	_, err := svc.Submit(context.Background(), generation.SubmitInput{
		UserID: uid, ModelID: "m-edits",
		Inputs: map[string]any{"prompt": "改一下", "images": []any{img}},
	})
	if !errors.Is(err, billing.ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("冻结失败后临时目录应为空，残留: %v", names)
	}
}
