package mediaprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeSparseMP4 写一个 ftyp + 巨大 mdat（稀疏空洞，不占磁盘）+ 尾部 moov 的文件。
// 用来证明按文件探测只读 box 头和 moov，不会把整个文件读进内存，也不受 500MB 上限约束。
func writeSparseMP4(t *testing.T, mdatPayload int64, durationMs uint32) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "big.mp4")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(ftyp); err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 16)
	binary.BigEndian.PutUint32(hdr[0:4], 1)
	copy(hdr[4:8], "mdat")
	binary.BigEndian.PutUint64(hdr[8:16], uint64(16+mdatPayload))
	if _, err := f.Write(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(mdatPayload, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(box("moov", mvhdV0(1000, durationMs))); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeMP4FileMoovAfterHugeMdat(t *testing.T) {
	p := writeSparseMP4(t, 3<<30, 4200) // 3GB 空洞
	ms, err := ProbeMP4DurationMsFile(p)
	if err != nil || ms != 4200 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

func TestProbeMP4FileRejectsGarbageAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(p, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeMP4DurationMsFile(p); !errors.Is(err, ErrNotMP4) {
		t.Fatalf("err=%v", err)
	}
	if _, err := ProbeMP4DurationMsFile(filepath.Join(t.TempDir(), "nope.mp4")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

// moov 体积异常（声明几个 GB）时不能照单全收地分配内存。
func TestProbeMP4FileRejectsOversizedMoov(t *testing.T) {
	p := filepath.Join(t.TempDir(), "evil.mp4")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	hdr := make([]byte, 16)
	binary.BigEndian.PutUint32(hdr[0:4], 1)
	copy(hdr[4:8], "moov")
	binary.BigEndian.PutUint64(hdr[8:16], uint64(16+(1<<30)))
	f.Write(ftyp)
	f.Write(hdr)
	f.Truncate(int64(len(ftyp)) + 16 + (1 << 30))
	f.Close()
	if _, err := ProbeMP4DurationMsFile(p); err == nil {
		t.Fatal("超大 moov 应拒绝")
	}
}

func TestPortableFileFallsBackWithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	p := writeSparseMP4(t, 1<<20, 2500)
	ms, err := ProbeDurationMsPortableFile(context.Background(), p)
	if err != nil || ms != 2500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}
