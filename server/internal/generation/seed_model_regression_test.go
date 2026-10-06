package generation

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

// loadSeedModel 从仓库根的 seed/model_configs.jsonl 读出真实的内置模型配置，
// 防止测试模板与内置配置脱节（C1 就是这样漏网的）。
func loadSeedModel(t *testing.T, modelID string) *models.ModelConfig {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "seed", "model_configs.jsonl"))
	if err != nil {
		t.Fatalf("open seed: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var row struct {
			ModelID          string          `json:"model_id"`
			BasePath         string          `json:"base_path"`
			RequestTemplate  json.RawMessage `json:"request_template"`
			ImageInputFormat string          `json:"image_input_format"`
			ImageRequestMode string          `json:"image_request_mode"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			continue
		}
		if row.ModelID == modelID {
			return &models.ModelConfig{ModelID: row.ModelID, Enabled: true, BasePath: row.BasePath,
				RequestTemplate: row.RequestTemplate, ImageInputFormat: row.ImageInputFormat,
				ImageRequestMode: row.ImageRequestMode}
		}
	}
	t.Fatalf("seed 里没有 %s", modelID)
	return nil
}

// C1：gpt-image-2 的内置模板把 {{images}} 插进 payload，但 edits 模式走 multipart，
// payload 里的 image 字段根本不会发出去，不能因此被判成本机地址拦掉。
func TestPrepareExecutionSeedGPTImage2EditsWithLocalImage(t *testing.T) {
	for _, id := range []string{"gpt-image-2"} {
		t.Run(id, func(t *testing.T) {
			m := loadSeedModel(t, id)
			if m.ImageRequestMode != models.ImageRequestModeOpenAIImageEdits {
				t.Fatalf("seed 配置变了: mode=%s", m.ImageRequestMode)
			}
			svc := planService(m, okSettings(), "sk")
			svc.tmpDir = t.TempDir()
			plan, err := svc.prepareExecution(context.Background(), 1, id,
				map[string]any{"prompt": "p", "size": "1024x1024", "images": []any{"media://u1/a"}}, "", "", nil)
			if err != nil {
				t.Fatalf("multipart 模型带本地参考图不应被拦: %v", err)
			}
			defer removePreparedFiles(plan.preparedImages)
			if len(plan.preparedImages) != 1 {
				t.Fatalf("应准备一张 multipart 图, got %d", len(plan.preparedImages))
			}
			// 真正发出去的字段里不能有本机地址。
			for k, v := range scalarMultipartFields(plan.payload) {
				if strings.Contains(v, "127.0.0.1") || strings.HasPrefix(v, "media://") {
					t.Fatalf("multipart 字段 %s 带了本机地址: %s", k, v)
				}
			}
		})
	}
}

// 没有参考图时 edits 模型走 JSON（client.Call 发整个 payload），拦截仍要生效于整个 payload。
func TestEnsureNoLocalMediaEditsWithoutPreparedChecksWholePayload(t *testing.T) {
	m := loadSeedModel(t, "gpt-image-2")
	svc := planService(m, okSettings(), "sk")
	err := svc.ensureNoLocalMediaForRequest(m, []byte(`{"prompt":"p","image":["media://u1/a"]}`), nil)
	if err != ErrLocalMediaUnreachable {
		t.Fatalf("JSON 发送路径应拦截, got %v", err)
	}
	// multipart 路径：标量字段里出现本机地址也要拦。
	err = svc.ensureNoLocalMediaForRequest(m, []byte(`{"prompt":"media://u1/a","image":["media://u1/a"]}`),
		[]preparedImageInput{{Path: "x"}})
	if err != ErrLocalMediaUnreachable {
		t.Fatalf("multipart 标量字段带本机地址应拦截, got %v", err)
	}
}
