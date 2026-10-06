package agent

import (
	"context"
	"testing"
)

func TestEmitNoopWhenContextMissing(t *testing.T) {
	emit := EmitFromContext(context.Background())
	emit(Artifact{ArtifactID: "x"}) // 不能 panic
}
