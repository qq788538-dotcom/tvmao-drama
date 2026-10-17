// apps/api/internal/agent/agent_tools_repo.go
package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db"
)

var ErrAgentToolNotFound = errors.New("agent tool not found")

type AgentToolRow struct {
	ID              int64           `json:"id"`
	ToolName        string          `json:"tool_name"`
	DisplayName     string          `json:"display_name"`
	Category        string          `json:"category"`
	SourceModelID   int64           `json:"source_model_id"`
	ToolDescription string          `json:"tool_description"`
	ParamsSchema    json.RawMessage `json:"params_schema"`
	DefaultParams   json.RawMessage `json:"default_params"`
	IsPublished     bool            `json:"is_published"`
	SortWeight      int             `json:"sort_weight"`
	UseCount30d     int64           `json:"use_count_30d"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AgentToolsRepo interface {
	Create(t AgentToolRow) (int64, error)
	Update(t AgentToolRow) error
	Delete(id int64) error
	Get(id int64) (*AgentToolRow, error)
	List() ([]AgentToolRow, error)
	ListPublished() ([]AgentToolRow, error)
	MaxUpdatedAt() (time.Time, error)
}

type sqliteAgentToolsRepo struct{ db *sql.DB }

func NewAgentToolsRepo(db *sql.DB) AgentToolsRepo { return &sqliteAgentToolsRepo{db: db} }

func (r *sqliteAgentToolsRepo) Create(t AgentToolRow) (int64, error) {
	if len(t.ParamsSchema) == 0 {
		t.ParamsSchema = json.RawMessage(`{}`)
	}
	if len(t.DefaultParams) == 0 {
		t.DefaultParams = json.RawMessage(`{}`)
	}
	res, err := r.db.Exec(`INSERT INTO agent_tools
		(tool_name, display_name, category, source_model_id, tool_description,
		 params_schema, default_params, is_published, sort_weight)
		VALUES (?,?,?,?,?, ?,?,?,?)`,
		t.ToolName, t.DisplayName, t.Category, t.SourceModelID, t.ToolDescription,
		string(t.ParamsSchema), string(t.DefaultParams), boolToInt(t.IsPublished), t.SortWeight)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *sqliteAgentToolsRepo) Update(t AgentToolRow) error {
	if len(t.ParamsSchema) == 0 {
		t.ParamsSchema = json.RawMessage(`{}`)
	}
	if len(t.DefaultParams) == 0 {
		t.DefaultParams = json.RawMessage(`{}`)
	}
	_, err := r.db.Exec(`UPDATE agent_tools SET
		tool_name=?, display_name=?, category=?, source_model_id=?, tool_description=?,
		params_schema=?, default_params=?, is_published=?, sort_weight=?
		WHERE id=?`,
		t.ToolName, t.DisplayName, t.Category, t.SourceModelID, t.ToolDescription,
		string(t.ParamsSchema), string(t.DefaultParams), boolToInt(t.IsPublished), t.SortWeight,
		t.ID)
	return err
}

func (r *sqliteAgentToolsRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM agent_tools WHERE id=?`, id)
	return err
}

func (r *sqliteAgentToolsRepo) Get(id int64) (*AgentToolRow, error) {
	row := r.db.QueryRow(`SELECT id, tool_name, display_name, category, source_model_id, tool_description,
		params_schema, default_params, is_published, sort_weight, use_count_30d, created_at, updated_at
		FROM agent_tools WHERE id=?`, id)
	return scanAgentTool(row)
}

func (r *sqliteAgentToolsRepo) List() ([]AgentToolRow, error) {
	return r.list(`SELECT id, tool_name, display_name, category, source_model_id, tool_description,
		params_schema, default_params, is_published, sort_weight, use_count_30d, created_at, updated_at
		FROM agent_tools ORDER BY category, sort_weight DESC, id ASC`)
}

func (r *sqliteAgentToolsRepo) ListPublished() ([]AgentToolRow, error) {
	return r.list(`SELECT id, tool_name, display_name, category, source_model_id, tool_description,
		params_schema, default_params, is_published, sort_weight, use_count_30d, created_at, updated_at
		FROM agent_tools WHERE is_published=1 ORDER BY category, sort_weight DESC, id ASC`)
}

func (r *sqliteAgentToolsRepo) MaxUpdatedAt() (time.Time, error) {
	// MAX() 的结果没有声明类型，驱动以字符串返回（不会解析成 time.Time），扫成 string 再解析。
	var max sql.NullString
	if err := r.db.QueryRow(`SELECT MAX(updated_at) FROM agent_tools`).Scan(&max); err != nil {
		return time.Time{}, err
	}
	if !max.Valid || max.String == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation(db.TimeLayout, max.String, time.UTC)
}

func (r *sqliteAgentToolsRepo) list(q string, args ...any) ([]AgentToolRow, error) {
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentToolRow, 0)
	for rows.Next() {
		t, err := scanAgentTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func scanAgentTool(rs rowScanner) (*AgentToolRow, error) {
	var t AgentToolRow
	var paramsSchema, defaultParams []byte
	var published int
	if err := rs.Scan(&t.ID, &t.ToolName, &t.DisplayName, &t.Category, &t.SourceModelID, &t.ToolDescription,
		&paramsSchema, &defaultParams, &published, &t.SortWeight, &t.UseCount30d, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAgentToolNotFound
		}
		return nil, err
	}
	t.ParamsSchema = json.RawMessage(paramsSchema)
	t.DefaultParams = json.RawMessage(defaultParams)
	t.IsPublished = published != 0
	return &t, nil
}
