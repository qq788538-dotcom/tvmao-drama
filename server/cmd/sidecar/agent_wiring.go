package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/agent"
	"github.com/james/huabu_studio/apps/api/internal/agent/agentseed"
	"github.com/james/huabu_studio/apps/api/internal/assets"
	"github.com/james/huabu_studio/apps/api/internal/generation"
	apphttp "github.com/james/huabu_studio/apps/api/internal/http"
	"github.com/james/huabu_studio/apps/api/internal/models"
	"github.com/james/huabu_studio/apps/api/internal/projects"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// agentWiringDeps 是 Agent 依赖的外部能力。除 DB 外都可以为 nil（对应计划尚未合并），
// 此时换成明确报错的兜底实现，Agent 对话本身照常可用。
type agentWiringDeps struct {
	DB      *sql.DB
	Gen     agent.GenerationService      // P1c 的 *generation.Service
	Signer  agent.URLSigner              // P1b 的 *apphttp.MediaSigner
	Assets  agent.AttachmentAssetFinder  // P1b 的 assets 仓库
	Objects agent.AttachmentObjectReader // P1b 的 assets.Storage
	Keys    agent.MediaKeyExtractor      // P1b 的 assets.Storage（ExtractKey）；接了 Signer/Objects 就必须接它
}

type agentWiring struct {
	Hub        *agent.Hub // 交给 P1c 的 canvasgen.SetChangeNotifier，承载 canvas_changed
	Registry   *agent.DBRegistry
	Agent      *apphttp.AgentHandler
	Skills     *apphttp.SkillsHandler
	AdminAgent *apphttp.AdminAgentHandler
}

func wireAgent(ctx context.Context, d agentWiringDeps) (*agentWiring, error) {
	if d.DB == nil {
		return nil, errors.New("wireAgent: 缺少数据库连接")
	}
	// 存储接了一半（有 Signer/Objects 没 Keys）时，LocalMediaResolver 认不出本机图片，
	// 只能报错不发图；这一定是装配漏传，直接拒绝启动比运行时悄悄失效好查。
	if d.Keys == nil && (d.Signer != nil || d.Objects != nil) {
		return nil, errors.New("wireAgent: 已接入存储但缺少 Keys（应传 P1b 的 assets.Storage）")
	}
	// 内置数据同步失败不阻止启动：已有数据仍可用，日志里能看到原因。
	if res, err := agentseed.Import(d.DB); err != nil {
		log.Printf("agent 内置数据导入失败: %v", err)
	} else {
		log.Printf("agent 内置数据: 分类 %d、Skill %d、工具 %d（跳过 %d）、大模型 %d",
			res.Categories, res.Skills, res.Tools, res.SkippedTools, res.Backbones)
	}

	settingsRepo := settings.NewSQLiteRepository(d.DB)
	modelsRepo := models.NewSQLiteRepository(d.DB)
	projectsRepo := projects.NewSQLiteRepository(d.DB)

	signer := d.Signer
	if signer == nil {
		signer = agentIdentitySigner{}
	}
	gen := d.Gen
	if gen == nil {
		gen = agentUnavailableGeneration{}
	}
	assetFinder := d.Assets
	if assetFinder == nil {
		assetFinder = agentUnavailableAssets{}
	}
	objects := d.Objects
	if objects == nil {
		objects = agentUnavailableAssets{}
	}
	// Keys 为 nil（P1b 未合并）时，resolver 对本机地址一律报错，绝不把 127.0.0.1 原样发给上游。
	resolver := &agent.LocalMediaResolver{Keys: d.Keys, Objects: objects}

	backbones := agent.NewBackboneRepo(d.DB)
	repo := agent.NewRepository(d.DB)
	hub := agent.NewHub()
	toolsRepo := agent.NewAgentToolsRepo(d.DB)
	skillsRepo := agent.NewSkillsRepo(d.DB)
	categoriesRepo := agent.NewSkillCategoriesRepo(d.DB)

	deps := agent.PlannerDeps{
		Backbones:   backbones,
		Settings:    settingsRepo,
		UpstreamKey: agentKeyResolver{settings: settingsRepo},
		Models:      modelsRepo,
		Billing:     agent.NoopBilling{},
		Media:       resolver,
	}
	registry := agent.NewDBRegistry(toolsRepo, agentModelResolver{repo: modelsRepo}, gen, deps, signer,
		skillsRepo, projectsRepo, agentCanvasCatalog{repo: modelsRepo}, assetFinder, objects, "")
	if err := registry.RefreshFromDB(); err != nil {
		log.Printf("agent 工具目录初次加载: %v", err)
	}
	registry.StartPeriodicRefresh(ctx, 10*time.Second)

	runner := &agentRunnerImpl{deps: deps, agentRepo: repo, skillsRepo: skillsRepo, tools: registry, signer: signer, media: resolver}
	agentHandler := apphttp.NewAgentHandler(hub, repo, runner, signer)
	agentHandler.BaseCtx = ctx // sidecar 退出时取消，进行中的 Run 跟着停
	return &agentWiring{
		Hub:      hub,
		Registry: registry,
		Agent:    agentHandler,
		Skills:   apphttp.NewSkillsHandler(skillsRepo, categoriesRepo, agentIconSigner{signer: signer}),
		AdminAgent: &apphttp.AdminAgentHandler{
			Repo: backbones, Switcher: agent.NewBackboneSwitcher(d.DB), Models: modelsRepo,
			Defaults: agentseed.DefaultBackboneModelName,
		},
	}, nil
}

