// Package agent — tools_meta.go：内置的 meta 工具，让 LLM 能在 meta-skill 流程中
// 发出一个 SkillDraft artifact。DBRegistry 把这个工具注入到每次 Snapshot/ByName 里，
// 这样无论 agent_tools 里发布了什么，它都始终可调用。
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// OutputSkillDefinitionToolName 是 LLM 调用该 meta 工具时用的标准名字。
const OutputSkillDefinitionToolName = "output_skill_definition"

// OutputSkillDefinitionTool 是内置的 meta 工具。它接收七个参数（SkillDraft 的各字段，
// 其中 force_tools 和 param_defaults 为了对 LLM 友好用 JSON 编码的字符串表示），并
// 发出一个 artifact_type 为 "skill_definition"、携带填充好的 SkillDraft 的 Artifact。
type OutputSkillDefinitionTool struct{}

func (t *OutputSkillDefinitionTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{
		"id": {
			Type:     schema.String,
			Desc:     "Stable, snake_case identifier for the skill (e.g. shiba_winter).",
			Required: true,
		},
		"name": {
			Type:     schema.String,
			Desc:     "Human-readable skill name shown in the UI.",
			Required: true,
		},
		"description": {
			Type: schema.String,
			Desc: "Short description of what the skill does.",
		},
		"category": {
			Type: schema.String,
			Desc: "Optional category, e.g. Image / Video / Text.",
		},
		"system_prompt_addon": {
			Type: schema.String,
			Desc: "Extra instructions appended to the system prompt when the skill is active.",
		},
		"force_tools": {
			Type: schema.String,
			Desc: "JSON-encoded array of tool names the skill must invoke, e.g. [\"generate_image_x\"].",
		},
		"param_defaults": {
			Type: schema.String,
			Desc: "JSON-encoded object of default parameter values applied when the skill runs.",
		},
	}
	return &schema.ToolInfo{
		Name:        OutputSkillDefinitionToolName,
		Desc:        "Emit a skill definition draft for the user to confirm and save. Use this during a meta-skill flow when the user is creating a new skill.",
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *OutputSkillDefinitionTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		Description       string `json:"description"`
		Category          string `json:"category"`
		SystemPromptAddon string `json:"system_prompt_addon"`
		ForceTools        string `json:"force_tools"`
		ParamDefaults     string `json:"param_defaults"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("%s: parse args: %w", OutputSkillDefinitionToolName, err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("%s: id is required", OutputSkillDefinitionToolName)
	}
	if args.Name == "" {
		return "", fmt.Errorf("%s: name is required", OutputSkillDefinitionToolName)
	}

	var forceTools []string
	if args.ForceTools != "" {
		if err := json.Unmarshal([]byte(args.ForceTools), &forceTools); err != nil {
			return "", fmt.Errorf("%s: parse force_tools: %w", OutputSkillDefinitionToolName, err)
		}
	}

	var paramDefaults map[string]any
	if args.ParamDefaults != "" {
		if err := json.Unmarshal([]byte(args.ParamDefaults), &paramDefaults); err != nil {
			return "", fmt.Errorf("%s: parse param_defaults: %w", OutputSkillDefinitionToolName, err)
		}
	}

	draft := &SkillDraft{
		ID:                args.ID,
		Name:              args.Name,
		Description:       args.Description,
		Category:          args.Category,
		SystemPromptAddon: args.SystemPromptAddon,
		ForceTools:        forceTools,
		ParamDefaults:     paramDefaults,
	}

	emit := EmitFromContext(ctx)
	emit(Artifact{
		ArtifactType: "skill_definition",
		ArtifactID:   uuid.NewString(),
		Source:       OutputSkillDefinitionToolName,
		Name:         draft.Name,
		Skill:        draft,
	})

	return fmt.Sprintf("Skill draft %q (%s) emitted; awaiting user confirmation.", draft.Name, draft.ID), nil
}
