package objectstore

import (
	"context"
	"errors"
	"testing"
)

func TestProbeSteps(t *testing.T) {
	ctx := context.Background()
	fc := newFake()
	// 模拟公网下载：fake 里此刻只有刚上传的那一个测试文件，原样取回。
	ok := Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) {
		for _, v := range fc.objects {
			return v, nil
		}
		return nil, errors.New("none")
	})
	if !ok.OK || len(fc.objects) != 0 {
		t.Fatalf("成功时应删掉测试文件: %+v objects=%d", ok, len(fc.objects))
	}

	fc = newFake()
	fc.putErr = errors.New("AccessDenied")
	if r := Probe(ctx, fc, "p", nil); r.OK || r.Step != "upload" {
		t.Fatalf("got %+v", r)
	}

	fc = newFake()
	r := Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) { return nil, errors.New("403") })
	if r.OK || r.Step != "download" {
		t.Fatalf("got %+v", r)
	}

	fc = newFake()
	r = Probe(ctx, fc, "p", func(context.Context, string) ([]byte, error) { return []byte("wrong"), nil })
	if r.OK || r.Step != "download" {
		t.Fatalf("内容不一致应在 download 步失败: %+v", r)
	}
}
