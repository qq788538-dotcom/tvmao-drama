package projects

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Project struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"userId"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	StorageBytes int64     `json:"storageBytes"`
	// CoverURL 是项目封面，两层来源取第一个非空：用户手动指定的 projects.cover_url，
	// 否则回退「该项目最早一次成功图片生成的产物」。repo 层存 canonical 的
	// media://key 形态；签名是 HTTP 边界的事（handler 用 MediaSigner 出短时缩略
	// URL），这里绝不能存签名 URL——会随 24h TTL 失效。
	CoverURL string `json:"coverUrl,omitempty"`
}

var ErrNotFound = errors.New("project not found")

// Snapshot 是 workflow_snapshots 当前最新的一行状态。
type Snapshot struct {
	Payload  json.RawMessage
	SyncedAt time.Time
	Version  int64
}

type Repository interface {
	Create(userID int64, name, description string) (int64, error)
	// ListForUser：limit<=0 = 拉全量（CLI 走这条，语义与加分页前一致）；
	// total 恒为该用户项目总数，与分页无关。
	ListForUser(userID int64, limit, offset int) ([]Project, int, error)
	GetForUser(userID, projectID int64) (*Project, error)
	Update(userID, projectID int64, name, description string) error
	// Touch 把 updated_at 刷成当前时间（打开项目时调用，让「所有项目」里最近打开的排第一）。
	Touch(userID, projectID int64) error
	// SetCover 手动指定封面（canonical media://key 形态）；传空串=清除手动指定，
	// 封面回落到自动取图。
	SetCover(userID, projectID int64, coverURL string) error
	Delete(userID, projectID int64) error // 软删：打 deleted_at，立即从列表隐藏
	SaveAuthoritative(projectID int64, payload json.RawMessage) (int64, error)
	// MutateSnapshot 事务内读-改-写画布快照(BEGIN IMMEDIATE + version+1),给后端权威回写用
	// (如 canvasgen 出图写回节点)。fn 收到当前 payload,返回(新 payload, 是否有改动);
	// 返回值是写入后的 version(无改动=当前 version),供回写方广播给观看端。
	MutateSnapshot(projectID int64, fn func(payload json.RawMessage) (json.RawMessage, bool, error)) (int64, error)
	MutateClientSnapshot(projectID int64, fn func(payload json.RawMessage) (json.RawMessage, bool, error)) (int64, error)
	GetLatest(projectID int64) (*Snapshot, error)
	// 后台清理 worker 用：列待清理(已软删)项目；硬删项目行(清完资产/任务后)。
	ListPendingDeletion(limit int) ([]PendingProject, error)
	HardDelete(projectID int64) error
}

// PendingProject 是待后台清理的软删项目——清理需要 user_id 做配额回收。
type PendingProject struct {
	ID     int64
	UserID int64
}

