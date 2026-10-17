package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var _ Storage = (*LocalStorage)(nil)

// 根目录放在带空格的「Application Support」下，顺带覆盖 macOS 的真实数据目录形态。
func newLocal(t *testing.T) *LocalStorage {
	t.Helper()
	s, err := NewLocalStorage(LocalConfig{
		Root:    filepath.Join(t.TempDir(), "Application Support", "media"),
		BaseURL: "http://127.0.0.1:5555",
		Token:   "mediatoken",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readKey(t *testing.T, s *LocalStorage, key string) string {
	t.Helper()
	rc, err := s.GetObject(context.Background(), key)
	if err != nil {
		t.Fatalf("GetObject(%q): %v", key, err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestNewLocalStorageCreatesPrivateRoot(t *testing.T) {
	if _, err := NewLocalStorage(LocalConfig{}); err == nil {
		t.Fatal("缺 Root 应报错")
	}
	s := newLocal(t)
	st, err := os.Stat(s.Root())
	if err != nil || !st.IsDir() {
		t.Fatalf("root 不存在: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("perm=%v want 0700", st.Mode().Perm())
	}
}

func TestPutGetDeleteRoundTrip(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	obj, err := s.PutObject(ctx, PutObjectInput{Key: "u1/2026/10/a.png", ContentType: "image/png", Body: []byte("png-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "media://u1/2026/10/a.png" || obj.Size != 9 {
		t.Fatalf("obj=%+v", obj)
	}
	if got := readKey(t, s, "u1/2026/10/a.png"); got != "png-bytes" {
		t.Fatalf("got %q", got)
	}
	// Reader+Size 传法（大文件流式）同样可用；生成产物的 key 没有扩展名。
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "gen/task-1-0", Reader: strings.NewReader("video"), Size: 5}); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "gen/task-1-0"); got != "video" {
		t.Fatalf("got %q", got)
	}
	if err := s.DeleteObject(ctx, "u1/2026/10/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetObject(ctx, "u1/2026/10/a.png"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("删除后 GetObject err=%v，want ErrNotExist", err)
	}
	if err := s.DeleteObject(ctx, "u1/2026/10/a.png"); err != nil {
		t.Fatalf("删除不存在的对象应视为成功: %v", err)
	}
}

func TestPutObjectOverwritesWithoutLeavingTempFiles(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	key := s.StaticKey(1, "gen/task-9-0")
	for _, body := range []string{"first", "second"} {
		if _, err := s.PutObject(ctx, PutObjectInput{Key: key, Body: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readKey(t, s, key); got != "second" {
		t.Fatalf("got %q", got)
	}
	p, _ := s.Path(key)
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("残留临时文件 %s", e.Name())
		}
	}
}

func TestNewKeyLayout(t *testing.T) {
	s := newLocal(t)
	now := time.Now()
	key := s.NewKey(7, "镜头 1.png")
	want := fmt.Sprintf("u7/%d/%d/镜头 1.png", now.Year(), int(now.Month()))
	if key != want {
		t.Fatalf("key=%q want %q", key, want)
	}
}

// Windows 文件名不能含 \ : * ? " < > |，用户上传的文件名里很常见。
func TestNewKeySanitizesWindowsIllegalNames(t *testing.T) {
	s := newLocal(t)
	key := s.NewKey(7, `镜头:1?<a>|"b"\c*.png`)
	name := key[strings.LastIndex(key, "/")+1:]
	if name != "镜头_1__a___b__c_.png" {
		t.Fatalf("name=%q", name)
	}
	if !ValidKey(key) {
		t.Fatalf("NewKey 产出了非法 key %q", key)
	}
	if _, err := s.PutObject(context.Background(), PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

// 多数文件系统的单个文件名上限是 255 字节。
func TestNewKeyTruncatesLongNamesKeepingExtension(t *testing.T) {
	s := newLocal(t)
	key := s.NewKey(1, strings.Repeat("长", 200)+".mp4")
	name := key[strings.LastIndex(key, "/")+1:]
	if len(name) > 150 || !strings.HasSuffix(name, ".mp4") || !utf8.ValidString(name) {
		t.Fatalf("name=%q len=%d", name, len(name))
	}
	if _, err := s.PutObject(context.Background(), PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

func TestNewKeyFallsBackForEmptyName(t *testing.T) {
	s := newLocal(t)
	for _, in := range []string{"", ".", "..", " . "} {
		key := s.NewKey(1, in)
		if !ValidKey(key) || !strings.HasSuffix(key, "/file") {
			t.Fatalf("NewKey(%q)=%q", in, key)
		}
	}
}

func TestStaticKeyIsDeterministic(t *testing.T) {
	s := newLocal(t)
	a, b := s.StaticKey(3, "gen/task-12-0"), s.StaticKey(3, "gen/task-12-0")
	if a != b || a != "u3/gen_task-12-0" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

// data.db 就在 media/ 的上一级，里面有 API Key：任何能逃出 root 的 key 都必须被拒。
func TestPathRejectsTraversalAndIllegalKeys(t *testing.T) {
	s := newLocal(t)
	bad := []string{"", "../data.db", "u1/../../data.db", "/etc/passwd", `u1\..\data.db`, "C:/Windows/x",
		"u1//a.png", "u1/./a.png", "u1/a.png/", "a\x00b", "u1/a.", "u1/a ", "u1/\xff.png"}
	for _, k := range bad {
		if p, err := s.Path(k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Path(%q)=%q, %v；want ErrInvalidKey", k, p, err)
		}
		if _, err := s.PutObject(context.Background(), PutObjectInput{Key: k, Body: []byte("x")}); err == nil {
			t.Errorf("PutObject(%q) 应失败", k)
		}
	}
	p, err := s.Path("u1/2026/10/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, s.Root()+string(filepath.Separator)) {
		t.Fatalf("p=%q 不在 root=%q 下", p, s.Root())
	}
}

func TestSignedURLRoundTripsThroughExtractKey(t *testing.T) {
	s := newLocal(t)
	for _, key := range []string{"u1/2026/10/a.png", "u1/2026/10/镜头 1&2+3=4.png", "gen/task-3-0"} {
		u, err := s.SignedURL(context.Background(), key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u, "http://127.0.0.1:5555/media/") || !strings.HasSuffix(u, "?token=mediatoken") {
			t.Fatalf("url=%q", u)
		}
		got, ok := s.ExtractKey(u)
		if !ok || got != key {
			t.Fatalf("ExtractKey(%q)=%q,%v want %q", u, got, ok, key)
		}
		thumb, _ := s.SignedThumbURL(context.Background(), key, time.Hour, 400)
		if thumb != u {
			t.Fatalf("缩略图应等于原图（spec S8）: %q vs %q", thumb, u)
		}
	}
	if _, err := s.SignedURL(context.Background(), "../x", time.Hour); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("非法 key 不应签名: %v", err)
	}
}

// 上一次启动发出的 URL（端口不同）也必须认得：生成任务入库时存的是签名 URL，重启续跑要靠它认回 key。
func TestExtractKeyAcceptsAnyLoopbackPortAndMediaScheme(t *testing.T) {
	s := newLocal(t)
	cases := map[string]string{
		"http://127.0.0.1:61234/media/u1/2026/10/a.png?token=old": "u1/2026/10/a.png",
		"http://127.0.0.1:1/media/gen/task-1-0":                   "gen/task-1-0",
		"media://u1/2026/10/a b.png":                              "u1/2026/10/a b.png",
		"media://u1/x.png?foo=1":                                  "u1/x.png",
		"http://127.0.0.1:5555/media/u1/a%20b.png?token=x":        "u1/a b.png",
	}
	for in, want := range cases {
		got, ok := s.ExtractKey(in)
		if !ok || got != want {
			t.Errorf("ExtractKey(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"https://cdn.example/media/u1/a.png", "http://localhost:5555/media/u1/a.png",
		"http://127.0.0.1:5555/api/v1/ping", "media://../data.db",
		"http://127.0.0.1:5555/media/..%2Fdata.db", "data:image/png;base64,AAAA", "",
		// media:// 存的是原始 key，不做 URL 解码；key 里本来就不允许 %，转义串一律不认。
		"media://u1/2026/10/a%20b.png", "media://u1/..%2Fdata.db", "media://u1/a%41.png",
		// http 分支解码后含 # 或 % 的 key 同样非法。
		"http://127.0.0.1:5555/media/u1/a%25b.png", "http://127.0.0.1:5555/media/u1/a%23b.png",
	} {
		if got, ok := s.ExtractKey(in); ok {
			t.Errorf("ExtractKey(%q)=%q 不应识别", in, got)
		}
	}
}

func TestCopyObjectIsIndependentOfSource(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "u1/a.png", Body: []byte("orig")}); err != nil {
		t.Fatal(err)
	}
	obj, err := s.CopyObject(ctx, "u1/a.png", "u2/2026/10/wf-1.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "media://u2/2026/10/wf-1.png" || obj.Size != 4 {
		t.Fatalf("obj=%+v", obj)
	}
	// 项目副本必须与原项目互不影响：源被覆盖写或删除，副本不变。
	if _, err := s.PutObject(ctx, PutObjectInput{Key: "u1/a.png", Body: []byte("changed")}); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "u2/2026/10/wf-1.png"); got != "orig" {
		t.Fatalf("副本被源的覆盖写改动: %q", got)
	}
	if err := s.DeleteObject(ctx, "u1/a.png"); err != nil {
		t.Fatal(err)
	}
	if got := readKey(t, s, "u2/2026/10/wf-1.png"); got != "orig" {
		t.Fatalf("got %q", got)
	}
	if _, err := s.CopyObject(ctx, "u1/missing.png", "u2/x.png", ""); err == nil {
		t.Fatal("源不存在应报错")
	}
}

// key 里的 # 和 % 会让 media:// 与 ExtractKey 对不上，sanitizeName 必须替换掉，ValidKey 必须拒收。
func TestValidKeyRejectsHashAndPercent(t *testing.T) {
	for _, k := range []string{"u1/a#b.png", "u1/50%off.png", "u1/a%41.png"} {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) 应为 false", k)
		}
	}
}

// 写入返回的 media:// 和签名后的 http URL，都必须能经 ExtractKey 认回同一个 key。
func TestPutObjectURLRoundTripsThroughExtractKey(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()
	cases := map[string]string{
		"镜头#1.png":     "镜头_1.png",
		"50%off.png":   "50_off.png",
		"a%41.png":     "a_41.png",
		"主角立绘.png":     "主角立绘.png",
		"my photo.png": "my photo.png",
	}
	for in, wantName := range cases {
		key := s.NewKey(1, in)
		if name := key[strings.LastIndex(key, "/")+1:]; name != wantName {
			t.Errorf("NewKey(%q) 文件名=%q want %q", in, name, wantName)
		}
		obj, err := s.PutObject(ctx, PutObjectInput{Key: key, Body: []byte("x")})
		if err != nil {
			t.Fatalf("PutObject(%q): %v", key, err)
		}
		if got, ok := s.ExtractKey(obj.URL); !ok || got != key {
			t.Errorf("ExtractKey(%q)=%q,%v want %q", obj.URL, got, ok, key)
		}
		signed, err := s.SignedURL(ctx, key, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := s.ExtractKey(signed); !ok || got != key {
			t.Errorf("ExtractKey(%q)=%q,%v want %q", signed, got, ok, key)
		}
		cp, err := s.CopyObject(ctx, key, key+".copy", "")
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := s.ExtractKey(cp.URL); !ok || got != key+".copy" {
			t.Errorf("CopyObject URL %q 认不回: %q,%v", cp.URL, got, ok)
		}
	}
}

// Windows 上 CON、NUL、COM1 等是设备名（带扩展名也是），不能直接做文件名。
func TestNewKeyEscapesWindowsReservedNames(t *testing.T) {
	s := newLocal(t)
	cases := map[string]string{"con": "_con", "NUL.txt": "_NUL.txt", "com1.png": "_com1.png", "Lpt9": "_Lpt9", "console.png": "console.png", "com10.png": "com10.png"}
	for in, want := range cases {
		key := s.StaticKey(1, in)
		if name := key[strings.LastIndex(key, "/")+1:]; name != want {
			t.Errorf("StaticKey(%q) 文件名=%q want %q", in, name, want)
		}
	}
}

// 指向目录的 key 不是对象：GetObject 应按不存在处理（媒体 handler 据此回 404 而不是 500）。
func TestGetObjectOnDirectoryIsNotExist(t *testing.T) {
	s := newLocal(t)
	if _, err := s.PutObject(context.Background(), PutObjectInput{Key: "u1/2026/a.png", Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetObject(context.Background(), "u1/2026"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err=%v want fs.ErrNotExist", err)
	}
}

// 进程在写入中途被杀会留下 .tmp-*：启动时清理根目录内超过 1 小时的残留，新的（可能正在写）不动。
func TestNewLocalStorageRemovesStaleTempFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	sub := filepath.Join(root, "u1", "2026")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	write := func(p string, stale bool) {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if stale {
			os.Chtimes(p, old, old)
		}
	}
	staleTmp := filepath.Join(sub, ".tmp-123")
	freshTmp := filepath.Join(sub, ".tmp-456")
	normal := filepath.Join(sub, "a.png")
	outside := filepath.Join(base, ".tmp-outside")
	write(staleTmp, true)
	write(freshTmp, false)
	write(normal, true)
	write(outside, true)

	if _, err := NewLocalStorage(LocalConfig{Root: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleTmp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("过期临时文件应被删除: %v", err)
	}
	for _, p := range []string{freshTmp, normal, outside} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s 不应被删除: %v", p, err)
		}
	}
}
