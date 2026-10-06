package agent

import "testing"

// mkSkill 造一条参与自动触发的最小 skill（默认 force_tool 类型）。
func mkSkill(id string, weight int, keywords ...string) Skill {
	return Skill{ID: id, Name: id, SkillType: "force_tool", SortWeight: weight, TriggerKeywords: keywords}
}

func TestMatchSkillByKeywords_命中关键词(t *testing.T) {
	skills := []Skill{mkSkill("writer", 0, "剧本", "编剧")}
	got := MatchSkillByKeywords("帮我写个短片剧本", skills)
	if got == nil || got.ID != "writer" {
		t.Fatalf("want writer, got %v", got)
	}
}

func TestMatchSkillByKeywords_没命中返回nil(t *testing.T) {
	skills := []Skill{mkSkill("writer", 0, "剧本")}
	if got := MatchSkillByKeywords("帮我生成一张猫的图片", skills); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestMatchSkillByKeywords_空文本不扫(t *testing.T) {
	skills := []Skill{mkSkill("writer", 0, "剧本")}
	// 纯图片/纯 @ 引用消息的 userText 为空（或只有空白），不该触发任何技能。
	if got := MatchSkillByKeywords("   \n\t ", skills); got != nil {
		t.Fatalf("want nil for blank text, got %v", got)
	}
}

func TestMatchSkillByKeywords_没配关键词的技能不参与(t *testing.T) {
	skills := []Skill{
		{ID: "no-kw", SkillType: "force_tool", SortWeight: 100},
		{ID: "no-kw-empty", SkillType: "force_tool", SortWeight: 99, TriggerKeywords: []string{}},
	}
	if got := MatchSkillByKeywords("剧本 no-kw", skills); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

// workflow 类型走 runWorkflow 直接执行步骤序列，可能包含扣积分的生成动作；
// meta 类型会被 toolsForSkill 强制锁成只剩 output_skill_definition 一个工具。
// 两者都绝不能被一句关键词自动挂上。
func TestMatchSkillByKeywords_只对force_tool生效(t *testing.T) {
	for _, typ := range []string{"workflow", "meta", "", "unknown"} {
		skills := []Skill{{ID: "x", SkillType: typ, TriggerKeywords: []string{"剧本"}}}
		if got := MatchSkillByKeywords("写个剧本", skills); got != nil {
			t.Fatalf("skill_type=%q 不该参与自动触发，got %v", typ, got)
		}
	}
}

func TestMatchSkillByKeywords_多命中取权重最高(t *testing.T) {
	// 入参顺序刻意与权重相反：ListForUser 的 ORDER BY 是 category 优先，
	// 组间并非全局 sort_weight 降序，所以本函数必须自己挑权重最大的。
	skills := []Skill{
		mkSkill("low", 1, "剧本"),
		mkSkill("high", 50, "剧本"),
		mkSkill("mid", 10, "剧本"),
	}
	got := MatchSkillByKeywords("写个剧本", skills)
	if got == nil || got.ID != "high" {
		t.Fatalf("want high, got %v", got)
	}
}

func TestMatchSkillByKeywords_同权重按id升序保证确定性(t *testing.T) {
	skills := []Skill{mkSkill("zebra", 5, "剧本"), mkSkill("alpha", 5, "剧本")}
	got := MatchSkillByKeywords("写个剧本", skills)
	if got == nil || got.ID != "alpha" {
		t.Fatalf("want alpha, got %v", got)
	}
}

func TestMatchSkillByKeywords_英文大小写不敏感(t *testing.T) {
	skills := []Skill{mkSkill("sd", 0, "StoryBoard")}
	got := MatchSkillByKeywords("做一个 storyboard 出来", skills)
	if got == nil || got.ID != "sd" {
		t.Fatalf("want sd, got %v", got)
	}
}

func TestMatchSkillByKeywords_关键词自身空白被忽略(t *testing.T) {
	// 后台 Select mode="tags" 很容易录进空串或纯空格；空关键词若参与匹配，
	// strings.Contains(任意文本, "") 恒为 true，会让该技能命中所有消息。
	skills := []Skill{{ID: "bad", SkillType: "force_tool", TriggerKeywords: []string{"", "  "}}}
	if got := MatchSkillByKeywords("随便说点什么", skills); got != nil {
		t.Fatalf("空关键词不该命中，got %v", got)
	}
}

func TestMatchSkillByKeywords_返回的是候选副本而非入参地址(t *testing.T) {
	// 调用方会对返回值做 ForceTools=nil（自动触发不套用工具裁剪）。
	// 若返回入参切片元素的地址，会就地改掉调用方持有的候选集。
	skills := []Skill{mkSkill("writer", 0, "剧本")}
	skills[0].ForceTools = []string{"create_text_node"}
	got := MatchSkillByKeywords("写个剧本", skills)
	if got == nil {
		t.Fatal("want match")
	}
	got.ForceTools = nil
	if skills[0].ForceTools == nil {
		t.Fatal("返回值不是副本：改动污染了入参候选集")
	}
}

func TestEncodeSkillJSON_关键词编码(t *testing.T) {
	// nil 落 `[]` 而不是 SQL NULL：与 force_tools 同待遇，读回来 decode 不用分支。
	_, _, _, kw := encodeSkillJSON(Skill{})
	if string(kw) != `[]` {
		t.Fatalf("nil keywords want [], got %s", kw)
	}
	_, _, _, kw = encodeSkillJSON(Skill{TriggerKeywords: []string{"剧本", "编剧"}})
	if string(kw) != `["剧本","编剧"]` {
		t.Fatalf(`want ["剧本","编剧"], got %s`, kw)
	}
}

func TestSkillIDsFromBlocks(t *testing.T) {
	cases := []struct {
		name   string
		blocks string
		want   []string
	}{
		{"单个skill块", `[{"type":"skill","id":"writer","name":"编剧"},{"type":"text","text":"hi"}]`, []string{"writer"}},
		{"多个skill块", `[{"type":"skill","id":"writer"},{"type":"skill","id":"director"}]`, []string{"writer", "director"}},
		{"没有skill块", `[{"type":"text","text":"hi"},{"type":"image","url":"u"}]`, nil},
		{"空数组", `[]`, nil},
		{"空字节", ``, nil},
		// 坏数据不该让整条自动触发链路报错：解析不了就当没加载过，
		// 最坏结果是重复挂一次 skill，而不是用户发不出消息。
		{"非法JSON", `{oops`, nil},
		{"skill块缺id", `[{"type":"skill","name":"编剧"}]`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := skillIDsFromBlocks([]byte(c.blocks))
			if len(got) != len(c.want) {
				t.Fatalf("want %v, got %v", c.want, got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("want %v, got %v", c.want, got)
				}
			}
		})
	}
}
