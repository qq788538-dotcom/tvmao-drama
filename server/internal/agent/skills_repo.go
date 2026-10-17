// apps/api/internal/agent/skills_repo.go
package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrSkillNotFound = errors.New("skill not found")

// Skill 描述一个 agent skill（L1 force_tool / L2 force_tool + prompt addon
// / 留给后续阶段的 L3 workflow）。JSON tag 用 snake_case，以对齐 admin SkillsPage
// 和 web SkillPopover 消费的协议形态。
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Category 是主分类（= Categories 首元素）：排序按它的分类顺序，旧读路径
	// 兼容。写入统一走 normalizeSkillCategories 保证两者一致。
	Category          string   `json:"category"`
	Categories        []string `json:"categories"`
	IconURL           string   `json:"icon_url"`
	SkillType         string   `json:"skill_type"` // 'force_tool' | 'workflow' | 'meta'
	SystemPromptAddon string   `json:"system_prompt_addon"`
	ForceTools        []string `json:"force_tools"`
	// TriggerKeywords 是关键词自动触发的词表：用户消息文本命中任一词（子串、
	// 大小写不敏感）时自动挂载本技能。空 = 不参与自动触发，即该列自身就是开关。
	TriggerKeywords []string        `json:"trigger_keywords"`
	ParamDefaults   map[string]any  `json:"param_defaults"`
	WorkflowSteps   json.RawMessage `json:"workflow_steps,omitempty"`
	IsOfficial      bool            `json:"is_official"`
	CreatorID       sql.NullInt64   `json:"-"`
	IsPublished     bool            `json:"is_published"`
	// IsFeatured = 首页首推：首页故事灵感入口下方那排 chip 就是它们（仅已发布
	// 的官方 skill 生效，排序复用 SortWeight）。
	IsFeatured bool  `json:"is_featured"`
	UseCount   int64 `json:"use_count"`
	SortWeight int   `json:"sort_weight"`
	// AutoTriggered 不落库：只在一次 Run 内标记"这个 skill 是关键词自动挂上的，
	// 不是用户手动选的"，让 skill block 能带上 auto 标记供前端区分展示。
	AutoTriggered bool      `json:"-"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// DecodedWorkflowSteps 把 JSON 编码的 workflow_steps 列反序列化成一个带类型的
// []WorkflowStep slice。当该列为空、或装着字面量 `null` 时返回 (nil, nil)——调用方
// 应当把它当作「没有 workflow」，不去调 workflow executor。
func (s *Skill) DecodedWorkflowSteps() ([]WorkflowStep, error) {
	if len(s.WorkflowSteps) == 0 || string(s.WorkflowSteps) == "null" {
		return nil, nil
	}
	var out []WorkflowStep
	if err := json.Unmarshal(s.WorkflowSteps, &out); err != nil {
		return nil, fmt.Errorf("decode workflow_steps: %w", err)
	}
	return out, nil
}

type SkillsRepo interface {
	Get(id string) (*Skill, error)
	Create(s Skill) error
	Update(s Skill) error
	Delete(id string) error
	List() ([]Skill, error)
	ListPublicByCategory(category string) ([]Skill, error)
	ListForUser(userID int64) ([]Skill, error)
	// ListFeatured 是首页首推行的数据源：已发布 + 官方 + is_featured，按权重。
	ListFeatured() ([]Skill, error)
	IncrementUseCount(id string) error
}

type sqliteSkillsRepo struct{ db *sql.DB }

func NewSkillsRepo(db *sql.DB) SkillsRepo { return &sqliteSkillsRepo{db: db} }

const skillColumns = `id, name, description, category, categories, icon_url, skill_type, system_prompt_addon,
		force_tools, param_defaults, workflow_steps, trigger_keywords, is_official, creator_id, is_published,
		is_featured, use_count, sort_weight, created_at, updated_at`

// normalizeSkillCategories 收敛单值/多值两个字段：Categories 是真源，空则回退
// 单值 Category；主分类（首元素）回写 Category。写入前必调，保证两列一致。
func normalizeSkillCategories(s *Skill) (categoriesJSON []byte) {
	if len(s.Categories) == 0 && s.Category != "" {
		s.Categories = []string{s.Category}
	}
	if len(s.Categories) > 0 {
		s.Category = s.Categories[0]
	} else {
		s.Category = ""
	}
	b, _ := json.Marshal(s.Categories)
	return b
}

func (r *sqliteSkillsRepo) Create(s Skill) error {
	forceTools, paramDefaults, workflowSteps, triggerKeywords := encodeSkillJSON(s)
	categories := normalizeSkillCategories(&s)
	_, err := r.db.Exec(`INSERT INTO skills
		(id, name, description, category, categories, icon_url, skill_type, system_prompt_addon,
		 force_tools, param_defaults, workflow_steps, trigger_keywords, is_official, creator_id, is_published,
		 is_featured, use_count, sort_weight)
		VALUES (?,?,?,?,?,?, ?,?, ?,?,?,?, ?,?,?, ?,?,?)`,
		s.ID, s.Name, s.Description, s.Category, string(categories), s.IconURL, s.SkillType, s.SystemPromptAddon,
		string(forceTools), string(paramDefaults), string(workflowSteps), string(triggerKeywords), boolToInt(s.IsOfficial), s.CreatorID, boolToInt(s.IsPublished),
		boolToInt(s.IsFeatured), s.UseCount, s.SortWeight)
	return err
}

func (r *sqliteSkillsRepo) Update(s Skill) error {
	forceTools, paramDefaults, workflowSteps, triggerKeywords := encodeSkillJSON(s)
	categories := normalizeSkillCategories(&s)
	_, err := r.db.Exec(`UPDATE skills SET
		name=?, description=?, category=?, categories=?, icon_url=?, skill_type=?, system_prompt_addon=?,
		force_tools=?, param_defaults=?, workflow_steps=?, trigger_keywords=?, is_official=?, creator_id=?, is_published=?,
		is_featured=?, sort_weight=?
		WHERE id=?`,
		s.Name, s.Description, s.Category, string(categories), s.IconURL, s.SkillType, s.SystemPromptAddon,
		string(forceTools), string(paramDefaults), string(workflowSteps), string(triggerKeywords), boolToInt(s.IsOfficial), s.CreatorID, boolToInt(s.IsPublished),
		boolToInt(s.IsFeatured), s.SortWeight, s.ID)
	return err
}

func (r *sqliteSkillsRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM skills WHERE id=?`, id)
	return err
}

