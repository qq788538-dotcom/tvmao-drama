// Package endpoint 读取桌面端壳写下的 CLI 地址文件（CLI spec 3.2）。
// sidecar 每次启动用随机端口和新 token，CLI 每次执行都重新读这个文件，不缓存。
package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const FileName = "cli-endpoint.json"

// appDirName 与 tauri.conf.json 的 identifier 一致（壳用 app_data_dir()）。
const appDirName = "com.miaoju.desktop"

// ErrNotRunning：地址文件不存在，应用没开（或已正常退出）。
var ErrNotRunning = errors.New("喵剧未运行")

type Info struct {
	Version    int    `json:"version"`
	Port       int    `json:"port"`
	Token      string `json:"token"`
	AppVersion string `json:"appVersion"`
}

func (i Info) BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", i.Port) }

func DefaultDir() (string, error) { return defaultDir(runtime.GOOS, os.UserHomeDir, os.Getenv) }

func defaultDir(goos string, home func() (string, error), getenv func(string) string) (string, error) {
	switch goos {
	case "darwin":
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("无法确定用户主目录: %w", err)
		}
		return filepath.Join(h, "Library", "Application Support", appDirName), nil
	case "windows":
		a := getenv("APPDATA")
		if a == "" {
			return "", errors.New("未找到 APPDATA 环境变量")
		}
		return filepath.Join(a, appDirName), nil
	default:
		return "", fmt.Errorf("喵剧桌面端只支持 macOS 与 Windows（当前 %s）", goos)
	}
}

func Load(dir string) (Info, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, ErrNotRunning
	}
	if err != nil {
		return Info{}, fmt.Errorf("读取地址文件: %w", err)
	}
	var i Info
	if err := json.Unmarshal(b, &i); err != nil {
		return Info{}, fmt.Errorf("地址文件格式错误: %w", err)
	}
	if i.Version != 1 {
		return Info{}, fmt.Errorf("地址文件版本 %d 不受支持，请在喵剧 设置 → 命令行工具 中重新安装", i.Version)
	}
	if i.Port <= 0 || i.Port > 65535 || i.Token == "" {
		return Info{}, errors.New("地址文件内容不完整")
	}
	return i, nil
}
