package workflows

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

type recordingDeleter struct {
	keys []string
	err  error
}

func (d *recordingDeleter) DeleteObject(_ context.Context, key string) error {
	d.keys = append(d.keys, key)
	return d.err
}

func TestDeleteNowQueueDeletesImmediately(t *testing.T) {
	d := &recordingDeleter{}
	q := NewDeleteNowQueue(d)
	if err := q.EnqueueObjectKey("u1/x.png"); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueObjectKey(""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.keys, []string{"u1/x.png"}) {
		t.Fatalf("keys=%v（空 key 不应触发删除）", d.keys)
	}
}

// 删除失败只记日志：走到回滚时调用方的主操作已经失败，不能再用回滚错误覆盖它。
func TestDeleteNowQueueSwallowsErrors(t *testing.T) {
	q := NewDeleteNowQueue(&recordingDeleter{err: errors.New("busy")})
	if err := q.EnqueueObjectKey("u1/x.png"); err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	var nilQueue *DeleteNowQueue
	if err := nilQueue.EnqueueObjectKey("k"); err != nil {
		t.Fatal(err)
	}
}

// 复制到一半失败：磁盘上只能剩源文件，已复制出去的副本必须被删掉，否则是永远没人删的泄漏。
func TestDuplicateRollbackRemovesCopiedFilesFromDisk(t *testing.T) {
	storage, err := assets.NewLocalStorage(assets.LocalConfig{
		Root: filepath.Join(t.TempDir(), "media"), BaseURL: "http://127.0.0.1:5555", Token: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var want []string
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("src/%d.png", i)
		if _, err := storage.PutObject(ctx, assets.PutObjectInput{Key: key, Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		want = append(want, key)
	}
	repo := &fakeAssetsRepo{insertAll: true} // 每个对象都“已复制但没入库”
	d := NewAssetDuplicator(storage, repo, &fakeStorageAdjuster{}, NewDeleteNowQueue(storage))
	if _, err := d.Duplicate(ctx, 2, 9, "project_duplicate", snapshotWithKeys(5)); err == nil {
		t.Fatal("期望 Duplicate 返回错误")
	}
	var files []string
	filepath.WalkDir(storage.Root(), func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(storage.Root(), p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("回滚后磁盘上的文件=%v want 只剩源文件 %v", files, want)
	}
}
