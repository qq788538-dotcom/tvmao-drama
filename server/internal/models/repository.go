package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

type ModelConfig struct {
	ID               int64           `json:"id"`
	Type             string          `json:"type"`
	Name             string          `json:"name"`
	ModelID          string          `json:"modelId"`
	BasePath         string          `json:"basePath"`
	AdminNote        string          `json:"adminNote,omitempty"`
	PriceNote        string          `json:"priceNote,omitempty"` // 用户端计费展示文案（如「100积分/分钟」），仅按上游响应实际用量结算的模型填写；纯展示，不参与计算
	ImageInputFormat string          `json:"imageInputFormat"`
	ImageRequestMode string          `json:"imageRequestMode"`
	Enabled          bool            `json:"enabled"`
	Hidden           bool            `json:"hidden"`
	SortOrder        int             `json:"sortOrder"`
	RequestTemplate  json.RawMessage `json:"requestTemplate"`
	PollingTemplate  json.RawMessage `json:"pollingTemplate,omitempty"`
	UISchema         json.RawMessage `json:"uiSchema,omitempty"`
	BillingConfigRaw json.RawMessage `json:"billingConfig"`        // {flatFee,freezeEstimate,items[]}，统一计费模型；解析见 BillingConfig()
	GenControlRaw    json.RawMessage `json:"genControl,omitempty"` // {concurrency,modules{<模块>{maxRetries,retryIntervalSec}}}，生成并发+重试统一配置；解析见 GenConcurrency()/GenRetry()
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	// UpstreamBaseURL / UpstreamAPIKey 是每模型独立上游（0070）：两项同时非空才生效，否则走全局。
	// 只经 ResolveUpstream 使用；json:"-" 保证任何直接序列化 ModelConfig 的接口都不会带出 Key 明文。
	UpstreamBaseURL string `json:"-"`
	UpstreamAPIKey  string `json:"-"`
}

// DefaultGenConcurrency 是 gen_control.concurrency 缺失/为 0 时的兜底并发额度。
// 语义是「没配 = 用安全上限」，而不是「不限」——防空配置把 goroutine/上游调用打到无限。
const DefaultGenConcurrency = 200

// GenRetryConfig 是某个模块在某模型上的重试策略。零值 = 不重试。
type GenRetryConfig struct {
	MaxRetries       int `json:"maxRetries"`
	RetryIntervalSec int `json:"retryIntervalSec"`
}

type genControl struct {
	Concurrency   int                       `json:"concurrency"`
	Modules       map[string]GenRetryConfig `json:"modules"`
	InputLimits   map[string]InputLimit     `json:"inputLimits"`
	InputDuration *InputDurationConfig      `json:"inputDuration"`
}

func (m ModelConfig) genControl() genControl {
	var gc genControl
	if len(m.GenControlRaw) > 0 {
		_ = json.Unmarshal(m.GenControlRaw, &gc)
	}
	return gc
}

// GenConcurrency 返回该模型的并发额度，已应用默认：concurrency>0 取配置值，否则取 DefaultGenConcurrency(200)。
// 永不返回 0/无限——并发闸恒在，是全平台生成的唯一并发出口(画布/drama/未来模块共用一把)。
func (m ModelConfig) GenConcurrency() int {
	if c := m.genControl().Concurrency; c > 0 {
		return c
	}
	return DefaultGenConcurrency
}

// GenRetry 返回指定模块(canvas/drama/…)在本模型上的重试策略。模块缺失或字段为 0 即零值 = 不重试。
func (m ModelConfig) GenRetry(module string) GenRetryConfig {
	return m.genControl().Modules[module]
}

// InputLimit 是某媒体类型的输入数量约束。min/max 用指针区分「缺失=不限」与
// 「显式 0」——max:0 是禁止连接，语义完全不同于不限，int 零值表达不了。
type InputLimit struct {
	Min *int `json:"min"`
	Max *int `json:"max"`
}

// InputLimits 返回该模型的输入数量约束（key: video/image/audio）。nil = 未配置 =
// 无任何约束，存量模型全走这条。对象内不认识的键随宽松 Unmarshal 静默丢弃——
// 后续加新约束键（maxDurationSec 之类）不需要动这里。
func (m ModelConfig) InputLimits() map[string]InputLimit {
	return m.genControl().InputLimits
}

