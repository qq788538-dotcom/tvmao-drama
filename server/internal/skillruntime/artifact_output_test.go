package skillruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestArtifactSubmitterSubmitsExactFileContent(t *testing.T) {
	runDir := t.TempDir()
	want := " {\n  \"title\": \"原样保留\",\n  \"shots\": []\n}\n"
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	submitter, err := newArtifactSubmitter(runDir, "candidate.json")
	if err != nil {
		t.Fatalf("newArtifactSubmitter: %v", err)
	}
	defer submitter.Close()
	tool := submitter.Tool()
	got, err := tool.InvokableRun(context.Background(), `{"path":"candidate.json"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if got != "artifact submitted" {
		t.Fatalf("InvokableRun result = %q, want %q", got, "artifact submitted")
	}
	content, ok := submitter.SubmittedContent()
	if !ok {
		t.Fatal("SubmittedContent is unset after successful submission")
	}
	if content != want {
		t.Fatalf("SubmittedContent = %q, want exact bytes %q", content, want)
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != submitArtifactToolName {
		t.Fatalf("tool name = %q, want %q", info.Name, submitArtifactToolName)
	}
	if info.Desc == "" || info.ParamsOneOf == nil {
		t.Fatalf("tool info is incomplete: %#v", info)
	}
	paramsSchema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("convert tool params to JSON schema: %v", err)
	}
	if paramsSchema.Type != "object" || !reflect.DeepEqual(paramsSchema.Required, []string{"path"}) {
		t.Fatalf("params schema type/required = %q/%v, want object/[path]", paramsSchema.Type, paramsSchema.Required)
	}
	pathSchema, ok := paramsSchema.Properties.Get("path")
	if !ok {
		t.Fatal("params schema is missing path")
	}
	if pathSchema.Type != "string" || !reflect.DeepEqual(pathSchema.Enum, []any{"candidate.json"}) {
		t.Fatalf("path schema type/enum = %q/%v, want string/[candidate.json]", pathSchema.Type, pathSchema.Enum)
	}
}

func TestArtifactSubmitterFailureDoesNotDestroySuccessfulSubmission(t *testing.T) {
	runDir := t.TempDir()
	want := `{"first":true}`
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	submitter, err := newArtifactSubmitter(runDir, "candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer submitter.Close()
	if _, err := submitter.Tool().InvokableRun(context.Background(), `{"path":"candidate.json"}`); err != nil {
		t.Fatalf("initial submission: %v", err)
	}

	for _, args := range []string{
		`{"path":`,
		`{"path":"candidate.json","unknown":true}`,
		`{"path":"candidate.json"} {"path":"candidate.json"}`,
		`{"path":"other.json"}`,
	} {
		if _, err := submitter.Tool().InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("InvokableRun(%q) succeeded, want rejection", args)
		}
		assertSubmittedContent(t, submitter, want)
	}
}

func TestArtifactSubmitterRepeatedSuccessIsIdempotent(t *testing.T) {
	runDir := t.TempDir()
	path := filepath.Join(runDir, "candidate.json")
	want := `{"version":1}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	submitter, err := newArtifactSubmitter(runDir, "candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer submitter.Close()
	tool := submitter.Tool()
	if _, err := tool.InvokableRun(context.Background(), `{"path":"candidate.json"}`); err != nil {
		t.Fatalf("initial submission: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tool.InvokableRun(context.Background(), `{"path":"candidate.json"}`)
	if err != nil {
		t.Fatalf("idempotent submission: %v", err)
	}
	if got != "artifact submitted" {
		t.Fatalf("idempotent result = %q", got)
	}
	assertSubmittedContent(t, submitter, want)
}

func TestArtifactSubmitterConcurrentCallsPreserveFirstSuccess(t *testing.T) {
	runDir := t.TempDir()
	path := filepath.Join(runDir, "candidate.json")
	want := `{"version":1}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	submitter, err := newArtifactSubmitter(runDir, "candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer submitter.Close()
	if _, err := submitter.Tool().InvokableRun(context.Background(), `{"path":"candidate.json"}`); err != nil {
		t.Fatalf("initial submission: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	args := []string{
		`{"path":"candidate.json"}`,
		`{"path":"candidate.json"}`,
		`{"path":`,
		`{"path":"wrong.json"}`,
		`{"path":"candidate.json","unknown":true}`,
	}
	var wg sync.WaitGroup
	for _, value := range args {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = submitter.Tool().InvokableRun(context.Background(), value)
		}()
	}
	close(start)
	wg.Wait()
	assertSubmittedContent(t, submitter, want)
}

func TestArtifactSubmitterKeepsCapabilityWhenRootPathIsReplaced(t *testing.T) {
	parent := t.TempDir()
	runDir := filepath.Join(parent, "run")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := `{"original":true}`
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	submitter, err := newArtifactSubmitter(runDir, "candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer submitter.Close()

	movedDir := filepath.Join(parent, "moved")
	if err := os.Rename(runDir, movedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), []byte(`{"replacement":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := submitter.Tool().InvokableRun(context.Background(), `{"path":"candidate.json"}`); err != nil {
		t.Fatalf("submit through fixed root capability: %v", err)
	}
	assertSubmittedContent(t, submitter, want)
}

func TestArtifactSubmitterRejectsInvalidInvocationAndLeavesContentUnset(t *testing.T) {
	tests := []struct {
		name         string
		expectedPath string
		args         func(string) string
		prepare      func(t *testing.T, runDir string)
	}{
		{
			name: "malformed JSON",
			args: func(string) string { return `{"path":` },
		},
		{
			name: "empty path",
			args: func(string) string { return `{"path":""}` },
		},
		{
			name: "wrong declared path",
			args: func(string) string { return `{"path":"other.json"}` },
		},
		{
			name: "absolute path",
			args: func(runDir string) string {
				raw, err := json.Marshal(map[string]string{"path": filepath.Join(runDir, "candidate.json")})
				if err != nil {
					panic(err)
				}
				return string(raw)
			},
		},
		{
			name: "traversal",
			args: func(string) string { return `{"path":"../candidate.json"}` },
		},
		{
			name: "missing file",
			args: func(string) string { return `{"path":"candidate.json"}` },
		},
		{
			name: "directory",
			args: func(string) string { return `{"path":"candidate.json"}` },
			prepare: func(t *testing.T, runDir string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(runDir, "candidate.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink whose target is inside root",
			args: func(string) string { return `{"path":"candidate.json"}` },
			prepare: func(t *testing.T, runDir string) {
				t.Helper()
				realPath := filepath.Join(runDir, "real.json")
				if err := os.WriteFile(realPath, []byte(`{"ok":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realPath, filepath.Join(runDir, "candidate.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty file",
			args: func(string) string { return `{"path":"candidate.json"}` },
			prepare: func(t *testing.T, runDir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(runDir, "candidate.json"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "file over size limit",
			args: func(string) string { return `{"path":"candidate.json"}` },
			prepare: func(t *testing.T, runDir string) {
				t.Helper()
				path := filepath.Join(runDir, "candidate.json")
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(maxArtifactBytes + 1); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:         "parent symlink escapes root",
			expectedPath: "nested/candidate.json",
			args:         func(string) string { return `{"path":"nested/candidate.json"}` },
			prepare: func(t *testing.T, runDir string) {
				t.Helper()
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "candidate.json"), []byte(`{"outside":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(runDir, "nested")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			expectedPath := tt.expectedPath
			if expectedPath == "" {
				expectedPath = "candidate.json"
			}
			if tt.prepare != nil {
				tt.prepare(t, runDir)
			}
			submitter, err := newArtifactSubmitter(runDir, expectedPath)
			if err != nil {
				t.Fatalf("newArtifactSubmitter: %v", err)
			}
			defer submitter.Close()

			if _, err := submitter.Tool().InvokableRun(context.Background(), tt.args(runDir)); err == nil {
				t.Fatal("InvokableRun succeeded, want rejection")
			}
			if content, ok := submitter.SubmittedContent(); ok {
				t.Fatalf("SubmittedContent = %q, true after rejected submission", content)
			}
		})
	}
}

func assertSubmittedContent(t *testing.T, submitter *artifactSubmitter, want string) {
	t.Helper()
	content, ok := submitter.SubmittedContent()
	if !ok || content != want {
		t.Fatalf("SubmittedContent = %q, %t; want %q, true", content, ok, want)
	}
}

func TestArtifactSubmitterConstructorRejectsUnsafeExpectedPath(t *testing.T) {
	runDir := t.TempDir()
	for _, path := range []string{"", " ", ".", "..", "../candidate.json", filepath.Join(runDir, "candidate.json")} {
		t.Run(strings.ReplaceAll(path, string(filepath.Separator), "_"), func(t *testing.T) {
			if _, err := newArtifactSubmitter(runDir, path); err == nil {
				t.Fatalf("newArtifactSubmitter(%q) succeeded, want rejection", path)
			}
		})
	}
}
