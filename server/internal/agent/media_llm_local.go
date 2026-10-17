package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// MediaKeyExtractor 把本机媒体地址（sidecar 签出的 http URL 或 media://key）认回存储 key；
// 认不出（外链）返回 false。assets.Storage 直接满足。
type MediaKeyExtractor interface {
	ExtractKey(urlOrKey string) (string, bool)
}

const defaultMaxLLMImageBytes = 10 << 20

var errLocalMediaUnresolvable = errors.New("本机图片无法转换后发送给模型：存储模块未接入或地址无法识别")

// IsLocalMediaURL 判断地址是否指向本机（上游访问不到）：media:// 或回环地址上的 http(s)。
func IsLocalMediaURL(u string) bool {
	if strings.HasPrefix(u, "media://") {
		return true
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		return false
	}
	host := pu.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// LocalMediaResolver 是 S12 未定期间的实现：
//   - 外链 / data URI：原样放行（上游自己能拿到）；
//   - 本机图片：读出字节转 data URI（多数视觉模型接受）；
//   - 本机视频 / 音频：经对象存储发布；未配置时返回 ErrMediaNeedsPublicURL。
type LocalMediaResolver struct {
	Keys          MediaKeyExtractor
	Objects       AttachmentObjectReader
	MaxImageBytes int64          // 0 = 10MB
	Publisher     MediaPublisher // nil 或未配置：视频/音频报 ErrMediaNeedsPublicURL
}

func (r *LocalMediaResolver) ResolveForLLM(ctx context.Context, kind, u string) (string, error) {
	if strings.HasPrefix(u, "data:") {
		return u, nil
	}
	if r.Keys == nil {
		// 没接存储（装配时漏传 Keys）：本机地址上游访问不到，原样发出去
		// 模型只会对着一个打不开的链接胡说，还会把媒体 token 带出本机。明确报错。
		if IsLocalMediaURL(u) {
			return "", errLocalMediaUnresolvable
		}
		return u, nil
	}
	key, ok := r.Keys.ExtractKey(u)
	if !ok {
		if IsLocalMediaURL(u) {
			return "", errLocalMediaUnresolvable
		}
		return u, nil
	}
	if kind != "image" && kind != "" {
		if r.Publisher == nil || !r.Publisher.Configured() {
			return "", ErrMediaNeedsPublicURL
		}
		return r.Publisher.Publish(ctx, key)
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

const (
	// maxInlineImageMessages：只有最近这么多条带图的消息会内联本机图片。更早的图片
	// 在当轮已经给模型看过，它的回复里已有结论；每轮都重新读盘、转 base64 会让请求体
	// 随对话线性膨胀（15 张 5MB 图 ≈ 每轮 100MB），上游直接 413。
	maxInlineImageMessages = 2
	// defaultMaxInlineImageBytes：单次请求内联图片 data URI 的总字节上限。
	defaultMaxInlineImageBytes = 20 << 20
)

const (
	noteOldImage    = "[较早引用的本机图片未重复发送给模型；需要再看请让用户重新 @ 该图片]"
	noteOverBudget  = "[图片未能发送给模型：本轮图片总量超过上限，请减少同时引用的图片]"
	noteImageFailed = "[图片未能发送给模型："
)

// resolveMediaForLLM 返回 msgs 的浅拷贝，其中 image_url 部件换成上游拿得到的地址。
// 不改原 msgs：它们还要参与后续轮次，且持久化必须保留原地址。解析失败的图片换成
// 一段文字说明，让 planner 知道这张图没送到，而不是整轮失败。
//
// 只给最近 maxInlineImageMessages 条带图消息内联本机图片，且总量不超过
// MaxInlineImageBytes；从最新的图片往回分配预算，超出的换成文字说明。外链不占预算。
func (a *Agent) resolveMediaForLLM(ctx context.Context, msgs []*schema.Message) []*schema.Message {
	if a.MediaResolver == nil {
		return msgs
	}
	budget := a.MaxInlineImageBytes
	if budget <= 0 {
		budget = defaultMaxInlineImageBytes
	}
	out := make([]*schema.Message, len(msgs))
	copy(out, msgs)
	imageMsgs := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || !hasImagePart(m) {
			continue
		}
		imageMsgs++
		recent := imageMsgs <= maxInlineImageMessages
		parts := make([]schema.ChatMessagePart, len(m.MultiContent))
		for j := len(m.MultiContent) - 1; j >= 0; j-- {
			p := m.MultiContent[j]
			parts[j] = p
			if p.Type != schema.ChatMessagePartTypeImageURL || p.ImageURL == nil {
				continue
			}
			src := p.ImageURL.URL
			if !recent && IsLocalMediaURL(src) {
				parts[j] = textPart(noteOldImage)
				continue
			}
			if !recent && !strings.HasPrefix(src, "data:") {
				continue // 外链：上游自己去拿，不占预算也不读盘
			}
			u, err := a.MediaResolver.ResolveForLLM(ctx, "image", src)
			if err != nil {
				parts[j] = textPart(noteImageFailed + err.Error() + "]")
				continue
			}
			if strings.HasPrefix(u, "data:") {
				if int64(len(u)) > budget {
					parts[j] = textPart(noteOverBudget)
					continue
				}
				budget -= int64(len(u))
			}
			img := *p.ImageURL
			img.URL = u
			p.ImageURL = &img
			parts[j] = p
		}
		cp := *m
		cp.MultiContent = parts
		out[i] = &cp
	}
	return out
}

func hasImagePart(m *schema.Message) bool {
	for _, p := range m.MultiContent {
		if p.Type == schema.ChatMessagePartTypeImageURL && p.ImageURL != nil {
			return true
		}
	}
	return false
}

func textPart(s string) schema.ChatMessagePart {
	return schema.ChatMessagePart{Type: schema.ChatMessagePartTypeText, Text: s}
}
