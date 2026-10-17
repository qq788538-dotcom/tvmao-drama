package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// 与服务端 cliapi DTO 对应的类型。字段是公共契约，改动要同步 swagger。

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type HistoryItem struct {
	URL    string         `json:"url"`
	Prompt string         `json:"prompt,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	At     int64          `json:"at,omitempty"`
}

type NodeMedia struct {
	Kind    string `json:"kind,omitempty"`
	AssetID int64  `json:"assetId,omitempty"`
}

type Node struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	InternalType string         `json:"internalType"`
	Writable     bool           `json:"writable"`
	Position     Position       `json:"position"`
	Size         Size           `json:"size"`
	Status       string         `json:"status,omitempty"`
	Content      string         `json:"content,omitempty"`
	Error        string         `json:"error,omitempty"`
	TaskID       int64          `json:"taskId,omitempty"`
	Params       map[string]any `json:"params"`
	Media        *NodeMedia     `json:"media,omitempty"`
	History      []HistoryItem  `json:"history"`
}

// NodeDetail 是 `node get` 的响应：节点本体 + 运行前审计用的派生输入与已产出媒体。
type NodeDetail struct {
	Node
	DerivedInputs *DerivedInputs `json:"derivedInputs,omitempty"`
	Outputs       []string       `json:"outputs"`
}

type DerivedInputs struct {
	Images []string `json:"images"`
	Texts  []string `json:"texts"`
	Videos []string `json:"videos"`
	Audios []string `json:"audios"`
}

type Edge struct {
	ID         string `json:"id"`
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	InputType  string `json:"inputType,omitempty"`
}

type Canvas struct {
	Version int64  `json:"version"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

type Project struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	StorageBytes int64  `json:"storageBytes,omitempty"`
}

type Model struct {
	ModelID           string `json:"modelId"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
	InputSchema       any    `json:"inputSchema"`
}

type Run struct {
	TaskID  int64          `json:"taskId"`
	Status  string         `json:"status"`
	ModelID string         `json:"modelId,omitempty"`
	NodeID  string         `json:"nodeId,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
}

// Compliance 是一个节点素材的 Seedance 合规校验状态。
// Status: unverified（没提交过）/ pending / active（通过）/ failed / failed_permanent。
type Compliance struct {
	NodeID string `json:"nodeId"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type RunStatus struct {
	TaskID     int64    `json:"taskId"`
	Status     string   `json:"status"`
	OutputURLs []string `json:"outputUrls,omitempty"`
	AssetIDs   []int64  `json:"assetIds,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type Asset struct {
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	Deduped  bool   `json:"deduped,omitempty"`
}

// ---- 项目 ----

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out struct {
		Data []Project `json:"data"`
	}
	err := c.do(ctx, request{method: http.MethodGet, path: "/projects"}, &out)
	return out.Data, err
}

func (c *Client) GetProject(ctx context.Context, id int64) (Project, error) {
	var out Project
	err := c.do(ctx, request{method: http.MethodGet, path: "/projects/" + itoa(id)}, &out)
	return out, err
}

func (c *Client) CreateProject(ctx context.Context, name, desc, idemKey string) (Project, error) {
	var out Project
	err := c.do(ctx, request{
		method: http.MethodPost, path: "/projects",
		body:           map[string]string{"name": name, "description": desc},
		idempotencyKey: idemKey,
	}, &out)
	return out, err
}

func (c *Client) UpdateProject(ctx context.Context, id int64, name, desc *string) (Project, error) {
	body := map[string]any{}
	if name != nil {
		body["name"] = *name
	}
	if desc != nil {
		body["description"] = *desc
	}
	var out Project
	err := c.do(ctx, request{method: http.MethodPatch, path: "/projects/" + itoa(id), body: body}, &out)
	return out, err
}

func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	return c.do(ctx, request{method: http.MethodDelete, path: "/projects/" + itoa(id)}, nil)
}

