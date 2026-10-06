package generation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrepareTempDir_RemovesOnlyStaleFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gen")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "in-stale.bin")
	fresh := filepath.Join(root, "in-fresh.bin")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := PrepareTempDir(root, time.Hour); err != nil {
		t.Fatalf("PrepareTempDir: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("过期残骸应被清掉: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("新鲜文件(另一色可能在用)必须保留: %v", err)
	}
}

func TestPrepareTempDir_CreatesMissingDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a", "b", "gen")
	if err := PrepareTempDir(root, time.Hour); err != nil {
		t.Fatalf("PrepareTempDir: %v", err)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Fatalf("目录应被创建: %v", err)
	}
}

func TestServiceTempDir_DefaultsToOSTemp(t *testing.T) {
	s := &Service{}
	if got := s.tempDir(); got != os.TempDir() {
		t.Fatalf("未设置时应回退 os.TempDir: %q", got)
	}
	s.SetTempDir("/x/y")
	if got := s.tempDir(); got != "/x/y" {
		t.Fatalf("SetTempDir 未生效: %q", got)
	}
}
