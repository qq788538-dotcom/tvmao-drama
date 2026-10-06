package skillruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk/filesystem"
)

const maxEmbedTextBytes = 4 << 20

var jsonFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// embedText 实现 embed-text：小说风格 Skill 要求把 source.txt 原样注入 JSON，
// 白名单不允许 python -c，于是由 sidecar 内置完成。保持已有字段顺序（validate_storyboard.py 会校验顶层顺序）。
func (s *RestrictedShell) embedText(ctx context.Context, args []string) (*filesystem.ExecuteResponse, error) {
	jsonRel, field, textRel := args[0], args[1], args[2]
	if !jsonFieldPattern.MatchString(field) {
		return nil, rejectf("字段名只能由字母、数字、下划线组成：%q", field)
	}
	jsonPath, err := s.resolveWorkspacePath(jsonRel)
	if err != nil {
		return nil, err
	}
	textPath, err := s.resolveWorkspacePath(textRel)
	if err != nil {
		return nil, err
	}
	textAbs := filepath.Join(s.workspace, textPath)
	info, err := os.Stat(textAbs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, rejectf("文本文件不存在：%q", textRel)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxEmbedTextBytes {
		return nil, rejectf("文本文件必须是不超过 4 MiB 的普通文件：%q", textRel)
	}
	text, err := os.ReadFile(textAbs)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(text) {
		return nil, rejectf("文本文件不是 UTF-8：%q", textRel)
	}

	var fields []orderedField
	raw, err := os.ReadFile(filepath.Join(s.workspace, jsonPath))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if fields, err = decodeOrderedObject(raw); err != nil {
			return nil, rejectf("%s 不是 JSON 对象：%v", jsonRel, err)
		}
	}
	value, err := marshalNoEscape(string(text))
	if err != nil {
		return nil, err
	}
	content, err := encodeOrderedObject(setOrderedField(fields, field, value))
	if err != nil {
		return nil, err
	}
	backend, err := NewWorkspaceBackend(s.workspace)
	if err != nil {
		return nil, err
	}
	if err := backend.Write(ctx, &filesystem.WriteRequest{FilePath: jsonPath, Content: content}); err != nil {
		return nil, err
	}
	code := 0
	return &filesystem.ExecuteResponse{
		Output:   fmt.Sprintf("OK: 已把 %s 原样写入 %s 的 %s 字段（%d 字）\n", textRel, jsonRel, field, utf8.RuneCount(text)),
		ExitCode: &code,
	}, nil
}

type orderedField struct {
	Key   string
	Value json.RawMessage
}

func decodeOrderedObject(raw []byte) ([]orderedField, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("顶层必须是对象")
	}
	var fields []orderedField
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, orderedField{Key: key, Value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("对象之后还有多余内容")
	}
	return fields, nil
}

// setOrderedField 替换同名字段（保留第一个位置、删掉重复项）；不存在则追加到末尾。
func setOrderedField(fields []orderedField, key string, value json.RawMessage) []orderedField {
	out := make([]orderedField, 0, len(fields)+1)
	replaced := false
	for _, f := range fields {
		if f.Key != key {
			out = append(out, f)
			continue
		}
		if !replaced {
			out = append(out, orderedField{Key: key, Value: value})
			replaced = true
		}
	}
	if !replaced {
		out = append(out, orderedField{Key: key, Value: value})
	}
	return out
}

func encodeOrderedObject(fields []orderedField) (string, error) {
	var b strings.Builder
	b.WriteString("{\n")
	for i, f := range fields {
		key, err := marshalNoEscape(f.Key)
		if err != nil {
			return "", err
		}
		b.WriteString("  ")
		b.Write(key)
		b.WriteString(": ")
		b.Write(f.Value)
		if i < len(fields)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
