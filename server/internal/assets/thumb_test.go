package assets

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newThumbCache(t *testing.T) *ThumbCache {
	t.Helper()
	c, err := NewThumbCache(filepath.Join(t.TempDir(), "cache", "thumbs"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// halves 左半 left、右半 right 的不透明图。
func halves(w, h int, left, right color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.SetRGBA(x, y, left)
			} else {
				img.SetRGBA(x, y, right)
			}
		}
	}
	return img
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeFile(t *testing.T, p string) (image.Image, string) {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, format, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img, format
}

// withExifOrientation 在 SOI 之后插一个只含方向标签的 APP1（大端 TIFF）。
func withExifOrientation(jpg []byte, orient uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	binary.Write(&tiff, binary.BigEndian, uint16(42))
	binary.Write(&tiff, binary.BigEndian, uint32(8))
	binary.Write(&tiff, binary.BigEndian, uint16(1)) // 1 个条目
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3)) // SHORT
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, orient)
	binary.Write(&tiff, binary.BigEndian, uint16(0))
	binary.Write(&tiff, binary.BigEndian, uint32(0)) // 无下一个 IFD
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

func near(c color.Color, want color.RGBA) bool {
	r, g, b, _ := c.RGBA()
	d := func(a uint32, b uint8) bool { x := int(a>>8) - int(b); return x > -40 && x < 40 }
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

var (
	red  = color.RGBA{220, 20, 20, 255}
	blue = color.RGBA{20, 20, 220, 255}
)

func TestThumbJPEGScaledAndCached(t *testing.T) {
	c := newThumbCache(t)
	src := writeFile(t, t.TempDir(), "a.jpg", encodeJPEG(t, halves(1000, 500, red, blue)))
	p, mime, err := c.Get(context.Background(), "u1/a.jpg", src, 400)
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("p=%s mime=%s err=%v", p, mime, err)
	}
	img, format := decodeFile(t, p)
	if format != "jpeg" || img.Bounds().Dx() != 400 || img.Bounds().Dy() != 200 {
		t.Fatalf("format=%s size=%v", format, img.Bounds())
	}
	if !near(img.At(50, 100), red) || !near(img.At(350, 100), blue) {
		t.Fatalf("颜色不对: %v %v", img.At(50, 100), img.At(350, 100))
	}
	st1, _ := os.Stat(p)

	// 再取命中缓存，不重新生成。
	p2, _, err := c.Get(context.Background(), "u1/a.jpg", src, 400)
	if err != nil || p2 != p {
		t.Fatalf("应命中缓存: %s %v", p2, err)
	}
	if st2, _ := os.Stat(p2); !st2.ModTime().Equal(st1.ModTime()) {
		t.Fatal("命中缓存不应重写文件")
	}
	// 不同宽度各自一份。
	p3, _, err := c.Get(context.Background(), "u1/a.jpg", src, 200)
	if err != nil || p3 == p {
		t.Fatalf("不同宽度应分开缓存: %s %v", p3, err)
	}
}

// 原图被覆盖写（mtime 比缓存新）：重新生成，不返回旧缩略图。
func TestThumbRegeneratesWhenSourceIsNewer(t *testing.T) {
	c := newThumbCache(t)
	dir := t.TempDir()
	src := writeFile(t, dir, "a.jpg", encodeJPEG(t, halves(800, 400, red, red)))
	p, _, err := c.Get(context.Background(), "k", src, 100)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.jpg", encodeJPEG(t, halves(800, 400, blue, blue)))
	p, _, err = c.Get(context.Background(), "k", src, 100)
	if err != nil {
		t.Fatal(err)
	}
	if img, _ := decodeFile(t, p); !near(img.At(10, 10), blue) {
		t.Fatalf("应按新原图重新生成: %v", img.At(10, 10))
	}
}

