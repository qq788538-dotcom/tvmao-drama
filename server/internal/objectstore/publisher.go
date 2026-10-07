package objectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// PresignTTL：交给上游的地址有效期（沿用 generation.inputMediaURLExpiry）。
const PresignTTL = 24 * time.Hour

// ObjectReader：本地存储（*assets.LocalStorage 满足）。
type ObjectReader interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

// Publisher 把本机媒体发布成上游能下载的地址。每次调用现读配置，保存后立即生效。
type Publisher struct {
	settings SettingsStore
	objects  ObjectReader
	factory  ClientFactory

	mu       sync.Mutex
	client   Client
	clientOf Config
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	url  string
	err  error
}

func NewPublisher(s SettingsStore, objects ObjectReader, factory ClientFactory) *Publisher {
	if factory == nil {
		factory = NewClient
	}
	return &Publisher{settings: s, objects: objects, factory: factory, inflight: map[string]*call{}}
}

func (p *Publisher) Configured() bool {
	_, ok, err := Load(p.settings)
	return err == nil && ok
}

func (p *Publisher) clientFor(c Config) (Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.clientOf == c {
		return p.client, nil
	}
	cl, err := p.factory(c)
	if err != nil {
		return nil, err
	}
	p.client, p.clientOf = cl, c
	return cl, nil
}

// Publish：读本地对象 → sha256 → 已存在则跳过上传 → 预签名。同一 mediaKey 并发调用合并为一次。
func (p *Publisher) Publish(ctx context.Context, mediaKey string) (string, error) {
	c, ok, err := Load(p.settings)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotConfigured
	}
	p.mu.Lock()
	cl, ok := p.inflight[mediaKey]
	if !ok {
		cl = &call{done: make(chan struct{})}
		p.inflight[mediaKey] = cl
		// 共享上传脱离任何单个调用方的 ctx：某个调用方放弃不会取消其他等待者。
		go p.run(context.WithoutCancel(ctx), cl, c, mediaKey)
	}
	p.mu.Unlock()

	// 所有调用方（含发起者）都只等结果或自己的 ctx。
	select {
	case <-cl.done:
		return cl.url, cl.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// run 执行共享上传。defer 顺序：先写结果（含 panic 转错误）、再摘除 inflight、最后 close(done)，
// 保证等待者看到的结果已就绪且该 key 不会被永久卡死。
func (p *Publisher) run(ctx context.Context, cl *call, c Config, mediaKey string) {
	defer func() {
		if r := recover(); r != nil {
			// panic 内容不透出细节，避免泄露密钥
			cl.url, cl.err = "", fmt.Errorf("上传到对象存储失败：内部错误 %v", r)
		}
		p.mu.Lock()
		delete(p.inflight, mediaKey)
		p.mu.Unlock()
		close(cl.done)
	}()
	cl.url, cl.err = p.publish(ctx, c, mediaKey)
	if cl.err != nil {
		cl.err = fmt.Errorf("上传到对象存储失败：%s", redact(cl.err.Error(), c))
	}
}

func (p *Publisher) publish(ctx context.Context, c Config, mediaKey string) (string, error) {
	client, err := p.clientFor(c)
	if err != nil {
		return "", err
	}
	// 第一遍：流式算 sha256 并嗅探类型；第二遍：重新打开上传。内存不随文件大小增长。
	rc, err := p.objects.GetObject(ctx, mediaKey)
	if err != nil {
		return "", fmt.Errorf("读取本机文件: %w", err)
	}
	h := sha256.New()
	head := make([]byte, 512)
	n, _ := io.ReadFull(rc, head)
	h.Write(head[:n])
	rest, err := io.Copy(h, rc)
	rc.Close()
	if err != nil {
		return "", fmt.Errorf("读取本机文件: %w", err)
	}
	size := int64(n) + rest
	ext := strings.ToLower(path.Ext(mediaKey))
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = http.DetectContentType(head[:n])
	}
	key := c.Prefix + "/" + hex.EncodeToString(h.Sum(nil)) + ext

	exists, err := client.Exists(ctx, key)
	if err != nil {
		return "", err
	}
	if !exists {
		rc, err := p.objects.GetObject(ctx, mediaKey)
		if err != nil {
			return "", fmt.Errorf("读取本机文件: %w", err)
		}
		err = client.Put(ctx, key, rc, size, ctype)
		rc.Close()
		if err != nil {
			return "", err
		}
	}
	return client.PresignGet(ctx, key, PresignTTL)
}

// redact 把错误信息里的密钥原文替换掉（SDK 错误偶尔会带上请求头）。
func redact(msg string, c Config) string {
	for _, s := range []string{c.SecretKey, c.AccessKey} {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return msg
}
