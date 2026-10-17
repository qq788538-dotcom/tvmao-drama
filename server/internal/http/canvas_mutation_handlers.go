package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/james/huabu_studio/apps/api/internal/canvasowner"
	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
	"github.com/james/huabu_studio/apps/api/internal/projects"
)

type canvasOwner interface {
	Claim(ctx context.Context, projectID int64, userID int64, actorUserID int64) (canvasowner.ClaimResult, error)
	Heartbeat(ctx context.Context, projectID int64, ownerToken string) error
	Require(ctx context.Context, projectID int64, ownerToken string) error
	Probe(ctx context.Context, projectID int64) (canvasowner.ProbeResult, error)
}

// canvasChangeNotifier 是 agent Hub 的窄子集：画布写成功后广播给该项目下所有连接
// （团队成员据此本地合并这笔变更，拿不到 op 摘要时退回重拉快照）。nil = 未装配，
// 跳过广播——单测与未 wire 场景下画布写入必须照常成功。
type canvasChangeNotifier interface {
	NotifyCanvasChanged(userID, projectID, version int64, sourceMark string, op *canvasstate.CanvasOpSummary)
}

type CanvasMutationHandler struct {
	repo     projects.Repository
	owner    canvasOwner
	signer   *MediaSigner
	notifier canvasChangeNotifier
}

func NewCanvasMutationHandler(repo projects.Repository, owner canvasOwner, signer *MediaSigner) *CanvasMutationHandler {
	return &CanvasMutationHandler{repo: repo, owner: owner, signer: signer}
}

// SetChangeNotifier 注入变更广播器。用 setter 而非改构造签名：广播是可选装配
// （agent Hub 在 main.go 里晚于本 handler 才具备），既有调用方一行不用改。
func (h *CanvasMutationHandler) SetChangeNotifier(n canvasChangeNotifier) { h.notifier = n }

type canvasOwnerTokenRequest struct {
	OwnerToken string `json:"ownerToken"`
}

type canvasUpsertNodeRequest struct {
	OwnerToken string          `json:"ownerToken"`
	Node       json.RawMessage `json:"node"`
}

type canvasPatchNodeRequest struct {
	OwnerToken string          `json:"ownerToken"`
	Patch      json.RawMessage `json:"patch"`
}

type canvasDeleteNodesRequest struct {
	OwnerToken string   `json:"ownerToken"`
	NodeIDs    []string `json:"nodeIds"`
}

type canvasPatchNodePositionsRequest struct {
	OwnerToken string                          `json:"ownerToken"`
	Nodes      []canvasstate.NodeGeometryPatch `json:"nodes"`
}

type canvasUpsertConnectionRequest struct {
	OwnerToken string          `json:"ownerToken"`
	Connection json.RawMessage `json:"connection"`
}

func (h *CanvasMutationHandler) ClaimOwner(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if !h.verifyProject(w, userID, projectID) {
		return
	}
	// actor 只被编进锁值供 Probe 展示「谁在编辑」；个人身份恒 0，抢占语义一字未改。
	result, err := h.owner.Claim(r.Context(), projectID, userID, actorUserIDFromContext(r.Context()))
	if err != nil {
		writeCanvasOwnerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type canvasOwnerProbeResponse struct {
	Held          bool  `json:"held"`
	EditorActorID int64 `json:"editorActorId"`
	IsSelf        bool  `json:"isSelf"`
	// StaleSeconds 距持锁者最后一次心跳的秒数，-1=未知，held=false 时恒 0。
	// 锁 TTL 24h 且关页面不释放，光看 held 会把全团队顶在只读里一整天；前端拿它
	// 判断「对方其实早走了」。心跳周期 10s，所以远大于它就是已离开。
	StaleSeconds int64 `json:"staleSeconds"`
}

// ProbeOwner 只读回答「这个项目现在有没有人在编辑、是不是我自己」。
//
// 为什么要单独一个 GET 而不是复用 claim：claim 是无条件抢占，团队成员打开一个别人
// 正在编辑的项目时必须**先看后决定**（默认进观看模式），拿 claim 探测等于每次打开
// 都把在编辑的同事踢下线。
func (h *CanvasMutationHandler) ProbeOwner(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if !h.verifyProject(w, userID, projectID) {
		return
	}
	res, err := h.owner.Probe(r.Context(), projectID)
	if err != nil {
		writeCanvasOwnerError(w, err) // Redis 不可用 → 503，与其它锁端点同一套错误模型
		return
	}
	if !res.Held {
		writeJSON(w, http.StatusOK, canvasOwnerProbeResponse{})
		return
	}
	// 「自己」比的是真人身份：团队身份下 userID 是全团队共用的影子账号，只比它的话
	// 同团队所有成员都会被判成自己人，观看模式永远进不去。
	me := actorUserIDFromContext(r.Context())
	if me == 0 {
		me = userID
	}
	holder := res.ActorUserID
	if holder == 0 {
		holder = res.UserID
	}
	writeJSON(w, http.StatusOK, canvasOwnerProbeResponse{
		Held:          true,
		EditorActorID: res.ActorUserID,
		IsSelf:        me == holder,
		StaleSeconds:  res.StaleSeconds,
	})
}

func (h *CanvasMutationHandler) HeartbeatOwner(w http.ResponseWriter, r *http.Request) {
	var body canvasOwnerTokenRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if !h.verifyProject(w, userID, projectID) {
		return
	}
	if err := h.owner.Heartbeat(r.Context(), projectID, body.OwnerToken); err != nil {
		if writeCanvasOwnerError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *CanvasMutationHandler) UpsertNode(w http.ResponseWriter, r *http.Request) {
	var body canvasUpsertNodeRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil || len(body.Node) == 0 {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	node := h.canonicalize(body.Node)
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.UpsertNode(payload, node)
	})
}

func (h *CanvasMutationHandler) PatchNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeID")
	var body canvasPatchNodeRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil || nodeID == "" || len(body.Patch) == 0 {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	patch := h.canonicalize(body.Patch)
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.PatchNode(payload, nodeID, patch)
	})
}

