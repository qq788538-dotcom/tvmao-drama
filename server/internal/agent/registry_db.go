// apps/api/internal/agent/registry_db.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelResolver 把一个 agent_tools.source_model_id 映射到 generation.Service
// 期望的上游 model slug（model_configs.model_id）。
type ModelResolver interface {
	ResolveModelSlug(modelConfigID int64) (string, error)
	// ResolvePollMaxWait 返回该 model 的 PollingTemplate.MaxWait——也就是后端
	// completeAsync/pollUpstream 愿意为这个 model 的任务等多久。工具的阻塞轮询
	// （DynamicTool.pollUntilDone）用它对齐：否则慢视频 model（grok 等）会在硬
	// 编码的 5 分钟 defaultMaxWait 处先超时，planner 把超时当失败反复重试、对话
	// 一直转圈，而后端那条独立链路照样把视频跑完进了生成历史。
	ResolvePollMaxWait(modelConfigID int64) (time.Duration, error)
}

// DBRegistry 是一个 ToolRegistry，它的 Snapshot 反映 agent_tools 里已发布的行，
// 在 admin 写入时 + 定期安全轮询时刷新。
type DBRegistry struct {
	repo     AgentToolsRepo
	resolver ModelResolver
	genSvc   GenerationService
	signer   URLSigner

	// builtins 在构造时一次性填入始终开启的工具（meta + analyze_image +
	// analyze_video）。无论 agent_tools 里有没有行，Snapshot/ByName 都会把它们暴露出来。
	builtins []Tool

	mu     sync.RWMutex
	tools  []Tool
	byName map[string]Tool
	// displayNames 与 byName 同源同步刷新：tool_name → agent_tools.display_name
	//（仅记非空值）。对话 UI 的 tool_use 帧用它把内部函数名换成面向用户的名称。
	displayNames map[string]string

	// lastFingerprint + loggedOnce 让定期轮询在结果没变时静默：10s 的兜底 ticker
	// 每次都会跑 RefreshFromDB，稳态下工具目录纹丝不动，没必要每 10s 刷一条相同日志。
	// 只有指纹（工具数/行数/名字集合）真的变了——或者进程起来后第一次——才打。
	lastFingerprint string
	loggedOnce      bool
}

func NewDBRegistry(repo AgentToolsRepo, resolver ModelResolver, genSvc GenerationService, deps PlannerDeps, signer URLSigner, skills SkillsRepo, canvas CanvasSnapshotReader, modelCatalog CanvasModelCatalog, attachAssets AttachmentAssetFinder, attachObjects AttachmentObjectReader, _ string) *DBRegistry {
	builtins := []Tool{
		// output_skill_definition 不注册：桌面端不允许用户新建 Skill，
		// 前端也不渲染保存弹窗，planner 产出的技能草稿无处可去。工具类型本身保留。
		NewAnalyzeImageTool(deps, nil),
		NewAnalyzeVideoTool(deps, nil),
		NewAnalyzeAudioTool(deps, nil),
		// create_text_node / load_skill / create_gen_node 与上面三个同为「始终
		// 开启」的内置工具：文本产物落画布、skill 链式加载、铺生成节点（不执行
		// 生成）都不依赖 agent_tools 目录里的模型行。
		&CreateTextNodeTool{},
		&LoadSkillTool{Skills: skills},
		&CreateGenNodeTool{},
		// 画布完整操作权六件套（读走服务端快照，写经前端执行，见 tools_canvas_ops.go）。
		&ReadCanvasTool{Reader: canvas},
		&CreateNodeTool{Catalog: modelCatalog},
		&UpdateNodeTool{Reader: canvas, Catalog: modelCatalog},
		&DeleteNodeTool{Reader: canvas},
		&ConnectNodesTool{Reader: canvas},
		&RemoveConnectionTool{Reader: canvas},
		// 能力查询两件套：让 agent 看见「这个节点能改什么、模型 id 是什么」。
		// 没有它们，写工具的校验只会把 agent 挡在门外却不告诉它门在哪
		//（见 tools_canvas_capability.go 的包注释）。
		&ListCanvasModelsTool{Catalog: modelCatalog},
		&DescribeCanvasNodeTool{Reader: canvas, Catalog: modelCatalog},
		// 大文本附件分段读取：超过内联阈值的 .txt/.md 与
		// 画布文字节点，引用行只给 artifact_id，正文靠它按需读。
		&ReadAttachmentTool{Assets: attachAssets, Objects: attachObjects, Canvas: canvas},
	}
	return &DBRegistry{repo: repo, resolver: resolver, genSvc: genSvc, signer: signer, builtins: builtins, byName: map[string]Tool{}}
}

func (r *DBRegistry) Snapshot() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools)+len(r.builtins))
	out = append(out, r.tools...)
	out = append(out, r.builtins...)
	return out
}

