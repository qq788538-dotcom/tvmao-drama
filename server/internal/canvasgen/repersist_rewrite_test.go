package canvasgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/canvasstate"
)

type rewriteSnaps struct {
	payloads map[int64]string
	versions map[int64]int64
}

func (s *rewriteSnaps) MutateSnapshot(pid int64, fn func(json.RawMessage) (json.RawMessage, bool, error)) (int64, error) {
	next, changed, err := fn(json.RawMessage(s.payloads[pid]))
	if err != nil {
		return 0, err
	}
	if !changed {
		return s.versions[pid], nil
	}
	s.payloads[pid] = string(next)
	s.versions[pid]++
	return s.versions[pid], nil
}

type rewriteSearch struct{ hits []int64 }

func (r rewriteSearch) SnapshotProjectsContaining(string) ([]int64, error) { return r.hits, nil }

type rewriteNotifier struct{ got []int64 }

func (n *rewriteNotifier) NotifyCanvasChanged(_ int64, pid, version int64, _ string, op *canvasstate.CanvasOpSummary) {
	if op != nil {
		panic("整拉快照的广播不该带 op")
	}
	n.got = append(n.got, pid, version)
}

func TestRewriteMediaRefReplacesEveryCopyInSnapshotAndBroadcasts(t *testing.T) {
	old := "https://cloudflarer2.example.com/x/1.png"
	next := "media://tapnow/u5/gen_task-7-0"
	// content 与 history[].url 是同一串;另一个画布(粘贴过去的)也有。
	snaps := &rewriteSnaps{
		payloads: map[int64]string{
			133: `{"nodes":[{"id":"n1","content":"` + old + `","settings":{"history":[{"url":"` + old + `"}]}}]}`,
			54:  `{"nodes":[{"id":"n9","content":"` + old + `"}]}`,
			99:  `{"nodes":[]}`,
		},
		versions: map[int64]int64{133: 30, 54: 10, 99: 1},
	}
	svc := NewService(nil, snaps)
	svc.SetSnapshotSearcher(rewriteSearch{hits: []int64{133, 54, 99}})
	n := &rewriteNotifier{}
	svc.notifier = n

	if err := svc.RewriteMediaRef(context.Background(), 5, old, next); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int64{133, 54} {
		if strings.Contains(snaps.payloads[pid], old) {
			t.Fatalf("project %d 仍含旧 URL: %s", pid, snaps.payloads[pid])
		}
		if strings.Count(snaps.payloads[pid], next) == 0 {
			t.Fatalf("project %d 未写入新 URL", pid)
		}
	}
	if strings.Count(snaps.payloads[133], next) != 2 {
		t.Fatalf("content 与 history 应都被替换: %s", snaps.payloads[133])
	}
	if snaps.versions[133] != 31 || snaps.versions[54] != 11 || snaps.versions[99] != 1 {
		t.Fatalf("版本推进不对: %v(没命中的画布不该 +1)", snaps.versions)
	}
	// 只广播真改了的两个画布,带新 version。
	want := []int64{133, 31, 54, 11}
	if len(n.got) != len(want) {
		t.Fatalf("广播=%v want %v", n.got, want)
	}
	for i := range want {
		if n.got[i] != want[i] {
			t.Fatalf("广播=%v want %v", n.got, want)
		}
	}
}

func TestRewriteMediaRefNoopWithoutSearcher(t *testing.T) {
	snaps := &rewriteSnaps{payloads: map[int64]string{1: `{"nodes":[]}`}, versions: map[int64]int64{1: 1}}
	svc := NewService(nil, snaps)
	if err := svc.RewriteMediaRef(context.Background(), 5, "https://a/1.png", "media://k"); err != nil {
		t.Fatal(err)
	}
	if snaps.versions[1] != 1 {
		t.Fatalf("未注入 searcher 不该动快照")
	}
}
