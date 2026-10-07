// apps/api/internal/agent/backbone_test.go
package agent

import (
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