func (r *DBRegistry) ByName(name string) (Tool, bool) {
	for _, b := range r.builtins {
		if info, err := b.Info(context.Background()); err == nil && info.Name == name {
			return b, true
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

// builtinDisplayNames 是内置工具的面向用户名称。内置工具不在 agent_tools 表里，
// 没有后台可配的 display_name，中文名只能与工具定义一样住在代码里。
var builtinDisplayNames = map[string]string{
	ListCanvasModelsToolName:      "查可用模型",
	DescribeCanvasNodeToolName:    "查节点能力",
	OutputSkillDefinitionToolName: "输出技能定义",
	AnalyzeImageToolName:          "分析图片",
	AnalyzeVideoToolName:          "分析视频",
	AnalyzeAudioToolName:          "分析音频",
	ReadAttachmentToolName:        "读取文本附件",
	CreateTextNodeToolName:        "创建文本节点",
	LoadSkillToolName:             "加载技能",
	CreateGenNodeToolName:         "创建生成节点",
	ReadCanvasToolName:            "读取画布",
	CreateNodeToolName:            "创建节点",
	UpdateNodeToolName:            "更新节点",
	DeleteNodeToolName:            "删除节点",
	ConnectNodesToolName:          "连接节点",
	RemoveConnectionToolName:      "断开连线",
}

func (r *DBRegistry) DisplayName(name string) string {
	r.mu.RLock()
	dn := r.displayNames[name]
	r.mu.RUnlock()
	if dn != "" {
		return dn
	}
	return builtinDisplayNames[name]
}

func (r *DBRegistry) RefreshFromDB() error {
	rows, err := r.repo.ListPublished()
	if err != nil {
		return fmt.Errorf("list published: %w", err)
	}

	tools := make([]Tool, 0, len(rows))
	byName := make(map[string]Tool, len(rows))
	displayNames := make(map[string]string, len(rows))
	for _, row := range rows {
		modelSlug, err := r.resolver.ResolveModelSlug(row.SourceModelID)
		if err != nil {
			log.Printf("agent registry: skip tool %q — resolve model_id=%d: %v",
				row.ToolName, row.SourceModelID, err)
			continue
		}
		// maxWait 解析失败不致命：slug 已经解析成功、工具照样能用，只是把 MaxWait
		// 留空让 pollUntilDone 回退到 defaultMaxWait，而不是因此整个工具不加载。
		maxWait, err := r.resolver.ResolvePollMaxWait(row.SourceModelID)
		if err != nil {
			log.Printf("agent registry: tool %q — resolve poll maxWait model_id=%d: %v (回退默认)",
				row.ToolName, row.SourceModelID, err)
			maxWait = 0
		}
		tool, err := buildDynamicFromRow(row, modelSlug, maxWait, r.genSvc, r.signer)
		if err != nil {
			log.Printf("agent registry: skip tool %q — build: %v", row.ToolName, err)
			continue
		}
		tools = append(tools, tool)
		byName[row.ToolName] = tool
		if row.DisplayName != "" {
			displayNames[row.ToolName] = row.DisplayName
		}
	}

	names := make([]string, 0, len(tools))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	// 指纹带上 tools/rows 计数，这样「有行解析失败导致 3/4」这种变化也算变了、会打。
	fingerprint := fmt.Sprintf("%d/%d:%s", len(tools), len(rows), strings.Join(names, ","))

	r.mu.Lock()
	r.tools = tools
	r.byName = byName
	r.displayNames = displayNames
	changed := !r.loggedOnce || fingerprint != r.lastFingerprint
	r.lastFingerprint = fingerprint
	r.loggedOnce = true
	r.mu.Unlock()

	// 锁外打日志，别拿着写锁做 IO。
	if changed {
		log.Printf("agent registry: loaded %d/%d tools: %v", len(tools), len(rows), names)
	}
	return nil
}

func buildDynamicFromRow(row AgentToolRow, modelSlug string, maxWait time.Duration, genSvc GenerationService, signer URLSigner) (Tool, error) {
	var paramsSchema map[string]ParameterSpec
	if err := json.Unmarshal(row.ParamsSchema, &paramsSchema); err != nil {
		return nil, fmt.Errorf("parse params_schema for %s: %w", row.ToolName, err)
	}
	var defaultParams map[string]any
	if err := json.Unmarshal(row.DefaultParams, &defaultParams); err != nil {
		defaultParams = map[string]any{}
	}
	t := &DynamicTool{
		ToolName:      row.ToolName,
		Category:      row.Category,
		ModelID:       modelSlug,
		Description:   row.ToolDescription,
		ParamsSchema:  paramsSchema,
		DefaultParams: defaultParams,
		GenService:    genSvc,
		Signer:        signer,
	}
	// 只有拿到正数预算才覆盖 MaxWait——加 30s 余量覆盖「后端 finalize 写库成
	// succeeded」与「工具下一次 Get 轮到」之间最多一个 poll interval 的间隙，避免
	// 差几秒踩空。maxWait<=0（resolver 出错或 model 没配）时保持 0，让 pollUntilDone
	// 走 defaultMaxWait，绝不把上限反而调得比默认还小。
	if maxWait > 0 {
		t.MaxWait = maxWait + 30*time.Second
	}
	return t, nil
}

// MarkChanged 是 admin-hook 的入口——admin handler 在 create/update/delete 之后
// 调用它，让 RefreshFromDB 近乎实时地发生，不用等定期轮询。
func (r *DBRegistry) MarkChanged() { go func() { _ = r.RefreshFromDB() }() }

// StartPeriodicRefresh 在一个 goroutine 里运行，每隔 interval 刷新一次。
// 传入一个会在 shutdown 时取消的 ctx。
func (r *DBRegistry) StartPeriodicRefresh(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = r.RefreshFromDB()
			}
		}
	}()
	return done
}
