// apps/api/internal/agent/loop.go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/james/huabu_studio/apps/api/internal/billing"
)

// agentSettleTimeout 给脱离请求生命周期的结算/退款 context 设上限——客户端断开会
// cancel 掉请求 context，但 freeze 后的资金收口必须照常完成（参考 chat handler）。
const agentSettleTimeout = 30 * time.Second

// defaultMaxRounds 给单次 Run 在被强制停止前能跑多少个 planner -> tool -> planner
// 循环设上限。防止 model 不停反复调工具时陷入失控循环。20 是给多阶段 skill
// （编剧七阶段这类「直出」模式一轮里连跑多个阶段+工具）留的余量；原 8 轮对
// 这类重 skill 会在收口前被掐断，且已花的积分不退。
const defaultMaxRounds = 20

// maxToolFanout 给同一回合内并发执行的 tool_call 数设上限。一次发 4 张图很常见，
// 但要给「planner 一口气发几十个工具」的病态情况留个保险，免得瞬间打爆上游 /
// 生成服务。超出上限的工具排队等空位，不丢。
const maxToolFanout = 8

// FrameSink 是把帧流式推给 WS client 的出站通道。实现方必须自己保证 Send 的并发
// 安全：同一次 Run 里 tool fanout（最多 maxToolFanout 个工具 goroutine）会经
// execTool 的 emit 并发调用 Send，loop 不替实现方做串行化。现有链路
// （UserSink → Hub.SendToProject → coder/websocket Conn.Write）恰好安全：Hub 的
// conn 表走 RWMutex，每条 ws 连接的写出由 websocket 库内部的写锁串行化。
type FrameSink interface {
	Send(Frame)
}

// Agent 每次 Run 驱动一个 ReAct 循环。Planner 特意声明成最小公约数的
// BaseChatModel，这样测试只用 Generate+Stream 就能 stub 它。工具绑定在运行时
// 决定：检查 planner 是否同时实现了 ToolCallingChatModel.WithTools（首选，不改
// 原对象）或 ChatModel.BindTools（旧式，原地改对象）。
type Agent struct {
	Planner   model.BaseChatModel
	Tools     ToolRegistry
	Repo      Repository
	Sink      FrameSink
	MaxRounds int

	// Signer 把写进 content_blocks 的 URL 做 canonicalize，让 DB 行能熬过 24h 的
	// signed-URL TTL。为 nil 时回退到原样存 URL（旧行为——会留下永久失效隐患）。
	Signer URLSigner
	// MediaResolver 把发给 planner 的 image_url 换成上游拿得到的地址（S12 接缝）。
	// nil = 不改写（参考仓行为）。只影响出网请求，不影响落库内容。
	MediaResolver LLMMediaResolver
	// MaxInlineImageBytes 是单次 LLM 请求里内联图片（data URI）的总字节上限；0 = 20MB。
	MaxInlineImageBytes int64

	// PlannerContextWindow 是 planner backbone 的总 context-window 预算（单位 token）。
	// 每次 Run 开头由 CompressIfNeeded 使用。为 0 时关闭自动压缩。
	PlannerContextWindow int

	// Summarizer 在触发压缩时被调用。为 nil 时即使 PlannerContextWindow > 0 也关闭压缩。
	Summarizer Summarizer

	// Billing + UserID + PlannerBilling 让每次 planner LLM 往返都像 chat 一样
	// freeze→settle（失败 refund）。Billing 为 nil 时（如纯单测用 stub planner）
	// streamOnce 完全跳过计费——生产里这三者都由 main.go wiring 填满。
	Billing        AgentBilling
	UserID         int64
	PlannerBilling billing.ModelBilling
}

