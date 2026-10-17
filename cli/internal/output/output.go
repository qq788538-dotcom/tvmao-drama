// Package output 收口 CLI 的所有输出。
//
// 硬规则：**stdout 只放最终机器可读结果，进度与诊断一律 stderr**。
// Agent 直接 `miaoju node create ... | jq .id` 就该拿到 ID，不需要从
// 自然语言日志里抠。任何往 stdout 写进度的代码都是 bug。
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type Format string

const (
	JSON   Format = "json"
	Table  Format = "table"
	NDJSON Format = "ndjson"
)

func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case JSON, "":
		return JSON, nil
	case Table:
		return Table, nil
	case NDJSON:
		return NDJSON, nil
	default:
		return "", fmt.Errorf("不支持的输出格式 %q（可选 json / table / ndjson）", s)
	}
}

type Writer interface {
	// Emit 输出最终结果到 stdout。
	Emit(v any) error
	// Progress 输出进度到 stderr；--quiet 时静默。
	Progress(format string, a ...any)
	// Warn 输出警告到 stderr；**不受 --quiet 影响**——静音的是噪音，不是问题。
	Warn(format string, a ...any)
	Format() Format
}

type writer struct {
	format Format
	stdout io.Writer
	stderr io.Writer
	quiet  bool
}

func New(format Format, stdout, stderr io.Writer, quiet bool) Writer {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return &writer{format: format, stdout: stdout, stderr: stderr, quiet: quiet}
}

func (w *writer) Format() Format { return w.format }

func (w *writer) Progress(format string, a ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintf(w.stderr, format+"\n", a...)
}

func (w *writer) Warn(format string, a ...any) {
	fmt.Fprintf(w.stderr, format+"\n", a...)
}

func (w *writer) Emit(v any) error {
	switch w.format {
	case NDJSON:
		return w.emitNDJSON(v)
	case Table:
		return w.emitTable(v)
	default:
		enc := json.NewEncoder(w.stdout)
		enc.SetIndent("", "  ")
		// 关掉 HTML 转义：提示词里的 < > & 被转成 < 会让人以为内容变了。
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
}

// emitNDJSON 对数组逐条输出一行，非数组退化成单行。
func (w *writer) emitNDJSON(v any) error {
	enc := json.NewEncoder(w.stdout)
	enc.SetEscapeHTML(false)
	items, ok := asSlice(v)
	if !ok {
		return enc.Encode(v)
	}
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

// emitTable 只处理「对象数组」这一种形状，其余退回 JSON。
// table 是给人看的便利视图，不是契约；复杂结构强行拍平只会更难读。
func (w *writer) emitTable(v any) error {
	items, ok := asSlice(v)
	if !ok || len(items) == 0 {
		if !ok {
			return w.emitAsJSON(v)
		}
		fmt.Fprintln(w.stdout, "(空)")
		return nil
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return w.emitAsJSON(v)
		}
		rows = append(rows, m)
	}

	cols := tableColumns(rows)
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len([]rune(c))
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		cells[r] = make([]string, len(cols))
		for i, c := range cols {
			s := scalarString(row[c])
			cells[r][i] = s
			if n := len([]rune(s)); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	for i, c := range cols {
		b.WriteString(pad(c, widths[i]))
		if i < len(cols)-1 {
			b.WriteString("  ")
		}
	}
	fmt.Fprintln(w.stdout, strings.TrimRight(b.String(), " "))
	for _, row := range cells {
		b.Reset()
		for i, s := range row {
			b.WriteString(pad(s, widths[i]))
			if i < len(row)-1 {
				b.WriteString("  ")
			}
		}
		fmt.Fprintln(w.stdout, strings.TrimRight(b.String(), " "))
	}
	return nil
}

func (w *writer) emitAsJSON(v any) error {
	enc := json.NewEncoder(w.stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// tableColumns 取所有行的键并集，首列固定 id（如果有）。
// 按出现顺序而不是字典序——第一行的字段顺序通常就是最有意义的顺序。
func tableColumns(rows []map[string]any) []string {
	seen := map[string]bool{}
	var cols []string
	for _, row := range rows {
		for k := range row {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sortColumns(cols)
	return cols
}

// sortColumns 把 id / name 顶到前面，其余保持字典序，让 table 好扫。
func sortColumns(cols []string) {
	priority := map[string]int{"id": 0, "nodeId": 0, "modelId": 0, "taskId": 0, "name": 1, "type": 2, "status": 3}
	rank := func(s string) int {
		if p, ok := priority[s]; ok {
			return p
		}
		return 10
	}
	for i := 1; i < len(cols); i++ {
		for j := i; j > 0; j-- {
			a, b := cols[j-1], cols[j]
			ra, rb := rank(a), rank(b)
			if ra < rb || (ra == rb && a <= b) {
				break
			}
			cols[j-1], cols[j] = b, a
		}
	}
}

// asSlice 把任意值 round-trip 成 []any（只有 JSON 数组才成功）。
func asSlice(v any) ([]any, bool) {
	blob, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var out []any
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil, false
	}
	return out, true
}

// scalarString 把单元格值转成一行文本；嵌套结构压成紧凑 JSON。
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.ReplaceAll(t, "\n", " ")
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		blob, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(blob)
	}
}

func pad(s string, width int) string {
	n := width - len([]rune(s))
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}
