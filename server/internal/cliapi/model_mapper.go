package cliapi

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

// model_mapper 把管理员配置的模型目录翻成 CLI 的公共模型契约。
//
// 两件事必须守住：
//
//  1. **绝不泄漏上游细节**。basePath / requestTemplate / pollingTemplate /
//     adminNote 都不进公共 DTO——它们含上游路径与协议，CLI 拿到既无用又危险。
//  2. **uiSchema 归一化成 JSON Schema**。uiSchema 是给网页渲染下拉框用的内部形状，
//     直接暴露等于把前端渲染细节变成公共 API。Agent 能否稳定用 CLI，主要取决于
//     这份 schema 是否完整。

// JSONSchemaProperty 是归一化后的单个输入参数描述。
type JSONSchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Default     any    `json:"default,omitempty"`
}

type JSONSchema struct {
	Type       string                        `json:"type"`
	Properties map[string]JSONSchemaProperty `json:"properties"`
	Required   []string                      `json:"required,omitempty"`
}

// Model 是 CLI 看到的模型。稳定 modelId 才是权威值；Name 只用于本地易用性解析。
type Model struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // image | video | audio | text
	// Available=false 的模型仍出现在列表里，但必须给出原因——
	// 不能等用户提交生成后才发现不可用。
	Available         bool       `json:"available"`
	UnavailableReason string     `json:"unavailableReason,omitempty"`
	InputSchema       JSONSchema `json:"inputSchema"`
}

// uiSchemaField 是内部 uiSchema 的单字段形状，与前端 UISchemaField 对应。
type uiSchemaField struct {
	Label   string `json:"label"`
	Default any    `json:"default"`
	Options []struct {
		Value any    `json:"value"`
		Label string `json:"label"`
	} `json:"options"`
}

// ToModel 把内部模型配置翻成公共 DTO。
func ToModel(m models.ModelConfig) Model {
	out := Model{
		ModelID:   m.ModelID,
		Name:      m.Name,
		Kind:      m.Type,
		Available: m.Enabled,
		InputSchema: JSONSchema{
			Type:       "object",
			Properties: map[string]JSONSchemaProperty{},
		},
	}
	if !m.Enabled {
		out.UnavailableReason = "该模型已被管理员停用"
	}

	var schema map[string]uiSchemaField
	if len(m.UISchema) > 0 {
		_ = json.Unmarshal(m.UISchema, &schema)
	}
	// map 遍历序不稳定，但 JSON object 本身无序，无碍；required 单独排序保证稳定输出。
	for key, field := range schema {
		prop := JSONSchemaProperty{Description: field.Label}
		for _, opt := range field.Options {
			prop.Enum = append(prop.Enum, opt.Value)
		}
		prop.Default = field.Default
		prop.Type = inferJSONType(field.Default, prop.Enum)
		out.InputSchema.Properties[key] = prop
	}

	// prompt 是画布所有生成节点的固有输入，不在 uiSchema 里（uiSchema 只管
	// 下拉框那几个参数）。不注入的话，Agent 读 schema 会以为不用传提示词。
	out.InputSchema.Properties["prompt"] = JSONSchemaProperty{
		Type:        "string",
		Description: "生成提示词。留空时回退到上游 text-node 的文本；都为空则用占位符。",
	}
	out.InputSchema.Required = []string{"prompt"}
	sort.Strings(out.InputSchema.Required)
	return out
}

// inferJSONType 从 default 或第一个枚举值推断 JSON 类型。
// 都没有时退回 string——自由文本字段比「凭空猜一个类型」安全。
func inferJSONType(def any, enum []any) string {
	probe := def
	if probe == nil && len(enum) > 0 {
		probe = enum[0]
	}
	switch probe.(type) {
	case float64, int, int64, json.Number:
		return "number"
	case bool:
		return "boolean"
	default:
		return "string"
	}
}

// FilterModels 按类型与关键字收窄模型列表；空条件不过滤。
// 搜索同时命中 modelId 与 name，大小写不敏感。
func FilterModels(list []models.ModelConfig, kind, q string) []models.ModelConfig {
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]models.ModelConfig, 0, len(list))
	for _, m := range list {
		if kind != "" && m.Type != kind {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(m.ModelID), q) &&
			!strings.Contains(strings.ToLower(m.Name), q) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ResolveModelRef 把用户给的模型引用解析成唯一模型。
//
// 顺序：ID 精确 → 名称精确。名称匹配到多个时报 MODEL_REF_AMBIGUOUS 并带候选，
// **不取第一个**——展示名、供应商内部名、稳定 ID 是三样
// 东西，混为一谈会让用户以为自己选的是 A、实际跑的是 B。
func ResolveModelRef(list []models.ModelConfig, ref string) (models.ModelConfig, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return models.ModelConfig{}, New(CodeInvalidArgument, "模型不能为空，请用 tvmao model list 查看可用模型")
	}
	for _, m := range list {
		if m.ModelID == ref {
			return m, nil
		}
	}
	var hits []models.ModelConfig
	for _, m := range list {
		if m.Name == ref {
			hits = append(hits, m)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return models.ModelConfig{}, Errf(CodeModelNotFound, "找不到模型 %q，请用 tvmao model list 查看可用模型", ref)
	default:
		ids := make([]string, 0, len(hits))
		for _, m := range hits {
			ids = append(ids, m.ModelID)
		}
		return models.ModelConfig{}, Errf(CodeModelRefAmbiguous, "模型名称 %q 匹配到多个结果，请改用稳定 modelId", ref).
			WithDetails(map[string]any{"candidates": ids})
	}
}
