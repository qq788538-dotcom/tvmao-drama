package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
