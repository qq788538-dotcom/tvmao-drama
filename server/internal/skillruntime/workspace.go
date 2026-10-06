package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

type RunWorkspace struct {
	root string
	now  func() time.Time
}

func NewRunWorkspace(root string) (*RunWorkspace, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("Skill Run workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	if err := os.Chmod(real, 0o700); err != nil {
		return nil, fmt.Errorf("chmod workspace root: %w", err)
	}
	return &RunWorkspace{root: real, now: time.Now}, nil
}

func (w *RunWorkspace) Root() string { return w.root }

func (w *RunWorkspace) Create(source string) (string, error) {
	if w == nil || strings.TrimSpace(w.root) == "" {
		return "", errors.New("Skill Run workspace is not configured")
	}
	day := w.now().In(time.Local).Format("20060102")
	dayDir := filepath.Join(w.root, day)
	if err := os.MkdirAll(dayDir, 0o700); err != nil {
		return "", fmt.Errorf("create workspace date directory: %w", err)
	}
	if err := os.Chmod(dayDir, 0o700); err != nil {
		return "", fmt.Errorf("chmod workspace date directory: %w", err)
	}
	runDir, err := os.MkdirTemp(dayDir, "run-")
	if err != nil {
		return "", fmt.Errorf("create Run workspace: %w", err)
	}
	if err := os.Chmod(runDir, 0o700); err != nil {
		return runDir, fmt.Errorf("chmod Run workspace: %w", err)
	}
	if err := copySkillTree(source, runDir); err != nil {
		return runDir, fmt.Errorf("copy Skill into Run workspace: %w", err)
	}
	return runDir, nil
}

func copySkillTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("Skill source is not a directory")
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported Skill file type at %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			return os.Chmod(target, 0o700)
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("unsupported Skill file type at %s", path)
		}
		return copySkillFile(path, target, entryInfo.Mode())
	})
}

func copySkillFile(source, destination string, sourceMode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o600)
	if sourceMode.Perm()&0o111 != 0 {
		mode = 0o700
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(destination, mode)
}

// WorkspaceBackend reuses the catalog backend's confined read operations but
// enables writes only inside one copied Run workspace.
type WorkspaceBackend struct{ *ReadOnlyBackend }

func NewWorkspaceBackend(root string) (*WorkspaceBackend, error) {
	backend, err := NewReadOnlyBackend(root)
	if err != nil {
		return nil, err
	}
	return &WorkspaceBackend{ReadOnlyBackend: backend}, nil
}

func (b *WorkspaceBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return errors.New("write request is required")
	}
	path, err := b.resolveForWrite(req.FilePath)
	if err != nil {
		return err
	}
	return b.atomicWrite(path, req.Content)
}

func (b *WorkspaceBackend) Edit(_ context.Context, req *filesystem.EditRequest) error {
	if req == nil {
		return errors.New("edit request is required")
	}
	if req.OldString == "" {
		return errors.New("old string is required")
	}
	if req.OldString == req.NewString {
		return errors.New("new string must differ from old string")
	}
	path, err := b.resolveForWrite(req.FilePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("workspace edit target must be a regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(raw)
	count := strings.Count(content, req.OldString)
	if count == 0 {
		return errors.New("old string not found")
	}
	if !req.ReplaceAll && count != 1 {
		return fmt.Errorf("old string appears %d times; set replace_all to replace every occurrence", count)
	}
	limit := 1
	if req.ReplaceAll {
		limit = -1
	}
	return b.atomicWrite(path, strings.Replace(content, req.OldString, req.NewString, limit))
}

func (b *WorkspaceBackend) resolveForWrite(path string) (string, error) {
	if b == nil || b.ReadOnlyBackend == nil {
		return "", errors.New("workspace backend is not configured")
	}
	if path == "" || strings.ContainsRune(path, '\x00') {
		return "", errors.New("invalid workspace path")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(b.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if !withinRoot(b.root, candidate) || candidate == b.root {
		return "", fmt.Errorf("path escapes workspace root: %s", path)
	}
	if info, err := os.Lstat(candidate); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("workspace write target cannot be a symlink")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("workspace write target must be a regular file")
		}
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
		if !withinRoot(b.root, real) {
			return "", fmt.Errorf("path resolves outside workspace root: %s", path)
		}
		return candidate, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := b.validateExistingParent(filepath.Dir(candidate)); err != nil {
		return "", err
	}
	return candidate, nil
}

func (b *WorkspaceBackend) validateExistingParent(parent string) error {
	current := parent
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return errors.New("workspace write parent must be a real directory")
			}
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return err
			}
			if !withinRoot(b.root, real) {
				return errors.New("workspace write parent resolves outside root")
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		next := filepath.Dir(current)
		if next == current || !withinRoot(b.root, next) {
			return errors.New("workspace write parent escapes root")
		}
		current = next
	}
}

func (b *WorkspaceBackend) atomicWrite(path, content string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if !withinRoot(b.root, realParent) {
		return errors.New("workspace write parent resolves outside root")
	}
	tmp, err := os.CreateTemp(realParent, ".write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.WriteString(tmp, content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

var _ filesystem.Backend = (*WorkspaceBackend)(nil)
