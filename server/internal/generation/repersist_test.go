package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRepersistStore struct {
	updated map[int64]json.RawMessage
	kinds   map[int64]string
}

func (f *fakeRepersistStore) UpdateOutput(id int64, out json.RawMessage, kind string) error {
	if f.updated == nil {
		f.updated, f.kinds = map[int64]json.RawMessage{}, map[int64]string{}
	}
	f.updated[id] = out
	f.kinds[id] = kind
	return nil
}

// repersistRepo 在 fakeRecoverRepo 上覆盖 Get / SinkForTask。
type repersistRepo struct {
	fakeRecoverRepo
	task *Task
	sink json.RawMessage
}

func (r *repersistRepo) Get(userID, taskID int64) (*Task, error) {
	if r.task == nil || r.task.ID != taskID || r.task.UserID != userID {
		return nil, ErrNotFound
	}
	return r.task, nil
}

func (r *repersistRepo) SinkForTask(int64) (json.RawMessage, error) { return r.sink, nil }

func pngServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nDATA"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRepersistTaskMovesUnsavedSlots(t *testing.T) {
	srv := pngServer(t)
	old := srv.URL + "/1.png?a=1&b=2"
	pid := int64(9)
	repo := &repersistRepo{
		task: &Task{ID: 7, UserID: 1, ProjectID: &pid, Status: "succeeded",
			Output: json.RawMessage(`{"outputUrls":["` + old + `","media://kept"],"assetIds":[0,11]}`)},
		sink: json.RawMessage(`{"kind":"canvas_node","projectId":9,"nodeId":"n1","userId":1}`),
	}
	store := &fakeRepersistStore{}
	svc := &Service{repo: repo, storage: &captureStorage{}, httpClient: srv.Client(), repersist: store}

	res, err := svc.RepersistTask(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Replacements) != 1 || res.Replacements[0].Index != 0 || res.Replacements[0].OldURL != old {
		t.Fatalf("只应搬第 0 个槽位, got %+v", res.Replacements)
	}
	if !strings.HasPrefix(res.OutputURLs[0], "media://") || res.OutputURLs[1] != "media://kept" {
		t.Fatalf("outputUrls=%v", res.OutputURLs)
	}
	if string(res.Sink) != string(repo.sink) {
		t.Fatalf("应带回 sink 供画布回写, got %s", res.Sink)
	}
	var out map[string]any
	_ = json.Unmarshal(store.updated[7], &out)
	if urls := anyStrings(out["outputUrls"]); !strings.HasPrefix(urls[0], "media://") {
		t.Fatalf("output 应已回写本地地址, got %v", urls)
	}
	if store.kinds[7] != "image" {
		t.Fatalf("outputKind 应按实测 mime 回写 image, got %q", store.kinds[7])
	}
}

func TestRepersistTaskNothingToDo(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 8, UserID: 1, Status: "succeeded",
		Output: json.RawMessage(`{"outputUrls":["media://a"],"assetIds":[3]}`)}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 8); !errors.Is(err, ErrNothingToRepersist) {
		t.Fatalf("全部已在本地应返回 ErrNothingToRepersist, got %v", err)
	}
}

func TestRepersistTaskRejectsNonSucceeded(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 9, UserID: 1, Status: "processing"}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 9); !errors.Is(err, ErrNothingToRepersist) {
		t.Fatalf("未成功的任务没有可保存的产物, got %v", err)
	}
}

func TestRepersistTaskNotFoundForOtherUser(t *testing.T) {
	repo := &repersistRepo{task: &Task{ID: 10, UserID: 2, Status: "succeeded"}}
	svc := &Service{repo: repo, storage: &captureStorage{}, repersist: &fakeRepersistStore{}}
	if _, err := svc.RepersistTask(context.Background(), 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
