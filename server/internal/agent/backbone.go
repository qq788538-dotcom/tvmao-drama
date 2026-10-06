// apps/api/internal/agent/backbone.go
package agent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/model"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/james/huabu_studio/apps/api/internal/billing"
	"github.com/james/huabu_studio/apps/api/internal/models"
)

const (
	RolePlanner        = "planner"
	RoleSubagentVision = "subagent_vision"
	RoleSubagentVideo  = "subagent_video"
	RoleSubagentAudio  = "subagent_audio"
	RoleUtility        = "utility"
)

var ErrNoActiveBackbone = errors.New("no active backbone model for role")

type BackboneModel struct {
	ID              int64     `json:"id"`
	Alias           string    `json:"alias"`
	Provider        string    `json:"provider"`
	ModelName       string    `json:"model_name"`
	Role            string    `json:"role"`
	ThinkingEnabled bool      `json:"thinking_enabled"`
	ThinkingBudget  int       `json:"thinking_budget"`
	MaxTokens       int       `json:"max_tokens"`
	ContextWindow   int       `json:"context_window"`
	Temperature     float64   `json:"temperature"`
	IsActive        bool      `json:"is_active"`
	SortWeight      int       `json:"sort_weight"`
	Description     string    `json:"description"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type BackboneRepo interface {
	Create(m BackboneModel) (int64, error)
	Update(m BackboneModel) error
	Delete(id int64) error
	Get(id int64) (*BackboneModel, error)
	List() ([]BackboneModel, error)
	GetActiveByRole(role string) (*BackboneModel, error)
}

type sqliteBackboneRepo struct{ db *sql.DB }

func NewBackboneRepo(db *sql.DB) BackboneRepo { return &sqliteBackboneRepo{db: db} }

func (r *sqliteBackboneRepo) Create(m BackboneModel) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO agent_backbone_models
		(alias, provider, model_name, role,
		 thinking_enabled, thinking_budget, max_tokens, context_window, temperature,
		 is_active, sort_weight, description)
		VALUES (?,?,?,?, ?,?,?,?,?, ?,?,?)`,
		m.Alias, m.Provider, m.ModelName, m.Role,
		boolToInt(m.ThinkingEnabled), m.ThinkingBudget, m.MaxTokens, m.ContextWindow, m.Temperature,
		boolToInt(m.IsActive), m.SortWeight, m.Description)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *sqliteBackboneRepo) Update(m BackboneModel) error {
	_, err := r.db.Exec(`UPDATE agent_backbone_models SET
		alias=?, provider=?, model_name=?, role=?,
		thinking_enabled=?, thinking_budget=?, max_tokens=?, context_window=?, temperature=?,
		is_active=?, sort_weight=?, description=?
		WHERE id=?`,
		m.Alias, m.Provider, m.ModelName, m.Role,
		boolToInt(m.ThinkingEnabled), m.ThinkingBudget, m.MaxTokens, m.ContextWindow, m.Temperature,
		boolToInt(m.IsActive), m.SortWeight, m.Description,
		m.ID)
	return err
}

