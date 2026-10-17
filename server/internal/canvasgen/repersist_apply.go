package canvasgen

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/generation"
)

// isUnsavedOutput：产物没能落本地（重试 3 次仍失败，保留了上游地址）。
func isUnsavedOutput(url string, assetIDs []int64) bool {
	remote := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return remote && (len(assetIDs) == 0 || assetIDs[0] == 0)
}

// ApplyRepersist 在用户手动「保存到本地」成功后，把源节点里的上游外链换成本地地址并清掉
// unsaved / genTaskId。按节点结构改而不是按字节替换：writeNode 用 json.Marshal 落库，URL 里的
// & 会被写成 \u0026，RewriteMediaRef 的 bytes.ReplaceAll 对这类 URL 改不到。
// sink 不是画布节点（其他发起方/直连任务）时什么都不做。
func (s *Service) ApplyRepersist(_ context.Context, sink json.RawMessage, reps []generation.RepersistReplacement) error {
	var ref canvasSinkRef
	if len(sink) == 0 || json.Unmarshal(sink, &ref) != nil || ref.Kind != sinkKindCanvasNode || ref.ProjectID == 0 || ref.NodeID == "" {
		return nil
	}
	byOld := make(map[string]generation.RepersistReplacement, len(reps))
	for _, r := range reps {
		byOld[r.OldURL] = r
	}
	return s.writeNode(ref.UserID, ref.ProjectID, ref.NodeID, func(node map[string]any) {
		settings := nodeSettings(node)
		if c, _ := node["content"].(string); c != "" {
			if r, ok := byOld[c]; ok {
				node["content"] = r.NewURL
				if r.Index == 0 && r.AssetID > 0 {
					settings["assetId"] = r.AssetID
				}
			}
		}
		if hist, ok := settings["history"].([]any); ok {
			for i, h := range hist {
				switch v := h.(type) {
				case string:
					if r, ok := byOld[v]; ok {
						hist[i] = r.NewURL
					}
				case map[string]any:
					if u, _ := v["url"].(string); u != "" {
						if r, ok := byOld[u]; ok {
							v["url"] = r.NewURL
						}
					}
				}
			}
		}
		delete(settings, "unsaved")
		delete(settings, "genTaskId")
	})
}
