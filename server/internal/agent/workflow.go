// apps/api/internal/agent/workflow.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// WorkflowStep 是一个顺序 workflow skill（L3）里的一个节点。每个 step 用一个静态的
// input map 调用单个工具。Inputs 里的字符串值可以包含 `{{step.<step_name>.<var>}}`
// 或 `{{input.<key>}}` 占位符，会在工具运行前一刻被替换：
//
//   - {{step.<name>.url}} / {{step.<name>.artifact_id}} 取自前一个 step 发出的第一个
//     artifact
//   - {{step.<name>.result}} 取该工具的文本返回值
//   - {{input.<key>}} 取自调用方提供的 initialVars
//     （通常用 user prompt 文本打底）
type WorkflowStep struct {
	Name     string         `json:"name"`
	ToolName string         `json:"tool_name"`
	Inputs   map[string]any `json:"inputs"`
}

// ExecuteWorkflow 对着给定的 registry 顺序执行各 step。它在每个 step 前发一个
// step_start 帧（Block.Text = step.Name），之后发一个 step_end 帧，成功时
// stop_reason="ok"、失败时 "error"。工具的 emit 通过标准的
// content_block_start/tool_event 机制传播到 sink——工具推的每个 artifact 都变成
// 它自己的 tool_event 帧，所以 WS client 看到的实时进度跟单工具 ReAct 调用没区别。
//
// 失败时（工具缺失、工具报错）ExecuteWorkflow 在 step_end(error) 帧之后立即返回；
// 后续 step 不再跑。外围的 action_start/action_end 框架由调用方负责。
// 第一个返回值是成功执行完的 step 数——调用方拿它写「实际跑了几步」的合成消息；
// 只返回 error 的话，半途而废的运行在历史里会被写成全部跑完。
func ExecuteWorkflow(
	ctx context.Context,
	registry ToolRegistry,
	steps []WorkflowStep,
	initialVars map[string]any,
	threadID, actionID string,
	sink FrameSink,
) (int, error) {
	stepVars := map[string]map[string]any{}

	for i, step := range steps {
		if step.ToolName == "" {
			return i, fmt.Errorf("step %d (%s): tool_name empty", i, step.Name)
		}

		sink.Send(Frame{
			Type:     FrameStepStart,
			ThreadID: threadID,
			ActionID: actionID,
			Block:    &ContentBlock{Type: "text", Text: step.Name},
		})

		t, ok := registry.ByName(step.ToolName)
		if !ok {
			sink.Send(Frame{
				Type:       FrameStepEnd,
				ThreadID:   threadID,
				ActionID:   actionID,
				StopReason: "error",
			})
			return i, fmt.Errorf("step %s: tool %q not registered", step.Name, step.ToolName)
		}

		resolved := substituteVars(step.Inputs, stepVars, initialVars)
		argsJSON, err := json.Marshal(resolved)
		if err != nil {
			sink.Send(Frame{
				Type:       FrameStepEnd,
				ThreadID:   threadID,
				ActionID:   actionID,
				StopReason: "error",
			})
			return i, fmt.Errorf("step %s: marshal inputs: %w", step.Name, err)
		}

		var artifacts []Artifact
		emit := func(a Artifact) {
			artifacts = append(artifacts, a)
			sink.Send(Frame{
				Type:     FrameContentBlockStart,
				ThreadID: threadID,
				ActionID: actionID,
				Block: &ContentBlock{
					Type:      "tool_event",
					ID:        uuid.NewString(),
					Name:      step.ToolName,
					Artifacts: []Artifact{a},
				},
			})
		}
		toolCtx := WithEmit(ctx, emit)

		result, terr := t.InvokableRun(toolCtx, string(argsJSON))
		if terr != nil {
			sink.Send(Frame{
				Type:       FrameStepEnd,
				ThreadID:   threadID,
				ActionID:   actionID,
				StopReason: "error",
			})
			return i, fmt.Errorf("step %s: %w", step.Name, terr)
		}

		vars := map[string]any{"result": result}
		if len(artifacts) > 0 {
			vars["url"] = artifacts[0].URL
			vars["artifact_id"] = artifacts[0].ArtifactID
		}
		stepVars[step.Name] = vars

		sink.Send(Frame{
			Type:       FrameStepEnd,
			ThreadID:   threadID,
			ActionID:   actionID,
			StopReason: "ok",
		})
	}
	return len(steps), nil
}

// stepRefRe 匹配 `{{step.<step_name>.<var>}}` 占位符，允许大括号内有可选的空白。
var stepRefRe = regexp.MustCompile(`\{\{\s*step\.([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)\s*\}\}`)

// inputRefRe 匹配 `{{input.<key>}}` 占位符，允许大括号内有可选的空白。
var inputRefRe = regexp.MustCompile(`\{\{\s*input\.([a-zA-Z0-9_]+)\s*\}\}`)

// substituteVars 遍历 inputs map，替换字符串值里的占位符。非字符串值原样透传，
// 这样调用方仍能直接喂 number/boolean/object。缺失的引用会被替换成空字符串——工具
// 看到的是空值而不是原始占位符，符合设计里「不泄漏模板语法」的规则。
func substituteVars(inputs map[string]any, stepVars map[string]map[string]any, initial map[string]any) map[string]any {
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		if s, ok := v.(string); ok {
			out[k] = substituteString(s, stepVars, initial)
		} else {
			out[k] = v
		}
	}
	return out
}

// substituteString 对单个字符串同时应用 step 和 input 两种占位符替换。结果会被
// 去掉首尾空白，这样像 `"{{input.prompt}}"` 这样的模板能干净地解析成 input 值。
func substituteString(s string, stepVars map[string]map[string]any, initial map[string]any) string {
	s = stepRefRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := stepRefRe.FindStringSubmatch(m)
		if vals, ok := stepVars[parts[1]]; ok {
			if v, ok := vals[parts[2]]; ok {
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	})
	s = inputRefRe.ReplaceAllStringFunc(s, func(m string) string {
		parts := inputRefRe.FindStringSubmatch(m)
		if v, ok := initial[parts[1]]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	})
	return strings.TrimSpace(s)
}
