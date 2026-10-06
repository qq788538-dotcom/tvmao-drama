// Package agent — tools_attachment.go：内置的 read_attachment 工具，让 LLM 能
// 分段读取「超过内联阈值的大文本」——用户上传的 .txt/.md 附件（对象存储）与
// 画布文字节点正文（快照 settings.text）。设计见
// docs/superpowers/specs/2026-08-31-agent-large-text-attachment-design.md。
//
// 为什么不内联：内联正文进会话历史每轮重放，大文件单条消息直接超 backbone
// 窗口，第一轮请求就失败；历史压缩只折「老消息」救不了当轮。分段读取把大
// 文本拆成一条条可折叠、可重读的 tool_result——原文永远躺在存储里，读过的段
// 被压缩回收后随时能重读，这是与内联截断的本质区别。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/james/huabu_studio/apps/api/internal/assets"
)

const ReadAttachmentToolName = "read_attachment"

const (
	// maxAttachmentReadRunes 是单次读取的服务端硬 cap。「模型够干活」与「历史
	// 别太肥」的折中：段越大，从读入到被压缩折叠之间每轮重放的 token 越多。
	maxAttachmentReadRunes = 20_000
	// maxAttachmentBytes 与前端拒收上限一致（10MB）。直连 API 绕过前端上传的
	// 更大对象在这里被挡住，不会把几百 MB 拉进内存。
	maxAttachmentBytes = 10 << 20
	// query 命中的上下文窗口与命中条数上限。给模型的是「定位线索」不是正文——
	// 拿到 offset 后自己决定精读哪段。
	queryContextRunes = 200
	maxQueryMatches   = 5
)

// AttachmentAssetFinder 是按 id 查 asset 行的最小能力（assets.MySQLRepository
// 直接满足）。窄接口方便单测 stub。
type AttachmentAssetFinder interface {
	FindByID(id int64) (*assets.AssetRecord, error)
}

// AttachmentObjectReader 是按 object key 读对象内容的最小能力（assets.Storage
// 的子集）。
type AttachmentObjectReader interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

type ReadAttachmentTool struct {
	Assets  AttachmentAssetFinder
	Objects AttachmentObjectReader
	Canvas  CanvasSnapshotReader
}

