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
)

// InputMediaSigner 是 spec S12「输入媒体如何让上游拿到」的接入点（待定）。实现方把 inputs 里的
// 本地媒体引用（media://key 或 sidecar 签出的本地 URL）换成上游能直接下载的地址并返回新 map
// （不得修改入参）。未注入时走参考仓原逻辑 storage.SignedURL——桌面端得到的是 127.0.0.1
// 地址，上游拿不到，最终由 ensureNoLocalMedia 在调上游之前明确失败。
// 本计划不实现任何上传到公网主机的逻辑。
type InputMediaSigner interface {
	SignInputMedia(ctx context.Context, inputs map[string]any) (map[string]any, error)
}

// SetInputMediaSigner 注入 S12 方案的实现（setter 惯例同 SetTempDir）。
func (s *Service) SetInputMediaSigner(m InputMediaSigner) { s.inputSigner = m }

// ErrLocalMediaUnreachable：插值后的上游请求里仍有本机媒体地址。在调上游之前失败，
// 不产生任何上游调用。
var ErrLocalMediaUnreachable = errors.New("该模型要求参考图/视频/音频是上游能直接下载的网址，桌面端暂不支持把本地文件交给它（待定事项 S12）")

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
