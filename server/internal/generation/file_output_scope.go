package generation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errFileOutputOutOfScope：上游结果里的 file:// 指向了 spill 临时文件以外的位置。
var errFileOutputOutOfScope = errors.New("上游返回的本地文件路径不在生成临时目录内，已拒绝")

// spillFilePrefix 与 upstream.spillOneString 的 os.CreateTemp(dir, "out-*.png") 对应。
const spillFilePrefix = "out-"

// resolveSpillOutputPath 校验 file:// 产物路径（I1）：只接受 tempDir 里、直接位于目录
// 第一层、按 spill 命名的普通文件。符号链接逐级解析后再比较，防止软链/".." 越界读到
// data.db、~/.ssh 之类的本机文件。返回解析后的真实路径。
func (s *Service) resolveSpillOutputPath(raw string) (string, error) {
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%w: %q", errFileOutputOutOfScope, raw)
	}
	root, err := filepath.EvalSymlinks(s.tempDir())
	if err != nil {
		return "", fmt.Errorf("%w: temp dir: %v", errFileOutputOutOfScope, err)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errFileOutputOutOfScope, err)
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %q", errFileOutputOutOfScope, raw)
	}
	if !strings.HasPrefix(rel, spillFilePrefix) {
		return "", fmt.Errorf("%w: %q", errFileOutputOutOfScope, raw)
	}
	// 原始路径本身若是软链（哪怕指向目录内）也拒绝：spill 文件由 CreateTemp 创建，不会是链接。
	if fi, err := os.Lstat(filepath.Clean(raw)); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q", errFileOutputOutOfScope, raw)
	}
	return real, nil
}
