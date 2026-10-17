package agentseed

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 种子工具引用的生产模型 id（agent_tools.source_model_id）。
var toolModelIDs = []int64{1, 9, 19, 46}

func insertModels(t *testing.T, conn *sql.DB, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if _, err := conn.Exec(`INSERT INTO model_configs (id, type, name, model_id, base_path, request_template)
			VALUES (?, 'image', 'm', ?, '/x', '{}')`, id, "seed-model-"+strconv.FormatInt(id, 10)); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, conn *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportLoadsAllTables(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	res, err := Import(conn)
	if err != nil {
		t.Fatal(err)
	}
	if res.Categories != 4 || res.Skills != 10 || res.Tools != 4 || res.Backbones != 4 || res.SkippedTools != 0 {
		t.Fatalf("导入计数不对: %+v", res)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM skills WHERE typeof(force_tools)='text' AND typeof(workflow_steps)='text'`); n != 10 {
		t.Fatalf("skills JSON 列应为 text，命中 %d", n)
	}
	// trigger_keywords 在种子里有 null，要落成 NULL 而不是字符串 "null"
	if n := count(t, conn, `SELECT COUNT(*) FROM skills WHERE trigger_keywords = 'null'`); n != 0 {
		t.Fatalf("trigger_keywords 不应存字符串 null")
	}
}

// 第二次导入：计数不变，use_count 保留。
func TestImportIsIdempotentAndKeepsUseCount(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE skills SET use_count=7 WHERE id='director_master'`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM skills`); n != 10 {
		t.Fatalf("重复导入后 skills=%d", n)
	}
	if n := count(t, conn, `SELECT use_count FROM skills WHERE id='director_master'`); n != 7 {
		t.Fatalf("use_count 被覆盖: %d", n)
	}
}

// Agent 模型写死为内置值：旧版设置页切换过的 model_name（model_name_overridden=1）
// 在导入时恢复为种子值并清除标记，其余参数同样回到内置值。
func TestImportResetsOverriddenModelName(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	var seedName string
	_ = conn.QueryRow(`SELECT model_name FROM agent_backbone_models WHERE id=1`).Scan(&seedName)
	if _, err := conn.Exec(`UPDATE agent_backbone_models SET model_name='user-pick', model_name_overridden=1, max_tokens=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	var name string
	var maxTokens, overridden int
	_ = conn.QueryRow(`SELECT model_name, max_tokens, model_name_overridden FROM agent_backbone_models WHERE id=1`).Scan(&name, &maxTokens, &overridden)
	if name != seedName || overridden != 0 {
		t.Fatalf("用户切换过的模型应恢复为种子值 %q：got %q overridden=%d", seedName, name, overridden)
	}
	if maxTokens != 0 {
		t.Fatalf("其余参数应恢复内置值，max_tokens=%d", maxTokens)
	}
}

func TestImportSkipsToolsWithMissingModel(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, 9, 19) // 缺 1 和 46
	res, err := Import(conn)
	if err != nil {
		t.Fatalf("缺模型不应让导入失败: %v", err)
	}
	if res.Tools != 2 || res.SkippedTools != 2 {
		t.Fatalf("应导入 2 个、跳过 2 个: %+v", res)
	}
	insertModels(t, conn, 1, 46)
	res, _ = Import(conn)
	if res.Tools != 4 || res.SkippedTools != 0 {
		t.Fatalf("补齐模型后应全部导入: %+v", res)
	}
}

// 种子不在种子里的旧行要删掉（版本升级下架某个 Skill）。
func TestImportRemovesRowsNotInSeed(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	if _, err := conn.Exec(`INSERT INTO skills (id, name, category, skill_type, system_prompt_addon, force_tools, param_defaults, workflow_steps)
		VALUES ('retired','r','x','force_tool','','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM skills WHERE id='retired'`); n != 0 {
		t.Fatal("下架的内置 Skill 未删除")
	}
}

// 内嵌副本必须与仓库根 seed/ 一致：重新导出生产数据只改一处会被这里拦下。
func TestEmbeddedSeedMatchesRepoSeed(t *testing.T) {
	for _, name := range []string{"skills", "skill_categories", "agent_tools", "agent_backbone_models"} {
		root, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "seed", name+".jsonl"))
		if err != nil {
			t.Fatalf("读仓库根种子 %s: %v", name, err)
		}
		embedded, _ := dataFS.ReadFile("data/" + name + ".jsonl")
		if !bytes.Equal(root, embedded) {
			t.Fatalf("%s.jsonl 两处不一致：cp seed/%s.jsonl server/internal/agent/agentseed/data/", name, name)
		}
	}
}

// 新种子把两个现有分类的 name 互换时，逐行 upsert 不能撞上 name UNIQUE。
func TestImportHandlesSwappedCategoryNames(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	var n2, n3 string
	_ = conn.QueryRow(`SELECT name FROM skill_categories WHERE id=2`).Scan(&n2)
	_ = conn.QueryRow(`SELECT name FROM skill_categories WHERE id=3`).Scan(&n3)
	// 模拟旧版本库：id 2 / 3 的名字与当前种子互换。
	for _, q := range []string{
		`UPDATE skill_categories SET name='tmp' WHERE id=2`,
		`UPDATE skill_categories SET name='` + n2 + `' WHERE id=3`,
		`UPDATE skill_categories SET name='` + n3 + `' WHERE id=2`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Import(conn); err != nil {
		t.Fatalf("分类名互换后导入失败: %v", err)
	}
	var got string
	_ = conn.QueryRow(`SELECT name FROM skill_categories WHERE id=2`).Scan(&got)
	if got != n2 {
		t.Fatalf("id 2 应恢复为 %q，得到 %q", n2, got)
	}
}

// 旧库里 model_name 仍是合并前的 canvas-agent-gpt-5.5：升级导入后变成 gpt-5.5；
// 被用户在设置页覆盖过的行（model_name_overridden=1）同样回到种子值（Agent 模型已写死）。
// 已安装用户库里的旧名（canvas-agent-gpt-5.5、gpt-5.5）在下次启动导入时都升级为种子值 gpt-latest。
func TestImportUpgradesOldGPTModelNames(t *testing.T) {
	conn := dbtest.Open(t)
	insertModels(t, conn, toolModelIDs...)
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE agent_backbone_models SET model_name='gpt-5.5' WHERE id IN (1,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE agent_backbone_models SET model_name_overridden=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(conn); err != nil {
		t.Fatal(err)
	}
	var m1, m2 string
	if err := conn.QueryRow(`SELECT model_name FROM agent_backbone_models WHERE id=1`).Scan(&m1); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT model_name FROM agent_backbone_models WHERE id=2`).Scan(&m2); err != nil {
		t.Fatal(err)
	}
	if m1 != "gpt-latest" {
		t.Fatalf("未覆盖的旧行应升级为 gpt-latest，got %q", m1)
	}
	if m2 != "gpt-latest" {
		t.Fatalf("用户覆盖过的行也应回到种子值，got %q", m2)
	}
}