// Run 端到端执行用户的一轮对话：
//   - 持久化 user 消息
//   - 发出 thread_start / action_start 帧
//   - 把工具绑定到 planner（按 Run 维度，尽量不改原对象）
//   - 循环至多 MaxRounds 次：流式跑 LLM -> 发帧 -> 若是 tool_use，跑工具并把
//     tool_result 喂回去 -> 否则发 action_end/thread_end 并返回
//   - 超过 max rounds 时发一个 error 帧并返回 error
//
// 当 skill 非 nil 时，本次 Run 的工具集会被过滤到 skill.ForceTools（不认识的名字
// 会记一条 warning 日志后跳过；ForceTools 为空时回退到完整 registry）。skill 的
// SystemPromptAddon 以全文快照的形式存进本轮 user 消息的 skill block，由历史回放
// 渲染成 System 消息——所以指令对本轮和**之后的每一轮**都生效（Claude Code 式），
// 而 ForceTools 的工具限制只作用于带 skill 的这一轮（工具集是 API 参数，进不了
// 历史）。
//
// mentions 存在时，会变成当前 user 消息上的多模态 part（image_url），让 planner
// 直接看到被显式 @ 引用的 canvas 内容。除此之外聊天 thread 跟 canvas 是隔离的：
// canvas 节点不会自动暴露给 LLM，所以只有用户主动 @ 引用某个资源时它才能用。
// 持久化仍然只存文本——mentions 每次调用都是新鲜的，绝不会从历史里回放。
func (a *Agent) Run(ctx context.Context, threadID, actionID, userText string, mentions []Mention, skill *Skill) error {
	maxRounds := a.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	a.Sink.Send(Frame{Type: FrameThreadStart, ThreadID: threadID})
	a.Sink.Send(Frame{Type: FrameActionStart, ThreadID: threadID, ActionID: actionID})

	// 在调用 planner 之前先持久化 user 消息，这样即使第一轮就崩了也能留下审计痕迹。
	// 选中的 skill + 带 URL 的 mentions 会被存成 skill / image block，这样历史重载时
	// 能重新渲染出 skill chip + 附带的图片；canvas-summary 是请求级的，不存。
	persistBlocks := make([]ContentBlock, 0, len(mentions)+2)
	if skill != nil {
		// Content 存 skill 指令的全文快照（Claude Code 式）：LLM 无状态，所谓
		// skill 跨轮生效 = 指令文本进入对话记录、随每轮历史重放自然带上。曾经
		// 只存 ID+Name（仅够前端重画 chip），第二轮起指令彻底消失——多步骤
		// skill 跑完第一步就断片。快照语义还顺带免疫「skill 事后被改/删，老会话
		// 跟着乱」。workflow 类型不存快照：它不喂 LLM，addon 不是对话指令。
		snapshot := ""
		if skill.SkillType != "workflow" {
			if sys := buildSkillSystemMessage(skill); sys != nil {
				snapshot = sys.Content
			}
		}
		persistBlocks = append(persistBlocks, ContentBlock{Type: "skill", ID: skill.ID, Name: skill.Name, Content: snapshot, Auto: skill.AutoTriggered})
	}
	persistBlocks = append(persistBlocks, ContentBlock{Type: "text", Text: userText})
	for _, m := range mentions {
		// 大文本附件不带正文也不一定带 URL：落成 file / text_ref block，存
		// artifact_id + 字数，回放时重新渲染成 attachmentRefLine。
		if m.Kind == "file" || m.Kind == "text" {
			blockType := "file"
			if m.Kind == "text" {
				blockType = "text_ref"
			}
			persistBlocks = append(persistBlocks, ContentBlock{Type: blockType, ID: m.ArtifactID, Name: m.Name, CharCount: m.CharCount})
			continue
		}
		if m.URL != "" {
			// block 类型跟着 mention 的 kind 走：视频/音频曾一律落成 image block，
			// 历史界面用 <img> 渲染视频 URL 直接破图。空 kind（旧客户端）保持 image。
			blockType := "image"
			if m.Kind == "video" || m.Kind == "audio" {
				blockType = m.Kind
			}
			persistBlocks = append(persistBlocks, ContentBlock{Type: blockType, URL: a.canonicalize(m.URL), Name: m.Name})
		}
	}
	userBlocks, _ := json.Marshal(persistBlocks)
	if err := a.Repo.AppendMessage(Message{
		ID:            uuid.NewString(),
		ThreadID:      threadID,
		ActionID:      actionID,
		Role:          "user",
		ContentBlocks: userBlocks,
	}); err != nil {
		a.sendFatal(threadID, actionID, "persist", err.Error())
		return err
	}

	// 按 skill_type 路由：
	//   - "workflow" → 跑声明式的 step-sequencer，而不是 LLM 循环。
	//   - "meta"     → 落到 LLM 循环，但工具集被限制到内置的 output_skill_definition
	//                  工具（由下面的 toolsForSkill 处理）。skill 的 system_prompt_addon
	//                  已经由 buildSkillSystemMessage 拼到前面了。
	//   - "force_tool" / "" / 未知 → 走已有路径。
	if skill != nil && skill.SkillType == "workflow" {
		// thread_start/action_start 上面已经发过了；runWorkflow 负责收尾整个 envelope。
		return a.runWorkflow(ctx, threadID, actionID, userText, skill)
	}

	// 在读回历史之前先自动压缩更老的历史。CompressIfNeeded 在 summarizer 为 nil、
	// thread 很小、或消息数不超过 keepTail 时本身就是 no-op。
	_ = CompressIfNeeded(ctx, a.Repo, a.Summarizer, threadID, a.PlannerContextWindow)

	history, err := a.buildHistory(threadID)
	if err != nil {
		a.sendFatal(threadID, actionID, "history", err.Error())
		return err
	}

	// 构造本次 Run 的工具集，可选地按 skill.ForceTools 过滤。
	tools := a.toolsForSkill(skill)
	if toolNames := toolNamesForLog(ctx, tools); len(toolNames) > 0 {
		log.Printf("agent run: thread=%s tools=%v", threadID, toolNames)
	} else {
		log.Printf("agent run: thread=%s tools=<empty>", threadID)
	}

	// history 以刚刚持久化的纯文本 user 消息结尾。当调用方带了 mentions 时，把这
	// 最后一条 user 消息换成多模态版本，让 planner 直接看到被引用的 canvas 图片。
	msgs := history
	if len(mentions) > 0 {
		userMsg := buildUserMessage(userText, mentions)
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == schema.User {
			msgs[len(msgs)-1] = userMsg
		} else {
			msgs = append(msgs, userMsg)
		}
	}

	// 全局 system prompt 拼在最前面，排在 buildHistory 可能带回的 thread summary
	// 之前。两个理由：语义上它是「你是谁、什么该做什么不该做」的底座，summary 是
	// 会话内容；工程上内容永不变的放最前，才谈得上被上游的前缀缓存命中。
	//
	// 在这里 prepend 而不是塞进 buildHistory：buildHistory 的语义是「回放历史」，
	// 而且有一批测试直接按下标断言它的返回值（skill_snapshot_test / pairing_prune_test），
	// 在它内部加一条会把那些断言的含义整体挪位。
	msgs = append([]*schema.Message{{Role: schema.System, Content: GlobalSystemPrompt}}, msgs...)

	// skill 指令不再在这里额外拼 System 消息：它已随本轮 user 消息的 skill block
	// 快照落库（上面 persistBlocks 处），而 buildHistory 回放 skill block 时会渲染出
	// System 消息——本轮和后续所有轮走的是同一条路径。再拼一次就是同文双份。

	boundModel, toolsBound, err := a.bindToolsWithList(ctx, tools)
	if err != nil {
		a.sendFatal(threadID, actionID, "tools", err.Error())
		return err
	}
	// 显式携带 tool_choice（ToolChoiceAllowed 无白名单 → 渲染成字符串 "auto"），
	// 不再依赖 eino WithTools 的隐式默认——那个默认藏在库里，排查上游 400 时
	// 没法从我们自己的代码确认出网形态（2026-08-17 测试环境排障的教训）。
	// 只在真绑了工具时带：不带 tools 的请求附 tool_choice 会被部分上游拒绝。
	var llmOpts []model.Option
	if toolsBound {
		llmOpts = append(llmOpts, model.WithToolChoice(schema.ToolChoiceAllowed))
	}

	// blockIdx 是整个 action 内全局递增的流式块序号。曾经每轮 streamOnce 都从 0
	// 重新起算，前端 store 按 index 定位块——第二轮的文本会实时覆盖第一轮同下标
	// 的块（最终态靠 action_end 后的 re-hydrate 兜底，但过程中内容会闪没）。
	blockIdx := 0
	for round := 0; round < maxRounds; round++ {
		assistant, nextIdx, serr := a.streamOnce(ctx, boundModel, msgs, threadID, actionID, blockIdx, llmOpts...)
		blockIdx = nextIdx
		if serr != nil {
			// 用户主动取消（abort 帧 → Hub.CancelRun 掐 Run ctx）不是故障：错误码
			// 固定 aborted，前端据此渲染中性的「已停止」而不是红色报错。计费边界
			//（用户拍板）：已消耗的 LLM 轮次照常结算（settle 走 Background ctx，
			// 取消掐不断）；已提交的生成任务不撤不退，继续跑完照常扣积分。
			if ctx.Err() != nil {
				a.sendFatal(threadID, actionID, "aborted", "已停止生成")
				return serr
			}
			a.sendFatal(threadID, actionID, "llm", serr.Error())
			return serr
		}

		blocks := assistantToBlocks(assistant, a.Tools)
		// LLM 偶尔会把 signed https URL 回显进它的回复文本里；持久化前把它们重写成
		// canonical 的 media://，这样 DB 行不会随 24h signed-URL TTL 失效。
		a.canonicalizeBlocks(blocks)
		stopReason := stopReasonOf(assistant)

		// 兜底保险（必须在落库之前）：万一 appendOrMergeToolCall 仍然产出一个没有
		// name 的 tool_call（某些 provider 在每个 chunk 上 Index 和 ID 都不给），就
		// 中止，而不是拿 "" 去 registry 里查。曾经这个校验在落库之后，DB 里留下
		//「有 ID、无 name、无配对 tool_result」的孤儿 tool_use（回放侧只过滤 ID 为
		// 空的），严格 backbone 直接 400、thread 从此每次 Run 都失败。现在先剥掉全部
		// tool_use block 只存文本——本轮一个工具都不会跑，留 tool_use 徒增孤儿。
		for _, tc := range assistant.ToolCalls {
			if strings.TrimSpace(tc.Function.Name) == "" {
				textOnly := make([]ContentBlock, 0, len(blocks))
				for _, b := range blocks {
					if b.Type != "tool_use" {
						textOnly = append(textOnly, b)
					}
				}
				if bj, merr := json.Marshal(textOnly); merr == nil {
					// 落库失败也不改变结局（本轮已注定终止），尽力保住文本部分即可。
					_ = a.Repo.AppendMessage(Message{
						ID:            uuid.NewString(),
						ThreadID:      threadID,
						ActionID:      actionID,
						Role:          "assistant",
						ContentBlocks: bj,
						StopReason:    "error",
					})
				}
				a.sendFatal(threadID, actionID, "empty_tool_call",
					"planner emitted tool_call with empty name (stream chunk merge anomaly); aborting")
				return errors.New("planner emitted tool_call with empty name")
			}
		}

		blocksJSON, merr := json.Marshal(blocks)
		if merr != nil {
			// 绝不能吞掉这个错误落成 []：同轮 tool_result 照常落库的话，配对被拆散，
			// 回放时严格 backbone 400、thread 永久变砖。Input 已在 assistantToBlocks
			// 里合法化过，这里理论上到不了；真到了就显式终止，本轮什么都不落。
			a.sendFatal(threadID, actionID, "persist", merr.Error())
			return merr
		}
		if perr := a.Repo.AppendMessage(Message{
			ID:            uuid.NewString(),
			ThreadID:      threadID,
			ActionID:      actionID,
			Role:          "assistant",
			ContentBlocks: blocksJSON,
			StopReason:    stopReason,
		}); perr != nil {
			a.sendFatal(threadID, actionID, "persist", perr.Error())
			return perr
		}

		if len(assistant.ToolCalls) == 0 || stopReason == "end_turn" {
			a.Sink.Send(Frame{Type: FrameActionEnd, ThreadID: threadID, ActionID: actionID, StopReason: stopReason})
			a.Sink.Send(Frame{Type: FrameThreadEnd, ThreadID: threadID})
			return nil
		}

		// Phase 0 串行执行 tool call；暂时还没有 fan-out 并发。
		// 并行 fan-out 执行同一回合的所有 tool_call——独立的生成任务（如一口气发
		// 4 张 generate_image）本就该并发，而不是串行排队等前一张跑完。结果按
		// tool_call 原始顺序回灌，这样喂给 LLM 的 tool 消息序列、持久化、历史重载
		// 都是确定的（实时 artifact 帧的到达顺序可以交错，无所谓，各自带 id）。
		// 并发安全：registry.ByName 走 RLock；execTool 的 captured/emit 每调用本地；
		// Sink→ws.Write 由 coder/websocket 内部 writeMu 串行化；生成服务自管并发。
		type toolOutcome struct {
			result    string
			artifacts []Artifact
		}
		outcomes := make([]toolOutcome, len(assistant.ToolCalls))
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxToolFanout)
		for i, tc := range assistant.ToolCalls {
			wg.Add(1)
			go func(i int, tc schema.ToolCall) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// 单个工具 panic 不该掀翻整个进程（现在是并发 goroutine，未捕获的
				// panic 会直接 crash server）——兜底 recover 成一条失败结果，让其余
				// 工具照常完成。
				defer func() {
					if r := recover(); r != nil {
						outcomes[i] = toolOutcome{result: fmt.Sprintf("Tool %s panicked: %v", tc.Function.Name, r)}
					}
				}()
				result, artifacts, terr := a.execTool(ctx, tc, threadID, actionID)
				if terr != nil {
					result = fmt.Sprintf("Tool %s failed: %v", tc.Function.Name, terr)
				}
				outcomes[i] = toolOutcome{result: result, artifacts: artifacts}
			}(i, tc)
		}
		wg.Wait()

		toolResultsForLLM := make([]*schema.Message, 0, len(assistant.ToolCalls))
		resultBlocks := make([]ContentBlock, 0, len(assistant.ToolCalls))
		for i, tc := range assistant.ToolCalls {
			oc := outcomes[i]
			resultBlocks = append(resultBlocks, ContentBlock{
				Type:      "tool_result",
				ToolUseID: tc.ID,
				Content:   oc.result,
			})
			// 把发出的任何 artifact 都存成一个 tool_event block，这样历史重载时能
			// 重新渲染出 image / video 缩略图。没有 artifact 时跳过（analyze_image
			// 这类纯文本工具返回 nil）。
			if len(oc.artifacts) > 0 {
				resultBlocks = append(resultBlocks, ContentBlock{
					Type:      "tool_event",
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Artifacts: a.canonicalizeArtifacts(oc.artifacts),
				})
			}
			toolResultsForLLM = append(toolResultsForLLM, &schema.Message{
				Role:       schema.Tool,
				ToolCallID: tc.ID,
				Content:    oc.result,
			})
		}
		// tool 结果经常含有可 canonicalize 的 URL（formatToolResult 写的是 signed
		// URL；tool_event artifact 前面已经 canonicalize 过了）。再走一遍，捕捉文本
		// 内容里的回显。
		a.canonicalizeBlocks(resultBlocks)
		rb, _ := json.Marshal(resultBlocks)
		if perr := a.Repo.AppendMessage(Message{
			ID:            uuid.NewString(),
			ThreadID:      threadID,
			ActionID:      actionID,
			Role:          "tool",
			ContentBlocks: rb,
		}); perr != nil {
			a.sendFatal(threadID, actionID, "persist", perr.Error())
			return perr
		}

		msgs = append(msgs, assistant)
		msgs = append(msgs, toolResultsForLLM...)
	}

	a.sendFatal(threadID, actionID, "max_rounds", "agent exceeded max rounds")
	return errors.New("agent exceeded max rounds")
}