// agentRunnerImpl 把 agent.Agent 循环适配到 apihttp.AgentRunner 接口。
// planner ChatModel 每次 Run 都重新构建，目的是热更 backbone 配置（管理端改完即时
// 生效）；apiKey 现在统一取 settings.upstream_api_key（全局 key，和 chat 一致）。
// 当 skillID 非空时，runner 会加载对应的 skill（并自增其 use_count），
// 让 agent 循环限定工具集 / 在 prompt 前拼上 addon。
type agentRunnerImpl struct {
	deps       agent.PlannerDeps
	agentRepo  agent.Repository
	skillsRepo agent.SkillsRepo
	tools      agent.ToolRegistry
	signer     agent.URLSigner
	media      agent.LLMMediaResolver
}

func (r *agentRunnerImpl) Run(ctx context.Context, userID, projectID int64, threadID, actionID, userText string, mentions []agent.Mention, skillID string, sink agent.FrameSink) error {
	var skill *agent.Skill
	if skillID != "" {
		s, err := r.skillsRepo.Get(skillID)
		if err != nil {
			// 完整三件套：这条路径发生在 agent.Run 之前（action_start 还没发），
			// 裸 error 帧缺 thread_id 会被前端整帧丢弃，且没有 action_end 解转圈。
			agent.SendFatal(sink, threadID, actionID, "skill_not_found", err.Error())
			return err
		}
		skill = s
		// 尽力而为：计数器自增失败不应阻塞真正的 Run；
		// agent 循环本身并不依赖 use_count。
		_ = r.skillsRepo.IncrementUseCount(skillID)
	} else if auto := agent.ResolveAutoSkill(userID, threadID, userText, r.skillsRepo, r.agentRepo); auto != nil {
		// 关键词自动触发。只在用户**没有**手动选技能时才走这条——显式意图永远优先。
		// ResolveAutoSkill 内部已经把 ForceTools 清空并打上 AutoTriggered 标记；
		// 它对任何依赖故障都返回 nil 而不是 error，所以这里没有失败分支要处理。
		skill = auto
		_ = r.skillsRepo.IncrementUseCount(auto.ID)
	}
	llm, plannerCfg, plannerBilling, err := agent.BuildModelForUserWithCfg(ctx, userID, agent.RolePlanner, r.deps)
	if err != nil {
		agent.SendFatal(sink, threadID, actionID, "no_planner", err.Error())
		return err
	}
	ctxWindow := 0
	if plannerCfg != nil {
		ctxWindow = plannerCfg.ContextWindow
	}
	a := &agent.Agent{
		Planner:              llm,
		Tools:                r.tools,
		Repo:                 r.agentRepo,
		Sink:                 sink,
		PlannerContextWindow: ctxWindow,
		Summarizer:           &agent.LLMSummarizer{UserID: userID, Deps: r.deps},
		Signer:               r.signer,
		Billing:              r.deps.Billing,
		UserID:               userID,
		PlannerBilling:       plannerBilling,
		MediaResolver:        r.media,
	}
	ctx = agent.WithUserID(agent.WithProjectID(ctx, projectID), userID)
	return a.Run(ctx, threadID, actionID, userText, mentions, skill)
}

// agentCanvasCatalog 把 models.Repository 适配为 agent.CanvasModelCatalog，供
// 画布能力查询工具（list_canvas_models / describe_canvas_node）与写工具的清单
// 校验使用。只透出 agent 需要的四个字段——上游 basePath、计费配置、请求模板与
// 它无关，也不该让它看见。
type agentCanvasCatalog struct {
	repo *models.SQLiteRepository
}

