package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	"github.com/james/huabu_studio/apps/api/internal/mediaprobe"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// maxDesubDownloadBytes 限制懒回填时落临时文件的源视频体积上限（只在对象不是本地文件、
// 需要先流式落盘时生效；本地 /media 文件原地探测，不受此限）。
const maxDesubDownloadBytes = 500 << 20 // 500MB

// postprocessProbeTimeout 是一次懒回填（打开/下载 + 探测）的总时限。本地 /media 卡住或远端
// 不回包时，请求不能无限挂住。var 而非 const：测试缩短它。
var postprocessProbeTimeout = 2 * time.Minute

// postprocessDownloadClient 是 HTTP 回退路径专用 client：带整体超时，作为 ctx 超时之外的第二道保险。
var postprocessDownloadClient = &http.Client{Timeout: 5 * time.Minute}

// postprocessObjects 是 assets.Storage 在这里用到的方法：按 key 直接打开存储里的对象。
// 本地存储返回 *os.File，探测器原地读，不经 HTTP、不进内存。
type postprocessObjects interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

// canvasPostprocessEnqueuer 是 canvasgen.Service 的窄接口——去字幕产物入队用。
type canvasPostprocessEnqueuer interface {
	Enqueue(ctx context.Context, in canvasgen.EnqueueInput) (int64, error)
}

// canvasPostprocessProjects 是 projects.Repository 的窄接口：项目归属校验 + 读最新快照定位源节点。
type canvasPostprocessProjects interface {
	GetForUser(userID, projectID int64) (*projects.Project, error)
	GetLatest(projectID int64) (*projects.Snapshot, error)
}

// canvasPostprocessAssets 是 assets.Repository 的窄接口：按 object_key 读/回填 duration_ms。
type canvasPostprocessAssets interface {
	GetDurationMsByObjectKey(objectKey string) (int64, bool, error)
	SetDurationMsByObjectKey(objectKey string, durationMs int64) error
}

// canvasPostprocessGeneration 是 generation.Service 的窄接口：反查限时源任务的 upstreamUrls。
type canvasPostprocessGeneration interface {
	Get(ctx context.Context, userID, taskID int64) (*generation.Task, error)
}

// postprocessModelLookup 按 modelId 取模型配置（含隐藏）。参考仓定义在
// drama_agent_task_handlers.go，桌面端不移植 drama，搬到这里。
type postprocessModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// postprocessSigner 是 *MediaSigner 在这里用到的两个方法（P1b 提供实现）。用窄接口而不是
// *MediaSigner，本文件可以先于 P1b 合并编译，测试用替身。
type postprocessSigner interface {
	ObjectKey(string) string
	Sign(string) string
}

// CanvasPostprocessHandler 管画布去字幕白名单 + 去字幕提交 + 后处理预估——自由画布独立设施，
// 不复用 drama 的 drama_agent_config(平级隔离，见 CLAUDE.md「平级独立原则」)。
type CanvasPostprocessHandler struct {
	settings settings.Repository
	canvas   canvasPostprocessEnqueuer
	projects canvasPostprocessProjects
	assets   canvasPostprocessAssets
	gen      canvasPostprocessGeneration
	// models 只为预估服务：读模型的 billing_config 在后端算积分。提交路径不需要它
	// （计费由 generation 侧统一做），故这个依赖是预估端点引入的。
	models postprocessModelLookup
	signer postprocessSigner
	// objects 非 nil 时，懒回填优先按 object key 直接打开存储里的文件（见 WithObjectStore）。
	objects postprocessObjects
	// download/probe 是懒回填时长的两步：拿到源视频的流（对象存储打不开时才走 HTTP）→ 探测
	// 磁盘上的文件（先试系统 ffprobe，没有就只读 MP4 的 moov；桌面端用户机器通常没有 ffprobe）。
	// 拆成可替换字段，测试注入 fake。
	download func(ctx context.Context, url string) (io.ReadCloser, error)
	probe    func(ctx context.Context, path string) (int64, error)
	// probes 合并同一 object key 的并发探测：多个节点同时点预估/提交只下载探测一次。
	probes singleflight.Group
}

