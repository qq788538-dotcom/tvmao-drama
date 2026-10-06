package directormodels

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

func TestInsertListGetDeleteCount(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	id1, err := repo.Insert(Model{UserID: 1, Name: "robot", FileName: "robot.fbx", ObjectKey: "u1/2026/10/a_robot.fbx", SizeBytes: 10, MimeType: "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := repo.Insert(Model{UserID: 1, Name: "chair", FileName: "chair.glb", ObjectKey: "u1/2026/10/b_chair.glb", SizeBytes: 20, MimeType: "model/gltf-binary"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListByUser(1)
	if err != nil {
		t.Fatal(err)
	}
	// 同一秒内插入：created_at 相同，按 id DESC 兜底，新的在前。
	if len(list) != 2 || list[0].ID != id2 || list[1].ID != id1 || list[0].CreatedAt.IsZero() {
		t.Fatalf("list = %+v", list)
	}
	m, err := repo.GetByID(id1)
	if err != nil || m.Name != "robot" || m.SizeBytes != 10 {
		t.Fatalf("get = %+v %v", m, err)
	}
	if _, err := repo.GetByID(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// 别人的 user_id 删不掉
	if err := repo.Delete(conn, 2, id1); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountByUser(1); n != 2 {
		t.Fatalf("越权删除不应生效，count=%d", n)
	}
	if err := repo.Delete(conn, 1, id1); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.CountByUser(1); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

func insertProjectWithPayload(t *testing.T, conn *sql.DB, userID int64, payload string, deleted bool) {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO projects (user_id, name) VALUES (?, 'p')`, userID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO workflow_snapshots (project_id, payload) VALUES (?, ?)`, id, payload); err != nil {
		t.Fatal(err)
	}
	if deleted {
		if _, err := conn.Exec(`UPDATE projects SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
}

// key 由文件名派生、几乎必带下划线；SQLite 的 LIKE 没有默认转义，漏写 ESCAPE 会误报引用。
func TestCountProjectsReferencingEscapesAndScopes(t *testing.T) {
	conn := dbtest.Open(t)
	if _, err := conn.Exec(`INSERT INTO users (id, email) VALUES (2, 'other@local')`); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLiteRepository(conn)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12Xchair.fbx"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, true)
	insertProjectWithPayload(t, conn, 2, `{"nodes":[{"content":"media://u1/ab12_chair.fbx"}]}`, false)
	for key, want := range map[string]int64{"u1/ab12_chair.fbx": 1, "u1/ab12Xchair.fbx": 1, "u1/nothing.fbx": 0} {
		n, err := repo.CountProjectsReferencing(conn, 1, key)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s: n=%d want %d", key, n, want)
		}
	}
}

// 引用检查必须精确匹配 media://<key>：区分大小写、key 后面必须紧跟 JSON 字符串
// 结束引号（或嵌套 JSON 里的转义引号），不能把 LIB/A.FBX、a.fbx2 算成 a.fbx 的引用。
func TestCountProjectsReferencingExactMatch(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)
	insertProjectWithPayload(t, conn, 1, `{"n":[{"content":"media://LIB/A.FBX"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"n":[{"content":"media://lib/a.fbx2"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"n":[{"content":"media://lib/a.fbx.bak"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"n":[{"content":"media://lib/b.fbx"}]}`, false)
	insertProjectWithPayload(t, conn, 1, `{"n":[{"raw":"{\"u\":\"media://lib/b.fbx\"}"}]}`, false)
	for key, want := range map[string]int64{
		"lib/a.fbx":  0,
		"LIB/A.FBX":  1,
		"lib/a.fbx2": 1,
		"lib/b.fbx":  2, // 普通字符串值 + 嵌套 JSON 字符串里的转义引号
		"lib/b":      0,
	} {
		n, err := repo.CountProjectsReferencing(conn, 1, key)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s: n=%d want %d", key, n, want)
		}
	}
}
