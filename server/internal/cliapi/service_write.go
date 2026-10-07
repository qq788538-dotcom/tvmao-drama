package cliapi

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// CLI 的画布写入。
//
// 全部走 projects.MutateSnapshot（SELECT…FOR UPDATE + version+1，**不推进
// last_client_version**）+ canvasstate 的既有 mutation 原语。这意味着：
//
//   - CLI 与网页、Agent、生成回写在 MySQL 行锁上串行化，不会丢更新；
//   - CLI **不 claim 浏览器的画布编辑权**——否则一次自动化命令会让用户网页失权；
//   - canvasstate 的原语是「外科式」改，其余节点与未知字段字节原样保留。

// MutationOptions 承载显式并发保护。
type MutationOptions struct {
	// ExpectVersion > 0 时，mutate 前比对快照版本，不等即 CANVAS_VERSION_CONFLICT。
	// 普通局部命令不传，按服务器最新状态做 merge mutation。
	ExpectVersion int64
}

// checkVersion 在 mutate 前比对版本。
//
// 已知取舍：这是「mutate 前 GetLatest 比对」，不是事务内比对，因此与写入之间
// 有一个极小窗口。要做到事务内比对得改 projects.MutateSnapshot 的回调签名，
// 那是现有代码，不在本次允许改动的两处之内。对 CLI 的用途（人/Agent 显式传
// --snapshot-version 做乐观保护）这个精度足够。
func (s *Service) checkVersion(projectID int64, opts MutationOptions) error {
	if opts.ExpectVersion <= 0 {
		return nil
	}
	snap, err := s.projects.GetLatest(projectID)
	current := int64(0)
	if err == nil && snap != nil {
		current = snap.Version
	} else if err != nil && !errors.Is(err, projects.ErrNotFound) {
		return New(CodeInternal, "读取画布版本失败").WithCause(err)
	}
	if current != opts.ExpectVersion {
		return Errf(CodeCanvasVersionConflict, "画布已被改动（当前版本 %d，期望 %d）", current, opts.ExpectVersion).
			WithDetails(map[string]any{"currentVersion": current, "expectedVersion": opts.ExpectVersion})
	}
	return nil
}

// CreateNode 建节点。
func (s *Service) CreateNode(userID, projectID int64, req CreateNodeRequest, opts MutationOptions) (Node, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return Node{}, err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return Node{}, err
	}
	raw, err := BuildNodeJSON(req)
	if err != nil {
		return Node{}, err
	}
	if _, err := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		// 摘要丢弃：CLI 写入至今不广播 canvas_op（没有 WS 连接的调用方），这条路径
		// 与二期并发编辑无关。要接的话在这里把摘要交给 notifier 即可。
		next, changed, _, err := canvasstate.UpsertNode(ensurePayload(payload), raw)
		return next, changed, err
	}); err != nil {
		return Node{}, New(CodeInternal, "写入画布失败").WithCause(err)
	}

	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		return Node{}, New(CodeInternal, "节点序列化异常").WithCause(err)
	}
	return ToNode(node, s.sign), nil
}