func (r *sqliteSkillsRepo) Get(id string) (*Skill, error) {
	row := r.db.QueryRow(`SELECT `+skillColumns+` FROM skills WHERE id=?`, id)
	return scanSkill(row)
}

// categoryOrderExpr 把分类的展示顺序注入排序：按 skill_categories.sort_order，
// 不在字典里的孤儿分类排最后。曾经按分类名字符串序排——分类顺序应由后台的
// 分类管理决定，不是拼音/编码顺序。
const categoryOrderExpr = `COALESCE((SELECT c.sort_order FROM skill_categories c WHERE c.name = skills.category), 999999)`

func (r *sqliteSkillsRepo) List() ([]Skill, error) {
	return r.list(`SELECT ` + skillColumns + ` FROM skills ORDER BY ` + categoryOrderExpr + `, category, sort_weight DESC, id ASC`)
}

func (r *sqliteSkillsRepo) ListPublicByCategory(category string) ([]Skill, error) {
	if category == "" {
		// 无分类过滤 = SkillPopover 的「全部」视图：先按分类顺序、再按组内权重。
		return r.list(`SELECT ` + skillColumns + ` FROM skills
			WHERE is_published=1 AND is_official=1
			ORDER BY ` + categoryOrderExpr + `, category, sort_weight DESC, id ASC`)
	}
	// 多分类：主分类命中或 categories 数组包含均算命中（无分类的 skill 保持
	// 「任何分类下都可见」的既有语义）。
	return r.list(`SELECT `+skillColumns+` FROM skills
		WHERE is_published=1 AND is_official=1
		  AND (category=? OR category='' OR EXISTS (
		       SELECT 1 FROM json_each(COALESCE(skills.categories, '[]')) WHERE json_each.value = ?))
		ORDER BY sort_weight DESC, id ASC`, category, category)
}

