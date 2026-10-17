// Package canvasmanifest 是「画布节点能力清单」的真源。
//
// 它回答两个问题：这个类型的节点有哪些 settings 字段、agent 能对每个字段做什么。
//
// 为什么要有它：在此之前 agent 对画布是盲写——read_canvas 手挑 7 个字段回传（实际
// 在用的有 30+），update_node 除三个机器字段外任意 JSON 放行、写错字段名静默成功。
// planner 写过一个不存在的 `model` 字段，还收到「已提交修改」的回执。静默失败对
// agent 是最坏的失败：它没有任何信号可以据以自我纠正。
//
// **真源是同目录的 manifest.json，不是这个文件里的 Go 结构体。** 浏览器侧有一份
// 字节级相同的副本（apps/web/src/modules/canvas/editor/agent/canvasManifest.json），
// sync_test.go 钉死两份一致。改清单必须两处一起改——同 apps/flow-cli 的
// scriptschema 纪律。用同一份 JSON 而不是两侧各写一份结构体，是因为「字节比对」
// 比「逐字段比对」更难糊弄过去。
package canvasmanifest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed manifest.json
var manifestJSON []byte

// Access 是 agent 对一个字段的权限。三档的区别在**可见性**与**可写性**两个维度：
//
//	open     读得到、写得进
//	readonly 读得到、写入报错——agent 需要它当上下文（比如 script.assets 是 rows
//	         里 @[类型-名称] token 的外键，不给读就写不出合法的分镜提示词），
//	         但改它有副作用，不该用裸写 settings 的方式做
//	machine  连读都不给。回传内部运行时状态只会让 planner 把它当可操作对象，
//	         白吃上下文还诱发误写
type Access string

const (
	AccessOpen     Access = "open"
	AccessReadonly Access = "readonly"
	AccessMachine  Access = "machine"
)

// FieldSpec 是一个 settings 字段的契约。
type FieldSpec struct {
	Access Access `json:"access"`
	Type   string `json:"type"`
	Desc   string `json:"desc"`

	// WholeReplace 标记数组字段必须整体替换（合并语义对数组没有意义）。
	WholeReplace bool `json:"wholeReplace,omitempty"`

	// IDManaged 标记元素 id 由执行侧接管：agent 不需要提供，提供了就复用。
	// script.rows 靠这个保住 gen-video 节点的 scriptRowId 反向锚——锚断了会让
	// 「这行已铺过几个视频节点」的防重复计数恒为 0，用户再点批量铺节点就翻倍，
	// 每个都要花积分生成。这个风险不该由 agent 承担。
	IDManaged bool `json:"idManaged,omitempty"`

	// FullValue 标记这个**只读**字段要给 agent 全文，而不是只报规模。默认只报
	// 规模（video-input.frames 展开能有上百条，纯噪音）；script.assets 是例外——
	// rows 是 open 的，而 row 的 prompt 用 @[类型-名称] 引用资产，不给全文
	// 就等于不给：agent 写不出合法的 token。
	FullValue bool `json:"fullValue,omitempty"`

	// ResetsDynamicFields 标记「改了这个字段，该节点的动态字段要重置为新的默认
	// 值」。目前只有 modelId：手动切模型时前端做的是
	// updateNodeSettings(id, { modelId, ...modelSettingDefaults(uiSchema) })，
	// agent 裸写只改 modelId 会把旧模型的分辨率档位留在新模型上。
	ResetsDynamicFields bool `json:"resetsDynamicFields,omitempty"`
}

