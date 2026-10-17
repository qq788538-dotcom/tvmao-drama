package aicharacters

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db/dbtest"
)

func seedCharacters(t *testing.T, conn *sql.DB) {
	t.Helper()
	rows := []struct {
		id    string
		age   int
		g     string
		occ   string
		desc  string
		group string
		img   string
	}{
		{"asset-001", 25, "女", "演员", "常演宫女", "青年", "media://builtin/characters/asset-001.jpg"},
		{"asset-002", 45, "男", "厨师", "川菜馆主厨", "中年", "media://builtin/characters/asset-002.jpg"},
		{"asset-003", 70, "男", "养蜂人", "只卖100%纯蜂蜜", "老年", "media://builtin/characters/asset-003.png"},
		{"asset-004", 30, "女", "程序员", "只写 snake_case 代码", "青年", "media://builtin/characters/asset-004.jpg"},
	}
	for _, r := range rows {
		if _, err := conn.Exec(`INSERT INTO seedance_characters
			(asset_id, age, gender, occupation, description, age_group, image_url) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.age, r.g, r.occ, r.desc, r.group, r.img); err != nil {
			t.Fatal(err)
		}
	}
}

func assetIDs(list []Character) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.AssetID)
	}
	return out
}

func TestListFiltersAndOrder(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	cases := []struct {
		name string
		p    ListParams
		want []string
	}{
		{"全部按 asset_id 排序", ListParams{Limit: 10}, []string{"asset-001", "asset-002", "asset-003", "asset-004"}},
		{"性别", ListParams{Gender: "女", Limit: 10}, []string{"asset-001", "asset-004"}},
		{"年龄段", ListParams{AgeGroup: "老年", Limit: 10}, []string{"asset-003"}},
		{"搜职业", ListParams{Search: "演员", Limit: 10}, []string{"asset-001"}},
		{"搜描述", ListParams{Search: "川菜", Limit: 10}, []string{"asset-002"}},
		{"分页", ListParams{Limit: 2, Offset: 1}, []string{"asset-002", "asset-003"}},
	}
	for _, c := range cases {
		got, err := repo.List(c.p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(assetIDs(got), c.want) {
			t.Fatalf("%s: got %v want %v", c.name, assetIDs(got), c.want)
		}
	}
}

// SQLite 的 LIKE 没有默认转义字符：漏写 ESCAPE 时 "_" 会匹配任意单字、"%" 会匹配全部。
func TestListSearchTreatsWildcardsLiterally(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	for search, want := range map[string][]string{
		"%":    {"asset-003"},
		"100%": {"asset-003"},
		"_":    {"asset-004"},
		"e_c":  {"asset-004"},
	} {
		got, err := repo.List(ListParams{Search: search, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(assetIDs(got), want) {
			t.Fatalf("search %q: got %v want %v", search, assetIDs(got), want)
		}
	}
}

func TestGetAndNotFound(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	c, err := repo.Get("asset-002")
	if err != nil {
		t.Fatal(err)
	}
	if c.Age != 45 || c.Gender != "男" || c.ImageURL != "media://builtin/characters/asset-002.jpg" {
		t.Fatalf("got %+v", c)
	}
	if _, err := repo.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPublicAssetIDsMapsBareKeys(t *testing.T) {
	conn := dbtest.Open(t)
	seedCharacters(t, conn)
	repo := NewSQLiteRepository(conn)
	got, err := repo.PublicAssetIDs([]string{"builtin/characters/asset-002.jpg", "u1/2026/10/mine.png"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"builtin/characters/asset-002.jpg": "asset-002"}) {
		t.Fatalf("got %v", got)
	}
	empty, err := repo.PublicAssetIDs(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空输入应返回空 map: %v %v", empty, err)
	}
}
