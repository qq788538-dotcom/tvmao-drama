package aicharacters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// SettingImageBaseURL 存角色形象图的在线地址前缀（spec F7「图片地址写进配置」）。
// 图片地址 = 前缀 + "/" + object_key（object_key 即 image_url 去掉 media:// 的部分，
// 如 tapnow/sd-assets/asset-xxx.jpg）。空串 = 未配置。
const SettingImageBaseURL = "ai_character_image_base_url"

// ErrImageBaseNotConfigured：要用到形象图但地址前缀没配。
var ErrImageBaseNotConfigured = errors.New("AI 角色形象图地址未配置")

// maxImageBytes 单张形象图下载上限。包级变量只为测试能调小。
var maxImageBytes int64 = 20 << 20

// ObjectStore 是本地媒体存储里缓存要用的两个动作。Task 8 用 P1b 的 assets.Storage 适配。
type ObjectStore interface {
	Exists(ctx context.Context, key string) bool
	Put(ctx context.Context, key, contentType string, body []byte) error
}

// ImageCache 把角色形象图按需下载进本地媒体目录（spec F7「在线加载并按需缓存」）。
// 只写对象、不写 assets 行：公共图不进用户配额、不进孤儿回收（见包注释）。
// 同一张图被并发请求时可能下载两次，Put 覆盖写同一个 key，结果一致，不做去重。
type ImageCache struct {
	repo  Repository
	get   func(key string) (string, error)
	store ObjectStore
	http  *http.Client
}

func NewImageCache(repo Repository, get func(string) (string, error), store ObjectStore) *ImageCache {
	return &ImageCache{repo: repo, get: get, store: store, http: &http.Client{Timeout: 60 * time.Second}}
}

// Ensure 保证角色形象图在本地：本地已有或 key 不是角色图 → (false, nil)，调用方按原逻辑处理；
// 是角色图且本地没有 → 下载落盘，返回 (true, err)。
func (c *ImageCache) Ensure(ctx context.Context, key string) (bool, error) {
	if key == "" || c.store.Exists(ctx, key) {
		return false, nil
	}
	ids, err := c.repo.PublicAssetIDs([]string{key})
	if err != nil {
		return false, err
	}
	if ids[key] == "" {
		return false, nil
	}
	base, err := c.get(SettingImageBaseURL)
	if err != nil {
		return true, err
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return true, ErrImageBaseNotConfigured
	}
	body, contentType, err := c.download(ctx, base+"/"+escapeKeyPath(key))
	if err != nil {
		return true, err
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(key))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return true, c.store.Put(ctx, key, contentType, body)
}

// escapeKeyPath 逐段转义 object key，保留 "/" 分隔。
func escapeKeyPath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// download 拉全量字节（有上限）；Content-Type 只认 image/*，其余返回空串由调用方按扩展名兜底。
func (c *ImageCache) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("下载角色形象图失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载角色形象图失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("下载角色形象图失败: %w", err)
	}
	if int64(len(body)) > maxImageBytes {
		return nil, "", fmt.Errorf("角色形象图超过 %d 字节上限", maxImageBytes)
	}
	contentType := ""
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && strings.HasPrefix(mt, "image/") {
		contentType = mt
	}
	return body, contentType, nil
}

// Middleware 包在 P1b 的 GET/HEAD /media/* 外面：请求的是角色形象图且本地还没有时，
// 先下载缓存再交给原 handler。下载失败只记日志，原 handler 照常返回 404。
func (c *ImageCache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if key, ok := strings.CutPrefix(r.URL.Path, "/media/"); ok && key != "" {
				if handled, err := c.Ensure(r.Context(), key); handled && err != nil {
					log.Printf("aicharacters: 缓存形象图 %s 失败: %v", key, err)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
