package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameMarshal_ThreadStart(t *testing.T) {
	f := Frame{Type: FrameThreadStart, ThreadID: "thr_1"}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"type":"thread_start"`) || !strings.Contains(got, `"thread_id":"thr_1"`) {
		t.Fatalf("unexpected json: %s", got)
	}
}

func TestFrameMarshal_TextDelta(t *testing.T) {
	f := Frame{
		Type:     FrameContentBlockDelta,
		ThreadID: "thr_1",
		ActionID: "act_1",
		Index:    0,
		Delta:    &BlockDelta{Type: "text_delta", Text: "hello"},
	}
	b, _ := json.Marshal(f)
	want := `{"type":"content_block_delta","thread_id":"thr_1","action_id":"act_1","delta":{"type":"text_delta","text":"hello"}}`
	if string(b) != want {
		t.Fatalf("want %s, got %s", want, b)
	}
}

func TestFrameMarshal_ToolEventArtifacts(t *testing.T) {
	f := Frame{
		Type:     FrameContentBlockStart,
		ThreadID: "thr_1",
		ActionID: "act_1",
		Index:    2,
		Block: &ContentBlock{
			Type: "tool_event",
			Name: "echo_image",
			ID:   "toolu_x",
			Artifacts: []Artifact{{
				ArtifactType: "image_info", ArtifactID: "art_1",
				URL: "http://e/p/1x1.png", Name: "柴犬", Width: 1024, Height: 1024,
				Prompt: "A shiba inu",
			}},
		},
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"artifact_type":"image_info"`) {
		t.Fatalf("artifact_type missing: %s", b)
	}
}

func TestFrameMarshal_UserMessageInbound(t *testing.T) {
	raw := `{"type":"user_message","thread_id":"thr_1","action_id":"act_1","content":"画一只柴犬"}`
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if f.Type != FrameUserMessage || f.Content != "画一只柴犬" {
		t.Fatalf("parse mismatch: %+v", f)
	}
}

func TestFrameUnmarshal_UserMessageWithSkillID(t *testing.T) {
	raw := `{"type":"user_message","thread_id":"t","action_id":"a","content":"x","skill_id":"seedance_2_0_video"}`
	var f Frame
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if f.SkillID != "seedance_2_0_video" {
		t.Fatalf("got %q", f.SkillID)
	}
}