// InputDurationConfig 是「计费时长来源」配置：该模型的 duration 不来自用户参数，
// 而由服务端从输入素材推导（视频编辑类模型：输出时长 = 输入视频时长）。
type InputDurationConfig struct {
	From string `json:"from"`
}

// InputDuration 返回计费时长来源；nil = 未配置 = 行为零变化。from 只认 "video"——
// 其他值视同未配置（宽松忽略，为将来 "audio" 等留形状，老二进制遇到新值不炸）。
func (m ModelConfig) InputDuration() *InputDurationConfig {
	cfg := m.genControl().InputDuration
	if cfg == nil || cfg.From != "video" {
		return nil
	}
	return cfg
}

const (
	ImageInputFormatURL     = "url"
	ImageInputFormatDataURI = "data_uri"
	ImageInputFormatBase64  = "base64"

	ImageRequestModeJSON             = "json"
	ImageRequestModeOpenAIImageEdits = "openai_image_edits"
)

func normalizeImageProtocolDefaults(m *ModelConfig) {
	if m.ImageInputFormat == "" {
		m.ImageInputFormat = ImageInputFormatURL
	}
	if m.ImageRequestMode == "" {
		m.ImageRequestMode = ImageRequestModeJSON
	}
}

// BillingConfig 解析 billing_config JSON 成 billing.ModelBilling。损坏/空 → 零值(flatFee=0,无项)。
// 收口成单一来源，generation/chat/agent 三处调用方共用，避免各写一份映射。
func (m ModelConfig) BillingConfig() billing.ModelBilling {
	var mb billing.ModelBilling
	if len(m.BillingConfigRaw) > 0 {
		_ = json.Unmarshal(m.BillingConfigRaw, &mb)
	}
	return mb
}

// nullableJSON 把 RawMessage 写库：空→NULL，非空→string（TEXT 落库）。
func nullableJSON(raw json.RawMessage) any {
	if !jsonHasValue(raw) {
		return nil
	}
	return string(raw)
}

var ErrNotFound = errors.New("model not found")

