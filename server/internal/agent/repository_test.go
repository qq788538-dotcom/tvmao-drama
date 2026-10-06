package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makeTestUserAndProject 插入一个 user + project 来满足外键；返回 id。
func makeTestUserAndProject(t *testing.T, db *sql.DB) (userID, projectID int64) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (email, password_hash, role) VALUES (?, '', 'user')`,
		"agenttest+"+uuid.NewString()+"@x.com")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ = res.LastInsertId()
	res, err = db.Exec(`INSERT INTO projects (user_id, name) VALUES (?, ?)`, userID, "agent test")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ = res.LastInsertId()
	return userID, projectID
}

func TestThreadAndMessageRoundTrip(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	if _, err := conn.Exec(`DELETE FROM agent_threads`); err != nil {
		t.Fatal(err)
	}

	userID, projectID := makeTestUserAndProject(t, conn)
	repo := NewRepository(conn)

	threadID := uuid.NewString()
	if err := repo.CreateThread(Thread{ID: threadID, ProjectID: projectID, UserID: userID, Title: "测试"}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	got, err := repo.GetThread(threadID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got.Title != "测试" || got.AgentName != "planner" {
		t.Fatalf("thread mismatch: %+v", got)
	}

	actionID := uuid.NewString()
	userMsg := Message{
		ID:            uuid.NewString(),
		ThreadID:      threadID,
		ActionID:      actionID,
		Role:          "user",
		ContentBlocks: mustJSON([]map[string]any{{"type": "text", "text": "画一只柴犬"}}),
	}
	if err := repo.AppendMessage(userMsg); err != nil {
		t.Fatalf("Append user: %v", err)
	}

	asstMsg := Message{
		ID:            uuid.NewString(),
		ThreadID:      threadID,
		ActionID:      actionID,
		Role:          "assistant",
		ContentBlocks: mustJSON([]map[string]any{{"type": "text", "text": "好的"}}),
		StopReason:    "end_turn",
	}
	if err := repo.AppendMessage(asstMsg); err != nil {
		t.Fatalf("Append assistant: %v", err)
	}

	msgs, err := repo.ListMessages(threadID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("order wrong: %v", msgs)
	}
}

// TestListMessagesPageBefore 钉死懒加载开窗的四条硬性质：
//  1. 逐页翻到底后拼接结果与 ListMessages 全量逐条一致（无重无漏，含微秒同刻
//     的 tie 组——keyset 双键与排序键一致才成立，这是选双键而非纯时间戳的原因）；
//  2. summarized 行永远不出现；
//  3. has_more 在最后一页翻假；
//  4. 游标失效（不存在/已折叠）返回空页而非报错。
func TestListMessagesPageBefore(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	if _, err := conn.Exec(`DELETE FROM agent_threads`); err != nil {
		t.Fatal(err)
	}

	userID, projectID := makeTestUserAndProject(t, conn)
	repo := NewRepository(conn)
	threadID := uuid.NewString()
	if err := repo.CreateThread(Thread{ID: threadID, ProjectID: projectID, UserID: userID}); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	// 75 条消息，created_at 显式指定：大多数逐秒递增，其中 m040~m044 五条共享
	// 同一微秒时间戳（模拟一轮工具往返同刻落多行），靠零填充 id 的字典序 tie-break。
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 75; i++ {
		id := fmt.Sprintf("m%03d", i)
		sec := i
		if i >= 40 && i <= 44 {
			sec = 40 // tie 组
		}
		if _, err := conn.Exec(`INSERT INTO agent_messages
			(id, thread_id, action_id, role, content_blocks, created_at)
			VALUES (?,?,?,?,?,?)`,
			id, threadID, "a", "user", `[]`, base.Add(time.Duration(sec)*time.Second).Format(agentMicroLayout)); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	// m001~m005 已折叠进 summary：任何页都不该出现。
	if err := repo.MarkMessagesSummarized([]string{"m001", "m002", "m003", "m004", "m005"}); err != nil {
		t.Fatalf("mark summarized: %v", err)
	}

	full, err := repo.ListMessages(threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(full) != 70 {
		t.Fatalf("want 70 active, got %d", len(full))
	}

	// 从最新往回逐页翻到底，拼回完整列表。
	var paged []Message
	before := ""
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not terminate")
		}
		msgs, hasMore, err := repo.ListMessagesPageBefore(threadID, before, 30)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(msgs) == 0 {
			t.Fatalf("page %d unexpectedly empty", page)
		}
		paged = append(msgs, paged...)
		if !hasMore {
			break
		}
		before = msgs[0].ID
	}
	if len(paged) != len(full) {
		t.Fatalf("paged %d != full %d", len(paged), len(full))
	}
	for i := range full {
		if paged[i].ID != full[i].ID {
			t.Fatalf("order mismatch at %d: paged=%s full=%s", i, paged[i].ID, full[i].ID)
		}
		if paged[i].SummarizedAt.Valid {
			t.Fatalf("summarized message leaked: %s", paged[i].ID)
		}
	}

	// limit 大于活跃总数：一页全给，has_more=false。
	all, hasMore, err := repo.ListMessagesPageBefore(threadID, "", 200)
	if err != nil || hasMore || len(all) != 70 {
		t.Fatalf("oversize page: len=%d hasMore=%v err=%v", len(all), hasMore, err)
	}

	// 游标失效：不存在的 id 与已折叠的 id 都返回空页 + false，不报错。
	for _, cur := range []string{"nope", "m003"} {
		msgs, hasMore, err := repo.ListMessagesPageBefore(threadID, cur, 30)
		if err != nil || hasMore || len(msgs) != 0 {
			t.Fatalf("stale cursor %q: len=%d hasMore=%v err=%v", cur, len(msgs), hasMore, err)
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestAppendMessageStoresMicrosecondTextAndJSONText(t *testing.T) {
	conn := openTestDB(t)
	repo := NewRepository(conn)
	uid, pid := makeTestUserAndProject(t, conn)
	if err := repo.CreateThread(Thread{ID: "thr_m", ProjectID: pid, UserID: uid}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendMessage(Message{ID: "m1", ThreadID: "thr_m", ActionID: "a", Role: "user",
		ContentBlocks: json.RawMessage(`[{"type":"text","text":"hi"}]`)}); err != nil {
		t.Fatal(err)
	}
	var created, typ string
	if err := conn.QueryRow(`SELECT CAST(created_at AS TEXT), typeof(content_blocks) FROM agent_messages WHERE id='m1'`).Scan(&created, &typ); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{6}$`).MatchString(created) {
		t.Fatalf("created_at 应为微秒文本: %q", created)
	}
	if typ != "text" {
		t.Fatalf("content_blocks 应存为 text，实际 %s", typ)
	}
	msgs, err := repo.ListMessages("thr_m")
	if err != nil || len(msgs) != 1 || msgs[0].CreatedAt.Nanosecond()%1000 != 0 || msgs[0].CreatedAt.IsZero() {
		t.Fatalf("读回失败: %v %+v", err, msgs)
	}
}

