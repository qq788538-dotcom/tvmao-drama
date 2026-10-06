package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrThreadNotFound = errors.New("agent thread not found")

type Thread struct {
	ID             string    `json:"id"`
	ParentThreadID string    `json:"parent_thread_id"`
	ProjectID      int64     `json:"project_id"`
	UserID         int64     `json:"user_id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	AgentName      string    `json:"agent_name"`
	Summary        string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Message struct {
	ID            string          `json:"id"`
	ThreadID      string          `json:"thread_id"`
	ActionID      string          `json:"action_id"`
	Role          string          `json:"role"` // 'user' | 'assistant' | 'tool'
	ContentBlocks json.RawMessage `json:"content_blocks"`
	StopReason    string          `json:"stop_reason"`
	CreatedAt     time.Time       `json:"created_at"`
	SummarizedAt  sql.NullTime    `json:"-"`
}

type Repository interface {
	CreateThread(t Thread) error
	GetThread(id string) (*Thread, error)
	ListThreadsByProject(projectID int64) ([]Thread, error)
	UpdateThreadStatus(id, status string) error
	UpdateThreadTitle(id, title string) error
	DeleteThread(id string) error
	UpdateThreadSummary(threadID, summary string) error
	AppendMessage(m Message) error
	// ListMessages 只返回 active（未被 summarize）的消息，按升序排列。
	ListMessages(threadID string) ([]Message, error)
	// ListMessagesPageBefore 是前端懒加载专用的开窗查询：返回 thread 内排序
	// （created_at ASC, id ASC）严格早于游标消息的最近 limit 条活跃消息，以及
	// 「是否还有更早的」。beforeID 传空串表示从最新往回取第一页。游标消息查不到
	//（已被压缩折叠 / 线程被删）不算错误——压缩是正常生命周期——返回空页 + false，
	// 让前端就地停止翻页。LLM 历史回放与压缩判断必须拿全量，走上面的 ListMessages。
	ListMessagesPageBefore(threadID, beforeID string, limit int) ([]Message, bool, error)
	// MarkMessagesSummarized 把给定的 message ID 标记为已折进 thread summary，
	// 让后续回放跳过它们。
	MarkMessagesSummarized(messageIDs []string) error
	// LoadedSkillIDs 返回该 thread 当前上下文里**仍在生效**的 skill id（供关键词
	// 自动触发去重）。口径刻意与 ListMessages 一致，带 summarized_at IS NULL：
	// 被压缩掉的那批消息里，skill 指令的全文快照已经不在喂给 planner 的历史里了，
	// 把它算作"已加载"会让方法论在压缩后悄悄失效且再也挂不回来。
	LoadedSkillIDs(threadID string) ([]string, error)
}

type sqliteRepo struct{ db *sql.DB }

func NewRepository(db *sql.DB) Repository { return &sqliteRepo{db: db} }

func (r *sqliteRepo) CreateThread(t Thread) error {
	if t.AgentName == "" {
		t.AgentName = "planner"
	}
	if t.Status == "" {
		t.Status = "active"
	}
	_, err := r.db.Exec(`INSERT INTO agent_threads
		(id, parent_thread_id, project_id, user_id, title, status, agent_name)
		VALUES (?,?,?,?,?,?,?)`,
		t.ID, t.ParentThreadID, t.ProjectID, t.UserID, t.Title, t.Status, t.AgentName)
	return err
}

func (r *sqliteRepo) GetThread(id string) (*Thread, error) {
	row := r.db.QueryRow(`SELECT id, parent_thread_id, project_id, user_id, title, status, agent_name, summary, created_at, updated_at
		FROM agent_threads WHERE id=?`, id)
	var t Thread
	var summary sql.NullString
	if err := row.Scan(&t.ID, &t.ParentThreadID, &t.ProjectID, &t.UserID, &t.Title, &t.Status, &t.AgentName, &summary, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrThreadNotFound
		}
		return nil, err
	}
	t.Summary = summary.String
	return &t, nil
}

func (r *sqliteRepo) ListThreadsByProject(projectID int64) ([]Thread, error) {
	rows, err := r.db.Query(`SELECT id, parent_thread_id, project_id, user_id, title, status, agent_name, summary, created_at, updated_at
		FROM agent_threads WHERE project_id=? ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Thread, 0)
	for rows.Next() {
		var t Thread
		var summary sql.NullString
		if err := rows.Scan(&t.ID, &t.ParentThreadID, &t.ProjectID, &t.UserID, &t.Title, &t.Status, &t.AgentName, &summary, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Summary = summary.String
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *sqliteRepo) UpdateThreadStatus(id, status string) error {
	_, err := r.db.Exec(`UPDATE agent_threads SET status=? WHERE id=?`, status, id)
	return err
}

func (r *sqliteRepo) UpdateThreadTitle(id, title string) error {
	_, err := r.db.Exec(`UPDATE agent_threads SET title=? WHERE id=?`, title, id)
	return err
}

func (r *sqliteRepo) DeleteThread(id string) error {
	_, err := r.db.Exec(`DELETE FROM agent_threads WHERE id=?`, id)
	return err
}

func (r *sqliteRepo) UpdateThreadSummary(threadID, summary string) error {
	_, err := r.db.Exec(`UPDATE agent_threads SET summary=? WHERE id=?`, summary, threadID)
	return err
}

func (r *sqliteRepo) AppendMessage(m Message) error {
	if len(m.ContentBlocks) == 0 {
		m.ContentBlocks = json.RawMessage(`[]`)
	}
	_, err := r.db.Exec(`INSERT INTO agent_messages
		(id, thread_id, action_id, role, content_blocks, stop_reason, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		m.ID, m.ThreadID, m.ActionID, m.Role, string(m.ContentBlocks), m.StopReason, agentMicroNow())
	return err
}

func (r *sqliteRepo) ListMessages(threadID string) ([]Message, error) {
	rows, err := r.db.Query(`SELECT id, thread_id, action_id, role, content_blocks, stop_reason, created_at, summarized_at
		FROM agent_messages WHERE thread_id=? AND summarized_at IS NULL ORDER BY created_at ASC, id ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		var blocks []byte
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.ActionID, &m.Role, &blocks, &m.StopReason, &m.CreatedAt, &m.SummarizedAt); err != nil {
			return nil, err
		}
		m.ContentBlocks = json.RawMessage(blocks)
		out = append(out, m)
	}
	return out, rows.Err()
}

