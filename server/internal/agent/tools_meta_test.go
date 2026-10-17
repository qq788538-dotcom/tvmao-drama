package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOutputSkillDefinitionTool_EmitsDraft(t *testing.T) {
	tool := &OutputSkillDefinitionTool{}
	args := map[string]string{
		"id":                  "shiba_winter",
		"name":                "Shiba 雪地",
		"description":         "winter redraw skill",
		"category":            "Image",
		"system_prompt_addon": "always winter scene",
		"force_tools":         `["generate_image_x"]`,
		"param_defaults":      `{"aspect_ratio":"9:16"}`,
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	var arts []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { arts = append(arts, a) })

	res, err := tool.InvokableRun(ctx, string(argsJSON))
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact, got %d", len(arts))
	}
	a := arts[0]
	if a.ArtifactType != "skill_definition" {
		t.Fatalf("artifact_type: %q", a.ArtifactType)
	}
	if a.Skill == nil {
		t.Fatal("Skill must be non-nil")
	}
	if a.Skill.ID != "shiba_winter" {
		t.Fatalf("Skill.ID: %q", a.Skill.ID)
	}
	if a.Skill.Name != "Shiba 雪地" {
		t.Fatalf("Skill.Name: %q", a.Skill.Name)
	}
	if a.Skill.Description != "winter redraw skill" {
		t.Fatalf("Skill.Description: %q", a.Skill.Description)
	}
	if a.Skill.Category != "Image" {
		t.Fatalf("Skill.Category: %q", a.Skill.Category)
	}
	if a.Skill.SystemPromptAddon != "always winter scene" {
		t.Fatalf("Skill.SystemPromptAddon: %q", a.Skill.SystemPromptAddon)
	}
	if len(a.Skill.ForceTools) != 1 || a.Skill.ForceTools[0] != "generate_image_x" {
		t.Fatalf("Skill.ForceTools: %v", a.Skill.ForceTools)
	}
	if a.Skill.ParamDefaults["aspect_ratio"] != "9:16" {
		t.Fatalf("Skill.ParamDefaults[aspect_ratio]: %v", a.Skill.ParamDefaults["aspect_ratio"])
	}
	if !strings.Contains(res, "Shiba 雪地") {
		t.Fatalf("result should mention skill name: %q", res)
	}
}

func TestOutputSkillDefinitionTool_RequiresIDAndName(t *testing.T) {
	tool := &OutputSkillDefinitionTool{}
	ctx := context.Background()

	if _, err := tool.InvokableRun(ctx, `{"name":"x"}`); err == nil {
		t.Fatal("expected error when id is missing")
	}
	if _, err := tool.InvokableRun(ctx, `{"id":"x"}`); err == nil {
		t.Fatal("expected error when name is missing")
	}
}

func TestOutputSkillDefinitionTool_OptionalJSONFieldsCanBeEmpty(t *testing.T) {
	tool := &OutputSkillDefinitionTool{}
	args := map[string]string{
		"id":   "no_extras",
		"name": "no extras",
	}
	argsJSON, _ := json.Marshal(args)

	var arts []Artifact
	ctx := WithEmit(context.Background(), func(a Artifact) { arts = append(arts, a) })

	if _, err := tool.InvokableRun(ctx, string(argsJSON)); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact, got %d", len(arts))
	}
	if arts[0].Skill.ForceTools != nil {
		t.Fatalf("ForceTools should be nil, got %v", arts[0].Skill.ForceTools)
	}
	if arts[0].Skill.ParamDefaults != nil {
		t.Fatalf("ParamDefaults should be nil, got %v", arts[0].Skill.ParamDefaults)
	}
}
