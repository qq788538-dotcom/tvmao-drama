// Package mediaprobe 用系统 ffprobe 探测媒体真实时长。
// 数"真实存在的帧末包时间戳"而非容器声明的 duration 字段——后者可被工具单独篡改,
// 用于按秒计费时会被薅;数真实帧则改元数据无效。仅用于上传视频(用户可篡改的来源)。
//
// 桌面端不打包 ffprobe：找不到 ffprobe 时 ProbeDurationMs 返回
// ErrProbeUnavailable，调用方（上传接口）按「时长未知」处理，不得因此拒绝上传。
// generation 的 inputDuration 不调用本包（见 generation/input_duration.go）。
package mediaprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

var ErrProbeUnavailable = errors.New("ffprobe unavailable")

// ProbeDurationMs 返回视频真实时长(毫秒)。写临时文件后 ffprobe 顺序 demux 视频流,
// 取最后一个包的 pts_time+duration_time 作为真实时长(不读 format.duration 声明值)。
func ProbeDurationMs(ctx context.Context, data []byte) (int64, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0, ErrProbeUnavailable
	}
	f, err := os.CreateTemp("", "probe-*.bin")
	if err != nil {
		return 0, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return 0, err
	}
	f.Close()
	return ProbeDurationMsFile(ctx, f.Name())
}

// ProbeDurationMsFile 同 ProbeDurationMs，直接探测磁盘上的文件（不经内存、不写临时文件）。
func ProbeDurationMsFile(ctx context.Context, path string) (int64, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0, ErrProbeUnavailable
	}
	// -select_streams v:0 只看首个视频流;packet 级 pts_time/duration_time = 实际帧时间。
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time,duration_time",
		"-of", "json", path)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	var parsed struct {
		Packets []struct {
			PTS string `json:"pts_time"`
			Dur string `json:"duration_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0, fmt.Errorf("ffprobe 输出解析失败: %w", err)
	}
	if len(parsed.Packets) == 0 {
		return 0, errors.New("无视频包,无法测时长")
	}
	last := parsed.Packets[len(parsed.Packets)-1]
	// ffprobe 对末包 duration_time 常输出字面量 "N/A"(容器没给出该包时长)。
	// fmt.Sscanf 对非数字输入会静默留零值,parseFloatOrZero 显式表达"解析失败按 0 处理"
	// 而不是伪装成一次成功的数值解析——语义等价,但不再靠隐藏的失败通路兜底。
	pts := parseFloatOrZero(last.PTS)
	dur := parseFloatOrZero(last.Dur)
	sec := pts + dur
	if sec <= 0 {
		return 0, errors.New("测得非正时长")
	}
	return int64(sec*1000 + 0.5), nil
}

// parseFloatOrZero 解析 ffprobe 的 pts_time/duration_time 字段。
// 该字段可能是 "N/A"(末包无 duration_time 时常见)或其它非数字字面量,
// 解析失败时按 0 处理而非报错——上层用 pts_time+duration_time 求和,
// duration_time 缺失时退化为只用 pts_time,是可接受的近似。
func parseFloatOrZero(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
