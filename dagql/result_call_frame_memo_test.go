package dagql

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func collectFrames(f *ResultCall, seen map[*ResultCall]struct{}) {
	if f == nil {
		return
	}
	if _, ok := seen[f]; ok {
		return
	}
	seen[f] = struct{}{}
	if f.Receiver != nil {
		collectFrames(f.Receiver.Call, seen)
	}
	for _, a := range f.Args {
		if a.Value != nil && a.Value.ResultRef != nil {
			collectFrames(a.Value.ResultRef.Call, seen)
		}
	}
}

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

// clone() must be DAG-preserving: a frame referenced via multiple paths must
// clone to a single shared node, not be re-expanded into a tree. Before this,
// cloning a large shared frame exploded to ~3.3e8 objects / ~28GB in a runaway.

func TestCloneDedupesSharedFrame(t *testing.T) {
	t.Parallel()

	base := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "base"}
	top := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(memoObjType),
		Field: "combine",
		Args:  []*ResultCallArg{inlineRefArg("a", base), inlineRefArg("b", base)},
	}

	cp := top.clone()
	ca := cp.Args[0].Value.ResultRef.Call
	cb := cp.Args[1].Value.ResultRef.Call
	require.Same(t, ca, cb, "shared frame must clone to a single shared node")
	require.NotSame(t, base, ca, "clone must be independent of the original")
	require.Equal(t, "base", ca.Field)
}

func TestCloneKeepsDistinctFramesDistinct(t *testing.T) {
	t.Parallel()

	baseA := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "baseA"}
	baseB := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "baseB"}
	top := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType(memoObjType),
		Field: "combine",
		Args:  []*ResultCallArg{inlineRefArg("a", baseA), inlineRefArg("b", baseB)},
	}

	cp := top.clone()
	require.NotSame(t, cp.Args[0].Value.ResultRef.Call, cp.Args[1].Value.ResultRef.Call)
}

func TestCloneSharedDAGIsLinearAndIndependent(t *testing.T) {
	t.Parallel()

	const depth = 300
	base := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoStrType), Field: "base"}
	var frame *ResultCall
	for i := range depth {
		f := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType(memoObjType),
			Field: fmt.Sprintf("step%d", i),
			Args:  []*ResultCallArg{inlineRefArg("ref", base)},
		}
		if frame != nil {
			f.Receiver = &ResultCallRef{Call: frame}
		}
		frame = f
	}

	orig := map[*ResultCall]struct{}{}
	collectFrames(frame, orig)
	require.Equal(t, depth+1, len(orig), "source DAG should be depth frames + 1 shared base")

	cp := frame.clone()
	cloned := map[*ResultCall]struct{}{}
	collectFrames(cp, cloned)

	// Linear, not exponential: a tree unroll would be vastly larger.
	require.Equal(t, depth+1, len(cloned), "clone must stay a DAG, not unroll to a tree")
	// Independent: no clone node may alias an original node.
	for c := range cloned {
		_, isOrig := orig[c]
		require.False(t, isOrig, "clone must not alias original frames")
	}
	// Base stays shared at every level within the clone.
	var firstBase *ResultCall
	for f := cp; f != nil; f = receiverFrame(f) {
		b := f.Args[0].Value.ResultRef.Call
		if firstBase == nil {
			firstBase = b
		}
		require.Same(t, firstBase, b, "base must be shared at every level of the clone")
	}
}
