package dagql

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call"
)

// recipeCallsFixture builds a recipe that exercises every edge RecipeCalls
// follows: a receiver spine, a module, ID literals nested in a list and an
// object, a sensitive ID argument, implicit inputs, a frame shared by several
// paths, and one recipe digest reached through two frames with different
// extra digests and effect IDs.
func recipeCallsFixture() *ResultCall {
	strType := NewResultCallType(memoStrType)
	objType := NewResultCallType(memoObjType)
	field := func(name string, typ *ResultCallType, receiver *ResultCall, args ...*ResultCallArg) *ResultCall {
		frame := &ResultCall{Kind: ResultCallKindField, Type: typ, Field: name, Args: args}
		if receiver != nil {
			frame.Receiver = &ResultCallRef{Call: receiver}
		}
		return frame
	}
	ref := func(frame *ResultCall) *ResultCallLiteral {
		return &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: frame}}
	}

	mod := field("moduleSource", objType, nil, &ResultCallArg{
		Name:  "ref",
		Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "./mod"},
	})
	shared := field("shared", strType, field("base", objType, nil))

	// Two distinct frames with one recipe digest: extra digests and effect IDs
	// are not part of recipe identity.
	twinA := field("twin", strType, nil)
	twinA.ExtraDigests = []call.ExtraDigest{
		{Digest: digest.FromString("content-b"), Label: call.ExtraDigestLabelContent},
		{Digest: digest.FromString("content-a"), Label: call.ExtraDigestLabelContent},
	}
	twinA.EffectIDs = []string{"effect-1"}
	twinB := field("twin", strType, nil)
	twinB.ExtraDigests = []call.ExtraDigest{{Digest: digest.FromString("pinned"), Label: "pinned"}}
	twinB.EffectIDs = []string{"effect-2", "effect-1"}

	secretSrc := field("secretSource", strType, nil)

	mid := field("mid", objType, field("start", objType, nil),
		&ResultCallArg{Name: "list", Value: &ResultCallLiteral{
			Kind:      ResultCallLiteralKindList,
			ListItems: []*ResultCallLiteral{ref(shared), ref(twinA), {Kind: ResultCallLiteralKindInt, IntValue: 3}},
		}},
		&ResultCallArg{Name: "secret", IsSensitive: true, Value: ref(secretSrc)},
	)
	mid.Module = &ResultCallModule{ResultRef: &ResultCallRef{Call: mod}, Name: "mod", Ref: "./mod", Pin: "pin"}
	mid.View = call.View("v1")
	mid.Nth = 2

	top := field("top", objType, mid,
		&ResultCallArg{Name: "obj", Value: &ResultCallLiteral{
			Kind: ResultCallLiteralKindObject,
			ObjectFields: []*ResultCallArg{
				{Name: "a", Value: ref(shared)},
				{Name: "b", Value: ref(twinB)},
				{Name: "c", Value: &ResultCallLiteral{
					Kind:                 ResultCallLiteralKindDigestedString,
					DigestedStringValue:  "hello",
					DigestedStringDigest: digest.FromString("hello"),
				}},
			},
		}},
	)
	top.ImplicitInputs = []*ResultCallArg{{Name: "cachePerSession", Value: ref(field("session", strType, nil))}}
	return top
}

func TestRecipeCallsMatchesRecipeIDProto(t *testing.T) {
	t.Parallel()
	ctx := ContextWithCache(context.Background(), nil)
	top := recipeCallsFixture()

	id, err := top.RecipeID(ctx)
	require.NoError(t, err)
	dagPB, err := id.ToProto()
	require.NoError(t, err)
	want := dagPB.GetRecipe().GetCallsByDigest()

	calls, err := top.RecipeCalls(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, calls)
	require.Equal(t, dagPB.GetRecipe().GetRootDigest(), calls[0].GetDigest(), "the root must come first")

	got := map[string]bool{}
	for _, callPB := range calls {
		require.False(t, got[callPB.GetDigest()], "digest %s returned twice", callPB.GetDigest())
		got[callPB.GetDigest()] = true
		wantPB := want[callPB.GetDigest()]
		require.NotNil(t, wantPB, "digest %s (%s) is not in the recipe DAG", callPB.GetDigest(), callPB.GetField())
		require.True(t, proto.Equal(wantPB, callPB), "call %s differs:\nwant %v\ngot  %v", callPB.GetField(), wantPB, callPB)
	}
	require.Len(t, calls, len(want), "every call of the recipe DAG must be returned")
}

func TestRecipeCallsSkipsCoveredSubtrees(t *testing.T) {
	t.Parallel()
	ctx := ContextWithCache(context.Background(), nil)

	const depth = 200
	var chain []*ResultCall
	var prev *ResultCall
	for range depth {
		frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(memoObjType), Field: "step"}
		if prev != nil {
			frame.Receiver = &ResultCallRef{Call: prev}
		}
		frame.Args = []*ResultCallArg{{
			Name:  "n",
			Value: &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: int64(len(chain))},
		}}
		chain = append(chain, frame)
		prev = frame
	}
	top := chain[depth-1]
	covered, err := chain[depth-3].RecipeDigest(ctx)
	require.NoError(t, err)

	var asked []string
	calls, err := top.RecipeCalls(ctx, func(dgst string) bool {
		asked = append(asked, dgst)
		return dgst == covered.String()
	})
	require.NoError(t, err)
	require.Len(t, calls, 2, "only the frames above the covered one are returned")
	require.Equal(t, []string{calls[1].GetDigest(), covered.String()}, asked,
		"the walk must stop at the covered frame instead of visiting the chain below it")

	rootDigest, err := top.RecipeDigest(ctx)
	require.NoError(t, err)
	calls, err = top.RecipeCalls(ctx, func(string) bool { return true })
	require.NoError(t, err)
	require.Len(t, calls, 1, "skip is never asked about the root")
	require.Equal(t, rootDigest.String(), calls[0].GetDigest())
}
