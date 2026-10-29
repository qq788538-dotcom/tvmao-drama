// Package canvasscript 自由画布脚本节点的“剧本生成分镜”：用户贴剧本 + 选风格（skill）+ 主切割时长
// → 异步跑 Skill → 产出 candidate.json 存任务表，前端轮询拿到后自行导入节点。
//
// 桌面端：没有积分，去掉 billing 依赖与零价闸门；单进程，
// 去掉 lifecycle 实例租约与心跳，启动时把上个进程遗留的 running 一次性回收（RecoverOnStartup）。
package canvasscript

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/projects"
	"github.com/qq788538-dotcom/tvmao-drama/server/internal/skillruntime"
)

const (
	SettingKey = "canvas_script_config"

	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	MinDurationSec = 4
	MaxDurationSec = 15

	// MaxScriptRunes 剧本长度上限（按字符数），前端同值拦截，这里是权威一道。
	MaxScriptRunes = 5000
)

// StyleEntry 一个可选风格：展示名 + 版本 + skills 目录名。Enabled=false 保留但用户不可见。
type StyleEntry struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	SkillName string   `json:"skillName"`
	Enabled   bool     `json:"enabled"`
	UserTags  []string `json:"userTags"`
}

// Key 用户侧风格标识："name" 或 "name version"。
func (e StyleEntry) Key() string {
	if e.Version != "" {
		return e.Name + " " + e.Version
	}
	return e.Name
}

// VisibleTo 标签任一命中即可见；空标签全员可见。
func (e StyleEntry) VisibleTo(tags []string) bool {
	if len(e.UserTags) == 0 {
		return true
	}
	for _, want := range e.UserTags {
		for _, t := range tags {
			if t == want {
				return true
			}
		}
	}
	return false
}

// ModelCandidate 可选 chat 模型：模型中心 modelId + 展示名。列表第一项为前端默认。
type ModelCandidate struct {
	ModelID     string `json:"modelId"`
	DisplayName string `json:"displayName"`
}

// ModelCandidateView 下发前端的候选。Credits 恒为 0：桌面端没有积分，字段只为前端兼容保留。
type ModelCandidateView struct {
	ModelID     string `json:"modelId"`
	DisplayName string `json:"displayName"`
	Credits     int64  `json:"credits"`
}

// Config “设置 → 脚本设置”的完整配置。
type Config struct {
	ModelCandidates []ModelCandidate `json:"modelCandidates"`
	Styles          []StyleEntry     `json:"styles"`
	Concurrency     int              `json:"concurrency"`
	MaxRetries      int              `json:"maxRetries"`
}

func (c Config) concurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return 1
}

func (c Config) maxAttempts() int {
	if c.MaxRetries >= 1 && c.MaxRetries <= 10 {
		return c.MaxRetries + 1
	}
	return 2
}

// hasCandidate 模型是否在当前候选列表里。
func (c Config) hasCandidate(modelID string) bool {
	for _, m := range c.ModelCandidates {
		if m.ModelID == modelID {
			return true
		}
	}
	return false
}

type Task struct {
	ID          int64
	UserID      int64
	ProjectID   int64
	NodeID      string
	SkillName   string
	DurationSec int
	Script      string
	Model       string
	Status      string
	Result      string
	Error       string
	Attempts    int64
	UpdatedAt   time.Time
}

type settingsStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// modelCatalog 即 models.SQLiteRepository 的窄面。
type modelCatalog interface {
	GetByModelID(modelID string) (*models.ModelConfig, error)
}

// keyResolver 解析全局上游 Key（模型未单独配置上游时用）；桌面端由装配处读 settings.upstream_api_key。
type keyResolver interface {
	Resolve(userID int64) (string, error)
}

type skillRunner interface {
	Snapshot(ctx context.Context, name string) (skillruntime.Snapshot, error)
	Run(ctx context.Context, req skillruntime.RunRequest) (skillruntime.RunResult, error)
}

