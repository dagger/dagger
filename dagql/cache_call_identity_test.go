package dagql

import (
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// TestCallIdentityFromPBMatchesTheFrame checks that a call's identity derived
// from its span encoding is exactly the identity the cache derives from the
// call's frame: the recipe digest, the self digest and the input digests.
func TestCallIdentityFromPBMatchesTheFrame(t *testing.T) {
	t.Parallel()

	receiver := cacheTestIntCall("receiver")
	module := cacheTestIntCall("_implementationScoped", call.ExtraDigest{Digest: digest.FromString("module-content"), Label: call.ExtraDigestLabelContent})
	argRef := cacheTestIntCall("argument")
	listRef := cacheTestIntCall("listed")
	objectRef := cacheTestIntCall("objectField")

	full := cacheTestIntCall("container", call.ExtraDigest{Digest: digest.FromString("request-extra"), Label: "aux"})
	full.Type = &ResultCallType{NamedType: "Container", NonNull: true, Elem: nil}
	full.Receiver = &ResultCallRef{Call: receiver}
	full.Module = &ResultCallModule{ResultRef: &ResultCallRef{Call: module}, Name: "wolfi", Ref: "github.com/dagger/wolfi", Pin: "0b73dcbe"}
	full.Nth = 2
	full.View = call.View("v1.0.0")
	full.Args = []*ResultCallArg{
		{Name: "null", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindNull}},
		{Name: "missing"},
		{Name: "bool", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindBool, BoolValue: true}},
		{Name: "enum", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindEnum, EnumValue: "AMD64"}},
		{Name: "int", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: -7}},
		{Name: "float", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindFloat, FloatValue: 1.5}},
		{Name: "string", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "alpine"}},
		{Name: "bytes", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindBytes, BytesValue: []byte{0, 1, 0xff}}},
		{Name: "secret", IsSensitive: true, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "hunter2"}},
		{Name: "digested", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindDigestedString, DigestedStringValue: `{"clientID":"a"}`, DigestedStringDigest: digest.FromString("digested")}},
		{Name: "ref", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: argRef}}},
		{Name: "list", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindList, ListItems: []*ResultCallLiteral{
			{Kind: ResultCallLiteralKindString, StringValue: "git"},
			{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: listRef}},
		}}},
		{Name: "object", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindObject, ObjectFields: []*ResultCallArg{
			{Name: "path", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "/src"}},
			{Name: "source", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: objectRef}}},
		}}},
	}
	full.ImplicitInputs = []*ResultCallArg{
		{Name: "cachePerClient", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "client-a"}},
	}

	synthetic := &ResultCall{Kind: ResultCallKindSynthetic, SyntheticOp: "blob", Type: NewResultCallType(Int(0).Type())}

	for name, frame := range map[string]*ResultCall{
		"every input kind": full,
		"no inputs":        cacheTestIntCall("constructor"),
		"receiver only":    {Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: "child", Receiver: &ResultCallRef{Call: receiver}},
		"synthetic":        synthetic,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &Cache{}
			pb, err := frame.callPB(c)
			require.NoError(t, err)
			// The call as a span carries it: encoded and decoded again.
			encoded, err := pb.Encode()
			require.NoError(t, err)
			var decoded callpbv1.Call
			require.NoError(t, decoded.Decode(encoded))

			id, err := callIdentityFromPB(&decoded)
			require.NoError(t, err)

			recipe, err := frame.deriveRecipeDigest(c)
			require.NoError(t, err)
			self, refs, err := frame.selfDigestAndInputRefs(c)
			require.NoError(t, err)
			var inputs []digest.Digest
			for _, ref := range refs {
				dig, err := ref.inputDigest(c)
				require.NoError(t, err)
				inputs = append(inputs, dig)
			}
			require.Equal(t, recipe, id.recipe)
			require.Equal(t, self, id.self)
			require.Equal(t, inputs, id.inputs)
			require.Equal(t, frame.ExtraDigests, id.extras)
		})
	}
}

// TestCallIdentityFromPBRefusesAMismatchedRecipe checks that a call whose
// encoding doesn't derive the recipe digest it names is refused.
func TestCallIdentityFromPBRefusesAMismatchedRecipe(t *testing.T) {
	t.Parallel()

	frame := cacheTestIntCall("constructor")
	pb, err := frame.callPB(&Cache{})
	require.NoError(t, err)
	pb.Digest = digest.FromString("another call").String()
	_, err = callIdentityFromPB(pb)
	require.ErrorContains(t, err, "derived recipe")
}
