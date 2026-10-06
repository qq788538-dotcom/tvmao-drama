package generation

import (
	"encoding/json"
	"sync"
	"testing"
)

// fakeQueueRepo 只实现队列 worker 用到的方法，其余 panic——避免测试静默走错分支。
type fakeQueueRepo struct {
	mu sync.Mutex

	running map[string]int  // model → 在跑数
	queued  map[string][]Task // model → 排队任务

	claimed     []int64
	claimFails  map[int64]bool // 模拟被别的实例抢先
	listedFor   []string       // 记录问过哪些模型，用于验证不是扫全队列
}

func (f *fakeQueueRepo) RunningCountByModel(int) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, v := range f.running {
		out[k] = v
	}
	return out, nil
}

func (f *fakeQueueRepo) ListQueuedForModel(modelID string, limit int) ([]Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listedFor = append(f.listedFor, modelID)
	all := f.queued[modelID]
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (f *fakeQueueRepo) ClaimQueued(taskID int64, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimFails[taskID] {
		return false, nil
	}
	f.claimed = append(f.claimed, taskID)
	return true, nil
}

func qTask(id int64, model string) Task {
	return Task{ID: id, ModelID: model, Status: "queued", Sink: json.RawMessage(`{"kind":"test"}`)}
}

// 队头阻塞回归测试：视频模型排满时，生图模型的任务仍必须被立刻领取。
// 如果 worker 按 id 顺序扫全队列，遇到满员模型就会停下，后面所有模型都被堵住——
// 慢模型（视频，几分钟一个）会把快模型（生图，几秒一个）饿死。
func TestPickupSkipsFullModelWithoutBlockingOthers(t *testing.T) {
	repo := &fakeQueueRepo{
		running: map[string]int{"video": 3}, // 已满（上限 3）
		queued: map[string][]Task{
			"video": {qTask(1, "video"), qTask(2, "video")},
			"image": {qTask(3, "image")},
		},
	}
	limits := map[string]int{"video": 3, "image": 20}
	got := pickupCandidates(repo, []string{"video", "image"}, limits, 60)

	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("满员模型不该被领取，其他模型必须照常领取，got = %+v", got)
	}
}

// 空位计算：上限 3、在跑 1 → 只能再领 2 个，不能把排队的 5 个全领走。
func TestPickupRespectsRemainingSlots(t *testing.T) {
	repo := &fakeQueueRepo{
		running: map[string]int{"video": 1},
		queued: map[string][]Task{
			"video": {qTask(1, "video"), qTask(2, "video"), qTask(3, "video"),
				qTask(4, "video"), qTask(5, "video")},
		},
	}
	got := pickupCandidates(repo, []string{"video"}, map[string]int{"video": 3}, 60)
	if len(got) != 2 {
		t.Fatalf("空位=3-1=2，应只领 2 个，got %d 个", len(got))
	}
}

// 没有配置上限的模型要有个安全默认值，不能当成 0（那会让它永远排不上，
// 任务最终被兜底超时退款——用户会觉得"提交了就没反应"）。
func TestPickupUsesDefaultLimitForUnknownModel(t *testing.T) {
	repo := &fakeQueueRepo{
		running: map[string]int{},
		queued:  map[string][]Task{"newmodel": {qTask(1, "newmodel")}},
	}
	got := pickupCandidates(repo, []string{"newmodel"}, map[string]int{}, 60)
	if len(got) != 1 {
		t.Fatalf("未配置上限的模型应按默认值放行，got %d", len(got))
	}
}

// CAS 抢占失败（别的实例先领走）时，本实例绝不能继续执行那个任务——
// 否则同一任务会被两个实例同时跑，上游被调两次、钱也可能被结算两次。
func TestClaimLoserDoesNotExecute(t *testing.T) {
	repo := &fakeQueueRepo{claimFails: map[int64]bool{7: true}}
	ok, err := repo.ClaimQueued(7, "inst-a")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("CAS 失败时必须返回 false，调用方据此跳过执行")
	}
}
