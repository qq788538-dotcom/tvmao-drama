package http

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/canvasgen"
)

// ── 坏配置降级 ─────────────────────────────────────────────

// 存量坏数据（例如旧版本无校验时写入的）不能让用户端接口全部 500：按“未配置”处理。
func TestPostprocessCorruptConfigDegrades(t *testing.T) {
	s := &desubFakeSettings{vals: map[string]string{canvasgen.SettingCanvasPostprocess: `{"subtitleRemovalModels":"x"}`}}
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2, snapshot: desubSnapshot(t, "node-src", "media://v.mp4", nil)}
	h := NewCanvasPostprocessHandler(s, &desubFakeEnqueuer{taskID: 1}, projRepo, &desubFakeAssets{found: map[string]int64{"v.mp4": 1000}}, &desubFakeGen{}, &postprocessFakeModelLookup{}, postprocessNoopSigner{})

	rec := httptest.NewRecorder()
	h.GetConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/canvas/postprocess-config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetConfig want 200, got %d body=%s", rec.Code, rec.Body)
	}
	var cfg map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	if subs, _ := cfg["subtitleRemoval"].([]any); subs == nil || len(subs) != 0 {
		t.Fatalf("坏配置应降级为空列表: %v", cfg)
	}

	body := map[string]any{"projectId": 2, "nodeId": "n", "sourceNodeId": "node-src", "modelId": "m", "resolution": "1080p"}
	for name, call := range map[string]func(http.ResponseWriter, *http.Request){
		"desub": h.Desub, "upscale": h.Upscale, "whitemodel": h.WhiteModel, "estimate": h.Estimate,
	} {
		rec := httptest.NewRecorder()
		call(rec, desubReq(body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400（未知模型）, got %d body=%s", name, rec.Code, rec.Body)
		}
	}
}

// ── 下载超时、本地 /media 直接读文件、峰值内存 ──────────────────────────────

// fakeObjects 按 key 打开磁盘上的文件，模拟 assets.LocalStorage.GetObject（返回 *os.File）。
type fakeObjects struct {
	files map[string]string
	opens atomic.Int32
}

func (f *fakeObjects) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	f.opens.Add(1)
	p, ok := f.files[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.Open(p)
}

// bigSparseMP4 写一个 600MB（稀疏，不占磁盘）且 moov 在尾部的 mp4：超过旧实现 500MB 的整读上限。
func bigSparseMP4(t *testing.T, durationMs uint32) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "big.mp4")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const mdatPayload = int64(600 << 20)
	ftyp := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}
	hdr := make([]byte, 16)
	binary.BigEndian.PutUint32(hdr[0:4], 1)
	copy(hdr[4:8], "mdat")
	binary.BigEndian.PutUint64(hdr[8:16], uint64(16+mdatPayload))
	f.Write(ftyp)
	f.Write(hdr)
	f.Seek(mdatPayload, io.SeekCurrent)
	mp4 := postprocessMP4(durationMs)
	f.Write(mp4[16:]) // 只要 moov
	return p
}

// 本地媒体：按 key 直接打开存储里的文件交给探测器，不走 HTTP、不整读进内存、不复制临时文件。
func TestResolveDuration_LocalObjectStoreProbesFileInPlace(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // 没有 ffprobe：走纯 Go moov 解析
	path := bigSparseMP4(t, 8400)
	objs := &fakeObjects{files: map[string]string{"u1/big.mp4": path}}
	assetsRepo := &desubFakeAssets{}
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://u1/big.mp4", nil),
		assetsRepo, nil,
	)
	h.WithObjectStore(objs)
	h.download = func(context.Context, string) (io.ReadCloser, error) {
		t.Error("本地对象存储可读时不应走 HTTP 下载")
		return nil, errors.New("unexpected")
	}
	var probed string
	realProbe := h.probe
	h.probe = func(ctx context.Context, p string) (int64, error) {
		probed = p
		return realProbe(ctx, p)
	}
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{"projectId": 2, "nodeId": "n", "sourceNodeId": "node-src", "modelId": "desub-std"}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if probed != path {
		t.Fatalf("应直接探测存储里的原文件 %s，实际 %s", path, probed)
	}
	if got := enq.captured.Inputs["duration"]; got != 8.4 {
		t.Fatalf("duration=%v", got)
	}
	if assetsRepo.setCalls["u1/big.mp4"] != 8400 {
		t.Fatalf("应回填: %v", assetsRepo.setCalls)
	}
}

