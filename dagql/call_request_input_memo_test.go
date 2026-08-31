package dagql

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// These tests pin the recipeCallMemo behavior added to resultCallRefFromIDInput:
// a recipe (inline) ID DAG with shared sub-nodes must expand into a shared DAG,
// not an unrolled tree. Before memoization, resuming a long agent/LLM session —
// whose accumulated state references the same base object via a long receiver
// chain and many args — expanded to ~10^8 ResultCall frames and tens of GB.

var (
	memoStrType = &ast.Type{NamedType: "String", NonNull: true}
	memoObjType = &ast.Type{NamedType: "Obj", NonNull: true}
)

func refArg(t *testing.T, frame *ResultCall, name string) *ResultCall {
	t.Helper()
	for _, arg := range frame.Args {
		if arg.Name != name {
			continue
		}
		require.NotNil(t, arg.Value, "arg %q has nil value", name)
		require.Equal(t, ResultCallLiteralKindResultRef, arg.Value.Kind, "arg %q", name)
		require.NotNil(t, arg.Value.ResultRef, "arg %q has nil ref", name)
		return arg.Value.ResultRef.Call
	}
	t.Fatalf("arg %q not found", name)
	return nil
}

// A base object referenced by two distinct args of the same call must expand to
// the exact same *ResultCall pointer — one expansion shared, not duplicated.
func TestRecipeMemoDedupesSharedNode(t *testing.T) {
	t.Parallel()

	base := call.New().Append(memoStrType, "base")
	top := call.New().Append(memoObjType, "combine",
		call.WithArgs(
			call.NewArgument("a", call.NewLiteralID(base), false),
			call.NewArgument("b", call.NewLiteralID(base), false),
		),
	)

	ref, err := resultCallRefFromIDInput(context.Background(), top, recipeCallMemo{})
	require.NoError(t, err)
	require.NotNil(t, ref)
	require.NotNil(t, ref.Call)

	a := refArg(t, ref.Call, "a")
	b := refArg(t, ref.Call, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.Equal(t, "base", a.Field)
	// The whole point: both references resolve to one shared frame.
	require.Same(t, a, b, "shared base must expand to a single shared *ResultCall")
}

// Distinct bases must NOT be collapsed together — the memo keys on recipe digest,
// so different recipes stay different frames.
func TestRecipeMemoKeepsDistinctNodesDistinct(t *testing.T) {
	t.Parallel()

	baseA := call.New().Append(memoStrType, "baseA")
	baseB := call.New().Append(memoStrType, "baseB")
	top := call.New().Append(memoObjType, "combine",
		call.WithArgs(
			call.NewArgument("a", call.NewLiteralID(baseA), false),
			call.NewArgument("b", call.NewLiteralID(baseB), false),
		),
	)

	ref, err := resultCallRefFromIDInput(context.Background(), top, recipeCallMemo{})
	require.NoError(t, err)

	a := refArg(t, ref.Call, "a")
	b := refArg(t, ref.Call, "b")
	require.NotSame(t, a, b, "distinct recipes must not be deduplicated")
	require.Equal(t, "baseA", a.Field)
	require.Equal(t, "baseB", b.Field)
}

// The blowup scenario: a deep receiver chain where every level references the same
// base. Without memoization the base is re-expanded once per level (and shared
// sub-DAGs unroll super-linearly). With it, the base is a single shared frame
// across all levels, so the retained frame is a DAG whose size is linear in the
// number of distinct nodes.
func TestRecipeMemoSharesAcrossReceiverChain(t *testing.T) {
	t.Parallel()

	const depth = 300
	base := call.New().Append(memoStrType, "base")

	var id *call.ID // call.New()
	for i := range depth {
		id = id.Append(memoObjType, fmt.Sprintf("step%d", i),
			call.WithArgs(call.NewArgument("ref", call.NewLiteralID(base), false)),
		)
	}

	ref, err := resultCallRefFromIDInput(context.Background(), id, recipeCallMemo{})
	require.NoError(t, err)
	require.NotNil(t, ref.Call)

	// Walk the receiver chain, collecting each level's base ref and every distinct
	// *ResultCall frame reachable.
	distinct := map[*ResultCall]struct{}{}
	var walk func(f *ResultCall)
	walk = func(f *ResultCall) {
		if f == nil {
			return
		}
		if _, seen := distinct[f]; seen {
			return
		}
		distinct[f] = struct{}{}
		if f.Receiver != nil {
			walk(f.Receiver.Call)
		}
		for _, arg := range f.Args {
			if arg.Value != nil && arg.Value.ResultRef != nil {
				walk(arg.Value.ResultRef.Call)
			}
		}
	}
	walk(ref.Call)

	levels := 0
	var firstBase *ResultCall
	for f := ref.Call; f != nil; f = receiverFrame(f) {
		levels++
		got := refArg(t, f, "ref")
		if firstBase == nil {
			firstBase = got
		}
		require.Same(t, firstBase, got, "base must be shared at every chain level")
	}
	require.Equal(t, depth, levels)

	// Linear, not exponential: depth chain frames + exactly one shared base.
	require.Equal(t, depth+1, len(distinct),
		"expected depth step frames + 1 shared base; a tree unroll would be far larger")
}

func receiverFrame(f *ResultCall) *ResultCall {
	if f == nil || f.Receiver == nil {
		return nil
	}
	return f.Receiver.Call
}