func (r *sqliteBackboneRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM agent_backbone_models WHERE id=?`, id)
	return err
}

func (r *sqliteBackboneRepo) Get(id int64) (*BackboneModel, error) {
	row := r.db.QueryRow(`SELECT id, alias, provider, model_name, role,
		thinking_enabled, thinking_budget, max_tokens, context_window, temperature,
		is_active, sort_weight, description, created_at, updated_at
		FROM agent_backbone_models WHERE id=?`, id)
	return scanBackbone(row)
}

func (r *sqliteBackboneRepo) List() ([]BackboneModel, error) {
	rows, err := r.db.Query(`SELECT id, alias, provider, model_name, role,
		thinking_enabled, thinking_budget, max_tokens, context_window, temperature,
		is_active, sort_weight, description, created_at, updated_at
		FROM agent_backbone_models ORDER BY role, sort_weight DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BackboneModel, 0)
	for rows.Next() {
		m, err := scanBackbone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (r *sqliteBackboneRepo) GetActiveByRole(role string) (*BackboneModel, error) {
	row := r.db.QueryRow(`SELECT id, alias, provider, model_name, role,
		thinking_enabled, thinking_budget, max_tokens, context_window, temperature,
		is_active, sort_weight, description, created_at, updated_at
		FROM agent_backbone_models
		WHERE role=? AND is_active=1
		ORDER BY sort_weight DESC, id ASC
		LIMIT 1`, role)
	m, err := scanBackbone(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoActiveBackbone
	}
	return m, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBackbone(rs rowScanner) (*BackboneModel, error) {
	var m BackboneModel
	var thinking, active int
	if err := rs.Scan(&m.ID, &m.Alias, &m.Provider, &m.ModelName, &m.Role,
		&thinking, &m.ThinkingBudget, &m.MaxTokens, &m.ContextWindow, &m.Temperature,
		&active, &m.SortWeight, &m.Description, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.ThinkingEnabled = thinking != 0
	m.IsActive = active != 0
	return &m, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PlannerDeps 是 BuildPlannerForUser 需要的一组依赖。
//
// 上游 SK 走 UpstreamKey 解析：用户配了独立 key 用用户的，否则回落全局
// settings.upstream_api_key——和 chat handler 一致。Settings 供 base_domain。
//
// Models + Billing 让 agent 的每次 LLM 往返都能像 chat 一样 freeze→settle：
// backbone 没有自己的计费字段，所以按 backbone.ModelName 去 model_configs 取计费
// 配置（GetByModelID(...).BillingConfig()），匹配不到就拒绝 Run（不允许免费白嫖）。
type PlannerDeps struct {
	Backbones   BackboneRepo
	Settings    SettingsReader
	UpstreamKey UpstreamKeyResolver
	Models      ModelLookup
	Billing     AgentBilling
	// Media 把发给 subagent 的媒体地址换成上游拿得到的形式（S12 接缝，可为 nil）。
	Media LLMMediaResolver
}

// SettingsReader 是本包需要用到的 settings.Repository 的一小片（查 base_domain）。
type SettingsReader interface {
	Get(key string) (string, error)
}

// UpstreamKeyResolver 按用户解析这次调用用哪把上游 SK(用户独立 key 优先,回落全局)。
type UpstreamKeyResolver interface {
	Resolve(userID int64) (string, error)
}

// ModelLookup 是本包需要的 models.Repository 的一小片：按 model_id（= backbone 的
// ModelName）查模型配置，从而拿到计费配置。
type ModelLookup interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// AgentBilling 是 agent 计费所需的最小接口，由 *billing.Service 直接满足。
// 三方法对齐 chat：每次 LLM 往返 Freeze→Settle，失败 Refund。
type AgentBilling interface {
	Freeze(ctx context.Context, userID, taskID, amountCredits int64, note string) (billing.Result, error)
	Settle(ctx context.Context, userID, taskID, frozenCredits, actualCredits int64, note string) (billing.Result, error)
	Refund(ctx context.Context, userID, taskID, frozenCredits int64, note string) (billing.Result, error)
}

// BuildPlannerForUser 解析当前 active 的 planner backbone，返回一个全新的
// eino ChatModel。
//
// 每个 backbone 都走平台的 newapi upstream：BaseURL = settings.base_domain
// （自动补 "/v1" 后缀），APIKey = 每用户独立的 upstream_credentials[userID]。
// cfg.Provider 只是元数据——newapi 会在 /v1/chat/completions 上把每个
// provider 都归一成 OpenAI 形态，所以我们始终用 eino-ext 的 openai client，
// 按 cfg.ModelName 路由。
func BuildPlannerForUser(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, error) {
	cm, _, _, err := buildModelForUser(ctx, userID, RolePlanner, deps)
	return cm, err
}

// BuildUtilityModelForUser 解析当前 active 的 utility backbone（用于历史压缩
// 这类后台辅助任务），返回一个全新的 ChatModel + 它的计费配置。当没有配置
// utility 行时返回（被 wrap 过的）ErrNoActiveBackbone——调用方应当把它当作
// 「跳过这个辅助任务」。计费配置供调用方对这次辅助 LLM 往返做 freeze/settle。
func BuildUtilityModelForUser(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
	cm, _, mb, err := buildModelForUser(ctx, userID, RoleUtility, deps)
	return cm, mb, err
}

// BuildModelForUserWithCfg 是共用的解析器：同时返回 eino model、BackboneModel
// 行、以及按 backbone.ModelName 命中的计费配置。这样需要行内元数据
// （比如 context_window）和计费配置的调用方就不用再查一次 DB。
func BuildModelForUserWithCfg(ctx context.Context, userID int64, role string, deps PlannerDeps) (model.ChatModel, *BackboneModel, billing.ModelBilling, error) {
	return buildModelForUser(ctx, userID, role, deps)
}

// BuildSubagentVisionForUser 解析 subagent_vision backbone，连同它的计费配置一起
// 返回（subagent 分析也要 freeze→settle）。当没有配置 vision 行时回退到 planner
// backbone，这样在只有单个 backbone 的部署里 analyze_image 工具仍能正常工作——回退
// 时同样带回 planner backbone 的计费配置。
func BuildSubagentVisionForUser(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
	cm, _, mb, err := buildModelForUser(ctx, userID, RoleSubagentVision, deps)
	if errors.Is(err, ErrNoActiveBackbone) {
		return buildPlannerWithBilling(ctx, userID, deps)
	}
	return cm, mb, err
}

// BuildSubagentVideoForUser 解析 subagent_video backbone，连同计费配置一起返回，
// 在 ErrNoActiveBackbone 时回退到 planner（理由跟 vision 一样）。
func BuildSubagentVideoForUser(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
	cm, _, mb, err := buildModelForUser(ctx, userID, RoleSubagentVideo, deps)
	if errors.Is(err, ErrNoActiveBackbone) {
		return buildPlannerWithBilling(ctx, userID, deps)
	}
	return cm, mb, err
}

// BuildSubagentAudioForUser 解析 subagent_audio backbone，连同计费配置一起返回，
// 在 ErrNoActiveBackbone 时回退到 planner（理由跟 vision/video 一样——单 backbone
// 部署里 analyze_audio 至少能试着跑，能不能真听懂取决于那个模型）。
func BuildSubagentAudioForUser(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
	cm, _, mb, err := buildModelForUser(ctx, userID, RoleSubagentAudio, deps)
	if errors.Is(err, ErrNoActiveBackbone) {
		return buildPlannerWithBilling(ctx, userID, deps)
	}
	return cm, mb, err
}

// buildPlannerWithBilling 是 subagent 回退到 planner 时用的小帮手：返回 planner
// model + planner backbone 的计费配置，这样回退路径也能正确 freeze→settle。
func buildPlannerWithBilling(ctx context.Context, userID int64, deps PlannerDeps) (model.ChatModel, billing.ModelBilling, error) {
	cm, _, mb, err := buildModelForUser(ctx, userID, RolePlanner, deps)
	return cm, mb, err
}

func buildModelForUser(ctx context.Context, userID int64, role string, deps PlannerDeps) (model.ChatModel, *BackboneModel, billing.ModelBilling, error) {
	var noBilling billing.ModelBilling
	cfg, err := deps.Backbones.GetActiveByRole(role)
	if err != nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: %w", role, err)
	}
	if deps.Settings == nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: settings repo not wired", role)
	}
	if deps.Models == nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: models repo not wired", role)
	}
	baseDomain, err := deps.Settings.Get("base_domain")
	if err != nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: read base_domain: %w", role, err)
	}
	if baseDomain == "" {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: base_domain is not configured", role)
	}
	// 用户独立 key 优先、回落全局(UpstreamKeyResolver)——和 chat handler 一致。
	apiKey, err := deps.UpstreamKey.Resolve(userID)
	if err != nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: read upstream_api_key: %w", role, err)
	}
	if apiKey == "" {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: upstream_api_key is not configured", role)
	}
	// backbone 没有自己的计费字段：按 backbone.ModelName 去 model_configs 取计费配置。
	// 匹配不到 → 拒绝（和 chat 一致，不允许免费白嫖）。
	mcfg, err := deps.Models.GetByModelID(cfg.ModelName)
	if err != nil || mcfg == nil {
		return nil, nil, noBilling, fmt.Errorf("%s backbone: no billing config for backbone model %q: %v", role, cfg.ModelName, err)
	}
	mb := mcfg.BillingConfig()
	baseURL := normaliseNewapiBaseURL(baseDomain)
	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Model:       cfg.ModelName,
		MaxTokens:   intPtr(cfg.MaxTokens),
		Temperature: float32Ptr(cfg.Temperature),
		// 挂日志 transport：planner 的 HTTP 由 eino 内部的 openai client 发出，不走
		// internal/upstream 的日志层——上游 400 时从服务端日志完全看不到我们发了什么、
		// 上游回了什么（2026-08-17 测试环境排障只能靠猜）。这层是唯一能看到最终
		// 出网 JSON 的地方。
		HTTPClient: &http.Client{Transport: &plannerLogTransport{inner: http.DefaultTransport}},
	})
	if err != nil {
		return nil, nil, noBilling, err
	}
	return cm, cfg, mb, nil
}

