// apps/api/internal/agent/backbone_test.go
package agent

import (
	"errors"
	"testing"
)

func TestBackboneRepoCRUD(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewBackboneRepo(conn)

	in := BackboneModel{
		Alias:         "planner-claude-sonnet",
		Provider:      "anthropic",
		ModelName:     "claude-sonnet-4-5-20250929",
		Role:          RolePlanner,
		MaxTokens:     4096,
		ContextWindow: 32768,
		IsActive:      true,
	}
	id, err := repo.Create(in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Alias != in.Alias || got.Role != RolePlanner {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.ContextWindow != 32768 {
		t.Fatalf("context_window round-trip failed: want 32768, got %d", got.ContextWindow)
	}

	active, err := repo.GetActiveByRole(RolePlanner)
	if err != nil {
		t.Fatalf("GetActiveByRole: %v", err)
	}
	if active.ID != id {
		t.Fatalf("active mismatch: want %d got %d", id, active.ID)
	}
}

func TestBackboneRepoGetActiveByRoleNotFound(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewBackboneRepo(conn)
	_, err := repo.GetActiveByRole(RolePlanner)
	if err != ErrNoActiveBackbone {
		t.Fatalf("want ErrNoActiveBackbone, got %v", err)
	}
}

func TestBackboneSwitcherSetAndReset(t *testing.T) {
	conn := openTestDB(t)
	repo := NewBackboneRepo(conn)
	sw := NewBackboneSwitcher(conn)
	id, err := repo.Create(BackboneModel{Alias: "主Agent", Provider: "openai", ModelName: "canvas-agent-gpt-5.5",
		Role: RolePlanner, MaxTokens: 0, Temperature: 0, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.SetModelName(id, "gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(id)
	var overridden int
	_ = conn.QueryRow(`SELECT model_name_overridden FROM agent_backbone_models WHERE id=?`, id).Scan(&overridden)
	if got.ModelName != "gpt-5.5" || overridden != 1 {
		t.Fatalf("切换后 model=%s overridden=%d", got.ModelName, overridden)
	}
	if err := sw.ResetModelName(id, "canvas-agent-gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(id)
	_ = conn.QueryRow(`SELECT model_name_overridden FROM agent_backbone_models WHERE id=?`, id).Scan(&overridden)
	if got.ModelName != "canvas-agent-gpt-5.5" || overridden != 0 {
		t.Fatalf("恢复后 model=%s overridden=%d", got.ModelName, overridden)
	}
	if err := sw.SetModelName(9999, "x"); !errors.Is(err, ErrBackboneNotFound) {
		t.Fatalf("不存在的 id 应返回 ErrBackboneNotFound，得到 %v", err)
	}
}
