package skillbundle

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestExtractWritesEverySkill(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "Application Support", "skills")
	got, err := Extract(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("Extract 应返回绝对路径，got %q", got)
	}
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 8 {
		t.Fatalf("应内置 8 个 Skill，got %v", names)
	}
	for _, name := range names {
		for _, rel := range []string{"SKILL.md", "scripts/validate_storyboard.py", "scripts/__init__.py"} {
			if _, err := os.Stat(filepath.Join(got, name, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s/%s 未释放: %v", name, rel, err)
			}
		}
		if _, err := os.Stat(filepath.Join(got, name, "tests")); err == nil {
			t.Errorf("%s/tests 不应打包", name)
		}
	}
}

func TestExtractReplacesStaleTree(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(dst, "old-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old-skill", "SKILL.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old-skill")); !os.IsNotExist(err) {
		t.Fatalf("旧版本残留的 Skill 应被清掉: %v", err)
	}
}

// 白名单只允许运行这些脚本，“只能写任务工作区”靠它们本身不写文件、不联网、不起子进程来保证。
// 同步了新版本 Skill 后这里失败，说明要重新评估白名单。
func TestBundledScriptsHaveNoSideEffects(t *testing.T) {
	forbidden := regexp.MustCompile(`subprocess|os\.system|os\.popen|socket|urllib|http\.client|requests|shutil|ctypes|` +
		`write_text\(|write_bytes\(|\bopen\(|os\.remove|os\.unlink|os\.rename|os\.replace|os\.environ|\beval\(|\bexec\(|__import__`)
	count := 0
	err := fs.WalkDir(bundled, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".py") {
			return err
		}
		count++
		data, err := bundled.ReadFile(p)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for line := 1; sc.Scan(); line++ {
			if m := forbidden.FindString(sc.Text()); m != "" {
				t.Errorf("%s:%d 含有 %q", p, line, m)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 19 {
		t.Fatalf("应扫描到至少 19 个 .py（8 个 __init__ + 11 个校验脚本），got %d", count)
	}
}

func TestSeedCanvasScriptStylesAreBundled(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "seed", "settings.jsonl"))
	if os.IsNotExist(err) {
		t.Skip("seed/settings.jsonl 不在预期位置")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	found := false
	for sc.Scan() {
		var row struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Key != "canvas_script_config" {
			continue
		}
		found = true
		var cfg struct {
			Styles []struct {
				SkillName string `json:"skillName"`
			} `json:"styles"`
		}
		if err := json.Unmarshal([]byte(row.Value), &cfg); err != nil {
			t.Fatal(err)
		}
		for _, s := range cfg.Styles {
			if !have[s.SkillName] {
				t.Errorf("风格引用的 Skill %q 没有内置，运行 scripts/sync-skills.sh", s.SkillName)
			}
		}
	}
	if !found {
		t.Fatal("seed 里没有 canvas_script_config")
	}
}