func (c *Client) GetCanvas(ctx context.Context, projectID int64) (Canvas, error) {
	var out Canvas
	err := c.do(ctx, request{method: http.MethodGet, path: "/projects/" + itoa(projectID) + "/canvas"}, &out)
	return out, err
}

// ---- 节点 ----

func (c *Client) ListNodes(ctx context.Context, projectID int64, nodeType, status string) ([]Node, error) {
	q := url.Values{}
	if nodeType != "" {
		q.Set("type", nodeType)
	}
	if status != "" {
		q.Set("status", status)
	}
	var out struct {
		Data []Node `json:"data"`
	}
	err := c.do(ctx, request{
		method: http.MethodGet, path: "/projects/" + itoa(projectID) + "/nodes", query: q,
	}, &out)
	return out.Data, err
}

func (c *Client) GetNode(ctx context.Context, projectID int64, nodeID string) (NodeDetail, error) {
	var out NodeDetail
	err := c.do(ctx, request{
		method: http.MethodGet, path: "/projects/" + itoa(projectID) + "/nodes/" + url.PathEscape(nodeID),
	}, &out)
	return out, err
}

// ResolveNode 把引用换成节点 ID。所有 mutation 都先走它再按 ID 操作，
// 这样引用里的 / 空格 非 ASCII 字符不会进 URL path。
func (c *Client) ResolveNode(ctx context.Context, projectID int64, ref string) (string, error) {
	var out struct {
		NodeID string `json:"nodeId"`
	}
	err := c.do(ctx, request{
		method: http.MethodGet, path: "/projects/" + itoa(projectID) + "/nodes/resolve",
		query: url.Values{"ref": {ref}},
	}, &out)
	return out.NodeID, err
}

