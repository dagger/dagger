package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

// These tests pin the recipeIDMemo behavior added to recipeIDWithVisiting: the
// reverse direction of recipeCallMemo. Rebuilding a *call.ID from a ResultCall
// frame must keep shared nodes shared, else the DAG unrolls into a tree — the
// second, symmetric blowup observed after engaging an LLM (~2.6e8 objects). These
// exercise the inline (ref.Call) sharing path, which needs no cache.

func inlineRefArg(name string, frame *ResultCall) *ResultCallArg {
	return &ResultCallArg{
		Name: name,
		Value: &ResultCallLiteral{
			Kind:      ResultCallLiteralKindResultRef,
			ResultRef: &ResultCallRef{Call: frame},
		},
	}
}

func argIDsByName(t *testing.T, id *call.ID) map[string]*call.ID {
	t.Helper()
	out := map[string]*call.ID{}
	for _, a := range id.Args() {
		lit, ok := a.Value().(*call.LiteralID)
		require.True(t, ok, "arg %q is not a LiteralID", a.Name())
		out[a.Name()] = lit.Value()
	}
	return out
}

func TestRecipeIDMemoDedupesSharedInlineFrame(t *testing.T) {
	t.Parallel()

	base := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "base"}
	top := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(memoObjType),
		Field: "combine",
		Args:  []*ResultCallArg{inlineRefArg("a", base), inlineRefArg("b", base)},
	}

	id, err := top.recipeID(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, id)

	byName := argIDsByName(t, id)
	require.Same(t, byName["a"], byName["b"],
		"a shared inline frame must rebuild to a single shared *call.ID")
}

func TestRecipeIDMemoKeepsDistinctFramesDistinct(t *testing.T) {
	t.Parallel()

	baseA := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "baseA"}
	baseB := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "baseB"}
	top := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(memoObjType),
		Field: "combine",
		Args:  []*ResultCallArg{inlineRefArg("a", baseA), inlineRefArg("b", baseB)},
	}

	id, err := top.recipeID(context.Background(), nil)
	require.NoError(t, err)

	byName := argIDsByName(t, id)
	require.NotSame(t, byName["a"], byName["b"], "distinct frames must not be deduplicated")
}
