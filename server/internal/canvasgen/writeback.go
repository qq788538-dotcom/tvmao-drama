package canvasgen

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasstate"
)

// writeNode 后端权威回写画布快照里 nodeID 那个节点:通过 projects.MutateSnapshot 在行锁事务内
// 读-改-写(version+1)。用**通用 map 外科式改**——只动目标节点,其余节点/字段字节原样保留,
// 绝不因结构体不完整而丢掉前端才有的字段。mutate 只改传入的那个节点 map。
//
// 为什么后端写而非前端:恒异步后,出图时用户浏览器可能已关,没有前端可回写。回写必须后端做,
// 前端轮询仅显示。
//
// 错误语义(v5 §5.7,必须如实返错——上一版全部吞掉只打日志,第一次 MarkDone 成功但真实
// 写入失败时结果就永久缺失了):
//   - 节点已被用户删除 → nil(成功 no-op:无处可写不是故障,重投也无意义);
//   - 快照 JSON 损坏 → error(真失败,投递方按退避重试,等人修复快照后重投能成功);
//   - DB 错 → error。
//
// userID 只用于广播落点(agent Hub 的连接键),不参与写入判定;为 0(存量任务的 sink ref
// 里没这个字段)时照常写入、只是不广播。
func (s *Service) writeNode(userID, projectID int64, nodeID string, mutate func(node map[string]any)) error {
	// 观测「检测到并发更新」误弹:每次后端回写都会 version+1,顶掉网页的 baseVersion。
	// 这条日志配合 [snapshot] PUT 日志能还原完整时序。排查完可以删。
	log.Printf("[snapshot] project=%d backend writeNode node=%s (version+1)", projectID, nodeID)
	changed := false
	// changedNode 是回写后的整节点，用来做 canvas_op 摘要的载荷。
	// kind 取 upsertNode 而不是 patchNode：这条路径的 mutate 回调直接在节点 map 上
	// 增删改（appendHistory / delete taskId / clearNodeError），根本没有一份「patch」
	// 可交——硬凑一个 patch 会漏掉 delete 语义（patch 语义里没有删除键的表达），
	// 让 peer 上残留一个已经被删掉的 taskId。整节点替换才与服务端结果逐字一致。
	var changedNode json.RawMessage
	version, err := s.snaps.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		// 快照行不存在(项目被硬删,外键级联带走了快照)→ 没节点可回写,成功 no-op。
		// 语义跟下面那句「节点已被用户删除」一模一样。必须显式挡在这里:
		// MutateSnapshot 现在会在无行时调 fn(nil) 并按结果决定要不要建行,
		// 不挡的话 Unmarshal(nil) 会报「快照损坏」,把一次正常的 no-op 变成回写失败。
		if len(payload) == 0 {
			return nil, false, nil
		}
		var doc map[string]any
		if err := json.Unmarshal(payload, &doc); err != nil {
			return nil, false, fmt.Errorf("画布快照损坏,无法回写: %w", err)
		}
		nodes, ok := doc["nodes"].([]any)
		if !ok {
			return nil, false, fmt.Errorf("画布快照缺少 nodes 数组,无法回写")
		}
		for _, raw := range nodes {
			node, ok := raw.(map[string]any)
			if !ok || node["id"] != nodeID {
				continue
			}
			mutate(node)
			next, err := json.Marshal(doc)
			if err != nil {
				return nil, false, err
			}
			// 摘要序列化失败不该毁掉一次成功的回写：置空即可，收端见不到 op 就退回
			// 整拉快照（旧版行为）。
			changedNode, _ = json.Marshal(node)
			changed = true
			return next, true, nil
		}
		return nil, false, nil // 节点已被用户删除 → 成功 no-op
	})
	if err != nil {
		return fmt.Errorf("writeNode project=%d node=%s: %w", projectID, nodeID, err)
	}
	// 只有真写进去了才广播:no-op(节点已删/快照行不存在)什么都没变,发帧只会让所有
	// 观看端白拉一次快照。sourceMark 恒为 "server"——后端权威回写没有 ownerToken,
	// 观看端也不该把它当自己的回声跳过。
	if changed && s.notifier != nil {
		var op *canvasstate.CanvasOpSummary
		if len(changedNode) > 0 {
			// 出帧前补签：节点里的 content 是 canonical `media://<key>`，浏览器不认
			// 这个协议。旧版同伴走 GET 快照，那条路上有 SignJSON（唯一签名出口）；
			// 新版本地合并绕开了它，不补签就会「出图那一刻同伴画布上的图整块裂开，
			// 刷新才好」，且零报错。落库的字节保持 canonical（签名 URL 会过期）。
			node := changedNode
			if s.signer != nil {
				node = s.signer.SignJSON(node)
			}
			op = &canvasstate.CanvasOpSummary{Kind: canvasstate.OpUpsertNode, Node: node}
		}
		s.notifier.NotifyCanvasChanged(userID, projectID, version, "server", op)
	}
	return nil
}

