package main

import (
	"database/sql"

	"github.com/james/huabu_studio/apps/api/internal/library"
)

// libraryReclaimer 把 library 孤儿回收适配成 generation.ObjectReclaimer
// （TryReclaim + IsStillReferenced 合一）。
type libraryReclaimer struct{ orphans library.OrphansRepository }

func (l libraryReclaimer) ReclaimTx(tx *sql.Tx, objectKey string, userID, sizeBytes int64) (bool, error) {
	referenced, err := library.IsStillReferenced(tx, objectKey)
	if err != nil {
		return false, err
	}
	if referenced {
		return false, nil
	}
	return true, l.orphans.Mark(tx, objectKey, userID, sizeBytes)
}