// 带透明的 PNG 出 PNG，透明保留；平均在预乘空间做，半透明边缘不发黑。
func TestThumbPNGKeepsAlpha(t *testing.T) {
	c := newThumbCache(t)
	img := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 600; x++ {
			if x < 300 {
				img.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 0})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{0, 200, 0, 255})
			}
		}
	}
	src := writeFile(t, t.TempDir(), "a.png", encodePNG(t, img))
	p, mime, err := c.Get(context.Background(), "u1/a.png", src, 300)
	if err != nil || mime != "image/png" {
		t.Fatalf("mime=%s err=%v", mime, err)
	}
	out, _ := decodeFile(t, p)
	if out.Bounds().Dx() != 300 || out.Bounds().Dy() != 150 {
		t.Fatalf("size=%v", out.Bounds())
	}
	if _, _, _, a := out.At(10, 10).RGBA(); a != 0 {
		t.Fatalf("透明区应保持透明: a=%d", a)
	}
	if r, g, _, a := out.At(290, 10).RGBA(); a>>8 != 255 || g>>8 < 190 || r>>8 > 10 {
		t.Fatalf("不透明区颜色不对: %v", out.At(290, 10))
	}
}

// 手机竖拍：像素横着存、EXIF 方向 6（顺时针 90°）。缩略图要按显示方向出，宽度指显示宽。
func TestThumbAppliesExifOrientation(t *testing.T) {
	c := newThumbCache(t)
	jpg := withExifOrientation(encodeJPEG(t, halves(80, 40, red, blue)), 6)
	if got := jpegOrientation(jpg); got != 6 {
		t.Fatalf("应读出方向 6, got %d", got)
	}
	src := writeFile(t, t.TempDir(), "p.jpg", jpg)
	p, _, err := c.Get(context.Background(), "p", src, 20)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := decodeFile(t, p)
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 40 {
		t.Fatalf("显示方向是 40×80，缩到 20 宽应为 20×40: %v", img.Bounds())
	}
	// 顺时针转 90° 后原图左半（红）在上、右半（蓝）在下。
	if !near(img.At(10, 5), red) || !near(img.At(10, 35), blue) {
		t.Fatalf("方向没转对: top=%v bottom=%v", img.At(10, 5), img.At(10, 35))
	}
}

func TestApplyOrientationAllCases(t *testing.T) {
	// 2×1：左红右蓝。
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.SetRGBA(0, 0, red)
	src.SetRGBA(1, 0, blue)
	type px struct{ x, y int }
	cases := map[int]struct {
		w, h   int
		redAt  px
		blueAt px
	}{
		1: {2, 1, px{0, 0}, px{1, 0}},
		2: {2, 1, px{1, 0}, px{0, 0}},
		3: {2, 1, px{1, 0}, px{0, 0}},
		4: {2, 1, px{0, 0}, px{1, 0}},
		5: {1, 2, px{0, 0}, px{0, 1}},
		6: {1, 2, px{0, 0}, px{0, 1}},
		7: {1, 2, px{0, 1}, px{0, 0}},
		8: {1, 2, px{0, 1}, px{0, 0}},
	}
	for o, want := range cases {
		out := applyOrientation(src, o)
		if out.Rect.Dx() != want.w || out.Rect.Dy() != want.h {
			t.Errorf("orient=%d size=%v", o, out.Rect)
			continue
		}
		if out.RGBAAt(want.redAt.x, want.redAt.y) != red || out.RGBAAt(want.blueAt.x, want.blueAt.y) != blue {
			t.Errorf("orient=%d 像素位置不对", o)
		}
	}
}

func TestThumbUnsupportedFallsBack(t *testing.T) {
	c := newThumbCache(t)
	dir := t.TempDir()
	var g bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 800, 400), color.Palette{color.Black, color.White})
	if err := gif.Encode(&g, pal, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"动图 gif":  g.Bytes(),
		"不认识的格式":  []byte("RIFF....WEBPVP8 not really"),
		"原图不比目标宽": encodeJPEG(t, halves(300, 100, red, blue)),
		"坏 JPEG":  append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0}, 64)...),
	} {
		src := writeFile(t, dir, "f", data)
		if _, _, err := c.Get(context.Background(), name, src, 400); !errors.Is(err, ErrThumbUnsupported) {
			t.Errorf("%s: 应回退原图, err=%v", name, err)
		}
	}
	if _, _, err := c.Get(context.Background(), "k", filepath.Join(dir, "missing"), 400); err == nil || errors.Is(err, ErrThumbUnsupported) {
		t.Errorf("原图不存在应返回文件错误: %v", err)
	}
	src := writeFile(t, dir, "ok.jpg", encodeJPEG(t, halves(800, 400, red, blue)))
	for _, w := range []int{0, -1, ThumbMaxWidth + 1} {
		if _, _, err := c.Get(context.Background(), "k", src, w); !errors.Is(err, ErrThumbUnsupported) {
			t.Errorf("w=%d 应不支持: %v", w, err)
		}
	}
}

