package cliapi

import "testing"

func TestStatusForKnownCodes(t *testing.T) {
	cases := map[string]int{
		CodeAuthRequired:          401,
		CodeTokenInvalid:          401,
		CodeTokenExpired:          401,
		CodeTokenRevoked:          401,
		CodeAudienceDenied:        401,
		CodeProjectNotFound:       404,
		CodeNodeNotFound:          404,
		CodeEdgeNotFound:          404,
		CodeModelNotFound:         404,
		CodeAssetNotFound:         404,
		CodeNodeReadonlyType:      400,
		CodeInvalidNodeType:       400,
		CodeInvalidModelParams:    400,
		CodeModelRequired:         400,
		CodeModelDisabled:         400,
		CodeModelUnavailable:      400,
		CodeInvalidArgument:       400,
		CodeNodeAlreadyRunning:    409,
		CodeCanvasVersionConflict: 409,
		CodeIdempotencyKeyReused:  409,
		CodeIdempotencyInProgress: 409,
		CodeModelRefAmbiguous:     409,
		CodeRateLimited:           429,
		CodeUpstreamUnavailable:   502,
		CodeTaskFailed:            502,
		CodeInternal:              500,
	}
	for code, want := range cases {
		if got := StatusFor(code); got != want {
			t.Errorf("StatusFor(%s) = %d, want %d", code, got, want)
		}
	}
}

func TestStatusForUnknownCodeIsInternal(t *testing.T) {
	if got := StatusFor("NOPE"); got != 500 {
		t.Fatalf("unknown code status = %d, want 500", got)
	}
}

func TestErrorCarriesDetails(t *testing.T) {
	err := New(CodeModelRefAmbiguous, "模型名称匹配到多个结果").
		WithDetails(map[string]any{"candidates": []string{"a", "b"}})
	if err.Code != CodeModelRefAmbiguous {
		t.Fatalf("code = %s", err.Code)
	}
	if err.Details["candidates"] == nil {
		t.Fatalf("details lost: %+v", err)
	}
	if err.Error() == "" {
		t.Fatal("Error() 必须非空")
	}
}

func TestErrfFormats(t *testing.T) {
	err := Errf(CodeInvalidArgument, "字段 %s 非法", "ratio")
	if err.Message != "字段 ratio 非法" {
		t.Fatalf("message = %q", err.Message)
	}
}

// retryable 决定 CLI 是否可以自动重试。判错会让「余额不足」被无限重试，
// 或让「上游抖动」在一次失败后就放弃。
func TestRetryableCodes(t *testing.T) {
	retryable := []string{CodeRateLimited, CodeUpstreamUnavailable, CodeIdempotencyInProgress, CodeInternal}
	for _, c := range retryable {
		if !New(c, "x").Retryable() {
			t.Errorf("%s 应可重试", c)
		}
	}
	notRetryable := []string{
		CodeAuthRequired, CodeTokenRevoked, CodeAudienceDenied, CodeModelRequired,
		CodeNodeAlreadyRunning, CodeIdempotencyKeyReused,
		CodeInvalidNodeType, CodeNodeNotFound, CodeTaskFailed,
	}
	for _, c := range notRetryable {
		if New(c, "x").Retryable() {
			t.Errorf("%s 不应可重试", c)
		}
	}
}
