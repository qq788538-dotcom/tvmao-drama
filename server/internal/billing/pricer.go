package billing

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Price 是一个计费项的单价：固定值 Fixed，或按档位字段查表 Table（键为 By 各字段值用 | 拼接）。
// JSON 两种写法都支持：裸数字 `36`(=Fixed) 或对象 `{"by":[...],"table":{...}}`。
type Price struct {
	Fixed   float64            `json:"fixed,omitempty"`
	By      []string           `json:"by,omitempty"`
	Table   map[string]float64 `json:"table,omitempty"`
	Default *float64           `json:"default,omitempty"`
	// 区间分档(整段按落入档的单价，如上下文长度 ≤64K/64-128K/>128K)：
	// ByRange 指一个数值字段(req:/resp:)，Tiers 升序，落入第一个 value≤MaxTokens 的档；
	// MaxTokens=nil 的档为兜底(匹配任意，须放最后)。
	ByRange string      `json:"byRange,omitempty"`
	Tiers   []PriceTier `json:"tiers,omitempty"`
}

// PriceTier 区间分档的一档。MaxTokens 为该档数值上限(含)；nil=兜底档(匹配任意)。
type PriceTier struct {
	MaxTokens *float64 `json:"maxTokens,omitempty"`
	Price     float64  `json:"price"`
}

// MarshalJSON：纯固定价输出裸数字(给 admin/回填产出干净 JSON)，否则输出对象。
func (p Price) MarshalJSON() ([]byte, error) {
	if len(p.By) == 0 && p.Table == nil && p.Default == nil && p.ByRange == "" && len(p.Tiers) == 0 {
		return json.Marshal(p.Fixed)
	}
	type alias Price
	return json.Marshal(alias(p))
}

// UnmarshalJSON：裸数字→Fixed；对象→各字段。
func (p *Price) UnmarshalJSON(b []byte) error {
	var n float64
	if json.Unmarshal(b, &n) == nil {
		*p = Price{Fixed: n}
		return nil
	}
	type alias Price
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Price(a)
	return nil
}

// ChargeItem 一个计费项：qty/divisor(由 unit 定) × price。
// BlockSize>0 时按段计量：qty 先变成 ceil(qty/blockSize)，不足一段按一段，
// price 即每段单价(如每 100 字 10 积分 → blockSize:100, price:10)。0/缺省 = 不分段。
type ChargeItem struct {
	Unit      string  `json:"unit"`
	Qty       string  `json:"qty"`
	BlockSize float64 `json:"blockSize,omitempty"`
	Price     Price   `json:"price"`
}

// ExtraCharge 是「某入参有值时额外收费」的规则：命中则对基础价施加倍率与/或附加积分。
// 典型用法是 doubao-seedance 传了参考视频时整体 ×1.8——这类加价没法用 price.by 档位表
// 表达，因为参考素材落在 inputs.videos 这样的**数组**里，档位表的 stringAt 对数组一律
// 取到 ""，空数组和非空数组分不开(见 README §「extras」)。
//
// When 只认 req: 前缀：EstimateFreeze 手里只有请求。若允许 resp:，冻结阶段判不出条件、
// 结算阶段才判出来，会出现「冻 1 倍、结 1.8 倍」——用户余额刚够冻结却在结算时扣穿。
type ExtraCharge struct {
	When   string  `json:"when"`             // 条件字段，req: 前缀 + 点分路径，如 req:inputs.videos
	Op     string  `json:"op,omitempty"`     // 条件类型，缺省 = OpHasValue(当前唯一实现)
	Factor float64 `json:"factor,omitempty"` // 倍率，0/缺省 = 不乘
	Add    float64 `json:"add,omitempty"`    // 附加积分，0/缺省 = 不加
	Note   string  `json:"note,omitempty"`   // 后台备注，不参与计算
}

// OpHasValue 判「该字段有值」：数组/字符串/map 非空、数字非 0、bool 为 true；
// 字段缺失或 null = 没值。
const OpHasValue = "hasValue"

// ModelBilling 是解析后的 billing_config。
type ModelBilling struct {
	FlatFee        int64        `json:"flatFee"`
	FreezeEstimate int64        `json:"freezeEstimate"`
	Items          []ChargeItem `json:"items"`
	// Extras 缺省为 nil = 该模型完全不走额外收费逻辑(EstimateFreeze/SettleAmount 里直接
	// 短路)，连 ×1.0 都不做——现网模型一个都没配，这样才谈得上「不影响现有功能」。
	Extras []ExtraCharge `json:"extras,omitempty"`
}

const maxSettleCredits = 1 << 53

func divisor(unit string) float64 {
	if unit == "token" {
		return 1_000_000
	}
	return 1
}

