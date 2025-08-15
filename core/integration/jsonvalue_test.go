package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type JSONSuite struct{}

func TestJSON(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(JSONSuite{})
}

func (JSONSuite) TestInteger(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Test creating a JSON integer and retrieving its value
	jsonInt := c.JSON().NewInteger(42)

	// Test AsInteger method
	value, err := jsonInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 42, value)

	// Test with negative integer
	jsonNegInt := c.JSON().NewInteger(-123)
	negValue, err := jsonNegInt.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, -123, negValue)

	// Test with zero
	jsonZero := c.JSON().NewInteger(0)
	zeroValue, err := jsonZero.AsInteger(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, zeroValue)
}