// PatchNode 局部修改节点。
func (s *Service) PatchNode(userID, projectID int64, nodeID string, req PatchNodeRequest, opts MutationOptions) (Node, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return Node{}, err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return Node{}, err
	}
	// 参数校验留在事务外，坏请求要快速失败；patch 的构造得等读到节点类型。
	if err := validateParams(req.Params); err != nil {
		return Node{}, err
	}

	var updated map[string]any
	_, mutErr := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		payload = ensurePayload(payload)
		internal, err := requireWritableNode(payload, nodeID)
		if err != nil {
			return nil, false, err
		}
		patch, err := BuildNodePatch(internal, req)
		if err != nil {
			return nil, false, err
		}
		next, changed, _, err := canvasstate.PatchNode(payload, nodeID, patch)
		if err != nil {
			return nil, false, err
		}
		updated = lookupNode(next, nodeID)
		return next, changed, nil
	})
	if mutErr != nil {
		return Node{}, mapMutationError(mutErr, nodeID)
	}
	if updated == nil {
		return Node{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	return ToNode(updated, s.sign), nil
}

// PatchNodePositionsRequest 是批量几何补丁。元素直接复用 canvasstate 的
// NodeGeometryPatch——网页拖动保存走的就是它，字段语义（nil=不动）两边一致。
type PatchNodePositionsRequest struct {
	Nodes []canvasstate.NodeGeometryPatch `json:"nodes"`
}

// PatchNodePositionsResult 带回新版本号：agent 摆完位置接着写别的时可以直接
// 用它做 If-Match，不必再 canvas get 一次。
type PatchNodePositionsResult struct {
	Version int64  `json:"version"`
	Nodes   []Node `json:"nodes"`
}

// PatchNodePositions 一次 mutate 改多个节点的 x/y/width/height。
//
// 为什么要有它：agent 布局一集要挪十几个节点，单节点 PATCH 逼它拆成十几条命令，
// 每条之间夹一轮思考，还因为每次 version+1 得反复 canvas get 追版本号——12 个节点
// 花几分钟，HTTP 只占十几秒，其余全是 agent 在等自己。批量 = 一次请求、一次
// version+1、整批原子。
//
// 不看可写类型：坐标不属于「内容/参数」，网页上任何节点都能拖，agent 能做的
// 应当等于 UI 能做的。requireWritableNode 保护的是只读节点的内容不变量，与几何无关。
func (s *Service) PatchNodePositions(userID, projectID int64, req PatchNodePositionsRequest, opts MutationOptions) (PatchNodePositionsResult, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return PatchNodePositionsResult{}, err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return PatchNodePositionsResult{}, err
	}
	if err := validateGeometryPatches(req.Nodes); err != nil {
		return PatchNodePositionsResult{}, err
	}

	var next json.RawMessage
	version, mutErr := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		payload = ensurePayload(payload)
		doc, err := decodeDoc(payload)
		if err != nil {
			return nil, false, err
		}
		// 先整批查存在性再改：canvasstate 的 ErrNodeNotFound 不带 id，agent 拿到
		// 「节点不存在」却不知道是哪一个，等于没报。
		for _, p := range req.Nodes {
			if _, ok := FindNode(doc, p.ID); !ok {
				return nil, false, Errf(CodeNodeNotFound, "节点 %s 不存在", p.ID)
			}
		}
		out, changed, _, err := canvasstate.PatchNodePositions(payload, req.Nodes)
		if err != nil {
			return nil, false, err
		}
		next = out
		return out, changed, nil
	})
	if mutErr != nil {
		return PatchNodePositionsResult{}, mapMutationError(mutErr, "")
	}

	res := PatchNodePositionsResult{Version: version, Nodes: make([]Node, 0, len(req.Nodes))}
	for _, p := range req.Nodes {
		if n := lookupNode(next, p.ID); n != nil {
			res.Nodes = append(res.Nodes, ToNode(n, s.sign))
		}
	}
	return res, nil
}

// validateGeometryPatches 把「agent 拼错了请求」在事务外拦住。
// 没有几何字段的元素与重复 id 都按坏请求报——前者静默无效果，后者哪个生效说不清，
// 两种都是 agent 拿不到纠正信号的静默失败。
func validateGeometryPatches(patches []canvasstate.NodeGeometryPatch) error {
	if len(patches) == 0 {
		return New(CodeInvalidArgument, "nodes 不能为空")
	}
	seen := make(map[string]struct{}, len(patches))
	for i, p := range patches {
		if strings.TrimSpace(p.ID) == "" {
			return Errf(CodeInvalidArgument, "nodes[%d] 缺少 id", i)
		}
		if _, dup := seen[p.ID]; dup {
			return Errf(CodeInvalidArgument, "节点 %s 在同一批里出现了两次", p.ID)
		}
		seen[p.ID] = struct{}{}
		if p.X == nil && p.Y == nil && p.Width == nil && p.Height == nil {
			return Errf(CodeInvalidArgument, "节点 %s 没有任何几何字段（x / y / width / height）", p.ID)
		}
	}
	return nil
}

