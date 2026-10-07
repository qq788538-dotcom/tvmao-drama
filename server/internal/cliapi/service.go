package cliapi

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/canvasinputs"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

// ProjectStore 是 Service 需要的 projects.Repository 子集。
//
// 用窄接口而不是直接持 *projects.MySQLRepository：单测可以注入内存实现，
// 且这里明确表达了「CLI 只用到这几个能力」——尤其是**不用**
// MutateClientSnapshot，那是网页局部 mutation 的保存路径。
// （曾经还有个 SaveWithOptimisticLock 的整快照保存路径，随网页改走局部
// mutation 一并删除，CLI 从来没用过它。）
type ProjectStore interface {
	// limit/offset 是网页列表页的无限滚动加的；CLI 恒传 0 拉全量——
	// `tvmao project list` 的语义是「列出我的项目」，分页会让它悄悄少东西。
	ListForUser(userID int64, limit, offset int) ([]projects.Project, int, error)
	GetForUser(userID, projectID int64) (*projects.Project, error)
	Create(userID int64, name, description string) (int64, error)
	Update(userID, projectID int64, name, description string) error
	Delete(userID, projectID int64) error
	GetLatest(projectID int64) (*projects.Snapshot, error)
	// MutateSnapshot 是后端权威写入：SELECT…FOR UPDATE + version+1，
	// **不推进 last_client_version**。CLI 与生成回写同属后端权威，
	// 因此不 claim 浏览器的画布编辑权，一次 CLI 命令不会让用户网页失权。
	// 返回写入后的 version（本层用不上，`_` 接收即可）。
	MutateSnapshot(projectID int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error)
}

// Service 是 CLI 的领域适配层：把稳定 DTO 请求翻译成对现有 repo/service 的调用。
type Service struct {
	projects ProjectStore
	sign     func(string) string
	// gen / models 由 WithGeneration 注入；只读场景可以不接，那时 run 相关
	// 接口会明确报「生成能力未配置」而不是 nil panic。
	gen    Enqueuer
	models ModelLookup
	// seedance / mediaKeys 由 WithSeedance 注入；没接时合规校验接口明确
	// 报「未配置」而不是 nil panic。
	seedance  SeedanceGate
	mediaKeys KeyExtractor
}

func NewService(store ProjectStore, sign func(string) string) *Service {
	if sign == nil {
		sign = func(s string) string { return s }
	}
	return &Service{projects: store, sign: sign}
}

// project 取项目并校验归属。
//
// 「不存在」与「是别人的」返回**同一个** PROJECT_NOT_FOUND：区分开会让
// 项目 id 可枚举。
func (s *Service) project(userID, projectID int64) (*projects.Project, error) {
	p, err := s.projects.GetForUser(userID, projectID)
	if err != nil || p == nil {
		if err != nil && !errors.Is(err, projects.ErrNotFound) {
			return nil, New(CodeInternal, "读取项目失败").WithCause(err)
		}
		return nil, Errf(CodeProjectNotFound, "项目 %d 不存在", projectID)
	}
	return p, nil
}

func toProjectDTO(p projects.Project) Project {
	return Project{
		ID:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		CreatedAt:    p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:    p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		StorageBytes: p.StorageBytes,
	}
}

func (s *Service) ListProjects(userID int64) ([]Project, error) {
	list, _, err := s.projects.ListForUser(userID, 0, 0) // 0 = 不分页，拉全量
	if err != nil {
		return nil, New(CodeInternal, "读取项目列表失败").WithCause(err)
	}
	out := make([]Project, 0, len(list))
	for _, p := range list {
		out = append(out, toProjectDTO(p))
	}
	return out, nil
}

func (s *Service) GetProject(userID, projectID int64) (Project, error) {
	p, err := s.project(userID, projectID)
	if err != nil {
		return Project{}, err
	}
	return toProjectDTO(*p), nil
}