func (h *CanvasMutationHandler) DeleteNodes(w http.ResponseWriter, r *http.Request) {
	var body canvasDeleteNodesRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.DeleteNodes(payload, body.NodeIDs)
	})
}

func (h *CanvasMutationHandler) PatchNodePositions(w http.ResponseWriter, r *http.Request) {
	var body canvasPatchNodePositionsRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.PatchNodePositions(payload, body.Nodes)
	})
}

func (h *CanvasMutationHandler) UpsertConnection(w http.ResponseWriter, r *http.Request) {
	var body canvasUpsertConnectionRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil || len(body.Connection) == 0 {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	connection := h.canonicalize(body.Connection)
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.UpsertConnection(payload, connection)
	})
}

func (h *CanvasMutationHandler) DeleteConnection(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "connectionID")
	var body canvasOwnerTokenRequest
	if err := decodeCanvasMutationJSON(r, &body); err != nil || connectionID == "" {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	h.mutate(w, r, body.OwnerToken, func(payload json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error) {
		return canvasstate.DeleteConnection(payload, connectionID)
	})
}

func (h *CanvasMutationHandler) mutate(w http.ResponseWriter, r *http.Request, ownerToken string, fn func(json.RawMessage) (json.RawMessage, bool, canvasstate.CanvasOpSummary, error)) {
	userID := userIDFromContext(r.Context())
	projectID := projectIDFromContext(r.Context())
	if !h.verifyProject(w, userID, projectID) {
		return
	}
	// 团队身份（actor>0）**不校验单人编辑锁**——现在去锁并发写。
	//
	// 那服务端写入靠什么串行化？靠 MutateClientSnapshot 的
	// `SELECT … FOR UPDATE`：并发写在 MySQL 行锁上排队，一次读-改-写整体原子，
	// 谁也丢不了更新。单人锁解决的从来不是「同时写会写坏」，而是「整快照覆盖式
	// 保存会互相顶掉」——那套保存路径已经不用了，写入全是节点级/字段级 op。
	//
	// 个人身份（actor==0）逐字保持旧版行为：锁、心跳、失锁弹窗、探测一条不动。
	// 个人多标签页仍然是「后开的接管编辑权」，那条 UI 与语义不在本次改动范围内。
	if actorUserIDFromContext(r.Context()) == 0 {
		if err := h.owner.Require(r.Context(), projectID, ownerToken); err != nil {
			if writeCanvasOwnerError(w, err) {
				return
			}
		}
	}
	// 摘要在闭包里截获：MutateClientSnapshot 的回调签名是既有的三元组，为一个
	// 广播字段去改 repo 层签名（另有 CLI 等调用方）不划算。changed 一并截获——
	// no-op 不发帧（见下）。
	var summary canvasstate.CanvasOpSummary
	var changed bool
	version, err := h.repo.MutateClientSnapshot(projectID, func(payload json.RawMessage) (json.RawMessage, bool, error) {
		next, opChanged, opSummary, opErr := fn(payload)
		summary, changed = opSummary, opChanged
		return next, opChanged, opErr
	})
	if err != nil {
		if writeCanvasMutationError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// **真的改了才广播**：no-op（服务端状态本来就等于目标）不涨 version，此时发帧会
	// 造出「同一个 version 两条内容不同的帧」。接收端靠 version==local+1 判断有没有
	// 丢帧——某条连接被 2s 写超时跳过一帧后，下一条 no-op 帧正好满足这个等式，缺口
	// 检测就静默失效，两端从此分叉。后端回写路径（writeback 的 if changed）一直是
	// 这个形态，这里对齐它。
	//
	// 写成功即广播：同项目的其它连接据此**本地重放**这一笔变更（拿不到 op 时退回
	// 整拉快照）。sourceMark 是发起方 ownerToken 的 8 位指纹，发起方自己收到时按它
	// 跳过回声；广播里绝不带完整 token（那等于把编辑权凭据发给所有观看者）。
	//
	// ⚠️ 团队路径的硬约束：ownerToken 在团队分支已不做校验，但它仍然是 source_mark
	// 的唯一来源，所以**每个标签页必须发一个各不相同的值**（约定 "team:<随机8位hex>"）。
	// 若全体团队客户端都发同一个常量（比如裸的 "team"），所有人的 source_mark 会撞成
	// 一个值，每个人都会把同伴的 op 当成自己的回声跳过——协作直接静默失效。
	// 服务端故意不替客户端造这个值：它要**跨请求稳定**地标识「发起它的那个标签页」，
	// 而 HTTP 请求本身不携带这样的身份。
	if h.notifier != nil && changed {
		var op *canvasstate.CanvasOpSummary
		if summary.Kind != "" {
			signed := signOpSummary(h.signer, summary)
			op = &signed
		}
		h.notifier.NotifyCanvasChanged(userID, projectID, version, canvasowner.SourceMark(ownerToken), op)
	}
	writeJSON(w, http.StatusOK, snapshotVersionResponse{Version: version})
}

// signOpSummary 把摘要里的媒体引用换成新鲜的短时签名 URL。
//
// 为什么必须做：快照里存的是 canonical 的 `media://<key>`，旧版同伴走 GET 快照，
// 那条路上有 SignJSON（project_handlers.go 的 GetSnapshot）——签名是**唯一出口**。
// 新版的本地合并绕开了那个出口：前端把 node.content 直接当 <img src> 用，而浏览器
// 不认 media:// 协议，于是「A 生成一张图 → B 画布上这张图整块裂开，刷新才好」，
// 且没有任何报错。摘要发出去之前补签这一次，等于把 op 帧也接进同一个出口。
//
// 只转换发出去的副本，落库的字节仍是 canonical：签名 URL 有时效，写进快照等于
// 埋一堆几小时后失效的死链。
func signOpSummary(signer *MediaSigner, summary canvasstate.CanvasOpSummary) canvasstate.CanvasOpSummary {
	if signer == nil {
		return summary
	}
	out := summary
	out.Node = signer.SignJSON(summary.Node)
	out.Patch = signer.SignJSON(summary.Patch)
	out.Connection = signer.SignJSON(summary.Connection)
	// 其余字段（node_id / node_ids / positions / connection_id）是 id 与数字，
	// 不可能承载媒体引用，不必也不该过签名器。
	return out
}

func (h *CanvasMutationHandler) verifyProject(w http.ResponseWriter, userID, projectID int64) bool {
	if _, err := h.repo.GetForUser(userID, projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

func (h *CanvasMutationHandler) canonicalize(raw json.RawMessage) json.RawMessage {
	if h.signer == nil {
		return raw
	}
	return h.signer.CanonicalizeJSON(raw)
}

func decodeCanvasMutationJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeCanvasOwnerError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, canvasowner.ErrOwnerExpired):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "画布编辑权已过期，请刷新后重试",
			"code":  "CANVAS_OWNER_EXPIRED",
		})
		return true
	case errors.Is(err, canvasowner.ErrOwnerLost):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "此画布已在其他页面继续编辑，请刷新",
			"code":  "CANVAS_OWNER_LOST",
		})
		return true
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "画布编辑锁暂不可用，请稍后重试",
			"code":  "CANVAS_OWNER_UNAVAILABLE",
		})
		return true
	}
}

func writeCanvasMutationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, canvasstate.ErrNodeNotFound):
		writeError(w, http.StatusNotFound, "node not found")
		return true
	case errors.Is(err, canvasstate.ErrConnectionNotFound):
		writeError(w, http.StatusNotFound, "connection not found")
		return true
	case errors.Is(err, canvasstate.ErrInvalidPayload):
		writeError(w, http.StatusBadRequest, "invalid body")
		return true
	default:
		return false
	}
}
