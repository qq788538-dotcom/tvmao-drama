package generation

import (
	"errors"
	"fmt"
)

// ErrStorageQuotaExceeded：用户存储已满时拒绝提交新生成任务的哨兵。
//
// 约定的语义是「提交时拦、产物落库永远放行」：积分在提交入口冻结，如果等到
// finalize/persistOutputs 阶段才拦，用户就是花了钱拿不到图。所以这道闸只存在
// 于两个提交入口（SubmitTx / Submit 直连路径）的冻结之前；产物落库路径对配额
// 无条件放行，超出的部分靠「下一次提交被拒」收敛，不靠事后拦截。
var ErrStorageQuotaExceeded = errors.New("存储空间已满，请清理后重试再生成")

// ensureStorageQuota 是所有生成提交入口共用的配额判定：used >= quota 即拒
// （恰好等于也算满）。不做 used+预估产物 > quota 的形态——生成前无从知道产物
// 多大，估出来的只是假精确；「已满就别再开新任务」是这道闸的全部语义。
// 读取与产物落库之间的 TOCTOU 窗口可接受，口径同 library 上传闸：在途任务
// 最多让用户超出一批产物的量，每次新提交都要重新过闸，不会无限超出。
func (s *Service) ensureStorageQuota(userID int64) error {
	// users 依赖缺席时跳过：部分测试装配（如 tx_submit_db_test）不注入 users，
	// 生产 wiring（main.go）恒注入，这里不是给生产留的逃生门。
	if s.users == nil {
		return nil
	}
	used, quota, err := s.users.StorageUsage(userID)
	if err != nil {
		return fmt.Errorf("load storage usage: %w", err)
	}
	if used >= quota {
		return ErrStorageQuotaExceeded
	}
	return nil
}
