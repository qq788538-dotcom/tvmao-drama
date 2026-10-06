package agent

import "errors"

// ErrBackboneNotFound：按 id 找不到 agent_backbone_models 行。
var ErrBackboneNotFound = errors.New("agent backbone model not found")

// BackboneModelSwitcher 是设置页切换 Agent 大模型（spec A2）需要的写能力。
// 只动 model_name，其余参数内置、不可改。实现见 backbone_switch.go。
type BackboneModelSwitcher interface {
	// SetModelName 改 model_name 并标记 model_name_overridden=1（内置数据升级不再覆盖）。
	SetModelName(id int64, modelName string) error
	// ResetModelName 把 model_name 恢复为内置默认值并清除标记。
	ResetModelName(id int64, defaultName string) error
}
