package lifecycle

import (
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"time"
)

var (
	instanceOnce sync.Once
	instanceID   string
)

// InstanceID 返回本进程稳定唯一 id：hostname-启动纳秒-随机后缀。
// 用于任务租约的 owner_instance，区分"本实例正在跑"与"别的实例/已死实例遗留"。
func InstanceID() string {
	instanceOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "unknown"
		}
		instanceID = fmt.Sprintf("%s-%d-%04d", host, time.Now().UnixNano(), rand.IntN(10000))
	})
	return instanceID
}
