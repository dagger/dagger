package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
)

type ModTreeSuite struct{}

func TestModTree(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModTreeSuite{})
}

func (ModTreeSuite) TestMatch(ctx context.Context, t *testctx.T) {
	t.Skip("FIXME not implemented")
}
