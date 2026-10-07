// Package builtin 管理内置数据（spec M8–M11）：编译进 sidecar 的种子、首次启动导入与升级合并、
// 用户可改字段的白名单与校验、恢复默认。HTTP 接口和设置页属于 P4，它们只通过本包的 Service 写内置数据。
package builtin

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// seeddata/ 下是仓库根 seed/ 同名文件的逐字节副本：go:embed 不能引用包目录之外的文件。
// 真源是 seed/；改了种子运行 scripts/sync-seed.sh，忘了同步会被 TestEmbeddedSeedMatchesRepoSeed 拦住。
//
//go:embed seeddata/model_configs.jsonl seeddata/settings.jsonl
var seedFS embed.FS

// seedFormatVersion 并入种子哈希。合并算法或可改字段白名单的语义变化时递增，
// 让已安装的用户在升级后的首次启动重新合并一次（即使种子文件本身没变）。
const seedFormatVersion = "p2a-2" // p2a-2：五项画布配置写死（2026-10-07）

// DatasetModelCenter 是本包在 builtin_seed_state 里使用的数据集名（模型中心 + 画布配置）。
// P2b 的 Skill、Agent 工具等数据集各用自己的名字。
const DatasetModelCenter = "model_center"

// SeedModel 是 model_configs.jsonl 的一行。字段名与参考仓生产库列名一致，JSON 列在文件里是对象。
// 解码时拒绝未知字段：生产库加了新列而这里没跟上，重新导出后测试会立即失败，而不是静默丢列。
type SeedModel struct {
	ID               int64           `json:"id"`
	Type             string          `json:"type"`
	Name             string          `json:"name"`
	ModelID          string          `json:"model_id"`
	BasePath         string          `json:"base_path"`
	Enabled          flexBool        `json:"enabled"`
	Hidden           flexBool        `json:"hidden"`
	SortOrder        int             `json:"sort_order"`
	RequestTemplate  json.RawMessage `json:"request_template"`
	PollingTemplate  json.RawMessage `json:"polling_template"`
	UISchema         json.RawMessage `json:"ui_schema"`
	BillingConfig    json.RawMessage `json:"billing_config"`
	GenControl       json.RawMessage `json:"gen_control"`
	ImageInputFormat string          `json:"image_input_format"`
	ImageRequestMode string          `json:"image_request_mode"`
	AdminNote        string          `json:"admin_note"`
	PriceNote        string          `json:"price_note"`
	// 生产库的时间戳只做记录，不导入：本地行的 created_at/updated_at 由本机生成。
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SeedSetting 是 settings.jsonl 的一行。Value 原样是字符串：base_domain 是纯文本，其余是 JSON 文本。
type SeedSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

// Seed 是解析并校验过的一份内置种子。
type Seed struct {
	Models   []SeedModel
	Settings []SeedSetting
	// Hash 是种子文件内容（连同 seedFormatVersion）的 sha256，十六进制。
	Hash     string
	models   map[string]int
	settings map[string]int
}

// Model 按 model_id 查种子模型。
func (s *Seed) Model(modelID string) (SeedModel, bool) {
	i, ok := s.models[modelID]
	if !ok {
		return SeedModel{}, false
	}
	return s.Models[i], true
}

// Setting 按 key 查种子配置。
func (s *Seed) Setting(key string) (SeedSetting, bool) {
	i, ok := s.settings[key]
	if !ok {
		return SeedSetting{}, false
	}
	return s.Settings[i], true
}

// Catalog 是配置校验时可以引用的模型：model_id → type（text/image/video/audio）。
type Catalog map[string]string

// Catalog 返回种子里全部模型的 model_id → type。
func (s *Seed) Catalog() Catalog {
	cat := Catalog{}
	for _, m := range s.Models {
		cat[m.ModelID] = m.Type
	}
	return cat
}

// Load 解析编译进二进制的种子。
func Load() (*Seed, error) {
	m, err := seedFS.ReadFile("seeddata/model_configs.jsonl")
	if err != nil {
		return nil, err
	}
	st, err := seedFS.ReadFile("seeddata/settings.jsonl")
	if err != nil {
		return nil, err
	}
	return parseSeed(m, st)
}

// secretKeyRe 匹配密钥类配置名。种子导出时已去掉 Key（seed/README.md），这里再挡一道：
// 万一重新导出时带进了密钥，构建期的测试就会失败，密钥不会被编译进安装包。
var secretKeyRe = regexp.MustCompile(`(?i)(_key$|secret|password|(^|[_-])token$|^token$)`)

var seedModelTypes = map[string]bool{"text": true, "image": true, "video": true, "audio": true}

func parseSeed(modelsJSONL, settingsJSONL []byte) (*Seed, error) {
	s := &Seed{models: map[string]int{}, settings: map[string]int{}}
	ids := map[int64]bool{}
	err := eachLine(modelsJSONL, func(line []byte) error {
		var m SeedModel
		if err := decodeStrict(line, &m); err != nil {
			return err
		}
		if err := validateSeedModel(m); err != nil {
			return err
		}
		if ids[m.ID] {
			return fmt.Errorf("id %d 重复", m.ID)
		}
		if _, dup := s.models[m.ModelID]; dup {
			return fmt.Errorf("model_id %s 重复", m.ModelID)
		}
		ids[m.ID] = true
		s.models[m.ModelID] = len(s.Models)
		s.Models = append(s.Models, m)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("model_configs.jsonl %w", err)
	}
	err = eachLine(settingsJSONL, func(line []byte) error {
		var st SeedSetting
		if err := decodeStrict(line, &st); err != nil {
			return err
		}
		if st.Key == "" {
			return errors.New("key 为空")
		}
		if secretKeyRe.MatchString(st.Key) {
			return fmt.Errorf("种子不得包含密钥类配置 %s", st.Key)
		}
		if _, dup := s.settings[st.Key]; dup {
			return fmt.Errorf("key %s 重复", st.Key)
		}
		s.settings[st.Key] = len(s.Settings)
		s.Settings = append(s.Settings, st)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("settings.jsonl %w", err)
	}
	sum := sha256.New()
	io.WriteString(sum, seedFormatVersion+"\n")
	sum.Write(modelsJSONL)
	io.WriteString(sum, "\n\x00\n")
	sum.Write(settingsJSONL)
	s.Hash = hex.EncodeToString(sum.Sum(nil))
	return s, nil
}

func validateSeedModel(m SeedModel) error {
	if m.ID <= 0 {
		return fmt.Errorf("模型 %q：id 必须是正整数", m.ModelID)
	}
	if m.ModelID == "" || m.Name == "" || m.BasePath == "" {
		return fmt.Errorf("模型 %d：model_id、name、base_path 不能为空", m.ID)
	}
	if !seedModelTypes[m.Type] {
		return fmt.Errorf("模型 %s：未知类型 %q", m.ModelID, m.Type)
	}
	if t := bytes.TrimSpace(m.RequestTemplate); len(t) == 0 || t[0] != '{' {
		return fmt.Errorf("模型 %s：request_template 必须是 JSON 对象", m.ModelID)
	}
	switch m.ImageInputFormat {
	case "", "url", "data_uri", "base64":
	default:
		return fmt.Errorf("模型 %s：未知 image_input_format %q", m.ModelID, m.ImageInputFormat)
	}
	switch m.ImageRequestMode {
	case "", "json", "openai_image_edits":
	default:
		return fmt.Errorf("模型 %s：未知 image_request_mode %q", m.ModelID, m.ImageRequestMode)
	}
	// 与参考仓后台保存模型时的校验一致（admin_model_handlers.go 的 validateBillingExtras）：
	// 配错的加价规则在运行期不报错、静默失效，所以必须在入库前挡住。
	if raw := nullableRaw(m.BillingConfig); raw != nil {
		var mb billing.ModelBilling
		if err := json.Unmarshal(raw, &mb); err != nil {
			return fmt.Errorf("模型 %s：billing_config 解析失败: %w", m.ModelID, err)
		}
		if err := billing.ValidateExtras(mb.Extras); err != nil {
			return fmt.Errorf("模型 %s：%w", m.ModelID, err)
		}
	}
	return nil
}

// eachLine 逐行回调非空行。不用 bufio.Scanner：它默认单行上限 64KB，P2b 的 skills 单行更长。
func eachLine(data []byte, fn func(line []byte) error) error {
	r := bufio.NewReader(bytes.NewReader(data))
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if e := fn(t); e != nil {
				return fmt.Errorf("第 %d 行: %w", n, e)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func decodeStrict(line []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("一行只能有一个 JSON 对象，后面有多余内容")
	}
	return nil
}

// flexBool 接受 0/1/true/false：生产库是 TINYINT(1)，导出成 0/1。
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "1", "true":
		*b = true
	case "0", "false":
		*b = false
	default:
		return fmt.Errorf("布尔值只接受 0/1/true/false，得到 %s", data)
	}
	return nil
}

// nullableRaw 把缺失或 null 的 JSON 列统一成 nil（落库为 NULL）。
func nullableRaw(raw json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil
	}
	return t
}

func rawPtr(raw json.RawMessage) *string {
	r := nullableRaw(raw)
	if r == nil {
		return nil
	}
	s := string(r)
	return &s
}

// modelParts 是模型行里用户可以改的那部分列（spec M10）。合并（M11）和编辑都以它为单位，
// 其余列（请求/轮询模板、base_path、图片协议、计费等）永远取种子值。
type modelParts struct {
	Name       string
	Enabled    bool
	Hidden     bool
	SortOrder  int
	UISchema   *string // nil = NULL
	GenControl *string // nil = NULL
}

func (m SeedModel) parts() modelParts {
	return modelParts{
		Name:       m.Name,
		Enabled:    bool(m.Enabled),
		Hidden:     bool(m.Hidden),
		SortOrder:  m.SortOrder,
		UISchema:   rawPtr(m.UISchema),
		GenControl: rawPtr(m.GenControl),
	}
}
