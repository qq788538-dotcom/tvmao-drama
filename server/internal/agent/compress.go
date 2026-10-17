// apps/api/internal/agent/compress.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/billing"
)

// keepTailPctNum/keepTailPctDen 把压缩后保留原文的「尾巴」表示成 context window
// 的一个分数（20%）。曾经按条数保留（keepTail=6）：条数与内容量根本不挂钩——
// 一轮带工具的往返就产生 2~3 条，6 条只护住最近 2~3 个来回，长程多阶段对话
// （编剧七阶段这类）里「已锁定的设定」很快滚出尾巴、只活在有损摘要里。按 token
// 取：75% 触发、压完留 20%，余量 55% 不会压完马上又触发；后台换大窗口模型时
// 护住的内容自动变多。
const keepTailPctNum = 20
const keepTailPctDen = 100

// thresholdNum/thresholdDen 把压缩触发阈值表示成 context_window 的一个分数（75%）。
const thresholdNum = 75
const thresholdDen = 100

// Summarizer 把已有的 thread summary + 一批更老的消息合成出一段新的累积 summary。
// 包在 interface 后面，这样测试可以注入一个确定性的 stub，而不用通过 PlannerDeps
// 真起一个 LLM。
type Summarizer interface {
	Summarize(ctx context.Context, existing string, msgs []Message) (string, error)
}

// estimateTokens 给定文本返回一个粗略的 token 数。我们用 rune 数 / 3.5，因为
// 阈值本身留了 25% 的安全余量，而且语料里以中文字符为主——对中文来说 rune 数
// 比 byte 数是好得多的近似。
func estimateTokens(text string) int {
	return (len([]rune(text)) * 10) / 35
}

// estimateMessagesTokens 对 summary 以及每条消息 JSON 编码后的 content_blocks
// 累加基于 rune 的 token 估算。
func estimateMessagesTokens(msgs []Message, summary string) int {
	var b strings.Builder
	b.WriteString(summary)
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.Write([]byte(m.ContentBlocks))
	}
	return estimateTokens(b.String())
}

// CompressIfNeeded 对比 thread 当前大小和 planner 的 context window，如果超过
// 75% 就把更老的消息折进 thread summary。低于阈值、或消息数不超过 keepTail 时
// 直接 no-op。出错会记日志并吞掉——压缩绝不能卡住用户的一轮对话。
func CompressIfNeeded(ctx context.Context, repo Repository, sum Summarizer, threadID string, contextWindow int) error {
	if contextWindow <= 0 {
		return nil
	}
	if sum == nil {
		return nil
	}
	thread, err := repo.GetThread(threadID)
	if err != nil || thread == nil {
		return nil
	}
	active, err := repo.ListMessages(threadID)
	if err != nil {
		return nil
	}
	tokens := estimateMessagesTokens(active, thread.Summary)
	threshold := contextWindow * thresholdNum / thresholdDen
	if tokens < threshold {
		return nil
	}
	cut := computeCut(active, contextWindow)
	if cut <= 0 {
		return nil
	}
	// skill 指令快照绝不进摘要：它是这个 thread 的「已加载指令」（Claude Code 式
	// 跨轮生效机制的载体），被折进有损 summary 等于把多阶段 skill 的方法论压没。
	// 带 skill block 的消息整条留在 active 区（不标记、不喂 summarizer），其余
	// 照常折叠。
	toSummarize := make([]Message, 0, cut)
	for _, m := range active[:cut] {
		if hasSkillBlock(m) {
			continue
		}
		toSummarize = append(toSummarize, m)
	}
	if len(toSummarize) == 0 {
		return nil
	}

	newSummary, err := sum.Summarize(ctx, thread.Summary, toSummarize)
	if err != nil {
		log.Printf("agent compress: summarizer failed (thread=%s, tokens=%d): %v", threadID, tokens, err)
		return nil
	}
	if strings.TrimSpace(newSummary) == "" {
		log.Printf("agent compress: summarizer returned empty result (thread=%s)", threadID)
		return nil
	}
	// 「存 summary」和「标记消息已折叠」是两步写，repo 接口没有事务，半程失败
	// 必然留下不一致，只能选风险敞口小的一边：
	//   - 先 Mark 再存 summary：Mark 成功 + summary 保存失败 = 那批消息从 active
	//     消失且没进 summary——内容彻底丢失，不可接受；
	//   - 先存 summary 再 Mark（现有顺序）：summary 成功 + Mark 失败 = 消息既在
	//     summary 又留在 active——回放内容双份、再压缩会把同批消息二次折叠，
	//     可接受但要尽力避免。
	// 所以保持现有顺序，并在 Mark 失败时把 summary 回滚到旧值收敛回一致态；
	// 回滚也失败才落到「双份」状态，打醒目日志留给运维。
	oldSummary := thread.Summary
	if err := repo.UpdateThreadSummary(threadID, newSummary); err != nil {
		log.Printf("agent compress: persist summary failed (thread=%s): %v", threadID, err)
		return nil
	}
	ids := make([]string, 0, len(toSummarize))
	for _, m := range toSummarize {
		ids = append(ids, m.ID)
	}
	if err := repo.MarkMessagesSummarized(ids); err != nil {
		log.Printf("agent compress: mark summarized failed (thread=%s): %v — rolling back summary", threadID, err)
		if rbErr := repo.UpdateThreadSummary(threadID, oldSummary); rbErr != nil {
			log.Printf("agent compress: SUMMARY ROLLBACK FAILED (thread=%s): %v — %d messages now live both in summary and active history; next compression will fold them again", threadID, rbErr, len(toSummarize))
		}
		return nil
	}
	log.Printf("agent compress: thread=%s folded %d messages into summary (tokens=%d, threshold=%d)", threadID, len(toSummarize), tokens, threshold)
	return nil
}

