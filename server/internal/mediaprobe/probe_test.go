package mediaprobe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 真实视频样本:放 testdata/sample_3s.mp4，真实时长约 3.0s(3000ms±)。
// 需要 ffprobe;缺任一则 skip(CI/本机未装时不误判失败)。
func TestProbeDurationMs_RealSample(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 未安装,跳过")
	}
	data, err := os.ReadFile(filepath.Join("testdata", "sample_3s.mp4"))
	if err != nil {
		t.Skip("缺 testdata/sample_3s.mp4 样本,跳过")
	}
	ms, err := ProbeDurationMs(context.Background(), data)
	if err != nil {
		t.Fatalf("ProbeDurationMs err: %v", err)
	}
	// 允许 ±200ms 容差(不同编码末帧对齐差异)。
	if ms < 2800 || ms > 3200 {
		t.Errorf("时长 = %dms, 期望 ~3000ms", ms)
	}
}

func TestProbeDurationMs_Garbage(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 未安装,跳过")
	}
	if _, err := ProbeDurationMs(context.Background(), []byte("not a video")); err == nil {
		t.Fatal("坏数据应返回 error")
	}
}

// TestProbeDurationMs_UsesVideoStreamNotFormatDuration 锁住核心防伪造行为:
// 造一个视频流真实只有 ~2s、但容器整体 format.duration ≈ 8s(音频流 8s 撑长了容器)
// 的样本——这是"篡改元数据也骗不过"这句注释承诺要防住的具体攻击形状。
// 若实现退化为读 format.duration(容器声明值,可被外部工具单独改写),
// 这里会测出 ~8000ms 而非 ~2000ms,测试即失败。
func TestProbeDurationMs_UsesVideoStreamNotFormatDuration(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe 未安装,跳过")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg 未安装,跳过(需要造样本)")
	}

	tmp, err := os.CreateTemp("", "mismatch-*.mp4")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	// 视频流 2s(testsrc) + 音频流 8s(sine) 混流:format.duration 由最长流决定,
	// 约等于 8s;而视频流真实只有 2s 的帧。
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=8",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-y", tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg 造样本失败: %v\n%s", err, out)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}

	ms, err := ProbeDurationMs(context.Background(), data)
	if err != nil {
		t.Fatalf("ProbeDurationMs err: %v", err)
	}
	if ms < 1700 || ms > 2300 {
		t.Errorf("时长 = %dms, 期望 ~2000ms(视频流真实时长);"+
			"若接近 8000ms 说明读到了 format.duration(容器声明值,可篡改)而非视频流真实帧时长", ms)
	}
}
