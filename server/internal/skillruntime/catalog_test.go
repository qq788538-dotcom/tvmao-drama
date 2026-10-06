package skillruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
)

func TestRepositoryNovelSkillDeclaresCandidateArtifact(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	skillsRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "skillbundle", "skills"))
	if _, err := os.Stat(filepath.Join(skillsRoot, "novel-real-person-short-drama-v1")); err != nil {
		t.Fatalf("内置 Skill 缺失: %v", err)
	}
	catalog, err := NewCatalog(skillsRoot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "novel-real-person-short-drama-v1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OutputArtifact != "candidate.json" {
		t.Fatalf("unexpected output artifact: %q", snapshot.OutputArtifact)
	}
}

func TestCatalogSnapshotPreservesOutputArtifact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "artifact-style", "SKILL.md"), `---
name: artifact-style
description: Artifact style
output_artifact: candidate.json
---
Return the candidate.
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "artifact-style")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OutputArtifact != "candidate.json" {
		t.Fatalf("unexpected output artifact: %q", snapshot.OutputArtifact)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored Snapshot
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.OutputArtifact != "candidate.json" {
		t.Fatalf("output artifact was not preserved by JSON round trip: %q", restored.OutputArtifact)
	}
}

func TestCatalogValidatesOutputArtifact(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		want        string
		wantErr     bool
	}{
		{name: "absolute path", declaration: "output_artifact: /tmp/candidate.json\n", wantErr: true},
		{name: "parent traversal", declaration: "output_artifact: ../candidate.json\n", wantErr: true},
		{name: "parent directory", declaration: "output_artifact: ..\n", wantErr: true},
		{name: "cleaned parent traversal", declaration: "output_artifact: nested/../../candidate.json\n", wantErr: true},
		{name: "current directory", declaration: "output_artifact: .\n", wantErr: true},
		{name: "explicit empty", declaration: "output_artifact: \"\"\n", wantErr: true},
		{name: "empty scalar", declaration: "output_artifact:\n", wantErr: true},
		{name: "null scalar", declaration: "output_artifact: null\n", wantErr: true},
		{name: "tilde null scalar", declaration: "output_artifact: ~\n", wantErr: true},
		{name: "trimmed and cleaned", declaration: "output_artifact: \" ./nested/../candidate.json \"\n", want: "candidate.json"},
		{name: "omitted", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "artifact-style", "SKILL.md"), "---\nname: artifact-style\ndescription: Artifact style\n"+tt.declaration+"---\nReturn the candidate.\n")

			catalog, err := NewCatalog(root)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := catalog.Snapshot(context.Background(), "artifact-style")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected invalid output_artifact error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.OutputArtifact != tt.want {
				t.Fatalf("unexpected output artifact: %q", snapshot.OutputArtifact)
			}
		})
	}
}

func TestCatalogSupportsEinoFrontmatterDelimitersWithoutOutputArtifact(t *testing.T) {
	tests := []struct {
		name    string
		opening string
	}{
		{name: "leading whitespace", opening: "\n \t\n  ---"},
		{name: "opening trailing spaces", opening: "---   "},
		{name: "opening comment", opening: "--- # comment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "legacy-style", "SKILL.md"), tt.opening+"\nname: legacy-style\ndescription: Legacy style\n---\nFollow the legacy style.\n")

			catalog, err := NewCatalog(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Get(context.Background(), "legacy-style"); err != nil {
				t.Fatalf("get legacy skill: %v", err)
			}
			snapshot, err := catalog.Snapshot(context.Background(), "legacy-style")
			if err != nil {
				t.Fatalf("snapshot legacy skill: %v", err)
			}
			if snapshot.OutputArtifact != "" {
				t.Fatalf("unexpected output artifact: %q", snapshot.OutputArtifact)
			}
		})
	}
}

func TestCatalogListsAndLoadsStandardSkills(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "z-style", "SKILL.md"), `---
name: z-style
description: Z style
---
Follow Z.
`)
	writeTestFile(t, filepath.Join(root, "a-style", "SKILL.md"), `---
name: a-style
description: A style
---
Follow A.
`)
	writeTestFile(t, filepath.Join(root, "nested", "child", "SKILL.md"), `---
name: ignored
description: nested
---
Ignored.
`)

	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a-style" || got[1].Name != "z-style" {
		t.Fatalf("unexpected skills: %#v", got)
	}

	loaded, err := catalog.Get(context.Background(), "a-style")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != "Follow A." {
		t.Fatalf("unexpected content: %q", loaded.Content)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BaseDirectory != filepath.Join(realRoot, "a-style") {
		t.Fatalf("unexpected base directory: %q", loaded.BaseDirectory)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "a-style")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BaseDirectory != "a-style" {
		t.Fatalf("snapshot directory must be portable, got %q", snapshot.BaseDirectory)
	}
}

func TestCatalogRejectsMalformedSkill(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "broken", "SKILL.md"), "no frontmatter")
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.List(context.Background()); err == nil {
		t.Fatal("expected malformed skill error")
	}
}

func TestReadOnlyBackendRejectsEscapesAndWrites(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	writeTestFile(t, filepath.Join(root, "safe", "reference.md"), "inside")
	writeTestFile(t, filepath.Join(parent, "secret.txt"), "outside")

	backend, err := NewReadOnlyBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Read(context.Background(), &filesystem.ReadRequest{FilePath: "safe/reference.md"}); err != nil {
		t.Fatalf("read inside root: %v", err)
	}
	if _, err := backend.Read(context.Background(), &filesystem.ReadRequest{FilePath: "../secret.txt"}); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := backend.Write(context.Background(), &filesystem.WriteRequest{FilePath: "safe/new.md", Content: "x"}); err == nil {
		t.Fatal("expected write rejection")
	}
}

func TestReadOnlyBackendReadReturnsRawContentWithoutLineNumbers(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "reference.md"), "first\nsecond\nthird")

	backend, err := NewReadOnlyBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := backend.Read(context.Background(), &filesystem.ReadRequest{
		FilePath: "reference.md",
		Offset:   2,
		Limit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if content.Content != "second\nthird" {
		t.Fatalf("Read must return raw content for Eino to number once, got %q", content.Content)
	}
}

func TestReadOnlyBackendRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	writeTestFile(t, filepath.Join(parent, "secret.txt"), "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}

	backend, err := NewReadOnlyBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Read(context.Background(), &filesystem.ReadRequest{FilePath: "escape.txt"}); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