type Repository interface {
	Create(m ModelConfig) (ModelConfig, error)
	Update(id int64, m ModelConfig) (ModelConfig, error)
	Delete(id int64) error
	List(onlyVisible bool) ([]ModelConfig, error)
	GetByModelID(modelID string) (*ModelConfig, error)
	GetByID(id int64) (*ModelConfig, error)
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func jsonHasValue(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if string(raw) == "null" {
		return false
	}
	return true
}

func (r *SQLiteRepository) Create(m ModelConfig) (ModelConfig, error) {
	if len(m.RequestTemplate) == 0 {
		m.RequestTemplate = json.RawMessage(`{}`)
	}
	normalizeImageProtocolDefaults(&m)
	var polling any
	if jsonHasValue(m.PollingTemplate) {
		polling = string(m.PollingTemplate)
	}
	var uiSchema any
	if jsonHasValue(m.UISchema) {
		uiSchema = string(m.UISchema)
	}
	res, err := r.db.Exec(
		`INSERT INTO model_configs (type, name, model_id, base_path, admin_note, price_note, image_input_format, image_request_mode, enabled, hidden, sort_order, request_template, polling_template, ui_schema, billing_config, gen_control) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Type, m.Name, m.ModelID, m.BasePath, m.AdminNote, m.PriceNote, m.ImageInputFormat, m.ImageRequestMode, m.Enabled, m.Hidden, m.SortOrder, string(m.RequestTemplate), polling, uiSchema, nullableJSON(m.BillingConfigRaw), nullableJSON(m.GenControlRaw),
	)
	if err != nil {
		return ModelConfig{}, err
	}
	id, _ := res.LastInsertId()
	m.ID = id
	return m, nil
}

func (r *SQLiteRepository) Update(id int64, m ModelConfig) (ModelConfig, error) {
	if len(m.RequestTemplate) == 0 {
		m.RequestTemplate = json.RawMessage(`{}`)
	}
	normalizeImageProtocolDefaults(&m)
	var polling any
	if jsonHasValue(m.PollingTemplate) {
		polling = string(m.PollingTemplate)
	}
	var uiSchema any
	if jsonHasValue(m.UISchema) {
		uiSchema = string(m.UISchema)
	}
	_, err := r.db.Exec(
		`UPDATE model_configs SET type=?, name=?, model_id=?, base_path=?, admin_note=?, price_note=?, image_input_format=?, image_request_mode=?, enabled=?, hidden=?, sort_order=?, request_template=?, polling_template=?, ui_schema=?, billing_config=?, gen_control=? WHERE id=?`,
		m.Type, m.Name, m.ModelID, m.BasePath, m.AdminNote, m.PriceNote, m.ImageInputFormat, m.ImageRequestMode, m.Enabled, m.Hidden, m.SortOrder, string(m.RequestTemplate), polling, uiSchema, nullableJSON(m.BillingConfigRaw), nullableJSON(m.GenControlRaw), id,
	)
	if err != nil {
		return ModelConfig{}, err
	}
	m.ID = id
	return m, nil
}

func (r *SQLiteRepository) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM model_configs WHERE id = ?`, id)
	return err
}

func (r *SQLiteRepository) List(onlyVisible bool) ([]ModelConfig, error) {
	q := `SELECT id, type, name, model_id, base_path, admin_note, price_note, image_input_format, image_request_mode, enabled, hidden, sort_order, request_template, polling_template, ui_schema, billing_config, gen_control, created_at, updated_at, upstream_base_url, upstream_api_key FROM model_configs`
	if onlyVisible {
		q += ` WHERE enabled = 1 AND hidden = 0`
	}
	q += ` ORDER BY sort_order ASC, id ASC`
	rows, err := r.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelConfig
	for rows.Next() {
		m, err := scanModelConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// FreezeEstimateForModel 按 slug 查模型计费配置，算出该模型一次调用的预冻结积分。
// 给 drama Agent chat 等「同步一次性调用」用：freeze 这么多，调用后按实际 settle。
func (r *SQLiteRepository) FreezeEstimateForModel(slug string) (int64, error) {
	m, err := r.GetByModelID(slug)
	if err != nil {
		return 0, err
	}
	return billing.EstimateFreeze(m.BillingConfig(), nil), nil
}

func (r *SQLiteRepository) GetByModelID(modelID string) (*ModelConfig, error) {
	row := r.db.QueryRow(`SELECT id, type, name, model_id, base_path, admin_note, price_note, image_input_format, image_request_mode, enabled, hidden, sort_order, request_template, polling_template, ui_schema, billing_config, gen_control, created_at, updated_at, upstream_base_url, upstream_api_key FROM model_configs WHERE model_id = ?`, modelID)
	m, err := scanModelConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

// GetByID 是按主键查询，供 agent 的 ModelResolver 把
// agent_tools.source_model_id（指向 model_configs.id 的外键）映射回
// generation.Service 路由所用的 model_id slug。
func (r *SQLiteRepository) GetByID(id int64) (*ModelConfig, error) {
	row := r.db.QueryRow(`SELECT id, type, name, model_id, base_path, admin_note, price_note, image_input_format, image_request_mode, enabled, hidden, sort_order, request_template, polling_template, ui_schema, billing_config, gen_control, created_at, updated_at, upstream_base_url, upstream_api_key FROM model_configs WHERE id = ?`, id)
	m, err := scanModelConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

type modelConfigScanner interface {
	Scan(dest ...any) error
}

func scanModelConfig(scanner modelConfigScanner) (*ModelConfig, error) {
	var m ModelConfig
	var req []byte
	var poll, uiSchema, billingCfg, genCtrl sql.NullString
	if err := scanner.Scan(&m.ID, &m.Type, &m.Name, &m.ModelID, &m.BasePath, &m.AdminNote, &m.PriceNote, &m.ImageInputFormat, &m.ImageRequestMode, &m.Enabled, &m.Hidden, &m.SortOrder, &req, &poll, &uiSchema, &billingCfg, &genCtrl, &m.CreatedAt, &m.UpdatedAt, &m.UpstreamBaseURL, &m.UpstreamAPIKey); err != nil {
		return nil, err
	}
	m.RequestTemplate = json.RawMessage(req)
	if poll.Valid {
		m.PollingTemplate = json.RawMessage(poll.String)
	}
	if uiSchema.Valid {
		m.UISchema = json.RawMessage(uiSchema.String)
	}
	if billingCfg.Valid {
		m.BillingConfigRaw = json.RawMessage(billingCfg.String)
	}
	if genCtrl.Valid {
		m.GenControlRaw = json.RawMessage(genCtrl.String)
	}
	normalizeImageProtocolDefaults(&m)
	return &m, nil
}
