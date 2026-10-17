package db_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/db"
)

// uriRecorder 是只记录 SQLite 交给 VFS 的文件名、然后拒绝打开的 VFS：
// 用真实的 SQLite URI 解析验证 pathToURI 的输出能还原成预期路径。
type uriRecorder struct {
	mu    sync.Mutex
	names []string
}

var errRecorded = errors.New("recorded")

func (r *uriRecorder) FullPathname(name string) (string, error) {
	r.mu.Lock()
	r.names = append(r.names, name)
	r.mu.Unlock()
	return "", errRecorded
}
func (r *uriRecorder) Open(string, vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	return nil, 0, errRecorded
}
func (r *uriRecorder) Delete(string, bool) error                   { return errRecorded }
func (r *uriRecorder) Access(string, vfs.AccessFlag) (bool, error) { return false, nil }

var recorder = &uriRecorder{}

func init() { vfs.Register("uri-recorder", recorder) }

// sqliteParsedPath 把 uri 交给真实的 SQLite 解析，返回它交给 VFS 的路径。
func sqliteParsedPath(t *testing.T, uri string) string {
	t.Helper()
	recorder.mu.Lock()
	recorder.names = nil
	recorder.mu.Unlock()
	conn, err := sqlite3.Open(uri + "?vfs=uri-recorder")
	if err == nil {
		conn.Close()
		t.Fatalf("%s：记录用 VFS 不应打开成功", uri)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.names) == 0 {
		t.Fatalf("%s：SQLite 没有调用 VFS（URI 解析失败？）：%v", uri, err)
	}
	return recorder.names[0]
}

func TestPathToURI(t *testing.T) {
	cases := []struct {
		name, goos, path, wantURI, wantParsed string
	}{
		{"win 中文用户名", "windows", `C:\Users\张三\AppData\Roaming\com.miaoju.desktop\data.db`,
			"file:C:/Users/%E5%BC%A0%E4%B8%89/AppData/Roaming/com.miaoju.desktop/data.db",
			"C:/Users/张三/AppData/Roaming/com.miaoju.desktop/data.db"},
		{"win 空格 # %", "windows", `D:\My Data\喵 剧#100%\data.db`,
			"file:D:/My%20Data/%E5%96%B5%20%E5%89%A7%23100%25/data.db",
			"D:/My Data/喵 剧#100%/data.db"},
		{"win 问号", "windows", `C:\a?b\data.db`,
			"file:C:/a%3Fb/data.db", "C:/a?b/data.db"},
		{"win 小写盘符 混合分隔符", "windows", `c:/Users/foo\data.db`,
			"file:c:/Users/foo/data.db", "c:/Users/foo/data.db"},
		{"win UNC", "windows", `\\server\share\喵剧 数据\data.db`,
			"file:////server/share/%E5%96%B5%E5%89%A7%20%E6%95%B0%E6%8D%AE/data.db",
			"//server/share/喵剧 数据/data.db"},
		{"win UNC # % ?", "windows", `\\nas\共享 盘\a#b%c?d\data.db`,
			"file:////nas/%E5%85%B1%E4%BA%AB%20%E7%9B%98/a%23b%25c%3Fd/data.db",
			"//nas/共享 盘/a#b%c?d/data.db"},
		{"unix 中文 空格 # %", "darwin", "/Users/张三/Library/Application Support/喵 剧#100%/data.db",
			"file:///Users/%E5%BC%A0%E4%B8%89/Library/Application%20Support/%E5%96%B5%20%E5%89%A7%23100%25/data.db",
			"/Users/张三/Library/Application Support/喵 剧#100%/data.db"},
		{"unix 问号", "linux", "/tmp/a?b/data.db", "file:///tmp/a%3Fb/data.db", "/tmp/a?b/data.db"},
		{"unix 反斜杠是普通字符", "darwin", `/tmp/a\b/data.db`, "file:///tmp/a%5Cb/data.db", `/tmp/a\b/data.db`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := db.PathToURI(c.path, c.goos)
			if got != c.wantURI {
				t.Fatalf("URI = %s，期望 %s", got, c.wantURI)
			}
			// Windows 盘符路径前不能有 "/"（file:///C:/... 会让 VFS 拿到 /C:/...）。
			if rest, ok := strings.CutPrefix(got, "file:///"); ok && c.goos == "windows" && len(rest) > 1 && rest[1] == ':' {
				t.Fatalf("Windows 盘符路径不能生成 file:///C:/...：%s", got)
			}
			parsed := sqliteParsedPath(t, got)
			if parsed != c.wantParsed {
				t.Fatalf("SQLite 解析出 %q，期望 %q", parsed, c.wantParsed)
			}
			// 在 Windows 上跑时，再确认 Go 的 VFS（filepath.Abs）认得 SQLite 交过去的路径。
			if runtime.GOOS == "windows" && c.goos == "windows" && !filepath.IsAbs(parsed) {
				t.Fatalf("Windows 上 %q 不是绝对路径，VFS 会打不开", parsed)
			}
			t.Logf("%-8s %s\n    -> %s\n    -> %s", c.goos, c.path, got, parsed)
		})
	}
}
