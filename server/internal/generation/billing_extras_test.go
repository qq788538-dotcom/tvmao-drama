package generation

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// 额外收费规则(billing_config.extras)的条件字段在**冻结**与**结算**两侧必须给出相同判定，
// 否则会出现「冻 1 倍、结 1.8 倍」扣穿余额，或反过来多冻用户的钱。
//
// 两侧喂给计费引擎的 req 并不是同一个对象：
//   - 冻结用 normalizedInputs（经 signInputMediaReferences + prepareImageInputs）
//   - 同步结算用调用方传进 finalize 的裸 inputs
//   - 异步轮询 / 孤儿恢复结算用 task 快照，即 sanitizeTaskSnapshot(inputs)
//
// 三者的字符串**内容**会变（media:// → 签名 URL、超长 base64 → 占位串），但只要数组的
// **长度**不变，hasValue 的判定就一致。本测试钉死的就是这条：谁把 sanitize 改成会增删
// 元素（比如"顺手"过滤掉被脱敏的项），立刻炸在这里，而不是在生产上变成静默少收。
func TestSanitizeTaskSnapshot_PreservesExtrasVerdict(t *testing.T) {
	longBase64 := base64.StdEncoding.EncodeToString(make([]byte, 4096))

	cases := []struct {
		name   string
		inputs map[string]any
		want   bool // 期望的「有值」判定
	}{
		{
			name:   "普通 URL 视频参考",
			inputs: map[string]any{"videos": []any{"https://cdn/a.mp4"}},
			want:   true,
		},
		{
			name:   "视频参考是超长 base64(会被脱敏成占位串，但元素还在)",
			inputs: map[string]any{"videos": []any{longBase64}},
			want:   true,
		},
		{
			name:   "data-uri 图片参考(会被脱敏)",
			inputs: map[string]any{"images": []any{"data:image/png;base64," + longBase64}},
			want:   true,
		},
		{
			name:   "多个参考里混着待脱敏项",
			inputs: map[string]any{"videos": []any{"https://cdn/a.mp4", longBase64}},
			want:   true,
		},
		{
			name:   "空数组",
			inputs: map[string]any{"videos": []any{}},
			want:   false,
		},
		{
			name:   "字段缺失",
			inputs: map[string]any{"prompt": "x"},
			want:   false,
		},
		{
			name:   "CLI 侧的 []string 形态",
			inputs: map[string]any{"videos": []string{"https://cdn/a.mp4"}},
			want:   true,
		},
	}

	cfg := billing.ModelBilling{
		FlatFee: 100,
		Extras: []billing.ExtraCharge{
			{When: "req:inputs.videos", Op: billing.OpHasValue, Factor: 1.8},
			{When: "req:inputs.images", Op: billing.OpHasValue, Factor: 1.8},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 冻结侧：原始 inputs。
			freeze := billing.EstimateFreeze(cfg, map[string]any{"inputs": c.inputs})
			// 异步结算侧：走 task 快照那条路。
			snapshot := sanitizeTaskSnapshot(c.inputs)
			settle, err := billing.SettleAmount(cfg, map[string]any{"inputs": snapshot}, nil)
			if err != nil {
				t.Fatalf("SettleAmount err: %v", err)
			}

			wantAmount := int64(100)
			if c.want {
				wantAmount = 180
			}
			if freeze != wantAmount {
				t.Fatalf("冻结额 = %d, want %d", freeze, wantAmount)
			}
			if settle != freeze {
				t.Fatalf("结算额 %d ≠ 冻结额 %d —— sanitize 改变了 extras 判定", settle, freeze)
			}
		})
	}
}

// 上一条测试依赖「脱敏确实发生了」才有意义——否则它可能只是证明了「没被改的数据判定一致」
// 这种废话。这里显式确认脱敏真的把内容换掉了，同时数组长度没变。
func TestSanitizeTaskSnapshot_RedactsButKeepsLength(t *testing.T) {
	longBase64 := base64.StdEncoding.EncodeToString(make([]byte, 4096))
	in := map[string]any{"videos": []any{"https://cdn/a.mp4", longBase64}}

	out := sanitizeTaskSnapshot(in)
	arr, ok := out["videos"].([]any)
	if !ok {
		t.Fatalf("videos 类型变了: %T", out["videos"])
	}
	if len(arr) != 2 {
		t.Fatalf("数组长度 = %d, want 2 —— sanitize 增删了元素，extras 判定会与冻结侧不一致", len(arr))
	}
	if arr[0] != "https://cdn/a.mp4" {
		t.Fatalf("普通 URL 不该被动: %v", arr[0])
	}
	if s, _ := arr[1].(string); !strings.HasPrefix(s, "[redacted") {
		t.Fatalf("超长 base64 应被脱敏, got %.40q", s)
	}
}
