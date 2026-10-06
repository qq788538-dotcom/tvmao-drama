package agent

import "database/sql"

// NewBackboneSwitcher 返回设置页切换模型用的写接口（与 NewBackboneRepo 同一张表）。
func NewBackboneSwitcher(db *sql.DB) BackboneModelSwitcher { return &sqliteBackboneRepo{db: db} }

func (r *sqliteBackboneRepo) SetModelName(id int64, modelName string) error {
	return r.execOne(`UPDATE agent_backbone_models SET model_name=?, model_name_overridden=1 WHERE id=?`, modelName, id)
}

func (r *sqliteBackboneRepo) ResetModelName(id int64, defaultName string) error {
	return r.execOne(`UPDATE agent_backbone_models SET model_name=?, model_name_overridden=0 WHERE id=?`, defaultName, id)
}

// execOne 执行一条按 id 的 UPDATE；SQLite 的 RowsAffected 是匹配行数，0 即 id 不存在。
func (r *sqliteBackboneRepo) execOne(q string, args ...any) error {
	res, err := r.db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBackboneNotFound
	}
	return nil
}
