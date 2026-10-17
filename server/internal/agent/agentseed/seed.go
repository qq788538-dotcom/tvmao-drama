// Package agentseed 把内置的 Skill、Skill 分类、Agent 工具目录、Agent 大模型
// （见仓库根 seed/README.md）写入 SQLite。
//
// 四张表全部内置、用户不能新建或编辑，所以语义是"种子即真相"：
// 每次启动整表同步——种子里没有的行删除，有的行按 id 覆盖。
// agent_backbone_models.model_name 写死为种子值：每次启动无条件覆盖并清除 model_name_overridden
// 标记，不提供修改入口。
package agentseed

import (
	"bufio"
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

//go:embed data/*.jsonl
var dataFS embed.FS

type Result struct {
	Categories, Skills, Tools, Backbones, SkippedTools int
}

type categoryRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
}

type skillRow struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Category          string          `json:"category"`
	Categories        json.RawMessage `json:"categories"`
	IconURL           string          `json:"icon_url"`
	SkillType         string          `json:"skill_type"`
	SystemPromptAddon string          `json:"system_prompt_addon"`
	ForceTools        json.RawMessage `json:"force_tools"`
	ParamDefaults     json.RawMessage `json:"param_defaults"`
	WorkflowSteps     json.RawMessage `json:"workflow_steps"`
	TriggerKeywords   json.RawMessage `json:"trigger_keywords"`
	IsOfficial        int             `json:"is_official"`
	IsPublished       int             `json:"is_published"`
	IsFeatured        int             `json:"is_featured"`
	SortWeight        int             `json:"sort_weight"`
}

type toolRow struct {
	ID              int64           `json:"id"`
	ToolName        string          `json:"tool_name"`
	DisplayName     string          `json:"display_name"`
	Category        string          `json:"category"`
	SourceModelID   int64           `json:"source_model_id"`
	ToolDescription string          `json:"tool_description"`
	ParamsSchema    json.RawMessage `json:"params_schema"`
	DefaultParams   json.RawMessage `json:"default_params"`
	IsPublished     int             `json:"is_published"`
	SortWeight      int             `json:"sort_weight"`
}

type backboneRow struct {
	ID              int64   `json:"id"`
	Alias           string  `json:"alias"`
	Provider        string  `json:"provider"`
	ModelName       string  `json:"model_name"`
	Role            string  `json:"role"`
	ThinkingEnabled int     `json:"thinking_enabled"`
	ThinkingBudget  int     `json:"thinking_budget"`
	MaxTokens       int     `json:"max_tokens"`
	ContextWindow   int     `json:"context_window"`
	Temperature     float64 `json:"temperature"`
	IsActive        int     `json:"is_active"`
	SortWeight      int     `json:"sort_weight"`
	Description     string  `json:"description"`
}

