package canvasgen

import "encoding/json"

// SettingCanvasPostprocess 是画布去字幕白名单在 settings 表里的 key。
// 画布模块与项目管理(drama)平级隔离——各自独立的 settings key，互不复用。
const SettingCanvasPostprocess = "canvas_postprocess_config"

// CanvasDesubBinding 是后台配置的一条去字幕模型绑定。
type CanvasDesubBinding struct {
	ModelID       string `json:"modelId"`
	Label         string `json:"label"`
	Subtitle      string `json:"subtitle"`
	IsTimeLimited bool   `json:"isTimeLimited"`
}

// CanvasUpscaleBinding 是后台配置的一条转高清模型绑定。
// 档位不在这里配——由该模型 uiSchema 的 resolution.options 决定，换模型档位跟着换。
type CanvasUpscaleBinding struct {
	ModelID string `json:"modelId"`
	// Label 是节点下拉框里的显示名。转高清模型在模型中心通常是隐藏的，那边的名字是
	// 给管理员看的内部名，不适合直接给用户看，故单独配；空则前端显示 modelId。
	Label string `json:"label"`
}

// CanvasPostprocessConfig 是画布后处理(去字幕 + 转高清 + 转白模)的整份配置。
type CanvasPostprocessConfig struct {
	SubtitleRemovalModels []CanvasDesubBinding `json:"subtitleRemovalModels"`
	// UpscaleModels 是转高清模型白名单，节点下拉框按此顺序列出、默认选第一个；空 = 未配置。
	// 曾经是单个 UpscaleModel（「转高清只选档位不换模型」），后来要让用户在模型间比价选择，
	// 改成与去字幕同形的列表。
	UpscaleModels []CanvasUpscaleBinding `json:"upscaleModels"`
	// UpscaleModel 是旧版单模型字段，只为读存量配置而保留：LoadCanvasPostprocessConfig
	// 会把它折进 UpscaleModels 并清空，业务代码不要读它。不用 SQL 迁移而是读时转换，
	// 是为了上线当下老配置即可用、蓝绿两色读同一份 settings 都不出错。
	UpscaleModel string `json:"upscaleModel,omitempty"`
	// WhiteModelModel 是转白模模型的 modelID(单模型 + 分辨率档位)；空 = 未配置。
	WhiteModelModel string `json:"whiteModelModel"`
}

// LoadCanvasPostprocessConfig 读画布后处理配置;空(未配置)返回空配置,不报错。
func LoadCanvasPostprocessConfig(get func(key string) (string, error)) (CanvasPostprocessConfig, error) {
	raw, err := get(SettingCanvasPostprocess)
	if err != nil {
		return CanvasPostprocessConfig{}, err
	}
	if raw == "" {
		return CanvasPostprocessConfig{}, nil
	}
	var cfg CanvasPostprocessConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return CanvasPostprocessConfig{}, err
	}
	// 新列表优先：管理员用新后台保存过就只认列表，旧字段即使残留也不再生效。
	if len(cfg.UpscaleModels) == 0 && cfg.UpscaleModel != "" {
		cfg.UpscaleModels = []CanvasUpscaleBinding{{ModelID: cfg.UpscaleModel}}
	}
	cfg.UpscaleModel = ""
	return cfg, nil
}

// UpscaleBinding 按 modelID 找转高清绑定。空 modelID 恒不命中——防止空串 == 空串放行。
func (c CanvasPostprocessConfig) UpscaleBinding(modelID string) (CanvasUpscaleBinding, bool) {
	if modelID == "" {
		return CanvasUpscaleBinding{}, false
	}
	for _, u := range c.UpscaleModels {
		if u.ModelID == modelID {
			return u, true
		}
	}
	return CanvasUpscaleBinding{}, false
}

// DesubModel 按 modelID 找去字幕绑定。
func (c CanvasPostprocessConfig) DesubModel(modelID string) (CanvasDesubBinding, bool) {
	for _, d := range c.SubtitleRemovalModels {
		if d.ModelID == modelID {
			return d, true
		}
	}
	return CanvasDesubBinding{}, false
}
