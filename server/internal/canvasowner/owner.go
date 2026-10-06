// Package canvasowner 是画布「单人编辑锁」。参考仓用 Redis 实现，防止同一账号的多个标签页
// 互相顶掉编辑；桌面端单用户、单窗口（spec S11），多人协作与编辑锁已删除（spec X2），没有可争的
// 对手。这里保留参考仓的类型、错误与 SourceMark，给 CanvasMutationHandler 一个恒放行的实现，
// 让 handler 代码和前端协议原样不动。
package canvasowner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrOwnerLost    = errors.New("canvas owner lost")
	ErrOwnerExpired = errors.New("canvas owner expired")
)

type ClaimResult struct {
	OwnerToken string    `json:"ownerToken"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// ProbeResult 是一次「谁在编辑」的只读探测结果。Held=false 时其余字段无意义。
type ProbeResult struct {
	Held         bool
	UserID       int64
	ActorUserID  int64
	StaleSeconds int64
}

// claimTTL 只用来填 ClaimResult.ExpiresAt（参考仓生产配置 24h），本地实现不会真的过期。
const claimTTL = 24 * time.Hour

// Local 是桌面端的编辑锁：Claim 照常发放随机 ownerToken（前端用它末段前 8 位做广播回声识别），
// Require / Heartbeat 对任何 token 都放行（包括空串——前端按 X2 删掉心跳后可能不再 Claim），
// Probe 永远回答「无人持锁」。
type Local struct{}

func NewLocal() *Local { return &Local{} }

func (l *Local) Claim(_ context.Context, _ int64, userID int64, actorUserID int64) (ClaimResult, error) {
	token, err := newOwnerToken(userID, actorUserID)
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{OwnerToken: token, ExpiresAt: time.Now().Add(claimTTL)}, nil
}

func (l *Local) Heartbeat(context.Context, int64, string) error { return nil }

func (l *Local) Require(context.Context, int64, string) error { return nil }

func (l *Local) Probe(context.Context, int64) (ProbeResult, error) { return ProbeResult{}, nil }

// SourceMark 是变更广播帧里的「谁改的」指纹：锁值最后一段（随机 hex）的前 8 个字符。
// 帧会推给项目下所有连接，带完整 token 等于把编辑凭据广播出去；前 8 位足够认出自己的回声。
// 前端必须用同一条规则（token.split(':').pop().slice(0,8)）算。
func SourceMark(ownerToken string) string {
	if ownerToken == "" {
		return ""
	}
	segs := strings.Split(ownerToken, ":")
	last := segs[len(segs)-1]
	if len(last) > 8 {
		return last[:8]
	}
	return last
}

// newOwnerToken 生成 "<userID>:<actorID>:<32 位随机 hex>"，与参考仓锁值格式一致。
func newOwnerToken(userID, actorUserID int64) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strconv.FormatInt(userID, 10) + ":" +
		strconv.FormatInt(actorUserID, 10) + ":" +
		hex.EncodeToString(b[:]), nil
}
