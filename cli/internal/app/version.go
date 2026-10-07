package app

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/spf13/cobra"
)

func newVersionCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "输出 CLI 版本",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return st.out.Emit(map[string]string{"version": st.deps.Version})
		},
	}
}

// newIdempotencyKey 生成一次性幂等键。默认每条写命令都带，
// 这样 client 的写重试才是安全的。
func newIdempotencyKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时宁可不带 key：那样 client 就不会重试写请求，
		// 语义仍然安全，只是失去自动重试能力。
		return ""
	}
	return "cli-" + hex.EncodeToString(buf)
}