// SendFatal 在 Run 的任意错误返回路径上发出一组确定的「终止帧」：先 error 描述，
// 再 action_end(stop_reason=error) + thread_end。这样即便 Run 半途失败（预算耗尽、
// LLM 流出错、持久化失败……），前端也能收到明确的终止信号、退出「正在思考」，
// 而不是依赖「最后一条消息恰好非 pending」这种间接启发式、或干脆永久转圈。
//
// error 帧必须带 ThreadID/ActionID：前端 handleFrame 进任何分支之前先按 thread_id
// 路由，缺了它整帧被静默丢弃——错误气泡永不显示、转圈只能等 21min 看门狗。曾经全部
// 8 处 FrameError 发射点都漏了这两个字段，错误处理分支因此整体是死代码。
// 导出成包级函数是因为 Run 之外还有两类「还没进 Agent.Run 就失败」的发射点
// （WS handler 的 thread_not_found、runner 的 skill_not_found / no_planner），
// 它们同样需要完整三件套，而不是各发各的裸 error 帧。
func SendFatal(sink FrameSink, threadID, actionID, code, msg string) {
	sink.Send(Frame{Type: FrameError, ThreadID: threadID, ActionID: actionID, Error: &ErrorPayload{Code: code, Message: msg}})
	sink.Send(Frame{Type: FrameActionEnd, ThreadID: threadID, ActionID: actionID, StopReason: "error"})
	sink.Send(Frame{Type: FrameThreadEnd, ThreadID: threadID})
}

