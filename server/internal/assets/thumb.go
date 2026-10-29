package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
)

// 桌面端缩略图（P6）：画布缩到总览时几十上百张原图同时解码，WebContent 内存顶不住。
// sidecar 在第一次请求 /media/<key>?w=<宽> 时把原图缩放到该宽度、落盘缓存，之后直接读缓存。
// 只处理 JPEG/PNG：gif（动图）、webp、heic、svg、视频等一律返回 ErrThumbUnsupported，
// 由调用方回退原图——不引入新依赖，也不会把动图缩成静帧。
const (
	// ThumbMaxWidth 是允许的最大缩略宽，与 /media/thumbs 的上限一致。
	ThumbMaxWidth = 2048
	// thumbConcurrency 限制同时缩放的张数：解码一张 4K 图要几十 MB，画布首屏会一次请求上百张。
	thumbConcurrency = 2
	// thumbMaxPixels 以上的原图不缩，回退原图：解码本身就要数百 MB，不值得在 sidecar 里做。
	thumbMaxPixels = 50_000_000
	// thumbCacheMaxAge 之前生成的缓存启动时清掉（原图删了之后缓存没人引用；用到时再生成即可）。
	thumbCacheMaxAge = 30 * 24 * time.Hour
	thumbJPEGQuality = 85
	// thumbHeaderBytes 是找 JPEG EXIF 方向时最多读的字节数；APP1 一定在图像数据之前。
	thumbHeaderBytes = 256 << 10
)

// ErrThumbUnsupported 表示这个文件不出缩略图（格式不支持、原图不比目标宽、像素太多），调用方应回退原图。
var ErrThumbUnsupported = errors.New("thumbnail unsupported")

// ThumbCache 按 (object key, 宽度) 生成并落盘缓存缩略图。缓存放在媒体根目录之外，
// 不进用量统计、导出和孤儿清扫；原图被覆盖写（mtime 变新）时自动重新生成。
type ThumbCache struct {
	dir   string
	sem   chan struct{}
	group singleflight.Group
}

// NewThumbCache 建缓存目录并清掉残留的临时文件和过期缓存。
func NewThumbCache(dir string) (*ThumbCache, error) {
	if dir == "" {
		return nil, errors.New("thumb cache dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建缩略图缓存目录: %w", err)
	}
	pruneThumbCache(dir, time.Now().Add(-thumbCacheMaxAge), time.Now().Add(-staleTempAge))
	return &ThumbCache{dir: dir, sem: make(chan struct{}, thumbConcurrency)}, nil
}

// pruneThumbCache 删掉 dir 内早于 before 的缓存文件、早于 tmpBefore 的 .tmp-* 残留。
func pruneThumbCache(dir string, before, tmpBefore time.Time) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		cutoff := before
		if strings.HasPrefix(d.Name(), ".tmp-") {
			cutoff = tmpBefore
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(p); err != nil {
				log.Printf("thumbs: 清理缓存 %s 失败: %v", p, err)
			}
		}
		return nil
	})
}

// basePath 是 (key, width) 的缓存路径（不含扩展名）。key 取哈希：不把用户文件名带进缓存目录，
// 也不用再校验一遍路径安全。按哈希前两位分子目录，避免单目录文件过多。
func (c *ThumbCache) basePath(key string, width int) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, h[:2], h+"-w"+strconv.Itoa(width))
}

var thumbFormats = []struct{ ext, mime string }{{".jpg", "image/jpeg"}, {".png", "image/png"}}

// cached 找比原图新的缓存文件。
func (c *ThumbCache) cached(base string, srcMod time.Time) (string, string, bool) {
	for _, f := range thumbFormats {
		if st, err := os.Stat(base + f.ext); err == nil && st.Mode().IsRegular() && !st.ModTime().Before(srcMod) {
			return base + f.ext, f.mime, true
		}
	}
	return "", "", false
}