func (t *ReadAttachmentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"artifact_id": {
			Type:     schema.String,
			Desc:     "From the reference line in the user message: asset_<id> for an uploaded text file, or a canvas text-node id.",
			Required: true,
		},
		"offset": {
			Type: schema.Integer,
			Desc: "Start position in characters (default 0). Use next_offset from the previous read to continue.",
		},
		"limit": {
			Type: schema.Integer,
			Desc: fmt.Sprintf("Max characters to return (default and hard cap %d).", maxAttachmentReadRunes),
		},
		"query": {
			Type: schema.String,
			Desc: "Optional keyword. Returns up to 5 match positions with surrounding context instead of a slice — use the returned offsets to read the parts you need.",
		},
	}
	return &schema.ToolInfo{
		Name:        ReadAttachmentToolName,
		Desc:        "Read a large text attachment (uploaded .txt/.md file or canvas text node) in chunks. The full text is NOT in the conversation — call this to see its content. Response includes total_chars and next_offset for paging.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *ReadAttachmentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		ArtifactID string `json:"artifact_id"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
		Query      string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", ReadAttachmentToolName, err)
	}
	if strings.TrimSpace(args.ArtifactID) == "" {
		return "", fmt.Errorf("%s: artifact_id is required", ReadAttachmentToolName)
	}

	text, err := t.resolveText(ctx, strings.TrimSpace(args.ArtifactID))
	if err != nil {
		return "", err
	}
	runes := []rune(text)
	total := len(runes)

	if q := strings.TrimSpace(args.Query); q != "" {
		return renderQueryMatches(runes, q, total)
	}

	offset := args.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		return "", fmt.Errorf("%s: offset %d 超出全文长度 %d", ReadAttachmentToolName, offset, total)
	}
	limit := args.Limit
	if limit <= 0 || limit > maxAttachmentReadRunes {
		limit = maxAttachmentReadRunes
	}
	end := offset + limit
	if end > total {
		end = total
	}

	// 导航元信息是刚需：没有 total/next_offset 模型只能瞎猜 offset 翻页。
	out := map[string]any{
		"total_chars": total,
		"offset":      offset,
		"end":         end,
		"content":     string(runes[offset:end]),
	}
	if end < total {
		out["next_offset"] = end
	} else {
		out["eof"] = true
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// resolveText 把 artifact_id 解析成全文。两种来源：
//   - asset_<id>：上传附件。查 assets 行并校验归属（IDOR 防线——别人的 asset id
//     必须报「不存在」，不泄露存在性，对齐 WS thread 归属校验的先例）。
//   - 其余：当前项目画布上的文字节点 id，从最新快照读 settings.text。
func (t *ReadAttachmentTool) resolveText(ctx context.Context, artifactID string) (string, error) {
	if idStr, ok := strings.CutPrefix(artifactID, "asset_"); ok {
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id <= 0 {
			return "", fmt.Errorf("%s: invalid asset id %q", ReadAttachmentToolName, artifactID)
		}
		if t.Assets == nil || t.Objects == nil {
			return "", fmt.Errorf("%s: attachment reading not configured", ReadAttachmentToolName)
		}
		rec, err := t.Assets.FindByID(id)
		if err != nil || rec == nil {
			return "", fmt.Errorf("%s: attachment not found", ReadAttachmentToolName)
		}
		if rec.UserID != UserIDFromContext(ctx) {
			return "", fmt.Errorf("%s: attachment not found", ReadAttachmentToolName)
		}
		rc, err := t.Objects.GetObject(ctx, rec.ObjectKey)
		if err != nil {
			return "", fmt.Errorf("%s: read object: %w", ReadAttachmentToolName, err)
		}
		defer rc.Close()
		// +1 探测超限：正常上传经前端 10MB 拒收，这里只兜直连 API 的绕过。
		data, err := io.ReadAll(io.LimitReader(rc, maxAttachmentBytes+1))
		if err != nil {
			return "", fmt.Errorf("%s: read object: %w", ReadAttachmentToolName, err)
		}
		if len(data) > maxAttachmentBytes {
			return "", fmt.Errorf("%s: 附件超过 10MB，无法读取", ReadAttachmentToolName)
		}
		return string(data), nil
	}

	if t.Canvas == nil {
		return "", fmt.Errorf("%s: canvas reading not configured", ReadAttachmentToolName)
	}
	projectID := ProjectIDFromContext(ctx)
	raw, err := t.Canvas.GetLatestSnapshot(projectID)
	if err != nil {
		return "", fmt.Errorf("%s: read canvas: %w", ReadAttachmentToolName, err)
	}
	var snapshot struct {
		Nodes []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Settings struct {
				Text string `json:"text"`
			} `json:"settings"`
		} `json:"nodes"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return "", fmt.Errorf("%s: parse snapshot: %w", ReadAttachmentToolName, err)
		}
	}
	for _, n := range snapshot.Nodes {
		if n.ID != artifactID {
			continue
		}
		if n.Type != "text" {
			return "", fmt.Errorf("%s: node %s is %q, not a text node", ReadAttachmentToolName, artifactID, n.Type)
		}
		return n.Settings.Text, nil
	}
	return "", fmt.Errorf("%s: text node %s not found on canvas", ReadAttachmentToolName, artifactID)
}

// renderQueryMatches 大小写不敏感的 substring 定位。给出的是命中 offset + 前后
// 上下文——模型据此决定去精读哪段，而不是让服务端替它猜「相关性」。
func renderQueryMatches(runes []rune, query string, total int) (string, error) {
	// 逐 rune 小写而不是 strings.ToLower 整串：个别 Unicode 小写映射会改变
	// rune 数（İ→i̇），整串转换会让命中 offset 与原文错位。
	lowerRunes := make([]rune, len(runes))
	for i, r := range runes {
		lowerRunes[i] = unicode.ToLower(r)
	}
	q := []rune(query)
	for i, r := range q {
		q[i] = unicode.ToLower(r)
	}
	matches := make([]map[string]any, 0, maxQueryMatches)
	for i := 0; i+len(q) <= len(lowerRunes) && len(matches) < maxQueryMatches; i++ {
		if string(lowerRunes[i:i+len(q)]) != string(q) {
			continue
		}
		start := i - queryContextRunes
		if start < 0 {
			start = 0
		}
		end := i + len(q) + queryContextRunes
		if end > len(runes) {
			end = len(runes)
		}
		matches = append(matches, map[string]any{
			"offset":  i,
			"context": string(runes[start:end]),
		})
		i += len(q) - 1
	}
	out := map[string]any{"total_chars": total, "query_matches": matches}
	if len(matches) == 0 {
		out["note"] = "no match; try another keyword or read sequentially"
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}
