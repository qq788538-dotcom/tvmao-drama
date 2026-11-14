package generation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/upstream"
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

// 下载超时不再用整请求时限（P24）：600s 整请求限时让慢链路上的大文件确定性失败，
// 重试又从头开始。改为两段：等响应头有上限（上游挂死在建连/排队），读 body 按「空闲」
// 计时（只要还在来字节就一直等，连续一段时间一个字节都没有才取消）。
//
// 包级变量而非常量：测试置短即可，生产不需要配置。
var (
	downloadHeaderTimeout = 60 * time.Second
	downloadIdleTimeout   = 60 * time.Second
)

// errDownloadIdle：读 body 期间连续 downloadIdleTimeout 没有收到任何字节。按瞬时错误重试。
var errDownloadIdle = errors.New("下载停滞：长时间未收到数据")

// newDownloadHTTPClient 是生成链路下载（产物、参考图）用的 client：没有整请求 Timeout，
// 只在 Transport 上卡响应头等待时长；读 body 的空闲超时由 getWithIdleTimeout 负责。
func newDownloadHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = downloadHeaderTimeout
	return &http.Client{Transport: tr}
}

// idleTimeoutBody 包装响应 body：每读到字节就把空闲计时器拨回 d；计时器触发即取消请求
// ctx（cause=errDownloadIdle），正在阻塞的 Read 随之返回，错误统一换成 errDownloadIdle。
type idleTimeoutBody struct {
	io.ReadCloser
	ctx   context.Context
	timer *time.Timer
	d     time.Duration
	stop  func()
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.d)
	}
	if err != nil && err != io.EOF && errors.Is(context.Cause(b.ctx), errDownloadIdle) {
		err = errDownloadIdle
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	err := b.ReadCloser.Close()
	b.stop()
	return err
}

// getWithIdleTimeout 发一个 GET（hdr 为附加请求头，可为 nil），响应体换成带读空闲超时的
// idleTimeoutBody。调用方照常 defer resp.Body.Close()（Close 同时停计时器、释放派生 ctx）。
func (s *Service) getWithIdleTimeout(ctx context.Context, rawURL string, hdr http.Header) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel(nil)
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel(nil)
		return nil, err
	}
	d := downloadIdleTimeout
	timer := time.AfterFunc(d, func() { cancel(errDownloadIdle) })
	resp.Body = &idleTimeoutBody{
		ReadCloser: resp.Body,
		ctx:        ctx,
		timer:      timer,
		d:          d,
		stop: func() {
			timer.Stop()
			cancel(nil)
		},
	}
	return resp, nil
}

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
	// 资产事务已在 persistSlot 内部有界重试过，项目已删除是确定性的：都不值得把文件再下一遍。
	if errors.Is(err, errOutputTooLarge) || errors.Is(err, errSlotAssetRecord) || errors.Is(err, errProjectDeleted) {
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
	f, n, mime, err := s.downloadFile(ctx, rawURL, nil, downloadFilePrefix+"*.part")
	if err != nil {
		return nil, 0, "", err
	}
	return &tempDownload{File: f}, n, mime, nil
}

// downloadFile 是 downloadToTemp 的本体：hdr 是附加请求头（contentPath 要带上游鉴权），
// pattern 是 CreateTemp 的文件名模式。成功时返回读指针在开头的文件，删除由调用方负责。
func (s *Service) downloadFile(ctx context.Context, rawURL string, hdr http.Header, pattern string) (*os.File, int64, string, error) {
	resp, err := s.getWithIdleTimeout(ctx, rawURL, hdr)
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

	// tempDir 未注入时 s.tempDir() 回退系统临时目录。
	f, err := os.CreateTemp(s.tempDir(), pattern)
	if err != nil {
		return nil, 0, "", fmt.Errorf("create temp: %w", err)
	}
	fail := func(err error) (*os.File, int64, string, error) {
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
	return f, n, mime, nil
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

// contentDownloader 是 pollUpstream 的可选能力：contentPath 产物（上游只给一个要带鉴权的
// 内容地址，没有可直接下载的 URL）流式落临时文件，不再整份读进内存再转 base64 data URL（P34）。
// 生产的 pollClient 实现；测试替身不实现时走老的整读路径。
type contentDownloader interface {
	downloadContent(ctx context.Context, apiKey, path string) (upstream.SpillFile, error)
}

// pollClient 把 upstream.Client 与 Service 的下载能力绑在一起交给 pollUpstream（completeAsync 构造）。
type pollClient struct {
	*upstream.Client
	svc     *Service
	baseURL string
}

// downloadContent 占一个全局下载名额，经 downloadFile 落进 tempDir 的 spill 文件（out- 前缀，
// 与 gpt-image 的 b64 落盘同形，产物以 file:// 交给 persistOutputs）。文件由调用方经
// CallResult.RemoveSpillFiles 删除。
func (c *pollClient) downloadContent(ctx context.Context, apiKey, path string) (upstream.SpillFile, error) {
	release, err := c.svc.acquireDownload(ctx)
	if err != nil {
		return upstream.SpillFile{}, err
	}
	defer release()
	hdr := http.Header{}
	if apiKey != "" {
		hdr.Set("Authorization", "Bearer "+apiKey)
	}
	f, n, _, err := c.svc.downloadFile(ctx, strings.TrimRight(c.baseURL, "/")+path, hdr, spillFilePrefix+"*.bin")
	if err != nil {
		return upstream.SpillFile{}, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return upstream.SpillFile{}, err
	}
	// resolveSpillOutputPath 只认绝对路径；tempDir 注入的是相对路径时在这里补齐。
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	return upstream.SpillFile{Path: name, Size: n}, nil
}