type SQLiteRepository struct{ db *sql.DB }

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (r *SQLiteRepository) Create(userID int64, name, description string) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO projects (user_id, name, description) VALUES (?, ?, ?)`, userID, name, description)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListForUser 列该用户未删除的项目，按 updated_at 倒序。
//
// limit<=0 表示不分页、拉全量——**CLI 的 /cli/projects 走的就是这条**，语义与加分页
// 之前逐字一致。网页列表页传 limit>0 做无限滚动。total 恒是该用户项目总数（与分页
// 无关），供前端判断还有没有下一页。
//
// 分页必须发生在**派生表里**，不能把 LIMIT 挂在最外层：外层 LIMIT 是在
// 「LEFT JOIN 全部 assets + GROUP BY」跑完之后才截断的，项目上百、每个项目几十条
// 资产时，这个 JOIN 才是本页最沉的一次查询——截断结果不减少扫描量，等于没优化。
// 先在子查询里分页出这一页的项目，再只 JOIN 这些项目的资产。
func (r *SQLiteRepository) ListForUser(userID int64, limit, offset int) ([]Project, int, error) {
	var total int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM projects WHERE user_id = ? AND deleted_at IS NULL`, userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	inner := `SELECT id, user_id, name, description, created_at, updated_at, cover_url
	            FROM projects
	           WHERE user_id = ? AND deleted_at IS NULL
	           ORDER BY updated_at DESC, id DESC`
	args := []any{userID}
	if limit > 0 {
		inner += ` LIMIT ? OFFSET ?`
		if offset < 0 {
			offset = 0
		}
		args = append(args, limit, offset)
	}

	rows, err := r.db.Query(`
		SELECT p.id, p.user_id, p.name, IFNULL(p.description, ''), p.created_at, p.updated_at,
		       IFNULL(SUM(a.size_bytes), 0) AS storage_bytes,
		       -- 封面两层来源：手动指定优先，没指定才自动取项目里最早的一张图片（上传、生成都算）。
		       -- NULLIF 是必需的——「清除封面」写的是空串而不是 NULL（见 SetCover），
		       -- 只用 IFNULL 的话空串会被当成有效值，清除后封面直接变没，而不是
		       -- 回落到自动取图。
		       IFNULL(NULLIF(p.cover_url, ''), IFNULL((
		         SELECT g.url FROM assets g
		          WHERE g.project_id = p.id
		            AND g.mime_type LIKE 'image/%'
		          ORDER BY g.id ASC
		          LIMIT 1
		       ), '')) AS cover_url
		  FROM (`+inner+`) p
		  LEFT JOIN assets a ON a.project_id = p.id
		 GROUP BY p.id
		 ORDER BY p.updated_at DESC, p.id DESC`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.StorageBytes, &p.CoverURL); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *SQLiteRepository) GetForUser(userID, projectID int64) (*Project, error) {
	row := r.db.QueryRow(`
		SELECT p.id, p.user_id, p.name, IFNULL(p.description, ''), p.created_at, p.updated_at,
		       IFNULL(SUM(a.size_bytes), 0) AS storage_bytes
		  FROM projects p
		  LEFT JOIN assets a ON a.project_id = p.id
		 WHERE p.user_id = ? AND p.id = ? AND p.deleted_at IS NULL
		 GROUP BY p.id`, userID, projectID)
	var p Project
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.StorageBytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *SQLiteRepository) Touch(userID, projectID int64) error {
	_, err := r.db.Exec(`UPDATE projects SET updated_at = CURRENT_TIMESTAMP WHERE user_id = ? AND id = ? AND deleted_at IS NULL`, userID, projectID)
	return err
}

func (r *SQLiteRepository) Update(userID, projectID int64, name, description string) error {
	if _, err := r.GetForUser(userID, projectID); err != nil {
		return err
	}
	_, err := r.db.Exec(`UPDATE projects SET name = ?, description = ? WHERE user_id = ? AND id = ?`, name, description, userID, projectID)
	return err
}

// SetCover 落手动封面。传空串走的是 UPDATE …=” 而不是 NULL——两者在读侧被
// NULLIF 一视同仁，写侧统一成一种形态就不用在这里分支。
// 存在性先于写入单独查一次（与 Update 同一模式）。不靠 UPDATE 的 RowsAffected
// 判 ErrNotFound：MySQL 对「新值与旧值相同」的 UPDATE 报告 0 行受影响，
// 重复设同一张封面会被误判成项目不存在。
// SQLite 报告匹配行数，但存在性仍单独查，两种库行为一致。
func (r *SQLiteRepository) SetCover(userID, projectID int64, coverURL string) error {
	if _, err := r.GetForUser(userID, projectID); err != nil {
		return err
	}
	_, err := r.db.Exec(
		`UPDATE projects SET cover_url = ? WHERE user_id = ? AND id = ?`,
		coverURL, userID, projectID)
	return err
}

