package upstream

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// SpillFile 是从响应 JSON 里抽出来、已 base64 解码落盘的一段字符串值。
type SpillFile struct {
	Path string
	Size int64
}

// SpillResult：JSON 是抽走大字符串后的剩余正文（被抽的值换成 "@spill:<n>"），
// Files 按出现顺序与占位符下标一一对应。
type SpillResult struct {
	JSON  []byte
	Files []SpillFile
}

const spillPlaceholderPrefix = "@spill:"

// spillLargeStrings 流式扫描 JSON：命中 keys 的键，其字符串值不进内存，边读边
// base64 解码写进 dir 下的临时文件；其余字节原样透传。
//
// 为什么自己写而不用 encoding/json：标准库没有「字符串值分段给我」的模式，
// 要么不解析、要么把整段 b64 一次性物化——那正是 9/4 事故里 5 份拷贝的第一份。
//
// 状态机只做到「能正确区分字符串内外 + 能识别键」这一层，不校验 JSON 语法；
// 剩余正文的合法性由下游 json.Unmarshal 兜底。任何错误都会删掉已写的文件。
func spillLargeStrings(r io.Reader, keys map[string]bool, dir string) (res *SpillResult, err error) {
	br := bufio.NewReaderSize(r, 64<<10)
	out := &strings.Builder{}
	result := &SpillResult{}
	defer func() {
		if err != nil {
			for _, f := range result.Files {
				_ = os.Remove(f.Path)
			}
		}
	}()

	// pendingKey：上一个字符串 token 是键且命中 keys，下一个值若是字符串就抽走。
	pendingKey := false
	// lastStr：最近一个完整字符串 token 的原文（不含引号）。看到 ':' 时判断它是不是键。
	var lastStr strings.Builder
	sawStr := false // 刚写完一个字符串，等着看下一个非空白字符是不是 ':'

	for {
		c, rerr := br.ReadByte()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
		switch {
		case c == '"':
			if pendingKey {
				// 值是字符串且键命中：抽走。
				pendingKey = false
				sawStr = false
				idx := len(result.Files)
				sf, serr := spillOneString(br, dir)
				if serr != nil {
					return nil, serr
				}
				result.Files = append(result.Files, sf)
				out.WriteString(`"` + spillPlaceholderPrefix + strconv.Itoa(idx) + `"`)
				continue
			}
			// 普通字符串：原样透传，同时记下原文供键判断。
			pendingKey = false
			lastStr.Reset()
			out.WriteByte('"')
			for {
				d, derr := br.ReadByte()
				if derr != nil {
					return nil, fmt.Errorf("unterminated string: %w", derr)
				}
				out.WriteByte(d)
				if d == '\\' {
					n, nerr := br.ReadByte()
					if nerr != nil {
						return nil, fmt.Errorf("unterminated escape: %w", nerr)
					}
					out.WriteByte(n)
					lastStr.WriteByte(d)
					lastStr.WriteByte(n)
					continue
				}
				if d == '"' {
					break
				}
				lastStr.WriteByte(d)
			}
			sawStr = true
		case c == ':' && sawStr:
			// 刚结束的字符串是键。
			pendingKey = keys[lastStr.String()]
			sawStr = false
			out.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			out.WriteByte(c)
		default:
			// 任何别的 token（数字/null/true/{/[/,/}/]）都意味着：上一个字符串不是键，
			// 或者命中键的值不是字符串（null 等）——两种情况都清标记。
			sawStr = false
			pendingKey = false
			out.WriteByte(c)
		}
	}
	result.JSON = []byte(out.String())
	return result, nil
}

// spillOneString 从「开引号之后」读到闭引号，base64 解码写临时文件。
// 只处理 base64 字母表会遇到的转义：`\/`。其他转义在 base64 里不该出现，按非法处理。
func spillOneString(br *bufio.Reader, dir string) (sf SpillFile, err error) {
	f, err := os.CreateTemp(dir, "out-*.png")
	if err != nil {
		return SpillFile{}, fmt.Errorf("create spill temp: %w", err)
	}
	w := &b64FileWriter{f: f}
	defer func() {
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	for {
		c, rerr := br.ReadByte()
		if rerr != nil {
			return SpillFile{}, fmt.Errorf("unterminated b64 string: %w", rerr)
		}
		if c == '"' {
			break
		}
		if c == '\\' {
			n, nerr := br.ReadByte()
			if nerr != nil {
				return SpillFile{}, fmt.Errorf("unterminated escape in b64: %w", nerr)
			}
			if n != '/' {
				return SpillFile{}, fmt.Errorf("unexpected escape \\%c in b64 value", n)
			}
			c = '/'
		}
		if err := w.writeByte(c); err != nil {
			return SpillFile{}, err
		}
	}
	if err := w.flush(true); err != nil {
		return SpillFile{}, err
	}
	return SpillFile{Path: f.Name(), Size: w.n}, nil
}

// b64FileWriter 按 4 字节对齐分块解码：标准 base64 每 4 个输入字符独立成 3 字节，
// 分块解码与整段解码结果相同，所以不需要把整段攒起来。
type b64FileWriter struct {
	f       *os.File
	pending []byte
	n       int64
}

const b64Chunk = 64 << 10

func (w *b64FileWriter) writeByte(c byte) error {
	w.pending = append(w.pending, c)
	if len(w.pending) >= b64Chunk {
		return w.flush(false)
	}
	return nil
}

func (w *b64FileWriter) flush(final bool) error {
	n := len(w.pending)
	if !final {
		n -= n % 4
	}
	if n == 0 {
		return nil
	}
	chunk := w.pending[:n]
	buf := make([]byte, base64.StdEncoding.DecodedLen(n))
	m, err := base64.StdEncoding.Decode(buf, chunk)
	if err != nil && final {
		// 尾块可能没补 '='：退一步试 Raw。中间块永远 4 对齐,不会走到这里。
		m, err = base64.RawStdEncoding.Decode(buf, chunk)
	}
	if err != nil {
		return fmt.Errorf("decode b64: %w", err)
	}
	if _, err := w.f.Write(buf[:m]); err != nil {
		return err
	}
	w.n += int64(m)
	w.pending = append(w.pending[:0], w.pending[n:]...)
	return nil
}
