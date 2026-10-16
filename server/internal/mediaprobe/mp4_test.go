package mediaprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func box(typ string, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(body)))
	copy(out[4:8], typ)
	return append(out, body...)
}

func mvhdV0(timescale, duration uint32) []byte {
	p := make([]byte, 100) // version 0, flags 0
	binary.BigEndian.PutUint32(p[12:16], timescale)
	binary.BigEndian.PutUint32(p[16:20], duration)
	return box("mvhd", p)
}

func mvhdV1(timescale uint32, duration uint64) []byte {
	p := make([]byte, 112)
	p[0] = 1
	binary.BigEndian.PutUint32(p[20:24], timescale)
	binary.BigEndian.PutUint64(p[24:32], duration)
	return box("mvhd", p)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var ftyp = box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41"))

func TestProbeMP4V0(t *testing.T) {
	data := concat(ftyp, box("moov", mvhdV0(1000, 5500), box("trak")))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 5500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

// 非 faststart 的文件 moov 在 mdat 之后（录屏、很多上游产物都是这样）。
func TestProbeMP4V1MoovAtEnd(t *testing.T) {
	data := concat(ftyp, box("mdat", make([]byte, 4096)), box("moov", mvhdV1(90000, 9*90000+45000)))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 9500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

func TestProbeMP4LargeSizeBox(t *testing.T) {
	mdatPayload := make([]byte, 32)
	large := make([]byte, 16)
	binary.BigEndian.PutUint32(large[0:4], 1)
	copy(large[4:8], "mdat")
	binary.BigEndian.PutUint64(large[8:16], uint64(16+len(mdatPayload)))
	data := concat(ftyp, large, mdatPayload, box("moov", mvhdV0(600, 1800)))
	ms, err := ProbeMP4DurationMs(data)
	if err != nil || ms != 3000 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
}

func TestProbeMP4Rejects(t *testing.T) {
	truncated := box("moov", mvhdV0(1000, 1000))
	binary.BigEndian.PutUint32(truncated[0:4], uint32(len(truncated)+50))
	cases := map[string][]byte{
		"垃圾":     []byte("not a video at all"),
		"无 moov": concat(ftyp, box("mdat", make([]byte, 8))),
		"截断":     truncated,
		"webm":   {0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81},
	}
	for name, data := range cases {
		if _, err := ProbeMP4DurationMs(data); !errors.Is(err, ErrNotMP4) {
			t.Fatalf("%s: err = %v, want ErrNotMP4", name, err)
		}
	}
	if _, err := ProbeMP4DurationMs(concat(ftyp, box("moov", mvhdV0(0, 100)))); err == nil {
		t.Fatal("timescale 为 0 应报错")
	}
}

// 用户机器上通常没有 ffprobe：Portable 必须退回纯 Go 解析。
func TestPortableFallsBackWithoutFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ProbeDurationMs(context.Background(), []byte("x")); !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("清空 PATH 后应找不到 ffprobe: %v", err)
	}
	ms, err := ProbeDurationMsPortable(context.Background(), concat(ftyp, box("moov", mvhdV0(1000, 2500))))
	if err != nil || ms != 2500 {
		t.Fatalf("ms=%d err=%v", ms, err)
	}
	if _, err := ProbeDurationMsPortable(context.Background(), []byte("garbage")); !errors.Is(err, ErrNotMP4) {
		t.Fatalf("err = %v", err)
	}
}

// ISO/IEC 14496-12：duration 全 1 表示“时长未知”（分片 MP4 常见），不能当成 49 天/几亿年；
// 超大 v1 duration 换算毫秒时也不能溢出成负数或乱值。
func TestProbeMP4UnknownOrOverflowDuration(t *testing.T) {
	cases := map[string][]byte{
		"v0 全 1":  concat(ftyp, box("moov", mvhdV0(1000, 0xFFFFFFFF))),
		"v1 全 1":  concat(ftyp, box("moov", mvhdV1(1000, 0xFFFFFFFFFFFFFFFF))),
		"v1 毫秒溢出": concat(ftyp, box("moov", mvhdV1(1, 1<<62))),
	}
	for name, data := range cases {
		if ms, err := ProbeMP4DurationMs(data); err == nil {
			t.Errorf("%s: ms=%d，want error", name, ms)
		}
	}
}