type Service struct {
	db         *sql.DB
	settings   settingsStore
	models     modelCatalog
	keys       keyResolver
	skills     skillRunner
	inflight   atomic.Int32
	running    sync.WaitGroup // 在跑的 execute goroutine；RunWorker 退出前等它们收尾
	instanceID string         // 本进程随机 id，写入 owner_instance 便于排查
}

func NewService(db *sql.DB, settings settingsStore, models modelCatalog, keys keyResolver, skills skillRunner) *Service {
	return &Service{db: db, settings: settings, models: models, keys: keys, skills: skills, instanceID: newInstanceID()}
}

func newInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoadConfig 读配置；未配置返回零值（风格列表为空 = 入口不可用）。
func (s *Service) LoadConfig() (Config, error) {
	raw, err := s.settings.Get(SettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("canvas_script_config 解析: %w", err)
	}
	return cfg, nil
}

func (s *Service) SaveConfig(cfg Config) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settings.Set(SettingKey, string(b))
}

// EnabledStyles 用户可见的风格 + 可选模型。模型中心查不到的候选剔除（配错 / 被删）；
// 不再按估价剔除——桌面端所有模型都是零价，credits<=0 闸门会把候选剔光。
func (s *Service) EnabledStyles(userTags []string) ([]StyleEntry, []ModelCandidateView, error) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	out := make([]StyleEntry, 0, len(cfg.Styles))
	for _, st := range cfg.Styles {
		if st.Enabled && st.VisibleTo(userTags) {
			out = append(out, st)
		}
	}
	cands := make([]ModelCandidateView, 0, len(cfg.ModelCandidates))
	for _, c := range cfg.ModelCandidates {
		if _, err := s.models.GetByModelID(c.ModelID); err != nil {
			log.Printf("canvasscript: 候选模型 %q 在模型中心不存在（%v），已剔除", c.ModelID, err)
			continue
		}
		cands = append(cands, ModelCandidateView{ModelID: c.ModelID, DisplayName: c.DisplayName})
	}
	return out, cands, nil
}

// UserError 是可以原样展示给用户的错误（校验类）；其余错误只写日志，不下发。
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userError(msg string) error { return &UserError{Msg: msg} }
func userErrorf(format string, args ...any) error {
	return &UserError{Msg: fmt.Sprintf(format, args...)}
}

// ErrTaskNotFound 任务不存在或不属于该用户。
var ErrTaskNotFound = errors.New("任务不存在")

// ErrProjectGone 提交时项目不存在或已软删。它也是 UserError（文案可直接展示），
// handler 用 errors.Is 单独映射成 404。
var ErrProjectGone error = &UserError{Msg: projects.GoneMessage}