// computeCut 决定 active[:cut] 折进 summary、active[cut:] 原样保留。规则：
//  1. 最近一条无条件保留（正在进行的回合不能被折走）；
//  2. 从新到旧累计，塞满约 20% context window 的 token 预算为止（keepTailPct*）；
//  3. 切点不能落在 tool 角色消息上：tool 行永远紧跟它的 assistant(tool_use) 行，
//     切在中间会把配对拆散——assistant 半边被折进 summary，保留区以孤儿 tool 行
//     开头，回放时严格 backbone（doubao 等）直接 400，该 thread 之后每次 Run 都
//     失败（还各白冻结一次 summarizer 费用）。向前挪到配对边界：宁可这一轮少压
//     几条，也不能切断配对。工具重度会话里裸切约 1/3 概率踩中 tool 行。
//
// 返回 0 表示这轮没有可压的东西（消息太少 / 尾巴吃下了全部 / 挪切点挪到了头）。
func computeCut(active []Message, contextWindow int) int {
	if len(active) < 2 {
		return 0
	}
	budget := contextWindow * keepTailPctNum / keepTailPctDen
	cut := len(active) - 1
	acc := estimateMessagesTokens(active[cut:], "")
	// i 停在 1：至少留 1 条给 summarizer，否则这轮压缩没有意义。
	for i := cut - 1; i >= 1; i-- {
		mt := estimateMessagesTokens(active[i:i+1], "")
		if acc+mt > budget {
			break
		}
		acc += mt
		cut = i
	}
	for cut > 0 && active[cut].Role == "tool" {
		cut--
	}
	return cut
}

// hasSkillBlock 判断一条持久化消息里是否带 skill 指令快照 block。解析失败按
// 「不带」处理——宁可照常压缩，也不因脏数据卡死压缩链路。
func hasSkillBlock(m Message) bool {
	var blocks []ContentBlock
	if err := json.Unmarshal(m.ContentBlocks, &blocks); err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "skill" {
			return true
		}
	}
	return false
}

// LLMSummarizer 是生产用的 Summarizer。它通过 PlannerDeps 解析 utility backbone，
// 请 LLM 把更老的消息折成一段新的中文 summary。当没有配置 utility backbone 时
// 返回 ("", nil)——并打一条 warning 日志——让 CompressIfNeeded 当作一次干净的跳过。
type LLMSummarizer struct {
	UserID int64
	Deps   PlannerDeps
}