// reqResolvable 报告该项能否仅凭请求(req)算出(用于发起时精确预冻结)：qty、所有 price.by 字段、以及 price.byRange(若有) 都须 req 可取。
func reqResolvable(it ChargeItem) bool {
	if !(it.Qty == "flat" || it.Qty == "chars.input" || strings.HasPrefix(it.Qty, "req:")) {
		return false
	}
	for _, f := range it.Price.By {
		if !strings.HasPrefix(f, "req:") {
			return false
		}
	}
	if it.Price.ByRange != "" && !strings.HasPrefix(it.Price.ByRange, "req:") {
		return false
	}
	if len(it.Price.Tiers) > 0 && it.Price.ByRange == "" {
		return false // tiers 无 byRange 为非法配置，结算会报错，不计入精确预冻结
	}
	return true
}

// SettleAmount 按计费项求和算实际应扣积分(整数)。req/resp 任一可为 nil。
func SettleAmount(m ModelBilling, req, resp map[string]any) (int64, error) {
	total := float64(m.FlatFee)
	for i := range m.Items {
		v, err := itemAmount(m.Items[i], req, resp)
		if err != nil {
			return 0, err
		}
		total += v
	}
	if len(m.Extras) == 0 {
		return ceilCredits(total) // 未配 extras：与加这个特性之前逐字节相同的路径
	}
	factor, add := extrasEffect(m.Extras, req)
	return ceilCredits(total*factor + add)
}

// EstimateFreeze 发起时预冻结：req 可算的项精确算 + flatFee + freezeEstimate(覆盖 resp-only 项)。
func EstimateFreeze(m ModelBilling, req map[string]any) int64 {
	total := float64(m.FlatFee)
	for i := range m.Items {
		if !reqResolvable(m.Items[i]) {
			continue
		}
		if v, err := itemAmount(m.Items[i], req, nil); err == nil {
			total += v
		}
	}
	if len(m.Extras) == 0 {
		amt, err := ceilCredits(total) // 未配 extras：与加这个特性之前逐字节相同的路径
		if err != nil {
			amt = 0
		}
		return amt + m.FreezeEstimate
	}
	factor, add := extrasEffect(m.Extras, req)
	amt, err := ceilCredits(total*factor + add)
	if err != nil {
		amt = 0
	}
	// freezeEstimate 同样要乘倍率：它兜的是 resp-only 项(token 等)的上限，而 settle 会把
	// 那些项一起乘上倍率。这里不乘的话，token 型模型配了 extras 就会「冻得比结算少」，
	// 结算时扣穿余额。add 不再加一遍——上面 total 里已经加过。
	return amt + int64(math.Ceil(float64(m.FreezeEstimate)*factor))
}

// extrasEffect 求所有命中规则的合并效果：倍率连乘、附加值相加。
// 未命中任何规则时返回 (1, 0)，即不改变基础价。
//
// 为什么 add 不参与连乘、而是加在乘法之外(见调用处 total*factor + add)：add 表达的是
// 「这个条件带来的固定成本」，不该被倍率再放大一遍；两个数字互不干扰，单独调其中一个
// 不会牵动另一个。
func extrasEffect(extras []ExtraCharge, req map[string]any) (factor, add float64) {
	factor, add = 1, 0
	for _, e := range extras {
		if !matchExtra(e, req) {
			continue
		}
		if e.Factor > 0 {
			factor *= e.Factor
		}
		add += e.Add
	}
	return factor, add
}

// matchExtra 判一条规则是否命中。
//
// **本函数刻意不返回 error**，与引擎其余部分「取不到值就报错、绝不静默扣 0」的铁律相反。
// 理由：SettleAmount 报错会走 generation.finalize 的 settle_fallback_freeze 分支——按冻结额
// 收——于是一条配错的加价规则不会炸出来，而是变成**静默少收**那部分加价，只在日志里留一行。
// 所以这里反过来做：条件求值全域有定义(hasValue 对任何值都给得出 true/false)，配置合法性
// 由 ValidateExtras 在后台保存时前置拦截，运行期没有出错的可能。
func matchExtra(e ExtraCharge, req map[string]any) bool {
	if !strings.HasPrefix(e.When, "req:") || req == nil {
		return false
	}
	switch e.Op {
	case "", OpHasValue:
		return hasValue(valueAt(req, strings.TrimPrefix(e.When, "req:")))
	}
	return false // 未知 op：ValidateExtras 已拦在保存前，这里兜底为不加价
}

