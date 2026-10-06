package skillruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestTrustedShellRunsFromSkillDirectoryAndReturnsExitCode(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "scripts", "result.py"), `from pathlib import Path
print(Path("input.txt").read_text().strip())
`)
	writeTestFile(t, filepath.Join(root, "input.txt"), "python-ok\n")
	shell, err := NewTrustedShell(root)
	if err != nil {
		t.Fatal(err)
	}

	result, err := shell.Execute(context.Background(), &filesystem.ExecuteRequest{
		Command: "python3 scripts/result.py; printf 'cwd=%s skill=%s work=%s tmp=%s' \"$PWD\" \"$SKILL_DIR\" \"$WORK_DIR\" \"$TMPDIR\"; exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "python-ok") ||
		!strings.Contains(result.Output, "cwd="+realRoot) ||
		!strings.Contains(result.Output, "skill="+realRoot) ||
		!strings.Contains(result.Output, "work="+realRoot) ||
		!strings.Contains(result.Output, "tmp="+filepath.Join(realRoot, ".tmp")) {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	tmpInfo, err := os.Stat(filepath.Join(realRoot, ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if !tmpInfo.IsDir() || tmpInfo.Mode().Perm() != 0o700 {
		t.Fatalf("unexpected TMPDIR mode: %v", tmpInfo.Mode())
	}
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("unexpected exit code: %#v", result.ExitCode)
	}
}


type shellModelState struct {
	sawExecuteTool  bool
	sawScriptOutput bool
	sawWriteFile    bool
}

type shellScriptModel struct {
	state *shellModelState
	tools []*schema.ToolInfo
}

func (m *shellScriptModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	clone := *m
	clone.tools = tools
	for _, item := range tools {
		if item.Name == "execute" {
			m.state.sawExecuteTool = true
		}
	}
	return &clone, nil
}

func (m *shellScriptModel) Generate(_ context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	for _, item := range options.Tools {
		if item.Name == "execute" {
			m.state.sawExecuteTool = true
		}
	}
	hasSkill := false
	hasWriteFile := false
	hasExecute := false
	for _, msg := range input {
		if msg.Role == schema.Tool && msg.ToolName == "skill" {
			hasSkill = true
		}
		if msg.Role == schema.Tool && msg.ToolName == "execute" {
			hasExecute = true
			m.state.sawScriptOutput = strings.Contains(msg.Content, "candidate-ok")
		}
		if msg.Role == schema.Tool && msg.ToolName == "write_file" {
			hasWriteFile = true
			m.state.sawWriteFile = true
		}
	}
	if !hasSkill {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "skill-1", Type: "function",
			Function: schema.FunctionCall{Name: "skill", Arguments: `{"skill":"script-style"}`},
		}}), nil
	}
	if !hasWriteFile {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "write-1", Type: "function",
			Function: schema.FunctionCall{Name: "write_file", Arguments: `{"file_path":"candidate.json","content":"candidate-ok"}`},
		}}), nil
	}
	if !hasExecute {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "execute-1", Type: "function",
			Function: schema.FunctionCall{Name: "execute", Arguments: `{"command":"python3 scripts/generate.py"}`},
		}}), nil
	}
	return schema.AssistantMessage("final from script", nil), nil
}

func (m *shellScriptModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unexpected streaming call")
}

var _ model.ToolCallingChatModel = (*shellScriptModel)(nil)

func TestRuntimeExposesExecuteToolForSkillScripts(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "script-style", "SKILL.md"), `---
name: script-style
description: Script style
---
Run scripts/generate.py and use its result.
`)
	writeTestFile(t, filepath.Join(root, "script-style", "scripts", "generate.py"), `from pathlib import Path
print(Path("candidate.json").read_text())
`)
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot(context.Background(), "script-style")
	if err != nil {
		t.Fatal(err)
	}
	state := &shellModelState{}
	workspace, err := NewRunWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(catalog, workspace, func(context.Context, ModelConfig) (model.ToolCallingChatModel, error) {
		return &shellScriptModel{state: state}, nil
	}, 10, TrustedShellFactory)

	result, err := runtime.Run(context.Background(), RunRequest{
		Skill: snapshot, Model: ModelConfig{APIKey: "sk", Model: "test"}, Input: "run it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final from script" || !state.sawExecuteTool || !state.sawWriteFile || !state.sawScriptOutput {
		t.Fatalf("script execution was not connected: result=%#v state=%#v", result, state)
	}
	if _, err := os.Stat(filepath.Join(root, "script-style", "candidate.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original Skill was modified: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(workspace.Root(), "*", "run-*", "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one retained candidate, got %#v", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "candidate-ok" {
		t.Fatalf("unexpected retained candidate: %q", content)
	}
}