// LoadedSkillIDs 只捞真正带 skill block 的那几行（json_each 过滤在 SQL 侧），
// 而不是把整个 thread 的消息拉回内存扫——buildHistory 随后还要再全量读一次，
// 这里再读一遍纯属浪费。绝大多数 thread 命中 0~2 行。
func (r *sqliteRepo) LoadedSkillIDs(threadID string) ([]string, error) {
	rows, err := r.db.Query(`SELECT content_blocks FROM agent_messages
		WHERE thread_id=? AND summarized_at IS NULL
		  AND EXISTS (SELECT 1 FROM json_each(agent_messages.content_blocks)
		              WHERE json_extract(json_each.value, '$.type') = 'skill')`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var blocks []byte
		if err := rows.Scan(&blocks); err != nil {
			return nil, err
		}
		out = append(out, skillIDsFromBlocks(blocks)...)
	}
	return out, rows.Err()
}

func (r *sqliteRepo) ListMessagesPageBefore(threadID, beforeID string, limit int) ([]Message, bool, error) {
	if limit <= 0 {
		return nil, false, errors.New("limit must be positive")
	}
	const cols = `SELECT id, thread_id, action_id, role, content_blocks, stop_reason, created_at, summarized_at
		FROM agent_messages`
	var (
		rows *sql.Rows
		err  error
	)
	if beforeID == "" {
		rows, err = r.db.Query(cols+` WHERE thread_id=? AND summarized_at IS NULL
			ORDER BY created_at DESC, id DESC LIMIT ?`, threadID, limit+1)
	} else {
		// 游标必须是本线程的活跃消息；查不到（已压缩 / 被删）按契约返回空页。
		var one int
		err = r.db.QueryRow(`SELECT 1 FROM agent_messages WHERE id=? AND thread_id=? AND summarized_at IS NULL`,
			beforeID, threadID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return []Message{}, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		// 行值比较直接比库内定宽微秒文本 + id，与排序键完全一致；不把时间扫进 Go 再绑回去
		// （modernc 会把 time.Time 写成带时区的另一种格式，比较就错了）。
		rows, err = r.db.Query(cols+` WHERE thread_id=? AND summarized_at IS NULL
			AND (created_at, id) < (SELECT created_at, id FROM agent_messages WHERE id=?)
			ORDER BY created_at DESC, id DESC LIMIT ?`,
			threadID, beforeID, limit+1)
	}
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	// 多取 1 条只为判断 has_more；DESC 取回后反转成 ASC，让响应与 ListMessages
	// 同序，前端拿到直接 prepend，不用自己反转。
	out := make([]Message, 0, limit+1)
	for rows.Next() {
		var m Message
		var blocks []byte
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.ActionID, &m.Role, &blocks, &m.StopReason, &m.CreatedAt, &m.SummarizedAt); err != nil {
			return nil, false, err
		}
		m.ContentBlocks = json.RawMessage(blocks)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, hasMore, nil
}

func (r *sqliteRepo) MarkMessagesSummarized(messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	// 用绑定参数构造一个 IN 子句，避免动态 ID 列表带来的 SQL 注入。
	placeholders := make([]byte, 0, len(messageIDs)*2)
	args := make([]any, 0, len(messageIDs))
	for i, id := range messageIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}
	q := "UPDATE agent_messages SET summarized_at=? WHERE id IN (" + string(placeholders) + ")"
	_, err := r.db.Exec(q, append([]any{agentMicroNow()}, args...)...)
	return err
}
