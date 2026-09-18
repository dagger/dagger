package dagql

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type identityOptTestQuery struct{}

func (identityOptTestQuery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

func newIdentityOptTestServer(t *testing.T) *Server {
	t.Helper()
	return newDagqlServerForTest(t, identityOptTestQuery{})
}

func TestFieldSpecResolveImplicitInputCallArgsResolvesDeterministicImplicitInputs(t *testing.T) {
	spec := &FieldSpec{
		ImplicitInputs: []ImplicitInput{
			{
				Name: "b",
				Resolver: func(context.Context, map[string]Input) (Input, error) {
					return NewString("first"), nil
				},
			},
			{
				Name: "a",
				Resolver: func(context.Context, map[string]Input) (Input, error) {
					return NewString("alpha"), nil
				},
			},
			{
				Name: "b",
				Resolver: func(context.Context, map[string]Input) (Input, error) {
					return NewString("second"), nil
				},
			},
		},
	}

	implicitArgs, err := spec.resolveImplicitInputCallArgs(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, implicitArgs, 2)
	require.Equal(t, "a", implicitArgs[0].Name)
	require.Equal(t, "b", implicitArgs[1].Name)

	require.Equal(t, ResultCallLiteralKindString, implicitArgs[0].Value.Kind)
	require.Equal(t, "alpha", implicitArgs[0].Value.StringValue)

	require.Equal(t, ResultCallLiteralKindString, implicitArgs[1].Value.Kind)
	require.Equal(t, "second", implicitArgs[1].Value.StringValue)
}

func TestFieldSpecResolveImplicitInputCallArgsPropagatesResolverErrors(t *testing.T) {
	spec := &FieldSpec{
		ImplicitInputs: []ImplicitInput{
			{
				Name: "broken",
				Resolver: func(context.Context, map[string]Input) (Input, error) {
					return nil, errors.New("boom")
				},
			},
		},
	}

	_, err := spec.resolveImplicitInputCallArgs(context.Background(), nil)
	require.ErrorContains(t, err, `resolve implicit input "broken"`)
	require.ErrorContains(t, err, "boom")
}

func TestFieldSpecResolveImplicitInputCallArgsRejectsNilResolvedInput(t *testing.T) {
	spec := &FieldSpec{
		ImplicitInputs: []ImplicitInput{
			{
				Name: "broken",
				Resolver: func(context.Context, map[string]Input) (Input, error) {
					return nil, nil
				},
			},
		},
	}

	_, err := spec.resolveImplicitInputCallArgs(context.Background(), nil)
	require.ErrorContains(t, err, `implicit input "broken" resolved to nil`)
}

func TestObjectPreselectAppliesFieldModule(t *testing.T) {
	srv := newIdentityOptTestServer(t)
	module := &ResultCallModule{
		Name: "mod",
		Ref:  "ref",
		Pin:  "pin",
	}

	Fields[identityOptTestQuery]{
		{
			Spec: &FieldSpec{
				Name:   "field",
				Type:   String(""),
				Module: module,
			},
			Func: func(context.Context, ObjectResult[identityOptTestQuery], map[string]Input, call.View) (AnyResult, error) {
				return NewResultForCall(NewString("value"), &ResultCall{
					Kind:  ResultCallKindField,
					Type:  NewResultCallType(NewString("").Type()),
					Field: "field",
				})
			},
		},
	}.Install(srv)

	root, ok := srv.Root().(ObjectResult[identityOptTestQuery])
	require.True(t, ok)

	_, preselected, err := root.preselect(engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "dagql-test-client",
		SessionID: "dagql-test-session",
	}), srv, Selector{Field: "field"})
	require.NoError(t, err)
	require.NotNil(t, preselected.request.ResultCall.Module)
	require.Equal(t, module.Name, preselected.request.ResultCall.Module.Name)
	require.Equal(t, module.Ref, preselected.request.ResultCall.Module.Ref)
	require.Equal(t, module.Pin, preselected.request.ResultCall.Module.Pin)
}