// Submit 校验并落任务，返回任务 id。
func (s *Service) Submit(ctx context.Context, userID, projectID int64, nodeID, styleName, script string, durationSec int, model string, userTags []string) (int64, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return 0, userError("剧本内容不能为空")
	}
	if utf8.RuneCountInString(script) > MaxScriptRunes {
		return 0, userErrorf("剧本最长 %d 字", MaxScriptRunes)
	}
	if durationSec < MinDurationSec || durationSec > MaxDurationSec {
		return 0, userErrorf("时长需在 %d-%d 秒", MinDurationSec, MaxDurationSec)
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return 0, err
	}
	if model == "" {
		return 0, userError("请选择模型")
	}
	if !cfg.hasCandidate(model) {
		return 0, userError("所选模型不可用")
	}
	// 二次校验：堵“拉完候选之后、提交之前模型被删”的竞态。
	if _, err := s.models.GetByModelID(model); err != nil {
		return 0, userError("所选模型不可用")
	}
	var style *StyleEntry
	for i := range cfg.Styles {
		if cfg.Styles[i].Key() == styleName && cfg.Styles[i].Enabled && cfg.Styles[i].VisibleTo(userTags) {
			style = &cfg.Styles[i]
			break
		}
	}
	if style == nil {
		return 0, userError("所选风格不存在或已停用")
	}
	if _, err := s.skills.Snapshot(ctx, style.SkillName); err != nil {
		log.Printf("canvasscript: 风格 %q 的 skill 不可用: %v", style.SkillName, err)
		return 0, userError("风格对应的 skill 不可用")
	}
	// 已删项目不再接任务：项目软删后到清理器硬删之间插进来的行，清理器会一并删掉；
	// 硬删之后再插的就没人收了，所以入口必须挡。projectID=0 是不挂项目的调用，跳过。
	// 「先校验、后删除」的窄竞态挡不住，那种行最多空跑一次，结果没人取，不影响别的数据。
	if projectID != 0 {
		ok, err := projects.ActiveExists(ctx, s.db, projectID)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, ErrProjectGone
		}
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO canvas_script_tasks (user_id, project_id, node_id, skill_name, duration_sec, script, model)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, projectID, nodeID, style.SkillName, durationSec, script, model)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get 查任务（只允许提交者本人查）。
func (s *Service) Get(ctx context.Context, userID, taskID int64) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, project_id, node_id, skill_name, duration_sec, model, status,
		       COALESCE(result,''), COALESCE(error,''), attempts, updated_at
		FROM canvas_script_tasks WHERE id=? AND user_id=?`, taskID, userID)
	var t Task
	if err := row.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.NodeID, &t.SkillName, &t.DurationSec, &t.Model,
		&t.Status, &t.Result, &t.Error, &t.Attempts, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return &t, nil
}

const tickInterval = 2 * time.Second

// 失败重排的指数退避：第 n 次失败后等 retryBaseDelay·2^(n-1)，封顶 retryMaxDelay。
// 上游限流（429）通常要几十秒才恢复，不退避的话 2 秒一拍就把重试次数用完了。
const (
	retryBaseDelay = 15 * time.Second
	retryMaxDelay  = 5 * time.Minute
)

// retryBackoff 返回第 attempts 次尝试失败后到下次可领的间隔。
func retryBackoff(attempts int64) time.Duration {
	d := retryBaseDelay
	for i := int64(1); i < attempts; i++ {
		d *= 2
		if d >= retryMaxDelay {
			return retryMaxDelay
		}
	}
	return d
}

// RecoverOnStartup 单进程：启动时遗留的 running 一定属于已退出的上个进程，不必等心跳超时。
// 未用尽重试的回到 pending；用尽的判失败。
func (s *Service) RecoverOnStartup(ctx context.Context) error {
	cfg, _ := s.LoadConfig()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE canvas_script_tasks SET status='pending', owner_instance=NULL, heartbeat_at=NULL
		WHERE status='running' AND attempts < ?`, cfg.maxAttempts()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE canvas_script_tasks SET status='failed', error='上次运行中断且重试次数用尽，请重新提交'
		WHERE status='running'`); err != nil {
		return err
	}
	return tx.Commit()
}

// RunWorker 常驻循环：每 2 秒在并发额度内抢占 pending 任务。ctx 取消即退出。
func (s *Service) RunWorker(ctx context.Context) {
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.running.Wait()
			return
		case <-tick.C:
			s.claimAndRun(ctx)
		}
	}
}

func (s *Service) claimAndRun(ctx context.Context) {
	cfg, err := s.LoadConfig()
	if err != nil {
		return
	}
	for int(s.inflight.Load()) < cfg.concurrency() {
		t, ok := s.claimOne(ctx)
		if !ok {
			return
		}
		s.inflight.Add(1)
		s.running.Add(1)
		go func(task *Task) {
			defer s.running.Done()
			defer s.inflight.Add(-1)
			s.execute(ctx, task, cfg)
		}(t)
	}
}

// claimOne 用一条 UPDATE … RETURNING 原子抢占最早的、退避已到点的 pending（无需 SELECT + CAS 两步）。
func (s *Service) claimOne(ctx context.Context) (*Task, bool) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE canvas_script_tasks
		SET status='running', owner_instance=?, heartbeat_at=CURRENT_TIMESTAMP, attempts=attempts+1, next_attempt_at=NULL
		WHERE id = (SELECT id FROM canvas_script_tasks
		            WHERE status='pending' AND (next_attempt_at IS NULL OR next_attempt_at <= CURRENT_TIMESTAMP)
		            ORDER BY id LIMIT 1)
		  AND status='pending'
		RETURNING id, user_id, project_id, node_id, skill_name, duration_sec, script, model, attempts`, s.instanceID)
	var t Task
	if err := row.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.NodeID, &t.SkillName, &t.DurationSec, &t.Script, &t.Model, &t.Attempts); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("canvasscript: 抢占任务失败: %v", err)
		}
		return nil, false
	}
	return &t, true
}