func NewCanvasPostprocessHandler(
	s settings.Repository,
	canvas canvasPostprocessEnqueuer,
	projectRepo canvasPostprocessProjects,
	assetsRepo canvasPostprocessAssets,
	gen canvasPostprocessGeneration,
	modelRepo postprocessModelLookup,
	signer postprocessSigner,
) *CanvasPostprocessHandler {
	return &CanvasPostprocessHandler{
		settings: s,
		canvas:   canvas,
		projects: projectRepo,
		assets:   assetsRepo,
		gen:      gen,
		models:   modelRepo,
		signer:   signer,
		download: downloadMedia,
		probe:    mediaprobe.ProbeDurationMsPortableFile,
	}
}

// WithObjectStore 让懒回填按 object key 直接从存储打开源视频（本地 /media 即磁盘文件，原地探测）。
// 不设置时退回用签名 URL 走 HTTP 下载。
func (h *CanvasPostprocessHandler) WithObjectStore(objects postprocessObjects) *CanvasPostprocessHandler {
	h.objects = objects
	return h
}

// loadConfig 读后处理配置；读失败或存量数据损坏时记日志并按“未配置”降级——
// 一份坏配置不能让所有后处理接口 500（同 T1 各 handler 的 load）。
func (h *CanvasPostprocessHandler) loadConfig() canvasgen.CanvasPostprocessConfig {
	cfg, err := canvasgen.LoadCanvasPostprocessConfig(h.settings.Get)
	if err != nil {
		slog.Warn("画布后处理配置读取失败，按未配置处理", "err", err)
		return canvasgen.CanvasPostprocessConfig{}
	}
	return cfg
}

// GetConfig GET /canvas/postprocess-config（需登录，只读）：画布去字幕白名单 + 转高清/转白模的
// 模型与档位，前端节点的可选项都来自这里。
//
// 档位在服务端从模型 uiSchema 读出再下发（而非让前端查 /models）：这样转高清/转白模模型
// 可以在模型中心保持「隐藏」——它们不该出现在生视频的模型选择列表里污染选项，但节点仍要
// 能显示 1080p/2K/4K 档位。仿 drama 的 upscaleResolutionOptions 同一思路。
func (h *CanvasPostprocessHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadConfig()
	subs := make([]map[string]any, 0, len(cfg.SubtitleRemovalModels))
	for _, d := range cfg.SubtitleRemovalModels {
		subs = append(subs, map[string]any{"modelId": d.ModelID, "label": d.Label, "subtitle": d.Subtitle, "isTimeLimited": d.IsTimeLimited})
	}
	ups := make([]map[string]any, 0, len(cfg.UpscaleModels))
	for _, u := range cfg.UpscaleModels {
		res := h.resolutionOptions(u.ModelID)
		// 没有档位的模型提交必被拒(档位是必选项)，列进下拉框只会让用户选了点不动，直接不下发。
		if len(res) == 0 {
			continue
		}
		label := u.Label
		if label == "" {
			label = u.ModelID
		}
		ups = append(ups, map[string]any{"modelId": u.ModelID, "label": label, "resolutions": res})
	}
	// upscaleModel/upscaleResolutions 是单模型时代的字段，填第一个模型：发版时浏览器里还开着
	// 旧页面的用户，旧代码读这两个字段仍能正常预估和提交(第一个模型在白名单里)。
	legacyModel, legacyRes := "", []map[string]any{}
	if len(ups) > 0 {
		legacyModel, _ = ups[0]["modelId"].(string)
		legacyRes, _ = ups[0]["resolutions"].([]map[string]any)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subtitleRemoval":       subs,
		"upscaleModels":         ups,
		"upscaleModel":          legacyModel,
		"upscaleResolutions":    legacyRes,
		"whiteModelModel":       cfg.WhiteModelModel,
		"whiteModelResolutions": h.resolutionOptions(cfg.WhiteModelModel),
	})
}

