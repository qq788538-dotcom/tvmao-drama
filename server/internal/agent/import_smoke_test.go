package agent

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestDependenciesImportable(t *testing.T) {
	_ = schema.Assistant // message role 常量存在；如果真实名字不一样，这个测试要改
	_ = websocket.StatusNormalClosure
	if uuid.NewString() == "" {
		t.Fatal("uuid.NewString returned empty")
	}
}