type CreateNodeInput struct {
	Type     string         `json:"type"`
	Position *Position      `json:"position,omitempty"`
	Size     *Size          `json:"size,omitempty"`
	Content  string         `json:"content,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

func (c *Client) CreateNode(ctx context.Context, projectID int64, in CreateNodeInput, idemKey string, expectVersion int64) (Node, error) {
	var out Node
	err := c.do(ctx, request{
		method: http.MethodPost, path: "/projects/" + itoa(projectID) + "/nodes",
		body: in, idempotencyKey: idemKey, ifMatchVersion: expectVersion,
	}, &out)
	return out, err
}

type PatchNodeInput struct {
	Position *Position      `json:"position,omitempty"`
	Size     *Size          `json:"size,omitempty"`
	Content  *string        `json:"content,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

func (c *Client) PatchNode(ctx context.Context, projectID int64, nodeID string, in PatchNodeInput, expectVersion int64) (Node, error) {
	var out Node
	err := c.do(ctx, request{
		method: http.MethodPatch, path: "/projects/" + itoa(projectID) + "/nodes/" + url.PathEscape(nodeID),
		body: in, ifMatchVersion: expectVersion,
	}, &out)
	return out, err
}

// GeometryPatch 是批量摆位的一个元素。nil 字段 = 不动，与服务端一致。
type GeometryPatch struct {
	ID     string   `json:"id"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
}

// MoveResult 带回新版本号，摆完位置接着写别的可直接用它做 --snapshot-version。
type MoveResult struct {
	Version int64  `json:"version"`
	Nodes   []Node `json:"nodes"`
}

// PatchNodePositions 一次请求改多个节点的几何。整批原子：任一 id 不存在全部不落地。
func (c *Client) PatchNodePositions(ctx context.Context, projectID int64, patches []GeometryPatch, expectVersion int64) (MoveResult, error) {
	var out MoveResult
	err := c.do(ctx, request{
		method: http.MethodPatch, path: "/projects/" + itoa(projectID) + "/nodes/positions",
		body: map[string]any{"nodes": patches}, ifMatchVersion: expectVersion,
	}, &out)
	return out, err
}

func (c *Client) DeleteNode(ctx context.Context, projectID int64, nodeID string, expectVersion int64) error {
	return c.do(ctx, request{
		method: http.MethodDelete, path: "/projects/" + itoa(projectID) + "/nodes/" + url.PathEscape(nodeID),
		ifMatchVersion: expectVersion,
	}, nil)
}

// ---- 连线 ----

func (c *Client) ListEdges(ctx context.Context, projectID int64) ([]Edge, error) {
	var out struct {
		Data []Edge `json:"data"`
	}
	err := c.do(ctx, request{method: http.MethodGet, path: "/projects/" + itoa(projectID) + "/edges"}, &out)
	return out.Data, err
}

func (c *Client) CreateEdge(ctx context.Context, projectID int64, from, to, idemKey string) (Edge, error) {
	var out Edge
	err := c.do(ctx, request{
		method: http.MethodPost, path: "/projects/" + itoa(projectID) + "/edges",
		body:           map[string]string{"fromNodeId": from, "toNodeId": to},
		idempotencyKey: idemKey,
	}, &out)
	return out, err
}

func (c *Client) DeleteEdge(ctx context.Context, projectID int64, edgeID string) error {
	return c.do(ctx, request{
		method: http.MethodDelete, path: "/projects/" + itoa(projectID) + "/edges/" + url.PathEscape(edgeID),
	}, nil)
}

// ---- 模型 ----

func (c *Client) ListModels(ctx context.Context, kind, q string) ([]Model, error) {
	query := url.Values{}
	if kind != "" {
		query.Set("type", kind)
	}
	if q != "" {
		query.Set("q", q)
	}
	var out struct {
		Data []Model `json:"data"`
	}
	err := c.do(ctx, request{method: http.MethodGet, path: "/models", query: query}, &out)
	return out.Data, err
}

func (c *Client) GetModel(ctx context.Context, ref string) (Model, error) {
	var out Model
	err := c.do(ctx, request{method: http.MethodGet, path: "/models/" + url.PathEscape(ref)}, &out)
	return out, err
}

// ---- 运行 ----

func (c *Client) RunNode(ctx context.Context, projectID int64, nodeID string, overrides map[string]any, idemKey string) (Run, error) {
	var out Run
	err := c.do(ctx, request{
		method:         http.MethodPost,
		path:           "/projects/" + itoa(projectID) + "/nodes/" + url.PathEscape(nodeID) + "/runs",
		body:           map[string]any{"overrides": overrides},
		idempotencyKey: idemKey,
	}, &out)
	return out, err
}

// ---- Seedance 2.0 合规校验 ----
//
// doubao-seedance* 的生成要求输入素材先过合规校验，否则任务直接失败。
// 入口是节点 ID：服务端从画布快照读那个节点的素材，CLI 这边不用碰对象存储 key，
// 也不用去搞一串会过期的签名 URL。

// VerifyCompliance 提交某个节点的素材去做合规校验。
// 服务端幂等：已通过且版本没变的重复提交不会再占上游配额。
func (c *Client) VerifyCompliance(ctx context.Context, projectID int64, nodeID string) (Compliance, error) {
	var out Compliance
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/projects/" + itoa(projectID) + "/nodes/" + url.PathEscape(nodeID) + "/compliance",
	}, &out)
	return out, err
}

// ListCompliance 列出项目里所有带素材节点的校验状态。
func (c *Client) ListCompliance(ctx context.Context, projectID int64) ([]Compliance, error) {
	var out []Compliance
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/projects/" + itoa(projectID) + "/compliance",
	}, &out)
	return out, err
}

func (c *Client) GetRun(ctx context.Context, taskID int64) (RunStatus, error) {
	var out RunStatus
	err := c.do(ctx, request{method: http.MethodGet, path: "/runs/" + itoa(taskID)}, &out)
	return out, err
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
