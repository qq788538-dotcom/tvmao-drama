// Package agent — tools_skill_load.go：内置的 load_skill 工具，让 planner 在执行
// 中途把另一个 skill 的指令加载进对话（「读取技能指令→已调用技能」，
// 支撑「编剧→导演→分镜」这类 skill 链式协作）。
//
// 机制与 skill 快照进历史同一条：指令文本以 tool_result 的形式进入对话记录，
// 随后续每轮历史重放自然带上、持续生效——不需要任何额外的状态管理。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// LoadSkillToolName 是 LLM 调用该工具时用的标准名字。
const LoadSkillToolName = "load_skill"

type LoadSkillTool struct {
	Skills SkillsRepo
}

func (t *LoadSkillTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"skill": {
			Type:     schema.String,
			Desc:     "Skill id or exact skill name to load (e.g. director-master or 导演大师).",
			Required: true,
		},
	}
	return &schema.ToolInfo{
		Name:        LoadSkillToolName,
		Desc:        "Load another skill's instructions into this conversation (they stay active for all later turns). Use when the current task needs a different skill's methodology, e.g. chaining 编剧→导演→分镜.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *LoadSkillTool) InvokableRun(_ context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", LoadSkillToolName, err)
	}
	query := strings.TrimSpace(args.Skill)
	if query == "" {
		return "", fmt.Errorf("%s: skill is required", LoadSkillToolName)
	}
	if t.Skills == nil {
		return "load_skill 不可用：skills 目录未接入。", nil
	}

	skill, err := t.resolve(query)
	if err != nil {
		// 找不到走可读文本而不是 error：error 会以「Tool load_skill failed」的
		// 形态喂回 planner，模型容易放弃；带候选清单的文本让它能自纠 skill 名。
		return err.Error(), nil
	}
	if skill.SkillType == "workflow" {
		return fmt.Sprintf("skill %q 是 workflow 类型（声明式步骤序列，不含对话指令），不能通过 load_skill 加载。", skill.Name), nil
	}
	if strings.TrimSpace(skill.SystemPromptAddon) == "" {
		return fmt.Sprintf("skill %q 没有配置指令文本（system_prompt_addon 为空）。", skill.Name), nil
	}

	// 返回体就是指令全文：它会作为 tool_result 落库，从此随历史重放持续生效。
	// 前缀声明让后续轮次的 planner 知道这段是「已加载的技能指令」而非普通工具输出。
	if sys := buildSkillSystemMessage(skill); sys != nil {
		return "已加载技能指令，从现在起持续生效：\n\n" + sys.Content, nil
	}
	return fmt.Sprintf("skill %q 加载失败。", skill.Name), nil
}

// resolve 先按 id 精确查，再按 name 精确匹配（列表内存扫描——skills 是后台配置的
// 小目录，量级几十条）。都不中时返回带可用清单的错误，帮 planner 自纠。
func (t *LoadSkillTool) resolve(query string) (*Skill, error) {
	if s, err := t.Skills.Get(query); err == nil && s != nil {
		return s, nil
	}
	all, err := t.Skills.List()
	if err != nil {
		return nil, fmt.Errorf("load_skill: 读取 skills 目录失败：%v", err)
	}
	names := make([]string, 0, len(all))
	for i := range all {
		if all[i].Name == query || all[i].ID == query {
			return &all[i], nil
		}
		names = append(names, all[i].Name)
	}
	return nil, fmt.Errorf("未找到 skill %q。可用的 skill：%s", query, strings.Join(names, "、"))
}
