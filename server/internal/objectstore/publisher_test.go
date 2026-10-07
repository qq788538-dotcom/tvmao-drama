package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClient struct {
	mu        sync.Mutex
	objects   map[string][]byte
	puts      atomic.Int32
	putErr    error
	delay     time.Duration
	panicMsg  string
	panicOnce atomic.Bool // 置位后下一次 Put 触发 panic（只一次）
}

func newFake() *fakeClient { return &fakeClient{objects: map[string][]byte{}} }

func (f *fakeClient) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok, nil
}
func (f *fakeClient) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	time.Sleep(f.delay)
	if f.panicOnce.CompareAndSwap(true, false) {
		panic("boom " + f.panicMsg)
	}
	if f.putErr != nil {
		return f.putErr
	}
	b, _ := io.ReadAll(r)
	f.mu.Lock()
	f.objects[key] = b
	f.mu.Unlock()
	f.puts.Add(1)
	return nil
}
func (f *fakeClient) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	return "https://bucket.example.com/" + key + "?ttl=" + ttl.String(), nil
}
func (f *fakeClient) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	delete(f.objects, key)
	f.mu.Unlock()
	return nil
}

type memObjects map[string][]byte

func (m memObjects) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func setup(t *testing.T, fc *fakeClient) (*Publisher, memSettings, *atomic.Int32) {
	t.Helper()
	s := memSettings{}
	if err := Save(s, validCfg()); err != nil {
		t.Fatal(err)
	}
	builds := &atomic.Int32{}
	p := NewPublisher(s, memObjects{"u1/2026/10/v.mp4": []byte("video-bytes")}, func(Config) (Client, error) {
		builds.Add(1)
		return fc, nil
	})
	return p, s, builds
}

func TestPublisherUploadsByHashAndPresigns(t *testing.T) {
	fc := newFake()
	p, _, _ := setup(t, fc)
	url, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("video-bytes"))
	want := "miaoju-inputs/" + hex.EncodeToString(sum[:]) + ".mp4"
	if !strings.Contains(url, want) || !strings.Contains(url, "ttl=24h0m0s") {
		t.Fatalf("url=%s want key %s", url, want)
	}
	if _, err := p.Publish(context.Background(), "u1/2026/10/v.mp4"); err != nil {
		t.Fatal(err)
	}
	if fc.puts.Load() != 1 {
		t.Fatalf("已存在应跳过上传, puts=%d", fc.puts.Load())
	}
}

func TestPublisherConcurrentSameKeyUploadsOnce(t *testing.T) {
	fc := newFake()
	fc.delay = 50 * time.Millisecond
	p, _, _ := setup(t, fc)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Publish(context.Background(), "u1/2026/10/v.mp4"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if fc.puts.Load() != 1 {
		t.Fatalf("并发同 key 应只传一次, puts=%d", fc.puts.Load())
	}
}

func TestPublisherNotConfigured(t *testing.T) {
	p := NewPublisher(memSettings{}, memObjects{}, func(Config) (Client, error) { return newFake(), nil })
	if p.Configured() {
		t.Fatal("空配置应未配置")
	}
	if _, err := p.Publish(context.Background(), "x.mp4"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestPublisherRebuildsClientWhenConfigChanges(t *testing.T) {
	fc := newFake()
	p, s, builds := setup(t, fc)
	ctx := context.Background()
	p.Publish(ctx, "u1/2026/10/v.mp4")
	p.Publish(ctx, "u1/2026/10/v.mp4")
	if builds.Load() != 1 {
		t.Fatalf("配置不变应复用 client, builds=%d", builds.Load())
	}
	c := validCfg()
	c.Bucket = "b2"
	Save(s, c)
	p.Publish(ctx, "u1/2026/10/v.mp4")
	if builds.Load() != 2 {
		t.Fatalf("配置变化应重建 client, builds=%d", builds.Load())
	}
}

func TestPublisherErrorHidesSecrets(t *testing.T) {
	fc := newFake()
	fc.putErr = errors.New("denied for AK/SK")
	p, _, _ := setup(t, fc)
	_, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err == nil || !strings.HasPrefix(err.Error(), "上传到对象存储失败：") {
		t.Fatalf("err=%v", err)
	}
	// validCfg 的 AccessKey=AK、SecretKey=SK；Publisher 必须把原文替换成 ***。
	if !strings.Contains(err.Error(), "denied for ***/***") {
		t.Fatalf("错误里不得出现密钥原文: %v", err)
	}
}

// 等待者自己的 ctx 取消后应立即返回，且不影响共享上传。
func TestPublisherWaiterHonoursOwnContext(t *testing.T) {
	fc := newFake()
	fc.delay = 200 * time.Millisecond
	p, _, _ := setup(t, fc)
	first := make(chan error, 1)
	go func() {
		_, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
		first <- err
	}()
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Publish(ctx, "u1/2026/10/v.mp4")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ctx error, got %v", err)
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Fatalf("等待者未及时返回: %v", d)
	}
	if err := <-first; err != nil {
		t.Fatalf("首个调用应成功: %v", err)
	}
}

// leader 的 ctx 取消不应让并发等待者失败。
func TestPublisherLeaderCancelDoesNotFailWaiters(t *testing.T) {
	fc := newFake()
	fc.delay = 100 * time.Millisecond
	p, _, _ := setup(t, fc)
	lctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = p.Publish(lctx, "u1/2026/10/v.mp4") }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	url, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err != nil || url == "" {
		t.Fatalf("waiter url=%q err=%v", url, err)
	}
	if fc.puts.Load() != 1 {
		t.Fatalf("puts=%d", fc.puts.Load())
	}
}

// 上传 panic 不能让该 key 永久卡死。
func TestPublisherPanicDoesNotWedgeKey(t *testing.T) {
	fc := newFake()
	fc.panicOnce.Store(true)
	fc.panicMsg = "leak SECRET-xyz ACCESS-abc"
	p, s, _ := setup(t, fc)
	cfg := validCfg()
	cfg.SecretKey, cfg.AccessKey = "SECRET-xyz", "ACCESS-abc"
	if err := Save(s, cfg); err != nil {
		t.Fatal(err)
	}
	_, err := p.Publish(context.Background(), "u1/2026/10/v.mp4")
	if err == nil || !strings.HasPrefix(err.Error(), "上传到对象存储失败：") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "***") || strings.Contains(err.Error(), "SECRET-xyz") || strings.Contains(err.Error(), "ACCESS-abc") {
		t.Fatalf("panic 错误未脱敏: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := p.Publish(ctx, "u1/2026/10/v.mp4"); err != nil {
		t.Fatalf("panic 后同 key 应可重试: %v", err)
	}
}
