package skillruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

func TestRunWorkspaceCreatesIndependentSkillCopies(t *testing.T) {
	source := filepath.Join(t.TempDir(), "storyboard")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "rules")
	script := filepath.Join(source, "scripts", "validate.py")
	writeTestFile(t, script, "print('ok')")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace.now = func() time.Time {
		return time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	}

	first, err := workspace.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspace.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("Run directories must be unique")
	}
	for _, root := range []string{first, second} {
		if filepath.Base(filepath.Dir(root)) != "20260710" || !strings.HasPrefix(filepath.Base(root), "run-") {
			t.Fatalf("unexpected workspace path: %s", root)
		}
		assertMode(t, root, 0o700)
		assertMode(t, filepath.Join(root, "SKILL.md"), 0o600)
		assertMode(t, filepath.Join(root, "scripts"), 0o700)
		assertMode(t, filepath.Join(root, "scripts", "validate.py"), 0o700)
	}

	if err := os.WriteFile(filepath.Join(first, "candidate.json"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "candidate.json"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstJSON, err := os.ReadFile(filepath.Join(first, "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := os.ReadFile(filepath.Join(second, "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != "first" || string(secondJSON) != "second" {
		t.Fatalf("workspace contents collided: %q %q", firstJSON, secondJSON)
	}
}

func TestRunWorkspaceRejectsSymlinks(t *testing.T) {
	source := filepath.Join(t.TempDir(), "skill")
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "rules")
	if err := os.Symlink("SKILL.md", filepath.Join(source, "linked.md")); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Create(source); err == nil || !strings.Contains(err.Error(), "unsupported Skill file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestWorkspaceBackendWritesAndEditsInsideRun(t *testing.T) {
	runDir := t.TempDir()
	backend, err := NewWorkspaceBackend(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: "drafts/candidate.json", Content: `{"ok":true,"again":true}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Edit(context.Background(), &filesystem.EditRequest{
		FilePath: "drafts/candidate.json", OldString: "true", NewString: "false", ReplaceAll: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(runDir, "drafts", "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":false,"again":false}` {
		t.Fatalf("unexpected edited content: %s", got)
	}
	assertMode(t, filepath.Join(runDir, "drafts"), 0o700)
	assertMode(t, filepath.Join(runDir, "drafts", "candidate.json"), 0o600)

	if err := backend.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: "single.txt", Content: "same same",
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Edit(context.Background(), &filesystem.EditRequest{
		FilePath: "single.txt", OldString: "same", NewString: "new",
	}); err == nil {
		t.Fatal("expected ambiguous single replacement to fail")
	}
}

func TestWorkspaceBackendRejectsWriteEscapes(t *testing.T) {
	parent := t.TempDir()
	runDir := filepath.Join(parent, "run")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	backend, err := NewWorkspaceBackend(runDir)
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{"../escape.json", filepath.Join(outside, "absolute.json")}
	for _, path := range requests {
		if err := backend.Write(context.Background(), &filesystem.WriteRequest{FilePath: path, Content: "x"}); err == nil {
			t.Fatalf("expected write escape rejection for %q", path)
		}
	}

	if err := os.Symlink(outside, filepath.Join(runDir, "linked-parent")); err != nil {
		t.Fatal(err)
	}
	if err := backend.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: "linked-parent/escape.json", Content: "x",
	}); err == nil {
		t.Fatal("expected symlinked parent rejection")
	}

	outsideFile := filepath.Join(outside, "secret.json")
	writeTestFile(t, outsideFile, "secret")
	if err := os.Symlink(outsideFile, filepath.Join(runDir, "linked-target.json")); err != nil {
		t.Fatal(err)
	}
	if err := backend.Write(context.Background(), &filesystem.WriteRequest{
		FilePath: "linked-target.json", Content: "overwrite",
	}); err == nil {
		t.Fatal("expected symlink target rejection")
	}
	if err := backend.Write(context.Background(), &filesystem.WriteRequest{FilePath: ".", Content: "x"}); err == nil {
		t.Fatal("expected directory target rejection")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}
