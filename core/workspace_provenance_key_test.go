package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceCallKeyDirectInputsOnly(t *testing.T) {
	cache, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx := dagql.ContextWithCache(t.Context(), cache)
	stringArg := func(name, value string) *dagql.ResultCallArg {
		return &dagql.ResultCallArg{Name: name, Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: value}}
	}
	ws := func(snapshot string) *dagql.ResultCall {
		return &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "snapshot", Type: dagql.NewResultCallType((*Workspace)(nil).Type()), Args: []*dagql.ResultCallArg{stringArg("snapshot", snapshot)}}
	}
	ref := func(frame *dagql.ResultCall) *dagql.ResultCallLiteral {
		return &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: &dagql.ResultCallRef{Call: frame}}
	}
	a, b := ws("a"), ws("b")
	typedReceiver := &ModuleObject{TypeDef: &ObjectTypeDef{Name: "Checks"}}
	receiver := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "checks", Type: dagql.NewResultCallType(typedReceiver.Type()), Args: []*dagql.ResultCallArg{stringArg("setting", "one")}}
	module := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "module", Type: dagql.NewResultCallType((*Module)(nil).Type()), ExtraDigests: []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("implementation-a")}}}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "__check_test", Type: dagql.NewResultCallType(dagql.String("").Type()), Receiver: &dagql.ResultCallRef{Call: receiver}, Module: &dagql.ResultCallModule{ResultRef: &dagql.ResultCallRef{Call: module}}, Args: []*dagql.ResultCallArg{{Name: "workspace", Value: ref(a)}, stringArg("name", "target")}}
	req := &dagql.CallRequest{ResultCall: frame}
	key := func(frame *dagql.ResultCall) string {
		got, err := workspaceCallKey(ctx, &dagql.CallRequest{ResultCall: frame})
		require.NoError(t, err)
		require.NotEmpty(t, got)
		return got
	}
	base := key(frame)
	changed := frame.Clone()
	changed.Args[0].Value = ref(b)
	require.Equal(t, base, key(changed), "direct Workspace state is excluded")
	require.Same(t, a, frame.Args[0].Value.ResultRef.Call, "normalization does not mutate the original request")
	changed = frame.Clone()
	changed.Args[1] = stringArg("name", "other")
	require.NotEqual(t, base, key(changed), "ordinary arguments remain inputs")
	changed = frame.Clone()
	changed.Receiver.Call.Args[0] = stringArg("setting", "two")
	require.NotEqual(t, base, key(changed), "receiver state remains an input")
	changed = frame.Clone()
	changed.Module.ResultRef.Call.ExtraDigests[0].Digest = digest.FromString("implementation-b")
	require.NotEqual(t, base, key(changed), "module implementation remains an input")
	changed = frame.Clone()
	changed.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("output")}}
	require.Equal(t, base, key(changed), "the call's own output is excluded")
	receiver.Args = append(receiver.Args, &dagql.ResultCallArg{Name: "historical", Value: ref(a)})
	historical := key(frame)
	changed = frame.Clone()
	changed.Receiver.Call.Args[1].Value = ref(b)
	require.NotEqual(t, historical, key(changed), "historical receiver Workspace inputs are not stripped")
	directory := &dagql.ResultCall{Kind: dagql.ResultCallKindSynthetic, SyntheticOp: "directory", Type: dagql.NewResultCallType((*Directory)(nil).Type()), Receiver: &dagql.ResultCallRef{Call: a}}
	withDirectory := frame.Clone()
	withDirectory.Args = append(withDirectory.Args, &dagql.ResultCallArg{Name: "source", Value: ref(directory)})
	captured := key(withDirectory)
	changed = withDirectory.Clone()
	changed.Args[2].Value.ResultRef.Call.Receiver.Call = b
	require.NotEqual(t, captured, key(changed), "ordinary Directory input history is not stripped")
	nested := frame.Clone()
	nested.Args[0].Value = &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindList, ListItems: []*dagql.ResultCallLiteral{{Kind: dagql.ResultCallLiteralKindObject, ObjectFields: []*dagql.ResultCallArg{{Name: "nested", Value: ref(a)}}}}}
	changed = nested.Clone()
	changed.Args[0].Value.ListItems[0].ObjectFields[0].Value = ref(b)
	require.Equal(t, key(nested), key(changed), "nested literal Workspace arguments are direct inputs too")
	nested.ImplicitInputs = []*dagql.ResultCallArg{{Name: "workspace", Value: ref(a)}}
	refs, err := workspaceRefs(ctx, nested)
	require.NoError(t, err)
	require.Len(t, refs, 2, "receiver history is not reported as a direct Workspace argument")
	for _, policy := range []dagql.ImplicitInput{dagql.PerCallInput, dagql.PerSessionInput} {
		blocked := frame.Clone()
		blocked.ImplicitInputs = []*dagql.ResultCallArg{stringArg(policy.Name, "policy")}
		got, err := workspaceCallKey(ctx, &dagql.CallRequest{ResultCall: blocked})
		require.NoError(t, err)
		require.Empty(t, got)
	}
	req.DoNotCache = true
	got, err := workspaceCallKey(ctx, req)
	require.NoError(t, err)
	require.Empty(t, got)
}
