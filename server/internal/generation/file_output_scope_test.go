package generation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// I1：上游返回的 file:// 只能指向本服务 tempDir 里的 spill 临时文件，其余一律拒绝。
func TestPersistOutputs_RejectsFileURLOutsideTempDir(t *testing.T) {
	tmp := t.TempDir()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "data.db")
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00secret-api-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	// tempDir 里但不是 spill 文件命名的，同样拒绝（tempDir 也放参考图等别的临时文件）。
	other := filepath.Join(tmp, "ref-1.png")
	_ = os.WriteFile(other, pngBytes, 0o600)
	// tempDir 里的软链指向外部文件，也要拒绝。
	link := filepath.Join(tmp, "out-link.png")
	linkOK := os.Symlink(dbPath, link) == nil

	cases := map[string]string{
		"etc-passwd":    "file:///etc/passwd",
		"data-db":       "file://" + dbPath,
		"traversal":     "file://" + filepath.Join(tmp, "..", filepath.Base(dataDir), "data.db"),
		"non-spill":     "file://" + other,
		"relative-path": "file://out-1.png",
	}
	if linkOK && runtime.GOOS != "windows" {
		cases["symlink"] = "file://" + link
	}
	for name, u := range cases {
		t.Run(name, func(t *testing.T) {
			st := &captureStorage{}
			s := &Service{storage: st, tmpDir: tmp}
			_, _, _, err := s.persistOutputs(context.Background(), 64, 1, nil, []string{u}, 0)
			if !errors.Is(err, errLocalPersist) {
				t.Fatalf("越界 file:// 必须拒绝, got %v", err)
			}
			if len(st.got) != 0 {
				t.Fatalf("越界文件不得被复制进媒体目录: %v", st.got)
			}
		})
	}
}

func TestPersistOutputs_AcceptsSpillFileInTempDir(t *testing.T) {
	tmp := t.TempDir()
	f, err := os.CreateTemp(tmp, "out-*.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(pngBytes)
	f.Close()
	st := &captureStorage{}
	s := &Service{storage: st, tmpDir: tmp}
	if _, _, _, err := s.persistOutputs(context.Background(), 64, 2, nil, []string{"file://" + f.Name()}, 0); err != nil {
		t.Fatalf("登记目录内的 spill 文件应能保存: %v", err)
	}
}

// M4：data: 产物落盘失败时不能把 base64 原样交给画布/output，按本地产物失败处理（退款）。
func TestPersistOutputs_DataURIFailureIsErrorNotInline(t *testing.T) {
	s := &Service{storage: &captureStorage{fail: true}}
	urls, _, _, err := s.persistOutputs(context.Background(), 64, 1, nil,
		[]string{"data:audio/mpeg;base64,SUQzBAAAAAAA"}, 0)
	if !errors.Is(err, errLocalPersist) {
		t.Fatalf("data: 落盘失败应报 errLocalPersist, got %v", err)
	}
	if len(urls) != 0 {
		t.Fatalf("不应返回 data URI 给调用方: %v", urls)
	}
}
