// Package skillbundle 把脚本节点用到的 Skill 目录编进 sidecar，启动时释放到数据目录，
// 供 skillruntime.Catalog 按普通目录读取。Skill 内容用 scripts/sync-skills.sh 同步。
package skillbundle

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// all: 前缀不能省：否则 go:embed 会跳过 scripts/__init__.py 这类以 _ 开头的文件。
//
//go:embed all:skills
var bundled embed.FS

const root = "skills"

// Names 返回内置 Skill 的目录名（字典序）。
func Names() ([]string, error) {
	entries, err := fs.ReadDir(bundled, root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Extract 清空 dst 后把整棵 Skill 树释放进去，返回 dst 的绝对路径。
// 每次启动都重新释放：版本永远与 sidecar 一致，用户误改的文件也会被还原。
func Extract(dst string) (string, error) {
	if strings.TrimSpace(dst) == "" {
		return "", errors.New("内置 Skill 释放目录为空")
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(abs); err != nil {
		return "", fmt.Errorf("清理旧的内置 Skill 目录: %w", err)
	}
	err = fs.WalkDir(bundled, root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		target := filepath.Join(abs, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundled.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("释放内置 Skill: %w", err)
	}
	return abs, nil
}