// resolutionOptions 读模型 uiSchema 的 resolution.options，转成 [{value,label}]。
// 模型未配 / 查不到 / 无该字段 → 空数组，前端据此显示「未配置…模型」并禁用提交。
// 这份列表同时是预估端点校验 resolution 的白名单来源(见 Estimate 的注释)。
func (h *CanvasPostprocessHandler) resolutionOptions(modelID string) []map[string]any {
	out := make([]map[string]any, 0)
	if h.models == nil || modelID == "" {
		return out
	}
	m, err := h.models.GetByModelID(modelID)
	if err != nil || m == nil || len(m.UISchema) == 0 {
		return out
	}
	var ui struct {
		Resolution struct {
			Options []struct {
				Value any    `json:"value"`
				Label string `json:"label"`
			} `json:"options"`
		} `json:"resolution"`
	}
	if json.Unmarshal(m.UISchema, &ui) != nil {
		return out
	}
	for _, o := range ui.Resolution.Options {
		out = append(out, map[string]any{"value": o.Value, "label": o.Label})
	}
	return out
}

// resolutionAllowed 判断一个 resolution 值是否在该模型的档位白名单内。
// 按字符串形态比对——uiSchema 的 option value 可能是数字或字符串，而 HTTP 请求体里
// 过来的通常是字符串（踩过一次枚举类型不一致的坑，见 generation/uischema_inputs.go）。
func (h *CanvasPostprocessHandler) resolutionAllowed(modelID, resolution string) bool {
	for _, o := range h.resolutionOptions(modelID) {
		if fmt.Sprintf("%v", o["value"]) == resolution {
			return true
		}
	}
	return false
}

// AdminGet GET /admin/canvas/postprocess-config：回显整份配置，admin-only。
// 走 LoadCanvasPostprocessConfig 而非原样回显 settings：存量的旧版单模型 upscaleModel
// 要折成列表第一行给后台展示，管理员补上显示名保存后就只剩新格式。
func (h *CanvasPostprocessHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadConfig()
	// nil 切片会序列化成 null，后台表单按数组读，统一给空数组。
	if cfg.SubtitleRemovalModels == nil {
		cfg.SubtitleRemovalModels = []canvasgen.CanvasDesubBinding{}
	}
	if cfg.UpscaleModels == nil {
		cfg.UpscaleModels = []canvasgen.CanvasUpscaleBinding{}
	}
	writeJSON(w, http.StatusOK, cfg)
}