func (a *Agent) sendFatal(threadID, actionID, code, msg string) {
	SendFatal(a.Sink, threadID, actionID, code, msg)
}

// bindToolsWithList 返回一个绑好给定工具列表、可直接用于本次 Run 的 planner。
// 首选路径是 ToolCallingChatModel.WithTools，因为它返回一个全新实例，在多个 Run
// 并发共享同一个 backbone 时也安全。只有当 planner 没实现 WithTools 时，我们才
// 回退到已废弃的 ChatModel.BindTools（原地改对象）。两者都没有时就原样返回 planner
// ——这对用 no-tool stub 的测试很有用。
// 第二个返回值报告是否真的绑上了工具——调用方据此决定要不要显式带 tool_choice
//（无工具的请求带 tool_choice 会被部分上游拒绝，测试 stub 也走 false 分支）。
func (a *Agent) bindToolsWithList(ctx context.Context, tools []Tool) (model.BaseChatModel, bool, error) {
	infos := toolInfosFromList(ctx, tools)
	if len(infos) == 0 {
		return a.Planner, false, nil
	}
	if tc, ok := a.Planner.(model.ToolCallingChatModel); ok {
		bound, err := tc.WithTools(infos)
		if err != nil {
			return nil, false, err
		}
		return bound, true, nil
	}
	if bm, ok := a.Planner.(model.ChatModel); ok {
		if err := bm.BindTools(infos); err != nil {
			return nil, false, err
		}
		return bm, true, nil
	}
	return a.Planner, false, nil
}

// toolNamesForLog 抽取工具名用于诊断日志。Info() 的报错会被跳过——结果尽力而为，
// 绝不阻塞 Run。
func toolNamesForLog(ctx context.Context, tools []Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if info, err := t.Info(ctx); err == nil && info != nil {
			names = append(names, info.Name)
		}
	}
	return names
}

// toolsForSkill 返回本次 Run 的工具快照，当挂了 skill 且它的 ForceTools 列表非空
// 时过滤到 skill.ForceTools。registry 里没有的名字会记一条 warning 日志后跳过，
// 而不是让 Run 失败——LLM 只是看到一个小一点的工具集。
//
// 当 skill.SkillType == "meta" 时，无视 ForceTools，工具集被强制为唯一一个内置的
// output_skill_definition 工具，这样 LLM 只能调这个 meta 工具来产出一个 SkillDraft。
func (a *Agent) toolsForSkill(skill *Skill) []Tool {
	if skill != nil && skill.SkillType == "meta" {
		if t, ok := a.Tools.ByName(OutputSkillDefinitionToolName); ok {
			return []Tool{t}
		}
		log.Printf("agent: meta skill %q but %s tool not registered", skill.ID, OutputSkillDefinitionToolName)
		return nil
	}
	if skill == nil || len(skill.ForceTools) == 0 {
		return a.Tools.Snapshot()
	}
	out := make([]Tool, 0, len(skill.ForceTools))
	for _, name := range skill.ForceTools {
		t, ok := a.Tools.ByName(name)
		if !ok {
			log.Printf("agent: skill %q references unknown tool %q (skipped)", skill.ID, name)
			continue
		}
		out = append(out, t)
	}
	return out
}