// DeleteNode 删节点，并在**同一次 mutate 内**清掉它的所有关联连线。
// 分两次写会在中间态留下悬空边——网页读到会报错。
func (s *Service) DeleteNode(userID, projectID int64, nodeID string, opts MutationOptions) error {
	if _, err := s.project(userID, projectID); err != nil {
		return err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return err
	}
	found := false
	_, mutErr := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		payload = ensurePayload(payload)
		if _, err := requireWritableNode(payload, nodeID); err != nil {
			return nil, false, err
		}
		found = true

		next, changed, _, err := canvasstate.DeleteNodes(payload, []string{nodeID})
		if err != nil {
			return nil, false, err
		}
		for _, edge := range parseEdges(next) {
			if edge.FromNodeID != nodeID && edge.ToNodeID != nodeID {
				continue
			}
			afterEdge, edgeChanged, _, err := canvasstate.DeleteConnection(next, edge.ID)
			if err != nil {
				if errors.Is(err, canvasstate.ErrConnectionNotFound) {
					continue
				}
				return nil, false, err
			}
			next = afterEdge
			changed = changed || edgeChanged
		}
		return next, changed, nil
	})
	if mutErr != nil {
		return mapMutationError(mutErr, nodeID)
	}
	if !found {
		return Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	return nil
}

type CreateEdgeRequest struct {
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
}

// CreateEdge 建连线。
//
// 同 (from,to) 已存在时**返回原连线**，不新建——这样 CLI 的 `--left` 是「确保
// 这条边存在」而不是「再加一条」，重复执行同一条命令不会攒出重复边。
//
// 不写 inputType：生产 561 + 测试 85 条连线里只有 1 条有值，它不是分类依据。
// 输入顺序由 connections 数组位置承载，按期望顺序依次建边即可。
func (s *Service) CreateEdge(userID, projectID int64, req CreateEdgeRequest, opts MutationOptions) (Edge, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return Edge{}, err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return Edge{}, err
	}
	from := strings.TrimSpace(req.FromNodeID)
	to := strings.TrimSpace(req.ToNodeID)
	if from == "" || to == "" {
		return Edge{}, New(CodeInvalidArgument, "fromNodeId 与 toNodeId 都不能为空")
	}
	if from == to {
		return Edge{}, New(CodeInvalidArgument, "不能把节点连到它自己")
	}

	var result Edge
	_, mutErr := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		payload = ensurePayload(payload)
		doc, err := decodeDoc(payload)
		if err != nil {
			return nil, false, err
		}
		if _, ok := FindNode(doc, from); !ok {
			return nil, false, Errf(CodeNodeNotFound, "上游节点 %s 不存在", from)
		}
		if _, ok := FindNode(doc, to); !ok {
			return nil, false, Errf(CodeNodeNotFound, "下游节点 %s 不存在", to)
		}
		for _, e := range parseEdges(payload) {
			if e.FromNodeID == from && e.ToNodeID == to {
				result = e
				return nil, false, nil // 幂等：已存在即不改
			}
		}
		result = Edge{ID: NewEdgeID(), FromNodeID: from, ToNodeID: to}
		raw, err := json.Marshal(map[string]any{"id": result.ID, "from": from, "to": to})
		if err != nil {
			return nil, false, err
		}
		next, changed, _, err := canvasstate.UpsertConnection(payload, raw)
		return next, changed, err
	})
	if mutErr != nil {
		return Edge{}, mapMutationError(mutErr, "")
	}
	return result, nil
}

func (s *Service) DeleteEdge(userID, projectID int64, edgeID string, opts MutationOptions) error {
	if _, err := s.project(userID, projectID); err != nil {
		return err
	}
	if err := s.checkVersion(projectID, opts); err != nil {
		return err
	}
	_, mutErr := s.projects.MutateSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		next, changed, _, err := canvasstate.DeleteConnection(ensurePayload(payload), edgeID)
		return next, changed, err
	})
	if mutErr != nil {
		if errors.Is(mutErr, canvasstate.ErrConnectionNotFound) {
			return Errf(CodeEdgeNotFound, "连线 %s 不存在", edgeID)
		}
		return mapMutationError(mutErr, "")
	}
	return nil
}

