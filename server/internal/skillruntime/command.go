package skillruntime

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 受限 shell 的命令语法：execute 不经过任何 shell，这里自己分词并只认两种命令：
//
//	python3 <Skill 自带 .py 脚本> [参数…]
//	embed-text <JSON 文件> <字段名> <文本文件>
//
// 其余一律拒绝。拒绝原因直接作为工具结果给模型看，所以要写清楚“该怎么写”。

type CommandKind int

const (
	CommandPython CommandKind = iota + 1
	CommandEmbedText
)

const embedTextCommand = "embed-text"

type ParsedCommand struct {
	Kind   CommandKind
	Script string   // CommandPython：脚本相对路径，分隔符已统一为 /
	Args   []string // CommandPython：脚本参数原样；CommandEmbedText：JSON 路径、字段名、文本路径
}

// ErrCommandRejected 标记“命令不在白名单内”，与 I/O 等真正的错误区分开。
var ErrCommandRejected = errors.New("命令被拒绝")

type commandRejectedError struct{ reason string }

func (e *commandRejectedError) Error() string { return e.reason }
func (e *commandRejectedError) Unwrap() error { return ErrCommandRejected }

func rejectf(format string, args ...any) error {
	return &commandRejectedError{reason: fmt.Sprintf(format, args...)}
}

var (
	pythonNamePattern = regexp.MustCompile(`^(?i)(python(3(\.[0-9]+)?)?(\.exe)?|py)$`)
	flagPattern       = regexp.MustCompile(`^--?[A-Za-z][A-Za-z0-9_-]*$`)
	numberPattern     = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	windowsDeviceName = regexp.MustCompile(`^(?i)(con|prn|aux|nul|conin\$|conout\$|com[0-9¹²³]|lpt[0-9¹²³])(\..*)?$`)
)

// 不加引号时出现即拒绝：管道、重定向、串联、变量/命令替换、通配、子 shell、注释、历史展开。
const shellMetaChars = "|&;<>$`()*?[]{}~!#"

// 唯一容忍的重定向：模型习惯在命令末尾加 2>&1，而这里本来就合并 stdout/stderr。
const mergeStderrSuffix = " 2>&1"

func ParseCommand(line string) (ParsedCommand, error) {
	tokens, err := tokenizeCommand(line)
	if err != nil {
		return ParsedCommand{}, err
	}
	head := tokens[0]
	if head == embedTextCommand {
		if len(tokens) != 4 {
			return ParsedCommand{}, rejectf("用法：embed-text <JSON 文件> <字段名> <文本文件>")
		}
		return ParsedCommand{Kind: CommandEmbedText, Args: []string{toSlash(tokens[1]), tokens[2], toSlash(tokens[3])}}, nil
	}
	if !pythonNamePattern.MatchString(head) {
		return ParsedCommand{}, rejectf("不支持的命令 %q：execute 不是 bash，只能用 python3 运行 Skill 自带脚本，或使用 embed-text", head)
	}
	if len(tokens) < 2 {
		return ParsedCommand{}, rejectf("缺少脚本路径：用法 python3 scripts/<脚本名>.py [参数…]")
	}
	script := tokens[1]
	if strings.HasPrefix(script, "-") {
		return ParsedCommand{}, rejectf("不允许 python 选项 %q（例如 -c、-m）：只能直接运行 Skill 自带的 .py 脚本", script)
	}
	script = toSlash(script)
	if !strings.HasSuffix(strings.ToLower(script), ".py") {
		return ParsedCommand{}, rejectf("只能运行 .py 脚本：%q", script)
	}
	if err := checkRelPath(script); err != nil {
		return ParsedCommand{}, err
	}
	return ParsedCommand{Kind: CommandPython, Script: script, Args: append([]string{}, tokens[2:]...)}, nil
}

