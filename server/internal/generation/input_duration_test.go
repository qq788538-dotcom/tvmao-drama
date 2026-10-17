package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/models"
)

type fakeDurStorage struct {
	AssetStorage
	key string // ExtractKey 命中时返回的 key；空 = 解析失败
}

func (f fakeDurStorage) ExtractKey(string) (string, bool) { return f.key, f.key != "" }

type fakeDurAssets struct {
	AssetRecorder
	durMs     int64 // >0 = duration_ms 命中
	getErr    error
	setCalled bool
}

func (f *fakeDurAssets) GetDurationMsByObjectKey(string) (int64, bool, error) {
	if f.getErr != nil {
		return 0, false, f.getErr
	}
	return f.durMs, f.durMs > 0, nil
}

func (f *fakeDurAssets) SetDurationMsByObjectKey(string, int64) error {
	f.setCalled = true
	return nil
}

var vcfg = &models.InputDurationConfig{From: "video"}

func TestResolveInputDuration(t *testing.T) {
	t.Run("未配置放行且不动 inputs", func(t *testing.T) {
		in := map[string]any{"duration": 5}
		if err := (&Service{}).resolveInputDuration(context.Background(), nil, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 5 {
			t.Fatal("未配置不得改写 duration")
		}
	})
	t.Run("无视频输入拒绝", func(t *testing.T) {
		svc := &Service{storage: fakeDurStorage{}, assets: &fakeDurAssets{}}
		err := svc.resolveInputDuration(context.Background(), vcfg, map[string]any{})
		var de *InputDurationError
		if !errors.As(err, &de) || de.Msg != "该模型需要连接视频输入" {
			t.Fatalf("期望无视频文案, got %v", err)
		}
	})
	t.Run("duration_ms 命中即注入并覆盖客户端的值", func(t *testing.T) {
		in := map[string]any{"videos": []string{"media://u1/v"}, "duration": 1}
		svc := &Service{storage: fakeDurStorage{key: "u1/v"}, assets: &fakeDurAssets{durMs: 12500}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 12.5 {
			t.Fatalf("got %v want 12.5", in["duration"])
		}
	})
	t.Run("key 解析失败放行且不改 inputs", func(t *testing.T) {
		in := map[string]any{"videos": []any{"https://x/v.mp4"}, "duration": 3}
		svc := &Service{storage: fakeDurStorage{}, assets: &fakeDurAssets{durMs: 9000}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if in["duration"] != 3 {
			t.Fatalf("got %v want 3", in["duration"])
		}
	})
	t.Run("DB 报错放行", func(t *testing.T) {
		svc := &Service{storage: fakeDurStorage{key: "k"}, assets: &fakeDurAssets{getErr: errors.New("db timeout")}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, map[string]any{"videos": []any{"u"}}); err != nil {
			t.Fatalf("桌面端不计费，取不到时长应放行, got %v", err)
		}
	})
	// SubmitTx 在写事务里调用本函数：任何经 r.db 的写都会自死锁（Review Focus 5）。
	t.Run("未命中放行且绝不回填", func(t *testing.T) {
		as := &fakeDurAssets{}
		svc := &Service{storage: fakeDurStorage{key: "k"}, assets: as}
		in := map[string]any{"videos": []any{"u"}}
		if err := svc.resolveInputDuration(context.Background(), vcfg, in); err != nil {
			t.Fatal(err)
		}
		if as.setCalled {
			t.Fatal("不得调用 SetDurationMsByObjectKey（写事务内经 r.db 写库会自死锁）")
		}
		if _, ok := in["duration"]; ok {
			t.Fatal("查不到时长时不得编造 duration")
		}
	})
}

// Submit 级只测失败路径（注入发生在触碰 repo/billing 之前，nil 依赖安全返回）。
func TestSubmitRejectsMissingInputVideo(t *testing.T) {
	svc := NewService(nil, inputDurationModelLookup{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Submit(context.Background(), SubmitInput{
		ModelID: "m-edit", Inputs: map[string]any{},
	})
	var de *InputDurationError
	if !errors.As(err, &de) {
		t.Fatalf("期望 InputDurationError, got %v", err)
	}
}

type inputDurationModelLookup struct{}

func (inputDurationModelLookup) GetByModelID(string) (*models.ModelConfig, error) {
	return &models.ModelConfig{ModelID: "m-edit", Enabled: true,
		GenControlRaw: json.RawMessage(`{"inputDuration":{"from":"video"}}`)}, nil
}