// runWorkflow 驱动一个 workflow 类型的 skill：不让 LLM 去规划 tool call，而是通过
// ExecuteWorkflow 把 workflow 的 WorkflowSteps 顺序执行掉。user 消息已经被持久化、
// thread_start / action_start 帧已经由调用方（Agent.Run）发出；runWorkflow 用
// action_end / thread_end 收尾整个 envelope，并持久化一条合成的 assistant 消息来
// 概括这次运行，让历史在多轮之间保持连贯。
func (a *Agent) runWorkflow(ctx context.Context, threadID, actionID, userText string, skill *Skill) error {
	steps, err := skill.DecodedWorkflowSteps()
	if err != nil {
		// 走完整三件套而不是裸 error 帧：这条路径发生在 action_start 之后、
		// action_end 之前，少了 action_end 前端的转圈解不开。
		a.sendFatal(threadID, actionID, "workflow_decode", err.Error())
		return err
	}
	if len(steps) == 0 {
		msg := fmt.Sprintf("skill %q has empty workflow_steps", skill.ID)
		a.sendFatal(threadID, actionID, "workflow_empty", msg)
		return errors.New(msg)
	}

	// 用 user prompt + skill ParamDefaults 给 initialVars 打底，这样各 step 能通过
	// {{input.prompt}} 引用提示词，并通过 {{input.<key>}} 引用任何默认参数。
	initial := map[string]any{"prompt": userText}
	for k, v := range skill.ParamDefaults {
		initial[k] = v
	}

	executed, wfErr := ExecuteWorkflow(ctx, a.Tools, steps, initial, threadID, actionID, a.Sink)
	stopReason := "end_turn"
	if wfErr != nil {
		stopReason = "error"
		// step 失败原因必须以 error 帧透出：step_end 帧不携带错误信息，没有这一帧
		// 用户看到的就是「跑完第 N 步凭空停了」，只能去猜后续步骤为什么没跑。
		a.Sink.Send(Frame{Type: FrameError, ThreadID: threadID, ActionID: actionID,
			Error: &ErrorPayload{Code: "workflow_step", Message: wfErr.Error()}})
	}

	a.Sink.Send(Frame{Type: FrameActionEnd, ThreadID: threadID, ActionID: actionID, StopReason: stopReason})
	a.Sink.Send(Frame{Type: FrameThreadEnd, ThreadID: threadID})

	// 持久化一条合成的 assistant 消息，让后续轮次能看到一条连贯的「用户问了 X、
	// workflow 跑了 Y 个 step」记录。这段文本是确定性的、而非 LLM 生成的，因为这条
	// 分支从不调用 planner。失败时写实际执行数——曾经无条件写 len(steps)，历史里
	// 一条半途而废的运行看起来像全部跑完了。
	summaryText := fmt.Sprintf("Workflow %q executed %d step(s).", skill.Name, executed)
	if wfErr != nil {
		summaryText = fmt.Sprintf("Workflow %q failed after %d/%d step(s): %v", skill.Name, executed, len(steps), wfErr)
	}
	asstBlocks := []ContentBlock{{
		Type: "text",
		Text: summaryText,
	}}
	a.canonicalizeBlocks(asstBlocks)
	blocksJSON, _ := json.Marshal(asstBlocks)
	if perr := a.Repo.AppendMessage(Message{
		ID:            uuid.NewString(),
		ThreadID:      threadID,
		ActionID:      actionID,
		Role:          "assistant",
		ContentBlocks: blocksJSON,
		StopReason:    stopReason,
	}); perr != nil {
		// workflow 跑完后做 summary 阶段的持久化失败，不应该盖掉 workflow 本身的
		// error（如果有的话），但仍然值得暴露出来。
		if wfErr == nil {
			a.Sink.Send(Frame{Type: FrameError, ThreadID: threadID, ActionID: actionID,
				Error: &ErrorPayload{Code: "persist", Message: perr.Error()}})
			return perr
		}
	}
	return wfErr
}

// buildSkillSystemMessage 在挂了 skill 时，组装出拼在历史前面的 System 消息。
// 没设 skill 时返回 nil。
func buildSkillSystemMessage(skill *Skill) *schema.Message {
	if skill == nil {
		return nil
	}
	content := fmt.Sprintf("[Skill: %s]\n%s", skill.Name, skill.SystemPromptAddon)
	if len(skill.ParamDefaults) > 0 {
		if b, err := json.Marshal(skill.ParamDefaults); err == nil {
			content += "\nDefault parameters: " + string(b)
		}
	}
	return &schema.Message{Role: schema.System, Content: content}
}

// streamOnce 消费一次 LLM stream，把 text delta 和 tool_use block 作为帧转发出去，
// 并返回本轮合并后的最终消息。合并后的 final.ToolCalls 跨多个 chunk 按 ID 累积，
// 因为 eino 每个 chunk 只发部分参数片段。
//
// baseIdx 是本轮首个流式块的全局序号（整个 action 内单调递增，由 Run 维护），
// 第二个返回值是消费掉的下一个可用序号。曾经每轮都从 0 起算，前端按 index 定位
// 块，第二轮的文本会实时覆盖第一轮同下标的块。
func (a *Agent) streamOnce(ctx context.Context, llm model.BaseChatModel, msgs []*schema.Message, threadID, actionID string, baseIdx int, llmOpts ...model.Option) (*schema.Message, int, error) {
	// freeze 这一次 LLM 往返。余额不足或冻结失败 → 直接让这次往返失败，不发起 Stream。
	// Billing 为 nil 时（单测 stub）freezeAmt 仍算出来但 freezeDone=false，后续不结算。
	freezeAmt := billing.EstimateFreeze(a.PlannerBilling, nil)
	freezeDone := false
	if a.Billing != nil {
		if _, err := a.Billing.Freeze(ctx, a.UserID, 0, freezeAmt, "AI 助手 · 思考"); err != nil {
			return nil, baseIdx, err
		}
		freezeDone = true
	}

	msgs = a.resolveMediaForLLM(ctx, msgs)
	stream, err := llm.Stream(ctx, msgs, llmOpts...)
	if err != nil {
		// freeze 已成立但 Stream 没起来 → 退款（资金安全：freeze 后每条出口必配对）。
		if freezeDone {
			chargeRefund(ctx, a.Billing, a.UserID, freezeAmt, "AI 助手 · 思考")
		}
		return nil, baseIdx, err
	}
	defer stream.Close()

	final := &schema.Message{Role: schema.Assistant}
	// 抓最后一个带 usage 的 chunk 的 ResponseMeta.Usage（覆盖式留末帧，对齐 chat 抓末帧 usage）。
	var lastUsage *schema.TokenUsage
	currentTextIdx := -1
	for {
		chunk, rerr := stream.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			// 消费中途出错 → freeze 后必须退款。
			if freezeDone {
				chargeRefund(ctx, a.Billing, a.UserID, freezeAmt, "AI 助手 · 思考")
			}
			return final, baseIdx, rerr
		}
		if chunk == nil {
			continue
		}
		if chunk.ResponseMeta != nil && chunk.ResponseMeta.Usage != nil {
			lastUsage = chunk.ResponseMeta.Usage
		}

		if chunk.Content != "" {
			if currentTextIdx == -1 {
				currentTextIdx = baseIdx
				a.Sink.Send(Frame{
					Type:     FrameContentBlockStart,
					ThreadID: threadID,
					ActionID: actionID,
					Index:    currentTextIdx,
					Block:    &ContentBlock{Type: "text"},
				})
			}
			a.Sink.Send(Frame{
				Type:     FrameContentBlockDelta,
				ThreadID: threadID,
				ActionID: actionID,
				Index:    currentTextIdx,
				Delta:    &BlockDelta{Type: "text_delta", Text: chunk.Content},
			})
			final.Content += chunk.Content
		}
		for _, tc := range chunk.ToolCalls {
			final.ToolCalls = appendOrMergeToolCall(final.ToolCalls, tc)
		}
	}
	if currentTextIdx >= 0 {
		a.Sink.Send(Frame{
			Type:     FrameContentBlockStop,
			ThreadID: threadID,
			ActionID: actionID,
			Index:    currentTextIdx,
		})
	}
	// 工具块序号排在文本块之后；本轮没有文本时直接从 baseIdx 起。
	toolBase := baseIdx
	if currentTextIdx >= 0 {
		toolBase = currentTextIdx + 1
	}
	for i, tc := range final.ToolCalls {
		idx := toolBase + i
		a.Sink.Send(Frame{
			Type:     FrameContentBlockStart,
			ThreadID: threadID,
			ActionID: actionID,
			Index:    idx,
			Block: &ContentBlock{
				Type:        "tool_use",
				ID:          tc.ID,
				Name:        tc.Function.Name,
				DisplayName: a.Tools.DisplayName(tc.Function.Name),
				Input:       json.RawMessage(tc.Function.Arguments),
			},
		})
		a.Sink.Send(Frame{
			Type:     FrameContentBlockStop,
			ThreadID: threadID,
			ActionID: actionID,
			Index:    idx,
		})
	}
	// EOF：这次往返成功完成 → settle。把 eino usage 拼成 SettleAmount 要的 response map
	// 复用现有 token 计费；拿不到 usage（lastUsage==nil 或算不出）按冻结额结算，不退不补。
	if freezeDone {
		chargeSettle(ctx, a.Billing, a.UserID, freezeAmt, a.PlannerBilling, lastUsage, "AI 助手 · 思考")
	}
	return final, toolBase + len(final.ToolCalls), nil
}

