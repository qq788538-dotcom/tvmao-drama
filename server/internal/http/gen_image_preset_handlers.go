package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/james/huabu_studio/apps/api/internal/canvasgen"
	"github.com/james/huabu_studio/apps/api/internal/settings"
)

// 绘图提示词预设：整块存 settings 一个 KV（先例 cli_release / canvas_postprocess_config）。
// 预设正文（value.prompt）是运营资产，对前端保密——用户端点白名单剥字段后才下发，
// 提交生成只传 key，key → 正文的替换在服务端执行层完成
// （设计文档 docs/2026-08-14-gen-image-prompt-presets-design.md 需求第 9/10 条）。

// genImagePresetKeyRe：key 是程序内部稳定标识（落库/对账用，用户永远看不到），
// 收紧成小写蛇形避免出现「像 name 一样随手改」的诱惑。
var genImagePresetKeyRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// genImagePresetPublicFields 是用户端点下发 value 时的字段白名单。
// 用白名单而非「删掉 prompt」的黑名单：将来 value 里再加敏感字段，黑名单忘了删就
// 泄露；白名单忘了加只是前端少个展示字段，错的方向是安全的。
var genImagePresetPublicFields = []string{
	"name", "category", "icon", "description", "placeholder", "minImages", "maxImages", "sort",
}

type GenImagePresetHandler struct {
	settings settings.Repository
}

func NewGenImagePresetHandler(repo settings.Repository) *GenImagePresetHandler {
	return &GenImagePresetHandler{settings: repo}
}

// load 读配置；未设置/损坏 → 空配置（不 500：后台首次进入要能渲染，用户端菜单隐藏即可）。
func (h *GenImagePresetHandler) load() canvasgen.GenImagePresetsConfig {
	cfg, _ := canvasgen.LoadGenImagePresetsConfig(h.settings.Get)
	if cfg.Models == nil {
		cfg.Models = []string{}
	}
	if cfg.Presets == nil {
		cfg.Presets = []canvasgen.GenImagePresetEntry{}
	}
	return cfg
}

// AdminGet GET /api/v1/admin/canvas/gen-image-presets —— 回显全量（含 prompt），admin-only。
func (h *GenImagePresetHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.load())
}

