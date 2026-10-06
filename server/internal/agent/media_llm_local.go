package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// MediaKeyExtractor 把本机媒体地址（sidecar 签出的 http URL 或 media://key）认回存储 key；
// 认不出（外链）返回 false。P1b 的 assets.Storage 直接满足。
type MediaKeyExtractor interface {
	ExtractKey(urlOrKey string) (string, bool)
}

const defaultMaxLLMImageBytes = 10 << 20

// LocalMediaResolver 是 S12 未定期间的实现：
//   - 外链 / data URI：原样放行（上游自己能拿到）；
//   - 本机图片：读出字节转 data URI（多数视觉模型接受）；
//   - 本机视频 / 音频：返回 ErrMediaNeedsPublicURL——上游只认公网地址，方案待定。
type LocalMediaResolver struct {
	Keys          MediaKeyExtractor
	Objects       AttachmentObjectReader
	MaxImageBytes int64 // 0 = 10MB
}

func (r *LocalMediaResolver) ResolveForLLM(ctx context.Context, kind, u string) (string, error) {
	if strings.HasPrefix(u, "data:") || r.Keys == nil {
		return u, nil
	}
	key, ok := r.Keys.ExtractKey(u)
	if !ok {
		return u, nil
	}
	if kind != "image" && kind != "" {
		return "", ErrMediaNeedsPublicURL
	}
	if r.Objects == nil {
		return "", fmt.Errorf("本机图片无法读取：存储未接入")
	}
	limit := r.MaxImageBytes
	if limit <= 0 {
		limit = defaultMaxLLMImageBytes
	}
	rc, err := r.Objects.GetObject(ctx, key)
	if err != nil {
		return "", fmt.Errorf("读取本机图片失败: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return "", fmt.Errorf("读取本机图片失败: %w", err)
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("图片超过 %d MB，无法发送给模型", limit>>20)
	}
	// 生成产物的 key 没有扩展名，按内容嗅探 MIME。
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return "", fmt.Errorf("文件不是图片（%s），无法作为图片发送给模型", mime)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// resolveMediaForLLM 返回 msgs 的浅拷贝，其中 image_url 部件换成上游拿得到的地址。
// 不改原 msgs：它们还要参与后续轮次，且持久化必须保留原地址。解析失败的图片换成
// 一段文字说明，让 planner 知道这张图没送到，而不是整轮失败。
func (a *Agent) resolveMediaForLLM(ctx context.Context, msgs []*schema.Message) []*schema.Message {
	if a.MediaResolver == nil {
		return msgs
	}
	out := make([]*schema.Message, len(msgs))
	for i, m := range msgs {
		if m == nil || len(m.MultiContent) == 0 {
			out[i] = m
			continue
		}
		cp := *m
		parts := make([]schema.ChatMessagePart, 0, len(m.MultiContent))
		for _, p := range m.MultiContent {
			if p.Type == schema.ChatMessagePartTypeImageURL && p.ImageURL != nil {
				u, err := a.MediaResolver.ResolveForLLM(ctx, "image", p.ImageURL.URL)
				if err != nil {
					parts = append(parts, schema.ChatMessagePart{Type: schema.ChatMessagePartTypeText, Text: "[图片未能发送给模型：" + err.Error() + "]"})
					continue
				}
				img := *p.ImageURL
				img.URL = u
				p.ImageURL = &img
			}
			parts = append(parts, p)
		}
		cp.MultiContent = parts
		out[i] = &cp
	}
	return out
}