// setNodeError / clearNodeError 把失败原因写进节点 settings.error(前端展示),与画布节点约定一致。
func setNodeError(node map[string]any, msg string) {
	settings, ok := node["settings"].(map[string]any)
	if !ok {
		settings = map[string]any{}
		node["settings"] = settings
	}
	settings["error"] = msg
}

func clearNodeError(node map[string]any) {
	if settings, ok := node["settings"].(map[string]any); ok {
		delete(settings, "error")
	}
}

// nodeMediaKind 从节点类型推出媒体种类,与前端写的 settings.kind 取值一致。
// 认不出的类型返回空串,调用方就不写这个字段——宁可不写,也不要瞎猜一个值。
func nodeMediaKind(node map[string]any) string {
	switch node["type"] {
	case "gen-image":
		return "image"
	case "gen-video":
		return "video"
	case "gen-audio":
		return "audio"
	default:
		return ""
	}
}

// nodeSettings 取(必要时创建)节点的 settings map。
func nodeSettings(node map[string]any) map[string]any {
	settings, ok := node["settings"].(map[string]any)
	if !ok {
		settings = map[string]any{}
		node["settings"] = settings
	}
	return settings
}

// normalizeHistory 与前端 editor/utils/genHistory.ts 的同名函数**必须逐字同构**:
// 老快照的条目是纯 URL 字符串,新的是 {url,prompt,params,at};没有 history 但有
// content 的更早期节点,按「只有一条、用 content 初始化」处理。
//
// 两边同构是硬要求:页面开着时前端也会算一遍并 PATCH 上来,算法不一致就会出现
// 「关页面和不关页面,历史条数不一样」。
func normalizeHistory(raw any, fallbackContent any) []any {
	items := []any{}
	if list, ok := raw.([]any); ok {
		for _, entry := range list {
			switch v := entry.(type) {
			case string:
				if v != "" {
					items = append(items, map[string]any{"url": v})
				}
			case map[string]any:
				if url, ok := v["url"].(string); ok && url != "" {
					items = append(items, v)
				}
			}
		}
	}
	if len(items) == 0 {
		if url, ok := fallbackContent.(string); ok && url != "" {
			return []any{map[string]any{"url": url}}
		}
	}
	return items
}

// appendHistory 把这次成功的结果并入 settings.history,并把 historyIndex 指到它。
//
// 为什么后端也要写:恒异步后用户完全可能提交完就关页面(生视频动辄几分钟),那时没有
// 前端来追加这一条。只靠前端写的话,「第 2 次及以后的生成 + 中途关页面」这条路径下,
// 结果会写进 content 却进不了 history——节点上看到的还是旧图,新结果翻都翻不到。
//
// 幂等:同 URL 只留一条(前端可能已经写过了,sink 也可能重投),重复调用只会把
// historyIndex 指到那一条,不会长出第二条。
//
// 提示词来自 settings.pendingGen——前端点「生成」那一刻就落库的快照。生成期间
// 「生成」按钮是禁用的,一个节点同时只有一个在跑的任务,所以它不会被下一次提交覆盖。
// 用完即删。
func appendHistory(node map[string]any, url string, at string) {
	settings := nodeSettings(node)
	// 注意:必须在调用方把 node["content"] 改成新 URL **之前**调用,
	// 否则 fallbackContent 会把新结果当成「老的那一条」重复计入。
	items := normalizeHistory(settings["history"], node["content"])

	pending, _ := settings["pendingGen"].(map[string]any)
	delete(settings, "pendingGen")

	for i, entry := range items {
		if item, ok := entry.(map[string]any); ok && item["url"] == url {
			settings["history"] = items
			settings["historyIndex"] = i
			return
		}
	}

	item := map[string]any{"url": url}
	if pending != nil {
		if prompt, ok := pending["prompt"].(string); ok && prompt != "" {
			item["prompt"] = prompt
		}
		if params, ok := pending["params"].(map[string]any); ok && len(params) > 0 {
			item["params"] = params
		}
		// 绘图提示词预设快照（与前端 appendHistoryItem 同构）：presetName 是提交
		// 那一刻的显示名——预设日后被删/改名,历史仍显示当时的名字;正文不进历史。
		if presetKey, ok := pending["presetKey"].(string); ok && presetKey != "" {
			item["presetKey"] = presetKey
		}
		if presetName, ok := pending["presetName"].(string); ok && presetName != "" {
			item["presetName"] = presetName
		}
	}
	if at != "" {
		item["at"] = at
	}
	items = append(items, item)
	settings["history"] = items
	settings["historyIndex"] = len(items) - 1
}