// NodeSpec 是一个节点类型的字段契约。
type NodeSpec struct {
	// DynamicFieldsFrom 非空表示该类型除了 Fields 里的静态字段，还有一批字段来自
	// 别处、值域运行时才知道。目前唯一取值 "modelUiSchema"——比例/分辨率/时长
	// 这些由所选模型的 uiSchema（后台配的 JSON）决定。
	//
	// 动态字段**不要**在 Fields 里重复声明。ratio 一度两边都有，结果是：静态
	// 清单认它 open 就直接放行，值域校验永远走不到，模型没配 uiSchema 时也能写
	// 进一个前端根本不渲染的字段。判据是「agent 能做的 = UI 能做的」——UI 的
	// 比例控件只在模型配了 ratio 时才出现，agent 也该如此。
	DynamicFieldsFrom string               `json:"dynamicFieldsFrom,omitempty"`
	Fields            map[string]FieldSpec `json:"fields"`
}

// Manifest 是整份清单。
type Manifest struct {
	Version int `json:"version"`
	// CommonFields 适用于所有节点类型，不必在每个类型里重复。
	CommonFields map[string]FieldSpec `json:"commonFields"`
	NodeTypes    map[string]NodeSpec  `json:"nodeTypes"`
}

var loaded Manifest

func init() {
	if err := json.Unmarshal(manifestJSON, &loaded); err != nil {
		// 清单是 go:embed 进来的静态资源，解析失败 = 构建产物坏了，早死早超生。
		panic(fmt.Sprintf("canvasmanifest: manifest.json 解析失败: %v", err))
	}
}

// Get 返回整份清单。
func Get() Manifest { return loaded }

// Raw 返回清单原文，供同构测试比对。
func Raw() []byte { return manifestJSON }

// Field 查一个节点类型的某个字段的契约。类型专属字段优先于通用字段——同名时
// 前者赢（目前没有这种重叠，但顺序要明确，不然加字段时是个静默陷阱）。
func Field(nodeType, field string) (FieldSpec, bool) {
	if spec, ok := loaded.NodeTypes[nodeType]; ok {
		if f, ok := spec.Fields[field]; ok {
			return f, true
		}
	}
	f, ok := loaded.CommonFields[field]
	return f, ok
}

// KnownNodeType 报告清单是否认识这个节点类型。注意它与「agent 能否创建该类型」
// 是两回事：清单覆盖全部 14 种（read_canvas 要读得懂画布上的任何节点），可创建
// 集合另有一张表（tools_canvas_ops.go 的 knownNodeTypes，目前 8 种）。加字段时
// 别把「字段可写」误推成「类型可建」。
func KnownNodeType(nodeType string) bool {
	_, ok := loaded.NodeTypes[nodeType]
	return ok
}

// VisibleFields 返回该类型下 agent 读得到的字段（open + readonly），按名字排序。
// machine 字段不在其中——它们连读都不给。
func VisibleFields(nodeType string) []string {
	out := make([]string, 0, 8)
	collect := func(m map[string]FieldSpec) {
		for name, f := range m {
			if f.Access == AccessMachine {
				continue
			}
			out = append(out, name)
		}
	}
	if spec, ok := loaded.NodeTypes[nodeType]; ok {
		collect(spec.Fields)
	}
	for name, f := range loaded.CommonFields {
		if f.Access == AccessMachine {
			continue
		}
		// 类型专属同名字段已经收过，不重复。
		if spec, ok := loaded.NodeTypes[nodeType]; ok {
			if _, dup := spec.Fields[name]; dup {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// WritableFields 返回该类型下 agent 写得进的字段（open），按名字排序。
func WritableFields(nodeType string) []string {
	out := make([]string, 0, 8)
	if spec, ok := loaded.NodeTypes[nodeType]; ok {
		for name, f := range spec.Fields {
			if f.Access == AccessOpen {
				out = append(out, name)
			}
		}
	}
	for name, f := range loaded.CommonFields {
		if f.Access != AccessOpen {
			continue
		}
		if spec, ok := loaded.NodeTypes[nodeType]; ok {
			if _, dup := spec.Fields[name]; dup {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HasDynamicFields 报告该类型是否有随所选模型变化的动态字段。
func HasDynamicFields(nodeType string) bool {
	spec, ok := loaded.NodeTypes[nodeType]
	return ok && spec.DynamicFieldsFrom != ""
}
