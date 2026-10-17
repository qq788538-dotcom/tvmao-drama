package aicharacters

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
)

// 角色形象图内置在 sidecar 里（512px WebP，约 27MB）：不放对象存储，浏览和使用都不产生外网流量。
// 原图（平均 329KB，共 556MB）不内置也不在线拉取——Seedance 生成走 asset://<asset_id>，
// 用不到像素；本地这份只供画布展示和给非 Seedance 模型当参考图。
// 文件名 = object key 的 basename（种子 image_url 形如 media://<prefix>/<asset_id>.webp）。
// 形象图不随开源仓库分发（.gitignore 已排除），没有数据包时目录里只有 README.md。
//
//go:embed images
var embeddedImages embed.FS

// EmbeddedImages 返回内置形象图目录（根下直接是 <asset_id>.webp），供 sidecar 装配。
func EmbeddedImages() fs.FS {
	sub, err := fs.Sub(embeddedImages, "images")
	if err != nil {
		panic(err) // 目录名写死在 go:embed 上，失败只可能是编译期就该发现的错。
	}
	return sub
}

// ObjectStore 是本地媒体存储里缓存要用的两个动作，由 assets.Storage 适配。
type ObjectStore interface {
	Exists(ctx context.Context, key string) bool
	Put(ctx context.Context, key, contentType string, body []byte) error
}

// ImageCache 把内置的角色形象图按需复制进本地媒体目录，之后走 /media/* 的统一读取路径
// （签名、Range、合规兜底 PublicAssetIDs、生成时 asset:// 替换都不用为它开特例）。
// 只写对象、不写 assets 行：公共图不进用户配额、不进孤儿回收（见包注释）。
// 同一张图被并发请求时可能复制两次，Put 覆盖写同一个 key，结果一致，不做去重。
type ImageCache struct {
	repo  Repository
	src   fs.FS
	store ObjectStore
}

func NewImageCache(repo Repository, src fs.FS, store ObjectStore) *ImageCache {
	return &ImageCache{repo: repo, src: src, store: store}
}

// Ensure 保证角色形象图在本地：本地已有或 key 不是角色图 → (false, nil)，调用方按原逻辑处理；
// 是角色图且本地没有 → 从内置资源复制落盘，返回 (true, err)。
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
	body, err := fs.ReadFile(c.src, path.Base(key))
	if err != nil {
		return true, fmt.Errorf("内置角色形象图缺失 %s: %w", key, err)
	}
	contentType := mime.TypeByExtension(path.Ext(key))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return true, c.store.Put(ctx, key, contentType, body)
}

// Middleware 包在 GET/HEAD /media/* 外面：请求的是角色形象图且本地还没有时，
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
