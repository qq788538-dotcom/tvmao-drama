package skillruntime

import (
	"bytes"
	"time"
)

// 单条 Skill 命令的输出上限与默认超时（与参考仓一致）。
const (
	maxShellOutputBytes = 1024 * 1024
	defaultShellTimeout = 30 * time.Second
)

// cappedBuffer 只保留前 max 字节，超出部分丢弃并标记 truncated；Write 永远报告全部写入，
// 免得子进程因管道写失败而提前退出。
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLen, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	return originalLen, nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