// hasValue 判一个 JSON 值算不算「有值」。
//
// 用 reflect 而不是穷举 case：同一份 inputs 有两种来源——网页走 HTTP JSON 反序列化
// (数组是 []any)，CLI 走 canvasinputs.BuildVideoInputs 在 Go 侧直接构造(数组是 []string)。
// 穷举类型必然漏掉一边，而漏掉的那边会静默判成「没值」→ 不加价。
func hasValue(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return rv.Len() > 0
	case reflect.Bool:
		return rv.Bool()
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() != 0
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return hasValue(rv.Elem().Interface())
	}
	return true // 其它类型(结构体等)：非 nil 即算有值
}

// valueAt 按点分路径取原始值(不做标量转换)。
//
// 没有复用 numberAt/stringAt，也没有把它们重构到这个函数上：那两个是现网所有计费的
// 取值口，本次改动的硬约束是「没配 extras 的模型走原路径」，动它们就等于把风险漏进
// 存量计费。多这十行换 diff 完全关在新代码里，划算。
func valueAt(m map[string]any, path string) any {
	cur := any(m)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[seg]
	}
	return cur
}

// ValidateExtras 校验额外收费规则的合法性，供后台保存模型时前置调用。
//
// 这是 matchExtra「运行期不报错」策略的另一半：所有会让加价算不准的配置错误都必须在
// 这里挡住，存不进库，否则就会变成生产上的静默少收/多收。
func ValidateExtras(extras []ExtraCharge) error {
	for i, e := range extras {
		n := i + 1
		if !strings.HasPrefix(e.When, "req:") {
			return fmt.Errorf("额外收费规则 #%d：参数路径必须以 req: 开头(如 req:inputs.videos)", n)
		}
		if strings.TrimSpace(strings.TrimPrefix(e.When, "req:")) == "" {
			return fmt.Errorf("额外收费规则 #%d：参数路径不能为空", n)
		}
		if e.Op != "" && e.Op != OpHasValue {
			return fmt.Errorf("额外收费规则 #%d：不支持的条件类型 %q", n, e.Op)
		}
		if e.Factor == 0 && e.Add == 0 {
			return fmt.Errorf("额外收费规则 #%d：倍率与附加积分至少填一个", n)
		}
		if e.Factor < 0 || e.Add < 0 {
			return fmt.Errorf("额外收费规则 #%d：倍率与附加积分不能为负", n)
		}
		if math.IsNaN(e.Factor) || math.IsInf(e.Factor, 0) || math.IsNaN(e.Add) || math.IsInf(e.Add, 0) {
			return fmt.Errorf("额外收费规则 #%d：倍率或附加积分不是有效数字", n)
		}
		// 上限是防手滑(多打一个 0 就是十倍收费)，不是业务限制；真要超出改这里。
		if e.Factor > 100 {
			return fmt.Errorf("额外收费规则 #%d：倍率不得超过 100", n)
		}
		if e.Add > 1_000_000 {
			return fmt.Errorf("额外收费规则 #%d：附加积分不得超过 1000000", n)
		}
	}
	return nil
}

func itemAmount(it ChargeItem, req, resp map[string]any) (float64, error) {
	qty, err := extractQty(it, req, resp)
	if err != nil {
		return 0, err
	}
	// second 不足一秒按一秒：上游按实际时长返回 4.58 这类小数秒，先取整秒再乘单价
	// （ceil(4.58)*p，而不是 ceil(4.58*p)——后者会把小数秒折进积分尾数）。放在
	// blockSize 之前，分段计量也基于整秒。
	if it.Unit == "second" {
		qty = math.Ceil(qty)
	}
	if it.BlockSize > 0 {
		qty = math.Ceil(qty / it.BlockSize)
	}
	price, err := resolvePrice(it.Price, req, resp)
	if err != nil {
		return 0, err
	}
	return qty / divisor(it.Unit) * price, nil
}

func extractQty(it ChargeItem, req, resp map[string]any) (float64, error) {
	switch it.Qty {
	case "flat":
		return 1, nil
	case "tokens.inputNonCached":
		p, c := tokenVal(resp, "prompt_tokens"), tokenVal(resp, "prompt_tokens_details.cached_tokens")
		if p == nil {
			return 0, fmt.Errorf("token billing: usage.prompt_tokens missing")
		}
		n := *p
		if c != nil {
			n -= *c
		}
		if n < 0 {
			n = 0
		}
		return checkQty(n)
	case "tokens.cached":
		if c := tokenVal(resp, "prompt_tokens_details.cached_tokens"); c != nil {
			return checkQty(*c)
		}
		return 0, nil
	case "tokens.output":
		if o := tokenVal(resp, "completion_tokens"); o != nil {
			return checkQty(*o)
		}
		return 0, fmt.Errorf("token billing: usage.completion_tokens missing")
	case "chars.input":
		return charsInput(req)
	}
	src, path, err := splitSource(it.Qty, req, resp)
	if err != nil {
		return 0, err
	}
	if it.Unit == "second" {
		if s := stringAt(src, path); s != "" {
			return checkQty(leadingDigits(s))
		}
	}
	v := numberAt(src, path)
	if v == nil {
		return 0, fmt.Errorf("qty %q: path not found or not a number", it.Qty)
	}
	return checkQty(*v)
}

