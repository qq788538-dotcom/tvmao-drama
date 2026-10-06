package canvasinputs

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// CalculateResolution 是 apps/web/src/modules/canvas/editor/utils/resolution.ts
// 里 calculateResolution 的 Go 同构实现。
//
// 为什么会有两份：CLI 没有浏览器，生成入参的派生必须在服务端算一遍；而画布结构
// 正处在活跃演进期（history 刚改格式、subtitle-erase 刚加），此时把线上生成路径
// 迁到服务端风险最高，所以先接受双实现、用对齐测试锁住。
//
// 任一侧改动必须同步改另一侧并更新 resolution_test.go 的 golden 表。
// 判定标准不是「两边都能跑」，而是同一 (ratio, resolution) 两边输出**逐字一致**。
// golden 表的期望值由实际执行前端函数导出，不是推算的。
//
// 移植细节：JS 的 Math.round 是「四舍五入、.5 向 +∞」，Go 的 math.Round 是
// 「四舍五入、.5 远离零」。本函数输入恒为正数，两者等价。
type Resolution struct {
	Str string
	W   int
	H   int
}

var explicitSizeRe = regexp.MustCompile(`^(\d+)[xX](\d+)$`)
var kSizeRe = regexp.MustCompile(`^(\d+)K$`)

func CalculateResolution(ratio, baseResolution string) Resolution {
	baseW, baseH := 1024.0, 1024.0

	// 显式 WxH 优先，且**忽略 ratio**——与前端一致：用户既然写死了尺寸，
	// 比例就不该再插手。
	if m := explicitSizeRe.FindStringSubmatch(strings.TrimSpace(baseResolution)); m != nil {
		w, errW := strconv.Atoi(m[1])
		h, errH := strconv.Atoi(m[2])
		if errW == nil && errH == nil {
			safeW := int(math.Max(16, math.Round(float64(w)/16)*16))
			safeH := int(math.Max(16, math.Round(float64(h)/16)*16))
			return Resolution{Str: itoa(safeW) + "x" + itoa(safeH), W: safeW, H: safeH}
		}
	}

	switch baseResolution {
	case "1080P":
		baseW, baseH = 1920, 1080
	case "720P":
		baseW, baseH = 1280, 720
	case "480P":
		baseW, baseH = 854, 480
	case "2K":
		baseW, baseH = 2048, 2048
	case "4K":
		// 图片 4K = 4096px 边长（和 1K/2K 一致），不是视频的 3840×2160。
		baseW, baseH = 4096, 4096
	default:
		if m := kSizeRe.FindStringSubmatch(strings.ToUpper(baseResolution)); m != nil {
			if k, err := strconv.Atoi(m[1]); err == nil {
				size := float64(k) * 1024
				baseW, baseH = size, size
			}
		}
	}

	if ratio == "Auto" {
		return Resolution{Str: itoa(int(baseW)) + "x" + itoa(int(baseH)), W: int(baseW), H: int(baseH)}
	}

	parts := strings.Split(ratio, ":")
	if len(parts) != 2 {
		return Resolution{Str: "1024x1024", W: 1024, H: 1024}
	}
	rW, errW := strconv.ParseFloat(parts[0], 64)
	rH, errH := strconv.ParseFloat(parts[1], 64)
	// 前端是 `if (!rW || !rH)`：NaN 与 0 都落这个兜底。
	if errW != nil || errH != nil || rW == 0 || rH == 0 {
		return Resolution{Str: "1024x1024", W: 1024, H: 1024}
	}

	var targetW, targetH float64
	switch {
	case math.Abs(rW-rH) < 0.1:
		targetW, targetH = baseW, baseH
	case rW > rH:
		targetW = baseW
		if baseResolution == "Auto" || baseResolution == "1K" {
			targetW = 1280
		}
		targetH = math.Round(targetW * (rH / rW))
	default:
		targetH = baseW
		if baseResolution == "Auto" || baseResolution == "1K" {
			targetH = 1280
		}
		targetW = math.Round(targetH * (rW / rH))
	}

	w := int(math.Round(targetW/16) * 16)
	h := int(math.Round(targetH/16) * 16)
	return Resolution{Str: itoa(w) + "x" + itoa(h), W: w, H: h}
}

func itoa(v int) string { return strconv.Itoa(v) }
