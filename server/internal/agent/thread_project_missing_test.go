package agent

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// 给不存在的项目建线程：返回 ErrProjectNotFound（handler 映射 404），而不是外键报错（500）。
func TestCreateThreadMissingProject(t *testing.T) {
	conn := openTestDB(t)
	defer conn.Close()
	userID, _ := makeTestUserAndProject(t, conn)
	repo := NewRepository(conn)
	err := repo.CreateThread(Thread{ID: uuid.NewString(), ProjectID: 987654, UserID: userID, Title: "x"})
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}