// GetCanvas 返回归一化后的整张画布。
func (s *Service) GetCanvas(userID, projectID int64) (Canvas, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return Canvas{}, err
	}
	snap, err := s.projects.GetLatest(projectID)
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			// 项目存在但从未保存过画布——回空画布，不是错误。
			return Canvas{Version: 0, Nodes: []Node{}, Edges: []Edge{}}, nil
		}
		return Canvas{}, New(CodeInternal, "读取画布失败").WithCause(err)
	}
	if snap == nil {
		return Canvas{Version: 0, Nodes: []Node{}, Edges: []Edge{}}, nil
	}
	return ParseCanvas(snap.Payload, snap.Version, s.sign)
}

// NodeFilter 收窄 node list；零值字段不过滤。
type NodeFilter struct {
	// Type 按**语义类型**过滤（image-generator 等）。传内部类型不匹配——
	// 契约只认一套命名。
	Type   string
	Status string
}

func (s *Service) ListNodes(userID, projectID int64, f NodeFilter) ([]Node, error) {
	c, err := s.GetCanvas(userID, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		if f.Type != "" && n.Type != f.Type {
			continue
		}
		if f.Status != "" && n.Status != f.Status {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *Service) GetNode(userID, projectID int64, nodeID string) (Node, error) {
	c, err := s.GetCanvas(userID, projectID)
	if err != nil {
		return Node{}, err
	}
	for _, n := range c.Nodes {
		if n.ID == nodeID {
			return n, nil
		}
	}
	return Node{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
}

// ResolveNode 把节点引用解析成唯一节点 ID。
//
// 第一阶段**只解析 ID**：不引入节点 name，因为快照结构不能改，而存量节点
// 本来就没有名字。调用方拿 node create 的返回 ID 记在自己上下文里即可。
func (s *Service) ResolveNode(userID, projectID int64, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", New(CodeInvalidArgument, "ref 不能为空")
	}
	n, err := s.GetNode(userID, projectID, ref)
	if err != nil {
		return "", err
	}
	return n.ID, nil
}

// GetNodeDetail 在节点详情上补出「运行前审计」需要的信息：
// 按连线派生的实际输入、以及已产出的输出列表。
//
// spec §19.5：Agent 可以先完整搭建一集的节点图，再由用户确认是否产生费用——
// 前提是运行前能看清每个节点会拿到什么输入。
func (s *Service) GetNodeDetail(userID, projectID int64, nodeID string) (NodeDetail, error) {
	if _, err := s.project(userID, projectID); err != nil {
		return NodeDetail{}, err
	}
	snap, err := s.projects.GetLatest(projectID)
	if err != nil || snap == nil {
		return NodeDetail{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}
	canvas, err := ParseCanvas(snap.Payload, snap.Version, s.sign)
	if err != nil {
		return NodeDetail{}, err
	}
	var node *Node
	for i := range canvas.Nodes {
		if canvas.Nodes[i].ID == nodeID {
			node = &canvas.Nodes[i]
			break
		}
	}
	if node == nil {
		return NodeDetail{}, Errf(CodeNodeNotFound, "节点 %s 不存在", nodeID)
	}

	detail := NodeDetail{Node: *node, Outputs: []string{}}
	for _, h := range node.History {
		detail.Outputs = append(detail.Outputs, h.URL)
	}

	// 只有生成节点才谈得上「派生输入」。
	if node.InternalType == "gen-image" || node.InternalType == "gen-video" {
		cs, err := canvasinputs.ParseSnapshot(snap.Payload)
		if err != nil {
			return NodeDetail{}, New(CodeInternal, "画布快照损坏").WithCause(err)
		}
		linked := canvasinputs.Connected(cs, nodeID, s.sign)
		detail.DerivedInputs = &DerivedInputs{
			Images: linked.Images, Texts: linked.Texts,
			Videos: linked.Videos, Audios: linked.Audios,
		}
	}
	return detail, nil
}

func (s *Service) ListEdges(userID, projectID int64) ([]Edge, error) {
	c, err := s.GetCanvas(userID, projectID)
	if err != nil {
		return nil, err
	}
	return c.Edges, nil
}
