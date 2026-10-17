// Package agent — skill_autotrigger.go：skill 的关键词自动触发。
//
// 为什么是硬匹配而不是让模型自己判断：漏触发的后果是退回现状（用户手动选
// skill），无损；而让 planner 读一份 skill 目录自行决定加载，误触发时它会自作
// 主张套上一段长指令，用户不知道行为为什么变了。硬匹配还能被运营验证——
// 「用户说 X 就一定走 Y」可复现、出问题能改词表。
package agent

import (
	"encoding/json"
	"strings"
)

// MatchSkillByKeywords 从候选里挑出该自动挂载的 skill，没有则返回 nil。
//
// 三条闸：
//  1. 文本去空白后为空不扫（纯图片 / 纯 @ 引用消息）。
//  2. 只认 skill_type == "force_tool"。workflow 类型走 runWorkflow 直接执行步骤
//     序列（可能包含扣积分的生成动作），meta 类型会被 toolsForSkill 强制锁成只剩
//     output_skill_definition——注意 meta 那道锁在 ForceTools 判断**之前**，
//     调用方「自动触发不套用 ForceTools」的做法拦不住它，只能在这里挡掉。
//  3. 关键词自身去空白后为空的跳过：strings.Contains(任意文本, "") 恒为 true，
//     后台的 tags 输入框很容易录进一个空串，那会让该技能命中所有消息。
//
// 多个候选命中时取 SortWeight 最大的；权重相同按 ID 升序，保证同一输入永远得到
// 同一结果。**不能依赖入参顺序**——唯一的候选来源 ListForUser 的 ORDER BY 是
// `category, sort_weight DESC, id ASC`，分类优先意味着组间并非全局权重降序。
//
// 返回的是候选的**副本**：调用方要对它做 ForceTools=nil，返回入参元素地址会就地
// 改掉调用方持有的候选集。
func MatchSkillByKeywords(userText string, candidates []Skill) *Skill {
	text := strings.ToLower(strings.TrimSpace(userText))
	if text == "" {
		return nil
	}
	var best *Skill
	for i := range candidates {
		s := &candidates[i]
		if s.SkillType != "force_tool" || len(s.TriggerKeywords) == 0 {
			continue
		}
		if !matchesAnyKeyword(text, s.TriggerKeywords) {
			continue
		}
		if best == nil || s.SortWeight > best.SortWeight ||
			(s.SortWeight == best.SortWeight && s.ID < best.ID) {
			best = s
		}
	}
	if best == nil {
		return nil
	}
	cp := *best
	return &cp
}

// matchesAnyKeyword 做子串匹配而不是分词：中文没有词边界，分词只会引入新的漏匹配。
func matchesAnyKeyword(lowerText string, keywords []string) bool {
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		if strings.Contains(lowerText, kw) {
			return true
		}
	}
	return false
}

// skillIDsFromBlocks 从一条消息的 content_blocks 里挑出 skill block 的 id。
//
// 解析失败一律返回 nil 而不是 error：这条链路只服务「要不要重复挂同一个技能」这个
// 判断，坏数据的正确降级是「当作没加载过」——最坏多挂一次指令，绝不能让用户发不出消息。
func skillIDsFromBlocks(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "skill" && b.ID != "" {
			out = append(out, b.ID)
		}
	}
	return out
}

// ResolveAutoSkill 是自动触发的总入口：给定用户这一轮的文本，决定要不要替他挂一个
// skill。返回 nil 表示不挂。调用方只在**用户没有手动选** skill 时才该调它——
// 显式意图永远优先。
//
// 每一处依赖失败都降级而不是报错：自动触发是锦上添花，绝不能让用户发不出消息。
//   - 技能目录读不到 → 当作没有可触发的技能。
//   - 「已加载」名单读不到 → 当作空，退化成"可能重复挂一次"，好过整个功能失效。
func ResolveAutoSkill(userID int64, threadID, userText string, skills SkillsRepo, repo Repository) *Skill {
	if skills == nil {
		return nil
	}
	candidates, err := skills.ListForUser(userID)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	if repo != nil {
		if loaded, lerr := repo.LoadedSkillIDs(threadID); lerr == nil && len(loaded) > 0 {
			candidates = excludeSkillIDs(candidates, loaded)
		}
	}
	hit := MatchSkillByKeywords(userText, candidates)
	if hit == nil {
		return nil
	}
	// 自动触发只注入 addon + ParamDefaults，不套用 ForceTools：误触发的代价必须
	// 停在"多了段用不上的指令"，不能是用户突然铺不了生成节点却看不出原因。
	hit.ForceTools = nil
	hit.AutoTriggered = true
	return hit
}

func excludeSkillIDs(candidates []Skill, exclude []string) []Skill {
	drop := make(map[string]struct{}, len(exclude))
	for _, id := range exclude {
		drop[id] = struct{}{}
	}
	out := make([]Skill, 0, len(candidates))
	for _, s := range candidates {
		if _, skip := drop[s.ID]; !skip {
			out = append(out, s)
		}
	}
	return out
}
