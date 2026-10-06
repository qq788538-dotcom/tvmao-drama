package db_test

import (
	"regexp"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestAgentTablesExist(t *testing.T) {
	conn := dbtest.Open(t)
	for _, name := range []string{"agent_backbone_models", "agent_threads", "agent_messages", "agent_tools", "skills", "skill_categories"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("缺表 %s", name)
		}
	}
}

// 删除项目要级联删掉其下的对话线程和消息，否则留下孤儿行。
func TestAgentMessagesCascadeWithProject(t *testing.T) {
	conn := dbtest.Open(t)
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (1, 'p')`)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO agent_threads (id, project_id, user_id) VALUES ('t1', ?, 1)`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO agent_messages (id, thread_id, action_id, role, content_blocks) VALUES ('m1','t1','a','user','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM projects WHERE id=?`, pid); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM agent_messages`).Scan(&n)
	if n != 0 {
		t.Fatalf("消息未级联删除，剩 %d", n)
	}
}

// keyset 分页依赖微秒精度；默认值兜底也必须是 26 字符的定宽文本。
func TestAgentMessagesCreatedAtDefaultHasMicroseconds(t *testing.T) {
	conn := dbtest.Open(t)
	_, _ = conn.Exec(`INSERT INTO projects (id, user_id, name) VALUES (9, 1, 'p')`)
	_, _ = conn.Exec(`INSERT INTO agent_threads (id, project_id, user_id) VALUES ('t1', 9, 1)`)
	if _, err := conn.Exec(`INSERT INTO agent_messages (id, thread_id, action_id, role, content_blocks) VALUES ('m1','t1','a','user','[]')`); err != nil {
		t.Fatal(err)
	}
	var s string
	if err := conn.QueryRow(`SELECT CAST(created_at AS TEXT) FROM agent_messages WHERE id='m1'`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{6}$`).MatchString(s) {
		t.Fatalf("created_at 默认值格式不对: %q", s)
	}
}

// JSON 列被存成 BLOB 后 json_each 会按 JSONB 解析出错——约束直接拒绝。
func TestAgentJSONColumnsRejectBlob(t *testing.T) {
	conn := dbtest.Open(t)
	_, err := conn.Exec(`INSERT INTO skills (id, name, category, skill_type, system_prompt_addon, force_tools, param_defaults, workflow_steps)
		VALUES ('s1','n','c','force_tool','', ?, '{}', '[]')`, []byte(`[]`))
	if err == nil {
		t.Fatal("force_tools 以 BLOB 写入应被 CHECK 拒绝")
	}
}

func TestAgentUpdatedAtTriggers(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO skill_categories (name, updated_at) VALUES ('c', '2000-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE skill_categories SET color='red' WHERE name='c'`); err != nil {
		t.Fatal(err)
	}
	var s string
	_ = conn.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM skill_categories WHERE name='c'`).Scan(&s)
	if s == "2000-01-01 00:00:00" {
		t.Fatal("updated_at 触发器未生效")
	}
}

// P2a builtin.Apply 升级内置数据时会删除模型行、或把模型行挪到新主键
// （先挪到负数临时 id 再写回）。被 Agent 工具引用的模型行不能因此触发外键失败，
// 否则 sidecar 启动直接退出。
func TestAgentToolsFollowModelConfigDeleteAndRekey(t *testing.T) {
	conn := dbtest.Open(t)
	insertModel := func(id int64, slug string) {
		t.Helper()
		if _, err := conn.Exec(`INSERT INTO model_configs (id, type, name, model_id, base_path, request_template)
			VALUES (?, 'image', 'm', ?, '/x', '{}')`, id, slug); err != nil {
			t.Fatal(err)
		}
	}
	insertTool := func(name string, modelID int64) {
		t.Helper()
		if _, err := conn.Exec(`INSERT INTO agent_tools (tool_name, category, source_model_id, tool_description, params_schema, default_params)
			VALUES (?, 'image', ?, 'd', '{}', '{}')`, name, modelID); err != nil {
			t.Fatal(err)
		}
	}
	insertModel(1, "m-1")
	insertModel(2, "m-2")
	insertTool("t1", 1)
	insertTool("t2", 2)

	// 改主键：和 builtin.moveModelID 一样经过负数临时 id。
	if _, err := conn.Exec(`UPDATE model_configs SET id = -7 WHERE id = 1`); err != nil {
		t.Fatalf("改主键到临时 id 失败: %v", err)
	}
	if _, err := conn.Exec(`UPDATE model_configs SET id = 7 WHERE id = -7`); err != nil {
		t.Fatalf("改主键失败: %v", err)
	}
	var ref int64
	if err := conn.QueryRow(`SELECT source_model_id FROM agent_tools WHERE tool_name='t1'`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if ref != 7 {
		t.Fatalf("工具引用应跟随新主键，得到 %d", ref)
	}

	// 删除：和 builtin.deleteModelRow 一样按 model_id 删。
	if _, err := conn.Exec(`DELETE FROM model_configs WHERE model_id = 'm-2'`); err != nil {
		t.Fatalf("删除被工具引用的模型失败: %v", err)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM agent_tools WHERE tool_name='t2'`).Scan(&n)
	if n != 0 {
		t.Fatalf("模型删除后工具应级联删除，剩 %d", n)
	}
	var fkErrs int
	rows, err := conn.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		fkErrs++
	}
	rows.Close()
	if fkErrs != 0 {
		t.Fatalf("foreign_key_check 报 %d 处违规", fkErrs)
	}
}
