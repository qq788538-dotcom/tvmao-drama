package generation

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TempDirStaleAfter：启动清理只删「超过这么久没动过」的文件。在飞任务最长 30 分钟
// 超时,加上蓝绿 drain 也到不了 1 小时,所以超过它的一定是上次进程被强杀留下的残骸。
// 不整目录清空——蓝绿切换时新色启动那一刻旧色还在 drain,手里的参考图/产物临时文件
// 正在用,整清会让那些任务失败退款。共用一个目录、按时效清,两色零配置。
const TempDirStaleAfter = time.Hour

// PrepareTempDir 建目录并清掉过期残骸。目录不存在就建,存在就只扫一层文件。
func PrepareTempDir(root string, staleAfter time.Duration) error {
	if root == "" {
		return fmt.Errorf("gen temp dir is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create gen temp dir %q: %w", root, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read gen temp dir %q: %w", root, err)
	}
	cutoff := time.Now().Add(-staleAfter)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(root, e.Name()))
		}
	}
	return nil
}

// SetTempDir 注入参考图/multipart/产物临时文件的落盘目录（main.go 在 PrepareTempDir
// 后调用）。不改 NewService 签名——与 SetLeaseConfig 同一 setter 惯例。
func (s *Service) SetTempDir(dir string) { s.tmpDir = dir }

// tempDir 未注入时回退 os.TempDir：单测和本地 dev 不用先建目录。
func (s *Service) tempDir() string {
	if s.tmpDir != "" {
		return s.tmpDir
	}
	return os.TempDir()
}
