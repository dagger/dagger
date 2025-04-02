package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TODO: delete this or incorporate into rest of suite

type TODOSuite struct{}

func TestTODO(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TODOSuite{})
}

func (TODOSuite) TestFoo(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := c.Container().Sync(ctx)
	require.NoError(t, err)
}