// 连续追加的消息（同一微秒内也可能）必须按写入顺序读回。
func TestListMessagesOrderStableForRapidAppends(t *testing.T) {
	conn := openTestDB(t)
	repo := NewRepository(conn)
	uid, pid := makeTestUserAndProject(t, conn)
	_ = repo.CreateThread(Thread{ID: "thr_r", ProjectID: pid, UserID: uid})
	var want []string
	for i := 0; i < 50; i++ {
		id := uuid.NewString()
		want = append(want, id)
		if err := repo.AppendMessage(Message{ID: id, ThreadID: "thr_r", ActionID: "a", Role: "user"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ListMessages("thr_r")
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("第 %d 条乱序: got %s want %s", i, got[i].ID, want[i])
		}
	}
}

// 同一微秒的多条消息靠 id 作第二键，翻页不重复也不漏。
func TestListMessagesPageBeforeSameMicrosecond(t *testing.T) {
	conn := openTestDB(t)
	repo := NewRepository(conn)
	uid, pid := makeTestUserAndProject(t, conn)
	_ = repo.CreateThread(Thread{ID: "thr_k", ProjectID: pid, UserID: uid})
	for _, id := range []string{"a", "b", "c"} {
		if _, err := conn.Exec(`INSERT INTO agent_messages (id, thread_id, action_id, role, content_blocks, created_at)
			VALUES (?, 'thr_k', 'x', 'user', '[]', '2026-10-06 10:00:00.000001')`, id); err != nil {
			t.Fatal(err)
		}
	}
	page, more, err := repo.ListMessagesPageBefore("thr_k", "c", 1)
	if err != nil || len(page) != 1 || page[0].ID != "b" || !more {
		t.Fatalf("before c: %v %+v more=%v", err, page, more)
	}
	page, more, err = repo.ListMessagesPageBefore("thr_k", "b", 5)
	if err != nil || len(page) != 1 || page[0].ID != "a" || more {
		t.Fatalf("before b: %v %+v more=%v", err, page, more)
	}
	page, more, err = repo.ListMessagesPageBefore("thr_k", "missing", 5)
	if err != nil || len(page) != 0 || more {
		t.Fatalf("游标不存在应返回空页: %v %+v", err, page)
	}
}

func TestLoadedSkillIDsAndSummarized(t *testing.T) {
	conn := openTestDB(t)
	repo := NewRepository(conn)
	uid, pid := makeTestUserAndProject(t, conn)
	_ = repo.CreateThread(Thread{ID: "thr_s", ProjectID: pid, UserID: uid})
	_ = repo.AppendMessage(Message{ID: "s1", ThreadID: "thr_s", ActionID: "a", Role: "user",
		ContentBlocks: json.RawMessage(`[{"type":"skill","id":"director_master"},{"type":"text","text":"x"}]`)})
	_ = repo.AppendMessage(Message{ID: "s2", ThreadID: "thr_s", ActionID: "a", Role: "assistant",
		ContentBlocks: json.RawMessage(`[{"type":"text","text":"skill"}]`)})
	ids, err := repo.LoadedSkillIDs("thr_s")
	if err != nil || len(ids) != 1 || ids[0] != "director_master" {
		t.Fatalf("LoadedSkillIDs: %v %v", err, ids)
	}
	if err := repo.MarkMessagesSummarized([]string{"s1"}); err != nil {
		t.Fatal(err)
	}
	ids, _ = repo.LoadedSkillIDs("thr_s")
	if len(ids) != 0 {
		t.Fatalf("压缩后的 skill 不应再算已加载: %v", ids)
	}
	var s string
	_ = conn.QueryRow(`SELECT CAST(summarized_at AS TEXT) FROM agent_messages WHERE id='s1'`).Scan(&s)
	if len(s) != 26 {
		t.Fatalf("summarized_at 应为微秒文本: %q", s)
	}
}