func (c agentCanvasCatalog) ListCanvasModels() ([]agent.CanvasModelInfo, error) {
	// onlyVisible=true：隐藏/停用的模型 agent 选了也用不了，给它看只会诱发
	// 「选了一个用不了的模型」这种需要用户来解释的失败。
	cfgs, err := c.repo.List(true)
	if err != nil {
		return nil, err
	}
	out := make([]agent.CanvasModelInfo, 0, len(cfgs))
	for _, m := range cfgs {
		out = append(out, agent.CanvasModelInfo{
			ModelID: m.ModelID, Name: m.Name, Type: m.Type, UISchema: m.UISchema,
		})
	}
	return out, nil
}

func (c agentCanvasCatalog) GetCanvasModel(modelID string) (*agent.CanvasModelInfo, bool) {
	cfg, err := c.repo.GetByModelID(modelID)
	if err != nil || cfg == nil {
		return nil, false
	}
	return &agent.CanvasModelInfo{
		ModelID: cfg.ModelID, Name: cfg.Name, Type: cfg.Type, UISchema: cfg.UISchema,
	}, true
}

// agentModelResolver 把 models.Repository 适配为 agent.ModelResolver。
// agent_tools.source_model_id 是指向 model_configs.id 的外键；agent 循环
// （通过 DynamicTool）需要 model_id slug 才能路由到 generation.Service。
type agentModelResolver struct {
	repo *models.SQLiteRepository
}

func (m agentModelResolver) ResolveModelSlug(id int64) (string, error) {
	cfg, err := m.repo.GetByID(id)
	if err != nil {
		return "", err
	}
	return cfg.ModelID, nil
}

// ResolvePollMaxWait 解出该 model 的 PollingTemplate.MaxWait，让 agent 工具的阻塞
// 轮询跟后端 completeAsync/pollUpstream 等同样久。默认值刻意对齐 generation
// pollUpstream（maxWait 缺省 300s）——两层走同一套兜底，才不会一层先放弃、另一层
// 还在跑。注意：grok video 这类慢 model 必须在模型中心把 maxWait 配成真实耗时
// （如 1200s），这里只负责把它透出去；否则两层一起卡在 300s。
func (m agentModelResolver) ResolvePollMaxWait(id int64) (time.Duration, error) {
	cfg, err := m.repo.GetByID(id)
	if err != nil {
		return 0, err
	}
	var tpl struct {
		MaxWait int `json:"maxWait"`
	}
	// 解析失败就当没配，落到下面的默认值——PollingTemplate 是可选的、形态各异，
	// 不该因为某个 model 没填或填歪了就让工具构造失败。
	_ = json.Unmarshal(cfg.PollingTemplate, &tpl)
	maxWait := time.Duration(tpl.MaxWait) * time.Second
	if maxWait <= 0 {
		maxWait = 300 * time.Second
	}
	return maxWait, nil
}

// agentKeyResolver：上游 Key 统一取 settings.upstream_api_key（spec A2）。
type agentKeyResolver struct{ settings *settings.SQLiteRepository }

func (k agentKeyResolver) Resolve(int64) (string, error) { return k.settings.Get("upstream_api_key") }

// agentIdentitySigner：P1b 的 MediaSigner 未接入时，URL 原样进出。
type agentIdentitySigner struct{}

func (agentIdentitySigner) Sign(s string) string         { return s }
func (agentIdentitySigner) Canonicalize(s string) string { return s }

// agentIconSigner 给 Skill 图标出口用：缩略图退化为原图签名（种子里 icon_url 全为空，够用）。
// P1b 合并后直接把 *apphttp.MediaSigner 传给 NewSkillsHandler，删掉本类型。
type agentIconSigner struct{ signer agent.URLSigner }

func (s agentIconSigner) Sign(u string) string             { return s.signer.Sign(u) }
func (s agentIconSigner) SignThumb(u string, _ int) string { return s.signer.Sign(u) }

var errAgentDepUnavailable = errors.New("该能力尚未接入（等待存储/生成模块合并）")

type agentUnavailableGeneration struct{}

func (agentUnavailableGeneration) Submit(context.Context, generation.SubmitInput) (*generation.SubmitResult, error) {
	return nil, errAgentDepUnavailable
}
func (agentUnavailableGeneration) Get(context.Context, int64, int64) (*generation.Task, error) {
	return nil, errAgentDepUnavailable
}

type agentUnavailableAssets struct{}

func (agentUnavailableAssets) FindByID(int64) (*assets.AssetRecord, error) {
	return nil, errAgentDepUnavailable
}
func (agentUnavailableAssets) GetObject(context.Context, string) (io.ReadCloser, error) {
	return nil, errAgentDepUnavailable
}