func checkQty(q float64) (float64, error) {
	if math.IsNaN(q) || math.IsInf(q, 0) || q < 0 {
		return 0, fmt.Errorf("invalid quantity %v", q)
	}
	return q, nil
}

func resolvePrice(p Price, req, resp map[string]any) (float64, error) {
	// 区间分档：按某数值字段落入的档返回整段单价。
	if p.ByRange != "" {
		src, path, err := splitSource(p.ByRange, req, resp)
		if err != nil {
			return 0, err
		}
		v := numberAt(src, path)
		if v == nil {
			return 0, fmt.Errorf("price byRange %q: path not found or not a number", p.ByRange)
		}
		for _, t := range p.Tiers {
			if t.MaxTokens == nil || *v <= *t.MaxTokens {
				return t.Price, nil
			}
		}
		return 0, fmt.Errorf("price byRange %q: value %v 超出所有档位且无兜底档", p.ByRange, *v)
	}
	if len(p.Tiers) > 0 {
		// 配了 tiers 却没 byRange = 配置错误，报错而非静默回退到 Fixed(=0)
		return 0, fmt.Errorf("price: tiers set but byRange empty")
	}
	if len(p.By) == 0 {
		return p.Fixed, nil
	}
	parts := make([]string, 0, len(p.By))
	for _, field := range p.By {
		src, path, err := splitSource(field, req, resp)
		if err != nil {
			return 0, err
		}
		parts = append(parts, stringAt(src, path))
	}
	key := strings.Join(parts, "|")
	if v, ok := p.Table[key]; ok {
		return v, nil
	}
	if p.Default != nil {
		return *p.Default, nil
	}
	return 0, fmt.Errorf("price tier key %q not in table", key)
}

func splitSource(s string, req, resp map[string]any) (map[string]any, string, error) {
	switch {
	case strings.HasPrefix(s, "req:"):
		return req, strings.TrimPrefix(s, "req:"), nil
	case strings.HasPrefix(s, "resp:"):
		return resp, strings.TrimPrefix(s, "resp:"), nil
	}
	return nil, "", fmt.Errorf("source %q must start with req: or resp:", s)
}

func ceilCredits(total float64) (int64, error) {
	amount := math.Ceil(total)
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > maxSettleCredits {
		return 0, fmt.Errorf("settle amount out of range: %v", total)
	}
	// math.Ceil 已保证正值 ≥1，此处是兜底(防未来若改用 Floor/Round 漏掉最小 1 积分)。
	if total > 0 && amount < 1 {
		amount = 1
	}
	return int64(amount), nil
}

// charsInput 统计 chat 请求 req.messages[].content 的文本总字符数(按 rune,
// 中文一个字算 1)。content 兼容 OpenAI 两种形态：纯字符串,或多模态 parts 数组
// (只累计 text 部分,图片等不计)。messages 取不到时报错——与引擎「取不到值不
// 静默扣 0」的铁律一致;凡把该来源配到非 chat 形态的调用路径,结算会显式失败
// 而非漏收费。
func charsInput(req map[string]any) (float64, error) {
	if req == nil {
		return 0, fmt.Errorf("chars billing: req missing")
	}
	msgs, ok := req["messages"].([]any)
	if !ok {
		return 0, fmt.Errorf("chars billing: req.messages missing or not an array")
	}
	n := 0
	for _, m := range msgs {
		obj, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch c := obj["content"].(type) {
		case string:
			n += utf8.RuneCountInString(c)
		case []any:
			for _, p := range c {
				if part, ok := p.(map[string]any); ok {
					if s, ok := part["text"].(string); ok {
						n += utf8.RuneCountInString(s)
					}
				}
			}
		}
	}
	return float64(n), nil
}

func tokenVal(resp map[string]any, path string) *float64 {
	if resp == nil {
		return nil
	}
	return numberAt(resp, "usage."+path)
}

// leadingDigits 从形如 "6s"/"10s"/"0.5s" 的字符串解析前导数值；解析不出返回 0。
func leadingDigits(s string) float64 {
	var n float64
	// Sscanf 读到首个非数值字符即停："6s"→6, "0.5s"→0.5, ""→0(匹配数为0,n保持0)。
	fmt.Sscanf(s, "%f", &n)
	return n
}

func numberAt(m map[string]any, path string) *float64 {
	cur := any(m)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[seg]
	}
	if f, ok := cur.(float64); ok {
		return &f
	}
	return nil
}

func stringAt(m map[string]any, path string) string {
	cur := any(m)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = obj[seg]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}