// Get 返回 srcPath（key 对应的原文件）缩放到 width 宽后的缓存文件路径和 MIME。
// 缓存命中直接返回；否则在并发上限内生成，同一 (key, width) 的并发请求只生成一次。
// 不出缩略图时返回 ErrThumbUnsupported。ctx 只决定本次请求等不等结果：生成是多个请求共享的，
// 不能随第一个请求取消——否则先发起的那个 <img> 被浏览器中止，排在后面的同图请求全都拿到
// context.Canceled、回退成原图。已经入队的生成照常跑完落缓存，下次直接命中。
func (c *ThumbCache) Get(ctx context.Context, key, srcPath string, width int) (string, string, error) {
	if width <= 0 || width > ThumbMaxWidth {
		return "", "", ErrThumbUnsupported
	}
	st, err := os.Stat(srcPath)
	if err != nil {
		return "", "", err
	}
	if !st.Mode().IsRegular() {
		return "", "", ErrThumbUnsupported
	}
	base := c.basePath(key, width)
	if p, m, ok := c.cached(base, st.ModTime()); ok {
		return p, m, nil
	}
	if err := ctx.Err(); err != nil {
		return "", "", err // 已取消的请求不再入队
	}
	ch := c.group.DoChan(base, func() (any, error) {
		c.sem <- struct{}{}
		defer func() { <-c.sem }()
		// 排队期间别的请求可能已经生成好了。
		if p, m, ok := c.cached(base, st.ModTime()); ok {
			return [2]string{p, m}, nil
		}
		p, m, err := generateThumb(srcPath, base, width)
		if err != nil {
			return nil, err
		}
		return [2]string{p, m}, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", "", res.Err
		}
		out := res.Val.([2]string)
		return out[0], out[1], nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

// generateThumb 解码 srcPath、按显示方向缩到 width 宽，写到 base+扩展名。
func generateThumb(srcPath, base string, width int) (string, string, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	head := make([]byte, thumbHeaderBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", "", err
	}
	head = head[:n]
	var decode func(io.Reader) (image.Image, error)
	var cfg image.Config
	orient := 1
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		if cfg, err = jpeg.DecodeConfig(io.MultiReader(bytes.NewReader(head), f)); err != nil {
			return "", "", ErrThumbUnsupported
		}
		decode = jpeg.Decode
		orient = jpegOrientation(head)
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		if cfg, err = png.DecodeConfig(io.MultiReader(bytes.NewReader(head), f)); err != nil {
			return "", "", ErrThumbUnsupported
		}
		decode = png.Decode
	default:
		return "", "", ErrThumbUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > thumbMaxPixels {
		return "", "", ErrThumbUnsupported
	}
	// width 指显示宽度：EXIF 方向 5-8 要转 90°，显示宽是原图的高。
	dispW, dispH := cfg.Width, cfg.Height
	if orient >= 5 {
		dispW, dispH = dispH, dispW
	}
	if dispW <= width {
		return "", "", ErrThumbUnsupported // 原图本来就不宽，缩略图没有意义
	}
	dw := width
	dh := max(1, int((int64(dispH)*int64(width)+int64(dispW)/2)/int64(dispW)))
	if orient >= 5 {
		dw, dh = dh, dw
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	src, err := decode(f)
	if err != nil {
		return "", "", ErrThumbUnsupported
	}
	dst := applyOrientation(resizeBox(src, dw, dh), orient)

	ext, mime := ".jpg", "image/jpeg"
	var buf bytes.Buffer
	if dst.Opaque() {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: thumbJPEGQuality})
	} else {
		ext, mime = ".png", "image/png"
		err = png.Encode(&buf, dst)
	}
	if err != nil {
		return "", "", fmt.Errorf("编码缩略图: %w", err)
	}
	out := base + ext
	if _, err := writeFileAtomic(out, &buf); err != nil {
		return "", "", fmt.Errorf("写缩略图缓存: %w", err)
	}
	return out, mime, nil
}

// resizeBox 用区域平均（box filter）把 src 缩到 dw×dh，只缩不放。每个源像素恰好计入一个目标像素，
// 在预乘 alpha 空间求平均，透明边缘不发黑。常见的 YCbCr（JPEG）/RGBA/NRGBA/Gray 走直读像素的快路径。
func resizeBox(src image.Image, dw, dh int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh = min(max(dw, 1), sw), min(max(dh, 1), sh)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))

	xmap := make([]int, sw)
	colN := make([]uint64, dw)
	for sx := 0; sx < sw; sx++ {
		xmap[sx] = sx * dw / sw
		colN[xmap[sx]]++
	}
	rowN := make([]uint64, dh)
	for sy := 0; sy < sh; sy++ {
		rowN[sy*dh/sh]++
	}

	row := make([]uint32, sw*4)
	acc := make([]uint64, dw*4)
	read := rowReader(src)
	flush := func(dy int) {
		o := dst.PixOffset(0, dy)
		for dx := 0; dx < dw; dx++ {
			n := colN[dx] * rowN[dy]
			for c := 0; c < 4; c++ {
				dst.Pix[o+dx*4+c] = uint8((acc[dx*4+c] + n/2) / n)
				acc[dx*4+c] = 0
			}
		}
	}
	cur := 0
	for sy := 0; sy < sh; sy++ {
		if dy := sy * dh / sh; dy != cur {
			flush(cur)
			cur = dy
		}
		read(b.Min.Y+sy, row)
		for sx := 0; sx < sw; sx++ {
			i, j := sx*4, xmap[sx]*4
			acc[j] += uint64(row[i])
			acc[j+1] += uint64(row[i+1])
			acc[j+2] += uint64(row[i+2])
			acc[j+3] += uint64(row[i+3])
		}
	}
	flush(cur)
	return dst
}