// AdminPut PUT /admin/canvas/postprocess-config：整份覆盖存 settings。
// 先按结构体反序列化（类型不对 400），逐项校验后用结构体重新序列化落库——存进去的永远是
// LoadCanvasPostprocessConfig 能读回的形状，多余字段丢弃（校验口径同 T1 的三个 AdminPut）。
func (h *CanvasPostprocessHandler) AdminPut(w http.ResponseWriter, r *http.Request) {
	var body canvasgen.CanvasPostprocessConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 旧版单模型字段折进列表（与加载口径一致），落库只留新格式。
	if len(body.UpscaleModels) == 0 && strings.TrimSpace(body.UpscaleModel) != "" {
		body.UpscaleModels = []canvasgen.CanvasUpscaleBinding{{ModelID: body.UpscaleModel}}
	}
	body.UpscaleModel = ""
	if body.SubtitleRemovalModels == nil {
		body.SubtitleRemovalModels = []canvasgen.CanvasDesubBinding{}
	}
	if body.UpscaleModels == nil {
		body.UpscaleModels = []canvasgen.CanvasUpscaleBinding{}
	}
	body.WhiteModelModel = strings.TrimSpace(body.WhiteModelModel)
	seen := map[string]bool{}
	for i := range body.SubtitleRemovalModels {
		d := &body.SubtitleRemovalModels[i]
		d.ModelID = strings.TrimSpace(d.ModelID)
		if d.ModelID == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("去字幕第 %d 条：模型 ID 不能为空", i+1))
			return
		}
		if seen[d.ModelID] {
			writeError(w, http.StatusBadRequest, "去字幕模型重复："+d.ModelID)
			return
		}
		seen[d.ModelID] = true
	}
	seen = map[string]bool{}
	for i := range body.UpscaleModels {
		u := &body.UpscaleModels[i]
		u.ModelID = strings.TrimSpace(u.ModelID)
		if u.ModelID == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("转高清第 %d 条：模型 ID 不能为空", i+1))
			return
		}
		if seen[u.ModelID] {
			writeError(w, http.StatusBadRequest, "转高清模型重复："+u.ModelID)
			return
		}
		seen[u.ModelID] = true
	}
	blob, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.settings.Set(canvasgen.SettingCanvasPostprocess, string(blob)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// canvasDesubRequest 是 POST /canvas-tasks/desub 的请求体。
type canvasDesubRequest struct {
	ProjectID    int64  `json:"projectId"`
	NodeID       string `json:"nodeId"`
	SourceNodeID string `json:"sourceNodeId"`
	ModelID      string `json:"modelId"`
}

// canvasSourceNode 是从快照里定位到的源节点的最小子集：content(媒体引用) + settings(限时元数据)。
type canvasSourceNode struct {
	Content  string
	Settings map[string]any
}

// Desub POST /canvas-tasks/desub {projectId,nodeId,sourceNodeId,modelId}：对画布里一个已完成的
// 视频节点提交去字幕，产物落 nodeId 指向的擦除字幕节点。限时模型走 22h 内的上游直连地址(反查
// generation_tasks.output.upstreamUrls，裸地址只在本函数内用于组 inputs，不写快照/不返回前端)；
// 其余走本地对象存储签名地址。计费秒数服务端权威：优先读 assets.duration_ms，NULL 则懒下载探测
// 并回填，仍拿不到直接拒绝(不估算，防篡改薅按秒计费——见 mediaprobe 包注释)。桌面端不计费（spec X3），服务端时长仍是上游请求里 `duration` 的唯一来源。
func (h *CanvasPostprocessHandler) Desub(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var body canvasDesubRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.NodeID == "" || body.SourceNodeID == "" || body.ModelID == "" {
		writeError(w, http.StatusBadRequest, "projectId/nodeId/sourceNodeId/modelId required")
		return
	}
	if _, err := h.projects.GetForUser(userID, body.ProjectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg := h.loadConfig()
	dm, ok := cfg.DesubModel(body.ModelID)
	if !ok {
		writeError(w, http.StatusBadRequest, "未知去字幕模型")
		return
	}
	src, ok := h.findSourceNode(body.ProjectID, body.SourceNodeID)
	if !ok {
		writeError(w, http.StatusBadRequest, "源视频未就绪")
		return
	}
	var srcURL string
	if dm.IsTimeLimited {
		// 限时：必须反查到未过期的 22h 上游直连地址，否则拒绝(防页面停留过久过期仍提交)。
		expire, _ := src.Settings["upstreamExpire"].(string)
		taskID := toInt64(src.Settings["srcGenTaskId"])
		srcURL = h.liveUpstreamURL(r.Context(), userID, taskID, expire)
		if srcURL == "" {
			writeError(w, http.StatusBadRequest, "暂不支持限时去字幕")
			return
		}
	} else {
		// src.Content 已是 canonical 形式(media://key，见 generation.persistOutputs)，Sign 直接吃它；
		// 先用 ObjectKey 探一下"认不认识"，认不出说明源还没落对象存储。
		if h.signer.ObjectKey(src.Content) == "" {
			writeError(w, http.StatusBadRequest, "源视频未就绪")
			return
		}
		srcURL = h.signer.Sign(src.Content)
	}
	durMs, err := h.resolveDurationMs(r.Context(), src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法获取视频时长")
		return
	}
	inputs := map[string]any{"video": srcURL, "duration": float64(durMs) / 1000.0}
	taskID, err := h.canvas.Enqueue(r.Context(), canvasgen.EnqueueInput{
		UserID:    userID,
		ProjectID: body.ProjectID,
		NodeID:    body.NodeID,
		ModelID:   body.ModelID,
		Inputs:    inputs,
	})
	if err != nil {
		// 存储已满是用户可自解的前置条件，409 而不是把它裹进 500。
		if errors.Is(err, generation.ErrStorageQuotaExceeded) {
			writeError(w, http.StatusConflict, "存储空间已满，请清理后重试再生成")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": taskID, "status": "processing"})
}

// canvasTieredRequest 是 POST /canvas-tasks/upscale 与 /canvas-tasks/whitemodel 共用的请求体。
// 同 Desub：**没有** duration 字段——计费秒数只能来自服务端。
type canvasTieredRequest struct {
	ProjectID    int64  `json:"projectId"`
	NodeID       string `json:"nodeId"`
	SourceNodeID string `json:"sourceNodeId"`
	ModelID      string `json:"modelId"`
	Resolution   string `json:"resolution"`
}

// Upscale POST /canvas-tasks/upscale {projectId,nodeId,sourceNodeId,modelId,resolution}：
// 对画布里一个已完成的视频节点提交转高清，产物落 nodeId 指向的转高清节点。
func (h *CanvasPostprocessHandler) Upscale(w http.ResponseWriter, r *http.Request) {
	h.submitTiered(w, r, func(c canvasgen.CanvasPostprocessConfig, modelID string) bool {
		_, ok := c.UpscaleBinding(modelID)
		return ok
	}, "未知转高清模型")
}

// WhiteModel POST /canvas-tasks/whitemodel {projectId,nodeId,sourceNodeId,modelId,resolution}：
// 对画布里一个已完成的视频节点提交转白模，产物落 nodeId 指向的转白模节点。
// 与转高清只差「后台配的是哪个模型」，故共用 submitTiered 而不是复制一份——两份的话，
// 计费秒数、源地址校验这些防薅口径迟早会分叉。
func (h *CanvasPostprocessHandler) WhiteModel(w http.ResponseWriter, r *http.Request) {
	h.submitTiered(w, r, func(c canvasgen.CanvasPostprocessConfig, modelID string) bool {
		// 未配置(空串)时也拒——避免空串 == 空串意外放行任意请求。
		return c.WhiteModelModel != "" && modelID == c.WhiteModelModel
	}, "未知转白模模型")
}

// submitTiered 是「模型 + 分辨率档位」类后处理(转高清 / 转白模)的提交实现，
// modelAllowed 判断请求的模型是否在本端点的白名单内(转高清是列表，转白模是单个)。
//
// 与 Desub 的差异：(a) 没有「限时上游直连」分支——只吃对象存储里已落地的视频；
// (b) 多一个 resolution 档位参数。相同点是计费秒数走同一个 resolveDurationMs(服务端权威，
// 拿不到直接拒绝、不估算兜底)，故按秒计费薅不到。
//
// resolution 的越界校验交给 generation.Submit 的 validateInputsAgainstUISchema——那是所有
// 走 generation 的提交入口的统一关口，此处不重复。（但预估端点必须自己校验，因为它不走
// Submit，见 Estimate 的注释。）
func (h *CanvasPostprocessHandler) submitTiered(
	w http.ResponseWriter,
	r *http.Request,
	modelAllowed func(cfg canvasgen.CanvasPostprocessConfig, modelID string) bool,
	unknownModelMsg string,
) {
	userID := userIDFromContext(r.Context())
	var body canvasTieredRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.NodeID == "" || body.SourceNodeID == "" || body.ModelID == "" {
		writeError(w, http.StatusBadRequest, "projectId/nodeId/sourceNodeId/modelId required")
		return
	}
	if _, err := h.projects.GetForUser(userID, body.ProjectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg := h.loadConfig()
	if !modelAllowed(cfg, body.ModelID) {
		writeError(w, http.StatusBadRequest, unknownModelMsg)
		return
	}
	src, ok := h.findSourceNode(body.ProjectID, body.SourceNodeID)
	if !ok {
		writeError(w, http.StatusBadRequest, "源视频未就绪")
		return
	}
	// 只走对象存储签名地址。ObjectKey 认不出说明源还没落存储(如上游产物还没转存完)。
	if h.signer.ObjectKey(src.Content) == "" {
		writeError(w, http.StatusBadRequest, "源视频未就绪")
		return
	}
	durMs, err := h.resolveDurationMs(r.Context(), src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法获取视频时长")
		return
	}
	inputs := map[string]any{"video": h.signer.Sign(src.Content), "duration": float64(durMs) / 1000.0}
	if body.Resolution != "" {
		inputs["resolution"] = body.Resolution
	}
	taskID, err := h.canvas.Enqueue(r.Context(), canvasgen.EnqueueInput{
		UserID:    userID,
		ProjectID: body.ProjectID,
		NodeID:    body.NodeID,
		ModelID:   body.ModelID,
		Inputs:    inputs,
	})
	if err != nil {
		// 同 Desub：存储已满 409，用户清理后可重试。
		if errors.Is(err, generation.ErrStorageQuotaExceeded) {
			writeError(w, http.StatusConflict, "存储空间已满，请清理后重试再生成")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": taskID, "status": "processing"})
}

// canvasPostprocessEstimateRequest 是 POST /canvas-tasks/postprocess-estimate 的请求体。
// 注意这里**没有** duration 字段——秒数是计费量，只能来自服务端。客户端即使在 JSON 里
// 塞了 duration 也会被忽略(结构体不接这个键)，这是本端点存在的首要理由。
type canvasPostprocessEstimateRequest struct {
	ProjectID    int64  `json:"projectId"`
	SourceNodeID string `json:"sourceNodeId"`
	ModelID      string `json:"modelId"`
	// Resolution 给转高清/转白模用(档位影响单价)；去字幕不传。空串 = 不参与计费。
	Resolution string `json:"resolution"`
}

// Estimate POST /canvas-tasks/postprocess-estimate {projectId,sourceNodeId,modelId,resolution?}
// → {credits,durationMs}：后处理(去字幕/转高清)的预计积分，由后端权威计算。
//
// 为什么预估必须在后端算：上一轮实现让前端读 <video>.duration 去调 /models/estimate，
// 那是浏览器解析出的**容器声明时长**，可被工具单独篡改——用户把一个两分多钟的视频
// 声明成 1 秒，就看到「预计 1 积分」而实际按真实时长结算。旧 spec 早已规定「预估与
// 结算统一读权威秒数」，但只落实了结算那一半，因为它没指明前端从哪拿到权威秒数。
// 现在把「构造计费 req」整件事收进后端一处，结构上就不可能再分叉。
//
// 与提交路径共用 resolveDurationMs：预估看到的秒数与提交时实际计费的秒数是同一个来源，
// 而不是两段各自实现的逻辑。拿不到权威时长一律 400，不估算兜底(无依据的预估等于骗人)。
func (h *CanvasPostprocessHandler) Estimate(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	var body canvasPostprocessEstimateRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.ProjectID == 0 || body.SourceNodeID == "" || body.ModelID == "" {
		writeError(w, http.StatusBadRequest, "projectId/sourceNodeId/modelId required")
		return
	}
	if _, err := h.projects.GetForUser(userID, body.ProjectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg := h.loadConfig()
	// 白名单校验：只有后台配过的后处理模型能问价。否则这个端点就成了任意模型的
	// 价格试探接口(模型中心里隐藏的模型本不该被外部枚举)。
	_, isDesub := cfg.DesubModel(body.ModelID)
	_, isUpscale := cfg.UpscaleBinding(body.ModelID)
	isWhiteModel := cfg.WhiteModelModel != "" && body.ModelID == cfg.WhiteModelModel
	if !isDesub && !isUpscale && !isWhiteModel {
		writeError(w, http.StatusBadRequest, "未知后处理模型")
		return
	}
	// resolution 是计费参数(档位影响单价)，而**预估不经过 generation.Submit**，那条
	// validateInputsAgainstUISchema 白名单校验在这条路上根本不会执行。若放行一个 uiSchema
	// 里不存在的档位，分级计费匹配不到任何级，会算出 0 或最低档的价——提交时虽会被 Submit
	// 拒掉、薅不到钱，但预估与提交又成了两套口径，正是本次要消灭的那类错误。故此处自查一遍。
	// 按**被问价的模型自己的** uiSchema 查——转高清和转白模各有各的档位表，拿错表会放行越界档位。
	if body.Resolution != "" && !h.resolutionAllowed(body.ModelID, body.Resolution) {
		writeError(w, http.StatusBadRequest, "未知档位")
		return
	}
	src, ok := h.findSourceNode(body.ProjectID, body.SourceNodeID)
	if !ok {
		writeError(w, http.StatusBadRequest, "源视频未就绪")
		return
	}
	durMs, err := h.resolveDurationMs(r.Context(), src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法获取视频时长")
		return
	}
	inputs := map[string]any{"duration": float64(durMs) / 1000.0}
	if body.Resolution != "" {
		inputs["resolution"] = body.Resolution
	}
	credits := h.estimateCredits(body.ModelID, inputs)
	writeJSON(w, http.StatusOK, map[string]any{"credits": credits, "durationMs": durMs})
}

// estimateCredits 用模型的 billing_config 算预冻结积分。走的是 billing.EstimateFreeze——
// 与 /models/estimate(admin_model_handlers.go)、与真实冻结用的是同一个函数，不另写一套算法。
// 模型查不到返回 0(与 RuntimeModelHandler.Estimate 的降级一致：白名单已过，说明是配置漂移)。
func (h *CanvasPostprocessHandler) estimateCredits(modelID string, inputs map[string]any) int64 {
	if h.models == nil {
		return 0
	}
	m, err := h.models.GetByModelID(modelID)
	if err != nil || m == nil {
		return 0
	}
	return billing.EstimateFreeze(m.BillingConfig(), map[string]any{"inputs": inputs})
}

// findSourceNode 读项目最新画布快照，按 id 定位源节点，取其 content/settings。
// 找不到快照/节点 → false（handler 据此回 400「源视频未就绪」）。
func (h *CanvasPostprocessHandler) findSourceNode(projectID int64, nodeID string) (canvasSourceNode, bool) {
	snap, err := h.projects.GetLatest(projectID)
	if err != nil || snap == nil || len(snap.Payload) == 0 {
		return canvasSourceNode{}, false
	}
	var doc map[string]any
	if json.Unmarshal(snap.Payload, &doc) != nil {
		return canvasSourceNode{}, false
	}
	nodes, _ := doc["nodes"].([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || node["id"] != nodeID {
			continue
		}
		content, _ := node["content"].(string)
		settingsMap, _ := node["settings"].(map[string]any)
		return canvasSourceNode{Content: content, Settings: settingsMap}, true
	}
	return canvasSourceNode{}, false
}

// liveUpstreamURL 限时去字幕的源地址：expire 必须能解析且未过期，taskID 必须能反查到
// generation_tasks.output.upstreamUrls[0]。任一步失败返回空串——裸地址只活在这个函数的返回值里，
// 调用方仅用它组一次性的 inputs，绝不落盘/绝不回给前端。
func (h *CanvasPostprocessHandler) liveUpstreamURL(ctx context.Context, userID, taskID int64, expireRFC3339 string) string {
	if taskID == 0 || expireRFC3339 == "" {
		return ""
	}
	expire, err := time.Parse(time.RFC3339, expireRFC3339)
	if err != nil || !time.Now().Before(expire) {
		return ""
	}
	task, err := h.gen.Get(ctx, userID, taskID)
	if err != nil || task == nil {
		return ""
	}
	var out struct {
		UpstreamURLs []string `json:"upstreamUrls"`
	}
	if json.Unmarshal(task.Output, &out) != nil || len(out.UpstreamURLs) == 0 {
		return ""
	}
	return out.UpstreamURLs[0]
}

// resolveDurationMs 服务端权威时长(毫秒)：优先读 assets.duration_ms；NULL 则懒回填——
// 拿到源视频文件 → 探测时长 → 回填(best-effort，失败不影响本次已探测到的结果)。
// 打开/下载/探测任一步失败直接返回 error，调用方据此拒绝(不估算)。
//
// 峰值内存：本地 /media 按 key 打开后原地探测（ffprobe 直接读文件，或只读 moov），不整读；
// 其余来源流式落临时文件再探测。同一 key 的并发请求经 singleflight 合并，整体受 postprocessProbeTimeout 限时。
func (h *CanvasPostprocessHandler) resolveDurationMs(ctx context.Context, src canvasSourceNode) (int64, error) {
	objKey := h.signer.ObjectKey(src.Content)
	if objKey != "" {
		if ms, found, err := h.assets.GetDurationMsByObjectKey(objKey); err == nil && found && ms > 0 {
			return ms, nil
		}
	}
	if objKey == "" {
		return 0, errors.New("源视频对象 key 未知，无法探测时长")
	}
	// 合并后的探测不随任一请求取消（否则第一个请求断开会连带其余请求失败），只受总时限约束。
	ch := h.probes.DoChan(objKey, func() (any, error) {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postprocessProbeTimeout)
		defer cancel()
		ms, err := h.probeSource(pctx, objKey, src.Content)
		if err != nil {
			return int64(0), err
		}
		_ = h.assets.SetDurationMsByObjectKey(objKey, ms) // 回填 best-effort
		return ms, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return 0, res.Err
		}
		return res.Val.(int64), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// probeSource 拿到源视频的磁盘文件并探测。优先按 key 直接打开对象存储：本地存储返回 *os.File，
// 原地探测、零拷贝；返回的不是文件就流式落临时文件。对象存储不可用/打不开时才走签名 URL 的 HTTP 下载。
func (h *CanvasPostprocessHandler) probeSource(ctx context.Context, objKey, content string) (int64, error) {
	var rc io.ReadCloser
	if h.objects != nil {
		if o, err := h.objects.GetObject(ctx, objKey); err == nil {
			rc = o
		}
	}
	if rc == nil {
		// Sign 吃 content(canonical media://key 形式)而非裸 objKey——裸 key 不带前缀，
		// 签名器认不出来会原样透传，签不出可下载的 URL。
		o, err := h.download(ctx, h.signer.Sign(content))
		if err != nil {
			return 0, fmt.Errorf("下载源视频失败: %w", err)
		}
		rc = o
	}
	defer rc.Close()

	path := ""
	if f, ok := rc.(*os.File); ok {
		path = f.Name()
	} else {
		tmp, err := spoolToTemp(ctx, rc)
		if err != nil {
			return 0, fmt.Errorf("读取源视频失败: %w", err)
		}
		defer os.Remove(tmp)
		path = tmp
	}
	ms, err := h.probe(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("探测视频时长失败: %w", err)
	}
	return ms, nil
}

// spoolToTemp 把流拷到临时文件（有体积上限，随 ctx 取消中止），返回路径；调用方负责删除。
func spoolToTemp(ctx context.Context, r io.Reader) (string, error) {
	f, err := os.CreateTemp("", "postprocess-probe-*.bin")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(ctxReader{ctx: ctx, r: r}, maxDesubDownloadBytes+1))
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > maxDesubDownloadBytes {
		err = fmt.Errorf("源视频超出上限(%dMB)", maxDesubDownloadBytes>>20)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// ctxReader 让 io.Copy 在 ctx 取消后尽快停下（对不感知 ctx 的 Reader 也有效）。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// toInt64 把快照 settings 里的数字字段转 int64——JSON 反序列化成 map[string]any 后数字统一是
// float64(srcGenTaskId 即此例)；兼容 json.Number/int64 以防调用方传入方式变化。
func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

// downloadMedia 是懒回填的 HTTP 回退实现：返回响应体流，不整读进内存。
// 时限由调用方的 ctx（postprocessProbeTimeout）和 client 自身的 Timeout 共同保证。
func downloadMedia(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := postprocessDownloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}
