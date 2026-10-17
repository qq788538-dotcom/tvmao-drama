package billing

import (
	"context"
	"testing"
)

func TestActorContextRoundTrip(t *testing.T) {
	if got := ActorFromContext(context.Background()); got != 0 {
		t.Fatalf("未设置时应为 0（个人路径），got %d", got)
	}
	if got := ActorFromContext(WithActor(context.Background(), 42)); got != 42 {
		t.Fatalf("got %d want 42", got)
	}
}