// 同一 (key, 宽度) 的并发请求只生成一次，都拿到同一个文件；名额占满时 ctx 取消立即返回。
func TestThumbConcurrentAndQueueCancel(t *testing.T) {
	c := newThumbCache(t)
	src := writeFile(t, t.TempDir(), "a.jpg", encodeJPEG(t, halves(1200, 600, red, blue)))
	var wg sync.WaitGroup
	paths := make([]string, 8)
	errs := make([]error, 8)
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], _, errs[i] = c.Get(context.Background(), "k", src, 400)
		}(i)
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("并发请求应拿到同一份缓存: %v %v", paths, errs)
		}
	}

	for i := 0; i < cap(c.sem); i++ {
		c.sem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Get(ctx, "k2", src, 400); !errors.Is(err, context.Canceled) {
		t.Fatalf("名额占满且 ctx 已取消应立即返回: %v", err)
	}
}

func TestResizeBoxAverages(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	src.Pix[0], src.Pix[1] = 0, 255
	out := resizeBox(src, 1, 1)
	if v := out.Pix[0]; v < 127 || v > 128 || out.Pix[3] != 255 {
		t.Fatalf("黑白平均应为中灰: %v", out.Pix[:4])
	}
	// 非整除的缩放比：每个源像素恰好进一个目标像素，不越界。
	odd := resizeBox(halves(7, 5, red, blue), 3, 2)
	if odd.Rect.Dx() != 3 || odd.Rect.Dy() != 2 {
		t.Fatalf("size=%v", odd.Rect)
	}
}

func TestPruneThumbCache(t *testing.T) {
	dir := t.TempDir()
	oldF := writeFile(t, dir, "old-w400.jpg", []byte("x"))
	newF := writeFile(t, dir, "new-w400.jpg", []byte("x"))
	tmpF := writeFile(t, dir, ".tmp-123", []byte("x"))
	past := time.Now().Add(-2 * thumbCacheMaxAge)
	os.Chtimes(oldF, past, past)
	os.Chtimes(tmpF, past, past)
	pruneThumbCache(dir, time.Now().Add(-thumbCacheMaxAge), time.Now().Add(-staleTempAge))
	if _, err := os.Stat(oldF); !os.IsNotExist(err) {
		t.Fatal("过期缓存应删掉")
	}
	if _, err := os.Stat(tmpF); !os.IsNotExist(err) {
		t.Fatal("残留临时文件应删掉")
	}
	if _, err := os.Stat(newF); err != nil {
		t.Fatal("新缓存应保留")
	}
}

// 共享的生成不随先到请求的 ctx 取消：先发起的请求被中止（浏览器撤掉了那张 <img>），
// 排在后面的同图请求仍要拿到缩略图，而不是 context.Canceled 后回退原图。
func TestThumbSharedGenerationSurvivesLeaderCancel(t *testing.T) {
	c := newThumbCache(t)
	src := writeFile(t, t.TempDir(), "a.jpg", encodeJPEG(t, halves(1200, 600, red, blue)))
	for i := 0; i < cap(c.sem); i++ {
		c.sem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, _, err := c.Get(ctx, "k", src, 400)
		leaderErr <- err
	}()
	time.Sleep(30 * time.Millisecond) // 让它先成为这次生成的发起者，卡在等名额
	type res struct {
		path string
		err  error
	}
	follower := make(chan res, 1)
	go func() {
		p, _, err := c.Get(context.Background(), "k", src, 400)
		follower <- res{p, err}
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的请求应返回 context.Canceled: %v", err)
	}
	for i := 0; i < cap(c.sem); i++ {
		<-c.sem
	}
	select {
	case r := <-follower:
		if r.err != nil || r.path == "" {
			t.Fatalf("后到的同图请求应拿到缩略图: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("后到的请求一直没拿到结果")
	}
}
