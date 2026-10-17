package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// UploadAsset 上传本地文件。
//
// 用更长的超时：30 秒传不完一个视频。上传**不带幂等键也不重试**——
// 重传一个大文件的代价远高于让用户自己重来，而服务端本来就按内容哈希去重。
func (c *Client) UploadAsset(ctx context.Context, projectID int64, path string) (Asset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Asset{}, err
	}
	defer f.Close()
	// 文件直接流进 multipart 缓冲区：先 ReadFile 再写进 buffer 会在内存里同时放两份完整文件
	// （几百 MB 的视频就是翻倍的内存）。body 仍整体缓冲，因为重连后要能原样重发。
	var buf bytes.Buffer
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		buf.Grow(int(fi.Size()) + 1024)
	}
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return Asset{}, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return Asset{}, err
	}
	if err := mw.Close(); err != nil {
		return Asset{}, err
	}

	var out Asset
	err = c.do(ctx, request{
		method: http.MethodPost, path: "/projects/" + itoa(projectID) + "/assets",
		rawBody: buf.Bytes(), contentType: mw.FormDataContentType(), longTimeout: true,
	}, &out)
	return out, err
}

func (c *Client) GetAsset(ctx context.Context, assetID int64) (Asset, error) {
	var out Asset
	err := c.do(ctx, request{method: http.MethodGet, path: "/assets/" + itoa(assetID)}, &out)
	return out, err
}

// DownloadURL 把已签名的 URL 内容写到 w。
//
// 单独走一个裸 http 请求而不是 c.do：签名 URL 指向对象存储，不是 CLI API，
// 不该带 Authorization 头（带了反而可能被某些网关拒）。
func (c *Client) DownloadURL(ctx context.Context, signedURL string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	hc := *c.hc
	hc.Timeout = uploadTimeout
	resp, err := hc.Do(req)
	if err != nil {
		return 0, classifyNetErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, &APIError{
			Status: resp.StatusCode, Code: "UPSTREAM_UNAVAILABLE",
			Message: fmt.Sprintf("下载失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}
	return io.Copy(w, resp.Body)
}