// rowReader 返回把第 y 行读成预乘 RGBA（每通道 0-255）的函数。
func rowReader(src image.Image) func(y int, out []uint32) {
	b := src.Bounds()
	switch s := src.(type) {
	case *image.YCbCr:
		return func(y int, out []uint32) {
			for x := b.Min.X; x < b.Max.X; x++ {
				yi, ci := s.YOffset(x, y), s.COffset(x, y)
				r, g, bl := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
				i := (x - b.Min.X) * 4
				out[i], out[i+1], out[i+2], out[i+3] = uint32(r), uint32(g), uint32(bl), 255
			}
		}
	case *image.RGBA:
		return func(y int, out []uint32) {
			p := s.Pix[s.PixOffset(b.Min.X, y):]
			for i := range out {
				out[i] = uint32(p[i])
			}
		}
	case *image.NRGBA:
		return func(y int, out []uint32) {
			p := s.Pix[s.PixOffset(b.Min.X, y):]
			for i := 0; i < len(out); i += 4 {
				a := uint32(p[i+3])
				out[i] = (uint32(p[i])*a + 127) / 255
				out[i+1] = (uint32(p[i+1])*a + 127) / 255
				out[i+2] = (uint32(p[i+2])*a + 127) / 255
				out[i+3] = a
			}
		}
	case *image.Gray:
		return func(y int, out []uint32) {
			p := s.Pix[s.PixOffset(b.Min.X, y):]
			for x := 0; x < len(out)/4; x++ {
				v := uint32(p[x])
				out[x*4], out[x*4+1], out[x*4+2], out[x*4+3] = v, v, v, 255
			}
		}
	default:
		return func(y int, out []uint32) {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := src.At(x, y).RGBA()
				i := (x - b.Min.X) * 4
				out[i], out[i+1], out[i+2], out[i+3] = r>>8, g>>8, bl>>8, a>>8
			}
		}
	}
}

// applyOrientation 按 EXIF 方向（1-8）把图转成正向显示。缩略图不带 EXIF，
// 不转的话手机竖拍照片的缩略图会是横的（浏览器显示原图时会按 EXIF 自动转）。
func applyOrientation(img *image.RGBA, orient int) *image.RGBA {
	if orient < 2 || orient > 8 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	ow, oh := w, h
	if orient >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			var sx, sy int
			switch orient {
			case 2: // 水平翻转
				sx, sy = w-1-ox, oy
			case 3: // 旋转 180°
				sx, sy = w-1-ox, h-1-oy
			case 4: // 垂直翻转
				sx, sy = ox, h-1-oy
			case 5: // 沿主对角线翻转
				sx, sy = oy, ox
			case 6: // 顺时针 90°
				sx, sy = oy, h-1-ox
			case 7: // 沿副对角线翻转
				sx, sy = w-1-oy, h-1-ox
			case 8: // 逆时针 90°
				sx, sy = w-1-oy, ox
			}
			copy(out.Pix[out.PixOffset(ox, oy):out.PixOffset(ox, oy)+4], img.Pix[img.PixOffset(sx, sy):img.PixOffset(sx, sy)+4])
		}
	}
	return out
}

// jpegOrientation 从 JPEG 头部的 EXIF（APP1）里读方向标签 0x0112；没有或解析不了返回 1。
func jpegOrientation(head []byte) int {
	i := 2 // 跳过 SOI
	for i+4 <= len(head) {
		if head[i] != 0xff {
			return 1
		}
		marker := head[i+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			i += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 { // 图像数据开始 / 文件结束
			return 1
		}
		size := int(binary.BigEndian.Uint16(head[i+2:]))
		if size < 2 || i+2+size > len(head) {
			return 1
		}
		seg := head[i+4 : i+2+size]
		if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation 解析 TIFF 结构，只看 IFD0 里的方向标签。
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return 1
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return 1
	}
	n := int(bo.Uint16(tiff[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(tiff) {
			return 1
		}
		if bo.Uint16(tiff[e:]) == 0x0112 && bo.Uint16(tiff[e+2:]) == 3 { // SHORT
			if v := int(bo.Uint16(tiff[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
