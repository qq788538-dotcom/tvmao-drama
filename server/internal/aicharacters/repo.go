// Package aicharacters 是自由画布的「AI 角色库」：一批预制、已过 Seedance 合规
// 校验的公共角色（seedance_characters 表，由内置种子数据导入，见 P2b）。
//
// 刻意保持独立：不建 service 层、不与素材库/assets 共享代码——这批图是平台公共
// 资产，游离在用户配额、assets 引用计数和孤儿 GC 之外（绝不能为它插 assets 行，
// 否则用户删节点会触发 TryReclaim 把公共对象标成 orphan、7 天后从对象存储真删）。
// 其他模块将来要用，按约定 copy 代码，不共用。
package aicharacters

import (
	"database/sql"
	"errors"
	"strings"
)

// Character 与 seedance_characters 表一行同构。ImageURL 是 canonical 的
// media://<object_key> 形式，出图前必须过 MediaSigner——喂裸 key 给签名器
// 会被原样吐回（见 mediaProtocol 注释）。
type Character struct {
	AssetID     string
	Age         int
	Gender      string
	Occupation  string
	Description string
	AgeGroup    string
	ImageURL    string
}

// ListParams 的空字符串字段表示不过滤。
type ListParams struct {
	Search   string // occupation + description 模糊匹配
	Gender   string // 男 / 女
	AgeGroup string // 青年 / 中年 / 老年
	Offset   int
	Limit    int
}

type Repository interface {
	List(p ListParams) ([]Character, error)
	// Get 按 asset_id 取单个角色；不存在返回 ErrNotFound。项目管理「导入公共人像
	// 为角色资产」按这个入口取原始数据。
	Get(assetID string) (*Character, error)
	// PublicAssetIDs 按裸 object_key 批量查角色的 Seedance asset_id，供合规层
	// 在「用户自己的校验记录查不到」时兜底。查不到的 key 不出现在结果里。
	PublicAssetIDs(objectKeys []string) (map[string]string, error)
}

// mediaProtocol 与 assets.MediaProtocol 同值。不 import assets：本包只需要知道
// canonical URL 的形态（同 library/seedance_service.go 的做法）。
const mediaProtocol = "media://"

var ErrNotFound = errors.New("virtual portrait not found")

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// escapeLike 把用户输入转义成 LIKE 安全的字面量（\ % _ 都是 LIKE 元字符）。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (r *SQLiteRepository) List(p ListParams) ([]Character, error) {
	q := `SELECT asset_id, age, gender, occupation, description, age_group, image_url
	        FROM seedance_characters WHERE 1=1`
	args := []any{}
	if p.Gender != "" {
		q += ` AND gender=?`
		args = append(args, p.Gender)
	}
	if p.AgeGroup != "" {
		q += ` AND age_group=?`
		args = append(args, p.AgeGroup)
	}
	if p.Search != "" {
		pat := "%" + escapeLike(p.Search) + "%"
		q += ` AND (occupation LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\')`
		args = append(args, pat, pat)
	}
	// asset_id 自带创建时间戳，按它排序 = 稳定的时间序；1729 行的静态表不值得加索引。
	q += ` ORDER BY asset_id LIMIT ? OFFSET ?`
	args = append(args, p.Limit, p.Offset)

	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		var c Character
		if err := rows.Scan(&c.AssetID, &c.Age, &c.Gender, &c.Occupation,
			&c.Description, &c.AgeGroup, &c.ImageURL); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *SQLiteRepository) Get(assetID string) (*Character, error) {
	var c Character
	err := r.db.QueryRow(`SELECT asset_id, age, gender, occupation, description, age_group, image_url
	        FROM seedance_characters WHERE asset_id=?`, assetID).
		Scan(&c.AssetID, &c.Age, &c.Gender, &c.Occupation, &c.Description, &c.AgeGroup, &c.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *SQLiteRepository) PublicAssetIDs(objectKeys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(objectKeys) == 0 {
		return out, nil
	}
	// 表存的是 canonical 的 media://<key>，调用方（合规层）手里是裸 key——在这里
	// 补前缀查询，让「image_url 存什么形态」的知识只留在本包。
	q := `SELECT asset_id, image_url FROM seedance_characters WHERE image_url IN (?` +
		strings.Repeat(",?", len(objectKeys)-1) + `)`
	args := make([]any, 0, len(objectKeys))
	for _, k := range objectKeys {
		args = append(args, mediaProtocol+k)
	}
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var assetID, imageURL string
		if err := rows.Scan(&assetID, &imageURL); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(imageURL, mediaProtocol)] = assetID
	}
	return out, rows.Err()
}
