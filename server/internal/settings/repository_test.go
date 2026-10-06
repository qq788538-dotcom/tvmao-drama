package settings

import (
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestGetMissingKeyReturnsEmpty(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	v, err := repo.Get("base_domain")
	if err != nil || v != "" {
		t.Fatalf("got %q, %v; want empty, nil", v, err)
	}
}

func TestSetInsertsThenOverwrites(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	if err := repo.Set("base_domain", "https://a.example"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Set("base_domain", "https://b.example"); err != nil {
		t.Fatal(err)
	}
	v, err := repo.Get("base_domain")
	if err != nil || v != "https://b.example" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestAllReturnsEveryKey(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	_ = repo.Set("a", "1")
	_ = repo.Set("b", `{"x":2}`)
	all, err := repo.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all["a"] != "1" || all["b"] != `{"x":2}` {
		t.Fatalf("all=%v", all)
	}
}

// 后台配置页保存的 JSON 配置（如 canvas_postprocess_config）可能很大；TEXT 无长度限制。
func TestSetLargeValue(t *testing.T) {
	repo := NewSQLiteRepository(dbtest.Open(t))
	big := make([]byte, 200_000)
	for i := range big {
		big[i] = 'x'
	}
	if err := repo.Set("big", string(big)); err != nil {
		t.Fatal(err)
	}
	v, _ := repo.Get("big")
	if len(v) != len(big) {
		t.Fatalf("len=%d want %d", len(v), len(big))
	}
}