// ---- 项目写入 ----

func (s *Service) CreateProject(userID int64, name, description string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, New(CodeInvalidArgument, "项目名称不能为空")
	}
	id, err := s.projects.Create(userID, name, description)
	if err != nil {
		return Project{}, New(CodeInternal, "创建项目失败").WithCause(err)
	}
	return s.GetProject(userID, id)
}

// UpdateProject 是部分更新，但底层 projects.Update 是全量覆盖——
// 所以必须先读当前值，只覆盖请求里出现的字段，否则只改 description 会把
// name 清空。
func (s *Service) UpdateProject(userID, projectID int64, name, description *string) (Project, error) {
	p, err := s.project(userID, projectID)
	if err != nil {
		return Project{}, err
	}
	nextName, nextDesc := p.Name, p.Description
	if name != nil {
		if strings.TrimSpace(*name) == "" {
			return Project{}, New(CodeInvalidArgument, "项目名称不能为空")
		}
		nextName = *name
	}
	if description != nil {
		nextDesc = *description
	}
	if err := s.projects.Update(userID, projectID, nextName, nextDesc); err != nil {
		return Project{}, New(CodeInternal, "更新项目失败").WithCause(err)
	}
	return s.GetProject(userID, projectID)
}

func (s *Service) DeleteProject(userID, projectID int64) error {
	if _, err := s.project(userID, projectID); err != nil {
		return err
	}
	if err := s.projects.Delete(userID, projectID); err != nil {
		return New(CodeInternal, "删除项目失败").WithCause(err)
	}
	return nil
}

// ---- 内部工具 ----

// ensurePayload 把空快照补成合法空画布，让 canvasstate 的原语能直接工作。
func ensurePayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || string(payload) == "null" {
		return json.RawMessage(`{"nodes":[],"connections":[]}`)
	}
	return payload
}

func decodeDoc(payload json.RawMessage) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, New(CodeInternal, "画布快照损坏，无法写入").WithCause(err)
	}
	return doc, nil
}

func lookupNode(payload json.RawMessage, nodeID string) map[string]any {
	doc, err := decodeDoc(payload)
	if err != nil {
		return nil
	}
	n, _ := FindNode(doc, nodeID)
	return n
}

func parseEdges(payload json.RawMessage) []Edge {
	var doc canvasDoc
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil
	}
	out := make([]Edge, 0, len(doc.Connections))
	for _, raw := range doc.Connections {
		out = append(out, ToEdge(raw))
	}
	return out
}

// requireWritableNode 校验节点存在且属于 4 种可写类型。
// 只读类型（preview / Agent 产物 / 后处理节点）由它们各自的产生方维护，
// CLI 去改会破坏那些不变量。
// 返回节点的内部类型：参数名怎么翻成 settings 键名要看它（gen-video 的
// prompt 存 videoPrompt），而这个信息只有读到快照里的节点才知道。
func requireWritableNode(payload json.RawMessage, nodeID string) (string, error) {
	doc, err := decodeDoc(payload)
	if err != nil {
		return "", err
	}
	raw, ok := FindNode(doc, nodeID)
	if !ok {
		return "", Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	internal := str(raw["type"])
	semantic, writable := SemanticType(internal)
	if !writable {
		return "", Errf(CodeNodeReadonlyType, "节点 %s 的类型 %q 只读，CLI 不能修改", nodeID, semantic)
	}
	return internal, nil
}

// mapMutationError 把 canvasstate / repo 的错误翻成稳定 CLI 错误码。
// 已经是 *Error 的（来自 mutate 回调里的业务校验）原样透传。
func mapMutationError(err error, nodeID string) error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	switch {
	case errors.Is(err, canvasstate.ErrNodeNotFound):
		return Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	case errors.Is(err, canvasstate.ErrConnectionNotFound):
		return New(CodeEdgeNotFound, "连线不存在")
	case errors.Is(err, canvasstate.ErrInvalidPayload):
		return New(CodeInternal, "画布快照损坏").WithCause(err)
	default:
		return New(CodeInternal, "写入画布失败").WithCause(err)
	}
}