func (s *Service) execute(ctx context.Context, t *Task, cfg Config) {
	// 状态回写不随 worker ctx 取消：关闭时 ctx 先被取消，而 RunWorker 会等本 goroutine
	// 收尾、sidecar 等 worker 退出后才关库，这时写库仍然安全。曾经直接用 ctx 写，
	// 关闭瞬间刚跑完的结果写不进去，任务停在 running，下次启动再整个重跑一遍。
	dbCtx := context.WithoutCancel(ctx)
	// 失败分流：还有重试额度 → 按指数退避重排 pending；用尽 → failed。
	// msg 存库并展示给用户，不得含路径等内部细节；detail 只写日志。
	fail := func(msg, detail string) {
		log.Printf("canvasscript: task=%d 失败详情: %s", t.ID, detail)
		if int(t.Attempts) < cfg.maxAttempts() {
			delay := retryBackoff(t.Attempts)
			log.Printf("canvasscript: task=%d attempt=%d 失败将在 %s 后重试: %s", t.ID, t.Attempts, delay, msg)
			_, _ = s.db.ExecContext(dbCtx, `
				UPDATE canvas_script_tasks SET status='pending', owner_instance=NULL, heartbeat_at=NULL, error=?,
				       next_attempt_at=datetime('now', ?)
				WHERE id=? AND status='running'`, msg, fmt.Sprintf("+%d seconds", int(delay/time.Second)), t.ID)
			return
		}
		_, _ = s.db.ExecContext(dbCtx, `UPDATE canvas_script_tasks SET status='failed', error=? WHERE id=? AND status='running'`, msg, t.ID)
	}
	// 配置类错误（模型已不在候选、skill 不存在、上游没配）重试多少次都一样，直接判失败，
	// 不占着重试额度空转；用户改完配置重新提交即可。
	failConfig := func(msg, detail string) {
		log.Printf("canvasscript: task=%d 配置错误，不重试: %s", t.ID, detail)
		_, _ = s.db.ExecContext(dbCtx, `UPDATE canvas_script_tasks SET status='failed', error=? WHERE id=? AND status='running'`, msg, t.ID)
	}

	if t.Model == "" {
		_, _ = s.db.ExecContext(dbCtx, `UPDATE canvas_script_tasks SET status='failed', error=? WHERE id=?`,
			"任务未记录模型，请重新提交", t.ID)
		return
	}
	// 提交后模型被移出候选：按停用处理，不再拿它跑。
	if !cfg.hasCandidate(t.Model) {
		failConfig("所选模型已不可用，请重新选择后提交", "模型 "+t.Model+" 不在候选列表")
		return
	}
	snapshot, err := s.skills.Snapshot(ctx, t.SkillName)
	if err != nil {
		failConfig("风格对应的 skill 不可用", err.Error())
		return
	}
	// 上游地址与 Key 经 models.ResolveUpstream 解析：任务记录的模型单独配了上游就用模型的，
	// 否则回落全局（Key 走 keyResolver）。模型行查不到时同样回落全局。
	// 只有 ErrNotFound 才回落全局；数据库瞬时错误直接失败重试，不把剧本发到可能是另一个
	// 计费账号的全局上游。
	var mcfg *models.ModelConfig
	if m, merr := s.models.GetByModelID(t.Model); merr == nil {
		mcfg = m
	} else if !errors.Is(merr, models.ErrNotFound) {
		fail("读取模型配置失败，请稍后重试", merr.Error())
		return
	}
	global := models.GlobalWithKey(s.settings, func() (string, error) { return s.keys.Resolve(t.UserID) })
	baseDomain, sk, err := models.ResolveUpstream(mcfg, global)
	if err != nil {
		fail("读取上游配置失败，请稍后重试", err.Error())
		return
	}
	if strings.TrimSpace(baseDomain) == "" {
		failConfig("服务未配置上游地址，请在设置中配置", "base_domain 未配置")
		return
	}
	if strings.TrimSpace(sk) == "" {
		failConfig("上游 API Key 未配置，请在设置中配置", "上游 API Key 未配置")
		return
	}

	input := fmt.Sprintf("使用skill %s\n\nDURATION_SEC=%d\n\n内容：\n%s", t.SkillName, t.DurationSec, t.Script)
	result, err := s.skills.Run(ctx, skillruntime.RunRequest{
		Skill:      snapshot,
		Model:      skillModelConfig(baseDomain, sk, t.Model, mcfg),
		Input:      input,
		SourceText: t.Script,
	})
	if err != nil {
		if ctx.Err() != nil {
			// 进程关闭打断的不是任务本身的失败：退回本次计数、回到 pending，下次启动重跑。
			// 走普通失败分流的话，最后一次尝试会被判成「重试次数用尽」永久失败。
			log.Printf("canvasscript: task=%d 运行被关闭打断，下次启动重跑: %v", t.ID, err)
			_, _ = s.db.ExecContext(dbCtx, `
				UPDATE canvas_script_tasks SET status='pending', owner_instance=NULL, heartbeat_at=NULL,
				       attempts=MAX(attempts-1, 0)
				WHERE id=? AND status='running'`, t.ID)
			return
		}
		fail("生成失败，请稍后重试", err.Error())
		return
	}
	if err := validateCandidate(result.Content); err != nil {
		fail("生成结果不符合要求，请重试", err.Error())
		return
	}
	if _, err := s.db.ExecContext(dbCtx, `
		UPDATE canvas_script_tasks SET status='succeeded', result=? WHERE id=? AND status='running'`, result.Content, t.ID); err != nil {
		log.Printf("canvasscript: 写结果失败 task=%d: %v", t.ID, err)
	}
}