// Delete 软删：打 deleted_at 立即从列表隐藏；级联清资产/任务/存储由后台 worker 异步做。
func (r *SQLiteRepository) Delete(userID, projectID int64) error {
	res, err := r.db.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE user_id = ? AND id = ? AND deleted_at IS NULL`, userID, projectID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPendingDeletion 列已软删、待后台清理的项目(含 user_id 供配额回收)。
func (r *SQLiteRepository) ListPendingDeletion(limit int) ([]PendingProject, error) {
	rows, err := r.db.Query(`SELECT id, user_id FROM projects WHERE deleted_at IS NOT NULL ORDER BY deleted_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingProject
	for rows.Next() {
		var p PendingProject
		if err := rows.Scan(&p.ID, &p.UserID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HardDelete 真正删项目行(worker 清完资产/任务后)。workflow_snapshots 经 FK ON DELETE CASCADE 自动删。
func (r *SQLiteRepository) HardDelete(projectID int64) error {
	_, err := r.db.Exec(`DELETE FROM projects WHERE id = ?`, projectID)
	return err
}

// SaveAuthoritative 给后端权威写路径用（agent、workflow clone 初始化等）。
// 这些写入代表「系统最新真相」，绝不能因为某个网页用户手里的旧 baseVersion
// 而失败；相反，它们每次成功都必须把 version 顶高，迫使旧网页保存随后收到 409。
//
// 写入后的 version 用 RETURNING 在同一条语句里取回：曾经写完再单独 SELECT 一次，
// 两条语句之间别的写者（网页保存、生成回写）推高的版本会被当成本次写入的版本返回。
func (r *SQLiteRepository) SaveAuthoritative(projectID int64, payload json.RawMessage) (int64, error) {
	var version int64
	err := r.db.QueryRow(
		`INSERT INTO workflow_snapshots (project_id, payload, version) VALUES (?, ?, 1) ON CONFLICT (project_id) DO UPDATE SET payload = excluded.payload, version = workflow_snapshots.version + 1 RETURNING version`,
		projectID,
		string(payload),
	).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// MutateSnapshot 事务内读-改-写画布快照:拿当前 payload,交给 fn 改,
// 有改动则回写并 version+1。事务以 BEGIN IMMEDIATE 开始（db.Open 的 _txlock=immediate），
// 一开始就拿到库级写锁，并发的读-改-写排队执行，防丢更新;
// version 递增逼过期网页保存收 409。
// 对标 drama 的 MutateCandidate(那是候选 JSON 的原子改;这里是整块画布快照)。
//
// 快照行不存在时**建行**,不是跳过。曾经这里 return nil 静默当成功,理由是"没保存过
// 就没节点可写"——那对生成结果回写成立(任务的节点必然已经在画布上),对 CLI 完全不成立:
// `miaoju project create` 只 INSERT projects 一行,新项目根本没有快照行,于是 CLI 的
// 每一次 node create 都写进空气还回 200,画布永远是空的。写路径分成两个函数、只有
// MutateClientSnapshot(网页保存)处理了这一支,是这个 bug 藏了这么久的原因。
//
// 项目真被删了不会因此造出孤儿快照:workflow_snapshots.project_id 有 ON DELETE CASCADE
// 外键,INSERT 会被外键直接挡下报错——这正是当初 return nil 想防的事,外键已经防住了。
//
// 建行时 last_client_version 给 0 而不是 1:这里是后端权威写入,按既有语义不推进
// 客户端版本线,网页据此仍能判断"我本地这份不是最新的"。
//
// 返回值是**写入后**的 version（无改动时是当前 version，行不存在且未建行时为 0）。
// 加这个返回值是为了让后端回写方能把新版本号广播给观看端：没有它，canvasgen 就得
// 在事务外再 SELECT 一次,读到的还可能是别人后来又推高的版本。
func (r *SQLiteRepository) MutateSnapshot(projectID int64, fn func(payload json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRow(`SELECT payload, version FROM workflow_snapshots WHERE project_id = ?`, projectID)
	var payload []byte
	var version int64
	if err := row.Scan(&payload, &version); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		// fn 传 nil 而不是空 JSON:与 MutateClientSnapshot 一致,让回调自己决定
		// 空画布长什么样(它本来就要处理 payload 为空的老快照)。
		next, changed, err := fn(nil)
		if err != nil {
			return 0, err
		}
		if !changed {
			return 0, nil
		}
		if _, err := tx.Exec(
			`INSERT INTO workflow_snapshots (project_id, payload, version, last_client_version) VALUES (?, ?, 1, 0)`,
			projectID,
			string(next),
		); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		committed = true
		return 1, nil
	}
	next, changed, err := fn(json.RawMessage(payload))
	if err != nil {
		return 0, err
	}
	if !changed {
		return version, nil
	}
	nextVersion := version + 1
	if _, err := tx.Exec(`UPDATE workflow_snapshots SET payload = ?, version = ? WHERE project_id = ?`, string(next), nextVersion, projectID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return nextVersion, nil
}

// MutateClientSnapshot 在 BEGIN IMMEDIATE 事务内应用一次用户触发的画布改动（事务开头即拿库级写锁）。
// 与后端权威回写不同，客户端改动会把 last_client_version 提到新版本号。
func (r *SQLiteRepository) MutateClientSnapshot(projectID int64, fn func(payload json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRow(`SELECT payload, version FROM workflow_snapshots WHERE project_id = ?`, projectID)
	var payload []byte
	var version int64
	if err := row.Scan(&payload, &version); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		next, changed, err := fn(nil)
		if err != nil {
			return 0, err
		}
		if !changed {
			if err := tx.Commit(); err != nil {
				return 0, err
			}
			committed = true
			return 0, nil
		}
		if _, err := tx.Exec(
			`INSERT INTO workflow_snapshots (project_id, payload, version, last_client_version) VALUES (?, ?, 1, 1)`,
			projectID,
			string(next),
		); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		committed = true
		return 1, nil
	}

	next, changed, err := fn(json.RawMessage(payload))
	if err != nil {
		return 0, err
	}
	if !changed {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		committed = true
		return version, nil
	}
	nextVersion := version + 1
	if _, err := tx.Exec(
		`UPDATE workflow_snapshots SET payload = ?, version = ?, last_client_version = ? WHERE project_id = ?`,
		string(next),
		nextVersion,
		nextVersion,
		projectID,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return nextVersion, nil
}

func (r *SQLiteRepository) GetLatest(projectID int64) (*Snapshot, error) {
	row := r.db.QueryRow(`SELECT payload, synced_at, version FROM workflow_snapshots WHERE project_id = ?`, projectID)
	var snapshot Snapshot
	var raw []byte
	if err := row.Scan(&raw, &snapshot.SyncedAt, &snapshot.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	snapshot.Payload = json.RawMessage(raw)
	return &snapshot, nil
}

func (r *SQLiteRepository) GetLatestSnapshot(projectID int64) (json.RawMessage, error) {
	snapshot, err := r.GetLatest(projectID)
	if err != nil || snapshot == nil {
		return nil, err
	}
	return snapshot.Payload, nil
}

// SnapshotProjectsContaining 找快照 JSON 里含某个子串的项目——补搬 worker 用它定位
// 「哪些画布存了这条上游外链」。旧 URL 可能被复制粘贴到别的画布,所以按内容搜而不是
// 只看任务的 project_id。instr 精确子串(区分大小写,不把 _ % 当通配符)全表扫:调用方是每 5 分钟、每轮几十条的后台 worker,
// 快照表行数=项目数,可接受;做索引/倒排为这一个低频场景不值。
func (r *SQLiteRepository) SnapshotProjectsContaining(substr string) ([]int64, error) {
	if substr == "" {
		return nil, nil
	}
	rows, err := r.db.Query(`SELECT project_id FROM workflow_snapshots WHERE instr(payload, ?) > 0`, substr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}
