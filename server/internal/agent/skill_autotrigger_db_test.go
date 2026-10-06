package agent

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestLoadedSkillIDs 钉住「已挂载技能」的口径：只认当前上下文里仍然在生效的
// skill block——被压缩（summarized_at 非 NULL）的那些不算。SQLite 内存库。
func TestLoadedSkillIDs(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	repo := NewRepository(conn)

	threadID := uuid.NewString()
	// SQLite 版 agent_threads.project_id 是外键（参考仓 MySQL 版传 0），这里建真项目。
	uid, pid := makeTestUserAndProject(t, conn)
	if err := repo.CreateThread(Thread{ID: threadID, ProjectID: pid, UserID: uid, Title: "t"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}

	blocks := func(bs ...ContentBlock) json.RawMessage {
		b, _ := json.Marshal(bs)
		return b
	}
	must := func(m Message) {
		t.Helper()
		if err := repo.AppendMessage(m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	must(Message{ID: uuid.NewString(), ThreadID: threadID, ActionID: "a1", Role: "user",
		ContentBlocks: blocks(ContentBlock{Type: "skill", ID: "writer", Name: "编剧"}, ContentBlock{Type: "text", Text: "写个剧本"})})
	must(Message{ID: uuid.NewString(), ThreadID: threadID, ActionID: "a2", Role: "user",
		ContentBlocks: blocks(ContentBlock{Type: "text", Text: "继续"})})
	must(Message{ID: uuid.NewString(), ThreadID: threadID, ActionID: "a3", Role: "user",
		ContentBlocks: blocks(ContentBlock{Type: "skill", ID: "director", Name: "导演"})})

	got, err := repo.LoadedSkillIDs(threadID)
	if err != nil {
		t.Fatalf("LoadedSkillIDs: %v", err)
	}
	if len(got) != 2 || !containsID(got, "writer") || !containsID(got, "director") {
		t.Fatalf("want [writer director], got %v", got)
	}

	// 别的 thread 的 skill 不该串进来。
	other := uuid.NewString()
	if err := repo.CreateThread(Thread{ID: other, ProjectID: pid, UserID: uid, Title: "o"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	got2, err := repo.LoadedSkillIDs(other)
	if err != nil {
		t.Fatalf("LoadedSkillIDs(other): %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("want empty for fresh thread, got %v", got2)
	}
}

func containsID(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
