package skillruntime

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommandAccepts(t *testing.T) {
	cases := []struct {
		line string
		want ParsedCommand
	}{
		{"python3 scripts/validate_storyboard.py candidate.json --duration-sec 10",
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec", "10"}}},
		{`python scripts\validate_storyboard.py candidate.json`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json"}}},
		{`python3 "scripts/validate_storyboard.py" 'candidate.json' --duration-sec=10`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec=10"}}},
		{"python3 scripts/validate_storyboard.py candidate.json --duration-sec 10 2>&1",
			ParsedCommand{Kind: CommandPython, Script: "scripts/validate_storyboard.py", Args: []string{"candidate.json", "--duration-sec", "10"}}},
		{"  python3.12   ./scripts/a.py  ",
			ParsedCommand{Kind: CommandPython, Script: "./scripts/a.py", Args: []string{}}},
		{`python3 scripts/a.py "带 空格.json" "say \"hi\""`,
			ParsedCommand{Kind: CommandPython, Script: "scripts/a.py", Args: []string{"带 空格.json", `say "hi"`}}},
		{"embed-text adapted_script.json sourceText source.txt",
			ParsedCommand{Kind: CommandEmbedText, Args: []string{"adapted_script.json", "sourceText", "source.txt"}}},
	}
	for _, c := range cases {
		got, err := ParseCommand(c.line)
		if err != nil {
			t.Errorf("%q: 不应拒绝: %v", c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %#v want %#v", c.line, got, c.want)
		}
	}
}

func TestParseCommandRejects(t *testing.T) {
	cases := []struct{ line, reason string }{
		{"", "命令为空"},
		{"   ", "命令为空"},
		{`bash -lc "python3 x.py"`, "不支持的命令"},
		{"cat candidate.json", "不支持的命令"},
		{"PYTHONPATH=. python3 scripts/a.py", "不支持的命令"},
		{`python3 -c "print(1)"`, "不允许 python 选项"},
		{"python3 -m http.server", "不允许 python 选项"},
		{"python3", "缺少脚本路径"},
		{"python3 scripts/run.sh", "只能运行 .py 脚本"},
		{"python3 /tmp/x.py", "绝对路径"},
		{"python3 ../other/scripts/x.py", "不能包含 .."},
		{"python3 scripts/a.py | head", "不支持 shell 语法"},
		{"python3 scripts/a.py > out.txt", "不支持 shell 语法"},
		{"cd scripts && python3 a.py", "不支持 shell 语法"},
		{"python3 scripts/a.py; rm -rf /", "不支持 shell 语法"},
		{"python3 scripts/a.py $(whoami)", "不支持 shell 语法"},
		{"python3 scripts/a.py `id`", "不支持 shell 语法"},
		{`python3 scripts/a.py "$HOME"`, "不支持变量或命令替换"},
		{"python3 scripts/a.py *.json", "不支持 shell 语法"},
		{"python3 scripts/a.py candidate.json --duration-sec <DURATION_SEC>", "不支持 shell 语法"},
		{`python3 "scripts/a.py`, "引号未闭合"},
		{"python3 scripts/a.py\nrm x", "不支持多行命令"},
		{"embed-text a.json sourceText", "用法：embed-text"},
	}
	for _, c := range cases {
		_, err := ParseCommand(c.line)
		if err == nil {
			t.Errorf("%q: 应被拒绝", c.line)
			continue
		}
		if !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%q: 错误应可用 errors.Is(ErrCommandRejected) 识别: %v", c.line, err)
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%q: 原因 %q 不含 %q", c.line, err.Error(), c.reason)
		}
	}
}

func TestCheckRelPath(t *testing.T) {
	for _, ok := range []string{"candidate.json", "./out/plan.json", "scripts/validate_storyboard.py", "带 空格.json"} {
		if err := checkRelPath(ok); err != nil {
			t.Errorf("%q 应允许: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x.json", "scripts/../../x", `C:\Windows\x`, "C:x", `\\server\share\x`,
		"candidate.json:stream", "NUL", "con.txt", "out/Com1", "~/x"} {
		if err := checkRelPath(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		} else if !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%q: 应是 ErrCommandRejected: %v", bad, err)
		}
	}
}

func TestMapScriptArgs(t *testing.T) {
	var seen []string
	resolve := func(p string) (string, error) {
		seen = append(seen, p)
		return "WS/" + p, nil
	}
	got, err := mapScriptArgs([]string{"candidate.json", "--duration-sec", "10", "-v", "--source=source.txt", "--ratio=1.5", "-3", `out\plan.json`}, resolve)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"WS/candidate.json", "--duration-sec", "10", "-v", "--source=WS/source.txt", "--ratio=1.5", "-3", "WS/out/plan.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if !reflect.DeepEqual(seen, []string{"candidate.json", "source.txt", "out/plan.json"}) {
		t.Fatalf("只有路径参数应交给 resolvePath，got %#v", seen)
	}
	for _, bad := range [][]string{{"../x.json"}, {"--source=/etc/passwd"}, {"C:/x"}, {"-s/etc/hosts"}, {"-s../x"}, {"-s../secret.txt"}, {"--source=-s/etc/hosts"}, {"-"}} {
		if _, err := mapScriptArgs(bad, resolve); !errors.Is(err, ErrCommandRejected) {
			t.Errorf("%v 应拒绝，got %v", bad, err)
		}
	}
}
