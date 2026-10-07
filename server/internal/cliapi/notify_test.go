package cliapi

import (
	"encoding/json"
	"errors"
	"testing"
)

type recNotifier struct{ got [][3]any }

func (r *recNotifier) Publish(projectID, version int64, source string) {
	r.got = append(r.got, [3]any{projectID, version, source})
}

// fakeMutStore 只实现 MutateSnapshot，其余方法用嵌入的 nil 接口占位（测试不调用）。
type fakeMutStore struct {
	ProjectStore
	version int64
}

func (f *fakeMutStore) MutateSnapshot(pid int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	_, changed, err := fn(json.RawMessage(`{}`))
	if err != nil {
		return 0, err
	}
	if changed {
		f.version++
	}
	return f.version, nil
}

func TestChangeNotifierPublishesOnlyOnChange(t *testing.T) {
	n := &recNotifier{}
	s := WithChangeNotifier(&fakeMutStore{version: 4}, n)

	_, _ = s.MutateSnapshot(7, func(p json.RawMessage) (json.RawMessage, bool, error) { return p, true, nil })
	_, _ = s.MutateSnapshot(7, func(p json.RawMessage) (json.RawMessage, bool, error) { return p, false, nil })
	_, _ = s.MutateSnapshot(7, func(p json.RawMessage) (json.RawMessage, bool, error) { return nil, false, errors.New("x") })

	if len(n.got) != 1 || n.got[0] != [3]any{int64(7), int64(5), "cli"} {
		t.Fatalf("got=%v，只应在真正写入后发一次", n.got)
	}
}

func TestChangeNotifierNilIsPassthrough(t *testing.T) {
	inner := &fakeMutStore{}
	if WithChangeNotifier(inner, nil) != ProjectStore(inner) {
		t.Fatal("notifier 为 nil 时应原样返回")
	}
}
