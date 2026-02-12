package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestModuleFunctionCacheImplicitInputs(t *testing.T) {
	sessionCtx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "client-1",
		SessionID: "session-1",
	})

	for _, tc := range []struct {
		name               string
		mod                *Module
		fn                 *Function
		expectSessionScope bool
	}{
		{
			name: "default policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: false,
		},
		{
			name: "explicit per-session policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name:        "fn",
				CachePolicy: FunctionCachePolicyPerSession,
			},
			expectSessionScope: true,
		},
		{
			name: "default policy with module disable-default-caching",
			mod: &Module{
				NameField:                     "test",
				DisableDefaultFunctionCaching: true,
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: true,
		},
		{
			name: "constructor always session scoped",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name: "",
			},
			expectSessionScope: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modFn := &ModuleFunction{
				mod:      tc.mod,
				metadata: tc.fn,
			}

			inputs := modFn.cacheImplicitInputs()
			byName := mapImplicitInputsByName(inputs)
			scopeInput, ok := byName[moduleFunctionScopeInputName]
			require.True(t, ok, "missing %q implicit input", moduleFunctionScopeInputName)
			require.Equal(
				t,
				hashutil.HashStrings("", tc.mod.NameField).String(),
				resolveImplicitInputString(t, scopeInput, context.Background()),
			)

			sessionInput, hasSessionInput := byName[dagql.CachePerSession.Name]
			require.Equal(t, tc.expectSessionScope, hasSessionInput)
			if tc.expectSessionScope {
				require.Equal(
					t,
					"session-1",
					resolveImplicitInputString(t, sessionInput, sessionCtx),
				)
			}
		})
	}
}

func TestModuleFunctionCacheImplicitInputsNilSafe(t *testing.T) {
	require.Nil(t, (*ModuleFunction)(nil).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{}).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{
		mod: &Module{},
	}).cacheImplicitInputs())
}

func TestModuleFunctionNormalizedReceiverDigestIgnoresSessionScopeInput(t *testing.T) {
	modFn := &ModuleFunction{
		mod: &Module{
			NameField: "test",
		},
		metadata: &Function{
			Name: "fn",
		},
	}

	receiverBase := call.New().Append(
		&ast.Type{
			NamedType: "Test",
			NonNull:   true,
		},
		"test",
	)
	receiverSessionA := receiverBase.With(call.WithImplicitInputs(
		call.NewArgument(dagql.CachePerSession.Name, call.NewLiteralString("session-a"), false),
	))
	receiverSessionB := receiverBase.With(call.WithImplicitInputs(
		call.NewArgument(dagql.CachePerSession.Name, call.NewLiteralString("session-b"), false),
	))

	digestA, err := modFn.normalizedReceiverDigest(receiverSessionA)
	require.NoError(t, err)
	digestB, err := modFn.normalizedReceiverDigest(receiverSessionB)
	require.NoError(t, err)
	require.Equal(t, digestA, digestB)
}

func mapImplicitInputsByName(inputs []dagql.ImplicitInput) map[string]dagql.ImplicitInput {
	byName := make(map[string]dagql.ImplicitInput, len(inputs))
	for _, input := range inputs {
		byName[input.Name] = input
	}
	return byName
}

func resolveImplicitInputString(t *testing.T, input dagql.ImplicitInput, ctx context.Context) string {
	t.Helper()

	value, err := input.Resolver(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, value)

	strVal, ok := value.(dagql.String)
	require.True(t, ok, "expected dagql.String implicit input value, got %T", value)
	return strVal.String()
}