// AdminPut PUT /api/v1/admin/canvas/gen-image-presets —— 整体覆盖。
// 只校验会让运行时出错的硬伤（key 冲突/缺 name/缺 prompt/图片数约束自相矛盾），
// value 的其余字段原样透传——扩展位不设白名单，后台加新字段不用改后端。
func (h *GenImagePresetHandler) AdminPut(w http.ResponseWriter, r *http.Request) {
	var body canvasgen.GenImagePresetsConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Models == nil {
		body.Models = []string{}
	}
	if body.Presets == nil {
		body.Presets = []canvasgen.GenImagePresetEntry{}
	}
	for i, m := range body.Models {
		body.Models[i] = strings.TrimSpace(m)
		if body.Models[i] == "" {
			writeError(w, http.StatusBadRequest, "模型 ID 不能为空")
			return
		}
	}
	seen := map[string]bool{}
	for i, p := range body.Presets {
		if !genImagePresetKeyRe.MatchString(p.Key) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条：key 必须是 1-64 位小写字母/数字/下划线/连字符", i+1))
			return
		}
		if seen[p.Key] {
			writeError(w, http.StatusBadRequest, "key 重复："+p.Key)
			return
		}
		seen[p.Key] = true
		if p.Value == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：value 必须是 JSON 对象", i+1, p.Key))
			return
		}
		if strings.TrimSpace(p.Name()) == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：value.name 不能为空", i+1, p.Key))
			return
		}
		if strings.TrimSpace(p.Prompt()) == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：value.prompt 不能为空", i+1, p.Key))
			return
		}
		// 图片数约束若配了就得能用：负数/min>max 会让所有提交都被拒，属于配置硬伤。
		if err := validatePresetImageBounds(p); err != "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：%s", i+1, p.Key, err))
			return
		}
		if err := validatePresetParams(p); err != "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("第 %d 条（%s）：%s", i+1, p.Key, err))
			return
		}
	}
	blob, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.settings.Set(canvasgen.SettingGenImagePresets, string(blob)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// validatePresetImageBounds 校验 minImages/maxImages：允许缺省，配了必须是非负数字且 min<=max。
// 返回空串 = 合法；非空 = 面向后台运营的错误文案。
func validatePresetImageBounds(p canvasgen.GenImagePresetEntry) string {
	check := func(field string) (float64, bool, string) {
		raw, exists := p.Value[field]
		if !exists {
			return 0, false, ""
		}
		n, ok := raw.(float64)
		if !ok || n < 0 || n != float64(int(n)) {
			return 0, true, field + " 必须是非负整数"
		}
		return n, true, ""
	}
	minV, hasMin, errMsg := check("minImages")
	if errMsg != "" {
		return errMsg
	}
	maxV, hasMax, errMsg := check("maxImages")
	if errMsg != "" {
		return errMsg
	}
	if hasMin && hasMax && minV > maxV {
		return "minImages 不能大于 maxImages"
	}
	return ""
}

// validatePresetParams 校验参数定义（value.params，选填）：维度/档位的 key 与
// name 是运行时寻址与渲染的硬依赖，缺了整个面板瘫痪；档位 prompt 允许为空
// （「无效果」档位是合法配置）。value.params 解析不动（形状不对）也算硬伤——
// 静默当没有会让运营以为配置生效了。
func validatePresetParams(p canvasgen.GenImagePresetEntry) string {
	raw, exists := p.Value["params"]
	if !exists || raw == nil {
		return ""
	}
	params := p.Params()
	if params == nil {
		return "params 不是合法的参数定义数组"
	}
	seenDim := map[string]bool{}
	for di, dim := range params {
		if !genImagePresetKeyRe.MatchString(dim.Key) {
			return fmt.Sprintf("params 第 %d 个维度：key 必须是 1-64 位小写字母/数字/下划线/连字符", di+1)
		}
		if seenDim[dim.Key] {
			return "params 维度 key 重复：" + dim.Key
		}
		seenDim[dim.Key] = true
		if strings.TrimSpace(dim.Name) == "" {
			return fmt.Sprintf("params 维度 %s：name 不能为空", dim.Key)
		}
		if len(dim.Options) == 0 {
			return fmt.Sprintf("params 维度 %s：至少要有一个档位", dim.Key)
		}
		seenOpt := map[string]bool{}
		for oi, o := range dim.Options {
			if !genImagePresetKeyRe.MatchString(o.Key) {
				return fmt.Sprintf("params 维度 %s 第 %d 个档位：key 必须是 1-64 位小写字母/数字/下划线/连字符", dim.Key, oi+1)
			}
			if seenOpt[o.Key] {
				return fmt.Sprintf("params 维度 %s：档位 key 重复 %s", dim.Key, o.Key)
			}
			seenOpt[o.Key] = true
			if strings.TrimSpace(o.Name) == "" {
				return fmt.Sprintf("params 维度 %s 档位 %s：name 不能为空", dim.Key, o.Key)
			}
		}
	}
	return ""
}

// UserGet GET /api/v1/canvas/gen-image-presets —— 登录用户可读，供绘图节点渲染预设菜单。
// value 走白名单剥离后下发：prompt 正文绝不出现在这个响应里。
func (h *GenImagePresetHandler) UserGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.load()
	presets := make([]map[string]any, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		v := map[string]any{}
		for _, f := range genImagePresetPublicFields {
			if raw, ok := p.Value[f]; ok {
				v[f] = raw
			}
		}
		// params 不能走平铺白名单：维度/档位的名字要下发（前端渲染参数面板），
		// 但每个档位的 prompt 片段与预设正文同级保密——重建一份只含展示字段的结构。
		if params := p.Params(); len(params) > 0 {
			outParams := make([]map[string]any, 0, len(params))
			for _, dim := range params {
				opts := make([]map[string]any, 0, len(dim.Options))
				for _, o := range dim.Options {
					m := map[string]any{"key": o.Key, "name": o.Name}
					if o.Default {
						m["default"] = true
					}
					opts = append(opts, m)
				}
				outParams = append(outParams, map[string]any{"key": dim.Key, "name": dim.Name, "options": opts})
			}
			v["params"] = outParams
		}
		presets = append(presets, map[string]any{"key": p.Key, "value": v})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": cfg.Models, "presets": presets})
}
