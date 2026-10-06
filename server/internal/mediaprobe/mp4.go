package mediaprobe

import (
	"context"
	"encoding/binary"
	"errors"
)

// ErrNotMP4：数据不是可解析的 ISO BMFF（mp4 / mov），或缺少 moov/mvhd。
var ErrNotMP4 = errors.New("不是可识别的 MP4/MOV 文件，无法读取时长")

// ProbeMP4DurationMs 从 ISO BMFF 的 moov/mvhd 读容器声明的时长（毫秒）。
//
// 参考仓坚持数真实帧（ProbeDurationMs）是为了防止篡改容器时长薅按秒计费；桌面端不计费
// （spec X3），而用户机器上通常没有 ffprobe，这是唯一可用的纯 Go 路径。
func ProbeMP4DurationMs(data []byte) (int64, error) {
	moov, ok := findBox(data, "moov")
	if !ok {
		return 0, ErrNotMP4
	}
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