// Summarize 对 utility backbone 跑一次同步的 Generate，并对这次 LLM 往返
// freeze→settle（失败 refund）。utility backbone 缺失时保持宽容：log + 返回空跳过
// （没模型就不压缩也不计费）。
func (s *LLMSummarizer) Summarize(ctx context.Context, existing string, msgs []Message) (string, error) {
	cm, mb, err := BuildUtilityModelForUser(ctx, s.UserID, s.Deps)
	if err != nil {
		log.Printf("agent compress: no utility backbone (user=%d): %v — skipping", s.UserID, err)
		return "", nil
	}

	// freeze 这次辅助 LLM 往返。Billing 为 nil（单测）时跳过计费。
	freezeAmt := billing.EstimateFreeze(mb, nil)
	freezeDone := false
	if s.Deps.Billing != nil {
		if _, ferr := s.Deps.Billing.Freeze(ctx, s.UserID, 0, freezeAmt, "AI 助手 · 记忆整理"); ferr != nil {
			return "", ferr
		}
		freezeDone = true
	}

	prompt := buildCompressionPrompt(existing, msgs)
	out, err := cm.Generate(ctx, []*schema.Message{{Role: schema.User, Content: prompt}})
	if err != nil {
		// freeze 后失败 → 退款（资金安全收口）。
		if freezeDone {
			chargeRefund(ctx, s.Deps.Billing, s.UserID, freezeAmt, "AI 助手 · 记忆整理")
		}
		return "", err
	}

	// 成功 → settle。从 out.ResponseMeta.Usage 拼 response map 复用 token 计费；
	// 拿不到 usage 按冻结额结算，不退不补。
	if freezeDone {
		chargeSettle(ctx, s.Deps.Billing, s.UserID, freezeAmt, mb, usageFromMessage(out), "AI 助手 · 记忆整理")
	}

	if out == nil {
		return "", nil
	}
	return strings.TrimSpace(out.Content), nil
}

// usageFromMessage 从一次 Generate 返回的消息里安全取出 *TokenUsage（out 或其
// ResponseMeta 可能为 nil）。取不到时返回 nil，让 chargeSettle 回退到冻结额。
func usageFromMessage(out *schema.Message) *schema.TokenUsage {
	if out == nil || out.ResponseMeta == nil {
		return nil
	}
	return out.ResponseMeta.Usage
}

// buildCompressionPrompt 渲染出我们交给 utility model 的 user 消息。每条更老的
// 消息都被压平成 "role: text" 行；这里丢掉非文本 block，因为 summary 的职责是
// 叙事压缩，而不是保留原始的 tool-call 载荷（那些仍留在 DB 行里，只是从回放里
// 被隐藏了）。
func buildCompressionPrompt(existing string, msgs []Message) string {
	var b strings.Builder
	// 「逐条原样保留」那句是长程多阶段对话（编剧七阶段这类）的保命线：用户在
	// 前几轮锁定的设定若被摘要概括掉一条，agent 当场失忆且用户不可见（界面读
	// DB 原文，只有喂 LLM 的这份被换掉）。
	b.WriteString("你是对话历史压缩助手。请把\"已有摘要\"和\"新历史\"合成一段不超过 800 字的中文摘要，保留事实/决定/数字/工件 id。用户已确认或已锁定的设定、参数、选择、决定，必须逐条原样保留，不得概括、合并或省略。直接输出摘要，不要别的内容。\n\n[已有摘要]\n")
	if strings.TrimSpace(existing) == "" {
		b.WriteString("（无）")
	} else {
		b.WriteString(existing)
	}
	b.WriteString("\n\n[新历史]\n")
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(flattenBlocksToText(m.ContentBlocks))
		b.WriteString("\n")
	}
	return b.String()
}

// flattenBlocksToText 从已持久化的 content_blocks 载荷里把文本类的部分抽出来，
// 让 prompt 保持可读。tool-use / tool-result 行被缩成一行标记，这样 summary 仍
// 记录了某个工具跑过，但不会把原始的 arguments JSON 全倒进去。
func flattenBlocksToText(raw json.RawMessage) string {
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil || len(blocks) == 0 {
		return string(raw)
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "tool_use":
			parts = append(parts, fmt.Sprintf("[tool_use %s]", b.Name))
		case "tool_result":
			parts = append(parts, fmt.Sprintf("[tool_result %s]", truncate(b.Content, 200)))
		}
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
