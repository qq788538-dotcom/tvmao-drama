package skillruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"gopkg.in/yaml.v3"
)

var errReadOnly = errors.New("skill filesystem is read-only")

// Catalog discovers standard SKILL.md files from immediate child directories.
type Catalog struct {
	root    string
	files   *ReadOnlyBackend
	backend skill.Backend
}

// Snapshot is the complete SKILL.md instruction set selected for one task run.
// It is JSON serializable so durable retries can reuse the same instructions.
type Snapshot struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Context        skill.ContextMode `json:"context,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	Model          string            `json:"model,omitempty"`
	OutputArtifact string            `json:"outputArtifact,omitempty"`
	Content        string            `json:"content"`
	BaseDirectory  string            `json:"baseDirectory"`
}

type skillRuntimeFrontMatter map[string]yaml.Node

type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Title 是 SKILL.md frontmatter 里可选的中文展示名。缺省为空——后台展示时回退到 Name。
	// 独立于 Description: Description 保留给能自动路由 skill 的 agent(Codex/Claude)当英文触发器,
	// Title 只服务后台下拉的人眼辨识, 两个关注点不互相污染。
	Title string `json:"title,omitempty"`
}

func NewCatalog(root string) (*Catalog, error) {
	files, err := NewReadOnlyBackend(root)
	if err != nil {
		return nil, err
	}
	backend, err := skill.NewBackendFromFilesystem(context.Background(), &skill.BackendFromFilesystemConfig{
		Backend: files,
		BaseDir: files.Root(),
	})
	if err != nil {
		return nil, err
	}
	return &Catalog{root: files.Root(), files: files, backend: backend}, nil
}

func (c *Catalog) Root() string { return c.root }

func (c *Catalog) Filesystem() filesystem.Backend { return c.files }

func (c *Catalog) List(ctx context.Context) ([]skill.FrontMatter, error) {
	items, err := c.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (c *Catalog) Get(ctx context.Context, name string) (skill.Skill, error) {
	return c.backend.Get(ctx, name)
}

func (c *Catalog) ListSkills(ctx context.Context) ([]SkillInfo, error) {
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillInfo, 0, len(items))
	for _, item := range items {
		info := SkillInfo{Name: item.Name, Description: item.Description}
		// title 是纯展示字段, 读取的任何失败都吞成空——绝不能因为某个 skill 缺 title
		// 或 frontmatter 读不到而让整个列表接口报错(兼容旧 SKILL.md)。
		if loaded, err := c.Get(ctx, item.Name); err == nil {
			info.Title = c.skillTitle(ctx, loaded.BaseDirectory)
		}
		out = append(out, info)
	}
	return out, nil
}

// skillTitle 读 baseDirectory/SKILL.md 的 frontmatter, 取可选的 title 字段。
// 任何读取/解析失败或字段缺失都返回空串, 由调用方回退到 Name。
func (c *Catalog) skillTitle(ctx context.Context, baseDirectory string) string {
	content, err := c.files.Read(ctx, &filesystem.ReadRequest{
		FilePath: filepath.Join(baseDirectory, "SKILL.md"),
	})
	if err != nil {
		return ""
	}
	frontMatter, err := parseSkillRuntimeFrontMatter(content.Content)
	if err != nil {
		return ""
	}
	node, ok := frontMatter["title"]
	if !ok || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func (c *Catalog) Snapshot(ctx context.Context, name string) (Snapshot, error) {
	loaded, err := c.Get(ctx, name)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := snapshotFromSkill(loaded)
	outputArtifact, err := c.outputArtifact(ctx, loaded.BaseDirectory)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.OutputArtifact = outputArtifact
	rel, err := filepath.Rel(c.root, loaded.BaseDirectory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Snapshot{}, errors.New("skill directory is outside the configured root")
	}
	snapshot.BaseDirectory = rel
	return snapshot, nil
}

func (c *Catalog) outputArtifact(ctx context.Context, baseDirectory string) (string, error) {
	content, err := c.files.Read(ctx, &filesystem.ReadRequest{
		FilePath: filepath.Join(baseDirectory, "SKILL.md"),
	})
	if err != nil {
		return "", fmt.Errorf("read skill frontmatter: %w", err)
	}
	frontMatter, err := parseSkillRuntimeFrontMatter(content.Content)
	if err != nil {
		return "", err
	}
	outputArtifact, ok := frontMatter["output_artifact"]
	if !ok {
		return "", nil
	}
	if outputArtifact.Kind != yaml.ScalarNode || outputArtifact.ShortTag() != "!!str" {
		return "", errors.New("output_artifact must be a string")
	}
	return validateArtifactPath(outputArtifact.Value)
}

func parseSkillRuntimeFrontMatter(content string) (skillRuntimeFrontMatter, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return skillRuntimeFrontMatter{}, errors.New("skill frontmatter is missing")
	}
	rest := content[3:]
	closing := strings.Index(rest, "\n---")
	if closing < 0 {
		return skillRuntimeFrontMatter{}, errors.New("skill frontmatter is not terminated")
	}
	var frontMatter skillRuntimeFrontMatter
	if err := yaml.Unmarshal([]byte(rest[:closing]), &frontMatter); err != nil {
		return skillRuntimeFrontMatter{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	return frontMatter, nil
}

func validateArtifactPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("output_artifact must not be empty")
	}
	if filepath.IsAbs(value) {
		return "", errors.New("output_artifact must be a relative path")
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("output_artifact must stay within the skill directory")
	}
	return cleaned, nil
}

func snapshotFromSkill(value skill.Skill) Snapshot {
	return Snapshot{
		Name: value.Name, Description: value.Description, Context: value.Context,
		Agent: value.Agent, Model: value.Model, Content: value.Content,
		BaseDirectory: value.BaseDirectory,
	}
}

func (s Snapshot) einoSkill() skill.Skill {
	return skill.Skill{
		FrontMatter: skill.FrontMatter{
			Name: s.Name, Description: s.Description, Context: s.Context,
			Agent: s.Agent, Model: s.Model,
		},
		Content: s.Content, BaseDirectory: s.BaseDirectory,
	}
}

func (s Snapshot) WithVariables(variables map[string]string) Snapshot {
	s.Content = substituteVariables(s.Content, variables)
	return s
}

// ReadOnlyBackend confines all reads to one root and rejects writes and symlink escapes.
type ReadOnlyBackend struct{ root string }

func NewReadOnlyBackend(root string) (*ReadOnlyBackend, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("skill root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve skill root: %w", err)
	}
	abs = filepath.Clean(abs)
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	} else if !errors.Is(evalErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("resolve skill root symlinks: %w", evalErr)
	}
	return &ReadOnlyBackend{root: abs}, nil
}

func (b *ReadOnlyBackend) Root() string { return b.root }

func (b *ReadOnlyBackend) resolve(path string) (string, error) {
	if strings.ContainsRune(path, '\x00') {
		return "", errors.New("invalid path")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(b.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if !withinRoot(b.root, candidate) {
		return "", fmt.Errorf("path escapes skill root: %s", path)
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(b.root, real) {
		return "", fmt.Errorf("path resolves outside skill root: %s", path)
	}
	return real, nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (b *ReadOnlyBackend) LsInfo(_ context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path, err := b.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]filesystem.FileInfo, 0, len(entries))
	for _, entry := range entries {
		full, resolveErr := b.resolve(filepath.Join(path, entry.Name()))
		if resolveErr != nil {
			continue
		}
		info, infoErr := os.Stat(full)
		if infoErr != nil {
			continue
		}
		out = append(out, fileInfo(full, info))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (b *ReadOnlyBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path, err := b.resolve(req.FilePath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	offset := req.Offset
	if offset < 1 {
		offset = 1
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if req.Limit > 0 && len(lines) >= req.Limit {
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &filesystem.FileContent{Content: strings.Join(lines, "\n")}, nil
}

func (b *ReadOnlyBackend) GrepRaw(_ context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	base := req.Path
	if base == "" {
		base = b.root
	}
	path, err := b.resolve(base)
	if err != nil {
		return nil, err
	}
	pattern := req.Pattern
	if req.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	var out []filesystem.GrepMatch
	err = filepath.WalkDir(path, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		resolved, resolveErr := b.resolve(filePath)
		if resolveErr != nil {
			return nil
		}
		if req.Glob != "" {
			rel, _ := filepath.Rel(path, resolved)
			matched, matchErr := doublestar.Match(req.Glob, filepath.ToSlash(rel))
			if matchErr != nil {
				return matchErr
			}
			if !matched {
				return nil
			}
		}
		f, openErr := os.Open(resolved)
		if openErr != nil {
			return openErr
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		line := 0
		for scanner.Scan() {
			line++
			if re.MatchString(scanner.Text()) {
				out = append(out, filesystem.GrepMatch{Path: resolved, Line: line, Content: scanner.Text()})
			}
		}
		scanErr := scanner.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return scanErr
		}
		return closeErr
	})
	return out, err
}

func (b *ReadOnlyBackend) GlobInfo(_ context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	base := req.Path
	if base == "" {
		base = b.root
	}
	path, err := b.resolve(base)
	if errors.Is(err, fs.ErrNotExist) {
		return []filesystem.FileInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	matches, err := doublestar.Glob(os.DirFS(path), filepath.ToSlash(req.Pattern), doublestar.WithFailOnIOErrors())
	if err != nil {
		return nil, err
	}
	out := make([]filesystem.FileInfo, 0, len(matches))
	for _, match := range matches {
		full, resolveErr := b.resolve(filepath.Join(path, filepath.FromSlash(match)))
		if resolveErr != nil {
			continue
		}
		info, infoErr := os.Stat(full)
		if infoErr != nil {
			continue
		}
		out = append(out, fileInfo(full, info))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func fileInfo(path string, info fs.FileInfo) filesystem.FileInfo {
	return filesystem.FileInfo{
		Path:       path,
		IsDir:      info.IsDir(),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func (b *ReadOnlyBackend) Write(context.Context, *filesystem.WriteRequest) error { return errReadOnly }

func (b *ReadOnlyBackend) Edit(context.Context, *filesystem.EditRequest) error { return errReadOnly }

var _ filesystem.Backend = (*ReadOnlyBackend)(nil)
var _ skill.Backend = (*Catalog)(nil)
