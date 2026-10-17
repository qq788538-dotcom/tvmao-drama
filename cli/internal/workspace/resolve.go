// Package workspace 解析「操作哪个项目」。
//
// 刻意不提供 config.json / profile / `miaoju config`：只有 --project 与目录绑定
// 两层显式优先级，没有隐式配置合并。
//
// （包不叫 context：包名 context 会和标准库撞，
// 每个同时用两者的文件都得起别名。）
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectBinding 是 `miaoju project use` 写在当前目录的绑定。
type ProjectBinding struct {
	Version int `json:"version"`
	// 旧版绑定文件里可能还有 server 字段，反序列化时直接忽略。
	ProjectID int64 `json:"projectId"`
}

const (
	bindingDir  = ".miaoju"
	bindingFile = "project.json"
)

func BindingPath(dir string) string { return filepath.Join(dir, bindingDir, bindingFile) }

// LoadProjectBinding 只读当前目录，**不向父目录查找**。
// 向上查找会让「我在哪个项目里」变得不可预测——cd 进子目录还在操作父项目，
// 是一类很难发现的误操作。
func LoadProjectBinding(dir string) (*ProjectBinding, error) {
	blob, err := os.ReadFile(BindingPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var b ProjectBinding
	if err := json.Unmarshal(blob, &b); err != nil {
		return nil, fmt.Errorf("%s 内容损坏，请重新执行 miaoju project use", BindingPath(dir))
	}
	return &b, nil
}

func SaveProjectBinding(dir string, b ProjectBinding) error {
	b.Version = 1
	if err := os.MkdirAll(filepath.Join(dir, bindingDir), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(BindingPath(dir), blob, 0o644)
}

func RemoveProjectBinding(dir string) error {
	err := os.Remove(BindingPath(dir))
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ResolveProject 按 --project > 目录绑定 的顺序确定项目。
//
// 都没有时**报错，不猜最近使用的项目**：猜错的代价是往别人的画布里写节点。
func ResolveProject(flagValue int64, dir string) (int64, error) {
	if flagValue > 0 {
		return flagValue, nil
	}
	b, err := LoadProjectBinding(dir)
	if err != nil {
		return 0, err
	}
	if b == nil || b.ProjectID <= 0 {
		return 0, errors.New("未指定项目：用 --project <id>，或先在本目录执行 miaoju project use <id>")
	}
	return b.ProjectID, nil
}
