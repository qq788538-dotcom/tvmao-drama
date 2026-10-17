package assets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MediaProtocol 是我们存进 DB 的伪 URL scheme，用来替代签名 URL：签名延后到 API 响应时再做。
const MediaProtocol = "media://"

// ErrInvalidKey 表示 object key 不合法：可能逃出媒体根目录，或含当前平台不能做文件名的字符。
var ErrInvalidKey = errors.New("invalid object key")

// LocalConfig 配置本地文件存储。
type LocalConfig struct {
	// Root 是媒体根目录，即 <data-dir>/media；不存在会以 0700 创建。
	Root string
	// BaseURL 是 sidecar 本次启动的地址，如 http://127.0.0.1:54321（无尾斜杠）。
	BaseURL string
	// Token 是只放行 GET/HEAD /media/* 的媒体 token，拼进对外 URL 的 ?token=。
	Token string
}

// LocalStorage 把对象存成 <Root>/<key> 的本地文件，实现 Storage 接口。
// DB 里只存 media://<key>，所以数据目录整体搬家后引用仍然有效。
type LocalStorage struct {
	root    string
	baseURL string
	token   string
}

func NewLocalStorage(cfg LocalConfig) (*LocalStorage, error) {
	if cfg.Root == "" {
		return nil, errors.New("local storage root is required")
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建媒体目录: %w", err)
	}
	removeStaleTempFiles(root, time.Now().Add(-staleTempAge))
	return &LocalStorage{root: root, baseURL: strings.TrimRight(cfg.BaseURL, "/"), token: cfg.Token}, nil
}

// staleTempAge 之前的 .tmp-* 视为残留。启动时不会有本进程的写入在进行，留一小时余量
// 只是为了不误删同一数据目录上另一个进程（理论上不该存在）正在写的文件。
const staleTempAge = time.Hour

// removeStaleTempFiles 删掉 root 内（不越出 root、不跟随符号链接）早于 before 的 .tmp-* 普通文件。
// 这些是 writeFileAtomic 写到一半进程被杀留下的，不在任何表里，不清就永久占盘。
func removeStaleTempFiles(root string, before time.Time) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.ModTime().Before(before) {
			return nil
		}
		if err := os.Remove(p); err != nil {
			log.Printf("media: 清理残留临时文件 %s 失败: %v", p, err)
		}
		return nil
	})
}

// Root 返回媒体根目录的绝对路径。
func (s *LocalStorage) Root() string { return s.root }

// Path 把 key 换算成本地绝对路径。key 不合法时返回 ErrInvalidKey——
// 媒体根目录的上一级就是 data.db（里面有 API Key），任何能逃出根目录的 key 都必须拒绝。
func (s *LocalStorage) Path(key string) (string, error) {
	if !ValidKey(key) {
		return "", ErrInvalidKey
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return p, nil
}

// ValidKey 判断 key 能否安全地映射成媒体根目录下的文件：段不能为空、"."、".."，
// 不能以 "." 或空格结尾（Windows 会静默截掉，两个 key 撞到同一个文件），
// 不能含控制字符和 Windows 文件名禁用字符，也不能含 # 和 %：DB 里存的是 media://<原始 key>，
// 不做任何转义，key 里出现这两个字符会和 URL 语义冲突（片段、转义序列）。
func ValidKey(key string) bool {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) || strings.HasPrefix(key, "/") {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`\:*?"<>|#%`, r) {
			return false
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return false
		}
	}
	return true
}

// maxNameBytes 是 key 最后一段（文件名）的上限。多数文件系统单个文件名上限 255 字节，
// 上传路径还会加 9 字节随机前缀，留足余量。
const maxNameBytes = 150

// sanitizeName 把用户给的文件名变成能在 macOS 和 Windows 上落盘、且能通过 ValidKey 的一段。
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|#%`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > maxNameBytes {
		ext := filepath.Ext(out)
		if len(ext) > 16 {
			ext = ""
		}
		stem := out[:len(out)-len(ext)]
		cut := maxNameBytes - len(ext)
		for cut > 0 && !utf8.RuneStart(stem[cut]) {
			cut--
		}
		out = stem[:cut] + ext
	}
	out = strings.TrimRight(out, ". ")
	if out == "" {
		out = "file"
	}
	if isWindowsReservedName(out) {
		out = "_" + out
	}
	return out
}

// isWindowsReservedName 判断文件名在 Windows 上是否是设备名（CON、NUL、COM1 等，带扩展名也算）。
func isWindowsReservedName(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}

func (s *LocalStorage) PutObject(_ context.Context, input PutObjectInput) (StoredObject, error) {
	p, err := s.Path(input.Key)
	if err != nil {
		return StoredObject{}, err
	}
	reader, _ := input.Content()
	n, err := writeFileAtomic(p, reader)
	if err != nil {
		return StoredObject{}, fmt.Errorf("put object: %w", err)
	}
	return StoredObject{URL: MediaProtocol + input.Key, Size: n}, nil
}

