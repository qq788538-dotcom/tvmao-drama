package aicharacters

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func countChars(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM seedance_characters`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// requirePack 返回内置角色种子；构建时没放数据包（开源仓库默认如此）则跳过测试。
func requirePack(t *testing.T) []byte {
	t.Helper()
	data, ok, err := readSeed()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("未放入 AI 角色库数据包，见 seed/README.md")
	}
	return data
}

func TestEmbeddedSeedMatchesRepoSeed(t *testing.T) {
	embedded, ok, err := readSeed()
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join("..", "..", "..", "seed", "seedance_characters.jsonl"))
	if errors.Is(err, fs.ErrNotExist) && !ok {
		t.Skip("未放入 AI 角色库数据包")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, repo) {
		t.Fatal("seeddata/seedance_characters.jsonl 与 seed/ 不一致：运行 scripts/sync-seed.sh")
	}
}

func TestImportWithoutPackLeavesTableAlone(t *testing.T) {
	if _, ok, _ := readSeed(); ok {
		t.Skip("已放入数据包")
	}
	conn := dbtest.Open(t)
	if _, err := importSeed(context.Background(), conn, []byte(lineA)); err != nil {
		t.Fatal(err)
	}
	res, err := Import(conn)
	if err != nil || !res.Skipped || countChars(t, conn) != 1 {
		t.Fatalf("没有数据包时应跳过且不删已有行: %+v %v 行数 %d", res, err, countChars(t, conn))
	}
}

func TestImportFreshDBAndIdempotent(t *testing.T) {
	want := len(mustParse(t, requirePack(t)))
	conn := dbtest.Open(t)
	res, err := Import(conn)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || res.Inserted != want || countChars(t, conn) != want {
		t.Fatalf("首次导入: %+v, 行数 %d", res, countChars(t, conn))
	}
	// 列表接口用的 repo 能读到数据。
	list, err := NewSQLiteRepository(conn).List(ListParams{Limit: 10})
	if err != nil || len(list) != 10 || list[0].ImageURL == "" || !strings.HasPrefix(list[0].ImageURL, "media://") {
		t.Fatalf("List: %v %+v", err, list)
	}
	res, err = Import(conn)
	if err != nil || !res.Skipped {
		t.Fatalf("再次导入应跳过: %+v %v", res, err)
	}
	// 抹掉哈希强制重新合并：也不应有任何行变化（不碰 updated_at）。
	if _, err := conn.Exec(`UPDATE seedance_characters SET updated_at = '2000-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM builtin_seed_state WHERE dataset = ?`, DatasetName); err != nil {
		t.Fatal(err)
	}
	res, err = Import(conn)
	if err != nil || res.Skipped || res.Inserted+res.Updated+res.Deleted != 0 {
		t.Fatalf("强制重导应无变化: %+v %v", res, err)
	}
	var touched int
	conn.QueryRow(`SELECT COUNT(*) FROM seedance_characters WHERE updated_at <> '2000-01-01 00:00:00'`).Scan(&touched)
	if touched != 0 || countChars(t, conn) != want {
		t.Fatalf("被改动的行 %d", touched)
	}
}

func mustParse(t *testing.T, data []byte) []seedCharacter {
	t.Helper()
	rows, err := parseSeed(data)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

const (
	lineA = `{"asset_id":"asset-1","age":20,"gender":"女","occupation":"医生","description":"d1","age_group":"青年","image_url":"media://k/1.jpg","extra":"ignored"}` + "\n"
	lineB = `{"asset_id":"asset-2","age":50,"gender":"男","occupation":"厨师","description":"d2","age_group":"中年","image_url":"media://k/2.jpg"}` + "\n"
	lineC = `{"asset_id":"asset-3","age":70,"gender":"男","occupation":"农夫","description":"d3","age_group":"老年","image_url":"media://k/3.jpg"}` + "\n"
)

func TestImportSeedChangesUpsertAndDelete(t *testing.T) {
	conn := dbtest.Open(t)
	ctx := context.Background()
	if res, err := importSeed(ctx, conn, []byte(lineA+lineB)); err != nil || res.Inserted != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	// B 改了职业，A 被移除，C 新增。
	newB := strings.Replace(lineB, "厨师", "面点师", 1)
	res, err := importSeed(ctx, conn, []byte(newB+lineC))
	if err != nil || res.Inserted != 1 || res.Updated != 1 || res.Deleted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := NewSQLiteRepository(conn).Get("asset-1"); err != ErrNotFound {
		t.Fatalf("asset-1 应已删除: %v", err)
	}
	c, err := NewSQLiteRepository(conn).Get("asset-2")
	if err != nil || c.Occupation != "面点师" {
		t.Fatalf("%+v %v", c, err)
	}
	if countChars(t, conn) != 2 {
		t.Fatal("行数应为 2")
	}
}

func TestImportRollsBackOnBadSeed(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := importSeed(context.Background(), conn, []byte(lineA+"{bad")); err == nil {
		t.Fatal("坏种子应报错")
	}
	if countChars(t, conn) != 0 {
		t.Fatal("坏种子不应写入")
	}
	if _, err := importSeed(context.Background(), conn, []byte(lineA+lineA)); err == nil {
		t.Fatal("重复 asset_id 应报错")
	}
}
