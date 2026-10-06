// apps/api/internal/agent/agent_tools_repo_test.go
package agent

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestAgentToolsRepoCRUD(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	if _, err := conn.Exec(`DELETE FROM agent_tools`); err != nil {
		t.Fatal(err)
	}

	// 需要一行 model_configs 来满足外键——用唯一 slug，这样重复跑也不会撞上
	// UNIQUE (model_id) 约束。
	slug := "test-model-" + uuid.NewString()
	res, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('image','test gen',?,'/v1/images/generations','{}')`, slug)
	if err != nil {
		t.Fatalf("seed model_configs: %v", err)
	}
	modelID, _ := res.LastInsertId()

	repo := NewAgentToolsRepo(conn)
	in := AgentToolRow{
		ToolName:        "generate_image_test",
		DisplayName:     "Test Generator",
		Category:        "image",
		SourceModelID:   modelID,
		ToolDescription: "Generate test images.",
		ParamsSchema:    json.RawMessage(`{"prompt":{"type":"string","required":true}}`),
		DefaultParams:   json.RawMessage(`{}`),
		IsPublished:     true,
	}
	id, err := repo.Create(in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ToolName != "generate_image_test" {
		t.Fatalf("name: %+v", got)
	}

	listed, err := repo.ListPublished()
	if err != nil {
		t.Fatalf("ListPublished: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("want 1 published, got %d", len(listed))
	}
}

func TestAgentToolsRepoListPublishedExcludesUnpublished(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	if _, err := conn.Exec(`DELETE FROM agent_tools`); err != nil {
		t.Fatal(err)
	}

	slug := "x-" + uuid.NewString()
	res, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('image','x',?,'/x','{}')`, slug)
	if err != nil {
		t.Fatalf("seed model_configs: %v", err)
	}
	modelID, _ := res.LastInsertId()

	repo := NewAgentToolsRepo(conn)
	_, _ = repo.Create(AgentToolRow{ToolName: "published", Category: "image", SourceModelID: modelID, IsPublished: true, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`)})
	_, _ = repo.Create(AgentToolRow{ToolName: "draft", Category: "image", SourceModelID: modelID, IsPublished: false, ParamsSchema: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`)})

	listed, _ := repo.ListPublished()
	if len(listed) != 1 || listed[0].ToolName != "published" {
		t.Fatalf("want only published, got %+v", listed)
	}
}

func TestAgentToolsMaxUpdatedAtParsesText(t *testing.T) {
	conn := openTestDB(t)
	repo := NewAgentToolsRepo(conn)
	res, err := conn.Exec(`INSERT INTO model_configs (type, name, model_id, base_path, request_template) VALUES ('image','x','m-max','/x','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	mid, _ := res.LastInsertId()
	if _, err := repo.Create(AgentToolRow{ToolName: "gen_max", Category: "image", SourceModelID: mid, ToolDescription: "d",
		ParamsSchema: json.RawMessage(`{"prompt":{"type":"string"}}`)}); err != nil {
		t.Fatal(err)
	}
	ts, err := repo.MaxUpdatedAt()
	if err != nil || ts.IsZero() {
		t.Fatalf("MaxUpdatedAt: %v %v", ts, err)
	}
	var typ string
	_ = conn.QueryRow(`SELECT typeof(params_schema) FROM agent_tools WHERE tool_name='gen_max'`).Scan(&typ)
	if typ != "text" {
		t.Fatalf("params_schema 应存 text，实际 %s", typ)
	}
}
