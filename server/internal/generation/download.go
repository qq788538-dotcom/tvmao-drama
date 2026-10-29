package generation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// 产物下载：http(s) 产物边下边写临时文件，不再整份读进内存（P2）。单文件有上限，
// 全进程有下载并发闸（首搬、孤儿续跑、手动补搬共用），上游返回异常大的文件或一批
// 大视频同时出片时，内存与磁盘占用都有界。

const (
	// defaultMaxImageOutputBytes 图片产物上限。实测生成图最大十几 MB，50MB 足够宽裕。
	defaultMaxImageOutputBytes int64 = 50 << 20
	// defaultMaxMediaOutputBytes 音视频（以及类型未知）产物上限。
	defaultMaxMediaOutputBytes int64 = 1 << 30
	// defaultDownloadConcurrency 全进程同时落盘下载的产物数。
	defaultDownloadConcurrency = 4
	// downloadFilePrefix 与 spill 的 "out-" 前缀区分：下载临时文件绝不能被
	// resolveSpillOutputPath 当成上游 file:// 产物接受。
	downloadFilePrefix = "dl-"
)

// errOutputTooLarge：产物超过单文件上限。确定性错误，不重试——重试只会把同一个
// 超大文件再完整下载一遍。
var errOutputTooLarge = errors.New("产物超过大小上限")

// fetchStatusError 是下载产物时上游返回的 HTTP 错误状态，供重试分类使用。
type fetchStatusError struct {
	Status int
}

func (e *fetchStatusError) Error() string {
	return fmt.Sprintf("下载产物失败：上游返回 HTTP %d", e.Status)
}

// retryable：408/429/5xx 是瞬时的；其余 4xx（链接过期、无权限、不存在）重试也拿不到。
func (e *fetchStatusError) retryable() bool {
	return e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// isRetryablePersistError 判断一次 http(s) 产物落盘失败是否值得重试。
// 超限和非瞬时 4xx 直接放弃；网络错误、存储写失败等其余错误照旧重试。
func isRetryablePersistError(err error) bool {
	if errors.Is(err, errOutputTooLarge) {
		return false
	}
	var se *fetchStatusError
	if errors.As(err, &se) {
		return se.retryable()
	}
	return true
}

// SetDownloadLimits 注入产物单文件上限（字节）；<=0 的一项用默认值。setter 惯例同 SetTempDir。
func (s *Service) SetDownloadLimits(maxImageBytes, maxMediaBytes int64) {
	s.maxImageOutputBytes = maxImageBytes
	s.maxMediaOutputBytes = maxMediaBytes
}

// SetDownloadConcurrency 注入全局下载并发数（<=0 用默认值）。须在首次下载前调用。
func (s *Service) SetDownloadConcurrency(n int) {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if n <= 0 {
		n = defaultDownloadConcurrency
	}
	s.dlSem = make(chan struct{}, n)
}

func (s *Service) maxImageOutput() int64 {
	if s.maxImageOutputBytes > 0 {
		return s.maxImageOutputBytes
	}
	return defaultMaxImageOutputBytes
}

func (s *Service) maxMediaOutput() int64 {
	if s.maxMediaOutputBytes > 0 {
		return s.maxMediaOutputBytes
	}
	return defaultMaxMediaOutputBytes
}

// outputLimitFor 按 mime 取上限：图片用图片上限，其余（音视频、类型未知）用音视频上限。
func (s *Service) outputLimitFor(mime string) int64 {
	if strings.HasPrefix(mime, "image/") {
		return s.maxImageOutput()
	}
	return s.maxMediaOutput()
}

// downloadSemaphore 懒建：单测直接 &Service{} 构造，不经 NewService。
func (s *Service) downloadSemaphore() chan struct{} {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()
	if s.dlSem == nil {
		s.dlSem = make(chan struct{}, defaultDownloadConcurrency)
	}
	return s.dlSem
}

// acquireDownload 取一个全局下载名额，ctx 取消/超时即放弃等待。返回的 release 可重复调用。
func (s *Service) acquireDownload(ctx context.Context) (func(), error) {
	sem := s.downloadSemaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-sem }) }, nil
}

// tempDownload 是下载落地的临时文件，Close 时连同文件一起删除。
type tempDownload struct {
	*os.File
}

func (t *tempDownload) Close() error {
	err := t.File.Close()
	_ = os.Remove(t.Name())
	return err
}

// downloadToTemp 把 http(s) 产物流式写进 tempDir 下的临时文件，返回读指针已回到开头的
// ReadCloser（Close 即删文件）、字节数和 mime。
//   - Content-Length 已超限的直接拒绝，不下载；
//   - 实际字节经 LimitReader(max+1) 卡上限，防 Content-Length 缺失或说谎；
//   - Content-Type 不是 image/video/audio 大类（为空、application/octet-stream、对象存储
//     常见的 binary/octet-stream 等）时按前 512 字节嗅探（与 file:// 分支同法），
//     嗅探出图片再按图片上限复核一次。
func (s *Service) downloadToTemp(ctx context.Context, rawURL string) (io.ReadCloser, int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, "", err
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, 0, "", &fetchStatusError{Status: resp.StatusCode}
	}
	mime := resp.Header.Get("Content-Type")
	sniff := needsSniff(mediaTypeOnly(mime))
	limit := s.outputLimitFor(mediaTypeOnly(mime))
	if resp.ContentLength > limit {
		return nil, 0, "", tooLarge(resp.ContentLength, limit)
	}

	// tempDir 未注入时 s.tempDir() 回退系统临时目录；文件由本函数或返回值的 Close 删除。
	f, err := os.CreateTemp(s.tempDir(), downloadFilePrefix+"*.part")
	if err != nil {
		return nil, 0, "", fmt.Errorf("create temp: %w", err)
	}
	fail := func(err error) (io.ReadCloser, int64, string, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, 0, "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fail(err)
	}
	if n > limit {
		return fail(tooLarge(n, limit))
	}
	if sniff {
		head := make([]byte, 512)
		m, _ := f.ReadAt(head, 0)
		mime = mediaTypeOnly(http.DetectContentType(head[:m]))
		if lim := s.outputLimitFor(mime); n > lim {
			return fail(tooLarge(n, lim))
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return &tempDownload{File: f}, n, mime, nil
}

// needsSniff：上游声明的不是媒体大类就不可信，按内容嗅探。只认 application/octet-stream
// 不够——S3 兼容存储给无类型对象回 binary/octet-stream，部分 CDN 回 application/x-download，
// 不嗅探的话视频的 mime / outputKind / DurationMs 照样缺（P28）。
func needsSniff(mediaType string) bool {
	mt := strings.ToLower(mediaType)
	return !strings.HasPrefix(mt, "image/") && !strings.HasPrefix(mt, "video/") && !strings.HasPrefix(mt, "audio/")
}

// tooLarge 生成带大小的超限错误。n 是已知字节数（流式截断时为 limit+1，只说明「超过」）。
func tooLarge(n, limit int64) error {
	if n == limit+1 {
		return fmt.Errorf("%w（%s）", errOutputTooLarge, formatMB(limit))
	}
	return fmt.Errorf("%w（%s，上限 %s）", errOutputTooLarge, formatMB(n), formatMB(limit))
}

func formatMB(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// mediaTypeOnly 去掉 mime 的 ;charset=... 等参数段。
func mediaTypeOnly(mime string) string {
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = mime[:idx]
	}
	return strings.TrimSpace(mime)
}
