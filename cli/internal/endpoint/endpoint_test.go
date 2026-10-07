package endpoint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDirPerOS(t *testing.T) {
	home := func() (string, error) { return "/Users/u", nil }
	env := func(k string) string {
		if k == "APPDATA" {
			return `C:\Users\u\AppData\Roaming`
		}
		return ""
	}
	if d, _ := defaultDir("darwin", home, env); d != filepath.Join("/Users/u", "Library", "Application Support", "com.miaoju.desktop") {
		t.Fatalf("darwin=%q", d)
	}
	if d, _ := defaultDir("windows", home, env); d != filepath.Join(`C:\Users\u\AppData\Roaming`, "com.miaoju.desktop") {
		t.Fatalf("windows=%q", d)
	}
	if _, err := defaultDir("linux", home, env); err == nil {
		t.Fatal("linux 应报不支持")
	}
	if _, err := defaultDir("windows", home, func(string) string { return "" }); err == nil {
		t.Fatal("缺 APPDATA 应报错")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("文件不存在应为 ErrNotRunning，got %v", err)
	}
	write := func(s string) { _ = os.WriteFile(filepath.Join(dir, FileName), []byte(s), 0o600) }

	write(`{"version":1,"port":52817,"token":"abc","appVersion":"1.2.0"}`)
	i, err := Load(dir)
	if err != nil || i.Port != 52817 || i.Token != "abc" || i.AppVersion != "1.2.0" {
		t.Fatalf("i=%+v err=%v", i, err)
	}
	if i.BaseURL() != "http://127.0.0.1:52817" {
		t.Fatalf("BaseURL=%q", i.BaseURL())
	}
	for _, bad := range []string{`{`, `{"version":2,"port":1,"token":"a"}`, `{"version":1,"port":0,"token":"a"}`, `{"version":1,"port":1,"token":""}`} {
		write(bad)
		if _, err := Load(dir); err == nil || errors.Is(err, ErrNotRunning) {
			t.Fatalf("%s 应报格式错误，got %v", bad, err)
		}
	}
}