// tokenizeCommand 按 POSIX 习惯分词的最小子集：空白分隔、单引号内全部原样、
// 双引号内只认 \" 和 \\。引号外的反斜杠按普通字符处理（兼容 Windows 路径）。
func tokenizeCommand(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if strings.ContainsRune(line, 0) {
		return nil, rejectf("命令里不能有 NUL 字符")
	}
	if strings.ContainsAny(line, "\r\n") {
		return nil, rejectf("不支持多行命令：每次只执行一条命令")
	}
	if strings.HasSuffix(line, mergeStderrSuffix) {
		line = strings.TrimSpace(strings.TrimSuffix(line, mergeStderrSuffix))
	}
	if line == "" {
		return nil, rejectf("命令为空")
	}
	const (
		stateNone = iota
		stateSingle
		stateDouble
	)
	var tokens []string
	var cur strings.Builder
	inToken := false
	state := stateNone
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch state {
		case stateSingle:
			if c == '\'' {
				state = stateNone
			} else {
				cur.WriteRune(c)
			}
		case stateDouble:
			switch c {
			case '"':
				state = stateNone
			case '\\':
				if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					i++
					cur.WriteRune(runes[i])
				} else {
					cur.WriteRune(c)
				}
			case '$', '`':
				return nil, rejectf("不支持变量或命令替换（%q）：参数请直接写出具体值", string(c))
			default:
				cur.WriteRune(c)
			}
		default:
			switch {
			case c == ' ' || c == '\t':
				if inToken {
					tokens = append(tokens, cur.String())
					cur.Reset()
					inToken = false
				}
				continue
			case c == '\'':
				state = stateSingle
			case c == '"':
				state = stateDouble
			case strings.ContainsRune(shellMetaChars, c):
				return nil, rejectf("不支持 shell 语法 %q：execute 不是 bash，不支持管道、重定向、&&、;、变量和通配符", string(c))
			default:
				cur.WriteRune(c)
			}
		}
		inToken = true
	}
	if state != stateNone {
		return nil, rejectf("引号未闭合")
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	if len(tokens) == 0 {
		return nil, rejectf("命令为空")
	}
	return tokens, nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// checkRelPath 是不碰文件系统的静态检查：只允许不含 .. 的相对路径，
// 挡住绝对路径、盘符（C:x）、UNC、NTFS 备用数据流（a:b）和 Windows 设备名。
func checkRelPath(p string) error {
	p = toSlash(p)
	switch {
	case p == "":
		return rejectf("路径为空")
	case strings.HasPrefix(p, "-"):
		// 以 - 开头的值会被脚本当成选项（如 -s/etc/hosts），一律拒绝。
		return rejectf("路径不能以 - 开头：%q", p)
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, "~"), len(p) >= 2 && p[1] == ':':
		return rejectf("只能使用工作区内的相对路径，不能用绝对路径：%q", p)
	case strings.Contains(p, ":"):
		return rejectf("路径里不能有冒号：%q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return rejectf("路径不能包含 ..：%q", p)
		}
		if windowsDeviceName.MatchString(seg) {
			return rejectf("路径不能使用设备名：%q", p)
		}
	}
	return nil
}

// mapScriptArgs 逐个处理脚本参数：数字、-x / --flag 原样保留；--flag=值 拆开处理值；
// 其余一律视为工作区文件路径，先做静态检查，再交给 resolvePath 做文件系统检查。
func mapScriptArgs(args []string, resolvePath func(string) (string, error)) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if i := strings.IndexByte(a, '='); i > 0 && flagPattern.MatchString(a[:i]) {
			v, err := mapArgValue(a[i+1:], resolvePath)
			if err != nil {
				return nil, err
			}
			out = append(out, a[:i]+"="+v)
			continue
		}
		if numberPattern.MatchString(a) || flagPattern.MatchString(a) {
			out = append(out, a)
			continue
		}
		v, err := mapArgValue(a, resolvePath)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func mapArgValue(v string, resolvePath func(string) (string, error)) (string, error) {
	if numberPattern.MatchString(v) {
		return v, nil
	}
	if err := checkRelPath(v); err != nil {
		return "", err
	}
	return resolvePath(toSlash(v))
}
