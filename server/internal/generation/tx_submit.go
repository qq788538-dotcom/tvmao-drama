package generation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// SubmitTx：在调用方的事务里建行 + 冻结（v5 §5.4，保证 1）。
//
// 与 Submit 的关键差别：
//   - 建行与 FreezeTx 同一个 tx——「建了行没冻结」「冻结了没建行」两种中间态
//     随事务原子性消失，pending_freeze 状态在这条路径上不存在；
//   - 行以 status='queued' 出生：行只在 Commit 时对外可见，队列 worker 看到它时
//     钱必然已冻结，不变量「worker 绝不领到没扣钱的任务」由原子性而非状态机保证；
//   - **事务内零网络 IO**：参考图签名/预取属于执行期材料（队列 worker 的
//     prepareExecution 会重建），这里只做纯计算与 DB 查询——持锁跨网络调用会把
//     行锁抓在手里等一个不受控的远端。
//
// 只接受带 sink 的任务（队列模式）：sinkless 直连是同步语义，调用方在原地等结果，
// 与「入队后由 worker 执行」不兼容，走 Submit。
//
// 崩溃/失败语义：任何一步失败调用方回滚 → 无孤儿行、无孤儿冻结；
// 余额不足原样透传 billing.ErrInsufficientBalance（errors.Is 可判）。
func (s *Service) SubmitTx(ctx context.Context, tx *sql.Tx, input SubmitInput) (*SubmitResult, error) {
	if len(input.Sink) == 0 {
		return nil, errors.New("SubmitTx 只接受带 sink 的队列任务；sinkless 直连请走 Submit")
	}
	if input.ModelID == "" {
		return nil, errors.New("modelId required")
	}
	model, err := s.models.GetByModelID(input.ModelID)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	if !model.Enabled {
		return nil, errors.New("model disabled")
	}

	// 与 Submit 相同的输入归一化链（全部纯计算/DB 查询，见 Submit 中各步 WHY）。
	input.Inputs = normalizeInputsToUISchema(model.UISchema, input.Inputs)

	// 归一化后紧跟白名单校验：越界枚举值在冻结前拒绝，不建行、不冻结。
	if err := validateInputsAgainstUISchema(model.UISchema, input.Inputs); err != nil {
		return nil, fmt.Errorf("输入校验失败: %w", err)
	}

	// 输入数量约束（gen_control.inputLimits，spec 2026-08-12）：与 uiSchema 白名单
	// 同一梯队，违约在建行/冻结之前拒绝。未配置的模型 len==0 直接放行。
	if err := validateInputLimits(model.InputLimits(), input.Inputs); err != nil {
		return nil, err
	}

	// 计费时长注入（gen_control.inputDuration，spec 2026-08-12）：与 Submit 同一段
	// 注入逻辑。sink 路径双跑覆盖为同值，幂等，随既有 normalize/validate 链先例，
	// 不单独优化——注意快照在其后 marshal，注入值随任务持久化，worker 重建不再探测。
	if err := s.resolveInputDuration(ctx, model.InputDuration(), input.Inputs); err != nil {
		return nil, err
	}
	if s.realPerson != nil && strings.HasPrefix(input.ModelID, "doubao-seedance") {
		replaced, err := s.replaceRealPersonImageRefs(input.UserID, input.Inputs)
		if err != nil {
			return nil, err
		}
		input.Inputs = replaced
	}
	if containsDataURI(input.Inputs) {
		return nil, ErrDataURIInput
	}

	// 存储闸：必须在建行/冻结之前（产物落库阶段绝不拦——那时积分已扣，
	// 拦 = 用户花钱拿不到图，见 ErrStorageQuotaExceeded 注释）。此处拒绝时
	// 事务内零写入，调用方回滚无残留。
	if err := s.ensureStorageQuota(input.UserID); err != nil {
		return nil, err
	}

	// 估价用归一化输入即可：计费只读 inputs.duration / inputs.n 等参数字段，
	// 不依赖执行期的图片材料准备（Estimate 接口同款口径）。
	freezeAmt := billing.EstimateFreeze(model.BillingConfig(), map[string]any{"inputs": input.Inputs})
	note := ledgerNote(model, input.Note)

	// 快照只存 worker 重建执行所需的 {modelId, inputs, note}——payload 是执行期产物，
	// 队列任务由 prepareExecution 现场重算，快照里存它只会引入第二真源。
	inputCapture := map[string]any{
		"modelId": input.ModelID,
		"inputs":  sanitizeTaskSnapshot(input.Inputs),
		"note":    input.Note,
	}
	// 预设只存 key（正文对前端保密，且 GET /tasks 原样吐回本快照）；
	// 空值不落键，存量任务快照形状保持逐字节不变。
	if input.PresetKey != "" {
		inputCapture["presetKey"] = input.PresetKey
		if len(input.PresetParams) > 0 {
			inputCapture["presetParams"] = input.PresetParams
		}
	}
	inputBytes, _ := json.Marshal(inputCapture)

	taskID, err := s.repo.CreateTx(tx, Task{
		UserID:      input.UserID,
		ProjectID:   input.ProjectID,
		ModelID:     input.ModelID,
		Status:      statusQueued,
		Input:       inputBytes,
		FrozenCents: freezeAmt,
		Sink:        input.Sink,
	})
	if err != nil {
		return nil, fmt.Errorf("persist task: %w", err)
	}

	if _, err := s.billing.FreezeTx(ctx, tx, input.UserID, taskID, freezeAmt, note); err != nil {
		// 不 MarkFailed 留痕：整个事务由调用方回滚，行根本不会存在。
		return nil, err
	}
	return &SubmitResult{TaskID: taskID, Status: statusQueued, FrozenCents: freezeAmt}, nil
}
