package generation

import (
	"fmt"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// output_kind 列(0103)的真实 DB 验证:List 的 Kind 过滤与回显。
// 桌面端没有存量数据，不做回填；这里只钉运行时行为。

func TestListFilterByOutputKind(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteRepository(conn)

	uid := int64(1)

	seed := func(kind any, model string) int64 {
		r, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, output_kind, input, frozen_cents) VALUES (?, ?, 'succeeded', ?, '{}', 0)`, uid, model, kind)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		return id
	}
	imgID := seed("image", "m-img")
	vidID := seed("video", "m-vid")
	seed(nil, "m-legacy") // 回填后仍无信号的老任务:不出现在任何 kind 过滤下

	for _, tc := range []struct {
		kind   string
		wantID int64
		wantN  int
	}{
		{"image", imgID, 1},
		{"video", vidID, 1},
		{"", 0, 3}, // 不过滤时全量可见(含 NULL 行)
	} {
		got, total, err := repo.List(ListFilter{UserID: uid, Kind: tc.kind})
		if err != nil {
			t.Fatalf("List kind=%q: %v", tc.kind, err)
		}
		if int(total) != tc.wantN || len(got) != tc.wantN {
			t.Fatalf("List kind=%q: total=%d len=%d want %d", tc.kind, total, len(got), tc.wantN)
		}
		if tc.wantID != 0 && got[0].ID != tc.wantID {
			t.Fatalf("List kind=%q: got task %d want %d", tc.kind, got[0].ID, tc.wantID)
		}
	}

	// 回显:过滤命中的行 OutputKind 要带回来,NULL 行是空串。
	rows, _, err := repo.List(ListFilter{UserID: uid})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[int64]string{}
	for _, r := range rows {
		kinds[r.ID] = r.OutputKind
	}
	if kinds[imgID] != "image" || kinds[vidID] != "video" {
		t.Fatalf("OutputKind 回显不对: %v", kinds)
	}
}

// 成功收口写列:finishSuccessTx 的 UPDATE 带 output_kind=NULLIF(?,”)。
func TestFinishSuccessWritesOutputKind(t *testing.T) {
	conn := dbtest.Open(t)

	for _, tc := range []struct {
		kind string
		want any // string 或 nil(期望 NULL)
	}{
		{"video", "video"},
		{"", nil}, // 纯文本/未知 mime → NULL
	} {
		uid := int64(1)
		tres, err := conn.Exec(`INSERT INTO generation_tasks (user_id, model_id, status, input, frozen_cents, lease_version) VALUES (?, 'm-fin', 'processing', '{}', 0, 7)`, uid)
		if err != nil {
			t.Fatal(err)
		}
		tid, _ := tres.LastInsertId()

		// 直接执行与 finishSuccessTx 相同形态的 UPDATE(完整 finishTx 需要 billing
		// 依赖,这里钉的是 SQL 对 output_kind 的写入语义本身)。
		if _, err := conn.Exec(
			`UPDATE generation_tasks SET status='succeeded', output=?, output_kind=NULLIF(?,''), cost_cents=?, audit_flag=?, error=NULL
			 WHERE id=? AND lease_version=? AND status IN ('running','processing','settling')`,
			`{}`, tc.kind, 0, "t", tid, 7,
		); err != nil {
			t.Fatal(err)
		}

		var got any
		if err := conn.QueryRow(`SELECT output_kind FROM generation_tasks WHERE id=?`, tid).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if tc.want == nil {
			if got != nil {
				t.Fatalf("kind=%q: 期望 NULL,得到 %v", tc.kind, got)
			}
		} else if fmt.Sprintf("%s", got) != tc.want {
			t.Fatalf("kind=%q: 得到 %v", tc.kind, got)
		}
	}
}
