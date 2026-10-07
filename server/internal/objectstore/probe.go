package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProbeResult：Step 取 upload | presign | download | delete；OK 时 Step 为空。
type ProbeResult struct {
	OK      bool   `json:"ok"`
	Step    string `json:"step,omitempty"`
	Message string `json:"message"`
}

// Probe 上传一个小文件 → 预签名 → 用普通 HTTP 下载核对（证明上游能拉到）→ 删除。
func Probe(ctx context.Context, c Client, prefix string, get func(context.Context, string) ([]byte, error)) ProbeResult {
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	key := prefix + "/.miaoju-test-" + hex.EncodeToString(rnd) + ".txt"
	body := []byte("miaoju object storage probe " + hex.EncodeToString(rnd))
	if err := c.Put(ctx, key, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		return ProbeResult{Step: "upload", Message: "上传失败：" + err.Error()}
	}
	defer func() { _ = c.Delete(context.WithoutCancel(ctx), key) }()
	url, err := c.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		return ProbeResult{Step: "presign", Message: "生成签名地址失败：" + err.Error()}
	}
	got, err := get(ctx, url)
	if err != nil {
		return ProbeResult{Step: "download", Message: "通过签名地址下载失败（上游将无法拉取）：" + err.Error()}
	}
	if !bytes.Equal(got, body) {
		return ProbeResult{Step: "download", Message: "下载到的内容与上传的不一致，请检查 Endpoint 是否为公网地址"}
	}
	if err := c.Delete(ctx, key); err != nil {
		return ProbeResult{Step: "delete", Message: "删除测试文件失败（请检查 AccessKey 是否有删除权限）：" + err.Error()}
	}
	return ProbeResult{OK: true, Message: "连接正常：上传、签名、公网下载、删除均成功"}
}

// HTTPGet 是 Probe 默认的下载实现。
func HTTPGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Probe 用给定配置（可未保存）做连接测试。错误信息中的密钥原文会被替换。
func (p *Publisher) Probe(ctx context.Context, c Config) ProbeResult {
	n, err := c.Normalize()
	if err != nil {
		return ProbeResult{Step: "config", Message: err.Error()}
	}
	cl, err := p.factory(n)
	if err != nil {
		return ProbeResult{Step: "config", Message: redact(err.Error(), n)}
	}
	r := Probe(ctx, cl, n.Prefix, HTTPGet)
	r.Message = redact(r.Message, n)
	return r
}