// writeFileAtomic 先写同目录的临时文件再 rename：并发读者永远看不到半截文件，
// 同一 key 的重放覆盖写也是原子的。
func writeFileAtomic(dst string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if err == nil {
		// rename 前落盘：否则断电后可能出现文件名是新的、内容却是空的或半截。
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	return n, nil
}

func (s *LocalStorage) DeleteObject(_ context.Context, key string) error {
	p, err := s.Path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LocalStorage) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.Path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// SignedURL 返回 sidecar 的本地媒体 URL。不过期：有效性由本次启动的媒体 token 决定，
// sidecar 重启后端口和 token 都会变，旧 URL 自然失效（ExtractKey 仍能认回 key）。
func (s *LocalStorage) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	if !ValidKey(key) {
		return "", ErrInvalidKey
	}
	if s.baseURL == "" {
		return "", errors.New("local storage base URL not configured")
	}
	return s.baseURL + "/media/" + escapeKey(key) + "?token=" + url.QueryEscape(s.token), nil
}

// SignedThumbURL 返回原图 URL：桌面端不生成缩略图。
func (s *LocalStorage) SignedThumbURL(ctx context.Context, key string, expiry time.Duration, _ int) (string, error) {
	return s.SignedURL(ctx, key, expiry)
}

func escapeKey(key string) string {
	segs := strings.Split(key, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

// localMediaURL 匹配任意端口的 sidecar 媒体 URL。端口不限：上一次启动发出的 URL
// （生成任务入库时存的是签名后的 inputs）在重启后也要能认回 key。
var localMediaURL = regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]{1,5}/media/([^?#]*)`)

// ExtractKey 在输入是 media:// 伪 URL 或 sidecar 自己发出的本地媒体 URL 时返回 (key, true)。
// 解出的 key 不合法（如 ../data.db）一律不认，外部 URL、data: 等原样不认。
//
// 两种形式的编码约定不同，必须与写入方对称：
//   - media://<key> 是 PutObject/CopyObject/Canonicalize 原文拼接的，不做 URL 解码；
//     key 本身不允许含 % 和 #（ValidKey），所以原文拼接始终无歧义。
//   - http://127.0.0.1:<port>/media/<escaped> 由 SignedURL 逐段 PathEscape，这里对应解码。
func (s *LocalStorage) ExtractKey(urlOrKey string) (string, bool) {
	var key string
	switch {
	case strings.HasPrefix(urlOrKey, MediaProtocol):
		key = strings.TrimPrefix(urlOrKey, MediaProtocol)
		if idx := strings.IndexAny(key, "?#"); idx >= 0 {
			key = key[:idx]
		}
	default:
		m := localMediaURL.FindStringSubmatch(urlOrKey)
		if m == nil {
			return "", false
		}
		var err error
		if key, err = url.PathUnescape(m[1]); err != nil {
			return "", false
		}
	}
	if !ValidKey(key) {
		return "", false
	}
	return key, true
}

// StaticKey 构造不含时间成分的确定性 key：同一 (userID, filename) 永远同一个 key，
// 生成产物重放覆盖写同一文件，不产生孤儿。
func (s *LocalStorage) StaticKey(userID int64, filename string) string {
	return fmt.Sprintf("u%d/%s", userID, sanitizeName(filename))
}

// NewKey 为一次新上传构造 key，布局 u<userID>/<年>/<月>/<文件名>。
func (s *LocalStorage) NewKey(userID int64, filename string) string {
	now := time.Now()
	return fmt.Sprintf("u%d/%d/%d/%s", userID, now.Year(), int(now.Month()), sanitizeName(filename))
}

// CopyObject 先试硬链接：同盘瞬间完成、不占双份空间；之后对源的覆盖写走 rename（换 inode）、
// 删除只是减链接数，都不影响副本。跨卷或文件系统不支持硬链接（FAT/exFAT）时退回整文件复制。
func (s *LocalStorage) CopyObject(_ context.Context, srcKey, dstKey, _ string) (StoredObject, error) {
	src, err := s.Path(srcKey)
	if err != nil {
		return StoredObject{}, err
	}
	dst, err := s.Path(dstKey)
	if err != nil {
		return StoredObject{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	if err := os.Link(src, dst); err == nil {
		st, err := os.Stat(dst)
		if err != nil {
			return StoredObject{}, fmt.Errorf("copy object: %w", err)
		}
		return StoredObject{URL: MediaProtocol + dstKey, Size: st.Size()}, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	defer f.Close()
	n, err := writeFileAtomic(dst, f)
	if err != nil {
		return StoredObject{}, fmt.Errorf("copy object: %w", err)
	}
	return StoredObject{URL: MediaProtocol + dstKey, Size: n}, nil
}
