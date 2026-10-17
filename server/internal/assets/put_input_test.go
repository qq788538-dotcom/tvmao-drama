package assets

import (
	"io"
	"strings"
	"testing"
)

func TestPutObjectInputContent_BodyWins(t *testing.T) {
	in := PutObjectInput{Body: []byte("abc"), Reader: strings.NewReader("zzz"), Size: 3}
	r, n := in.Content()
	got, _ := io.ReadAll(r)
	if string(got) != "abc" || n != 3 {
		t.Fatalf("Body 应优先: got=%q n=%d", got, n)
	}
}

func TestPutObjectInputContent_Reader(t *testing.T) {
	in := PutObjectInput{Reader: strings.NewReader("hello"), Size: 5}
	r, n := in.Content()
	got, _ := io.ReadAll(r)
	if string(got) != "hello" || n != 5 {
		t.Fatalf("Reader 路径: got=%q n=%d", got, n)
	}
}

func TestPutObjectInputContent_Empty(t *testing.T) {
	r, n := PutObjectInput{}.Content()
	got, _ := io.ReadAll(r)
	if len(got) != 0 || n != 0 {
		t.Fatalf("空入参应返回空 reader: got=%q n=%d", got, n)
	}
}
