package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/james/huabu_studio/apps/api/internal/datadir"
)

func TestRunSubcommandMigrateAndCleanup(t *testing.T) {
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	os.MkdirAll(filepath.Join(from, "media"), 0o700)
	os.WriteFile(filepath.Join(from, "media", "a.png"), []byte("PNG"), 0o600)
	var out bytes.Buffer
	handled, err := runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to}, &out)
	if !handled || err != nil || strings.TrimSpace(out.String()) != "MIGRATED" {
		t.Fatalf("handled=%v err=%v out=%q", handled, err, out.String())
	}
	// 没有 data.db 的源目录迁移后新目录也没有 data.db，cleanup 必须拒绝
	out.Reset()
	handled, err = runSubcommand([]string{"cleanup-data-dir", "--old", from, "--new", to}, &out)
	if !handled || err == nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestRunSubcommandArgs(t *testing.T) {
	var out bytes.Buffer
	if handled, err := runSubcommand([]string{"--data-dir", "/x"}, &out); handled || err != nil {
		t.Fatal("非子命令应交给正常启动流程")
	}
	if handled, err := runSubcommand(nil, &out); handled || err != nil {
		t.Fatal("无参数应交给正常启动流程")
	}
	if handled, err := runSubcommand([]string{"migrate-data-dir", "--from", "/a"}, &out); !handled || err == nil {
		t.Fatal("缺 --to 应报错")
	}
}

// 另一个迁移进程持锁时，子命令不得开始复制：等待超时后报错，目标目录保持未创建。
func TestRunSubcommandRespectsMigrateLock(t *testing.T) {
	old := migrateLockWait
	migrateLockWait = 100 * time.Millisecond
	defer func() { migrateLockWait = old }()

	lockPath := filepath.Join(t.TempDir(), datadir.MigrateLockFileName)
	release, err := datadir.AcquireMigrateLock(lockPath, 0, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	from, to := t.TempDir(), filepath.Join(t.TempDir(), "dest")
	os.WriteFile(filepath.Join(from, "a.txt"), []byte("x"), 0o600)
	var out bytes.Buffer
	handled, err := runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to, "--lock", lockPath}, &out)
	if !handled || err == nil || !strings.Contains(err.Error(), "另一个数据目录迁移仍在进行") {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if _, err := os.Stat(filepath.Join(to, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("拿不到锁时不应开始复制")
	}
	handled, err = runSubcommand([]string{"cleanup-data-dir", "--old", from, "--new", to, "--lock", lockPath}, &out)
	if !handled || err == nil || !strings.Contains(err.Error(), "另一个数据目录迁移仍在进行") {
		t.Fatalf("cleanup 也应受锁约束：handled=%v err=%v", handled, err)
	}

	release()
	out.Reset()
	handled, err = runSubcommand([]string{"migrate-data-dir", "--from", from, "--to", to, "--lock", lockPath}, &out)
	if !handled || err != nil || strings.TrimSpace(out.String()) != "MIGRATED" {
		t.Fatalf("释放后应能迁移：handled=%v err=%v out=%q", handled, err, out.String())
	}
}