// plannerLogTransport 给 planner/utility 的出网请求打日志。
// 成功只记一行摘要（不打 body——正常流量里 body 含用户对话内容，且 SSE 流不能预读）；
// 失败（传输错或 HTTP ≥400）把请求体和响应体全量打出来——错误响应不是流、体量小，
// 全量才够排查（截断 4KB 会把 JSON 尾部的 tool_choice 之类字段切掉，等于白打）。
// 不打任何请求头（Authorization 里是 sk）。
type plannerLogTransport struct{ inner http.RoundTripper }

func (t *plannerLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		log.Printf("[planner ✗] %s %s transport error: %v\nreq_body=%s", req.Method, req.URL, err, reqBody)
		return resp, err
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		log.Printf("[planner ✗] %s %s status=%d\nreq_body=%s\nresp_body=%s", req.Method, req.URL, resp.StatusCode, reqBody, respBody)
		return resp, nil
	}
	log.Printf("[planner →] %s %s status=%d req_bytes=%d", req.Method, req.URL, resp.StatusCode, len(reqBody))
	return resp, nil
}

// normaliseNewapiBaseURL 确保 URL 以 "/v1" 结尾，因为 newapi 把
// OpenAI 兼容的路由暴露在 /v1/chat/completions 上。幂等。
func normaliseNewapiBaseURL(s string) string {
	if s == "" {
		return s
	}
	// 去掉结尾的斜杠，方便后面做后缀检查。
	if s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	if len(s) >= 3 && s[len(s)-3:] == "/v1" {
		return s
	}
	return s + "/v1"
}

func intPtr(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func float32Ptr(f float64) *float32 {
	if f == 0 {
		return nil
	}
	v := float32(f)
	return &v
}
