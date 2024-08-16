package testctx

import (
	"context"
	"testing"
)

func TestTestCtx(t *testing.T) {
	// NOTE: these tests are demos for testctx, but don't actually run them,
	// some of them are expected to really fail

	Run(context.Background(), t, TestCtxSuite{})
}

type TestCtxSuite struct{}

var testRepeatsCounter = 3

func (TestCtxSuite) TestRepeats(ctx context.Context, t *T) {
	t.Retry(10)

	testRepeatsCounter--
	if testRepeatsCounter == 0 {
		return
	}
	t.Fail()
}

var testRepeatsPanicCounter = 3

func (TestCtxSuite) TestRepeatsPanic(ctx context.Context, t *T) {
	t.Retry(10)

	testRepeatsPanicCounter--
	if testRepeatsPanicCounter == 0 {
		return
	}
	panic("panic")
}

func (TestCtxSuite) TestRepeatsMax(ctx context.Context, t *T) {
	t.Retry(10)
	t.Fail()
}

func (TestCtxSuite) TestRepeatsMaxPanic(ctx context.Context, t *T) {
	t.Retry(10)
	panic("panic")
}
