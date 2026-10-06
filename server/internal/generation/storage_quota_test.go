package generation

import (
	"database/sql"
	"errors"
	"testing"
)

// quotaFakeUsers 是 UserStorage 的手写 fake，只为 ensureStorageQuota 三态判定。
type quotaFakeUsers struct {
	used, quota int64
	err         error
}

func (f quotaFakeUsers) AddStorage(int64, int64) error            { return nil }
func (f quotaFakeUsers) AddStorageTx(*sql.Tx, int64, int64) error { return nil }
func (f quotaFakeUsers) StorageUsage(int64) (int64, int64, error) {
	return f.used, f.quota, f.err
}

// 提交闸的判定边界：used >= quota 拒（恰好等于也算满——闸的语义是「已满就别再
// 开新任务」，不是「还差一点点也放行」）。产物落库路径不经过这个函数，落库
// 永远放行的语义由「闸只挂在两个提交入口」这一结构保证，不需要在这测。
func TestEnsureStorageQuota(t *testing.T) {
	cases := []struct {
		name        string
		used, quota int64
		wantReject  bool
	}{
		{"未超额放行", 99, 100, false},
		{"恰好等于配额=已满,拒", 100, 100, true},
		{"已超额,拒", 101, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{users: quotaFakeUsers{used: tc.used, quota: tc.quota}}
			err := s.ensureStorageQuota(7)
			if tc.wantReject {
				if !errors.Is(err, ErrStorageQuotaExceeded) {
					t.Fatalf("used=%d quota=%d 应返回 ErrStorageQuotaExceeded, got %v", tc.used, tc.quota, err)
				}
			} else if err != nil {
				t.Fatalf("used=%d quota=%d 应放行, got %v", tc.used, tc.quota, err)
			}
		})
	}
}

// users 依赖缺席（部分测试装配不带 users）时闸静默跳过，不报错。
func TestEnsureStorageQuotaSkipsWithoutUsersDep(t *testing.T) {
	s := &Service{}
	if err := s.ensureStorageQuota(7); err != nil {
		t.Fatalf("users=nil 应跳过闸, got %v", err)
	}
}

// 用量读取失败必须向上传播（fail-closed）：同一个 DB 马上还要建行，读不出
// 用量说明 DB 已经不健康，静默放行只会把错误推迟到更贵的地方。
func TestEnsureStorageQuotaPropagatesLookupError(t *testing.T) {
	boom := errors.New("db down")
	s := &Service{users: quotaFakeUsers{err: boom}}
	err := s.ensureStorageQuota(7)
	if !errors.Is(err, boom) {
		t.Fatalf("查询失败应传播原始错误, got %v", err)
	}
	if errors.Is(err, ErrStorageQuotaExceeded) {
		t.Fatal("查询失败不该被误判成配额超限")
	}
}
