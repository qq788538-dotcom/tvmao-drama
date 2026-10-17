package skillruntime

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWorkspaceCleanerDeletesOnlyExpiredDateDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"20260708", "20260709", "20260710", "20260711", "20260712", "20260713", "notes"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(root, "keep.txt"), "keep")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "20260707")); err != nil {
		t.Fatal(err)
	}

	cleaner := NewWorkspaceCleaner(root)
	now := time.Date(2026, 7, 13, 3, 0, 0, 0, time.Local)
	if err := cleaner.Cleanup(now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20260708", "20260709"} {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired directory %s still exists: %v", name, err)
		}
	}
	for _, name := range []string{"20260710", "20260711", "20260712", "20260713", "notes", "keep.txt", "20260707"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("retained entry %s missing: %v", name, err)
		}
	}
}

func TestWorkspaceCleanerContinuesAfterDeleteError(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"20260708", "20260709"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cleaner := NewWorkspaceCleaner(root)
	var attempted []string
	cleaner.removeAll = func(path string) error {
		attempted = append(attempted, filepath.Base(path))
		if filepath.Base(path) == "20260708" {
			return errors.New("busy")
		}
		return os.RemoveAll(path)
	}
	err := cleaner.Cleanup(time.Date(2026, 7, 13, 3, 0, 0, 0, time.Local))
	if err == nil {
		t.Fatal("expected joined cleanup error")
	}
	if !reflect.DeepEqual(attempted, []string{"20260708", "20260709"}) {
		t.Fatalf("unexpected delete attempts: %#v", attempted)
	}
}

func TestNextWorkspaceCleanup(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 7, 10, 2, 0, 0, 0, loc), time.Date(2026, 7, 10, 3, 0, 0, 0, loc)},
		{time.Date(2026, 7, 10, 3, 0, 0, 0, loc), time.Date(2026, 7, 11, 3, 0, 0, 0, loc)},
		{time.Date(2026, 7, 10, 18, 0, 0, 0, loc), time.Date(2026, 7, 11, 3, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		if got := nextWorkspaceCleanup(tc.now); !got.Equal(tc.want) {
			t.Fatalf("next cleanup from %v = %v, want %v", tc.now, got, tc.want)
		}
	}
}