func readJSONL[T any](name string) ([]T, error) {
	raw, err := dataFS.ReadFile("data/" + name + ".jsonl")
	if err != nil {
		return nil, err
	}
	var out []T
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20) // 单行 Skill 提示词可达数十 KB
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("%s.jsonl: %w", name, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// jsonText 把种子里的 JSON 值转成库里的 TEXT；null / 缺失返回 nil（落 NULL）或 fallback。
func jsonText(v json.RawMessage, fallback string) any {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		if fallback == "" {
			return nil
		}
		return fallback
	}
	return s
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// Import 同步四张表，单事务。须在 model_configs 种子导入之后调用。
func Import(conn *sql.DB) (Result, error) {
	var res Result
	cats, err := readJSONL[categoryRow]("skill_categories")
	if err != nil {
		return res, err
	}
	skills, err := readJSONL[skillRow]("skills")
	if err != nil {
		return res, err
	}
	tools, err := readJSONL[toolRow]("agent_tools")
	if err != nil {
		return res, err
	}
	backbones, err := readJSONL[backboneRow]("agent_backbone_models")
	if err != nil {
		return res, err
	}

	tx, err := conn.Begin()
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()

	// skill_categories：先删再 upsert，避免改名后与旧行的 name 唯一键冲突。
	catIDs := make([]any, len(cats))
	for i, c := range cats {
		catIDs[i] = c.ID
	}
	if _, err := tx.Exec(`DELETE FROM skill_categories WHERE id NOT IN (`+placeholders(len(catIDs))+`)`, catIDs...); err != nil {
		return res, err
	}
	// 保留的行先改成按 id 唯一的临时名，再逐行 upsert：新种子若把两个分类的 name 互换，
	// 逐行覆盖会在中途撞上 name UNIQUE。临时名只活在本事务里。
	if _, err := tx.Exec(`UPDATE skill_categories SET name = '__seed_tmp_' || id WHERE id IN (`+placeholders(len(catIDs))+`)`, catIDs...); err != nil {
		return res, err
	}
	for _, c := range cats {
		if _, err := tx.Exec(`INSERT INTO skill_categories (id, name, color, sort_order) VALUES (?,?,?,?)
			ON CONFLICT (id) DO UPDATE SET name=excluded.name, color=excluded.color, sort_order=excluded.sort_order`,
			c.ID, c.Name, c.Color, c.SortOrder); err != nil {
			return res, fmt.Errorf("skill_categories %d: %w", c.ID, err)
		}
		res.Categories++
	}

	skillIDs := make([]any, len(skills))
	for i, s := range skills {
		skillIDs[i] = s.ID
	}
	if _, err := tx.Exec(`DELETE FROM skills WHERE is_official=1 AND id NOT IN (`+placeholders(len(skillIDs))+`)`, skillIDs...); err != nil {
		return res, err
	}
	for _, s := range skills {
		if _, err := tx.Exec(`INSERT INTO skills
			(id, name, description, category, categories, icon_url, skill_type, system_prompt_addon,
			 force_tools, param_defaults, workflow_steps, trigger_keywords, is_official, creator_id, is_published, is_featured, sort_weight)
			VALUES (?,?,?,?,?,?,?,?, ?,?,?,?, ?,NULL,?,?,?)
			ON CONFLICT (id) DO UPDATE SET
			  name=excluded.name, description=excluded.description, category=excluded.category,
			  categories=excluded.categories, icon_url=excluded.icon_url, skill_type=excluded.skill_type,
			  system_prompt_addon=excluded.system_prompt_addon, force_tools=excluded.force_tools,
			  param_defaults=excluded.param_defaults, workflow_steps=excluded.workflow_steps,
			  trigger_keywords=excluded.trigger_keywords, is_official=excluded.is_official,
			  is_published=excluded.is_published, is_featured=excluded.is_featured, sort_weight=excluded.sort_weight`,
			s.ID, s.Name, s.Description, s.Category, jsonText(s.Categories, ""), s.IconURL, s.SkillType, s.SystemPromptAddon,
			jsonText(s.ForceTools, "[]"), jsonText(s.ParamDefaults, "{}"), jsonText(s.WorkflowSteps, "[]"), jsonText(s.TriggerKeywords, ""),
			s.IsOfficial, s.IsPublished, s.IsFeatured, s.SortWeight); err != nil {
			return res, fmt.Errorf("skills %s: %w", s.ID, err)
		}
		res.Skills++
	}

	toolIDs := make([]any, len(tools))
	for i, t := range tools {
		toolIDs[i] = t.ID
	}
	if _, err := tx.Exec(`DELETE FROM agent_tools WHERE id NOT IN (`+placeholders(len(toolIDs))+`)`, toolIDs...); err != nil {
		return res, err
	}
	for _, t := range tools {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM model_configs WHERE id=?`, t.SourceModelID).Scan(&exists); err != nil {
			return res, err
		}
		if exists == 0 {
			// 模型种子里没有这个 id：先跳过，下次启动模型到位后自动补上。
			log.Printf("agentseed: 跳过工具 %s，模型 id=%d 不存在", t.ToolName, t.SourceModelID)
			res.SkippedTools++
			continue
		}
		if _, err := tx.Exec(`INSERT INTO agent_tools
			(id, tool_name, display_name, category, source_model_id, tool_description, params_schema, default_params, is_published, sort_weight)
			VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (id) DO UPDATE SET
			  tool_name=excluded.tool_name, display_name=excluded.display_name, category=excluded.category,
			  source_model_id=excluded.source_model_id, tool_description=excluded.tool_description,
			  params_schema=excluded.params_schema, default_params=excluded.default_params,
			  is_published=excluded.is_published, sort_weight=excluded.sort_weight`,
			t.ID, t.ToolName, t.DisplayName, t.Category, t.SourceModelID, t.ToolDescription,
			jsonText(t.ParamsSchema, "{}"), jsonText(t.DefaultParams, "{}"), t.IsPublished, t.SortWeight); err != nil {
			return res, fmt.Errorf("agent_tools %s: %w", t.ToolName, err)
		}
		res.Tools++
	}

	bbIDs := make([]any, len(backbones))
	for i, b := range backbones {
		bbIDs[i] = b.ID
	}
	if _, err := tx.Exec(`DELETE FROM agent_backbone_models WHERE id NOT IN (`+placeholders(len(bbIDs))+`)`, bbIDs...); err != nil {
		return res, err
	}
	for _, b := range backbones {
		if _, err := tx.Exec(`INSERT INTO agent_backbone_models
			(id, alias, provider, model_name, role, thinking_enabled, thinking_budget, max_tokens, context_window,
			 temperature, is_active, sort_weight, description)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (id) DO UPDATE SET
			  alias=excluded.alias, provider=excluded.provider,
			  model_name=excluded.model_name, model_name_overridden=0,
			  role=excluded.role, thinking_enabled=excluded.thinking_enabled, thinking_budget=excluded.thinking_budget,
			  max_tokens=excluded.max_tokens, context_window=excluded.context_window, temperature=excluded.temperature,
			  is_active=excluded.is_active, sort_weight=excluded.sort_weight, description=excluded.description`,
			b.ID, b.Alias, b.Provider, b.ModelName, b.Role, b.ThinkingEnabled, b.ThinkingBudget, b.MaxTokens, b.ContextWindow,
			b.Temperature, b.IsActive, b.SortWeight, b.Description); err != nil {
			return res, fmt.Errorf("agent_backbone_models %d: %w", b.ID, err)
		}
		res.Backbones++
	}
	return res, tx.Commit()
}
