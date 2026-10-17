package library

import (
	"database/sql"
)

// Querier 让 IsStillReferenced 既能用 *sql.DB 也能用 *sql.Tx
type Querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// IsStillReferenced 实时查 assets ∪ library_items 是否还有人引用此 object_key。
// 用索引 seek，开销 < 1 ms。
func IsStillReferenced(q Querier, objectKey string) (bool, error) {
	var exists bool
	err := q.QueryRow(`
        SELECT EXISTS(SELECT 1 FROM assets        WHERE object_key=? LIMIT 1)
            OR EXISTS(SELECT 1 FROM library_items WHERE object_key=? LIMIT 1)
    `, objectKey, objectKey).Scan(&exists)
	return exists, err
}

// ReclaimDeps 是 TryReclaim 需要的最小依赖集合。
// 调用方传 *sql.Tx 同时给 Querier 和 Execer，即可在事务内完成「删行 + 标 orphan」。
type ReclaimDeps struct {
	Querier Querier
	Execer  Execer
	Orphans OrphansRepository
}

// TryReclaim 在 DB 删行后调用。如果该 object_key 还有别处引用，啥也不做；
// 否则在 orphan_objects 里打标记。永不直接删 TOS——交给 GC sweeper 在 7 天宽限期后真删。
func TryReclaim(d ReclaimDeps, objectKey string, userID, sizeBytes int64) error {
	referenced, err := IsStillReferenced(d.Querier, objectKey)
	if err != nil {
		return err
	}
	if referenced {
		return nil
	}
	return d.Orphans.Mark(d.Execer, objectKey, userID, sizeBytes)
}