// execTool 按名字解析出一个工具，拿 model 给的 argument JSON 调它，并确保工具发出
// 的任何 Artifact 都被包进一个 tool_event content block 帧里，让 WS client 能实时
// 渲染。捕获到的 artifact 也会返回，让调用方能把它们和 tool_result block 一起持久化
// ——否则历史重载就只剩纯文本的 tool_result，图片缩略图会消失。
func (a *Agent) execTool(ctx context.Context, tc schema.ToolCall, threadID, actionID string) (string, []Artifact, error) {
	t, ok := a.Tools.ByName(tc.Function.Name)
	if !ok {
		return "", nil, fmt.Errorf("tool not registered: %s", tc.Function.Name)
	}
	var captured []Artifact
	emit := func(art Artifact) {
		captured = append(captured, art)
		a.Sink.Send(Frame{
			Type:     FrameContentBlockStart,
			ThreadID: threadID,
			ActionID: actionID,
			Block: &ContentBlock{
				Type:      "tool_event",
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Artifacts: []Artifact{art},
			},
		})
	}
	result, err := t.InvokableRun(WithEmit(ctx, emit), tc.Function.Arguments)
	return result, captured, err
}

// buildHistory 重新载入该 thread 所有此前未被 summarize 的消息，并把它们转回
// schema.Message 形态。当 thread 有累积 summary（更老的消息已被 CompressIfNeeded
// 折进去）时，该 summary 会作为一条 System 消息拼到前面，让 planner 仍保有叙事连贯性。
func (a *Agent) buildHistory(threadID string) ([]*schema.Message, error) {
	thread, err := a.Repo.GetThread(threadID)
	if err != nil {
		return nil, err
	}
	prior, err := a.Repo.ListMessages(threadID)
	if err != nil {
		return nil, err
	}
	out := make([]*schema.Message, 0, len(prior)+1)
	if thread != nil && thread.Summary != "" {
		out = append(out, &schema.Message{
			Role:    schema.System,
			Content: "之前对话的摘要：\n" + thread.Summary,
		})
	}
	for _, m := range prior {
		var blocks []ContentBlock
		_ = json.Unmarshal(m.ContentBlocks, &blocks)
		// DB 里存的是 media:// canonical 形态；LLM 没法 fetch 也没法对伪 URL 做推理，
		// 所以回放时重写成新鲜的 signed https。
		a.signHistoryBlocks(blocks)
		out = append(out, blocksToSchemaMessage(m.Role, blocks)...)
	}
	return pruneUnpairedToolMessages(out), nil
}

// pruneUnpairedToolMessages 是历史回放的最后一道防线：丢弃 tool_use/tool_result
// 配对不完整的一侧。孤儿配对的来源不止一个——assistant 落库失败但 tool_result
// 落了（Marshal 吞错的年代）、落库成功但工具结果没落（进程被杀 / abort）、压缩
// 切点拆散配对（切点校正之前的存量数据）——共同后果都是：回放出「tool 消息没有
// 前置 assistant tool_calls」或反向的形态，严格模式 backbone（doubao 等）直接
// 400，该 thread 之后每次 Run 都在第一步失败，永远无法自愈。源头各自修了，这里
// 兜住所有存量脏数据与未知的新来源，让 thread 从「永久变砖」变「自动愈合」。
func pruneUnpairedToolMessages(msgs []*schema.Message) []*schema.Message {
	resultIDs := make(map[string]bool)
	callIDs := make(map[string]bool)
	for _, m := range msgs {
		switch m.Role {
		case schema.Tool:
			if m.ToolCallID != "" {
				resultIDs[m.ToolCallID] = true
			}
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				callIDs[tc.ID] = true
			}
		}
	}
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case schema.Assistant:
			if len(m.ToolCalls) > 0 {
				kept := make([]schema.ToolCall, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					if resultIDs[tc.ID] {
						kept = append(kept, tc)
					}
				}
				m.ToolCalls = kept
				// 修剪后既没有文本也没有配对完整的 tool_call → 整条没有信息量，丢弃
				//（空 assistant 消息本身也会让部分上游报错）。
				if len(kept) == 0 && m.Content == "" {
					continue
				}
			}
		case schema.Tool:
			if !callIDs[m.ToolCallID] {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// signHistoryBlocks 把 text/content/url 字段里的 media:// 子串重写掉，让回放时的
// LLM 看到可用的 URL。跟入口侧的 canonicalize 配对：持久化存 media://，这里在读取时
// 再水合回去。
func (a *Agent) signHistoryBlocks(blocks []ContentBlock) {
	if a.Signer == nil {
		return
	}
	signFn := func(s string) string {
		return urlInTextRe.ReplaceAllStringFunc(s, a.Signer.Sign)
	}
	for i := range blocks {
		if blocks[i].Text != "" {
			blocks[i].Text = signFn(blocks[i].Text)
		}
		if blocks[i].Content != "" {
			blocks[i].Content = signFn(blocks[i].Content)
		}
		if blocks[i].URL != "" {
			blocks[i].URL = a.Signer.Sign(blocks[i].URL)
		}
		// 与 canonicalizeBlocks 的 Input 处理配对：DB 里 tool_use arguments 存的是
		// media:// canonical 形态，回放前重签成新鲜的 signed https——LLM 没法对
		// media:// 伪 URL 做 fetch 或推理。同样是对 RawMessage 做子串替换：签名 URL
		// 不含引号/反斜杠/空白，替换后 JSON 仍合法（前提同 canonicalizeBlocks）。
		if blocks[i].Type == "tool_use" && len(blocks[i].Input) > 0 {
			blocks[i].Input = json.RawMessage(signFn(string(blocks[i].Input)))
		}
	}
}

// toolInfosFromList 为给定的工具 slice 收集 ToolInfo 条目。Info() 报错的工具会被
// 静默跳过——一个瞬时 error 不该让整个 Run 挂掉。
func toolInfosFromList(ctx context.Context, tools []Tool) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		out = append(out, info)
	}
	return out
}

