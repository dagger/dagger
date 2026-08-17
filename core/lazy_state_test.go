package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// A value built in process is consumed before dagql has attached its Result, so
// PeekOrEval has to answer from what the accessor already holds. The context
// carries no engine cache here, so evaluating at all would fail: that is what
// makes this assert the value came back without evaluation.
func TestLazyAccessorPeekOrEvalSkipsEvaluation(t *testing.T) {
	var accessor LazyAccessor[string, *Directory]
	accessor.SetValue("/workspace")

	value, err := accessor.PeekOrEval(context.Background(), dagql.Result[*Directory]{})
	require.NoError(t, err)
	require.Equal(t, "/workspace", value)
}

func TestLazyAccessorPeekOrEvalFallsBackWhenUnset(t *testing.T) {
	var accessor LazyAccessor[string, *Directory]

	_, err := accessor.PeekOrEval(context.Background(), dagql.Result[*Directory]{})
	require.Error(t, err, "an unset accessor must still go through evaluation")
}
