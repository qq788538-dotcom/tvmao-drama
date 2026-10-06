package generation

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/upstream"
)

// 端到端：参考图源 → 落盘 → multipart 发上游 → 响应 b64 抽成文件 → 流式上传存储。
// 验收不是「能跑通」,是参考图/产物字节逐字往返 + 调用结束临时目录零残留。
func TestEditsEndToEnd_BytesRoundTripAndTempDirClean(t *testing.T) {
	refPNG := append([]byte("\x89PNG\r\n\x1a\n"), []byte(strings.Repeat("r", 5000))...)
	outPNG := append([]byte("\x89PNG\r\n\x1a\n"), []byte(strings.Repeat("o", 7000))...)
	var gotUpload []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ref.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(refPNG)
		case "/v1/images/edits":
			_ = r.ParseMultipartForm(1 << 20)
			f, _ := r.MultipartForm.File["image"][0].Open()
			gotUpload, _ = io.ReadAll(f)
			f.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(outPNG) + `"}],"usage":{"total_tokens":9}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	st := &captureStorage{}
	s := &Service{storage: st, httpClient: srv.Client()}
	s.SetTempDir(tmp)

	inputs := map[string]any{"prompt": "p", "images": []any{srv.URL + "/ref.png"}}
	_, prepared, err := prepareImageInputs(context.Background(), inputs, models.ImageInputFormatURL, true, s.fetchInputImage, s.spoolInputImage)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	model := &models.ModelConfig{ImageRequestMode: models.ImageRequestModeOpenAIImageEdits, BasePath: "/v1/images/generations"}
	client := upstream.NewClient(upstream.ClientOptions{BaseURL: srv.URL, TempDir: tmp})
	res, err := s.callUpstream(context.Background(), client, "sk", model, []byte(`{"prompt":"p"}`), prepared)
	if err != nil {
		t.Fatalf("callUpstream: %v", err)
	}
	if string(gotUpload) != string(refPNG) {
		t.Fatal("上游收到的参考图字节应与源逐字相同")
	}
	if len(res.OutputURLs) != 1 || !strings.HasPrefix(res.OutputURLs[0], "file://") {
		t.Fatalf("OutputURLs = %#v", res.OutputURLs)
	}
	if res.Usage == nil || res.Usage.TotalTokens != 9 {
		t.Fatalf("计费 usage 应从剩余 JSON 正常抽取: %#v", res.Usage)
	}
	if strings.Contains(string(res.Body), "b64_json\":\"i") {
		t.Fatal("result.Body 不应再含整段 b64")
	}
	urls, _, mimes, err := s.persistOutputs(context.Background(), 64, 1, nil, res.OutputURLs, 0)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if string(st.got["u/gen/task-1-0"]) != string(outPNG) || mimes[0] != "image/png" || !strings.HasPrefix(urls[0], "media://") {
		t.Fatalf("产物字节/mime/URL 不符: mime=%s url=%s", mimes[0], urls[0])
	}
	res.RemoveSpillFiles()
	removePreparedFiles(prepared)
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Fatalf("临时目录应为空, 残留: %d", len(entries))
	}
}