// assistantToBlocks 把 planner 返回的 schema.Message 渲染成我们持久化到
// agent_messages 上的标准 ContentBlock 列表。tools 用来给 tool_use 块附上
// 面向用户的显示名（快照进消息，后台改名不回溯历史）；传 nil 则只存内部名。
func assistantToBlocks(m *schema.Message, tools ToolRegistry) []ContentBlock {
	var out []ContentBlock
	if m.Content != "" {
		out = append(out, ContentBlock{Type: "text", Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		// 上游被 max_tokens 截断在 arguments 中间时（finish_reason=length，流仍
		// 正常 EOF），Arguments 是非法 JSON。json.RawMessage 参与 Marshal 会让整个
		// blocks 序列化报错——曾经该错误在持久化处被 _ 吞掉，整条 assistant 落库成
		// []，同轮 tool_result 照常落库，配对拆散、thread 永久变砖。改存成 JSON
		// 字符串形式的原文：行合法、信息不丢；重放时它只是 assistant 的历史输出，
		// 上游不校验 arguments 的 JSON 形态。空 arguments 同理会炸 Marshal，补 {}。
		raw := json.RawMessage(tc.Function.Arguments)
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		} else if !json.Valid(raw) {
			if quoted, err := json.Marshal(tc.Function.Arguments); err == nil {
				raw = quoted
			} else {
				raw = json.RawMessage(`{}`)
			}
		}
		displayName := ""
		if tools != nil {
			displayName = tools.DisplayName(tc.Function.Name)
		}
		out = append(out, ContentBlock{
			Type:        "tool_use",
			ID:          tc.ID,
			Name:        tc.Function.Name,
			DisplayName: displayName,
			Input:       raw,
		})
	}
	return out
}

// stopReasonOf 在 Phase 0 对齐 Anthropic 的 stop_reason：消息里有任何 tool call
// 就是 tool_use，否则是 end_turn。
func stopReasonOf(m *schema.Message) string {
	if len(m.ToolCalls) > 0 {
		return "tool_use"
	}
	return "end_turn"
}

// blocksToSchemaMessage 是 assistantToBlocks 的逆操作（外加 user 和 tool 角色）。
// 单条持久化的 tool 消息可能携带多个 tool_result block；我们把每个拆回它自己的
// schema.Message，让每个都能按 ID 跟正确的 tool call 对应上。
func blocksToSchemaMessage(role string, blocks []ContentBlock) []*schema.Message {
	switch role {
	case "user":
		var content string
		var refLines []string
		var imageURLs []string
		out := make([]*schema.Message, 0, 2)
		for _, b := range blocks {
			switch b.Type {
			case "skill":
				// skill 指令快照回放成 System 消息，排在它所属的 user 消息之前——
				// 这是 skill 跨轮生效的唯一机制（Claude Code 式：指令活在对话记录
				// 里）。老数据的 skill block 没有 Content（当年只存 ID+Name 供前端
				// 画 chip），跳过即可，行为与历史一致。
				if b.Content != "" {
					out = append(out, &schema.Message{Role: schema.System, Content: b.Content})
				}
			case "text":
				content += b.Text
			case "image":
				// 附件必须跨轮存活。曾经回放只认 skill/text，媒体 block 被静默丢弃：
				// 用户发视频 → agent 反问一句 → 用户答"是的"，planner 这轮的历史里
				// 就只剩文件名文本，URL 彻底不见（"金鱼记忆"）。URL 已由
				// signHistoryBlocks 从 media:// 重签成新鲜 https，直接可用。
				if b.URL != "" {
					imageURLs = append(imageURLs, b.URL)
				}
			case "video", "audio":
				// 与发送轮 buildUserMessage 同款处理：视频/音频不能挂 image part，
				// 回放成同一措辞的文本引用行，planner 需要时自己调 analyze_video。
				if b.URL != "" {
					name := b.Name
					if name == "" {
						name = "附件"
					}
					refLines = append(refLines, mediaRefLine(b.Type, name, b.URL))
				}
			case "file", "text_ref":
				// 大文本附件引用跨轮存活：回放成与发送轮同一措辞的引用行，
				// planner 任何一轮都能凭 artifact_id 继续 read_attachment。
				kind := "file"
				if b.Type == "text_ref" {
					kind = "text"
				}
				name := b.Name
				if name == "" {
					name = b.ID
				}
				refLines = append(refLines, attachmentRefLine(kind, name, b.ID, b.CharCount))
			}
		}
		if len(refLines) > 0 {
			content += "\n\n" + strings.Join(refLines, "\n")
		}
		if len(imageURLs) == 0 {
			return append(out, &schema.Message{Role: schema.User, Content: content})
		}
		parts := []schema.ChatMessagePart{
			{Type: schema.ChatMessagePartTypeText, Text: content},
		}
		for _, u := range imageURLs {
			parts = append(parts, schema.ChatMessagePart{
				Type:     schema.ChatMessagePartTypeImageURL,
				ImageURL: &schema.ChatMessageImageURL{URL: u},
			})
		}
		return append(out, &schema.Message{Role: schema.User, MultiContent: parts})
	case "assistant":
		msg := &schema.Message{Role: schema.Assistant}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				msg.Content += b.Text
			case "tool_use":
				// 跳过 ID 为空的畸形 tool_use block——有 bug 的 stream parser
				//（比如 gpt-5.5 + eino-ext）留下的残渣，否则会以 id="" 的形式被回放
				// 成 assistant 的 tool_call，在下一个要求 tool_call_id 非空配对的
				// 严格模式 backbone（doubao 等）上过不了校验。
				if strings.TrimSpace(b.ID) == "" {
					continue
				}
				msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
					ID:   b.ID,
					Type: "function",
					Function: schema.FunctionCall{
						Name:      b.Name,
						Arguments: string(b.Input),
					},
				})
			}
		}
		return []*schema.Message{msg}
	case "tool":
		out := make([]*schema.Message, 0, len(blocks))
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			// 跳过没有对应 tool_use_id 的孤儿 tool_result——跟上面一样的卫生处理；
			// 回放这些会让 doubao 这类严格 backbone 报 "messages.tool_call_id missing" 而崩溃。
			if strings.TrimSpace(b.ToolUseID) == "" {
				continue
			}
			out = append(out, &schema.Message{
				Role:       schema.Tool,
				ToolCallID: b.ToolUseID,
				Content:    b.Content,
			})
		}
		return out
	}
	return nil
}

