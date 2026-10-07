package cliapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// 写请求幂等。
//
// 为什么必须有：CLI 会在网络失败后自动重试携带 Idempotency-Key 的写请求。
// 没有它，「创建生成任务」重试一次就是再建一个节点、再跑一次上游——
// 同一条命令生效两次。所以规则是**先解决幂等，才允许 CLI 自动重试写请求**。

const (
	IdempotencyInProgress = "in_progress"
	IdempotencyDone       = "done"
)

type IdempotencyRecord struct {
	Status      string
	StatusCode  int
	Body        []byte
	RequestHash string
}

type IdempotencyStore interface {
	// Begin 尝试占位。created=true 表示本次是首次请求（已写入 in_progress）；
	// false 表示已存在记录，调用方据 rec.Status 决定回放还是报冲突。
	Begin(userID int64, scope, key, hash string) (*IdempotencyRecord, bool, error)
	// Finish 把记录置为 done 并保存首次响应，供后续重放。
	Finish(userID int64, scope, key string, statusCode int, body []byte) error
	// Abort 删除 in_progress 记录。业务失败时必须调用，否则这个 key 会被永久
	// 占住，用户重试只会一直撞 IDEMPOTENCY_IN_PROGRESS。
	Abort(userID int64, scope, key string) error
}

// HashRequest 计算请求体的规范化摘要。
// Go 的 map 序列化按 key 排序，天然 canonical——idempotency_test 里有测试守住这个假设。
func HashRequest(v any) string {
	blob, err := json.Marshal(v)
	if err != nil {
		blob = []byte("<unmarshalable>")
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// RunIdempotent 执行一次可幂等的写操作。
//
// key 为空 = 不去重，fn 每次都跑（读命令与不带 key 的写走这条路）。
// 返回 (statusCode, responseBodyJSON, error)。
func RunIdempotent(
	store IdempotencyStore,
	userID int64,
	scope, key string,
	request any,
	fn func() (int, any, error),
) (int, []byte, error) {
	exec := func() (int, []byte, error) {
		status, payload, err := fn()
		if err != nil {
			return 0, nil, err
		}
		body, mErr := json.Marshal(payload)
		if mErr != nil {
			return 0, nil, New(CodeInternal, "序列化响应失败").WithCause(mErr)
		}
		return status, body, nil
	}

	if store == nil || key == "" {
		return exec()
	}

	hash := HashRequest(request)
	rec, created, err := store.Begin(userID, scope, key, hash)
	if err != nil {
		return 0, nil, New(CodeInternal, "幂等记录写入失败").WithCause(err)
	}
	if !created {
		// 同 key 不同请求：调用方把一个 key 用在了两件事上，必须报错而不是
		// 回放上一件事的响应——那样用户会以为第二个操作成功了。
		if rec.RequestHash != hash {
			return 0, nil, New(CodeIdempotencyKeyReused,
				"该 Idempotency-Key 已用于另一个请求，请换一个 key")
		}
		if rec.Status == IdempotencyInProgress {
			return 0, nil, New(CodeIdempotencyInProgress, "同一请求正在处理中，请稍后重试")
		}
		return rec.StatusCode, rec.Body, nil
	}

	status, body, err := exec()
	if err != nil {
		// 失败即释放占位，让重试能真正重跑。
		_ = store.Abort(userID, scope, key)
		return 0, nil, err
	}
	if fErr := store.Finish(userID, scope, key, status, body); fErr != nil {
		// 业务已经做完了，记录落库失败只影响后续重放的去重能力，
		// 不能因此把成功的操作报成失败。
		return status, body, nil
	}
	return status, body, nil
}

// SQLiteIdempotency 是 IdempotencyStore 的 SQLite 实现。
type SQLiteIdempotency struct{ db *sql.DB }

func NewSQLiteIdempotency(db *sql.DB) *SQLiteIdempotency { return &SQLiteIdempotency{db: db} }

func (s *SQLiteIdempotency) Begin(userID int64, scope, key, hash string) (*IdempotencyRecord, bool, error) {
	// 唯一键 + ON CONFLICT DO NOTHING：并发下只有一个请求能建成，其余读到已有记录。
	// 不用 INSERT OR IGNORE，避免吞掉唯一键以外的真实错误。
	res, err := s.db.Exec(`
		INSERT INTO cli_idempotency_keys (user_id, scope, idem_key, request_hash, status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, scope, idem_key) DO NOTHING`,
		userID, scope, key, hash, IdempotencyInProgress)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return &IdempotencyRecord{Status: IdempotencyInProgress, RequestHash: hash}, true, nil
	}

	var rec IdempotencyRecord
	var body sql.NullString
	err = s.db.QueryRow(`
		SELECT status, status_code, response_body, request_hash
		  FROM cli_idempotency_keys
		 WHERE user_id = ? AND scope = ? AND idem_key = ?`,
		userID, scope, key).Scan(&rec.Status, &rec.StatusCode, &body, &rec.RequestHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 记录刚被 Abort 删掉：当作首次请求重来一遍，比报错更符合用户预期。
			return s.Begin(userID, scope, key, hash)
		}
		return nil, false, err
	}
	if body.Valid {
		rec.Body = []byte(body.String)
	}
	return &rec, false, nil
}

func (s *SQLiteIdempotency) Finish(userID int64, scope, key string, statusCode int, body []byte) error {
	_, err := s.db.Exec(`
		UPDATE cli_idempotency_keys
		   SET status = ?, status_code = ?, response_body = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE user_id = ? AND scope = ? AND idem_key = ?`,
		IdempotencyDone, statusCode, string(body), userID, scope, key)
	return err
}

func (s *SQLiteIdempotency) Abort(userID int64, scope, key string) error {
	_, err := s.db.Exec(`
		DELETE FROM cli_idempotency_keys
		 WHERE user_id = ? AND scope = ? AND idem_key = ? AND status = ?`,
		userID, scope, key, IdempotencyInProgress)
	return err
}
