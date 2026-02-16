package dagql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func TestFieldSpecIdentityOptResolvesDeterministicImplicitInputs(t *testing.T) {
	spec := &dagql.FieldSpec{
		ImplicitInputs: []dagql.ImplicitInput{
			{
				Name: "b",
				Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
					return dagql.NewString("first"), nil
				},
			},
			{
				Name: "a",
				Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
					return dagql.NewString("alpha"), nil
				},
			},
			{
				Name: "b",
				Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
					return dagql.NewString("second"), nil
				},
			},
		},
	}

	identityOpt, err := spec.IdentityOpt(context.Background(), nil)
	require.NoError(t, err)

	id := call.New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "field").With(identityOpt)

	implicitInputs := id.ImplicitInputs()
	require.Len(t, implicitInputs, 2)
	require.Equal(t, "a", implicitInputs[0].Name())
	require.Equal(t, "b", implicitInputs[1].Name())

	litA, ok := implicitInputs[0].Value().(*call.LiteralString)
	require.True(t, ok)
	require.Equal(t, "alpha", litA.Value())

	litB, ok := implicitInputs[1].Value().(*call.LiteralString)
	require.True(t, ok)
	require.Equal(t, "second", litB.Value())
}

func TestFieldSpecIdentityOptAppliesModule(t *testing.T) {
	moduleID := call.New().Append(&ast.Type{
		NamedType: "Module",
		NonNull:   true,
	}, "module")
	module := call.NewModule(moduleID, "mod", "ref", "pin")

	spec := &dagql.FieldSpec{
		Module: module,
	}
	identityOpt, err := spec.IdentityOpt(context.Background(), nil)
	require.NoError(t, err)

	id := call.New().Append(&ast.Type{
		NamedType: "String",
		NonNull:   true,
	}, "field").With(identityOpt)

	require.NotNil(t, id.Module())
	require.NotNil(t, id.Module().ID())
	require.Equal(t, moduleID.Digest(), id.Module().ID().Digest())
}

func TestFieldSpecIdentityOptPropagatesResolverErrors(t *testing.T) {
	spec := &dagql.FieldSpec{
		ImplicitInputs: []dagql.ImplicitInput{
			{
				Name: "broken",
				Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
					return nil, errors.New("boom")
				},
			},
		},
	}

	_, err := spec.IdentityOpt(context.Background(), nil)
	require.ErrorContains(t, err, `resolve implicit input "broken"`)
	require.ErrorContains(t, err, "boom")
}

func TestFieldSpecIdentityOptRejectsNilResolvedInput(t *testing.T) {
	spec := &dagql.FieldSpec{
		ImplicitInputs: []dagql.ImplicitInput{
			{
				Name: "broken",
				Resolver: func(context.Context, map[string]dagql.Input) (dagql.Input, error) {
					return nil, nil
				},
			},
		},
	}

	_, err := spec.IdentityOpt(context.Background(), nil)
	require.ErrorContains(t, err, `implicit input "broken" resolved to nil`)
}