// canonicalize 把一个出口（signed/HTTPS）URL 转回标准的 `media://key` DB 形态，
// 这样持久化的行不会随 signed URL 的 24h TTL 失效。Signer 未设或 URL 不是我们的时
// 是 no-op。
func (a *Agent) canonicalize(u string) string {
	if a.Signer == nil {
		return u
	}
	return a.Signer.Canonicalize(u)
}

// canonicalizeArtifacts 返回一份 URL 被重写成 canonical 形态的 artifact 副本。原始
// slice（仍指向 signed URL）已经发给 WS 帧给实时 UI 用了；这份副本才是被持久化的。
func (a *Agent) canonicalizeArtifacts(in []Artifact) []Artifact {
	if a.Signer == nil {
		return in
	}
	out := make([]Artifact, len(in))
	for i, art := range in {
		art.URL = a.Signer.Canonicalize(art.URL)
		out[i] = art
	}
	return out
}

// urlInTextRe 故意放宽——匹配 http(s) 和 media scheme，在第一个空白 / 引号 /
// 尖括号 / 右圆括号处结束。每个匹配都喂给 URLSigner.Canonicalize，由它自己决定该
// URL 是不是属于我们的 bucket（外部 URL 上是 no-op）。
var urlInTextRe = regexp.MustCompile(`(?:https?|media)://[^\s"'<>)]+`)

// canonicalizeText 把 s 里指向我们 bucket 的每个 URL 子串重写回 media://key 形态。
// 外部 URL 原样透传。
func (a *Agent) canonicalizeText(s string) string {
	if a.Signer == nil || s == "" {
		return s
	}
	return urlInTextRe.ReplaceAllStringFunc(s, a.Signer.Canonicalize)
}

// canonicalizeBlocks 遍历一个 ContentBlock slice，把 text/content 字符串里的 URL
// 做 canonicalize。在每个 assistant / tool_result 的持久化处都会用到，让 DB 里不留
// 会过期的 signed URL。
func (a *Agent) canonicalizeBlocks(blocks []ContentBlock) {
	if a.Signer == nil {
		return
	}
	for i := range blocks {
		if blocks[i].Text != "" {
			blocks[i].Text = a.canonicalizeText(blocks[i].Text)
		}
		if blocks[i].Content != "" {
			blocks[i].Content = a.canonicalizeText(blocks[i].Content)
		}
		// tool_use 的 Input（工具 arguments 的 RawMessage）同样会带 signed URL——
		// planner 把 mention 的签名 URL 当参数传给工具就是典型来源。不处理的话
		// arguments 落库留下 24h 会过期的签名 URL，违反本文件「持久化一律 media://
		// canonical」的不变量。这里直接对 RawMessage 的字符串形态做子串替换、不解析
		// JSON 结构，前提是：URL 只出现在 JSON 字符串值里（urlInTextRe 在引号处停），
		// 而替换产物 media://<key> 形态不含引号/反斜杠/空白，所以替换后仍是合法 JSON。
		if blocks[i].Type == "tool_use" && len(blocks[i].Input) > 0 {
			blocks[i].Input = json.RawMessage(a.canonicalizeText(string(blocks[i].Input)))
		}
	}
}

// appendOrMergeToolCall 把一个流式的 ToolCall chunk 合并进正在累积的列表。
// OpenAI/doubao 等会把每个 tool_call 拆成多个 SSE chunk 发：
//   - 某个调用的第一个 chunk：ID + name 已设，arguments 为空或只有一部分
//   - 后续 chunk：ID 为空、name 为空、只有 arguments 片段
//
// 每个 chunk 都带一个 Index，标明它属于消息里的哪个 tool_call。我们主要按 Index
// （标准的协议字段）合并，回退到按 ID，最后对那种两者都不给的罕见 provider 回退到
// 「拼到最后一个条目上」。没有这套逻辑的话，每个后续 chunk 看起来都像一个 name 为空
// 的全新 tool_call → execTool 对每个片段都报 "tool not registered"（生产里一个真实
// 调用膨胀成了几十个幻影失败）。
func appendOrMergeToolCall(curr []schema.ToolCall, in schema.ToolCall) []schema.ToolCall {
	if in.Index != nil {
		for i := range curr {
			if curr[i].Index != nil && *curr[i].Index == *in.Index {
				mergeToolCall(&curr[i], in)
				return curr
			}
		}
		return append(curr, in)
	}
	if in.ID != "" {
		for i := range curr {
			if curr[i].ID == in.ID {
				mergeToolCall(&curr[i], in)
				return curr
			}
		}
		return append(curr, in)
	}
	// 既没 Index 也没 ID 的后续 chunk——合并进最近的那个条目（stream chunk 是按序到达的）。
	if len(curr) > 0 {
		mergeToolCall(&curr[len(curr)-1], in)
		return curr
	}
	return append(curr, in)
}

func mergeToolCall(dst *schema.ToolCall, in schema.ToolCall) {
	if in.ID != "" {
		dst.ID = in.ID
	}
	if in.Type != "" {
		dst.Type = in.Type
	}
	if in.Function.Name != "" {
		dst.Function.Name = in.Function.Name
	}
	dst.Function.Arguments += in.Function.Arguments
}
