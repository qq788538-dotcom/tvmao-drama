package billing

import (
	"context"
	"testing"
)

func TestDramaScopeRoundTrip(t *testing.T) {
	ctx := WithDramaScope(context.Background(), 11, 3)
	p, s := dramaScopeFromCtx(ctx)
	if p != 11 || s != 3 {
		t.Fatalf("round-trip = (%d,%d), want (11,3)", p, s)
	}
}

func TestDramaScopeAbsent(t *testing.T) {
	p, s := dramaScopeFromCtx(context.Background())
	if p != 0 || s != 0 {
		t.Fatalf("absent scope = (%d,%d), want (0,0)", p, s)
	}
}

func TestNullableID(t *testing.T) {
	if nullableID(0) != nil {
		t.Fatalf("nullableID(0) should be nil")
	}
	if nullableID(7) != int64(7) {
		t.Fatalf("nullableID(7) = %v, want int64(7)", nullableID(7))
	}
}
