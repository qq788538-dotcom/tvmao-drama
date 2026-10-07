package generation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

// MediaPublisher 把本机媒体发布到用户配置的对象存储并返回上游可下载的地址
// （对象存储设计 §3.2；*objectstore.Publisher 满足）。
type MediaPublisher interface {
	Configured() bool
	Publish(ctx context.Context, mediaKey string) (string, error)
}

// SetMediaPublisher 注入对象存储发布器（setter 惯例同 SetTempDir）。nil = 参考仓原逻辑。
func (s *Service) SetMediaPublisher(p MediaPublisher) { s.publisher = p }

// skipImagesFor：图片会以内容本身发给上游（data_uri / base64 / edits multipart）时不必上传。
func skipImagesFor(m *models.ModelConfig) bool {
	return m != nil && (m.ImageInputFormat != models.ImageInputFormatURL || m.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits)
}

// ErrLocalMediaUnreachable：插值后的上游请求里仍有本机媒体地址（未配置对象存储）。
// 在调上游之前失败，不产生任何上游调用。
var ErrLocalMediaUnreachable = errors.New("未配置对象存储：视频和音频需要上传到你自己的对象存储，请在 设置 → 配置 → 对象存储 中填写")

// ensureNoLocalMediaForRequest 只检查真正会发给上游的内容（C1）。
// edits 模式且有落盘参考图时走 multipart：payload 里的 image/images/mask 不会发送
// （图片字节走文件 part），只检查 scalarMultipartFields 产出的标量字段；其余情况整个
// payload 都会作为 JSON 发出，全量检查。判定条件必须与 callUpstream 的分支保持一致。
func (s *Service) ensureNoLocalMediaForRequest(model *models.ModelConfig, payload []byte, prepared []preparedImageInput) error {
	if model != nil && model.ImageRequestMode == models.ImageRequestModeOpenAIImageEdits && len(prepared) > 0 {
		fields := scalarMultipartFields(payload)
		sent, err := json.Marshal(fields)
		if err != nil {
			return nil
		}
		return s.ensureNoLocalMedia(sent)
	}
	return s.ensureNoLocalMedia(payload)
}

// ensureNoLocalMedia 扫描最终请求体里的每个字符串，发现 storage 认得的本地媒体引用就拒绝。
// data_uri / base64 / multipart 的参考图在此之前已被换成内容本身，不会命中。
func (s *Service) ensureNoLocalMedia(payload []byte) error {
	if s.storage == nil || len(payload) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}
	found := false
	assets.WalkStrings(decoded, func(v string) string {
		if !found {
			if key, ok := s.storage.ExtractKey(v); ok && key != "" {
				found = true
			}
		}
		return v
	})
	if found {
		return ErrLocalMediaUnreachable
	}
	return nil
}

// openLocalMedia：ref 是本地存储里的媒体（media:// 或 sidecar 签出的本地 URL）时直接读盘，
// 不走 HTTP——sidecar 的本地 URL 带 token 校验，自己请求自己既绕又脆。ok=false 表示不是本地媒体。
func (s *Service) openLocalMedia(ctx context.Context, ref string) (rc io.ReadCloser, ok bool, err error) {
	if s.storage == nil {
		return nil, false, nil
	}
	key, isLocal := s.storage.ExtractKey(ref)
	if !isLocal || key == "" {
		return nil, false, nil
	}
	rc, err = s.storage.GetObject(ctx, key)
	if err != nil {
		return nil, true, fmt.Errorf("read local media %q: %w", key, err)
	}
	return rc, true, nil
}

// readLocalMedia 读本地参考图进内存（data_uri / base64 模式），上限与 fetchInputImage 相同。
func (s *Service) readLocalMedia(ctx context.Context, ref string) ([]byte, string, bool, error) {
	rc, ok, err := s.openLocalMedia(ctx, ref)
	if !ok || err != nil {
		return nil, "", ok, err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, maxInputImageBytes+1))
	if err != nil {
		return nil, "", true, err
	}
	if len(body) > maxInputImageBytes {
		return nil, "", true, fmt.Errorf("image exceeds %d bytes", maxInputImageBytes)
	}
	return body, normalizeImageMIME(http.DetectContentType(body)), true, nil
}

// sniffReader 嗅探前 512 字节的 MIME，返回可继续从头读的 reader。
func sniffReader(r io.Reader) (io.Reader, string) {
	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	return br, http.DetectContentType(head)
}