func (r *sqliteSkillsRepo) ListForUser(userID int64) ([]Skill, error) {
	// 公开的官方 skill + 用户自己的 skill（creator_id 匹配，无视 published 标志
	// ——作者总能看到自己的草稿）。
	return r.list(`SELECT `+skillColumns+` FROM skills
		WHERE (is_published=1 AND is_official=1) OR creator_id=?
		ORDER BY category, sort_weight DESC, id ASC`, userID)
}

func (r *sqliteSkillsRepo) ListFeatured() ([]Skill, error) {
	return r.list(`SELECT ` + skillColumns + ` FROM skills
		WHERE is_published=1 AND is_official=1 AND is_featured=1
		ORDER BY sort_weight DESC, id ASC`)
}

func (r *sqliteSkillsRepo) IncrementUseCount(id string) error {
	_, err := r.db.Exec(`UPDATE skills SET use_count=use_count+1 WHERE id=?`, id)
	return err
}

func (r *sqliteSkillsRepo) list(q string, args ...any) ([]Skill, error) {
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Skill, 0)
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func encodeSkillJSON(s Skill) (forceTools, paramDefaults, workflowSteps, triggerKeywords []byte) {
	if s.ForceTools == nil {
		forceTools = []byte(`[]`)
	} else {
		b, err := json.Marshal(s.ForceTools)
		if err != nil || len(b) == 0 {
			forceTools = []byte(`[]`)
		} else {
			forceTools = b
		}
	}
	if s.ParamDefaults == nil {
		paramDefaults = []byte(`{}`)
	} else {
		b, err := json.Marshal(s.ParamDefaults)
		if err != nil || len(b) == 0 {
			paramDefaults = []byte(`{}`)
		} else {
			paramDefaults = b
		}
	}
	if len(s.WorkflowSteps) == 0 {
		workflowSteps = []byte(`[]`)
	} else {
		workflowSteps = []byte(s.WorkflowSteps)
	}
	// nil 与编码失败都落 `[]`：与 force_tools 同待遇，读回来 decode 不用分支，
	// 也免掉「NULL 还是空数组」这种在 SQL 里要额外 COALESCE 的二义。
	if s.TriggerKeywords == nil {
		triggerKeywords = []byte(`[]`)
	} else if b, err := json.Marshal(s.TriggerKeywords); err != nil || len(b) == 0 {
		triggerKeywords = []byte(`[]`)
	} else {
		triggerKeywords = b
	}
	return forceTools, paramDefaults, workflowSteps, triggerKeywords
}

func scanSkill(rs rowScanner) (*Skill, error) {
	var s Skill
	var forceTools, paramDefaults, workflowSteps, categories, triggerKeywords []byte
	var isOfficial, isPublished, isFeatured int
	if err := rs.Scan(
		&s.ID, &s.Name, &s.Description, &s.Category, &categories, &s.IconURL, &s.SkillType, &s.SystemPromptAddon,
		&forceTools, &paramDefaults, &workflowSteps, &triggerKeywords,
		&isOfficial, &s.CreatorID, &isPublished,
		&isFeatured, &s.UseCount, &s.SortWeight, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSkillNotFound
		}
		return nil, err
	}
	s.IsOfficial = isOfficial != 0
	s.IsPublished = isPublished != 0
	s.IsFeatured = isFeatured != 0
	s.ForceTools = decodeForceTools(forceTools)
	// 列为 NULL（迁移刚加、存量行未回填）时 decodeForceTools 返回 nil = 不参与自动触发。
	s.TriggerKeywords = decodeForceTools(triggerKeywords)
	// categories 列 NULL（老数据未回填/独立环境没跑回填）时回退单值 category。
	s.Categories = decodeForceTools(categories)
	if len(s.Categories) == 0 && s.Category != "" {
		s.Categories = []string{s.Category}
	}
	s.ParamDefaults = decodeParamDefaults(paramDefaults)
	if len(workflowSteps) == 0 {
		s.WorkflowSteps = json.RawMessage(`[]`)
	} else {
		s.WorkflowSteps = json.RawMessage(append([]byte(nil), workflowSteps...))
	}
	return &s, nil
}

func decodeForceTools(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func decodeParamDefaults(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}