// skillModelConfig 把模型中心那行的请求模板整份透传给上游。m 为 nil（模型行查不到）时不透传。
func skillModelConfig(baseDomain, sk, modelName string, m *models.ModelConfig) skillruntime.ModelConfig {
	var template json.RawMessage
	if m != nil {
		template = m.RequestTemplate
	}
	return skillruntime.ModelConfig{
		BaseURL:     baseDomain,
		APIKey:      sk,
		Model:       modelName,
		ExtraFields: passthroughModelFields(template),
	}
}

// passthroughModelFields 从请求模板挑出可直接发给上游的字段：剔掉 runtime 自己管的
// model / messages / stream，以及值形如 {{x}} 的插值占位。
func passthroughModelFields(template json.RawMessage) map[string]any {
	if len(template) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(template, &raw); err != nil {
		log.Printf("canvasscript: 请求模板解析失败，跳过参数透传: %v", err)
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch k {
		case "model", "messages", "stream":
			continue
		}
		if str, ok := v.(string); ok {
			if trimmed := strings.TrimSpace(str); strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
				continue
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateCandidate 产物形状把关：必须是含非空 storyboard 数组的 JSON 对象。
func validateCandidate(content string) error {
	var doc struct {
		Storyboard []json.RawMessage `json:"storyboard"`
	}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return fmt.Errorf("非法 JSON: %w", err)
	}
	if len(doc.Storyboard) == 0 {
		return errors.New("storyboard 为空")
	}
	return nil
}
