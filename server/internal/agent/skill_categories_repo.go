// Package agent — skill_categories_repo.go：skill 分类字典的 CRUD + 排序。
// 分类曾写死在前端两处常量里；本表把「词表 + 顺序 + 颜色」交给后台维护，
// skills.category 仍按名字（字符串）引用，存量数据零迁移。
package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type SkillCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
}

// ErrCategoryInUse：删除仍被 skill 引用的分类会留下孤儿分类名（前端渲染成
// 无色兜底），拒绝删除并让管理员先改掉那些 skill。
var ErrCategoryInUse = errors.New("category is still used by skills")

type SkillCategoriesRepo interface {
	List() ([]SkillCategory, error)
	Create(name, color string) (int64, error)
	// Update 改名/改色。改名会级联更新 skills.category（同一事务）——否则存量
	// skill 全部变成孤儿分类。
	Update(id int64, name, color string) error
	Delete(id int64) error
	// SetOrder 按给定 id 序整表重排（index 即 sort_order）。上/下移在前端算好
	// 新序列一次提交，避免「交换两行」式接口的并发交错。
	SetOrder(ids []int64) error
}

type sqliteSkillCategoriesRepo struct{ db *sql.DB }

func NewSkillCategoriesRepo(db *sql.DB) SkillCategoriesRepo {
	return &sqliteSkillCategoriesRepo{db: db}
}

func (r *sqliteSkillCategoriesRepo) List() ([]SkillCategory, error) {
	rows, err := r.db.Query(`SELECT id, name, color, sort_order FROM skill_categories ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillCategory{}
	for rows.Next() {
		var c SkillCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *sqliteSkillCategoriesRepo) Create(name, color string) (int64, error) {
	if color == "" {
		color = "blue"
	}
	// 新分类排到最后：MAX(sort_order)+1。
	res, err := r.db.Exec(`INSERT INTO skill_categories (name, color, sort_order)
		SELECT ?, ?, COALESCE(MAX(sort_order),0)+1 FROM skill_categories AS sc`, name, color)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *sqliteSkillCategoriesRepo) Update(id int64, name, color string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var oldName string
	if err := tx.QueryRow(`SELECT name FROM skill_categories WHERE id=?`, id).Scan(&oldName); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE skill_categories SET name=?, color=? WHERE id=?`, name, color, id); err != nil {
		return err
	}
	// 改名级联：skills 的单值 category 与 categories 数组都按名字引用，任何一处
	// 不跟着改都是整批孤儿。数组用 JSON 函数原地替换（按值精确匹配）。
	if name != oldName {
		if _, err := tx.Exec(`UPDATE skills SET category=? WHERE category=?`, name, oldName); err != nil {
			return err
		}
		// categories 数组按值替换：在 Go 里解码、换掉第一个匹配（与 MySQL JSON_SEARCH 'one' 同语义）、写回。
		rows, err := tx.Query(`SELECT id, categories FROM skills
			WHERE categories IS NOT NULL
			  AND EXISTS (SELECT 1 FROM json_each(skills.categories) WHERE json_each.value = ?)`, oldName)
		if err != nil {
			return err
		}
		type pending struct{ id, cats string }
		var todo []pending
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var cats []string
			if json.Unmarshal([]byte(raw), &cats) != nil {
				continue
			}
			for i, c := range cats {
				if c == oldName {
					cats[i] = name
					break
				}
			}
			b, _ := json.Marshal(cats)
			todo = append(todo, pending{id: id, cats: string(b)})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range todo {
			if _, err := tx.Exec(`UPDATE skills SET categories=? WHERE id=?`, p.cats, p.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (r *sqliteSkillCategoriesRepo) Delete(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var name string
	if err := tx.QueryRow(`SELECT name FROM skill_categories WHERE id=?`, id).Scan(&name); err != nil {
		return err
	}
	var used int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM skills
		  WHERE category=? OR EXISTS (SELECT 1 FROM json_each(COALESCE(skills.categories, '[]')) WHERE json_each.value = ?)`,
		name, name).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w: %d skill(s) in %q", ErrCategoryInUse, used, name)
	}
	if _, err := tx.Exec(`DELETE FROM skill_categories WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sqliteSkillCategoriesRepo) SetOrder(ids []int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE skill_categories SET sort_order=? WHERE id=?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