// 对象存储返回的不是文件（例如以后换成远端存储）：流式落临时文件再探测，探测完删除。
func TestResolveDuration_NonFileObjectIsSpooledToTemp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h, enq := newDesubTestHandler(
		[]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}},
		desubSnapshot(t, "node-src", "media://u1/a.mp4", nil),
		&desubFakeAssets{}, nil,
	)
	h.WithObjectStore(readerObjects{data: postprocessMP4(5100)})
	var probed string
	realProbe := h.probe
	h.probe = func(ctx context.Context, p string) (int64, error) {
		probed = p
		return realProbe(ctx, p)
	}
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{"projectId": 2, "nodeId": "n", "sourceNodeId": "node-src", "modelId": "desub-std"}))
	if rec.Code != http.StatusAccepted || enq.captured.Inputs["duration"] != 5.1 {
		t.Fatalf("got %d body=%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(probed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("临时文件应已删除: %s err=%v", probed, err)
	}
}

type readerObjects struct{ data []byte }

func (r readerObjects) GetObject(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(r.data)), nil
}

// 远端/HTTP 回退路径必须有超时：服务端一直不回包时，请求不能无限挂住。
func TestResolveDuration_HTTPDownloadTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select { // 发完头就卡住，模拟本地 /media 处理卡死
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	old := postprocessProbeTimeout
	postprocessProbeTimeout = 200 * time.Millisecond
	defer func() { postprocessProbeTimeout = old }()

	settingsRepo := newDesubSettings([]canvasgen.CanvasDesubBinding{{ModelID: "desub-std"}})
	projRepo := &desubFakeProjects{ownedUserID: 1, ownedProjectID: 2, snapshot: desubSnapshot(t, "node-src", "media://slow.mp4", nil)}
	enq := &desubFakeEnqueuer{taskID: 1}
	h := NewCanvasPostprocessHandler(settingsRepo, enq, projRepo, &desubFakeAssets{}, &desubFakeGen{}, &postprocessFakeModelLookup{}, urlSigner{base: srv.URL})

	start := time.Now()
	rec := httptest.NewRecorder()
	h.Desub(rec, desubReq(map[string]any{"projectId": 2, "nodeId": "n", "sourceNodeId": "node-src", "modelId": "desub-std"}))
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("下载应在超时后放弃，实际耗时 %v", el)
	}
	if rec.Code != http.StatusBadRequest || enq.captured != nil {
		t.Fatalf("want 400 且不入队, got %d body=%s", rec.Code, rec.Body)
	}
}

// urlSigner 把 media://key 签成指向测试服务器的 URL。
type urlSigner struct{ base string }

func (u urlSigner) ObjectKey(s string) string { return strings.TrimPrefix(s, "media://") }
func (u urlSigner) Sign(s string) string {
	return u.base + "/media/" + strings.TrimPrefix(s, "media://")
}

// 同一个源视频并发预估/提交，只探测一次（singleflight），避免 N 份下载与临时文件。
func TestResolveDuration_ConcurrentSameKeyProbesOnce(t *testing.T) {
	h, _ := newEstimateTestHandler(desubSnapshot(t, "node-src", "media://same.mp4", nil), nil)
	var downloads atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	h.download = func(context.Context, string) (io.ReadCloser, error) {
		downloads.Add(1)
		entered <- struct{}{}
		<-release
		return io.NopCloser(bytes.NewReader([]byte("x"))), nil
	}
	h.probe = func(context.Context, string) (int64, error) { return 3000, nil }

	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.Estimate(rec, estimateReq(map[string]any{"projectId": 2, "sourceNodeId": "node-src", "modelId": "desub-std"}))
			codes[i] = rec.Code
		}(i)
	}
	<-entered
	time.Sleep(150 * time.Millisecond) // 让其余请求都排进同一个 in-flight 调用
	close(release)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("req %d code=%d", i, c)
		}
	}
	if n := downloads.Load(); n != 1 {
		t.Fatalf("并发同 key 应只下载一次，实际 %d 次", n)
	}
}
