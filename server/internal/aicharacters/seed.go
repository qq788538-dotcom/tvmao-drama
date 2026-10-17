package aicharacters

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// seeddata/ 是仓库根 seed/seedance_characters.jsonl 的逐字节副本（go:embed 不能引用包目录之外的文件）。
// 真源是 seed/；改了种子运行 scripts/sync-seed.sh，忘了同步会被 TestEmbeddedSeedMatchesRepoSeed 拦住。
// 角色数据不随开源仓库分发（.gitignore 已排除）：没有数据包时这里只有 README.md，Import 什么都不做。
//
//go:embed seeddata
var seedFS embed.FS

const seedPath = "seeddata/seedance_characters.jsonl"

// readSeed 读内置角色种子；ok=false 表示构建时没放数据包。
func readSeed() (data []byte, ok bool, err error) {
	data, err = seedFS.ReadFile(seedPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// seedFormatVersion 并入种子哈希；导入语义变化时递增，让已安装用户升级后重新导入一次。
const seedFormatVersion = "p2b-chars-1"

// DatasetName 是本数据集在 builtin_seed_state 里的名字。
const DatasetName = "ai_characters"

// seedCharacter 是 seedance_characters.jsonl 的一行。字段映射以表结构为准，
// 行里多出来的字段（created_at/updated_at 等）忽略：本地时间戳由本机生成。
type seedCharacter struct {
	AssetID     string `json:"asset_id"`
	Age         int    `json:"age"`
	Gender      string `json:"gender"`
	Occupation  string `json:"occupation"`
	Description string `json:"description"`
	AgeGroup    string `json:"age_group"`
	ImageURL    string `json:"image_url"`
}

// HasBuiltinData 报告构建时是否放入了角色数据包。
func HasBuiltinData() bool {
	_, ok, _ := readSeed()
	return ok
}

// ImportResult 描述一次导入的结果。Skipped 为 true 表示哈希没变、没碰库。
type ImportResult struct {
	Skipped                           bool
	Total, Inserted, Updated, Deleted int
}

// Import 把内置角色数据导入 seedance_characters。sidecar 每次启动在 builtin.Apply 之后调用一次。
// 构建时没放数据包则跳过，不碰表里已有的角色。
func Import(conn *sql.DB) (ImportResult, error) {
	data, ok, err := readSeed()
	if err != nil || !ok {
		return ImportResult{Skipped: true}, err
	}
	return importSeed(context.Background(), conn, data)
}

func seedHash(data []byte) string {
	sum := sha256.New()
	io.WriteString(sum, seedFormatVersion+"\n")
	sum.Write(data)
	return hex.EncodeToString(sum.Sum(nil))
}

func parseSeed(data []byte) ([]seedCharacter, error) {
	var out []seedCharacter
	seen := map[string]bool{}
	r := bufio.NewReader(bytes.NewReader(data))
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var c seedCharacter
			if jerr := json.Unmarshal(line, &c); jerr != nil {
				return nil, fmt.Errorf("seedance_characters.jsonl 第 %d 行: %w", n, jerr)
			}
			if c.AssetID == "" || c.Gender == "" || c.AgeGroup == "" || c.ImageURL == "" {
				return nil, fmt.Errorf("seedance_characters.jsonl 第 %d 行: asset_id/gender/age_group/image_url 不能为空", n)
			}
			if seen[c.AssetID] {
				return nil, fmt.Errorf("seedance_characters.jsonl 第 %d 行: asset_id %s 重复", n, c.AssetID)
			}
			seen[c.AssetID] = true
			out = append(out, c)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func readHash(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, bool, error) {
	var h string
	err := q.QueryRowContext(ctx, `SELECT hash FROM builtin_seed_state WHERE dataset = ?`, DatasetName).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

func importSeed(ctx context.Context, conn *sql.DB, data []byte) (ImportResult, error) {
	rows, err := parseSeed(data)
	if err != nil {
		return ImportResult{}, err
	}
	h := seedHash(data)
	if cur, ok, err := readHash(ctx, conn); err != nil {
		return ImportResult{}, err
	} else if ok && cur == h {
		return ImportResult{Skipped: true, Total: len(rows)}, nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	// 拿到写锁后再确认一次：并发启动的另一个进程可能已经导完了。
	if cur, ok, err := readHash(ctx, tx); err != nil {
		return ImportResult{}, err
	} else if ok && cur == h {
		return ImportResult{Skipped: true, Total: len(rows)}, nil
	}
	res := ImportResult{Total: len(rows)}

	existing := map[string]bool{}
	er, err := tx.QueryContext(ctx, `SELECT asset_id FROM seedance_characters`)
	if err != nil {
		return ImportResult{}, err
	}
	for er.Next() {
		var id string
		if err := er.Scan(&id); err != nil {
			er.Close()
			return ImportResult{}, err
		}
		existing[id] = true
	}
	if err := er.Err(); err != nil {
		er.Close()
		return ImportResult{}, err
	}
	er.Close()

	// 种子即真相（同 agentseed）：种子里已删除的角色从表里删掉。
	inSeed := make(map[string]bool, len(rows))
	for _, c := range rows {
		inSeed[c.AssetID] = true
	}
	for id := range existing {
		if !inSeed[id] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM seedance_characters WHERE asset_id = ?`, id); err != nil {
				return ImportResult{}, err
			}
			res.Deleted++
		}
	}
	// 只在内容真有差异时才更新，没变的行不碰 updated_at。
	for _, c := range rows {
		r, err := tx.ExecContext(ctx, `INSERT INTO seedance_characters
			(asset_id, age, gender, occupation, description, age_group, image_url) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (asset_id) DO UPDATE SET age = excluded.age, gender = excluded.gender,
			  occupation = excluded.occupation, description = excluded.description,
			  age_group = excluded.age_group, image_url = excluded.image_url
			WHERE age IS NOT excluded.age OR gender IS NOT excluded.gender
			  OR occupation IS NOT excluded.occupation OR description IS NOT excluded.description
			  OR age_group IS NOT excluded.age_group OR image_url IS NOT excluded.image_url`,
			c.AssetID, c.Age, c.Gender, c.Occupation, c.Description, c.AgeGroup, c.ImageURL)
		if err != nil {
			return ImportResult{}, fmt.Errorf("导入角色 %s: %w", c.AssetID, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			if existing[c.AssetID] {
				res.Updated++
			} else {
				res.Inserted++
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO builtin_seed_state (dataset, hash) VALUES (?, ?)
		ON CONFLICT (dataset) DO UPDATE SET hash = excluded.hash, applied_at = CURRENT_TIMESTAMP`, DatasetName, h); err != nil {
		return ImportResult{}, err
	}
	return res, tx.Commit()
}
