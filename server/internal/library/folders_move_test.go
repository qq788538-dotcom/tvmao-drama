package library

import (
	"errors"
	"sync"
	"testing"

	"github.com/james/huabu_studio/apps/api/internal/db/dbtest"
)

// 两个并发 Move 互相挂到对方下面：查环和 UPDATE 不在同一事务时，两边都能通过查环，
// 造出 A↔B 环，之后 DescendantIDs 的 BFS 死循环。Move 在 immediate 事务内执行后，
// 两次 Move 串行，后一个必然看到环并返回 ErrFolderCycle。
func TestFoldersMoveConcurrentNoCycle(t *testing.T) {
	conn := dbtest.Open(t)
	repo := NewSQLiteFoldersRepository(conn)
	for round := 0; round < 50; round++ {
		a, err := repo.Create(1, nil, "a")
		if err != nil {
			t.Fatal(err)
		}
		b, err := repo.Create(1, nil, "b")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, mv := range [][2]int64{{a, b}, {b, a}} {
			wg.Add(1)
			go func(i int, id, parent int64) {
				defer wg.Done()
				<-start
				errs[i] = repo.Move(1, id, &parent)
			}(i, mv[0], mv[1])
		}
		close(start)
		wg.Wait()
		cycles := 0
		for _, err := range errs {
			if errors.Is(err, ErrFolderCycle) {
				cycles++
			} else if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if cycles != 1 {
			t.Fatalf("round %d: 应恰好一个 Move 因成环被拒, errs=%v", round, errs)
		}
	}
}
