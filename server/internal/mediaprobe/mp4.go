package mediaprobe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNotMP4：数据不是可解析的 ISO BMFF（mp4 / mov），或缺少 moov/mvhd。
var ErrNotMP4 = errors.New("不是可识别的 MP4/MOV 文件，无法读取时长")

// maxMoovBytes 是读进内存的 moov box 上限。正常视频的 moov 只有几十 KB 到几 MB（索引表），
// 声明更大的视为异常文件，防止一个伪造的 box 头让我们一次分配几个 GB。
const maxMoovBytes = 64 << 20

// ProbeMP4DurationMs 从 ISO BMFF 的 moov/mvhd 读容器声明的时长（毫秒）。
//
// 参考仓坚持数真实帧（ProbeDurationMs）是为了防止篡改容器时长薅按秒计费；桌面端不计费
// （spec X3），而用户机器上通常没有 ffprobe，这是唯一可用的纯 Go 路径。
func ProbeMP4DurationMs(data []byte) (int64, error) {
	return ProbeMP4DurationMsReaderAt(bytes.NewReader(data), int64(len(data)))
}

// ProbeMP4DurationMsFile 同上，直接读文件：只读顶层 box 头和 moov，跳过 mdat，
// 内存占用与文件大小无关。
func ProbeMP4DurationMsFile(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return ProbeMP4DurationMsReaderAt(f, st.Size())
}

// ProbeMP4DurationMsReaderAt 遍历顶层 box，只把 moov 读进内存解析 mvhd。
func ProbeMP4DurationMsReaderAt(r io.ReaderAt, size int64) (int64, error) {
	var off int64
	hdr := make([]byte, 16)
	for off+8 <= size {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return 0, ErrNotMP4
		}
		boxSize := uint64(binary.BigEndian.Uint32(hdr[0:4]))
		name := string(hdr[4:8])
		header := uint64(8)
		switch boxSize {
		case 1: // 64 位 largesize
			if off+16 > size {
				return 0, ErrNotMP4
			}
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return 0, ErrNotMP4
			}
			boxSize = binary.BigEndian.Uint64(hdr[8:16])
			header = 16
		case 0: // 延伸到文件末尾
			boxSize = uint64(size - off)
		}
		if boxSize < header || boxSize > uint64(size-off) {
			return 0, ErrNotMP4
		}
		if name == "moov" {
			n := boxSize - header
			if n > maxMoovBytes {
				return 0, fmt.Errorf("moov 过大(%d 字节)，拒绝解析", n)
			}
			moov := make([]byte, n)
			if _, err := r.ReadAt(moov, off+int64(header)); err != nil && !errors.Is(err, io.EOF) {
				return 0, ErrNotMP4
			}
			return mvhdDurationMs(moov)
		}
		off += int64(boxSize)
	}
	return 0, ErrNotMP4
}

// mvhdDurationMs 在 moov 负载里找 mvhd，按版本读 timescale/duration 换算成毫秒。
func mvhdDurationMs(moov []byte) (int64, error) {
	mvhd, ok := findBox(moov, "mvhd")
	if !ok || len(mvhd) < 4 {
		return 0, ErrNotMP4
	}
	var timescale uint32
	var duration uint64
	switch mvhd[0] {
	case 0:
		if len(mvhd) < 20 {
			return 0, ErrNotMP4
		}
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	case 1:
		if len(mvhd) < 32 {
			return 0, ErrNotMP4
		}
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		duration = binary.BigEndian.Uint64(mvhd[24:32])
	default:
		return 0, ErrNotMP4
	}
	if timescale == 0 || duration == 0 {
		return 0, errors.New("MP4 声明的时长为 0")
	}
	return int64((duration*1000 + uint64(timescale)/2) / uint64(timescale)), nil
}

// findBox 在一层 box 序列里找第一个指定类型的 box，返回其负载（不含头）。
func findBox(data []byte, typ string) ([]byte, bool) {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data[0:4]))
		name := string(data[4:8])
		header := uint64(8)
		switch size {
		case 1: // 64 位 largesize
			if len(data) < 16 {
				return nil, false
			}
			size = binary.BigEndian.Uint64(data[8:16])
			header = 16
		case 0: // 延伸到文件末尾
			size = uint64(len(data))
		}
		if size < header || size > uint64(len(data)) {
			return nil, false
		}
		if name == typ {
			return data[header:size], true
		}
		data = data[size:]
	}
	return nil, false
}

// ProbeDurationMsPortable 先用系统 ffprobe 数真实帧；机器上没有 ffprobe 时退回读 MP4 容器时长。
func ProbeDurationMsPortable(ctx context.Context, data []byte) (int64, error) {
	ms, err := ProbeDurationMs(ctx, data)
	if errors.Is(err, ErrProbeUnavailable) {
		return ProbeMP4DurationMs(data)
	}
	return ms, err
}

// ProbeDurationMsPortableFile 是 ProbeDurationMsPortable 的文件版：ffprobe 直接读该文件，
// 没有 ffprobe 时只读 moov。全程不把视频读进内存。
func ProbeDurationMsPortableFile(ctx context.Context, path string) (int64, error) {
	ms, err := ProbeDurationMsFile(ctx, path)
	if errors.Is(err, ErrProbeUnavailable) {
		return ProbeMP4DurationMsFile(path)
	}
	return ms, err
}
