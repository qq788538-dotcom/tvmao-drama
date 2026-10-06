package generation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/models"
)

func intp(n int) *int { return &n }

func TestValidateInputLimits(t *testing.T) {
	limits := map[string]models.InputLimit{
		"video": {Min: intp(1), Max: intp(1)},
	}
	cases := []struct {
		name    string
		limits  map[string]models.InputLimit
		inputs  map[string]any
		wantErr string // 空 = 期望通过
	}{
		{"未配置放行", nil, map[string]any{"videos": []any{"a", "b", "c"}}, ""},
		{"恰好满足", limits, map[string]any{"videos": []any{"a.mp4"}}, ""},
		{"超上限", limits, map[string]any{"videos": []any{"a", "b"}},
			"该模型最多支持连接 1 个视频，当前连接了 2 个"},
		{"低于下限", limits, map[string]any{"videos": []any{}},
			"该模型需要连接至少 1 个视频"},
		{"字段缺失按 0 计", limits, map[string]any{},
			"该模型需要连接至少 1 个视频"},
		{"max 0 禁止连接", map[string]models.InputLimit{"image": {Max: intp(0)}},
			map[string]any{"images": []any{"x.png"}}, "该模型不支持连接图片"},
		{"CLI 路径的 []string 也要认", limits,
			map[string]any{"videos": []string{"a", "b"}},
			"该模型最多支持连接 1 个视频，当前连接了 2 个"},
		{"不认识的约束键忽略", map[string]models.InputLimit{"total": {Max: intp(1)}},
			map[string]any{"videos": []any{"a", "b"}}, ""},
		{"未约束的类型不限", limits,
			map[string]any{"videos": []any{"a"}, "images": []any{"1", "2", "3", "4", "5"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInputLimits(tc.limits, tc.inputs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过, got %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("期望 %q, got %v", tc.wantErr, err)
			}
		})
	}
}

type inputLimitModelLookup struct{ m *models.ModelConfig }

func (f inputLimitModelLookup) GetByModelID(string) (*models.ModelConfig, error) { return f.m, nil }

func TestSubmitRejectsInputLimitViolation(t *testing.T) {
	svc := NewService(nil, inputLimitModelLookup{m: &models.ModelConfig{
		ModelID: "m-video-edit", Enabled: true,
		GenControlRaw: json.RawMessage(`{"inputLimits":{"video":{"min":1,"max":1}}}`),
	}}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Submit(context.Background(), SubmitInput{
		ModelID: "m-video-edit",
		Inputs:  map[string]any{"videos": []any{"a.mp4", "b.mp4"}},
	})
	var ile *InputLimitError
	if !errors.As(err, &ile) {
		t.Fatalf("期望 InputLimitError（在建行/冻结之前拒绝）, got %v", err)
	}
}

// TestSubmitTxRejectsInputLimitViolation：drama 四处调用点（assets_service.go、
// materials_service.go、candidates_service.go、material_candidates.go）都直接调
// SubmitTx，不经过 Submit——tx_submit.go 的接线是这些路径唯一的 inputLimits 拦截点，
// 不是 Submit 侧校验的冗余重复。tx=nil 是安全的：validateInputLimits 之前的每一步
// （GetByModelID/normalize/validateInputsAgainstUISchema）都不碰 tx，第一次真正用 tx
// 在 repo.CreateTx（本用例应在到达之前就已 return err），这条测试不需要真实 MySQL。
func TestSubmitTxRejectsInputLimitViolation(t *testing.T) {
	svc := NewService(nil, inputLimitModelLookup{m: &models.ModelConfig{
		ModelID: "m-video-edit", Enabled: true,
		GenControlRaw: json.RawMessage(`{"inputLimits":{"video":{"min":1,"max":1}}}`),
	}}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.SubmitTx(context.Background(), nil, SubmitInput{
		ModelID: "m-video-edit",
		Inputs:  map[string]any{"videos": []any{"a.mp4", "b.mp4"}},
		Sink:    json.RawMessage(`{}`),
	})
	var ile *InputLimitError
	if !errors.As(err, &ile) {
		t.Fatalf("期望 InputLimitError（在建行/冻结之前拒绝）, got %v", err)
	}
}
